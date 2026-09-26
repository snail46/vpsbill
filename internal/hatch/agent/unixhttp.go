package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// unixClient speaks HTTP to a local daemon socket (LXD or Podman).
type unixClient struct {
	http *http.Client
	base string
}

func newUnixClient(socket string) *unixClient {
	return &unixClient{
		base: "http://local",
		http: &http.Client{Timeout: 5 * time.Minute, Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socket)
			},
		}},
	}
}

// apiError is a non-2xx daemon response.
type apiError struct {
	Status  int
	Message string
}

func (e *apiError) Error() string { return fmt.Sprintf("daemon returned %d: %s", e.Status, e.Message) }

// do sends a JSON request and returns the raw body of a 2xx response.
func (c *unixClient) do(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var message struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(data, &message)
		text := message.Error
		if text == "" {
			text = message.Message
		}
		if text == "" {
			text = http.StatusText(resp.StatusCode)
		}
		return nil, &apiError{Status: resp.StatusCode, Message: text}
	}
	return data, nil
}
