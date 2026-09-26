package gateway

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/protocol"
	"vpsbill/internal/wsterm"
)

// Directory records which API instance holds each agent connection.
type Directory interface {
	Register(ctx context.Context, endpoint, instanceID, internalURL string) error
	Unregister(ctx context.Context, endpoint, instanceID string) error
	Lookup(ctx context.Context, endpoint, selfID string) (internalURL string, ok bool, err error)
}

// Cluster lets several API instances share agents: each registers the
// agents connected to it, and requests for an agent held elsewhere are
// forwarded to the holder's internal endpoint, authenticated with Key.
type Cluster struct {
	InstanceID  string
	InternalURL string
	Key         []byte
	Directory   Directory
	Client      *http.Client
}

const (
	internalPrefix  = "/internal/v1/agent/"
	headerTimestamp = "X-Vpsbill-Timestamp"
	headerSignature = "X-Vpsbill-Signature"
	headerErrorCode = "X-Vpsbill-Error-Code"
	headerErrorText = "X-Vpsbill-Error-Message"
	maxClockSkew    = time.Minute
)

// ClusterKey derives the forwarding key from the shared server secrets.
func ClusterKey(secret string) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte("vpsbill agent forwarding v1"))
	return mac.Sum(nil)
}

// EnableCluster turns on registration and forwarding. Without it the hub
// only serves agents connected to this process.
func (h *Hub) EnableCluster(cluster *Cluster) {
	if cluster.Client == nil {
		cluster.Client = &http.Client{}
	}
	h.cluster = cluster
}

func (h *Hub) register(ctx context.Context, endpoint string) {
	if h.cluster == nil {
		return
	}
	if err := h.cluster.Directory.Register(ctx, endpoint, h.cluster.InstanceID, h.cluster.InternalURL); err != nil {
		h.logger.Warn("register agent session", "endpoint", endpoint, "error", err)
	}
}

func (h *Hub) unregister(endpoint string) {
	if h.cluster == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.cluster.Directory.Unregister(ctx, endpoint, h.cluster.InstanceID); err != nil {
		h.logger.Warn("unregister agent session", "endpoint", endpoint, "error", err)
	}
}

// Call sends a request to the agent for endpoint, forwarding it to the API
// instance that holds the connection when it is not local.
func (h *Hub) Call(ctx context.Context, endpoint, method string, params, result any) error {
	if session, ok := h.Session(endpoint); ok {
		return session.Call(ctx, method, params, result)
	}
	holder, err := h.holder(ctx, endpoint)
	if err != nil {
		return err
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		return err
	}
	body, _ := json.Marshal(forwardedCall{Endpoint: endpoint, Method: method, Params: encoded})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, holder+internalPrefix+"call", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	h.cluster.sign(request, body)
	response, err := h.cluster.Client.Do(request)
	if err != nil {
		return fmt.Errorf("forward %s: %w", method, err)
	}
	defer response.Body.Close()
	var reply forwardedReply
	if err := json.NewDecoder(io.LimitReader(response.Body, maxFrameBytes)).Decode(&reply); err != nil {
		return fmt.Errorf("forward %s: HTTP %d", method, response.StatusCode)
	}
	switch {
	case reply.Offline:
		return ErrOffline
	case reply.Error != nil:
		return reply.Error
	case result != nil && len(reply.Result) > 0:
		return json.Unmarshal(reply.Result, result)
	}
	return nil
}

// OpenTerminal opens a shell through the agent for endpoint, possibly via
// the API instance that holds its connection.
func (h *Hub) OpenTerminal(ctx context.Context, endpoint, name string, cols, rows int) (wsterm.Session, error) {
	if session, ok := h.Session(endpoint); ok {
		return session.OpenStream(ctx, name, cols, rows)
	}
	holder, err := h.holder(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	query := url.Values{"endpoint": {endpoint}, "name": {name}, "cols": {strconv.Itoa(cols)}, "rows": {strconv.Itoa(rows)}}
	target := holder + internalPrefix + "stream?" + query.Encode()
	signing, _ := http.NewRequest(http.MethodGet, target, nil)
	h.cluster.sign(signing, nil)
	conn, response, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(target, "http"), &websocket.DialOptions{HTTPClient: h.cluster.Client, HTTPHeader: signing.Header})
	if err != nil {
		if response != nil && response.Header.Get(headerErrorCode) != "" {
			message, _ := url.QueryUnescape(response.Header.Get(headerErrorText))
			if response.Header.Get(headerErrorCode) == "offline" {
				return nil, ErrOffline
			}
			return nil, &protocol.Error{Code: response.Header.Get(headerErrorCode), Message: message}
		}
		return nil, fmt.Errorf("forward terminal: %w", err)
	}
	conn.SetReadLimit(maxFrameBytes)
	return wsterm.NewClient(conn), nil
}

func (h *Hub) holder(ctx context.Context, endpoint string) (string, error) {
	if h.cluster == nil {
		return "", ErrOffline
	}
	holder, ok, err := h.cluster.Directory.Lookup(ctx, endpoint, h.cluster.InstanceID)
	if err != nil {
		return "", fmt.Errorf("look up agent holder: %w", err)
	}
	if !ok {
		return "", ErrOffline
	}
	return strings.TrimRight(holder, "/"), nil
}

type forwardedCall struct {
	Endpoint string          `json:"endpoint"`
	Method   string          `json:"method"`
	Params   json.RawMessage `json:"params"`
}

type forwardedReply struct {
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *protocol.Error `json:"error,omitempty"`
	Offline bool            `json:"offline,omitempty"`
}

// InternalHandler serves forwarded requests from other API instances. It
// only uses local sessions, so forwarding never loops.
func (h *Hub) InternalHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+internalPrefix+"call", h.serveForwardedCall)
	mux.HandleFunc("GET "+internalPrefix+"stream", h.serveForwardedStream)
	return mux
}

func (h *Hub) serveForwardedCall(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxFrameBytes))
	if err != nil || h.cluster == nil || !h.cluster.verify(r, body) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	var call forwardedCall
	if err := json.Unmarshal(body, &call); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	var reply forwardedReply
	session, ok := h.Session(call.Endpoint)
	if !ok {
		reply.Offline = true
	} else {
		var result json.RawMessage
		err := session.Call(r.Context(), call.Method, call.Params, &result)
		var agentErr *protocol.Error
		switch {
		case err == nil:
			reply.Result = result
		case errors.Is(err, ErrOffline):
			reply.Offline = true
		case errors.As(err, &agentErr):
			reply.Error = agentErr
		default:
			reply.Error = &protocol.Error{Code: protocol.CodeInternal, Message: err.Error()}
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(reply)
}

func (h *Hub) serveForwardedStream(w http.ResponseWriter, r *http.Request) {
	if h.cluster == nil || !h.cluster.verify(r, nil) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	query := r.URL.Query()
	cols, _ := strconv.Atoi(query.Get("cols"))
	rows, _ := strconv.Atoi(query.Get("rows"))
	session, ok := h.Session(query.Get("endpoint"))
	if !ok {
		w.Header().Set(headerErrorCode, "offline")
		http.Error(w, "agent offline", http.StatusBadGateway)
		return
	}
	stream, err := session.OpenStream(r.Context(), query.Get("name"), cols, rows)
	if err != nil {
		code, message := protocol.CodeInternal, err.Error()
		var agentErr *protocol.Error
		if errors.As(err, &agentErr) {
			code, message = agentErr.Code, agentErr.Message
		} else if errors.Is(err, ErrOffline) {
			code = "offline"
		}
		w.Header().Set(headerErrorCode, code)
		w.Header().Set(headerErrorText, url.QueryEscape(message))
		http.Error(w, message, http.StatusBadGateway)
		return
	}
	defer stream.Close()
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Time{})
	_ = controller.SetWriteDeadline(time.Time{})
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxFrameBytes)
	wsterm.Pump(context.Background(), conn, stream)
}

// sign adds a timestamped HMAC over the method, request URI and body.
func (c *Cluster) sign(r *http.Request, body []byte) {
	timestamp := strconv.FormatInt(time.Now().Unix(), 10)
	r.Header.Set(headerTimestamp, timestamp)
	r.Header.Set(headerSignature, c.signature(r.Method, r.URL.RequestURI(), timestamp, body))
}

func (c *Cluster) verify(r *http.Request, body []byte) bool {
	timestamp := r.Header.Get(headerTimestamp)
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return false
	}
	if skew := time.Since(time.Unix(seconds, 0)); skew > maxClockSkew || skew < -maxClockSkew {
		return false
	}
	expected := c.signature(r.Method, r.URL.RequestURI(), timestamp, body)
	return hmac.Equal([]byte(expected), []byte(r.Header.Get(headerSignature)))
}

func (c *Cluster) signature(method, uri, timestamp string, body []byte) string {
	digest := sha256.Sum256(body)
	mac := hmac.New(sha256.New, c.Key)
	fmt.Fprintf(mac, "%s\n%s\n%s\n%x", method, uri, timestamp, digest)
	return hex.EncodeToString(mac.Sum(nil))
}
