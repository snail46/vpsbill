package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	"github.com/coder/websocket"
)

// Terminal starts an interactive exec. LXD answers with an operation whose
// data ("0") and control WebSockets carry the PTY and window resizes.
func (l *LXD) Terminal(ctx context.Context, name string, cols, rows int) (TerminalSession, error) {
	data, err := l.client.do(ctx, http.MethodPost, instancePath(name)+"/exec", map[string]any{
		"command":            shellCommand,
		"environment":        map[string]string{"TERM": "xterm-256color", "HOME": "/root"},
		"interactive":        true,
		"wait-for-websocket": true,
		"width":              cols,
		"height":             rows,
	})
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, ErrInstanceNotFound
		}
		return nil, err
	}
	var response struct {
		Operation string `json:"operation"`
		Metadata  struct {
			Metadata struct {
				FDs map[string]string `json:"fds"`
			} `json:"metadata"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode LXD exec: %w", err)
	}
	fds := response.Metadata.Metadata.FDs
	if response.Operation == "" || fds["0"] == "" || fds["control"] == "" {
		return nil, errors.New("LXD exec returned no websockets")
	}
	dial := func(secret string) (*websocket.Conn, error) {
		target := "ws://local" + response.Operation + "/websocket?secret=" + url.QueryEscape(secret)
		conn, _, err := websocket.Dial(ctx, target, &websocket.DialOptions{HTTPClient: l.client.http})
		if err == nil {
			conn.SetReadLimit(1 << 20)
		}
		return conn, err
	}
	control, err := dial(fds["control"])
	if err != nil {
		return nil, fmt.Errorf("connect LXD control socket: %w", err)
	}
	stream, err := dial(fds["0"])
	if err != nil {
		control.CloseNow()
		return nil, fmt.Errorf("connect LXD terminal socket: %w", err)
	}
	sessionCtx, cancel := context.WithCancel(context.Background())
	return &lxdTerminal{data: stream, control: control, ctx: sessionCtx, cancel: cancel}, nil
}

type lxdTerminal struct {
	data    *websocket.Conn
	control *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	pending []byte
}

func (t *lxdTerminal) Read(p []byte) (int, error) {
	for len(t.pending) == 0 {
		_, data, err := t.data.Read(t.ctx)
		if err != nil {
			return 0, io.EOF
		}
		t.pending = data
	}
	n := copy(p, t.pending)
	t.pending = t.pending[n:]
	return n, nil
}

func (t *lxdTerminal) Write(p []byte) (int, error) {
	if err := t.data.Write(t.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (t *lxdTerminal) Resize(cols, rows int) error {
	message, _ := json.Marshal(map[string]any{
		"command": "window-resize",
		"args":    map[string]string{"width": strconv.Itoa(cols), "height": strconv.Itoa(rows)},
	})
	return t.control.Write(t.ctx, websocket.MessageText, message)
}

func (t *lxdTerminal) Close() error {
	t.cancel()
	t.control.CloseNow()
	return t.data.Close(websocket.StatusNormalClosure, "")
}
