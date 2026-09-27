# 全新服务器部署教程

本文从一台空的 Debian 12 服务器开始，把计费站和母鸡装在同一台机器上，走通「客户注册 → 下单 → 到账 → 自动开通 → SSH 登录 → 续费 / 逾期暂停 / 恢复 → 删除」。步骤在一台 4 核 4 GB、30 GB 磁盘、服务商 1:1 NAT、无域名、无 IPv6 的测试机上实际跑过。计费站和母鸡分开部署时，只有「Agent 连接地址」和「HTTPS」两处不同，文中会单独说明。

下文用 `203.0.113.10` 代表服务器公网 IP，用 `10.0.0.5` 代表网卡上的内网 IP（没有 1:1 NAT 的机器两者相同）。

## 0. 端口规划

| 用途 | 端口 | 说明 |
|---|---|---|
| 计费站（HTTP） | 8088/tcp | 有域名时改为 80、443 |
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
APP_PORT=8088
DOMAIN=
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

浏览器打开 `http://203.0.113.10:8088`，首次访问进入安装页：

- 公开 URL 填 `http://203.0.113.10:8088`（有域名时填 `https://域名`）；
- 时区选 `Asia/Shanghai`；
- 填写初始超级管理员的邮箱和密码。

提交后，商家后台在 `/admin`，站点根路径是客户中心。登录后台后建议先在「安全中心」开启 TOTP 二步验证；站点名称、公开地址、时区、续费周期和发信邮箱以后都可以在「站点设置」里修改。

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

```sh
apt-get install -y podman
systemctl enable --now podman.socket
systemctl enable podman-restart.service
podman network create --subnet 10.89.0.0/24 hatchpod
mkdir -p /root/podman-img
cat > /root/podman-img/Containerfile <<'EOF'
FROM docker.io/library/debian:12
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
      systemd systemd-sysv openssh-server iproute2 ca-certificates curl nano \
 && rm -rf /var/lib/apt/lists/* \
 && systemctl enable ssh \
 && systemctl mask systemd-udevd.service systemd-udevd-kernel.socket systemd-udevd-control.socket getty.target console-getty.service
STOPSIGNAL SIGRTMIN+3
CMD ["/sbin/init"]
EOF
podman build --network host -t localhost/debian12-ssh:latest /root/podman-img
```

套餐里的系统模板 ID 填 `localhost/debian12-ssh:latest`。Podman 默认网络是 `10.88.0.0/16`，容易和宿主机已有网桥冲突，所以这里另建了 `hatchpod`，构建时也用 `--network host`。

### 4C. 安装 Hatch Agent

4A、4B 选完后装 Agent。计费站和母鸡在同一台机器上时，Agent 走回环地址：

```sh
curl -fsSL http://127.0.0.1:8088/api/v1/agent/download/install.sh | sh -s -- \
  --server http://127.0.0.1:8088 --runtime incus,podman \
  --lxd-network hatchbr0 --podman-network hatchpod --public-ip 203.0.113.10
```

- 只用其中一种时，`--runtime` 写 `incus` 或 `podman`；用 LXD snap 时写 `lxd`。
- 母鸡在另一台机器上时，`--server` 必须是 `https://计费域名`，下载地址同理。
- 同机还有 LXDAPI 等 NAT 面板时，编辑 `/etc/hatch/agent.json` 把 `port_range_start`/`port_range_end` 改为 `20000`/`29999`，然后 `systemctl restart hatch-agent`。

脚本最后会打印一行 64 位令牌，下一步要用。

### 4D. LXDAPI

按 [xkatld/lxdapi-web-server](https://github.com/xkatld/lxdapi-web-server) 的 `Shell/` 目录依次运行 `lxd_install.sh`、`lxdapi_install.sh`、`image_import.sh`。装好后在 LXDAPI 后台：

1. 「NAT 配置」：网卡 IP 填 `10.0.0.5`，显示 IP 填 `203.0.113.10`，网卡填出口网卡（如 `eth0`），端口段 `40000–49999`，开启「自动分配 22 端口」；
2. 记下 API Hash，并取证书指纹：

   ```sh
   openssl s_client -connect 127.0.0.1:8444 </dev/null 2>/dev/null | openssl x509 -noout -fingerprint -sha256
   ```

3. 导入的 `ubuntu-2404-lxc` 镜像默认禁止密码 SSH，按 [母鸡对接层](PROVIDERS.md#lxdapi-说明) 里的命令修正一次。

## 5. 后台接入节点和上架套餐

1. 「节点对接 → 新增节点对接」：
   - Hatch：对接方式选 Hatch Agent，填令牌、地域代号（如 `SHA`）和中文名，勾选 LXC / Podman；
   - LXDAPI：接口地址 `https://203.0.113.10:8444`，填 API Hash、NAT 公网 IPv4、出口网卡、「NAT 网卡 IP」（1:1 NAT 时填 `10.0.0.5`）、端口段、可售镜像别名、可分配的 vCPU / 内存 / 磁盘和证书指纹。
   提交时会实时连一次节点，失败会直接提示原因。节点以后可以「编辑」，没有未终止服务时可以「删除」。
2. 「商品套餐 → 创建新套餐」：选对接方式和虚拟化类型，填配置、价格和 NAT 端口映射配额，勾选允许的系统镜像（从在线节点读取），设默认镜像。

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
docker compose up -d --pull always
curl -fsSL http://127.0.0.1:8088/api/v1/agent/download/install.sh | sh -s -- --server http://127.0.0.1:8088
```

第二条命令用来同步升级 Hatch Agent，已有配置会保留。

改用域名 HTTPS：把域名解析到服务器，`.env` 改为 `APP_PORT=127.0.0.1:8088`、`DOMAIN=billing.example.com`，执行 `docker compose --profile tls up -d`，再到后台「站点设置」把公开访问地址改成 `https://billing.example.com` 并保存，立即生效。

备份、恢复和主密钥轮换见 [运维手册](OPERATIONS.md)。

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

客户可以充值余额并用余额支付账单（余额不可提现）。托管中心让用户接入自己的 Hatch 母机出售实例，平台托管资金、按天结算、收取手续费，母鸡离线满 24 小时自动按 2 倍清退。「站点设置 → 托管中心」可以开关托管中心，调整手续费比例和离线清退时限。完整规则、资金计算和后台操作见 [账户余额与托管中心](HOSTING.md)。

站点没有 HTTPS 时，只有与计费站同机的服务器能作为托管母机接入。

## 已知限制

- Podman 实例磁盘占用每分钟测量一次；磁盘配额需要 XFS pquota 并开启 `disk_quota`；
- LXDAPI 和 Hatch 没有历史监控曲线和 VNC；
- 在线支付未接入时只能后台人工确认到账；正式收款必须使用 HTTPS。
