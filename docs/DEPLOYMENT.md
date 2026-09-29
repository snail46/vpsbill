# 全新服务器部署教程

本文从一台空的 Debian 12 服务器开始，把计费站和母鸡装在同一台机器上，走通「客户注册 → 下单 → 到账 → 自动开通 → SSH 登录 → 续费 / 逾期暂停 / 恢复 → 删除」。步骤在一台 4 核 4 GB、30 GB 磁盘、服务商 1:1 NAT、无域名、无 IPv6 的测试机上实际跑过。计费站和母鸡分开部署时，只有「Agent 连接地址」和「HTTPS」两处不同，文中会单独说明。

下文用 `203.0.113.10` 代表服务器公网 IP，用 `10.0.0.5` 代表网卡上的内网 IP（没有 1:1 NAT 的机器两者相同）。

## 0. 端口规划

| 用途 | 端口 | 说明 |
|---|---|---|
| 客户前台（HTTP） | 8088/tcp | 用域名或 Cloudflare Tunnel 时改为只监听本机，见 [访问方式](ACCESS.md) |
| 管理后台（HTTP） | 8089/tcp | 建议只对自己的 IP 放行 |
| Hatch 端口映射 | 20000–29999/tcp+udp | 写在 `/etc/hatch/agent.json` |
| LXDAPI 端口映射 | 40000–49999/tcp+udp | 写在 LXDAPI 的 NAT 配置和节点设置里 |
| LXDAPI 接口 | 8444/tcp | 只需要计费站能访问；同机部署时可以不对外开放 |

不同后端的端口段不能重叠。云厂商安全组要同时放行这些端口。

## 1. 安装 Docker

```sh
curl -fsSL https://get.docker.com | sh
docker compose version
```

## 2. 部署计费站

```sh
mkdir -p /opt/vpsbill && cd /opt/vpsbill
curl -fsSL -o docker-compose.yml https://raw.githubusercontent.com/snail46/vpsbill/main/deploy/docker-compose.image.yml
umask 077
cat > .env <<EOF
IMAGE_TAG=latest
ACCESS_MODE=direct
PORTAL_PORT=8088
ADMIN_PORT=8089
POSTGRES_DB=vpsbill
POSTGRES_USER=vpsbill
POSTGRES_PASSWORD=$(openssl rand -hex 24)
SESSION_SECRET=$(openssl rand -hex 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
docker compose up -d --pull always
curl -fsS http://127.0.0.1:8088/health/ready
```

把 `.env` 里的 `ENCRYPTION_KEY` 另存到密码管理器。丢了它，节点密钥、实例 root 密码和二步验证都无法解密。

## 3. 图形化安装

浏览器打开后台地址 `http://203.0.113.10:8089`，首次访问进入安装页：

- 公开 URL 填客户前台地址 `http://203.0.113.10:8088`（有域名时填 `https://前台域名`）；
- 填写初始超级管理员的邮箱和密码。

平台的日期和月份一律按 UTC+8（北京时间）计算，不需要设置时区。

提交后，商家后台在后台地址的 `/admin`，客户中心在前台地址。安装完成后到「站点设置」填上**后台访问地址** `http://203.0.113.10:8089`。登录后台后建议先在「安全中心」开启 TOTP 二步验证；站点名称、Logo、公开地址、续费周期和发信邮箱以后都可以在「站点设置」里修改。Logo 可以上传 SVG、PNG、JPG、WebP、GIF 或 ICO 图片（不超过 512 KB，存在数据库里），也可以填图片地址，会显示在前台和后台左上角、登录页和浏览器标签上。

## 4. 准备母鸡（任选一种或几种）

### 4A. Hatch + Incus（LXC 系统容器）

安装 Incus（Zabbly 官方源，Debian 12/13、Ubuntu 22.04/24.04 通用）：

```sh
mkdir -p /etc/apt/keyrings
curl -fsSL https://pkgs.zabbly.com/key.asc -o /etc/apt/keyrings/zabbly.asc
cat > /etc/apt/sources.list.d/zabbly-incus-stable.sources <<EOF
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/stable
Suites: $(. /etc/os-release && echo ${VERSION_CODENAME})
Components: main
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/zabbly.asc
EOF
apt-get update && apt-get install -y incus btrfs-progs
```

建存储池、网桥和镜像（已有 Incus 的机器跳过已存在的部分，名字可以自己换）：

```sh
incus storage create default btrfs size=20GiB
incus network create hatchbr0 ipv4.address=10.77.0.1/24 ipv4.nat=true ipv6.address=none
incus image copy images:debian/12/cloud local: --alias debian12-cloud
incus image copy images:ubuntu/24.04/cloud local: --alias ubuntu2404-cloud
```

镜像别名就是套餐里的系统模板 ID。`images:` 官方镜像不带 SSH 服务端，Agent 首次设置密码时会自动安装 `openssh-server`，实例需要能访问软件源。

### 4B. Hatch + Podman（OCI 容器）

Podman 不用手动准备：下一步的安装脚本带 `--runtime podman`（或 `incus,podman`）时会自动安装 Podman，在 `/var/lib/hatch-podman.img` 建 XFS 数据盘（开启项目配额，每台实例都有硬盘上限），创建网络，并构建两个最小镜像：

- `localhost/hatch-debian12:latest`：Debian 12 + systemd + sshd；
- `localhost/hatch-alpine:latest`：Alpine + OpenRC + sshd。

脚本还会安装 lxcfs，实例里的 `free`、`top`、`uptime` 显示的是实例自己的限额，而不是母机的。

两个镜像空闲时只占几 MB 内存，**1 核 / 64 MB / 1 GB** 的套餐可以正常开机和 SSH 登录。套餐的系统模板 ID 填这两个名字。数据盘大小用 `--podman-disk 20G` 指定，默认是剩余空间减 2 GiB（空间会预先占用）。已有 Podman 容器的机器需要先删除容器再迁移存储。

### 4C. 安装 Hatch Agent

4A、4B 选完后装 Agent。计费站和母鸡在同一台机器上时，Agent 走回环地址：

```sh
curl -fsSL http://127.0.0.1:8088/api/v1/agent/download/install.sh | sh -s -- \
  --server http://127.0.0.1:8088 --enroll <接入码> \
  --lxd-network hatchbr0 --podman-network hatchpod --podman-disk 20G
```

接入码在后台「节点对接 → 接入教程」的命令里（外部母机直接复制那条命令即可，服务器地址也已填好）。公网 IP 和已安装的 Incus / LXD 自动识别。

- 脚本默认开启 zram（一半内存做压缩交换），小内存母机更稳；不需要时加 `--no-zram`。
- 母机最低配置：只跑 Podman 时 1 核 / 256 MB 内存可以运行（实测：Agent、Podman 和系统空闲时共用约 30 MB，两台 64 MB 实例同时运行正常；单个实例内存超限只会杀掉该实例内的进程，母机和其他实例不受影响），硬盘建议 10 GB 起（Podman 数据盘、两个基础镜像和系统）。宿主机本身是容器（LXC 等小 NAT 机）时，需要能使用 /dev/fuse 和 loop 设备，否则实例内 `free` 看到的是宿主机内存，且无法建立带配额的 Podman 数据盘；跑 LXD/Incus 建议 1 GB 内存以上，存储池用 btrfs 比 zfs 省内存（ZFS 缓存会占用不少内存）。

- 只想用其中一种时，加 `--runtime incus` 或 `--runtime podman`（用 LXD snap 时写 `lxd`）。
- 母鸡在另一台机器上时，`--server` 必须是 `https://计费域名`，下载地址同理。
- 同机还有 LXDAPI 等 NAT 面板时，编辑 `/etc/hatch/agent.json` 把 `port_range_start`/`port_range_end` 改为 `20000`/`29999`，然后 `systemctl restart hatch-agent`。

装好后约半分钟，母机出现在后台「节点对接」的待接入列表里。

### 4D. LXDAPI

按 [xkatld/lxdapi-web-server](https://github.com/xkatld/lxdapi-web-server) 的 `Shell/` 目录依次运行 `lxd_install.sh`、`lxdapi_install.sh`、`image_import.sh`。装好后在 LXDAPI 后台：

1. 「NAT 配置」：网卡 IP 填 `10.0.0.5`，显示 IP 填 `203.0.113.10`，网卡填出口网卡（如 `eth0`），端口段 `40000–49999`，开启「自动分配 22 端口」；
2. 记下 API Hash（证书指纹不用手动取，接入时点「自动读取」）；

3. 导入的 `ubuntu-2404-lxc` 镜像默认禁止密码 SSH，按 [母鸡对接层](PROVIDERS.md#lxdapi-说明) 里的命令修正一次。

## 5. 后台接入节点和上架套餐

1. 「节点对接 → 新增节点对接」：
   - Hatch：在「接入教程 → 待接入的母机」里点「接入」，填名称和地域（下拉选择或直接输入新地域，自动创建），虚拟化类型已按母机运行时预选；
   - LXDAPI：「接入教程 → LXDAPI → 新增 LXDAPI 节点」，接口地址 `https://203.0.113.10:8444`，填 API Hash、可售镜像别名、可分配的 vCPU / 内存 / 磁盘；公网 IPv4 按接口地址自动填，出口网卡和端口段预填 `eth0`、`40000–49999`，1:1 NAT 时补「NAT 网卡 IP」（`10.0.0.5`），证书指纹点「自动读取」。
   提交时会实时连一次节点，失败会直接提示原因。节点以后可以「编辑」，没有未终止服务时可以「删除」。
2. 「商品套餐 → 创建新套餐」：选对接方式和虚拟化类型，填配置、各计费周期价格（月付、季付、半年付、年付和一个自定义周期，留空表示不卖该周期）和 NAT 端口映射配额，勾选允许的系统镜像（从在线节点读取），设默认镜像。

## 6. 全链路验收

用无痕窗口打开站点根路径，注册一个客户账号：

1. 「选购 VPS」下单，生成订单和账单；
2. 后台「账单与交易」点「确认到账并开通」（没接在线支付时只能人工确认）；
3. 半分钟到两分钟后，客户「我的 VPS」显示运行中，卡片上有 SSH 地址和端口，root 密码可以查看和复制；
4. 在自己电脑上 `ssh root@203.0.113.10 -p 端口` 登录；再试 WebSSH、开关机、重启、重置密码、重装系统和端口映射；
5. 续费链路（可选）：到期前 7 天生成续费账单；到期未付服务变为「已逾期」，客户仍可正常使用；宽限期（默认 72 小时）过后自动暂停（LXDAPI、Hatch 为冻结，内存不丢）；付款后自动恢复；
6. 后台「VPS 服务」可以对服务开关机、重启或立即终止，终止后容器、端口映射和节点资源一并释放。

## 7. 日常运维

更新：

```sh
cd /opt/vpsbill
curl -fsSL -o docker-compose.yml https://raw.githubusercontent.com/snail46/vpsbill/main/deploy/docker-compose.image.yml
docker compose up -d --pull always
curl -fsSL http://127.0.0.1:8088/api/v1/agent/download/install.sh | sh -s -- --server http://127.0.0.1:8088
```

第一条命令更新编排文件（新版本可能增加了卷，例如数据备份用的 `backups`），`.env` 不受影响；最后一条同步升级 Hatch Agent，已有配置会保留。

改用域名 HTTPS、Cloudflare Tunnel 或自己的反向代理：见 [访问方式](ACCESS.md)。改完后到后台「站点设置」更新公开访问地址和后台访问地址。

数据备份：后台「数据备份」可以设置定时备份（本地 + WebDAV），也可以从本地、上传的文件或 WebDAV 还原，详见 [运维手册](OPERATIONS.md)。主密钥轮换也在那里。

## 客户找回密码

两种方式，可以同时用：

- **邮件自助找回**：后台「站点设置 → 发信邮箱（SMTP）」填写服务器、端口、加密方式、发件人和账号密码，保存后点「发送测试邮件」确认能收到。之后客户登录页的「忘记密码？」会发送 30 分钟内有效的一次性重置链接。没配置 SMTP 时，这个页面会提示客户联系客服。
- **管理员生成链接**：后台「客户管理」对客户点「重置密码链接」，得到 24 小时内有效的一次性链接，通过工单等可信渠道发给客户本人。

重置成功后，该客户所有已登录的会话都会退出；同一客户新生成链接后，旧链接自动失效。

## 邮件通知

配好 SMTP 后，在「站点设置 → 邮件通知」按类型开关：

| 发给 | 通知 | 触发条件 |
|---|---|---|
| 客户 | 实例即将到期 | 续费账单未支付，距到期不足设定天数（默认 3 天）时一封，最后 24 小时再一封 |
| 客户 | 实例流量告警 | 本月流量达到阈值（默认 80%）时一封，用尽时再一封 |
| 客户 | 工单收到回复 | 客服回复（内部备忘不发） |
| 管理员 | 母鸡即将到期 | 距「节点对接」里填写的到期日不足设定天数（默认 7 天）、最后 24 小时、已到期各一封 |
| 管理员 | 母鸡流量告警 | 节点上实例本月流量合计达到节点「月流量限额」的阈值和 100% |
| 管理员 | 新工单与客户回复 | 客户新建工单或回复 |

- 管理员通知默认发给所有管理员的登录邮箱，也可以在「管理员通知邮箱」里指定多个地址。
- 到期和流量每 10 分钟检查一次，同一事件只发一封；流量按自然月统计。母鸡流量是节点上各实例流量之和，不含宿主机自身流量。
- 邮件先进入发送队列，SMTP 暂时不通时自动重试（最多 8 次，间隔逐步加长）。没配置 SMTP 时不会产生任何通知。
- 母鸡到期日和月流量限额在「节点对接 → 新增 / 编辑」里填写，节点列表会显示到期倒计时和本月流量。

## 工单图片附件

客户和管理员在新建工单、回复时都可以附带图片：每条消息最多 5 张，支持 PNG、JPEG、GIF、WebP，单张大小上限在「站点设置 → 工单附件」设置（默认 5 MB，最大 20 MB）。服务端按文件内容识别格式，改扩展名的其他文件会被拒绝。附件保存在数据库里，随数据库一起备份；管理员内部备忘里的图片客户看不到。

## 账户余额与托管中心

客户可以充值余额并用余额支付账单（充值前需勾选同意充值须知：余额只能用于平台消费，不能提现或原路退款）。托管中心让用户接入自己的 Hatch 母机出售实例，平台托管资金、按天结算、收取手续费，母鸡离线满 24 小时自动按 2 倍清退。「站点设置 → 托管中心」可以开关托管中心，调整手续费比例和离线清退时限。用户可以在「交易市场」转让持有满 31 天的实例（余额成交、不退款），管理员在后台「交易市场」可下架违规挂售。界面右上角可切换白天/夜间主题。管理员在「商品套餐」页底部管理平台优惠码，机主在「托管中心 → 优惠码」管理自己的优惠码。完整规则、资金计算、退款和后台操作见 [账户余额与托管中心](HOSTING.md)。

站点没有 HTTPS 时，只有与计费站同机的服务器能作为托管母机接入。

## 已知限制

- Podman 实例磁盘占用每分钟测量一次；
- LXDAPI 和 Hatch 没有历史监控曲线和 VNC；
- 在线支付未接入时只能后台人工确认到账；正式收款必须使用 HTTPS。
