package agent

import (
	"context"
	"errors"
	"net/netip"

	"vpsbill/internal/hatch/protocol"
)

// ErrInstanceNotFound is returned by runtimes for unknown instance names.
var ErrInstanceNotFound = errors.New("instance not found")

// Runtime is one virtualization backend on the host.
type Runtime interface {
	// Virtualization is the plan key this runtime serves: "lxc" or "podman".
	Virtualization() string
	Images(ctx context.Context) ([]protocol.Image, error)
	// Network describes the bridge that static instance addresses are
	// allocated from.
	Network(ctx context.Context) (NetworkInfo, error)
	// Create creates and starts the instance with the given static address.
	Create(ctx context.Context, spec RuntimeSpec) error
	State(ctx context.Context, name string) (RuntimeState, error)
	Start(ctx context.Context, name string) error
	Stop(ctx context.Context, name string) error
	Restart(ctx context.Context, name string) error
	Pause(ctx context.Context, name string) error
	Resume(ctx context.Context, name string) error
	// Delete force-removes the instance; a missing instance is not an error.
	Delete(ctx context.Context, name string) error
	// Exec runs a shell script inside the instance and fails on a non-zero
	// exit status. Secrets are passed through env, never the command line.
	Exec(ctx context.Context, name, script string, env map[string]string) error
}

type RuntimeSpec struct {
	Name   string
	Image  string
	VCPU   int
	RAMMB  int
	DiskGB int
	IPv4   netip.Addr
	// IPv6 is the zero Addr when the instance has no IPv6 address.
	IPv6            netip.Addr
	NetworkDownMbps int
	NetworkUpMbps   int
}

// NetworkInfo is a runtime bridge. IPv6 is optional; when it is a routed
// public prefix the instances' IPv6 addresses are directly reachable.
type NetworkInfo struct {
	IPv4        netip.Prefix
	IPv4Gateway netip.Addr
	IPv6        netip.Prefix
	IPv6Gateway netip.Addr
}

// RuntimeState is a point-in-time observation. Counters are cumulative
// since the instance last started.
type RuntimeState struct {
	Status         string // running, stopped, paused or unknown
	IPv4           string
	CPUNanos       int64
	MemoryBytes    int64
	DiskBytes      int64
	RXBytes        int64
	TXBytes        int64
	DiskReadBytes  int64
	DiskWriteBytes int64
}

// passwordScript sets the root password and allows password SSH logins.
// It tolerates images without sshd so the password still works on the
// console.
const passwordScript = `set -e
printf 'root:%s\n' "$HATCH_PASSWORD" | chpasswd
if [ -d /etc/ssh ]; then
  mkdir -p /etc/ssh/sshd_config.d
  printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/99-hatch.conf
  if ! grep -qs '^Include /etc/ssh/sshd_config.d' /etc/ssh/sshd_config; then
    sed -i 's/^#\?PermitRootLogin.*/PermitRootLogin yes/; s/^#\?PasswordAuthentication.*/PasswordAuthentication yes/' /etc/ssh/sshd_config
  fi
  (systemctl restart ssh || systemctl restart sshd || rc-service sshd restart || service ssh restart) >/dev/null 2>&1 || true
fi
`

// Maintainer is implemented by runtimes whose host-side settings (such as
// bandwidth limits on a veth) are lost when an instance restarts without the
// agent; Maintain re-applies them and is called periodically.
type Maintainer interface {
	Maintain(ctx context.Context, name string) error
}
