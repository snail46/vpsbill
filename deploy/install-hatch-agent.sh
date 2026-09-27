#!/bin/sh
# Install the Hatch agent on a Linux host node.
#
# Straight from the billing site (it bundles the matching agent build):
#
#   curl -fsSL https://billing.example.com/api/v1/agent/download/install.sh | \
#       sh -s -- --server https://billing.example.com --runtime lxd --public-ip 203.0.113.10
#
# Or with a binary you downloaded yourself:
#
#   ./install-hatch-agent.sh --binary ./hatch-agent-linux-amd64 \
#       --server https://billing.example.com --runtime lxd --public-ip 203.0.113.10
#
# Optional: --lxd-network NAME and --podman-network NAME pick the bridges that
# instances attach to (defaults: lxdbr0 or incusbr0, and podman).
# --download-from URL fetches the agent from another billing address than
# --server (for example the public one while the agent uses a loopback URL).
#
# Run as root.
set -eu

BINARY=""
SERVER=""
DOWNLOAD_FROM=""
RUNTIME="lxd"
PUBLIC_IP=""
EXTRA=""

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --server) SERVER="$2"; shift 2 ;;
    --download-from) DOWNLOAD_FROM="$2"; shift 2 ;;
    --runtime) RUNTIME="$2"; shift 2 ;;
    --public-ip) PUBLIC_IP="$2"; shift 2 ;;
    --lxd-network|--podman-network) EXTRA="$EXTRA $1 $2"; shift 2 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

[ "$(id -u)" = "0" ] || { echo "run as root" >&2; exit 1; }
[ -n "$SERVER" ] || { echo "--server is required" >&2; exit 2; }
command -v nft >/dev/null 2>&1 || { echo "nftables (nft) is required" >&2; exit 1; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  else
    wget -qO "$2" "$1"
  fi
}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
BASE="${DOWNLOAD_FROM:-$SERVER}"
BASE="${BASE%/}/api/v1/agent/download"

if [ -z "$BINARY" ]; then
  case "$(uname -m)" in
    x86_64|amd64) ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
  esac
  echo "Downloading hatch-agent-linux-$ARCH from $BASE"
  fetch "$BASE/hatch-agent-linux-$ARCH" "$WORK/hatch-agent-linux-$ARCH"
  fetch "$BASE/SHA256SUMS" "$WORK/SHA256SUMS"
  (cd "$WORK" && grep " hatch-agent-linux-$ARCH\$" SHA256SUMS | sha256sum -c -) || { echo "checksum mismatch" >&2; exit 1; }
  BINARY="$WORK/hatch-agent-linux-$ARCH"
fi
[ -f "$BINARY" ] || { echo "--binary must point to the hatch-agent binary" >&2; exit 2; }

install -m 0755 "$BINARY" /usr/local/bin/hatch-agent
install -d -m 0700 /etc/hatch /var/lib/hatch

if [ ! -f /etc/hatch/agent.json ]; then
  /usr/local/bin/hatch-agent init --config /etc/hatch/agent.json \
    --server "$SERVER" --runtime "$RUNTIME" --public-ip "$PUBLIC_IP" $EXTRA
else
  echo "Keeping existing /etc/hatch/agent.json; token:"
  /usr/local/bin/hatch-agent token --config /etc/hatch/agent.json
fi

# The unit file sits next to this script in the repository; when the script
# is piped from the billing site, fetch it from there too.
SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" 2>/dev/null && pwd || echo "")
UNIT="$SCRIPT_DIR/hatch-agent.service"
if [ -z "$SCRIPT_DIR" ] || [ ! -f "$UNIT" ]; then
  UNIT="$WORK/hatch-agent.service"
  fetch "$BASE/hatch-agent.service" "$UNIT"
fi
install -m 0644 "$UNIT" /etc/systemd/system/hatch-agent.service
systemctl daemon-reload
systemctl enable hatch-agent
systemctl restart hatch-agent
systemctl --no-pager --lines=5 status hatch-agent || true
