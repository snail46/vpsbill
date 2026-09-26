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
	"strings"
	"time"

	"vpsbill/internal/hatch/protocol"
)

// Podman drives containers through the libpod REST API. Images must run an
// init system and sshd (for example a systemd-based image) to behave like a
// VPS; the agent only sets the root password inside them.
type Podman struct {
	client *unixClient
	config PodmanConfig
}

const libpod = "/v4.0.0/libpod"

func NewPodman(config PodmanConfig) *Podman {
	return &Podman{client: newUnixClient(config.Socket), config: config}
}

func (p *Podman) Virtualization() string { return "podman" }

func (p *Podman) request(ctx context.Context, method, path string, body any, target any) error {
	data, err := p.client.do(ctx, method, libpod+path, body)
	if err != nil {
		var apiErr *apiError
		if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
			return ErrInstanceNotFound
		}
		return err
	}
	if target == nil || len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}

func containerPath(name string) string { return "/containers/" + url.PathEscape(name) }

func (p *Podman) Images(ctx context.Context) ([]protocol.Image, error) {
	var images []struct {
		RepoTags []string `json:"RepoTags"`
	}
	if err := p.request(ctx, http.MethodGet, "/images/json", nil, &images); err != nil {
		return nil, err
	}
	result := []protocol.Image{}
	for _, image := range images {
		for _, tag := range image.RepoTags {
			if tag != "" && !strings.HasPrefix(tag, "<none>") {
				result = append(result, protocol.Image{ID: tag, Name: tag, Virtualization: "podman"})
			}
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result, nil
}

func (p *Podman) Network(ctx context.Context) (netip.Prefix, netip.Addr, error) {
	var network struct {
		Subnets []struct {
			Subnet  string `json:"subnet"`
			Gateway string `json:"gateway"`
		} `json:"subnets"`
	}
	if err := p.request(ctx, http.MethodGet, "/networks/"+url.PathEscape(p.config.Network)+"/json", nil, &network); err != nil {
		return netip.Prefix{}, netip.Addr{}, fmt.Errorf("inspect network %s: %w", p.config.Network, err)
	}
	for _, subnet := range network.Subnets {
		prefix, err := netip.ParsePrefix(subnet.Subnet)
		if err != nil || !prefix.Addr().Is4() {
			continue
		}
		gateway, err := netip.ParseAddr(subnet.Gateway)
		if err != nil {
			gateway = prefix.Masked().Addr().Next()
		}
		return prefix.Masked(), gateway, nil
	}
	return netip.Prefix{}, netip.Addr{}, fmt.Errorf("network %s has no IPv4 subnet", p.config.Network)
}

func (p *Podman) Create(ctx context.Context, spec RuntimeSpec) error {
	body := map[string]any{
		"name": spec.Name, "image": spec.Image, "hostname": spec.Name,
		"systemd":        "true",
		"restart_policy": "always",
		"netns":          map[string]string{"nsmode": "bridge"},
		"networks":       map[string]any{p.config.Network: map[string]any{"static_ips": []string{spec.IPv4.String()}}},
		"resource_limits": map[string]any{
			"cpu":    map[string]int64{"quota": int64(spec.VCPU) * 100000, "period": 100000},
			"memory": map[string]int64{"limit": int64(spec.RAMMB) << 20},
		},
	}
	if p.config.DiskQuota {
		body["storage_opts"] = map[string]string{"size": fmt.Sprintf("%dG", spec.DiskGB)}
	}
	if err := p.request(ctx, http.MethodPost, "/containers/create", body, nil); err != nil {
		return err
	}
	return p.Start(ctx, spec.Name)
}

func (p *Podman) State(ctx context.Context, name string) (RuntimeState, error) {
	var inspect struct {
		State struct {
			Status string `json:"Status"`
		} `json:"State"`
		NetworkSettings struct {
			Networks map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := p.request(ctx, http.MethodGet, containerPath(name)+"/json", nil, &inspect); err != nil {
		return RuntimeState{}, err
	}
	result := RuntimeState{Status: podmanStatus(inspect.State.Status), IPv4: inspect.NetworkSettings.Networks[p.config.Network].IPAddress}
	if result.Status != "running" {
		return result, nil
	}
	var stats struct {
		Stats []struct {
			CPUNano     int64 `json:"CPUNano"`
			MemUsage    int64 `json:"MemUsage"`
			NetInput    int64 `json:"NetInput"`
			NetOutput   int64 `json:"NetOutput"`
			BlockInput  int64 `json:"BlockInput"`
			BlockOutput int64 `json:"BlockOutput"`
		} `json:"Stats"`
	}
	query := url.Values{"containers": {name}, "stream": {"false"}}
	if err := p.request(ctx, http.MethodGet, "/containers/stats?"+query.Encode(), nil, &stats); err == nil && len(stats.Stats) > 0 {
		current := stats.Stats[0]
		result.CPUNanos, result.MemoryBytes = current.CPUNano, current.MemUsage
		result.RXBytes, result.TXBytes = current.NetInput, current.NetOutput
		result.DiskReadBytes, result.DiskWriteBytes = current.BlockInput, current.BlockOutput
	}
	return result, nil
}

func podmanStatus(status string) string {
	switch strings.ToLower(status) {
	case "running":
		return "running"
	case "paused":
		return "paused"
	case "exited", "stopped", "created", "configured":
		return "stopped"
	default:
		return "unknown"
	}
}

func (p *Podman) action(ctx context.Context, name, action string) error {
	return p.request(ctx, http.MethodPost, containerPath(name)+"/"+action, nil, nil)
}

func (p *Podman) Start(ctx context.Context, name string) error { return p.action(ctx, name, "start") }
func (p *Podman) Stop(ctx context.Context, name string) error  { return p.action(ctx, name, "stop") }
func (p *Podman) Restart(ctx context.Context, name string) error {
	return p.action(ctx, name, "restart")
}
func (p *Podman) Pause(ctx context.Context, name string) error { return p.action(ctx, name, "pause") }
func (p *Podman) Resume(ctx context.Context, name string) error {
	return p.action(ctx, name, "unpause")
}

func (p *Podman) Delete(ctx context.Context, name string) error {
	err := p.request(ctx, http.MethodDelete, containerPath(name)+"?force=true&v=true", nil, nil)
	if errors.Is(err, ErrInstanceNotFound) {
		return nil
	}
	return err
}

// Exec starts the script detached and polls for its exit status, which
// avoids hijacking the HTTP connection for an attached stream.
func (p *Podman) Exec(ctx context.Context, name, script string, env map[string]string) error {
	variables := make([]string, 0, len(env))
	for key, value := range env {
		variables = append(variables, key+"="+value)
	}
	var created struct {
		ID string `json:"Id"`
	}
	if err := p.request(ctx, http.MethodPost, containerPath(name)+"/exec", map[string]any{
		"Cmd": []string{"/bin/sh", "-c", script}, "Env": variables, "AttachStdout": false, "AttachStderr": false,
	}, &created); err != nil {
		return err
	}
	if err := p.request(ctx, http.MethodPost, "/exec/"+created.ID+"/start", map[string]bool{"Detach": true}, nil); err != nil {
		return err
	}
	for {
		var inspect struct {
			Running  bool `json:"Running"`
			ExitCode int  `json:"ExitCode"`
		}
		if err := p.request(ctx, http.MethodGet, "/exec/"+created.ID+"/json", nil, &inspect); err != nil {
			return err
		}
		if !inspect.Running {
			if inspect.ExitCode != 0 {
				return fmt.Errorf("command exited with status %d", inspect.ExitCode)
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
