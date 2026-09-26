package gateway

import (
	"context"
	"encoding/json"
	"io"
	"sync"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/protocol"
)

// streamBuffer bounds queued terminal output per stream. A consumer that
// falls this far behind is disconnected rather than stalling the agent's
// shared connection.
const streamBuffer = 1024

// Stream is a terminal multiplexed over the agent connection.
type Stream struct {
	session *Session
	id      string
	in      chan []byte
	done    chan struct{}
	once    sync.Once
	pending []byte
}

// OpenStream asks the agent to start a shell in the instance and returns it
// as a read/write stream.
func (s *Session) OpenStream(ctx context.Context, name string, cols, rows int) (*Stream, error) {
	id, err := requestID()
	if err != nil {
		return nil, err
	}
	stream := &Stream{session: s, id: id, in: make(chan []byte, streamBuffer), done: make(chan struct{})}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, ErrOffline
	}
	s.streams[id] = stream
	s.mu.Unlock()
	if err := s.Call(ctx, protocol.MethodConsoleOpen, protocol.ConsoleOpenParams{Name: name, Stream: id, Cols: cols, Rows: rows}, nil); err != nil {
		s.dropStream(id)
		return nil, err
	}
	return stream, nil
}

// deliver routes an incoming stream frame; it never blocks the read loop.
func (s *Session) deliver(frame protocol.Frame) {
	s.mu.Lock()
	stream := s.streams[frame.ID]
	s.mu.Unlock()
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
		case stream.in <- data:
		default:
			stream.shutdown()
			s.dropStream(stream.id)
		}
	case protocol.StreamClose:
		stream.shutdown()
		s.dropStream(stream.id)
	}
}

func (s *Session) dropStream(id string) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

func (s *Stream) shutdown() { s.once.Do(func() { close(s.done) }) }

func (s *Stream) Read(p []byte) (int, error) {
	for len(s.pending) == 0 {
		select {
		case data := <-s.in:
			s.pending = data
		case <-s.done:
			// Drain output that arrived before the close.
			select {
			case data := <-s.in:
				s.pending = data
			default:
				return 0, io.EOF
			}
		case <-s.session.done:
			return 0, io.EOF
		}
	}
	n := copy(p, s.pending)
	s.pending = s.pending[n:]
	return n, nil
}

func (s *Stream) send(method string, params any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	frame, _ := json.Marshal(protocol.Frame{Type: protocol.TypeStream, ID: s.id, Method: method, Params: encoded})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.session.conn.Write(ctx, websocket.MessageText, frame)
}

func (s *Stream) Write(p []byte) (int, error) {
	select {
	case <-s.done:
		return 0, io.ErrClosedPipe
	default:
	}
	if err := s.send(protocol.StreamData, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *Stream) Resize(cols, rows int) error {
	return s.send(protocol.StreamResize, protocol.TerminalSize{Cols: cols, Rows: rows})
}

func (s *Stream) Close() error {
	s.shutdown()
	s.session.dropStream(s.id)
	return s.send(protocol.StreamClose, struct{}{})
}
