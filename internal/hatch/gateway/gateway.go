// Package gateway accepts Hatch agent connections and lets the billing core
// call them. Sessions are keyed by the agent endpoint derived from the
// token, which is also the node's stored base URL.
package gateway

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/protocol"
	"vpsbill/internal/provider"
)

const (
	maxFrameBytes = 4 << 20
	helloTimeout  = 10 * time.Second
	pingInterval  = 30 * time.Second
	// Agents whose token belongs to no node yet may stay connected briefly so
	// an administrator can add the node; they receive no requests meanwhile.
	maxUnknownSessions = 8
	unknownSessionTTL  = 15 * time.Minute
	// reconnectGrace is how long after a server start, or after an agent
	// dropped, the agent is expected back by itself: it reconnects within
	// seconds after a server upgrade or its own self-upgrade restart.
	reconnectGrace = 2 * time.Minute
)

// ErrOffline means no agent is connected for the node.
var ErrOffline = errors.New("agent is not connected")

// KnownFunc reports whether a node with this agent endpoint exists.
type KnownFunc func(ctx context.Context, endpoint string) bool

// EnrollFunc records an agent that no node uses yet under the account its
// hello's enroll key belongs to, and reports whether the key was valid.
// Enrolled agents stay connected until the account adds or dismisses them.
type EnrollFunc func(ctx context.Context, endpoint, token string, hello protocol.Hello, r *http.Request) bool

type Hub struct {
	logger  *slog.Logger
	known   KnownFunc
	enroll  EnrollFunc
	cluster *Cluster

	mu       sync.Mutex
	sessions map[string]*Session
	unknown  int
	// gone is when each endpoint's last session ended.
	gone      map[string]time.Time
	started   time.Time
	onConnect func(endpoint string)

	// release is the agent build this server bundles; connecting agents
	// on another version are asked to upgrade to it.
	release string
}

func NewHub(logger *slog.Logger, known KnownFunc) *Hub {
	return &Hub{logger: logger, known: known, sessions: map[string]*Session{}, gone: map[string]time.Time{}, started: time.Now()}
}

type Session struct {
	endpoint string
	conn     *websocket.Conn
	hello    protocol.Hello
	known    bool
	enrolled bool

	mu       sync.Mutex
	pending  map[string]chan protocol.Frame
	streams  map[string]*Stream
	lastSeen time.Time
	done     chan struct{}
	closed   bool
}

// SetAgentRelease names the agent build to upgrade connecting agents to;
// "" or "dev" (a local build) upgrades nothing.
func (h *Hub) SetAgentRelease(version string) { h.release = version }

// SetOnConnect is called with the endpoint each time a registered agent
// connects, so its node's state can be refreshed right away. Set it before
// serving.
func (h *Hub) SetOnConnect(fn func(endpoint string)) { h.onConnect = fn }

// Reconnecting reports that the endpoint's agent is not connected but is
// expected back shortly: the server started or the agent dropped less than
// reconnectGrace ago. A failed call then says little about the node.
func (h *Hub) Reconnecting(endpoint string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.sessions[endpoint]; ok {
		return false
	}
	if time.Since(h.started) < reconnectGrace {
		return true
	}
	gone, ok := h.gone[endpoint]
	return ok && time.Since(gone) < reconnectGrace
}

// SetEnroller accepts agents carrying an enroll key.
func (h *Hub) SetEnroller(enroll EnrollFunc) { h.enroll = enroll }

// Session returns the connected agent for an endpoint.
func (h *Hub) Session(endpoint string) (*Session, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	session, ok := h.sessions[endpoint]
	return session, ok
}

// ServeHTTP authenticates the bearer token, upgrades the connection and
// serves the session until it ends.
func (h *Hub) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || len(token) < 32 || len(token) > 256 {
		http.Error(w, "agent token required", http.StatusUnauthorized)
		return
	}
	endpoint := provider.AgentEndpoint(token)
	known := h.known(r.Context(), endpoint)
	// Agents with an enroll key say so up front and skip the few slots for
	// unregistered agents; one whose key turns out invalid is closed right
	// after its hello.
	claimsEnroll := r.Header.Get(protocol.EnrollHeader) != ""
	if !known && !claimsEnroll && !h.reserveUnknown() {
		http.Error(w, "too many unregistered agents", http.StatusServiceUnavailable)
		return
	}
	reserved := !known && !claimsEnroll
	defer func() {
		if reserved {
			h.releaseUnknown()
		}
	}()
	// The API server's read/write timeouts would otherwise cut this
	// long-lived connection; liveness is enforced by pings instead.
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})

	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	conn.SetReadLimit(maxFrameBytes)
	defer conn.CloseNow()

	hello, err := readHello(r.Context(), conn)
	if err != nil {
		_ = conn.Close(websocket.StatusPolicyViolation, err.Error())
		return
	}
	enrolled := !known && hello.EnrollKey != "" && h.enroll != nil && h.enroll(r.Context(), endpoint, token, hello, r)
	if !known && claimsEnroll && !enrolled {
		_ = conn.Close(websocket.StatusPolicyViolation, "enroll key is not valid")
		return
	}
	// Enrolled agents may wait for their account indefinitely, so they do
	// not hold one of the few slots for unregistered agents.
	if enrolled && reserved {
		h.releaseUnknown()
		reserved = false
	}
	session := &Session{endpoint: endpoint, conn: conn, hello: hello, known: known, enrolled: enrolled, pending: map[string]chan protocol.Frame{}, streams: map[string]*Stream{}, lastSeen: time.Now(), done: make(chan struct{})}
	h.attach(session)
	defer h.detach(session)
	h.register(r.Context(), endpoint)
	h.logger.Info("hatch agent connected", "endpoint", endpoint, "hostname", hello.Hostname, "version", hello.AgentVersion, "registered", known, "enrolled", enrolled)
	if known && h.onConnect != nil {
		go h.onConnect(endpoint)
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go h.keepalive(ctx, session)
	if h.release != "" && h.release != "dev" && hello.AgentVersion != h.release && hello.AgentVersion != "dev" && (known || enrolled) {
		go h.upgrade(ctx, session)
	}
	session.readLoop(ctx)
	h.logger.Info("hatch agent disconnected", "endpoint", endpoint)
}

// upgrade asks an agent to move to the bundled build. Agents older than
// self-upgrade answer "unsupported" and need the install command once.
func (h *Hub) upgrade(ctx context.Context, session *Session) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	err := session.Call(ctx, protocol.MethodUpgrade, protocol.UpgradeParams{Version: h.release}, nil)
	var protocolErr *protocol.Error
	switch {
	case err == nil:
		h.logger.Info("hatch agent upgrading", "endpoint", session.endpoint, "from", session.hello.AgentVersion, "to", h.release)
	case errors.As(err, &protocolErr) && protocolErr.Code == protocol.CodeUnsupported:
		h.logger.Info("hatch agent does not upgrade itself; rerun the install command on the host", "endpoint", session.endpoint, "version", session.hello.AgentVersion, "reason", protocolErr.Message)
	default:
		h.logger.Warn("hatch agent upgrade failed", "endpoint", session.endpoint, "version", session.hello.AgentVersion, "error", err)
	}
}

func readHello(ctx context.Context, conn *websocket.Conn) (protocol.Hello, error) {
	ctx, cancel := context.WithTimeout(ctx, helloTimeout)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		return protocol.Hello{}, fmt.Errorf("read hello: %w", err)
	}
	var frame protocol.Frame
	var hello protocol.Hello
	if json.Unmarshal(data, &frame) != nil || frame.Type != protocol.TypeHello || json.Unmarshal(frame.Params, &hello) != nil {
		return protocol.Hello{}, errors.New("first frame must be hello")
	}
	if hello.ProtocolVersion != protocol.Version {
		return protocol.Hello{}, fmt.Errorf("unsupported protocol version %d", hello.ProtocolVersion)
	}
	return hello, nil
}

func (h *Hub) reserveUnknown() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.unknown >= maxUnknownSessions {
		return false
	}
	h.unknown++
	return true
}

func (h *Hub) releaseUnknown() {
	h.mu.Lock()
	h.unknown--
	h.mu.Unlock()
}

// attach registers the session, replacing an older connection that used the
// same token (for example after an agent restart the old TCP stream may
// linger until its next ping fails).
func (h *Hub) attach(session *Session) {
	h.mu.Lock()
	previous := h.sessions[session.endpoint]
	h.sessions[session.endpoint] = session
	h.mu.Unlock()
	if previous != nil {
		_ = previous.conn.Close(websocket.StatusPolicyViolation, "replaced by a newer connection")
	}
}

func (h *Hub) detach(session *Session) {
	h.mu.Lock()
	if h.sessions[session.endpoint] == session {
		delete(h.sessions, session.endpoint)
		h.gone[session.endpoint] = time.Now()
		go h.unregister(session.endpoint)
	}
	h.mu.Unlock()
	session.close()
}

func (h *Hub) keepalive(ctx context.Context, session *Session) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()
	started := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		pingCtx, cancel := context.WithTimeout(ctx, pingInterval)
		err := session.conn.Ping(pingCtx)
		cancel()
		if err != nil {
			_ = session.conn.Close(websocket.StatusGoingAway, "ping timeout")
			return
		}
		h.register(ctx, session.endpoint)
		if !session.known && !session.enrolled && time.Since(started) > unknownSessionTTL {
			if !h.known(ctx, session.endpoint) {
				_ = session.conn.Close(websocket.StatusPolicyViolation, "token is not registered to any node")
				return
			}
			session.known = true
		}
	}
}

func (s *Session) readLoop(ctx context.Context) {
	for {
		_, data, err := s.conn.Read(ctx)
		if err != nil {
			return
		}
		var frame protocol.Frame
		if err := json.Unmarshal(data, &frame); err != nil {
			continue
		}
		s.mu.Lock()
		s.lastSeen = time.Now()
		reply := s.pending[frame.ID]
		delete(s.pending, frame.ID)
		s.mu.Unlock()
		switch {
		case frame.Type == protocol.TypeResponse && reply != nil:
			reply <- frame
		case frame.Type == protocol.TypeStream:
			s.deliver(frame)
		}
	}
}

func (s *Session) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		close(s.done)
	}
}

func (s *Session) Hello() protocol.Hello { return s.hello }

// Call sends one request and waits for its response. A response error is
// returned as *protocol.Error.
func (s *Session) Call(ctx context.Context, method string, params, result any) error {
	encoded, err := json.Marshal(params)
	if err != nil {
		return fmt.Errorf("encode %s params: %w", method, err)
	}
	id, err := requestID()
	if err != nil {
		return err
	}
	frame, _ := json.Marshal(protocol.Frame{Type: protocol.TypeRequest, ID: id, Method: method, Params: encoded})
	reply := make(chan protocol.Frame, 1)
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return ErrOffline
	}
	s.pending[id] = reply
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
	}()
	if err := s.conn.Write(ctx, websocket.MessageText, frame); err != nil {
		return fmt.Errorf("send %s: %w", method, err)
	}
	select {
	case response := <-reply:
		if response.Error != nil {
			return response.Error
		}
		if result != nil && len(response.Result) > 0 {
			if err := json.Unmarshal(response.Result, result); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	case <-s.done:
		return ErrOffline
	case <-ctx.Done():
		return fmt.Errorf("%s: %w", method, ctx.Err())
	}
}

func requestID() (string, error) {
	buffer := make([]byte, 12)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}
