#!/bin/sh
# Install the Hatch agent on a Linux host node.
#
#   ./install-hatch-agent.sh --binary ./hatch-agent-linux-amd64 \
#       --server https://billing.example.com --runtime lxd --public-ip 203.0.113.10
#
# Optional: --lxd-network NAME and --podman-network NAME pick the bridges that
# instances attach to (defaults: lxdbr0 or incusbr0, and podman).
#
# Run as root. The binary comes from the CI "hatch-agent" artifact; verify it
# against SHA256SUMS before installing.
set -eu

BINARY=""
SERVER=""
RUNTIME="lxd"
PUBLIC_IP=""
EXTRA=""

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --server) SERVER="$2"; shift 2 ;;
    --runtime) RUNTIME="$2"; shift 2 ;;
    --public-ip) PUBLIC_IP="$2"; shift 2 ;;
    --lxd-network|--podman-network) EXTRA="$EXTRA $1 $2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

[ "$(id -u)" = "0" ] || { echo "run as root" >&2; exit 1; }
[ -n "$BINARY" ] && [ -f "$BINARY" ] || { echo "--binary must point to the hatch-agent binary" >&2; exit 2; }
[ -n "$SERVER" ] || { echo "--server is required" >&2; exit 2; }
command -v nft >/dev/null 2>&1 || { echo "nftables (nft) is required" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }

install -m 0755 "$BINARY" /usr/local/bin/hatch-agent
install -d -m 0700 /etc/hatch /var/lib/hatch

if [ ! -f /etc/hatch/agent.json ]; then
  /usr/local/bin/hatch-agent init --config /etc/hatch/agent.json \
    --server "$SERVER" --runtime "$RUNTIME" --public-ip "$PUBLIC_IP" $EXTRA
else
  echo "Keeping existing /etc/hatch/agent.json; token:"
  /usr/local/bin/hatch-agent token --config /etc/hatch/agent.json
fi

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
install -m 0644 "$SCRIPT_DIR/hatch-agent.service" /etc/systemd/system/hatch-agent.service
systemctl daemon-reload
systemctl enable --now hatch-agent
systemctl --no-pager --lines=5 status hatch-agent || true
