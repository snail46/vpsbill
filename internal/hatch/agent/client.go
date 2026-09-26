package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"time"

	"github.com/coder/websocket"

	"vpsbill/internal/hatch/protocol"
)

const (
	maxFrameBytes     = 4 << 20
	heartbeatInterval = 30 * time.Second
	maxConcurrent     = 16
	// requestTimeout bounds one request; instance creation waits for image
	// unpacking and first boot, so it is generous.
	requestTimeout = 10 * time.Minute
)

// Client keeps the agent connected to the billing server.
type Client struct {
	config  Config
	version string
	service *Service
	logger  *slog.Logger
	http    *http.Client
}

func NewClient(config Config, version string, service *Service, logger *slog.Logger) (*Client, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if config.CAFile != "" {
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read ca_file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("ca_file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	return &Client{
		config: config, version: version, service: service, logger: logger,
		http: &http.Client{Transport: &http.Transport{TLSClientConfig: tlsConfig, Proxy: http.ProxyFromEnvironment}},
	}, nil
}

// Run reconnects with jittered exponential backoff until ctx ends.
func (c *Client) Run(ctx context.Context) {
	backoff := time.Second
	for {
		started := time.Now()
		err := c.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		wait := backoff/2 + rand.N(backoff/2+1)
		c.logger.Warn("disconnected from billing server", "error", err, "retry_in", wait.Round(time.Millisecond).String())
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

func (c *Client) session(ctx context.Context) error {
	dialCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	conn, response, err := websocket.Dial(dialCtx, c.config.ConnectURL(), &websocket.DialOptions{
		HTTPClient: c.http,
		HTTPHeader: http.Header{"Authorization": {"Bearer " + c.config.Token}},
	})
	cancel()
	if err != nil {
		if response != nil {
			return fmt.Errorf("dial: %w (HTTP %d)", err, response.StatusCode)
		}
		return fmt.Errorf("dial: %w", err)
	}
	defer conn.CloseNow()
	conn.SetReadLimit(maxFrameBytes)

	hostname, _ := os.Hostname()
	if err := c.write(ctx, conn, protocol.Frame{Type: protocol.TypeHello, Params: mustJSON(protocol.Hello{
		ProtocolVersion: protocol.Version, AgentVersion: c.version, Hostname: hostname, Runtimes: c.config.Runtimes(),
	})}); err != nil {
		return err
	}
	c.logger.Info("connected to billing server", "url", c.config.ConnectURL())

	sessionCtx, stop := context.WithCancel(ctx)
	defer stop()
	go c.heartbeat(sessionCtx, conn)
	streams := newStreamTable()
	defer streams.closeAll()
	slots := make(chan struct{}, maxConcurrent)
	for {
		_, data, err := conn.Read(sessionCtx)
		if err != nil {
			return err
		}
		var frame protocol.Frame
		if err := json.Unmarshal(data, &frame); err != nil || frame.ID == "" {
			continue
		}
		if frame.Type == protocol.TypeStream {
			streams.dispatch(frame)
			continue
		}
		if frame.Type != protocol.TypeRequest {
			continue
		}
		select {
		case slots <- struct{}{}:
		case <-sessionCtx.Done():
			return sessionCtx.Err()
		}
		go func() {
			defer func() { <-slots }()
			c.respond(sessionCtx, conn, streams, frame)
		}()
	}
}

func (c *Client) respond(ctx context.Context, conn *websocket.Conn, streams *streamTable, request protocol.Frame) {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var result any
	var err error
	if request.Method == protocol.MethodConsoleOpen {
		err = c.openStream(requestCtx, ctx, conn, streams, request.Params)
	} else {
		result, err = c.service.Handle(requestCtx, request.Method, request.Params)
	}
	response := protocol.Frame{Type: protocol.TypeResponse, ID: request.ID}
	if err != nil {
		var protocolErr *protocol.Error
		if !errors.As(err, &protocolErr) {
			protocolErr = &protocol.Error{Code: protocol.CodeInternal, Message: err.Error()}
		}
		response.Error = protocolErr
		c.logger.Warn("request failed", "method", request.Method, "error", err)
	} else {
		response.Result = mustJSON(result)
	}
	if err := c.write(ctx, conn, response); err != nil {
		c.logger.Warn("send response", "method", request.Method, "error", err)
	}
}

func (c *Client) heartbeat(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		event := protocol.Frame{Type: protocol.TypeEvent, Method: protocol.EventHeartbeat, Params: mustJSON(protocol.Heartbeat{
			Time: time.Now().UTC(), Instances: len(c.service.store.List()),
		})}
		if err := c.write(ctx, conn, event); err != nil {
			return
		}
	}
}

func (c *Client) write(ctx context.Context, conn *websocket.Conn, frame protocol.Frame) error {
	data, err := json.Marshal(frame)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return conn.Write(writeCtx, websocket.MessageText, data)
}

func mustJSON(value any) json.RawMessage {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return data
}
