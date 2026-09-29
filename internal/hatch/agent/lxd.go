package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"

	"vpsbill/internal/hatch/protocol"
)

// LXD drives system containers through the LXD REST API (/1.0).
type LXD struct {
	client *unixClient
	config LXDConfig
}

func NewLXD(config LXDConfig) *LXD {
	return &LXD{client: newUnixClient(config.Socket), config: config}
}

func (l *LXD) Virtualization() string { return "lxc" }

type lxdResponse struct {
	Type      string          `json:"type"`
	Operation string          `json:"operation"`
	Metadata  json.RawMessage `json:"metadata"`
}

// request performs a call and, for asynchronous responses, waits for the
// background operation to finish. It returns the (final) metadata.
func (l *LXD) request(ctx context.Context, method, path string, body any) (json.RawMessage, error) {
	data, err := l.client.do(ctx, method, path, body)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return nil, ErrInstanceNotFound
		}
		return nil, err
	}
	var response lxdResponse
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decode LXD response: %w", err)
	}
	if response.Type != "async" {
		return response.Metadata, nil
	}
	waited, err := l.client.do(ctx, http.MethodGet, response.Operation+"/wait?timeout=600", nil)
	if err != nil {
		return nil, fmt.Errorf("wait for %s: %w", response.Operation, err)
	}
	var operation struct {
		// LXD answers the wait for a failed operation with an error
		// response (type "error") instead of a Failure status.
		Type     string `json:"type"`
		Error    string `json:"error"`
		Metadata struct {
			Status   string          `json:"status"`
			Err      string          `json:"err"`
			Metadata json.RawMessage `json:"metadata"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(waited, &operation); err != nil {
		return nil, fmt.Errorf("decode LXD operation: %w", err)
	}
	if operation.Type == "error" {
		return nil, fmt.Errorf("LXD operation failed: %s", operation.Error)
	}
	if operation.Metadata.Status != "Success" {
		return nil, fmt.Errorf("LXD operation %s: %s", strings.ToLower(operation.Metadata.Status), operation.Metadata.Err)
	}
	return operation.Metadata.Metadata, nil
}

func instancePath(name string) string { return "/1.0/instances/" + url.PathEscape(name) }

func (l *LXD) Images(ctx context.Context) ([]protocol.Image, error) {
	metadata, err := l.request(ctx, http.MethodGet, "/1.0/images?recursion=1", nil)
	if err != nil {
		return nil, err
	}
	var images []struct {
		Type       string                  `json:"type"`
		Aliases    []struct{ Name string } `json:"aliases"`
		Properties map[string]string       `json:"properties"`
	}
	if err := json.Unmarshal(metadata, &images); err != nil {
		return nil, err
	}
	result := []protocol.Image{}
	for _, image := range images {
		if image.Type != "" && image.Type != "container" {
			continue
		}
		for _, alias := range image.Aliases {
			result = append(result, protocol.Image{ID: alias.Name, Name: alias.Name, Virtualization: "lxc", Description: image.Properties["description"]})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (l *LXD) Network(ctx context.Context) (NetworkInfo, error) {
	metadata, err := l.request(ctx, http.MethodGet, "/1.0/networks/"+url.PathEscape(l.config.Network), nil)
	if err != nil {
		return NetworkInfo{}, err
	}
	var network struct {
		Config map[string]string `json:"config"`
	}
	if err := json.Unmarshal(metadata, &network); err != nil {
		return NetworkInfo{}, err
	}
	// ipv4.address and ipv6.address hold the bridge address in CIDR form,
	// e.g. 10.20.30.1/24; "none" or "auto" means no static subnet.
	ipv4, err := netip.ParsePrefix(network.Config["ipv4.address"])
	if err != nil {
		return NetworkInfo{}, fmt.Errorf("network %s has no static ipv4.address: %w", l.config.Network, err)
	}
	info := NetworkInfo{IPv4: ipv4.Masked(), IPv4Gateway: ipv4.Addr()}
	if ipv6, err := netip.ParsePrefix(network.Config["ipv6.address"]); err == nil && ipv6.Addr().Is6() {
		info.IPv6, info.IPv6Gateway = ipv6.Masked(), ipv6.Addr()
	}
	return info, nil
}

func (l *LXD) Create(ctx context.Context, spec RuntimeSpec) error {
	nic := map[string]string{"type": "nic", "network": l.config.Network, "name": "eth0", "ipv4.address": spec.IPv4.String()}
	if spec.IPv6.IsValid() {
		nic["ipv6.address"] = spec.IPv6.String()
	}
	if spec.NetworkDownMbps > 0 {
		nic["limits.ingress"] = strconv.Itoa(spec.NetworkDownMbps) + "Mbit"
	}
	if spec.NetworkUpMbps > 0 {
		nic["limits.egress"] = strconv.Itoa(spec.NetworkUpMbps) + "Mbit"
	}
	body := map[string]any{
		"name": spec.Name, "type": "container",
		"source": map[string]string{"type": "image", "alias": spec.Image},
		"config": map[string]string{
			"limits.cpu": strconv.Itoa(spec.VCPU), "limits.memory": strconv.Itoa(spec.RAMMB) + "MiB",
			"security.nesting": "false", "boot.autostart": "true",
		},
		"devices": map[string]any{
			"root": map[string]string{"type": "disk", "path": "/", "pool": l.config.StoragePool, "size": strconv.Itoa(spec.DiskGB) + "GiB"},
			"eth0": nic,
		},
	}
	if _, err := l.request(ctx, http.MethodPost, "/1.0/instances", body); err != nil {
		return err
	}
	return l.Start(ctx, spec.Name)
}

func (l *LXD) State(ctx context.Context, name string) (RuntimeState, error) {
	metadata, err := l.request(ctx, http.MethodGet, instancePath(name)+"/state", nil)
	if err != nil && !errors.Is(err, ErrInstanceNotFound) {
		// Incus answers /state with 500 "Invalid PID" while a container is
		// stopping; the instance record still carries a usable status.
		if status, statusErr := l.status(ctx, name); statusErr == nil {
			return RuntimeState{Status: status}, nil
		}
	}
	if err != nil {
		return RuntimeState{}, err
	}
	var state struct {
		Status string `json:"status"`
		CPU    struct {
			Usage int64 `json:"usage"`
		} `json:"cpu"`
		Memory struct {
			Usage int64 `json:"usage"`
		} `json:"memory"`
		Disk map[string]struct {
			Usage int64 `json:"usage"`
		} `json:"disk"`
		Network map[string]struct {
			Addresses []struct {
				Family  string `json:"family"`
				Address string `json:"address"`
				Scope   string `json:"scope"`
			} `json:"addresses"`
			Counters struct {
				BytesReceived int64 `json:"bytes_received"`
				BytesSent     int64 `json:"bytes_sent"`
			} `json:"counters"`
		} `json:"network"`
	}
	if err := json.Unmarshal(metadata, &state); err != nil {
		return RuntimeState{}, err
	}
	result := RuntimeState{Status: lxdStatus(state.Status), CPUNanos: state.CPU.Usage, MemoryBytes: state.Memory.Usage, DiskBytes: state.Disk["root"].Usage}
	if eth0, ok := state.Network["eth0"]; ok {
		// LXD reports the counters from the instance's point of view.
		result.RXBytes, result.TXBytes = eth0.Counters.BytesReceived, eth0.Counters.BytesSent
		for _, address := range eth0.Addresses {
			if address.Family == "inet" && address.Scope == "global" {
				result.IPv4 = address.Address
				break
			}
		}
	}
	return result, nil
}

func lxdStatus(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "running"
	case "stopped":
		return "stopped"
	case "frozen":
		return "paused"
	default:
		return "unknown"
	}
}

func (l *LXD) setState(ctx context.Context, name, action string, force bool) error {
	_, err := l.request(ctx, http.MethodPut, instancePath(name)+"/state", map[string]any{"action": action, "timeout": 30, "force": force})
	return err
}

func (l *LXD) Start(ctx context.Context, name string) error {
	return l.setState(ctx, name, "start", false)
}
func (l *LXD) Stop(ctx context.Context, name string) error {
	return l.setState(ctx, name, "stop", true)
}
func (l *LXD) Restart(ctx context.Context, name string) error {
	return l.setState(ctx, name, "restart", true)
}
func (l *LXD) Pause(ctx context.Context, name string) error {
	return l.setState(ctx, name, "freeze", false)
}
func (l *LXD) Resume(ctx context.Context, name string) error {
	return l.setState(ctx, name, "unfreeze", false)
}

func (l *LXD) Delete(ctx context.Context, name string) error {
	state, err := l.State(ctx, name)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if state.Status == "paused" {
		if err := l.Resume(ctx, name); err != nil {
			return err
		}
	}
	if state.Status != "stopped" {
		if err := l.Stop(ctx, name); err != nil {
			return err
		}
	}
	_, err = l.request(ctx, http.MethodDelete, instancePath(name), nil)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil
	}
	return err
}

func (l *LXD) Exec(ctx context.Context, name, script string, env map[string]string) error {
	metadata, err := l.request(ctx, http.MethodPost, instancePath(name)+"/exec", map[string]any{
		"command": []string{"/bin/sh", "-c", script}, "environment": env,
		"wait-for-websocket": false, "interactive": false, "record-output": false,
	})
	if err != nil {
		return err
	}
	var result struct {
		Return int `json:"return"`
	}
	if err := json.Unmarshal(metadata, &result); err != nil {
		return err
	}
	if result.Return != 0 {
		return fmt.Errorf("command exited with status %d", result.Return)
	}
	return nil
}

// Storage reports the size and use of the storage pool instances use.
func (l *LXD) Storage(ctx context.Context) (int64, int64, error) {
	metadata, err := l.request(ctx, http.MethodGet, "/1.0/storage-pools/"+url.PathEscape(l.config.StoragePool)+"/resources", nil)
	if err != nil {
		return 0, 0, err
	}
	var resources struct {
		Space struct {
			Total int64 `json:"total"`
			Used  int64 `json:"used"`
		} `json:"space"`
	}
	if err := json.Unmarshal(metadata, &resources); err != nil {
		return 0, 0, err
	}
	return resources.Space.Total, resources.Space.Used, nil
}

// quotaDrivers are the storage drivers that enforce a root disk size. The
// dir driver only does with project quotas on the backing file system,
// which cannot be checked through the API, so it is refused.
var quotaDrivers = map[string]bool{"zfs": true, "btrfs": true, "lvm": true, "lvmcluster": true, "ceph": true}

// CheckDiskQuota fails unless the pool's driver limits root disk sizes.
func (l *LXD) CheckDiskQuota(ctx context.Context) error {
	metadata, err := l.request(ctx, http.MethodGet, "/1.0/storage-pools/"+url.PathEscape(l.config.StoragePool), nil)
	if err != nil {
		return fmt.Errorf("inspect storage pool %s: %w", l.config.StoragePool, err)
	}
	var pool struct {
		Driver string `json:"driver"`
	}
	if err := json.Unmarshal(metadata, &pool); err != nil {
		return err
	}
	if !quotaDrivers[pool.Driver] {
		return fmt.Errorf("存储池 %s 使用 %s 驱动，无法限制实例硬盘；请改用 btrfs、zfs 或 lvm 存储池", l.config.StoragePool, pool.Driver)
	}
	return nil
}

// status reads the status from the instance record, which does not need the
// running container's PID.
func (l *LXD) status(ctx context.Context, name string) (string, error) {
	metadata, err := l.request(ctx, http.MethodGet, instancePath(name), nil)
	if err != nil {
		return "", err
	}
	var instance struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(metadata, &instance); err != nil {
		return "", err
	}
	return lxdStatus(instance.Status), nil
}
