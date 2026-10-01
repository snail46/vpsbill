// Package provider defines the contract between the billing core and the
// host-node control planes (CLICD, LXDAPI, ...). Business code only depends
// on this package; each adapter lives in its own sub-package and registers a
// Factory under its provider type.
//
// Every driver implements Driver. Optional features are expressed as extra
// interfaces and discovered with a type assertion, so an adapter never has to
// stub out operations its backend cannot perform. CapabilitiesOf derives the
// feature flags shown to the UI from the same assertions.
package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"time"
)

// ErrNotFound is returned (possibly wrapped) when the instance does not exist
// on the node. Deletion treats it as success; reconciliation reports "missing".
var ErrNotFound = errors.New("instance not found on node")

// ErrUnsupported is returned when the driver lacks the requested feature.
var ErrUnsupported = errors.New("operation not supported by this provider")

// Error is a failure reported by the node itself. Message is safe to show to
// customers because it originates from the node's documented error envelope.
type Error struct {
	Provider   string
	StatusCode int
	Message    string
}

func (e *Error) Error() string {
	return fmt.Sprintf("%s api returned %d: %s", e.Provider, e.StatusCode, e.Message)
}

// Config is what the billing core stores per node. Credential is decrypted
// by the caller immediately before Open and is never persisted in plaintext.
type Config struct {
	BaseURL    string
	Credential string
	// Options holds the node's non-secret provider settings as a JSON
	// object; each adapter decodes it into its own typed struct.
	Options json.RawMessage
	Timeout time.Duration
}

// Driver is the minimum every provider must support to be sellable:
// discover capacity and images, create idempotently, observe, power, delete.
type Driver interface {
	// HostInfo returns the node's raw host document (kept for diagnostics)
	// plus the normalized allocatable capacity used by the scheduler.
	HostInfo(ctx context.Context) (HostInfo, error)
	Images(ctx context.Context) ([]Image, error)
	// EnsureInstance must be safe to retry: the spec name is the stable
	// identity, and an existing instance with that name is returned as-is.
	EnsureInstance(ctx context.Context, spec CreateSpec) (EnsureResult, error)
	GetInstance(ctx context.Context, name string) (Instance, error)
	// PowerAction accepts "start", "stop" or "restart" and returns the node's
	// task identifier when it provides one.
	PowerAction(ctx context.Context, name, action string) (string, error)
	// DeleteInstance treats a missing instance as success.
	DeleteInstance(ctx context.Context, name string) error
}

// Reconnector is implemented by drivers whose nodes connect in to the
// billing server. Reconnecting reports that the node is expected back
// shortly, so a failed call is not yet a sign that it is down.
type Reconnector interface {
	Reconnecting() bool
}

// Reconnecting reports whether driver's node is only briefly away.
func Reconnecting(driver Driver) bool {
	r, ok := driver.(Reconnector)
	return ok && r.Reconnecting()
}

type Reinstaller interface {
	Reinstall(ctx context.Context, name string, spec ReinstallSpec) (string, error)
}

type PasswordResetter interface {
	// ResetPassword sets password, or lets the node generate one when empty,
	// and returns the password now in effect.
	ResetPassword(ctx context.Context, name, password string) (string, error)
}

// PortMapper manages NAT port forwards. Mappings are addressed by their
// index in Instance.PortMappings.
type PortMapper interface {
	FreePort(ctx context.Context, name string) (int, error)
	AddPortMapping(ctx context.Context, name string, mapping PortMapping) ([]PortMapping, error)
	UpdatePortMapping(ctx context.Context, name string, index int, mapping PortMapping) ([]PortMapping, error)
	DeletePortMapping(ctx context.Context, name string, index int) ([]PortMapping, error)
}

// Metrics exposes per-instance monitoring documents, passed through to the
// customer UI. Adapters should emit these keys where the backend has them:
//
//	usage:   cpu_usage_pct, memory_usage_bytes, memory_total_bytes, disk_usage_bytes,
//	         network_rx_bps, network_tx_bps, disk_read_bps, disk_write_bps
//	traffic: total_used_bytes, rx_bytes, tx_bytes, limit_gb
//	history: a list of timestamped samples, or nil when unavailable
type Metrics interface {
	InstanceUsage(ctx context.Context, name string) (any, error)
	InstanceTraffic(ctx context.Context, name string) (any, error)
	InstanceHistory(ctx context.Context, name string) (any, error)
}

// Console brokers WebSSH / VNC sessions. Target is the node websocket that
// the billing server reverse-proxies to, so the node address and credential
// never reach the browser; the short-lived ticket authenticates the session.
type Console interface {
	ConsoleKinds() []string
	ConsoleTicket(ctx context.Context, name, kind, userAgent string) (string, error)
	ConsoleTarget(name, kind string) (*url.URL, error)
}

// Terminal opens an interactive root shell for backends whose console is not
// browser-compatible. The billing server bridges it to the customer's
// WebSocket using the same framing as the CLICD WebSSH proxy.
type Terminal interface {
	OpenTerminal(ctx context.Context, name string, cols, rows int) (TerminalSession, error)
}

// TerminalSession is a live shell: Read returns output, Write sends input.
type TerminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// HostProbe returns detailed host diagnostics keyed by section name, shown
// on the admin host-probe page.
type HostProbe interface {
	ProbeSections() []ProbeSection
}

type ProbeSection struct {
	Name string
	Load func(ctx context.Context) (any, error)
}

// Suspender pauses an instance without powering it off, for overdue services.
type Suspender interface {
	Suspend(ctx context.Context, name string) error
	Resume(ctx context.Context, name string) error
}

type HostInfo struct {
	Raw      map[string]any
	Capacity Capacity
	// MachineID identifies the physical machine when the backend knows it,
	// so nodes sharing one are not sold twice.
	MachineID string
	// Health is the host's load, when the backend reports it.
	Health *HostHealth
	// DiskPerf is the host's measured disk speed, when the backend reports it.
	DiskPerf *DiskPerf
}

// HostHealth is a load sample of the whole host.
type HostHealth struct {
	CPUs           int         `json:"cpus"`
	Load1          float64     `json:"load1"`
	Load5          float64     `json:"load5"`
	Load15         float64     `json:"load15"`
	MemTotalMB     int64       `json:"mem_total_mb"`
	MemAvailableMB int64       `json:"mem_available_mb"`
	SwapTotalMB    int64       `json:"swap_total_mb"`
	SwapFreeMB     int64       `json:"swap_free_mb"`
	Disks          []DiskUsage `json:"disks,omitempty"`
	// QuotaErrors names runtimes that cannot enforce instance disk sizes.
	QuotaErrors map[string]string `json:"quota_errors,omitempty"`
}

type DiskUsage struct {
	Name    string `json:"name"`
	TotalGB int64  `json:"total_gb"`
	UsedGB  int64  `json:"used_gb"`
}

// Capacity is the node's normalized allocatable capacity.
type Capacity struct {
	VCPU   int   `json:"vcpu"`
	RAMMB  int64 `json:"ram_mb"`
	DiskGB int64 `json:"disk_gb"`
}

type CreateSpec struct {
	Name             string
	Virtualization   string
	TemplateID       string
	VCPU             int
	RAMMB            int
	DiskGB           int
	AssignNAT        bool
	PortMappingCount int
	AssignIPv4       bool
	IPv4Count        int
	AssignIPv6       bool
	IPv6Count        int
	SSHAuthMode      string
	SSHPassword      string
	SSHPublicKey     string
	ExpiresAt        *time.Time
	NetworkDownMbps  int
	NetworkUpMbps    int
	MonthlyTrafficGB int
	SnapshotLimit    int
	DiskIO           DiskIO
}

// DiskIO limits an instance's disk; 0 leaves that direction unlimited.
// Only Hatch nodes enforce it.
type DiskIO struct {
	ReadMBps  int `json:"disk_read_mbps"`
	WriteMBps int `json:"disk_write_mbps"`
	ReadIOPS  int `json:"disk_read_iops"`
	WriteIOPS int `json:"disk_write_iops"`
}

// Limited reports whether any direction is capped.
func (d DiskIO) Limited() bool {
	return d.ReadMBps > 0 || d.WriteMBps > 0 || d.ReadIOPS > 0 || d.WriteIOPS > 0
}

// Valid rejects negative values and values beyond any real disk.
func (d DiskIO) Valid() bool {
	return d.ReadMBps >= 0 && d.WriteMBps >= 0 && d.ReadIOPS >= 0 && d.WriteIOPS >= 0 &&
		d.ReadMBps <= 100000 && d.WriteMBps <= 100000 && d.ReadIOPS <= 10000000 && d.WriteIOPS <= 10000000
}

// DiskPerf is a node's measured disk speed (sequential MB/s, random 4 KiB
// IOPS) and the runtimes on it that cannot enforce DiskIO limits.
type DiskPerf struct {
	ReadMBps      int               `json:"read_mbps"`
	WriteMBps     int               `json:"write_mbps"`
	ReadIOPS      int               `json:"read_iops"`
	WriteIOPS     int               `json:"write_iops"`
	MeasuredAt    time.Time         `json:"measured_at"`
	IOLimitErrors map[string]string `json:"io_limit_errors,omitempty"`
}

type ReinstallSpec struct {
	TemplateID string
	Password   string
}

type EnsureResult struct {
	Instance Instance
	Created  bool
}

// Instance is the provider-neutral view of a VPS. The JSON field names are
// part of the customer runtime API.
type Instance struct {
	ExternalID       string        `json:"id"`
	UUID             string        `json:"uuid"`
	Name             string        `json:"name"`
	Virtualization   string        `json:"virtualization"`
	Status           string        `json:"status"`
	Template         string        `json:"template"`
	IP               string        `json:"ip"`
	IPv6             string        `json:"ipv6"`
	VCPU             int           `json:"vcpu"`
	RAMMB            int           `json:"ram_mb"`
	DiskGB           int           `json:"disk_gb"`
	SSHPort          int           `json:"ssh_port"`
	PortMappings     []PortMapping `json:"port_mappings"`
	PortMappingLimit int           `json:"port_mapping_limit"`
	MonthlyTrafficGB int           `json:"monthly_traffic_gb"`
	NetworkDownMbps  int           `json:"network_down_mbps"`
	NetworkUpMbps    int           `json:"network_up_mbps"`
	// InitialPassword is the root password reported by the node after
	// creation. It is sealed into the database and never serialized.
	InitialPassword string `json:"-"`
}

type PortMapping struct {
	ContainerPort int    `json:"container_port"`
	HostPort      int    `json:"host_port"`
	HostIP        string `json:"host_ip,omitempty"`
	Protocol      string `json:"protocol"`
	Description   string `json:"description"`
}

// Image is a system template. Only Enabled && Downloaded images are sellable.
type Image struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Virtualization string `json:"type"`
	Distro         string `json:"distro"`
	Release        string `json:"release"`
	Arch           string `json:"arch"`
	Variant        string `json:"variant,omitempty"`
	Description    string `json:"description"`
	Downloaded     bool   `json:"downloaded"`
	Enabled        bool   `json:"enabled"`
}

// Sellable reports whether the image can be offered for the virtualization.
// An empty image type is treated as compatible with any virtualization.
func (i Image) Sellable(virtualization string) bool {
	return i.Enabled && i.Downloaded && (i.Virtualization == "" || virtualization == "" || i.Virtualization == virtualization)
}

type Capabilities struct {
	Reinstall     bool     `json:"reinstall"`
	ResetPassword bool     `json:"reset_password"`
	PortMapping   bool     `json:"port_mapping"`
	Metrics       bool     `json:"metrics"`
	Console       []string `json:"console"`
	HostProbe     bool     `json:"host_probe"`
	Suspend       bool     `json:"suspend"`
}

func CapabilitiesOf(driver Driver) Capabilities {
	var result Capabilities
	_, result.Reinstall = driver.(Reinstaller)
	_, result.ResetPassword = driver.(PasswordResetter)
	_, result.PortMapping = driver.(PortMapper)
	_, result.Metrics = driver.(Metrics)
	_, result.HostProbe = driver.(HostProbe)
	_, result.Suspend = driver.(Suspender)
	if console, ok := driver.(Console); ok {
		result.Console = console.ConsoleKinds()
	} else if _, ok := driver.(Terminal); ok {
		result.Console = []string{"ssh"}
	}
	if result.Console == nil {
		result.Console = []string{}
	}
	return result
}

// NormalizeStatus maps node-specific runtime states onto the billing core's
// vocabulary: running, stopped, suspended, creating or unknown.
func NormalizeStatus(status string) string {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "running", "started":
		return "running"
	case "stopped", "stopping":
		return "stopped"
	case "suspended", "paused", "frozen":
		return "suspended"
	case "creating", "pending":
		return "creating"
	default:
		return "unknown"
	}
}
