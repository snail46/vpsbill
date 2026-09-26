package app

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/provider"
	"vpsbill/internal/store/postgres"
	"vpsbill/internal/wsterm"
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
	wsterm.Pump(ctx, conn, session)
}
