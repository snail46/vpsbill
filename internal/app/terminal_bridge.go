package app

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/provider"
	"vpsbill/internal/store/postgres"
)

// ticketProtocolPrefix is the WebSocket subprotocol that carries the console
// ticket; the browser uses the same framing for every provider.
const ticketProtocolPrefix = "clicd-ticket."

// bridgeTerminal serves the customer's WebSSH WebSocket from a provider
// terminal session. Browser binary frames are keystrokes and text frames
// carry {"type":"resize","cols":N,"rows":N}; output is sent as binary.
func (p *customerPortal) bridgeTerminal(w http.ResponseWriter, r *http.Request, access postgres.CustomerServiceAccess, terminal provider.Terminal) {
	subprotocol := ""
	for _, value := range strings.Split(r.Header.Get("Sec-WebSocket-Protocol"), ",") {
		if value = strings.TrimSpace(value); strings.HasPrefix(value, ticketProtocolPrefix) {
			subprotocol = value
		}
	}
	if subprotocol == "" || p.tickets.verify(strings.TrimPrefix(subprotocol, ticketProtocolPrefix), access.ServiceID, "ssh") != nil {
		http.Error(w, "invalid console ticket", http.StatusUnauthorized)
		return
	}
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	// Accept rejects cross-origin upgrades, so another site cannot open a
	// console with the customer's cookies.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{subprotocol}})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	openCtx, openCancel := context.WithTimeout(ctx, 30*time.Second)
	session, err := terminal.OpenTerminal(openCtx, access.InstanceName, 80, 24)
	openCancel()
	if err != nil {
		_ = conn.Write(ctx, websocket.MessageText, []byte("\r\n控制台连接失败："+err.Error()+"\r\n"))
		_ = conn.Close(websocket.StatusInternalError, "terminal unavailable")
		return
	}
	defer session.Close()
	identity := customerPrincipalFromContext(r.Context())
	_ = p.store.RecordServiceOperation(r.Context(), identity.UserID, access.ServiceID, "service.console.open", remoteIP(r), r.UserAgent(), map[string]any{"kind": "ssh"})
	pumpTerminal(ctx, conn, session)
}

// pumpTerminal copies terminal output to the browser and browser input (or
// resize messages) to the terminal until either side closes.
func pumpTerminal(ctx context.Context, conn *websocket.Conn, session provider.TerminalSession) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		defer cancel()
		buffer := make([]byte, 32<<10)
		for {
			n, err := session.Read(buffer)
			if n > 0 {
				if writeErr := conn.Write(ctx, websocket.MessageBinary, buffer[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				_ = conn.Close(websocket.StatusNormalClosure, "session ended")
				return
			}
		}
	}()
	for {
		kind, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if kind == websocket.MessageText {
			var message struct {
				Type string `json:"type"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if json.Unmarshal(data, &message) == nil && message.Type == "resize" && message.Cols > 0 && message.Rows > 0 {
				_ = session.Resize(message.Cols, message.Rows)
			}
			continue
		}
		if _, err := session.Write(data); err != nil {
			return
		}
	}
}
