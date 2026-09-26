// Package lxdapiprovider drives xkatld/lxdapi-web-server nodes through its
// system API (/api/system, X-API-Hash). The API is documented by the server's
// own handlers; every response is HTTP 200 with {code,msg,data}, so the code
// field, not the HTTP status, carries the outcome.
package lxdapiprovider

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"vpsbill/internal/provider"
)

type envelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

type client struct {
	baseURL string
	apiHash string
	http    *http.Client
}

func newClient(baseURL, apiHash string, options Options, timeout time.Duration) (*client, error) {
	parsed, err := url.Parse(strings.TrimRight(strings.TrimSpace(baseURL), "/"))
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return nil, errors.New("LXDAPI 地址必须是 https://主机:端口")
	}
	if strings.TrimSpace(apiHash) == "" {
		return nil, errors.New("LXDAPI API Hash is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	tlsConfig, err := options.tlsConfig()
	if err != nil {
		return nil, err
	}
	return &client{
		baseURL: parsed.String(),
		apiHash: apiHash,
		http: &http.Client{Timeout: timeout, Transport: &http.Transport{
			TLSClientConfig: tlsConfig, MaxIdleConnsPerHost: 10, IdleConnTimeout: 90 * time.Second,
		}},
	}, nil
}

// tlsConfig verifies the node certificate against system roots when
// tls_verify is set, otherwise pins it to tls_fingerprint. LXDAPI installs a
// self-signed certificate by default, so one of the two must be configured.
func (o Options) tlsConfig() (*tls.Config, error) {
	if o.TLSVerify {
		return &tls.Config{MinVersion: tls.VersionTLS12}, nil
	}
	pin := strings.ToLower(strings.NewReplacer(":", "", " ", "").Replace(o.TLSFingerprint))
	if len(pin) != sha256.Size*2 {
		return nil, errors.New("未启用证书校验时必须填写 64 位十六进制的证书 SHA-256 指纹")
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // replaced by the fingerprint check below
		VerifyPeerCertificate: func(rawCerts [][]byte, _ [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return errors.New("node presented no certificate")
			}
			sum := sha256.Sum256(rawCerts[0])
			if hex.EncodeToString(sum[:]) != pin {
				return errors.New("node certificate fingerprint mismatch")
			}
			return nil
		},
	}, nil
}

func (c *client) do(ctx context.Context, method, path string, body any, target any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-API-Hash", c.apiHash)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	var result envelope
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&result); err != nil {
		if resp.StatusCode != http.StatusOK {
			return &provider.Error{Provider: Type, StatusCode: resp.StatusCode, Message: http.StatusText(resp.StatusCode)}
		}
		return fmt.Errorf("decode response: %w", err)
	}
	if result.Code != http.StatusOK {
		apiErr := &provider.Error{Provider: Type, StatusCode: result.Code, Message: result.Msg}
		if result.Code == http.StatusNotFound {
			return errors.Join(provider.ErrNotFound, apiErr)
		}
		return apiErr
	}
	if target == nil || len(result.Data) == 0 {
		return nil
	}
	if err := json.Unmarshal(result.Data, target); err != nil {
		return fmt.Errorf("decode %s data: %w", path, err)
	}
	return nil
}

// container mirrors models.Container, which LXDAPI serializes with Go field
// names because the model has no json tags.
type container struct {
	ID               uint
	Name             string
	Image            string
	Password         string
	Status           string
	CPU              int
	Memory           int
	Disk             int
	Ingress          int
	Egress           int
	TrafficLimit     int
	PrivateIP        string
	PrivateIPv6      string
	IPv4             string
	IPv6             string
	IPv4MappingLimit int
	CPUUsage         float64
	MemoryUsageRaw   uint64
	DiskUsageRaw     uint64
	TrafficUsageRaw  uint64
}

type containerDetail struct {
	Container container `json:"container"`
	Status    string    `json:"status"`
}

type portMapping struct {
	ID            uint   `json:"id"`
	PublicIP      string `json:"public_ip"`
	PublicPort    int    `json:"public_port"`
	ContainerName string `json:"container_name"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Description   string `json:"description"`
}

type task struct {
	ID       uint   `json:"ID"`
	Action   string `json:"action"`
	Status   string `json:"status"`
	ErrorMsg string `json:"error_msg"`
}

type traffic struct {
	RxBytes int64
	TxBytes int64
	TotalGB float64
	LimitGB int
	Locked  bool
}

func (c *client) container(ctx context.Context, name string) (containerDetail, error) {
	var detail containerDetail
	err := c.do(ctx, http.MethodGet, "/api/system/containers/"+url.PathEscape(name), nil, &detail)
	return detail, err
}

func (c *client) action(ctx context.Context, name, action string, body any, target any) error {
	path := "/api/system/containers/" + url.PathEscape(name) + "/action?action=" + url.QueryEscape(action)
	return c.do(ctx, http.MethodPost, path, body, target)
}

func (c *client) portMappings(ctx context.Context, containerName string) ([]portMapping, error) {
	query := url.Values{"version": {"v4"}}
	if containerName != "" {
		query.Set("container", containerName)
	}
	var result struct {
		IPv4 []portMapping `json:"ipv4"`
	}
	err := c.do(ctx, http.MethodGet, "/api/system/port-mapping?"+query.Encode(), nil, &result)
	return result.IPv4, err
}

func (c *client) tasks(ctx context.Context, name string) ([]task, error) {
	var result []task
	err := c.do(ctx, http.MethodGet, "/api/system/tasks?name="+url.QueryEscape(name), nil, &result)
	return result, err
}

// waitTask polls an asynchronous LXDAPI task until it finishes or ctx ends.
func (c *client) waitTask(ctx context.Context, id uint) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	for {
		var current task
		if err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/system/tasks/detail?id=%d", id), nil, &current); err != nil {
			return fmt.Errorf("poll task %d: %w", id, err)
		}
		switch current.Status {
		case "success":
			return nil
		case "failed":
			return &provider.Error{Provider: Type, StatusCode: http.StatusUnprocessableEntity, Message: current.ErrorMsg}
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("task %d still %s: %w", id, current.Status, ctx.Err())
		case <-ticker.C:
		}
	}
}

var pollInterval = 2 * time.Second

type addressList struct {
	IPv4 []string `json:"ipv4"`
	IPv6 []string `json:"ipv6"`
}

// addresses returns the container's dedicated pool addresses. Errors are
// ignored because LXDAPI rejects the call when a pool feature is disabled.
func (c *client) addresses(ctx context.Context, name string) addressList {
	var result addressList
	_ = c.do(ctx, http.MethodGet, "/api/system/ip?version=all&container="+url.QueryEscape(name), nil, &result)
	return result
}
