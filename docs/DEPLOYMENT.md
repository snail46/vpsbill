# 私有仓库全新服务器部署

本文以 Debian 12/13 或 Ubuntu 22.04/24.04、单机 Docker Compose 和域名 HTTPS 为例。建议至少 2 vCPU、2 GB 内存和 20 GB 可用磁盘。生产环境应预先将域名 A/AAAA 记录指向服务器，并确保 API 容器能够访问所有 CLICD 节点。

## 1. 安装基础软件与 Docker

以下命令仅适用于官方 Debian/Ubuntu。它会先校验发行版，再使用 Docker 官方 apt 仓库；不要在 Debian 衍生版上直接把衍生版代号传给 Docker 仓库。对应官方说明：[Debian](https://docs.docker.com/engine/install/debian/)、[Ubuntu](https://docs.docker.com/engine/install/ubuntu/)。

```sh
sudo apt-get update
sudo apt-get install -y ca-certificates curl git openssl ufw
for pkg in docker.io docker-doc docker-compose podman-docker containerd runc; do sudo apt-get remove -y "$pkg" 2>/dev/null || true; done
sudo install -m 0755 -d /etc/apt/keyrings
OS_ID="$(. /etc/os-release && printf '%s' "$ID")"
OS_CODENAME="$(. /etc/os-release && printf '%s' "${UBUNTU_CODENAME:-$VERSION_CODENAME}")"
case "$OS_ID" in
  debian|ubuntu) ;;
  *) echo "仅支持 Debian 或 Ubuntu，当前为: $OS_ID"; exit 1 ;;
esac
sudo curl -fsSL "https://download.docker.com/linux/${OS_ID}/gpg" -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc
printf 'Types: deb\nURIs: https://download.docker.com/linux/%s\nSuites: %s\nComponents: stable\nArchitectures: %s\nSigned-By: /etc/apt/keyrings/docker.asc\n' "$OS_ID" "$OS_CODENAME" "$(dpkg --print-architecture)" | sudo tee /etc/apt/sources.list.d/docker.sources >/dev/null
sudo apt-get update
sudo apt-get install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin
sudo systemctl enable --now docker
sudo docker run --rm hello-world
sudo docker compose version
```

若输出“仅支持 Debian 或 Ubuntu”，请不要强行改 `OS_ID`；应按该发行版的 Docker 官方安装页操作，或改用 Debian/Ubuntu 服务器。若曾安装发行版自带的 `docker.io`/`podman-docker`，先按 Docker 官方“卸载冲突软件”小节清理后再执行上面命令。

后续示例使用 root 部署，避免 Docker socket 权限差异：

```sh
sudo -i
```

## 2. 为私有仓库创建只读 Deploy Key

不要把个人 GitHub 私钥或长期 Token 上传到服务器。为此仓库生成独立密钥：

```sh
install -d -m 700 /root/.ssh
ssh-keygen -t ed25519 -C "clicd-nat-production" -f /root/.ssh/clicd-nat-deploy -N ""
cat /root/.ssh/clicd-nat-deploy.pub
```

复制输出的整行公钥，进入 GitHub 仓库 `snail468/clicd-nat`：

1. `Settings` → `Deploy keys` → `Add deploy key`；
2. Title 填写服务器名称；
3. 粘贴公钥；
4. 不勾选 `Allow write access`，保存。

为该密钥创建专用 SSH 别名：

```sh
cat >/root/.ssh/config <<'EOF'
Host github-clicd-nat
  HostName github.com
  User git
  IdentityFile /root/.ssh/clicd-nat-deploy
  IdentitiesOnly yes
EOF
chmod 600 /root/.ssh/config
ssh -T git@github-clicd-nat
```

首次连接会要求确认 GitHub 主机指纹。确认指纹与 GitHub 公布的指纹一致后输入 `yes`。Deploy Key 验证成功时，GitHub 会说明认证成功但不提供 Shell，这是正常结果。

## 3. 克隆并初始化配置

```sh
mkdir -p /opt
cd /opt
git clone git@github-clicd-nat:snail468/clicd-nat.git
cd /opt/clicd-nat
chmod +x deploy.sh backup.sh restore.sh rotate-key.sh smoke-test.sh
./deploy.sh --init
nano .env
```

`--init` 只创建权限为 `600` 的 `.env`。脚本只生成容器启动不可缺少的三个随机密钥：`POSTGRES_PASSWORD`、`SESSION_SECRET`、`ENCRYPTION_KEY`。站点和业务参数不会再放入 `.env`。

使用域名时修改：

```dotenv
APP_PORT=127.0.0.1:8080
DOMAIN=billing.example.com
```

其中 `billing.example.com` 替换成真实域名。`APP_PORT` 必须绑定回环地址，外部流量统一从 Caddy 的 80/443 进入。

暂时没有域名、仅用于测试时可使用：

```dotenv
APP_PORT=8080
DOMAIN=
```

HTTP 模式不适合正式收款和保存客户信息。

不要改回任何 `CHANGE_ME_*` 值，也不要提交 `.env`。

## 4. 防火墙与首次启动

HTTPS 模式需放行 SSH、TCP 80/443 和 UDP 443。云厂商安全组也要同步放行。SSH 端口如果不是 22，应使用实际端口：

```sh
ufw allow 22/tcp
ufw allow 80/tcp
ufw allow 443/tcp
ufw allow 443/udp
ufw enable
```

确认域名解析已生效，然后部署：

```sh
cd /opt/clicd-nat
./deploy.sh
./smoke-test.sh
docker compose --env-file .env -f deploy/docker-compose.yml ps
```

首次构建通常需要几分钟。查看日志：

```sh
docker compose --env-file .env -f deploy/docker-compose.yml logs -f --tail=200
```

浏览器打开 `https://billing.example.com` 后进入一次性安装页。在页面中配置站点名称、公开 URL、时区、支付、通知、Metrics、自动化周期和唯一的初始超级管理员。留空的回调密钥与 Metrics Token 会安全生成并只显示一次；请立即保存。安装提交采用数据库事务，完成后不能再次访问安装入口。随后立即启用 TOTP 二步验证。客户入口为 `/portal`。

## 5. 接入 CLICD 节点

登录商家后台后添加区域、套餐和节点。节点需要：

- 管理网络可访问的 CLICD Base URL；
- 仅具备所需实例查询、创建、启动、停止和删除权限的 API Key；
- 正确的 LXC/KVM 类型、CPU、内存和磁盘容量。

先用测试套餐完成一次“下单 → 到账 → 自动开通 → 启停 → 对账”，再开放真实销售。支付回调和收银台协议分别见 `PAYMENT-WEBHOOK.md` 与 `PAYMENT-CHECKOUT.md`。

## 6. 备份、更新和恢复

首次上线后立即备份：

```sh
install -d -m 700 /var/backups/clicd
cd /opt/clicd-nat
./backup.sh /var/backups/clicd
```

数据库备份之外，必须把 `.env` 中的 `ENCRYPTION_KEY` 单独保存到密码管理器或离线保险库；缺少它将无法解密节点 API Key 和 TOTP 密钥。备份文件和 `.env` 不应只存放在同一台服务器。

更新版本：

```sh
cd /opt/clicd-nat
./backup.sh /var/backups/clicd
git pull --ff-only
./deploy.sh
./smoke-test.sh
```

恢复数据库：

```sh
cd /opt/clicd-nat
./restore.sh /var/backups/clicd/clicd-billing-YYYYMMDDTHHMMSSZ.dump --confirm
./smoke-test.sh
```

日常应监控 `/health/ready`、磁盘空间、容器重启次数、备份结果和自动化死信任务，并定期在隔离环境执行恢复演练。
