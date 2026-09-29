# Hatch Agent

Hatch 是 VPSBill 自研的宿主机 Agent。它运行在母鸡上，驱动本机的 **LXD**（系统容器）和 **Podman**（OCI 容器），由计费系统通过 `hatch` 对接方式统一管理。

## 架构

```
母鸡 (NAT 后也可)                         计费站点
┌──────────────────────────┐   wss   ┌──────────────────────────────┐
│ hatch-agent              │ ──────▶ │ /api/v1/agent/connect         │
│  ├─ LXD   /1.0 (unix)    │ Bearer  │  gateway.Hub（按令牌指纹分会话）│
│  ├─ Podman libpod (unix) │ ◀────── │  provider/hatch 驱动           │
│  └─ nftables  table ip hatch       │  开通 worker / 对账 / 客户中心  │
└──────────────────────────┘ 请求/响应└──────────────────────────────┘
```

- **Agent 主动连出**：宿主机不需要开放任何入站管理端口，也能在 NAT 后运行。
- **协议**：WebSocket + JSON 帧，定义在 `internal/hatch/protocol`。服务端只发送类型化请求（开通、电源、重装、改密、端口映射、用量、流量等），Agent 不执行任意命令。计费站点即使被攻破，也无法在宿主机上执行 shell。
- **身份**：Agent 令牌是 64 位十六进制随机数，只保存在 Agent 的 `/etc/hatch/agent.json`（0600）和计费库中（AES-256-GCM 加密）。节点的 `base_url` 存的是 `agent://<令牌 SHA-256 前 16 位>`，用于匹配会话，不暴露令牌。
- **未登记令牌**：尚未接入节点的 Agent 最多同时保持 8 个连接，15 分钟内未被接入即断开。已登记节点的 Agent 不受该上限影响。
- **多实例**：Agent 会话保存在它所连接的 API 进程中。部署多个 API 实例时设置 `INTERNAL_URL=auto`（或每个实例可互访的内部地址），实例会把持有的 Agent 登记到 `agent_sessions` 表并每 30 秒续期；其他实例收到针对该 Agent 的请求（含 WebSSH）时，通过内部端点 `/internal/v1/agent/` 转发给持有者。内部请求用由 `SESSION_SECRET`+`ENCRYPTION_KEY` 派生的 HMAC 签名，时间窗 60 秒，公网代理不转发 `/internal/`。单实例部署留空即可。

## Agent 负责的事情

| 能力 | LXD | Podman |
|---|---|---|
| 创建（CPU/内存/磁盘限制、静态内网 IP） | ✅（存储池须为 zfs/btrfs/lvm） | ✅（存储须在开启项目配额的 XFS 上，安装脚本自动创建） |
| 带宽限速 | ✅ `limits.ingress/egress` | ✅ `tc`（宿主机 veth 上 tbf 限下行、ingress police 限上行） |
| 开关机、重启、暂停/恢复 | ✅ | ✅ |
| 重装（保留内网 IP 与端口映射） | ✅ | ✅ |
| root 密码设置 / 重置 | ✅ exec `chpasswd` | ✅ exec `chpasswd` |
| NAT 端口映射（TCP/UDP） | ✅ nftables | ✅ nftables |
| 实时用量（CPU/内存/网络/磁盘 IO） | ✅ | ✅（磁盘占用每分钟测量一次） |
| 月流量累计（跨重启、按自然月清零） | ✅ | ✅ |
| WebSSH（客户中心网页终端） | ✅ 交互式 exec，经 Agent 连接复用传输 | ✅ TTY exec，经 Agent 连接复用传输 |
| IPv6（每实例一个独立地址） | ✅ 网桥 `ipv6.address` 子网内静态分配 | ✅ Podman 网络 IPv6 子网内静态分配 |

### 幂等与恢复

开通时 Agent 先把实例记录（含分配好的内网 IP）写入 `/var/lib/hatch/state.json`，再创建容器，然后设置密码，最后分配 SSH 端口映射。每一步都会先落盘；计费侧超时重试、Agent 崩溃或重启后，再次开通会从中断处继续，不会重复创建。同名但不是由 Hatch 创建的容器会被拒绝（`conflict`），不会被接管或覆盖。

### 端口映射

Agent 独占 nftables 的 `table ip hatch`，每次变更都整表原子替换：

```
fib daddr type local tcp dport 20022 dnat to 10.20.30.254:22
```

匹配发往本机任意地址的流量，所以宿主机自带公网 IP、云厂商 1:1 NAT 都适用。Agent 启动时会重放全部规则。如果 nft 拒绝了新规则集，这次变更会回滚。

## 宿主机准备

- Linux + systemd + nftables（`nft` 命令）。Podman 限速还需要 `tc` 和 `nsenter`（iproute2、util-linux）。
- **LXD**：网桥必须设置静态 `ipv4.address`（例如 `lxc network set lxdbr0 ipv4.address 10.20.30.1/24`）。Agent 从子网高位向下分配静态 IP，低位留给网桥 DHCP。在 LXD 中导入可售镜像并设置别名（`lxc image copy images:debian/12 local: --alias debian12`），别名就是套餐里的系统模板 ID。`images:` 上的官方镜像（包括 `/cloud` 变体）都不带 SSH 服务端；Agent 设置 root 密码时发现没有 sshd，会用镜像自带的包管理器（apt / dnf / apk）安装 `openssh-server`，所以实例需要能访问软件源，首次开通会多花半分钟左右。已经预装 sshd 的自制镜像不受影响。
- **Incus**：与 LXD 相同，网桥也要有静态 `ipv4.address`（例如 `incus network create hatchbr0 ipv4.address=10.77.0.1/24 ipv4.nat=true ipv6.address=none`）。安装 Agent 时用 `--runtime incus --lxd-network hatchbr0`。
- **Podman**：安装脚本（`--runtime podman`）会自动完成：安装 Podman，在一个预分配文件里建 XFS 数据盘并以项目配额（prjquota）挂载到 `/var/lib/hatch-podman`、把 Podman 存储移过去（任何宿主机文件系统都行，大小用 `--podman-disk 20G` 指定，默认剩余空间减 2 GiB）；启用 API 套接字和开机拉起；网络不存在时按 `10.89.0.0/24` 创建；构建两个最小镜像 `localhost/hatch-debian12:latest`（systemd）和 `localhost/hatch-alpine:latest`（OpenRC），空闲占用只有几 MB，**1 核 / 64 MB / 1 GB** 的实例可以正常运行和 SSH 登录。自带镜像需要以 init 为入口并带 sshd。
- **硬盘限额是强制的**：每台实例都有硬盘上限，不能开启时 Agent 拒绝创建实例，并在上报里标明原因，平台随即暂停该母机销售。LXD/Incus 的存储池必须是 zfs、btrfs 或 lvm（`dir` 无法限制）；Podman 必须是上面的 XFS 数据盘。
- **zram**（默认开启，`--no-zram` 关闭）：用一半内存做压缩交换，实例最多还能用与内存限额相同大小的交换，小内存母机更稳。
- 宿主机的 FORWARD 策略需要放行 DNAT 后的流量（Agent 自己的 forward 链已放行 `ct status dnat`）。
- **IPv6**（可选）：给网桥配置公网 IPv6 前缀，例如 `lxc network set lxdbr0 ipv6.address 2001:db8:1::1/64 ipv6.nat false`，或 `podman network create --ipv6 --subnet 2001:db8:2::/64 vps`。Agent 在前缀内随机分配地址，防止被顺序扫描。前缀最好由服务商路由到宿主机；如果服务商把 /64 直接放在网卡链路上（on-link），在配置里设置 `"ipv6_ndp_interface": "eth0"`，Agent 会开启 `proxy_ndp` 并为每个实例地址发布邻居代理。套餐勾选 IPv6 后，节点网桥必须有 IPv6 子网，否则开通会被拒绝。

## 安装

1. 在母鸡上以 root 执行。计费站的 API 镜像自带同版本的 Agent（amd64 / arm64），安装脚本会从 `/api/v1/agent/download/` 下载并按 `SHA256SUMS` 校验：

   ```sh
   curl -fsSL https://billing.example.com/api/v1/agent/download/install.sh | \
     sh -s -- --server https://billing.example.com --runtime lxd,podman --public-ip 203.0.113.10
   ```

   - `--runtime`：`lxd`、`incus`、`podman` 任意组合。宿主机用 Incus 时写 `incus`，Agent 会固定使用 `/var/lib/incus/unix.socket`；同一台机器同时装了 LXD snap 和 Incus 时必须这样写，否则自动探测会优先选中 LXD。
   - `--lxd-network`、`--podman-network`：实例接入的网桥 / Podman 网络，默认 `lxdbr0`（Incus 为 `incusbr0`）和 `podman`。
   - 计费站与母鸡是同一台机器时，`--server` 用 `http://127.0.0.1:端口`（只有回环地址允许 HTTP）。

   也可以自己下载二进制后运行仓库里的 `deploy/install-hatch-agent.sh --binary ./hatch-agent-linux-amd64 ...`，参数相同。

   脚本会写入 `/etc/hatch/agent.json`，打印令牌，并启用 `hatch-agent.service`。已有配置时保留原配置并重新打印令牌，所以同一条命令也用于升级 Agent。启动日志里的 `lxd_socket`、`lxd_network`、`podman_network` 是实际生效的值。

3. 在计费后台「节点对接 → 新增节点」选择 **Hatch Agent**，填入令牌、地区和虚拟化类型（`lxc` / `podman`）。接入时会实时调用 Agent 验证连接。

4. 新建套餐时选择对应的虚拟化类型（LXC 或 Podman），模板从在线节点的镜像中勾选。

轮换令牌：`hatch-agent init --force --server ...`，然后在后台用新令牌重新接入节点。

## 母机调优

安装脚本默认按母机内存自动调优（`--no-tune` 跳过）。管理员已经设得更高的值保留不动，调优前的值保存在 `/etc/hatch/tune-before.conf`。

| 项目 | 设置 | 解决的问题 |
|---|---|---|
| 连接跟踪表 | `nf_conntrack_max` = 内存 MB × 64（最少 16384，最多 1048576），哈希表为其 1/4；已建立连接超时 2 小时、TIME_WAIT 30 秒 | NAT 和端口转发都依赖它。内核默认按内存算，256 MB 母机只有约 4096 条，一台跑 P2P 或代理的实例就能占满，之后整台母机（包括 SSH）都建不了新连接 |
| 每实例连接数上限 | 实例内存 MB × 64（2048–65536），且不超过整表的 1/4，出入方向各算 | 单个实例占不满整张表，其他实例和母机不受影响。由 Agent 写进自己的 nftables 表 |
| 拥塞控制 | 母机和每个新实例都用 BBR（内核有 `tcp_bbr` 时） | 国际线路、丢包多的线路上速度明显更好。实例的 TCP 连接从实例自己的网络命名空间发出，所以 Agent 为每个新实例单独设置：Podman 写在创建参数里，LXC 实例写入实例内的 `/etc/sysctl.d/60-hatch-net.conf`（LXD 不接受 `linux.sysctl.*` 形式），客户可以自行修改 |
| 出口排队 | 出口网卡用 fq | 各连接公平分享上行带宽，一个实例的大流量不会拉高其他实例的延迟，同时给 BBR 做发包节奏控制。自定义的排队规则（htb、cake 等）不改 |
| MTU 探测 | `tcp_mtu_probing = 1`（母机和实例） | 某些线路丢大包又不回 ICMP 时，网页打不开、SSH 卡在登录，开启后自动退到小包 |
| 队列与缓冲 | `somaxconn` 4096；`netdev_max_backlog` 与 TCP 缓冲上限按内存分档（≤1 GB：4096 / 4 MB，≤4 GB：16384 / 16 MB，更大：32768 / 32 MB） | 突发流量不丢包，单连接高带宽传输不受缓冲限制；小内存母机不会因为缓冲过大挤占内存 |
| 内存保护 | Agent、SSH 的 OOM 分数 -900，Incus/LXD 守护进程 -500；`vm.min_free_kbytes` 为内存的 1/64（最多 64 MB） | 内存紧张时内核先杀实例里的进程，母机始终能登录和管理 |
| swap | 优先 zram（内存的一半）；zram 不可用且内存 ≤ 2 GB 时建一个与内存等大（最多 1 GB）的 swap 文件 | 内存尖峰时变慢而不是直接杀进程 |

回退：删除 `/etc/sysctl.d/90-hatch-tune.conf`、`/etc/modprobe.d/hatch-conntrack.conf` 和 `/etc/systemd/system/*/hatch-oom.conf` 后重启。

### 测试机上的实测对比

测试机：Debian 12（内核 6.1）、4 核 / 4 GB、云厂商虚拟盘，Podman 64 MB 实例。

| 场景 | 调优前 | 调优后 |
|---|---|---|
| 连接表耗尽：一台实例持有 6000 条已建立连接（表大小取 256 MB 母机的值：调优前默认约 4096，调优后 16384） | 其他实例建连 0/20 成功，内核丢包日志 544 条，母机 SSH 也连不上，持续到那台实例放开连接 | 该实例被限制在 4096 条，其他实例 20/20 成功（平均 2 ms），0 条丢包日志，母机 SSH 正常 |
| 母机直出下载，线路不丢包（RTT 42 ms，带宽约 26 Mbit/s） | CUBIC 3.20–3.29 MB/s | BBR 3.00–3.04 MB/s（慢约 7%），下载时 ping 两者都在 43–50 ms |
| 同上，模拟 1% 丢包 | CUBIC 0.18 MB/s | BBR 2.83 MB/s（约 16 倍） |
| 同上，模拟 3% 丢包 | CUBIC 0.085 MB/s | BBR 2.56 MB/s（约 30 倍） |

结论：连接跟踪表和每实例连接数上限直接防止"一台实例拖垮整台母机"，是稳定性上收益最大的一项；BBR 在不丢包的线路上略慢，但在丢包线路（国际、跨境）上决定了能不能用。

磁盘调度器没有列入调优：在这台机器上换成 BFQ 后，实例的小块同步写入没有变得更公平（负载下 0.54–0.76 秒对 mq-deadline 的 0.47–0.66 秒），空闲时反而更慢。云主机的磁盘瓶颈通常在宿主层面的 IOPS 限制上，客户机里的调度器管不到；需要隔离磁盘时，按实例设读写上限（cgroup `io.max`）才有效，实测给一台实例限速 20 MB/s 后其他实例的写入延迟回到空闲水平。套餐的「磁盘读写上限」就是这样实现的，见下一节。

## 磁盘读写上限

套餐可以给每台实例设磁盘读、写 MB/s 和读、写 IOPS 上限（留空不限）。Agent 把上限写到宿主机上每块磁盘（实例存储可能在 loop 文件、LVM 或第二块盘上）：Podman 通过创建参数里的 `blockIO`，LXC 通过 `raw.lxc` 里的 `lxc.cgroup2.io.max`（LXD 自带的 `limits.read`/`limits.write` 每个方向只能在 MB/s 和 IOPS 里选一个）。上限对新开通和重装的实例生效，已有实例不变。

**ZFS 存储池上不生效。** ZFS 由自己的内核线程写盘，cgroup 看不到是哪个容器写的：测试机上 Incus ZFS 池的实例限速 10 MB/s 后写入仍是 41.6 MB/s（不限时 43 MB/s）。测试机把 Incus 和 LXD 的存储池都换成 btrfs 后，由平台开通、套餐设读 20 MB/s、写 10 MB/s、读 500 IOPS 的实例实测：Incus 写 10.2 MB/s、读 20.2 MB/s、4 KiB 直读约 504 IOPS；LXD 写 8.1 MB/s、读 20.3 MB/s、约 500 IOPS（同池不限速的实例：写 60 MB/s、读 307 MB/s、约 2700 IOPS）。Agent 发现存储池是 zfs 或 ceph 时会上报，设置套餐时页面会提示；需要磁盘限速的母机请用 btrfs 或 lvm 存储池。Podman（overlay + XFS）不受影响。

IOPS 上限按磁盘实际收到的请求计，文件系统的元数据和日志也算在内：btrfs 上带 `dsync` 的 4 KiB 写入，每次约产生 13 个磁盘请求，限 100 IOPS 时实例里只能做约 8 次/秒。小内存实例的顺序写入同样受 IOPS 上限约束：内存小，写回时每次只刷几 KB。测试机上 64 MB 的 Podman 实例限 10 MB/s、300 写 IOPS 时，写 40 MB 只有 2.3 MB/s；去掉 IOPS 上限后写 100 MB 为 8.0 MB/s（接近 10 MB/s 的上限，也没有触发 OOM）。IOPS 上限宜宽松，主要用来防止单台实例把磁盘打满。

**建议值。** Agent 启动时在状态目录测一次磁盘（约 10 秒、最多 512 MB 的临时文件（不超过剩余空间的 1/10），O_DIRECT 加随机数据：1 MiB 顺序读写，4 KiB 随机读写 8 并发），结果保存在 `disk-perf.json`，30 天内重启不再测。云主机上虚拟化层的缓存会抬高测速（测试机上测出写约 600 MB/s，1 GB 持续直写约 340 MB/s），所以建议值偏宽松，更适合防止单台实例打满磁盘，而不是精确分配。设置套餐时，平台按母机实测值和这台母机能放下几台该套餐（按超售后的 CPU、内存、硬盘取最小）算出建议值：假定其中 1/4 同时读写、至少按 2 台算（每台最多占一半磁盘），取两位有效数字。多个节点时按最弱的一台算。

母机本身是容器（LXC、OpenVZ 的小 NAT 机）时，很多内核参数不能在里面修改，脚本会提示有几项没生效，其余照常；这种母机的连接跟踪表和磁盘调度由它的宿主决定。

## 配置参考（`/etc/hatch/agent.json`）

```json
{
  "server_url": "https://billing.example.com",
  "token": "<64 位十六进制>",
  "ca_file": "",
  "state_dir": "/var/lib/hatch",
  "public_ipv4": "203.0.113.10",
  "port_range_start": 20000,
  "port_range_end": 60000,
  "ipv6_ndp_interface": "",
  "nft_table": "hatch",
  "capacity": { "vcpu": 16, "ram_mb": 60000, "disk_gb": 900 },
  "lxd": { "socket": "/var/snap/lxd/common/lxd/unix.socket", "network": "lxdbr0", "storage_pool": "default" },
  "podman": { "socket": "/run/podman/podman.sock", "network": "podman" }
}
```

- Agent 自动检测整机 CPU、内存，磁盘取各运行时实例存储（LXD/Incus 存储池、Podman 数据盘）的总和。`capacity` 只能**调低**检测值（给宿主机留余量），填得比检测值高无效。超售在平台上按倍数设置，不在这里。
- Agent 还上报本机标识（`/etc/machine-id` 的哈希）和负载（负载、内存、交换、各存储用量）。同一台机器上的多个 Agent 会被识别出来，资源合并计算。另外 `port_range_start`/`port_range_end` 不要和同机其他 NAT 面板（如 LXDAPI）的端口段重叠。
- `server_url` 必须是 HTTPS（仅回环地址允许 HTTP，用于测试）。计费站点使用私有 CA 时，可以用 `ca_file` 指定。
- 删除 `lxd` 或 `podman` 段落即可禁用对应运行时。
- 同一台宿主机要运行两个 Agent（例如一个管 Incus、一个管 LXD snap，分别接入为两个节点）时，给第二个 Agent 单独的配置文件、`state_dir`、端口段和 `nft_table`（如 `"nft_table": "hatch_lxd"`），并复制一份 systemd 单元改用新配置、`ReadWritePaths` 指向新的 `state_dir`。`nft_table` 默认 `hatch`，两个 Agent 共用同一张表会互相覆盖端口转发。两个 Agent 都管 Podman 时，第二个还要用单独的 Podman 网络（安装时加 `--podman-network hatchpod2`，脚本会自动选一个空闲的 `10.89.N.0/24`）：每个 Agent 只知道自己分配过的内网地址，共用一个网络会分到同一个地址，实例开不起来。

## 反向代理

- 域名 + Caddy（`--profile tls`）：Caddy 直连 API，天然支持 WebSocket。
- 纯 HTTP 模式经过 `web` 容器的 nginx：`web/nginx.conf` 已为 `/api/v1/agent/connect` 开启 WebSocket 升级和长连接超时。
- API 进程每 30 秒对 Agent 发送一次 ping，自定义反向代理的读超时必须大于 30 秒。
