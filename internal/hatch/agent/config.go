// Package agent implements the Hatch host agent: it keeps one outbound
// WebSocket to the billing server and turns typed requests into LXD or
// Podman operations, nftables port forwards and traffic accounting.
package agent

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

const DefaultConfigPath = "/etc/hatch/agent.json"

type Config struct {
	// ServerURL is the billing site, e.g. https://billing.example.com.
	ServerURL string `json:"server_url"`
	Token     string `json:"token"`
	// CAFile optionally trusts a private CA for the billing server.
	CAFile   string `json:"ca_file,omitempty"`
	StateDir string `json:"state_dir"`
	// PublicIPv4 is shown to customers as the address of their NAT ports.
	PublicIPv4     string `json:"public_ipv4"`
	PortRangeStart int    `json:"port_range_start"`
	PortRangeEnd   int    `json:"port_range_end"`
	// IPv6NDPInterface answers neighbour solicitations for instance IPv6
	// addresses on this uplink. Needed when the provider puts the /64 on-link
	// instead of routing it to the host; leave empty for a routed prefix.
	IPv6NDPInterface string `json:"ipv6_ndp_interface,omitempty"`
	// Capacity overrides detected host capacity when set.
	Capacity CapacityConfig `json:"capacity"`
	LXD      *LXDConfig     `json:"lxd,omitempty"`
	Podman   *PodmanConfig  `json:"podman,omitempty"`
}

type CapacityConfig struct {
	VCPU   int   `json:"vcpu,omitempty"`
	RAMMB  int64 `json:"ram_mb,omitempty"`
	DiskGB int64 `json:"disk_gb,omitempty"`
}

type LXDConfig struct {
	Socket      string `json:"socket"`
	Network     string `json:"network"`
	StoragePool string `json:"storage_pool"`
}

type PodmanConfig struct {
	Socket  string `json:"socket"`
	Network string `json:"network"`
	// DiskQuota passes a rootfs size limit, which needs overlay on XFS with
	// project quotas; leave off otherwise or container creation fails.
	DiskQuota bool `json:"disk_quota"`
}

func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("parse %s: %w", path, err)
	}
	config.applyDefaults()
	return config, config.Validate()
}

func (c *Config) applyDefaults() {
	if c.StateDir == "" {
		c.StateDir = "/var/lib/hatch"
	}
	if c.PortRangeStart == 0 && c.PortRangeEnd == 0 {
		c.PortRangeStart, c.PortRangeEnd = 20000, 60000
	}
	if c.LXD != nil {
		if c.LXD.Socket == "" {
			c.LXD.Socket = firstExisting("/var/snap/lxd/common/lxd/unix.socket", "/var/lib/lxd/unix.socket")
		}
		if c.LXD.Network == "" {
			c.LXD.Network = "lxdbr0"
		}
		if c.LXD.StoragePool == "" {
			c.LXD.StoragePool = "default"
		}
	}
	if c.Podman != nil {
		if c.Podman.Socket == "" {
			c.Podman.Socket = "/run/podman/podman.sock"
		}
		if c.Podman.Network == "" {
			c.Podman.Network = "podman"
		}
	}
}

func (c Config) Validate() error {
	parsed, err := url.Parse(c.ServerURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("server_url must be an http(s) URL")
	}
	if parsed.Scheme == "http" && !isLoopback(parsed.Hostname()) {
		return errors.New("server_url must use https except for loopback testing")
	}
	if len(c.Token) < 32 {
		return errors.New("token must be at least 32 characters; run hatch-agent init")
	}
	if c.LXD == nil && c.Podman == nil {
		return errors.New("enable at least one runtime (lxd or podman)")
	}
	if c.PortRangeStart < 1 || c.PortRangeEnd > 65535 || c.PortRangeStart > c.PortRangeEnd {
		return errors.New("invalid port range")
	}
	if c.PublicIPv4 != "" {
		if _, err := netip.ParseAddr(c.PublicIPv4); err != nil {
			return errors.New("public_ipv4 is not an IP address")
		}
	}
	return nil
}

// ConnectURL is the gateway WebSocket endpoint on the billing server.
func (c Config) ConnectURL() string {
	base := strings.TrimRight(c.ServerURL, "/")
	if strings.HasPrefix(base, "https://") {
		base = "wss://" + strings.TrimPrefix(base, "https://")
	} else {
		base = "ws://" + strings.TrimPrefix(base, "http://")
	}
	return base + "/api/v1/agent/connect"
}

// Runtimes lists the enabled virtualization keys, as used in plans.
func (c Config) Runtimes() []string {
	var result []string
	if c.LXD != nil {
		result = append(result, "lxc")
	}
	if c.Podman != nil {
		result = append(result, "podman")
	}
	return result
}

// NewToken returns a random 64-character hex agent token.
func NewToken() (string, error) {
	buffer := make([]byte, 32)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return hex.EncodeToString(buffer), nil
}

// WriteConfig stores the config readable by root only, since it holds the token.
func WriteConfig(path string, config Config) error {
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return writeFileAtomic(path, append(data, '\n'), 0o600)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	temp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	defer os.Remove(temp.Name())
	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Chmod(mode); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(temp.Name(), path)
}

func firstExisting(paths ...string) string {
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return paths[0]
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)
	return err == nil && addr.IsLoopback()
}
