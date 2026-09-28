package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"vpsbill/internal/hatch/protocol"
)

// Podman drives containers through the libpod REST API. Images must run an
// init system and sshd (systemd on Debian, OpenRC on Alpine; see
// deploy/podman-images) to behave like a VPS; the agent only sets the root
// password inside them. Every container gets a root file system size limit,
// which needs the store on XFS with project quotas.
type Podman struct {
	client *unixClient
	config PodmanConfig
	run    CommandRunner
	// interfaceName resolves a host ifindex; replaced in tests.
	interfaceName func(index int) (string, error)

	mu     sync.Mutex
	shaped map[string]string // instance name -> host veth carrying its limits

	diskMu sync.Mutex
	disk   map[string]diskSample // instance name -> last measured root fs size

	// mount and space inspect the host file systems, exists checks for a
	// file; replaced in tests.
	mount  func(path string) (mountEntry, bool)
	space  func(path string) (int64, int64, error)
	exists func(path string) bool
}

const libpod = "/v4.0.0/libpod"

func NewPodman(config PodmanConfig) *Podman {
	return &Podman{
		client: newUnixClient(config.Socket), config: config, run: runCommand, shaped: map[string]string{}, disk: map[string]diskSample{},
		mount: mountOf, space: fsSpace, exists: func(path string) bool { _, err := os.Stat(path); return err == nil },
		interfaceName: func(index int) (string, error) {
			iface, err := net.InterfaceByIndex(index)
			if err != nil {
				return "", err
			}
			return iface.Name, nil
		},
	}
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

func (p *Podman) Network(ctx context.Context) (NetworkInfo, error) {
	var network struct {
		Subnets []struct {
			Subnet  string `json:"subnet"`
			Gateway string `json:"gateway"`
		} `json:"subnets"`
	}
	if err := p.request(ctx, http.MethodGet, "/networks/"+url.PathEscape(p.config.Network)+"/json", nil, &network); err != nil {
		return NetworkInfo{}, fmt.Errorf("inspect network %s: %w", p.config.Network, err)
	}
	var info NetworkInfo
	for _, subnet := range network.Subnets {
		prefix, err := netip.ParsePrefix(subnet.Subnet)
		if err != nil {
			continue
		}
		gateway, err := netip.ParseAddr(subnet.Gateway)
		if err != nil {
			gateway = prefix.Masked().Addr().Next()
		}
		if prefix.Addr().Is4() && !info.IPv4.IsValid() {
			info.IPv4, info.IPv4Gateway = prefix.Masked(), gateway
		} else if prefix.Addr().Is6() && !info.IPv6.IsValid() {
			info.IPv6, info.IPv6Gateway = prefix.Masked(), gateway
		}
	}
	if !info.IPv4.IsValid() {
		return NetworkInfo{}, fmt.Errorf("network %s has no IPv4 subnet", p.config.Network)
	}
	return info, nil
}

// store reads where Podman keeps images and containers.
func (p *Podman) store(ctx context.Context) (driver, root string, err error) {
	var info struct {
		Store struct {
			GraphDriverName string `json:"graphDriverName"`
			GraphRoot       string `json:"graphRoot"`
		} `json:"store"`
	}
	if err := p.request(ctx, http.MethodGet, "/info", nil, &info); err != nil {
		return "", "", fmt.Errorf("read podman info: %w", err)
	}
	return info.Store.GraphDriverName, info.Store.GraphRoot, nil
}

// Storage reports the size and use of the file system holding the store.
func (p *Podman) Storage(ctx context.Context) (int64, int64, error) {
	_, root, err := p.store(ctx)
	if err != nil {
		return 0, 0, err
	}
	return p.space(root)
}

// CheckDiskQuota requires the overlay driver on XFS with project quotas,
// the only setup where Podman can cap a container's root file system.
func (p *Podman) CheckDiskQuota(ctx context.Context) error {
	driver, root, err := p.store(ctx)
	if err != nil {
		return err
	}
	if driver != "overlay" {
		return fmt.Errorf("Podman 存储驱动是 %s，需要 overlay 才能限制实例硬盘", driver)
	}
	entry, ok := p.mount(root)
	if !ok || !hasProjectQuota(entry) {
		return errNoProjectQuota
	}
	return nil
}

// Small instances: swap may add as much again as the memory limit (backed
// by zram on hosts set up by the installer), and a process cap keeps a fork
// bomb inside its container.
const (
	podmanSwapFactor = 2
	podmanPidsLimit  = 1024
	// podmanLogSize caps the init's console log kept by conmon.
	podmanLogSize = 1 << 20
)

// lxcfsFiles are the /proc and /sys views lxcfs renders from a container's
// cgroup, so free, top and uptime inside it show its own limits instead of
// the host's.
var lxcfsFiles = []string{
	"/proc/cpuinfo", "/proc/diskstats", "/proc/meminfo", "/proc/stat", "/proc/swaps", "/proc/uptime", "/proc/loadavg",
	"/sys/devices/system/cpu/online",
}

const lxcfsRoot = "/var/lib/lxcfs"

// lxcfsMounts binds the lxcfs views that exist on the host.
func (p *Podman) lxcfsMounts() []map[string]any {
	var mounts []map[string]any
	for _, file := range lxcfsFiles {
		if source := lxcfsRoot + file; p.exists(source) {
			mounts = append(mounts, map[string]any{"destination": file, "type": "bind", "source": source, "options": []string{"rbind"}})
		}
	}
	return mounts
}

func (p *Podman) Create(ctx context.Context, spec RuntimeSpec) error {
	memory := int64(spec.RAMMB) << 20
	body := map[string]any{
		"name": spec.Name, "image": spec.Image, "hostname": spec.Name,
		"systemd":        "true",
		"restart_policy": "always",
		"netns":          map[string]string{"nsmode": "bridge"},
		"networks":       map[string]any{p.config.Network: map[string]any{"static_ips": staticIPs(spec)}},
		"labels": map[string]string{
			labelDown: strconv.Itoa(spec.NetworkDownMbps), labelUp: strconv.Itoa(spec.NetworkUpMbps),
		},
		"resource_limits": map[string]any{
			"cpu":    map[string]int64{"quota": int64(spec.VCPU) * 100000, "period": 100000},
			"memory": map[string]int64{"limit": memory, "swap": memory * podmanSwapFactor},
			"pids":   map[string]int64{"limit": podmanPidsLimit},
		},
		"storage_opts":      map[string]string{"size": fmt.Sprintf("%dG", spec.DiskGB)},
		"log_configuration": map[string]any{"driver": "k8s-file", "size": podmanLogSize},
	}
	if mounts := p.lxcfsMounts(); len(mounts) > 0 {
		body["mounts"] = mounts
	}
	if err := p.request(ctx, http.MethodPost, "/containers/create", body, nil); err != nil {
		return err
	}
	// A container that cannot start (an address already taken on the
	// bridge, a bad mount) is removed so the next attempt starts clean.
	if err := p.Start(ctx, spec.Name); err != nil {
		if cleanup := p.Delete(ctx, spec.Name); cleanup != nil {
			return fmt.Errorf("%w (remove failed container: %v)", err, cleanup)
		}
		return err
	}
	return nil
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
	result.DiskBytes = p.diskUsage(ctx, name)
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

func (p *Podman) Start(ctx context.Context, name string) error {
	if err := p.action(ctx, name, "start"); err != nil {
		return err
	}
	return p.reshape(ctx, name)
}
func (p *Podman) Stop(ctx context.Context, name string) error { return p.action(ctx, name, "stop") }
func (p *Podman) Restart(ctx context.Context, name string) error {
	if err := p.action(ctx, name, "restart"); err != nil {
		return err
	}
	return p.reshape(ctx, name)
}
func (p *Podman) Pause(ctx context.Context, name string) error { return p.action(ctx, name, "pause") }
func (p *Podman) Resume(ctx context.Context, name string) error {
	return p.action(ctx, name, "unpause")
}

func (p *Podman) Delete(ctx context.Context, name string) error {
	p.mu.Lock()
	delete(p.shaped, name)
	p.mu.Unlock()
	p.diskMu.Lock()
	delete(p.disk, name)
	p.diskMu.Unlock()
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
		"Cmd": []string{"/bin/sh", "-c", script}, "Env": variables, "AttachStdout": true, "AttachStderr": true,
	}, &created); err != nil {
		return err
	}
	// Start attached: the response streams the (discarded) output and ends
	// when the command exits. Detached sessions are unusable here because
	// Podman 4.x can report them as running forever after they finish.
	if _, err := p.client.do(ctx, http.MethodPost, libpod+"/exec/"+created.ID+"/start", map[string]bool{"Detach": false}); err != nil {
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

const (
	labelDown = "hatch.down_mbps"
	labelUp   = "hatch.up_mbps"
)

// reshape forces bandwidth limits to be re-applied, since a (re)start gives
// the container a new veth pair.
func (p *Podman) reshape(ctx context.Context, name string) error {
	p.mu.Lock()
	delete(p.shaped, name)
	p.mu.Unlock()
	return p.Maintain(ctx, name)
}

// Maintain applies the container's bandwidth limits when its host veth has
// changed since they were last applied, e.g. after podman-restart brought it
// up on boot without the agent.
func (p *Podman) Maintain(ctx context.Context, name string) error {
	var inspect struct {
		State struct {
			Status string `json:"Status"`
			Pid    int    `json:"Pid"`
		} `json:"State"`
		Config struct {
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	if err := p.request(ctx, http.MethodGet, containerPath(name)+"/json", nil, &inspect); err != nil {
		return err
	}
	down, _ := strconv.Atoi(inspect.Config.Labels[labelDown])
	up, _ := strconv.Atoi(inspect.Config.Labels[labelUp])
	if inspect.State.Status != "running" || inspect.State.Pid == 0 || (down <= 0 && up <= 0) {
		return nil
	}
	index, err := hostPeerIndex(ctx, p.run, inspect.State.Pid)
	if err != nil {
		return fmt.Errorf("find veth of %s: %w", name, err)
	}
	iface, err := p.interfaceName(index)
	if err != nil {
		return fmt.Errorf("find veth of %s: %w", name, err)
	}
	p.mu.Lock()
	current := p.shaped[name]
	p.mu.Unlock()
	if current == iface {
		return nil
	}
	if err := (TC{Run: p.run}).Apply(ctx, iface, down, up); err != nil {
		return fmt.Errorf("limit bandwidth of %s: %w", name, err)
	}
	p.mu.Lock()
	p.shaped[name] = iface
	p.mu.Unlock()
	return nil
}

func staticIPs(spec RuntimeSpec) []string {
	result := []string{spec.IPv4.String()}
	if spec.IPv6.IsValid() {
		result = append(result, spec.IPv6.String())
	}
	return result
}

// diskUsageTTL bounds how often the root file system is measured; libpod
// walks the container's layer to size it.
const diskUsageTTL = time.Minute

type diskSample struct {
	bytes int64
	at    time.Time
}

// diskUsage reports the container's root file system size (image plus
// changes), the figure `df /` shows inside it when a quota is set. It is
// measured at most once per diskUsageTTL; failures keep the last value.
func (p *Podman) diskUsage(ctx context.Context, name string) int64 {
	p.diskMu.Lock()
	sample, ok := p.disk[name]
	p.diskMu.Unlock()
	if ok && time.Since(sample.at) < diskUsageTTL {
		return sample.bytes
	}
	var inspect struct {
		SizeRw     int64 `json:"SizeRw"`
		SizeRootFs int64 `json:"SizeRootFs"`
	}
	measureCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	if err := p.request(measureCtx, http.MethodGet, containerPath(name)+"/json?size=true", nil, &inspect); err != nil {
		return sample.bytes
	}
	size := inspect.SizeRootFs
	if size <= 0 {
		size = inspect.SizeRw
	}
	p.diskMu.Lock()
	p.disk[name] = diskSample{bytes: size, at: time.Now()}
	p.diskMu.Unlock()
	return size
}
