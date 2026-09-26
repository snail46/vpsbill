package agent

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/protocol"
)

// agentStream is one terminal opened by the billing server. Input is queued
// so a slow shell never blocks the connection's read loop.
type agentStream struct {
	terminal TerminalSession
	input    chan []byte
	once     sync.Once
}

func (s *agentStream) close() {
	s.once.Do(func() {
		close(s.input)
		s.terminal.Close()
	})
}

type streamTable struct {
	mu    sync.Mutex
	items map[string]*agentStream
}

func newStreamTable() *streamTable { return &streamTable{items: map[string]*agentStream{}} }

func (t *streamTable) get(id string) *agentStream {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.items[id]
}

func (t *streamTable) remove(id string) {
	t.mu.Lock()
	stream := t.items[id]
	delete(t.items, id)
	t.mu.Unlock()
	if stream != nil {
		stream.close()
	}
}

func (t *streamTable) closeAll() {
	t.mu.Lock()
	items := t.items
	t.items = map[string]*agentStream{}
	t.mu.Unlock()
	for _, stream := range items {
		stream.close()
	}
}

// dispatch handles a stream frame from the server.
func (t *streamTable) dispatch(frame protocol.Frame) {
	stream := t.get(frame.ID)
	if stream == nil {
		return
	}
	switch frame.Method {
	case protocol.StreamData:
		var data []byte
		if json.Unmarshal(frame.Params, &data) != nil {
			return
		}
		select {
		case stream.input <- data:
		default:
			t.remove(frame.ID)
		}
	case protocol.StreamResize:
		var size protocol.TerminalSize
		if json.Unmarshal(frame.Params, &size) == nil && size.Cols > 0 && size.Rows > 0 {
			_ = stream.terminal.Resize(size.Cols, size.Rows)
		}
	case protocol.StreamClose:
		t.remove(frame.ID)
	}
}

// openStream starts a terminal and pumps it until either side closes.
func (c *Client) openStream(ctx, sessionCtx context.Context, conn *websocket.Conn, streams *streamTable, raw json.RawMessage) error {
	var params protocol.ConsoleOpenParams
	if err := json.Unmarshal(raw, &params); err != nil || params.Stream == "" {
		return errorf(protocol.CodeInvalid, "invalid console parameters")
	}
	terminal, err := c.service.OpenTerminal(ctx, params.Name, params.Cols, params.Rows)
	if err != nil {
		return err
	}
	stream := &agentStream{terminal: terminal, input: make(chan []byte, 256)}
	streams.mu.Lock()
	streams.items[params.Stream] = stream
	streams.mu.Unlock()

	go func() {
		for data := range stream.input {
			if _, err := terminal.Write(data); err != nil {
				streams.remove(params.Stream)
				return
			}
		}
	}()
	go func() {
		buffer := make([]byte, 16<<10)
		for {
			n, err := terminal.Read(buffer)
			if n > 0 {
				frame := protocol.Frame{Type: protocol.TypeStream, ID: params.Stream, Method: protocol.StreamData, Params: mustJSON(buffer[:n])}
				if c.write(sessionCtx, conn, frame) != nil {
					streams.remove(params.Stream)
					return
				}
			}
			if err != nil {
				_ = c.write(sessionCtx, conn, protocol.Frame{Type: protocol.TypeStream, ID: params.Stream, Method: protocol.StreamClose, Params: mustJSON(struct{}{})})
				streams.remove(params.Stream)
				return
			}
		}
	}()
	return nil
}
