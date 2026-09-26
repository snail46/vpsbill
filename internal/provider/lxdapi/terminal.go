package lxdapiprovider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"

	"vpsbill/internal/provider"
)

var _ provider.Terminal = (*Driver)(nil)

// OpenTerminal connects to LXDAPI's own console WebSocket (/ws/console) with
// a one-time token. LXDAPI accepts input as {"type":"input","data":...} text
// frames, sends output as text frames and has no resize message.
func (d *Driver) OpenTerminal(ctx context.Context, name string, _, _ int) (provider.TerminalSession, error) {
	var token struct {
		Token string `json:"token"`
	}
	if err := d.client.do(ctx, http.MethodPost, "/api/system/console/create-token", map[string]string{"hostname": name}, &token); err != nil {
		return nil, err
	}
	if token.Token == "" {
		return nil, errors.New("LXDAPI returned no console token")
	}
	target := "wss://" + strings.TrimPrefix(d.client.baseURL, "https://") + "/ws/console?token=" + url.QueryEscape(token.Token)
	conn, _, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: d.client.http})
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(1 << 20)
	sessionCtx, cancel := context.WithCancel(context.Background())
	return &terminal{conn: conn, ctx: sessionCtx, cancel: cancel}, nil
}

type terminal struct {
	conn    *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	pending []byte
}

func (t *terminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		_, data, err := t.conn.Read(t.ctx)
		if err != nil {
			return 0, io.EOF
		}
		t.pending = data
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *terminal) Write(p []byte) (int, error) {
	frame, err := json.Marshal(map[string]string{"type": "input", "data": string(p)})
	if err != nil {
		return 0, err
	}
	if err := t.conn.Write(t.ctx, websocket.MessageText, frame); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *terminal) Resize(int, int) error { return nil }

func (t *terminal) Close() error {
	t.cancel()
	return t.conn.Close(websocket.StatusNormalClosure, "")
}
