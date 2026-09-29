#!/bin/sh
# Install the Hatch agent on a Linux host node.
#
# Straight from the billing site (it bundles the matching agent build):
#
#   curl -fsSL https://billing.example.com/api/v1/agent/download/install.sh | \
#       sh -s -- --server https://billing.example.com --enroll KEY
#
# The billing site shows this command with your account's KEY filled in; the
# same command works on every host. After installing, the host shows up on
# the site as waiting to be added (hosting center "我的母机" for hosts, 节点对接
# for administrators).
#
# Or with a binary you downloaded yourself:
#
#   ./install-hatch-agent.sh --binary ./hatch-agent-linux-amd64 \
#       --server https://billing.example.com --runtime lxd
#
# Options:
#   --enroll KEY            account key from the billing site; lists the host
#                           there so nobody has to copy the agent token
#   --runtime LIST          lxd, incus and/or podman, comma separated; "auto"
#                           (default) uses Incus or LXD when installed, plus
#                           Podman (installed if missing)
#   --public-ip ADDRESS     the IPv4 customers reach; detected by default
#   --lxd-network NAME      bridge LXC instances attach to (lxdbr0 / incusbr0)
#   --podman-network NAME   Podman network for instances (created if missing)
#   --podman-disk SIZE      size of the Podman data disk, e.g. 20G; "auto"
#                           (default) uses the free space minus 2 GiB
#   --podman-images MODE    build (default) the Debian 12 and Alpine images
#                           that run in 64 MB, or skip
#   --no-zram               do not set up compressed swap in RAM
#   --no-tune               leave kernel network, conntrack and OOM settings as
#                           they are (see "Host tuning")
#   --download-from URL     fetch the agent from another billing address than
#                           --server (e.g. the public one while the agent uses
#                           a loopback URL)
#
# Every instance gets a hard disk size limit. For Podman that needs overlay on
# XFS with project quotas, so the script keeps Podman's store on an XFS file
# system in a preallocated file (/var/lib/hatch-podman.img); any host file
# system works. LXC needs a zfs, btrfs or lvm storage pool.
#
# Run as root.
set -eu

BINARY=""
SERVER=""
DOWNLOAD_FROM=""
RUNTIME="auto"
PUBLIC_IP=""
ENROLL=""
EXTRA=""
PODMAN_NETWORK="podman"
PODMAN_DISK="auto"
PODMAN_IMAGES="build"
ZRAM=1
TUNE=1

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --server) SERVER="$2"; shift 2 ;;
    --download-from) DOWNLOAD_FROM="$2"; shift 2 ;;
    --runtime) RUNTIME="$2"; shift 2 ;;
    --public-ip) PUBLIC_IP="$2"; shift 2 ;;
    --enroll) ENROLL="$2"; shift 2 ;;
    --lxd-network) EXTRA="$EXTRA $1 $2"; shift 2 ;;
    --podman-network) PODMAN_NETWORK="$2"; EXTRA="$EXTRA $1 $2"; shift 2 ;;
    --podman-disk) PODMAN_DISK="$2"; shift 2 ;;
    --podman-images) PODMAN_IMAGES="$2"; shift 2 ;;
    --no-zram) ZRAM=0; shift ;;
    --no-tune) TUNE=0; shift ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

[ "$(id -u)" = "0" ] || { echo "run as root" >&2; exit 1; }
[ -n "$SERVER" ] || { echo "--server is required" >&2; exit 2; }
command -v systemctl >/dev/null 2>&1 || { echo "systemd is required" >&2; exit 1; }

if [ "$RUNTIME" = "auto" ]; then
  if command -v incus >/dev/null 2>&1; then
    RUNTIME="incus,podman"
  elif command -v lxd >/dev/null 2>&1 || [ -S /var/snap/lxd/common/lxd/unix.socket ] || [ -S /var/lib/lxd/unix.socket ]; then
    RUNTIME="lxd,podman"
  else
    RUNTIME="podman"
  fi
  echo "Runtimes: $RUNTIME"
fi
case ",$RUNTIME," in *,podman,*) USE_PODMAN=1 ;; *) USE_PODMAN=0 ;; esac
case ",$RUNTIME," in *,lxd,*|*,incus,*) USE_LXC=1 ;; *) USE_LXC=0 ;; esac

fetch() {
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL -o "$2" "$1"
  else
    wget -qO "$2" "$1"
  fi
}

# apt_install refreshes the package lists once, since a fresh VPS image may
# ship without them.
APT_UPDATED=0
apt_install() {
  command -v apt-get >/dev/null 2>&1 || { echo "please install: $*" >&2; exit 1; }
  if [ "$APT_UPDATED" = 0 ]; then
    DEBIAN_FRONTEND=noninteractive apt-get update -q >/dev/null
    APT_UPDATED=1
  fi
  DEBIAN_FRONTEND=noninteractive apt-get install -y -q "$@" >/dev/null
}

# Minimal images (LXC templates, small VPS) often come without nftables.
command -v nft >/dev/null 2>&1 || apt_install nftables

# zram gives small hosts compressed swap in RAM; instances may swap up to
# their memory limit again, which lets 64 MB instances ride out peaks.
setup_zram() {
  if grep -q '^/dev/zram' /proc/swaps 2>/dev/null; then
    echo "zram swap already active"
    return
  fi
  if ! apt_install systemd-zram-generator 2>/dev/null; then
    echo "warning: systemd-zram-generator unavailable, skipping zram" >&2
    return
  fi
  if [ ! -f /etc/systemd/zram-generator.conf ]; then
    printf '[zram0]\nzram-size = ram / 2\ncompression-algorithm = zstd\n' > /etc/systemd/zram-generator.conf
  fi
  printf 'vm.swappiness = 100\n' > /etc/sysctl.d/90-hatch-zram.conf
  sysctl -q -p /etc/sysctl.d/90-hatch-zram.conf || true
  systemctl daemon-reload
  # Starting the swap unit waits until the device is set up and swapped on.
  systemctl start dev-zram0.swap 2>/dev/null || true
  grep -q '^/dev/zram' /proc/swaps && echo "zram swap enabled" || echo "warning: zram swap did not start" >&2
}

# ---- Host tuning -----------------------------------------------------------
# Sized from the host's memory. Values an admin already set higher are kept,
# the previous values are saved in /etc/hatch/tune-before.conf, and removing
# /etc/sysctl.d/90-hatch-tune.conf (plus the files named below) and
# rebooting undoes it. --no-tune skips all of it.
TUNE_CONF=/etc/sysctl.d/90-hatch-tune.conf
TUNE_KEYS="net.core.default_qdisc net.ipv4.tcp_congestion_control net.ipv4.tcp_mtu_probing net.netfilter.nf_conntrack_max net.netfilter.nf_conntrack_tcp_timeout_established net.netfilter.nf_conntrack_tcp_timeout_time_wait net.core.somaxconn net.core.netdev_max_backlog net.core.rmem_max net.core.wmem_max net.ipv4.tcp_rmem net.ipv4.tcp_wmem vm.min_free_kbytes"

# at_least KEY VALUE prints VALUE, or the current value when that is higher.
at_least() {
  CURRENT=$(sysctl -n "$1" 2>/dev/null | awk '{ print $NF }')
  case "$CURRENT" in
    ''|*[!0-9]*) echo "$2" ;;
    *) if [ "$CURRENT" -gt "$2" ]; then echo "$CURRENT"; else echo "$2"; fi ;;
  esac
}

# at_most KEY VALUE prints VALUE, or the current value when that is lower.
at_most() {
  CURRENT=$(sysctl -n "$1" 2>/dev/null || true)
  case "$CURRENT" in
    ''|*[!0-9]*) echo "$2" ;;
    *) if [ "$CURRENT" -lt "$2" ]; then echo "$CURRENT"; else echo "$2"; fi ;;
  esac
}

tune_host() {
  RAM_MB=$(awk '/^MemTotal:/ { print int($2 / 1024) }' /proc/meminfo)
  CONTAINER=0
  if systemd-detect-virt --container >/dev/null 2>&1; then CONTAINER=1; fi
  modprobe tcp_bbr 2>/dev/null || true
  modprobe nf_conntrack 2>/dev/null || true
  printf 'tcp_bbr\nnf_conntrack\n' > /etc/modules-load.d/hatch.conf

  # Each tracked connection costs about 300 bytes; 64 per MB of memory
  # keeps the table well under 2% of it. A small host's kernel default
  # (about 4096 entries on 256 MB) is filled by a single busy instance.
  CT_MAX=$((RAM_MB * 64))
  [ "$CT_MAX" -ge 16384 ] || CT_MAX=16384
  [ "$CT_MAX" -le 1048576 ] || CT_MAX=1048576
  if [ "$RAM_MB" -le 1024 ]; then BUF=4194304; BACKLOG=4096
  elif [ "$RAM_MB" -le 4096 ]; then BUF=16777216; BACKLOG=16384
  else BUF=33554432; BACKLOG=32768; fi
  # Keep 1/64 of memory (at most 64 MB) free for the kernel, so bursts do
  # not stall the host while it reclaims.
  MIN_FREE=$((RAM_MB * 16))
  [ "$MIN_FREE" -le 65536 ] || MIN_FREE=65536
  CC=cubic
  grep -qw bbr /proc/sys/net/ipv4/tcp_available_congestion_control 2>/dev/null && CC=bbr

  mkdir -p /etc/hatch
  if [ ! -f /etc/hatch/tune-before.conf ]; then
    for key in $TUNE_KEYS; do
      printf '%s = %s\n' "$key" "$(sysctl -n "$key" 2>/dev/null)"
    done > /etc/hatch/tune-before.conf
  fi
  cat > "$TUNE_CONF" <<EOF
# Written by the Hatch installer for a ${RAM_MB} MB host; see docs/HATCH-AGENT.md.
# Previous values: /etc/hatch/tune-before.conf
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = $CC
net.ipv4.tcp_mtu_probing = 1
net.netfilter.nf_conntrack_max = $(at_least net.netfilter.nf_conntrack_max "$CT_MAX")
net.netfilter.nf_conntrack_tcp_timeout_established = $(at_most net.netfilter.nf_conntrack_tcp_timeout_established 7200)
net.netfilter.nf_conntrack_tcp_timeout_time_wait = $(at_most net.netfilter.nf_conntrack_tcp_timeout_time_wait 30)
net.core.somaxconn = $(at_least net.core.somaxconn 4096)
net.core.netdev_max_backlog = $(at_least net.core.netdev_max_backlog "$BACKLOG")
net.core.rmem_max = $(at_least net.core.rmem_max "$BUF")
net.core.wmem_max = $(at_least net.core.wmem_max "$BUF")
net.ipv4.tcp_rmem = 4096 131072 $(at_least net.ipv4.tcp_rmem "$BUF")
net.ipv4.tcp_wmem = 4096 65536 $(at_least net.ipv4.tcp_wmem "$BUF")
vm.min_free_kbytes = $(at_least vm.min_free_kbytes "$MIN_FREE")
EOF
  FAILED=$(sysctl -p "$TUNE_CONF" 2>&1 >/dev/null | grep -c . || true)
  if [ "$FAILED" -gt 0 ]; then
    WHY=""
    [ "$CONTAINER" = 0 ] || WHY=" (the host is a container)"
    echo "note: $FAILED kernel settings could not be changed here$WHY; the rest apply" >&2
  fi

  # A bigger hash table keeps lookups fast at the higher limit.
  if [ -w /sys/module/nf_conntrack/parameters/hashsize ]; then
    echo $((CT_MAX / 4)) > /sys/module/nf_conntrack/parameters/hashsize 2>/dev/null || true
    echo "options nf_conntrack hashsize=$((CT_MAX / 4))" > /etc/modprobe.d/hatch-conntrack.conf
  fi

  # fq on the uplink shares it fairly between connections and paces BBR.
  # The default queueing is replaced (the kernel cannot delete it): a
  # multi-queue NIC gets a new mq whose queues pick fq up, others fq itself.
  # A custom one is left alone.
  UPLINK=$(ip -4 route show default 2>/dev/null | awk '{ for (i = 1; i < NF; i++) if ($i == "dev") { print $(i + 1); exit } }')
  if [ -n "$UPLINK" ]; then
    ROOT=$(tc qdisc show dev "$UPLINK" root 2>/dev/null | awk 'NR == 1 { print $2 }')
    case "$ROOT" in
      mq) tc qdisc replace dev "$UPLINK" root handle 1: mq 2>/dev/null || true ;;
      fq_codel|pfifo_fast|pfifo) tc qdisc replace dev "$UPLINK" root fq 2>/dev/null || true ;;
      fq|noqueue|'') ;;
      *) echo "note: $UPLINK uses $ROOT queueing; leaving it as it is" ;;
    esac
  fi

  # Under memory pressure the kernel should kill instance processes, not
  # the ways in: SSH and the container runtimes.
  for unit in ssh.service sshd.service incus.service lxd.service snap.lxd.daemon.service; do
    systemctl cat "$unit" >/dev/null 2>&1 || continue
    SCORE=-500
    case "$unit" in ssh*) SCORE=-900 ;; esac
    mkdir -p "/etc/systemd/system/$unit.d"
    printf '[Service]\nOOMScoreAdjust=%s\n' "$SCORE" > "/etc/systemd/system/$unit.d/hatch-oom.conf"
    PID=$(systemctl show -p MainPID --value "$unit" 2>/dev/null || true)
    [ "${PID:-0}" -gt 0 ] && echo "$SCORE" > "/proc/$PID/oom_score_adj" 2>/dev/null || true
  done
  systemctl daemon-reload

  # Without zram (not available, or --no-zram) a small host still gets some
  # swap, so a memory spike slows it down instead of killing processes.
  SWAP_KB=$(awk '/^SwapTotal:/ { print $2 }' /proc/meminfo)
  if [ "${SWAP_KB:-0}" = 0 ] && [ "$CONTAINER" = 0 ] && [ "$RAM_MB" -le 2048 ] && [ ! -e /var/lib/hatch-swap ]; then
    SIZE=$RAM_MB
    [ "$SIZE" -le 1024 ] || SIZE=1024
    if fallocate -l "${SIZE}M" /var/lib/hatch-swap 2>/dev/null && chmod 600 /var/lib/hatch-swap && mkswap -q /var/lib/hatch-swap >/dev/null && swapon /var/lib/hatch-swap; then
      grep -q '^/var/lib/hatch-swap ' /etc/fstab || echo '/var/lib/hatch-swap none swap sw 0 0' >> /etc/fstab
      echo "Created a ${SIZE} MB swap file"
    else
      rm -f /var/lib/hatch-swap
    fi
  fi

  echo "Host tuning: congestion $CC, fq, conntrack $(sysctl -n net.netfilter.nf_conntrack_max 2>/dev/null)"
}

PODMAN_MOUNT=/var/lib/hatch-podman
PODMAN_IMG=/var/lib/hatch-podman.img

# Podman can only cap a container's disk with overlay on XFS mounted with
# project quotas. The store moves onto such a file system, kept in a
# preallocated file so the host's own file system does not matter.
setup_podman_disk() {
  if findmnt -rno FSTYPE,OPTIONS "$PODMAN_MOUNT" 2>/dev/null | grep -q '^xfs .*prjquota'; then
    echo "Podman data disk already mounted at $PODMAN_MOUNT"
  else
    command -v mkfs.xfs >/dev/null 2>&1 || apt_install xfsprogs
    if [ -n "$(podman ps -aq 2>/dev/null)" ]; then
      echo "Podman already has containers in its current store; remove them before moving the store to $PODMAN_MOUNT" >&2
      exit 1
    fi
    SIZE="$PODMAN_DISK"
    if [ "$SIZE" = "auto" ]; then
      FREE=$(df -P -BG /var/lib | awk 'NR == 2 { sub("G", "", $4); print $4 }')
      SIZE=$((FREE - 2))
      [ "$SIZE" -ge 2 ] || { echo "only ${FREE} GiB free under /var/lib; need at least 4" >&2; exit 1; }
      SIZE="${SIZE}G"
    fi
    echo "Creating a $SIZE XFS data disk for Podman at $PODMAN_MOUNT"
    [ -e "$PODMAN_IMG" ] || fallocate -l "$SIZE" "$PODMAN_IMG"
    blkid "$PODMAN_IMG" >/dev/null 2>&1 || mkfs.xfs -q "$PODMAN_IMG"
    mkdir -p "$PODMAN_MOUNT"
    grep -q " $PODMAN_MOUNT " /etc/fstab || echo "$PODMAN_IMG $PODMAN_MOUNT xfs loop,prjquota,nofail 0 0" >> /etc/fstab
    mount "$PODMAN_MOUNT"
  fi
  mkdir -p /etc/containers "$PODMAN_MOUNT/storage"
  if ! grep -qs "graphroot = \"$PODMAN_MOUNT/storage\"" /etc/containers/storage.conf; then
    [ ! -f /etc/containers/storage.conf ] || cp /etc/containers/storage.conf /etc/containers/storage.conf.before-hatch
    cat > /etc/containers/storage.conf <<EOF
# Written by the Hatch installer: the store lives on an XFS file system with
# project quotas so every container's root file system has a size limit.
[storage]
driver = "overlay"
runroot = "/run/containers/storage"
graphroot = "$PODMAN_MOUNT/storage"
EOF
    podman system migrate >/dev/null 2>&1 || true
  fi
}

setup_podman_network() {
  podman network exists "$PODMAN_NETWORK" 2>/dev/null && return
  # Podman's default 10.88.0.0/16 often collides with existing bridges.
  # A second agent on the same host needs its own network, so take the next
  # free 10.89.N.0/24.
  N=0
  while [ "$N" -lt 255 ]; do
    if podman network create --subnet "10.89.$N.0/24" "$PODMAN_NETWORK" >/dev/null 2>&1; then
      echo "Created Podman network $PODMAN_NETWORK (10.89.$N.0/24)"
      return
    fi
    N=$((N + 1))
  done
  echo "Could not create Podman network $PODMAN_NETWORK" >&2
  exit 1
}

# The images keep only an init, sshd and basic tools so an idle instance
# stays well under 64 MB. Host keys are generated on first boot, so no two
# instances share them.
build_podman_images() {
  DIR="$WORK/images"
  mkdir -p "$DIR/debian12" "$DIR/alpine"
  cat > "$DIR/debian12/Containerfile" <<'EOF'
FROM docker.io/library/debian:12-slim
ENV container=podman
RUN apt-get update \
 && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv openssh-server iproute2 iputils-ping procps ca-certificates curl nano less \
 && apt-get clean && rm -rf /var/lib/apt/lists/* /var/log/*.log \
 && rm -f /etc/ssh/ssh_host_* \
 && printf '[Unit]\nDescription=Generate SSH host keys\nBefore=ssh.service\nConditionPathExists=!/etc/ssh/ssh_host_ed25519_key\n[Service]\nType=oneshot\nExecStart=/usr/bin/ssh-keygen -A\n[Install]\nWantedBy=multi-user.target\n' > /etc/systemd/system/ssh-hostkeys.service \
 && systemctl enable ssh ssh-hostkeys \
 && systemctl mask systemd-udevd.service systemd-udevd-kernel.socket systemd-udevd-control.socket \
      getty.target console-getty.service systemd-logind.service apt-daily.timer apt-daily-upgrade.timer \
      e2scrub_all.timer fstrim.timer systemd-tmpfiles-clean.timer \
 && mkdir -p /etc/systemd/journald.conf.d \
 && printf '[Journal]\nStorage=volatile\nRuntimeMaxUse=4M\n' > /etc/systemd/journald.conf.d/hatch.conf
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
EOF
  cat > "$DIR/alpine/Containerfile" <<'EOF'
FROM docker.io/library/alpine:3.22
RUN apk add --no-cache openrc openssh iproute2 procps-ng ca-certificates curl nano \
 && sed -i 's/^tty/#tty/' /etc/inittab \
 && sed -i -e 's/^#\?rc_sys=.*/rc_sys="docker"/' -e 's/^#\?rc_provide=.*/rc_provide="loopback net"/' /etc/rc.conf \
 && rm -f /etc/ssh/ssh_host_* \
 && rc-update add sshd default
STOPSIGNAL SIGUSR2
CMD ["/sbin/init"]
EOF
  for image in debian12 alpine; do
    echo "Building localhost/hatch-$image:latest"
    podman build --network host -t "localhost/hatch-$image:latest" "$DIR/$image" >"$WORK/build-$image.log" 2>&1 || {
      tail -20 "$WORK/build-$image.log" >&2
      echo "building the $image image failed; the host needs to reach docker.io and the distribution mirrors" >&2
      exit 1
    }
  done
}

WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
BASE="${DOWNLOAD_FROM:-$SERVER}"
BASE="${BASE%/}/api/v1/agent/download"

if [ "$USE_PODMAN" = 1 ]; then
  command -v podman >/dev/null 2>&1 || apt_install podman
  # lxcfs lets free, top and uptime inside a container show its own limits.
  if [ ! -e /var/lib/lxcfs/proc/meminfo ]; then
    command -v lxcfs >/dev/null 2>&1 || apt_install lxcfs
    # Debian's unit skips containers; a host that is itself a container
    # (common for small NAT servers) can still run it when FUSE is there.
    if systemd-detect-virt --container >/dev/null 2>&1 && [ -c /dev/fuse ]; then
      mkdir -p /etc/systemd/system/lxcfs.service.d
      printf '[Unit]\nConditionVirtualization=\n' > /etc/systemd/system/lxcfs.service.d/hatch.conf
      systemctl daemon-reload
    fi
    systemctl enable --now lxcfs >/dev/null 2>&1 && sleep 1 || true
  fi
  [ -e /var/lib/lxcfs/proc/meminfo ] || echo "warning: lxcfs unavailable; instances will see host memory in free" >&2
  setup_podman_disk
  systemctl enable --now podman.socket >/dev/null
  systemctl enable podman-restart.service >/dev/null 2>&1 || true
  setup_podman_network
  [ "$PODMAN_IMAGES" = "skip" ] || build_podman_images
fi
if [ "$USE_LXC" = 1 ]; then
  for cli in incus lxc; do
    command -v "$cli" >/dev/null 2>&1 || continue
    DRIVER=$("$cli" storage show default 2>/dev/null | awk '$1 == "driver:" { print $2 }')
    case "$DRIVER" in
      zfs|ceph) echo "note: $cli storage pool 'default' uses the $DRIVER driver, which ignores disk read/write limits; use btrfs or lvm if plans set them" >&2 ;;
      ""|btrfs|lvm|lvmcluster) ;;
      *) echo "warning: $cli storage pool 'default' uses the $DRIVER driver, which cannot limit instance disks; create a btrfs, zfs or lvm pool" >&2 ;;
    esac
    break
  done
fi
[ "$ZRAM" = 0 ] || setup_zram
[ "$TUNE" = 0 ] || tune_host

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

[ "$BINARY" -ef /usr/local/bin/hatch-agent ] || install -m 0755 "$BINARY" /usr/local/bin/hatch-agent
install -d -m 0700 /etc/hatch /var/lib/hatch

if [ ! -f /etc/hatch/agent.json ]; then
  /usr/local/bin/hatch-agent init --config /etc/hatch/agent.json \
    --server "$SERVER" --runtime "$RUNTIME" --public-ip "$PUBLIC_IP" --enroll "$ENROLL" $EXTRA
else
  echo "Keeping existing /etc/hatch/agent.json; token:"
  /usr/local/bin/hatch-agent token --config /etc/hatch/agent.json
  [ -z "$ENROLL" ] || /usr/local/bin/hatch-agent enroll --config /etc/hatch/agent.json "$ENROLL"
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
if [ "$USE_PODMAN" = 1 ] && [ "$PODMAN_IMAGES" != "skip" ]; then
  echo "Podman templates: localhost/hatch-debian12:latest and localhost/hatch-alpine:latest"
fi
if [ -n "$ENROLL" ]; then
  echo
  echo "安装完成：这台母机已出现在计费站的待接入列表（托管中心「我的母机」或后台「节点对接」），在网页上点「接入」即可，无需复制令牌。"
  echo "Done: this host is now listed on the billing site as waiting to be added."
fi
