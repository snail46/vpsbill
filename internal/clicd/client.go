package clicd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type APIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type APIError struct {
	StatusCode int
	Message    string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("clicd api returned %d: %s", e.StatusCode, e.Message)
}

func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

type Container struct {
	ID             int    `json:"id"`
	UUID           string `json:"uuid"`
	Name           string `json:"name"`
	Virtualization string `json:"virtualization"`
	Status         string `json:"status"`
	IP             string `json:"ip"`
	IPv6           string `json:"ipv6"`
	VCPU           int    `json:"vcpu"`
	RAMMB          int    `json:"ram_mb"`
	DiskGB         int    `json:"disk_gb"`
}

type CreateSpec struct {
	Name             string   `json:"name"`
	Virtualization   string   `json:"virtualization"`
	TemplateID       string   `json:"template_id"`
	VCPU             int      `json:"vcpu"`
	RAMMB            int      `json:"ram_mb"`
	DiskGB           int      `json:"disk_gb"`
	AssignNAT        bool     `json:"assign_nat"`
	PortMappingCount int      `json:"port_mapping_count,omitempty"`
	AssignIPv4       bool     `json:"assign_ipv4"`
	IPv4Count        int      `json:"ipv4_count,omitempty"`
	PublicIPv4s      []string `json:"public_ipv4s,omitempty"`
	AssignIPv6       bool     `json:"assign_ipv6"`
	IPv6Count        int      `json:"ipv6_count,omitempty"`
	IPv6Addresses    []string `json:"ipv6_addresses,omitempty"`
	SSHAuthMode      string   `json:"ssh_auth_mode"`
	SSHPublicKey     string   `json:"ssh_public_key,omitempty"`
	ExpiresAt        string   `json:"expires_at,omitempty"`
	NetworkDownMbps  int      `json:"network_down_mbps"`
	NetworkUpMbps    int      `json:"network_up_mbps"`
	MonthlyTrafficGB int      `json:"monthly_traffic_gb,omitempty"`
	SnapshotLimit    int      `json:"snapshot_limit,omitempty"`
}

type EnsureResult struct {
	Container Container
	Created   bool
}

func (c *Client) HostInfo(ctx context.Context) (map[string]any, error) {
	var response APIResponse[map[string]any]
	err := c.do(ctx, http.MethodGet, "/api/v1/host-info", nil, &response)
	return response.Data, err
}

func NewClient(baseURL, apiKey string, timeout time.Duration) (*Client, error) {
	parsed, err := url.Parse(strings.TrimRight(baseURL, "/"))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("invalid CLICD base URL")
	}
	if strings.TrimSpace(apiKey) == "" {
		return nil, errors.New("CLICD API key is required")
	}
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		baseURL: parsed.String(),
		apiKey:  apiKey,
		http: &http.Client{
			Timeout: timeout,
			Transport: &http.Transport{
				MaxIdleConns:        50,
				MaxIdleConnsPerHost: 10,
				IdleConnTimeout:     90 * time.Second,
			},
		},
	}, nil
}

func (c *Client) GetContainer(ctx context.Context, idOrName string) (Container, error) {
	var response APIResponse[Container]
	err := c.do(ctx, http.MethodGet, "/api/v1/containers/"+url.PathEscape(idOrName), nil, &response)
	return response.Data, err
}

func (c *Client) CreateContainer(ctx context.Context, spec CreateSpec) error {
	var response APIResponse[json.RawMessage]
	return c.do(ctx, http.MethodPost, "/api/v1/containers", spec, &response)
}

// PowerAction submits CLICD's asynchronous start/stop/restart operation. The
// returned task identifier is retained when the node provides one.
func (c *Client) PowerAction(ctx context.Context, idOrName, action string) (string, error) {
	switch action {
	case "start", "stop", "restart":
	default:
		return "", errors.New("unsupported power action")
	}
	if strings.TrimSpace(idOrName) == "" {
		return "", errors.New("container identifier is required")
	}
	var response APIResponse[map[string]any]
	err := c.do(ctx, http.MethodPost, "/api/v1/containers/"+url.PathEscape(idOrName)+"/"+action, nil, &response)
	if err != nil {
		return "", err
	}
	for _, key := range []string{"task_id", "id"} {
		if value, ok := response.Data[key].(string); ok {
			return value, nil
		}
		if value, ok := response.Data[key].(float64); ok {
			return fmt.Sprintf("%.0f", value), nil
		}
	}
	return "", nil
}

// DeleteContainer submits CLICD's documented DELETE .../{id-or-name}/delete action.
// A missing container is treated as success so termination remains safe to retry.
func (c *Client) DeleteContainer(ctx context.Context, idOrName string) error {
	if strings.TrimSpace(idOrName) == "" {
		return errors.New("container identifier is required")
	}
	err := c.do(ctx, http.MethodDelete, "/api/v1/containers/"+url.PathEscape(idOrName)+"/delete", nil, nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}

// EnsureContainer makes a synchronous CLICD create safe to retry. CLICD does
// not currently document an idempotency key for this endpoint, so the service
// name is used as the stable identity and queried again after ambiguous errors.
func (c *Client) EnsureContainer(ctx context.Context, spec CreateSpec) (EnsureResult, error) {
	if strings.TrimSpace(spec.Name) == "" {
		return EnsureResult{}, errors.New("container name is required")
	}
	existing, err := c.GetContainer(ctx, spec.Name)
	if err == nil {
		return EnsureResult{Container: existing, Created: false}, nil
	}
	if !IsNotFound(err) {
		return EnsureResult{}, fmt.Errorf("check existing container: %w", err)
	}

	createErr := c.CreateContainer(ctx, spec)
	created, lookupErr := c.GetContainer(ctx, spec.Name)
	if lookupErr == nil {
		return EnsureResult{Container: created, Created: createErr == nil}, nil
	}
	if createErr != nil {
		return EnsureResult{}, fmt.Errorf("create container: %w; reconciliation failed: %v", createErr, lookupErr)
	}
	return EnsureResult{}, fmt.Errorf("container creation returned success but lookup failed: %w", lookupErr)
}

func (c *Client) do(ctx context.Context, method, path string, body any, target any) error {
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
	req.Header.Set("X-API-Key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("send request: %w", err)
	}
	defer resp.Body.Close()
	limited := io.LimitReader(resp.Body, 2<<20)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var apiResponse APIResponse[json.RawMessage]
		_ = json.NewDecoder(limited).Decode(&apiResponse)
		message := apiResponse.Message
		if message == "" {
			message = http.StatusText(resp.StatusCode)
		}
		return &APIError{StatusCode: resp.StatusCode, Message: message}
	}
	if target == nil {
		return nil
	}
	if err := json.NewDecoder(limited).Decode(target); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
