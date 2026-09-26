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
- **单实例限制**：会话保存在 API 进程内存中。当前部署只有一个 API 容器；以后若水平扩展 API，需要把 Agent 请求路由到持有会话的实例。

## Agent 负责的事情

| 能力 | LXD | Podman |
|---|---|---|
| 创建（CPU/内存/磁盘限制、静态内网 IP） | ✅ | ✅（磁盘配额需 XFS pquota，需开启 `disk_quota`） |
| 带宽限速 | ✅ `limits.ingress/egress` | ✅ `tc`（宿主机 veth 上 tbf 限下行、ingress police 限上行） |
| 开关机、重启、暂停/恢复 | ✅ | ✅ |
| 重装（保留内网 IP 与端口映射） | ✅ | ✅ |
| root 密码设置 / 重置 | ✅ exec `chpasswd` | ✅ exec `chpasswd` |
| NAT 端口映射（TCP/UDP） | ✅ nftables | ✅ nftables |
| 实时用量（CPU/内存/网络/磁盘 IO） | ✅ | ✅（磁盘占用为 0） |
| 月流量累计（跨重启、按自然月清零） | ✅ | ✅ |
| WebSSH / VNC | ❌ 规划中 | ❌ 规划中 |
| IPv6 | ❌ 规划中 | ❌ 规划中 |

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
- **LXD**：网桥必须设置静态 `ipv4.address`（例如 `lxc network set lxdbr0 ipv4.address 10.20.30.1/24`）。Agent 从子网高位向下分配静态 IP，低位留给网桥 DHCP。在 LXD 中导入可售镜像并设置别名（`lxc image copy images:debian/12 local: --alias debian12`），别名就是套餐里的系统模板 ID。镜像需要带 sshd，推荐 `/cloud` 变体，或自行制作。
- **Podman**：启用 rootful API 套接字（`systemctl enable --now podman.socket`）和开机拉起（`systemctl enable podman-restart.service`）。网络默认使用 `podman`，也可以用 `podman network create` 另建。镜像需要以 systemd 等 init 为入口并带 sshd，否则无法当作 VPS 使用。
- 宿主机的 FORWARD 策略需要放行 DNAT 后的流量（Agent 自己的 forward 链已放行 `ct status dnat`）。

## 安装

1. 从 CI 的 `hatch-agent` 构件下载对应架构的二进制和 `SHA256SUMS`，并校验：

   ```sh
   sha256sum -c SHA256SUMS --ignore-missing
   ```

2. 安装并生成令牌：

   ```sh
   ./deploy/install-hatch-agent.sh --binary ./hatch-agent-linux-amd64 \
     --server https://billing.example.com --runtime lxd,podman --public-ip 203.0.113.10
   ```

   脚本会写入 `/etc/hatch/agent.json`，打印令牌，并启用 `hatch-agent.service`。

3. 在计费后台「节点对接 → 新增节点」选择 **Hatch Agent**，填入令牌、地区和虚拟化类型（`lxc` / `podman`）。接入时会实时调用 Agent 验证连接。

4. 新建套餐时选择对应的虚拟化类型（LXC 或 Podman），模板从在线节点的镜像中勾选。

轮换令牌：`hatch-agent init --force --server ...`，然后在后台用新令牌重新接入节点。

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
  "capacity": { "vcpu": 16, "ram_mb": 60000, "disk_gb": 900 },
  "lxd": { "socket": "/var/snap/lxd/common/lxd/unix.socket", "network": "lxdbr0", "storage_pool": "default" },
  "podman": { "socket": "/run/podman/podman.sock", "network": "podman", "disk_quota": false }
}
```

- `capacity` 不填时自动探测整机 CPU、内存和 `state_dir` 所在磁盘。建议按可售额度填写，给宿主机留余量。
- `server_url` 必须是 HTTPS（仅回环地址允许 HTTP，用于测试）。计费站点使用私有 CA 时，可以用 `ca_file` 指定。
- 删除 `lxd` 或 `podman` 段落即可禁用对应运行时。

## 反向代理

- 域名 + Caddy（`--profile tls`）：Caddy 直连 API，天然支持 WebSocket。
- 纯 HTTP 模式经过 `web` 容器的 nginx：`web/nginx.conf` 已为 `/api/v1/agent/connect` 开启 WebSocket 升级和长连接超时。
- API 进程每 30 秒对 Agent 发送一次 ping，自定义反向代理的读超时必须大于 30 秒。
