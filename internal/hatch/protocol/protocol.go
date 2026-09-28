// Package protocol defines the Hatch agent wire protocol.
//
// The agent dials the billing server (wss://<site>/api/v1/agent/connect)
// with "Authorization: Bearer <token>", so hosts behind NAT need no inbound
// port. After the upgrade both sides exchange JSON text frames:
//
//	agent  -> server  {"type":"hello","params":Hello}             once, first frame
//	server -> agent   {"type":"request","id":"…","method":"…","params":…}
//	agent  -> server  {"type":"response","id":"…","result":…}  or  "error":{code,message}
//	agent  -> server  {"type":"event","method":"heartbeat","params":Heartbeat}
//
// The server only ever sends the typed requests below; the agent executes
// nothing else, so a compromised billing server cannot run host commands.
package protocol

import (
	"encoding/json"
	"time"
)

// Version is bumped on incompatible wire changes.
const Version = 1

const (
	TypeHello    = "hello"
	TypeRequest  = "request"
	TypeResponse = "response"
	TypeEvent    = "event"
	// TypeStream frames carry an open terminal: ID is the stream, Method is
	// "data" (Params: base64 bytes), "resize" (Params: TerminalSize) or "close".
	TypeStream = "stream"
)

// Request methods. Params and results are the structs named in comments.
const (
	MethodHostInfo          = "host.info"          // -> HostInfo
	MethodImages            = "images.list"        // -> []Image
	MethodEnsure            = "instance.ensure"    // CreateSpec -> EnsureResult
	MethodGet               = "instance.get"       // NameParams -> Instance
	MethodPower             = "instance.power"     // PowerParams
	MethodDelete            = "instance.delete"    // NameParams
	MethodReinstall         = "instance.reinstall" // ReinstallParams
	MethodResetPassword     = "instance.password"  // PasswordParams -> PasswordParams
	MethodSuspend           = "instance.suspend"   // NameParams
	MethodResume            = "instance.resume"    // NameParams
	MethodUsage             = "instance.usage"     // NameParams -> map[string]any
	MethodTraffic           = "instance.traffic"   // NameParams -> map[string]any
	MethodFreePort          = "portmap.free"       // NameParams -> PortParams
	MethodAddPortMapping    = "portmap.add"        // PortMappingParams -> []PortMapping
	MethodUpdatePortMapping = "portmap.update"     // PortMappingParams -> []PortMapping
	MethodDeletePortMapping = "portmap.delete"     // PortMappingParams -> []PortMapping
	MethodConsoleOpen       = "console.open"       // ConsoleOpenParams; output then flows as stream frames
	EventHeartbeat          = "heartbeat"          // Heartbeat
)

// Stream methods.
const (
	StreamData   = "data"
	StreamResize = "resize"
	StreamClose  = "close"
)

// Error codes carried in Error.Code.
const (
	CodeNotFound    = "not_found"
	CodeUnsupported = "unsupported"
	CodeInvalid     = "invalid"
	CodeConflict    = "conflict"
	CodeInternal    = "internal"
)

type Frame struct {
	Type   string          `json:"type"`
	ID     string          `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *Error          `json:"error,omitempty"`
}

type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type Hello struct {
	ProtocolVersion int      `json:"protocol_version"`
	AgentVersion    string   `json:"agent_version"`
	Hostname        string   `json:"hostname"`
	Runtimes        []string `json:"runtimes"`
}

type Heartbeat struct {
	Time      time.Time `json:"time"`
	Instances int       `json:"instances"`
}

type Capacity struct {
	VCPU   int   `json:"vcpu"`
	RAMMB  int64 `json:"ram_mb"`
	DiskGB int64 `json:"disk_gb"`
}

type HostInfo struct {
	Hostname     string   `json:"hostname"`
	AgentVersion string   `json:"agent_version"`
	Runtimes     []string `json:"runtimes"`
	PublicIPv4   string   `json:"public_ipv4"`
	// Capacity is what the host offers: the detected hardware, lowered by
	// the operator's config. It never exceeds Detected.
	Capacity Capacity `json:"capacity"`
	Detected Capacity `json:"detected"`
	// MachineID is a hash of /etc/machine-id, so the server can tell when
	// several agents run on one machine and share its hardware.
	MachineID string      `json:"machine_id,omitempty"`
	Health    *HostHealth `json:"health,omitempty"`
	// QuotaErrors names runtimes that cannot enforce instance disk sizes;
	// they refuse to create instances until fixed.
	QuotaErrors map[string]string `json:"quota_errors,omitempty"`
	Details     map[string]any    `json:"details,omitempty"`
}

// HostHealth is a point-in-time load sample of the whole host.
type HostHealth struct {
	CPUs           int     `json:"cpus"`
	Load1          float64 `json:"load1"`
	Load5          float64 `json:"load5"`
	Load15         float64 `json:"load15"`
	MemTotalMB     int64   `json:"mem_total_mb"`
	MemAvailableMB int64   `json:"mem_available_mb"`
	SwapTotalMB    int64   `json:"swap_total_mb"`
	SwapFreeMB     int64   `json:"swap_free_mb"`
	// Disks is the instance storage of each runtime.
	Disks []DiskUsage `json:"disks,omitempty"`
}

type DiskUsage struct {
	Name    string `json:"name"`
	TotalGB int64  `json:"total_gb"`
	UsedGB  int64  `json:"used_gb"`
}

type Image struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	Virtualization string `json:"virtualization"`
	Description    string `json:"description,omitempty"`
}

type CreateSpec struct {
	Name             string `json:"name"`
	Virtualization   string `json:"virtualization"`
	TemplateID       string `json:"template_id"`
	VCPU             int    `json:"vcpu"`
	RAMMB            int    `json:"ram_mb"`
	DiskGB           int    `json:"disk_gb"`
	AssignNAT        bool   `json:"assign_nat"`
	PortMappingCount int    `json:"port_mapping_count"`
	AssignIPv6       bool   `json:"assign_ipv6"`
	Password         string `json:"password,omitempty"`
	NetworkDownMbps  int    `json:"network_down_mbps"`
	NetworkUpMbps    int    `json:"network_up_mbps"`
	MonthlyTrafficGB int    `json:"monthly_traffic_gb"`
}

type EnsureResult struct {
	Instance Instance `json:"instance"`
	Created  bool     `json:"created"`
}

type Instance struct {
	Name             string        `json:"name"`
	Virtualization   string        `json:"virtualization"`
	Status           string        `json:"status"`
	Template         string        `json:"template"`
	PrivateIPv4      string        `json:"private_ipv4"`
	PublicIPv4       string        `json:"public_ipv4"`
	IPv6             string        `json:"ipv6,omitempty"`
	VCPU             int           `json:"vcpu"`
	RAMMB            int           `json:"ram_mb"`
	DiskGB           int           `json:"disk_gb"`
	SSHPort          int           `json:"ssh_port"`
	PortMappings     []PortMapping `json:"port_mappings"`
	PortMappingLimit int           `json:"port_mapping_limit"`
	MonthlyTrafficGB int           `json:"monthly_traffic_gb"`
	NetworkDownMbps  int           `json:"network_down_mbps"`
	NetworkUpMbps    int           `json:"network_up_mbps"`
	// Password is only set in the EnsureResult of a newly created instance.
	Password string `json:"password,omitempty"`
}

type PortMapping struct {
	PublicPort    int    `json:"public_port"`
	ContainerPort int    `json:"container_port"`
	Protocol      string `json:"protocol"`
	Description   string `json:"description"`
}

type NameParams struct {
	Name string `json:"name"`
}

type PowerParams struct {
	Name   string `json:"name"`
	Action string `json:"action"`
}

type ReinstallParams struct {
	Name       string `json:"name"`
	TemplateID string `json:"template_id"`
	Password   string `json:"password"`
}

type PasswordParams struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

type PortParams struct {
	Port int `json:"port"`
}

type PortMappingParams struct {
	Name    string      `json:"name"`
	Index   int         `json:"index"`
	Mapping PortMapping `json:"mapping"`
}

type ConsoleOpenParams struct {
	Name   string `json:"name"`
	Stream string `json:"stream"`
	Cols   int    `json:"cols"`
	Rows   int    `json:"rows"`
}

type TerminalSize struct {
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}
