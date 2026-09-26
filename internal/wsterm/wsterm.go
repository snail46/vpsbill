// Package wsterm carries an interactive terminal over a WebSocket: binary
// frames are raw terminal bytes and text frames are JSON control messages
// ({"type":"resize","cols":N,"rows":N}). The customer console and the
// internal agent forwarding between API instances both use it.
package wsterm

import (
	"context"
	"encoding/json"
	"io"

	"github.com/coder/websocket"
)

// Session is an interactive terminal.
type Session interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

type resize struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

// Pump serves session over conn until either side ends. When the terminal
// ends the socket is closed normally.
func Pump(ctx context.Context, conn *websocket.Conn, session Session) {
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
			var message resize
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

// Client is the other end of Pump: a Session backed by a WebSocket.
type Client struct {
	conn    *websocket.Conn
	ctx     context.Context
	cancel  context.CancelFunc
	pending []byte
}

func NewClient(conn *websocket.Conn) *Client {
	ctx, cancel := context.WithCancel(context.Background())
	return &Client{conn: conn, ctx: ctx, cancel: cancel}
}

func (c *Client) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		kind, data, err := c.conn.Read(c.ctx)
		if err != nil {
			return 0, io.EOF
		}
		if kind == websocket.MessageBinary {
			c.pending = data
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func (c *Client) Write(p []byte) (int, error) {
	if err := c.conn.Write(c.ctx, websocket.MessageBinary, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (c *Client) Resize(cols, rows int) error {
	message, _ := json.Marshal(resize{Type: "resize", Cols: cols, Rows: rows})
	return c.conn.Write(c.ctx, websocket.MessageText, message)
}

func (c *Client) Close() error {
	c.cancel()
	return c.conn.Close(websocket.StatusNormalClosure, "")
}
