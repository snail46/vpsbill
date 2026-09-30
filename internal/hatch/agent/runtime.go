package agent

import (
	"context"
	"errors"
	"io"
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
	// Terminal starts an interactive root login shell with a PTY.
	Terminal(ctx context.Context, name string, cols, rows int) (TerminalSession, error)
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
	// DiskIO caps the instance's disk; Devices are the disks it applies
	// to, as "major:minor".
	DiskIO  protocol.DiskIO
	Devices []string
}

// IOLimitChecker is a runtime whose storage may not enforce disk I/O
// limits; CheckIOLimit explains why when it cannot.
type IOLimitChecker interface {
	CheckIOLimit(ctx context.Context) error
}

// TerminalSession is an interactive shell: Read returns output and Write
// sends keystrokes.
type TerminalSession interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
}

// shellCommand prefers a bash login shell and falls back to sh.
var shellCommand = []string{"/bin/sh", "-c", "cd /root 2>/dev/null; if command -v bash >/dev/null 2>&1; then exec bash -l; else exec sh -l; fi"}

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
// Stock images from images.linuxcontainers.org (the /cloud variants too)
// ship without an SSH server, so it installs one once the instance has a
// default route; when that fails the password still works on the console.
// sshd keeps the first value it reads, so the drop-in sorts before cloud
// images' 60-cloudimg-settings.conf (PasswordAuthentication no).
// netTuneScript stores $HATCH_SYSCTL as a sysctl.d file (applied at every
// boot by systemd or OpenRC) and applies it now.
const netTuneScript = `mkdir -p /etc/sysctl.d
printf '%s' "$HATCH_SYSCTL" > /etc/sysctl.d/60-hatch-net.conf
sysctl -p /etc/sysctl.d/60-hatch-net.conf >/dev/null 2>&1 || true`

// packagesScript installs the everyday tools customers expect (bash, curl,
// wget and friends) when the image lacks them; stock LXC images ship
// without curl and wget, and Alpine without bash. It waits for the network
// like passwordScript, only installs what is missing, and gives up quietly.
const packagesScript = `missing=""
for tool in bash curl wget nano less tar unzip; do
  command -v "$tool" >/dev/null 2>&1 || missing="$missing $tool"
done
[ -e /etc/ssl/certs/ca-certificates.crt ] || [ -e /etc/pki/tls/certs/ca-bundle.crt ] || missing="$missing ca-certificates"
[ -n "$missing" ] || exit 0
i=0
while [ $i -lt 30 ] && ! awk '$2 == "00000000" { found = 1 } END { exit !found }' /proc/net/route 2>/dev/null; do
  sleep 1; i=$((i + 1))
done
if command -v apt-get >/dev/null 2>&1; then
  export DEBIAN_FRONTEND=noninteractive
  apt-get -o DPkg::Lock::Timeout=120 -qq update >/dev/null 2>&1 || true
  apt-get -o DPkg::Lock::Timeout=120 -qq install -y --no-install-recommends $missing >/dev/null 2>&1 || true
  apt-get clean >/dev/null 2>&1 || true
elif command -v dnf >/dev/null 2>&1; then
  dnf -q install -y $missing >/dev/null 2>&1 || true
elif command -v yum >/dev/null 2>&1; then
  yum -q install -y $missing >/dev/null 2>&1 || true
elif command -v apk >/dev/null 2>&1; then
  apk add -q $missing >/dev/null 2>&1 || true
fi
exit 0
`

const passwordScript = `set -e
printf 'root:%s\n' "$HATCH_PASSWORD" | chpasswd
if [ ! -x /usr/sbin/sshd ] && ! command -v sshd >/dev/null 2>&1; then
  i=0
  while [ $i -lt 30 ] && ! awk '$2 == "00000000" { found = 1 } END { exit !found }' /proc/net/route 2>/dev/null; do
    sleep 1; i=$((i + 1))
  done
  if command -v apt-get >/dev/null 2>&1; then
    export DEBIAN_FRONTEND=noninteractive
    apt-get -o DPkg::Lock::Timeout=120 -qq update >/dev/null 2>&1 || true
    apt-get -o DPkg::Lock::Timeout=120 -qq install -y openssh-server >/dev/null 2>&1 || true
  elif command -v dnf >/dev/null 2>&1; then
    (dnf -q install -y openssh-server && systemctl enable --now sshd) >/dev/null 2>&1 || true
  elif command -v apk >/dev/null 2>&1; then
    (apk add -q openssh && rc-update add sshd default) >/dev/null 2>&1 || true
  fi
fi
if [ -f /etc/ssh/sshd_config ]; then
  mkdir -p /etc/ssh/sshd_config.d
  rm -f /etc/ssh/sshd_config.d/99-hatch.conf
  printf 'PermitRootLogin yes\nPasswordAuthentication yes\n' > /etc/ssh/sshd_config.d/00-hatch.conf
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

// StorageReporter is implemented by runtimes that keep instances in their
// own storage: its size counts as the host's disk capacity, and it must be
// able to enforce every instance's disk size.
type StorageReporter interface {
	// Storage returns the size and used bytes of the instance storage.
	Storage(ctx context.Context) (total, used int64, err error)
	// CheckDiskQuota fails when instance disk sizes cannot be enforced.
	CheckDiskQuota(ctx context.Context) error
}

// instanceSysctls are set in each new instance's network namespace: BBR
// (when the host kernel has it) copes far better than CUBIC with the lossy,
// long international paths NAT instances often sit behind, and MTU probing
// recovers from paths that drop large packets without telling anyone.
func instanceSysctls() map[string]string {
	sysctls := map[string]string{"net.ipv4.tcp_mtu_probing": "1"}
	if congestionAvailable("bbr") {
		sysctls["net.ipv4.tcp_congestion_control"] = "bbr"
	}
	return sysctls
}
