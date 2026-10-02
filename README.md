# VPSBill

面向 VPS 商家的财务计费、客户服务与自动化控制系统，通过统一的母鸡对接层（Provider）接入 CLICD、LXDAPI 和自研的 Hatch Agent（LXC / Podman）等节点后端。

## 当前状态

项目功能开发完成：包含客户门户、商家后台、订单账单、幂等支付、自动开通、续费逾期、工单通知、审计安全、备份恢复和生产部署。持续集成会执行单元测试、PostgreSQL 并发集成测试、前后端构建及 Docker 镜像构建。

## 一键部署

生产 Linux 主机安装 Docker Engine 和 Compose 插件后运行：

```sh
chmod +x deploy.sh
./deploy.sh
```

脚本首次运行会从 `.env.example` 生成 `.env`，只随机生成数据库密码、会话密钥和数据加密主密钥，然后构建并启动 PostgreSQL、API 和 Web 容器。客户前台和管理后台分开监听（默认 8080 和 8081），可以选择直接 IP 访问、Caddy 自动 HTTPS、Cloudflare Tunnel（无公网 IP）或自己的反向代理，见 [访问方式](docs/ACCESS.md)。

首次打开页面时会进入一次性安装向导，图形化设置站点、通知、Metrics、自动化周期和唯一的初始超级管理员。支付网关在登录后的「支付网关」菜单配置。节点 API Key 与支付密钥等敏感参数均使用 AES-256-GCM 主密钥加密保存。

站点根路径就是客户中心（`/portal/...`），商家后台在 `/admin`。客户身份与商家管理员身份在服务端分别校验；客户只能读取和操作所属账户的服务。

「我的 VPS」用小卡片列出实例（标题为套餐名、副标题为实例名，显示配置、带宽、系统、IP、到期和续费价格），按平台自营、托管市场和交易市场购入区分，可按来源筛选；点开实例进入详情页，提供开关机和重启（都要二次确认，完成后右上角提示结果）、同源代理的 WebSSH/KVM VNC（控制台可缩小到角落或全屏）、root 密码查看与重置、套餐白名单重装、IPv4 NAT 端口映射（TCP、UDP 或 TCP+UDP），以及每 10 秒刷新的 CPU、内存、磁盘、流量和 I/O 速率。初始及重置后的 root 密码使用 AES-256-GCM 加密，旧实例未留存密码时需先重置。所有实例默认开启**自动余额续费**（到期前 24 小时从余额扣款，余额不足时需手动支付），可在详情页随时关闭；托管实例的详情页有对应母机聊天室入口。「服务概览」展示实例、到期、账单、可用余额（可直接充值）、托管收益和平台公告，所有卡片可点击进入对应页面；管理员在后台「平台公告」发布和置顶公告。

在线支付默认关闭。登录商家后台后可交互式启用支付宝当面付（官方 `alipay.trade.precreate` / RSA2）、兼容彩虹易支付协议的易支付，或通用 HMAC 外部收银台；密钥留空会保留已有值且永不回显。启用后页面会显示对应异步回调地址，正式收款必须使用公开 HTTPS 地址。未配置时仅允许后台人工确认到账。

平台所有日期和月份（流量周期、优惠码到期、页面和邮件里的时间）一律按 UTC+8 计算。后台「站点设置」可随时修改站点名称、公开访问地址、续费与自动化周期、通知 Webhook、发信邮箱（SMTP）、工单附件大小，以及客户前台「联系我们」页面的联系方式（TG 客服、TG 群、QQ 群、邮箱等，按填写顺序显示）。配置 SMTP 后：客户可在登录页通过邮件自助找回密码，并收到实例即将到期、流量告警和工单回复邮件；管理员收到母鸡即将到期、母鸡流量告警和新工单邮件，各类通知可单独开关。未配置 SMTP 时，管理员可在「客户管理」为客户生成一次性重置链接。工单支持图片附件（PNG/JPEG/GIF/WebP，每条最多 5 张，单张大小可设）。

客户有**账户余额**：可充值，可直接支付新购和续费账单；余额和托管收益只能在平台内消费，不能提现。**托管中心**让用户把自己的服务器（Hatch Agent，LXC / Podman）接入平台，自定套餐和价格出售给其他用户：平台托管资金、按天把收益结算给机主并收取手续费（默认 20%），母鸡离线满 24 小时或机主下架时清退实例：退还买家剩余价值，机主再从余额赔付一份（以机主余额为限，不会扣成负数）；托管套餐可设置描述、每人限购和早期全额退款（购买 1 小时内且流量未超 1GB），买家可随时申请退款，其余情况按剩余天数比例退到余额；托管实例的工单由机主优先处理，每台托管母机有实时聊天室，管理员可以查看和介入。**交易市场**让用户转让持有满 31 天的运行中实例，用余额成交，平台收 20% 手续费，挂售期间实例停机冻结，到期未售出自动下架并暂停 3 天待续费，交易不退款。全平台流量按上下行双向合计计算。**优惠码**分平台（管理员，平台套餐）和机主（自己的托管套餐）两条线，可设最大使用次数、每账号限用次数、到期日期和续费同价。详见 [账户余额与托管中心](docs/HOSTING.md)。

「节点对接」支持三种母鸡后端：CLICD、LXDAPI（xkatld/lxdapi-web-server）和自研的 Hatch Agent（LXC 系统容器：Incus / LXD，或 Podman 容器；后台生成安装命令时先选虚拟化方式，选 LXC 而母机没装时自动安装 Incus 和系统镜像。Agent 主动连入，母鸡无需开放管理端口，并随站点版本自动升级）。新增节点表单按对接方式动态生成，客户中心按节点能力显示可用操作。「宿主机探针」一级页展示在线状态与调度容量；CLICD 详情页把 dashboard、host-info、host-history、host-report 四个接口完整转换为资源摘要、历史曲线、硬件/网络表格和结构化字段。「运营概览」展示 30 天实收、待收账款、客户、VPS、工单、自动化任务和宿主机健康数据。

商品套餐支持二次编辑和版本递增，按商品分类展示给客户，可复制、存为模板、批量创建和批量修改，可填描述和标签（显示在选购页的套餐卡片上）；每个套餐可选节点选择方式（指定节点、自动集中填充、自动分散均衡）。可售系统模板从在线节点中已启用、已下载的镜像聚合（Hatch 的 Podman 只列出安装脚本构建的 `localhost/hatch-*` 镜像），后台按系统版本勾选；NAT、公网 IPv4、IPv6 及数量由套餐统一设置，客户下单不能覆盖网络策略。前后台每个菜单均有独立 URL，刷新和浏览器前进/后退会保留当前位置。「选购 VPS」按分类浏览，右侧是紧凑的下单栏，顶部有托管市场和交易市场入口。前后台统一使用紧凑布局，所有确认框、输入框和结果提示都是站内弹窗，不使用浏览器自带的弹窗。

## 预构建镜像一键部署

`main` 分支每次通过完整 CI 后，GitHub Actions 会构建并推送以下多架构镜像（`linux/amd64`、`linux/arm64`）：

```text
ghcr.io/snail46/vpsbill-api:latest
ghcr.io/snail46/vpsbill-web:latest
```

同时发布不可变的提交标签 `sha-<完整提交哈希>`。生产环境建议在 `.env` 中固定该标签，需要升级时再明确修改：

```dotenv
IMAGE_TAG=sha-完整的40位Git提交哈希
```

服务器不需要 Git、Go、Node 或项目源码，只需：

```text
docker-compose.yml   # 使用 deploy/docker-compose.image.yml 的内容
.env                 # 参考 .env.example，必须使用独立随机密钥
```

镜像版 Compose 内嵌了 Caddy 配置，要求 Docker Compose 2.23.1 或更高版本。

两个 GHCR 镜像都是公开的，拉取不需要 `docker login`。API 镜像同时内置了同版本的 Hatch Agent，母鸡可以直接从 `https://计费域名/api/v1/agent/download/install.sh` 安装，见 [Hatch Agent](docs/HATCH-AGENT.md)。

### HTTPS 一条命令启动

`.env` 至少需要设置：

```dotenv
IMAGE_TAG=latest
PORTAL_BIND=127.0.0.1
ADMIN_BIND=127.0.0.1
PORTAL_TARGET=7080
ADMIN_TARGET=7081
DOMAIN=billing.example.com
ADMIN_DOMAIN=admin.example.com
POSTGRES_DB=vpsbill
POSTGRES_USER=vpsbill
POSTGRES_PASSWORD=随机长密码
SESSION_SECRET=64位十六进制随机值
ENCRYPTION_KEY=64位十六进制随机值
```

可直接生成最小 `.env`（先把域名改成自己的）：

```sh
umask 077
cat >.env <<EOF
IMAGE_TAG=latest
PORTAL_BIND=127.0.0.1
ADMIN_BIND=127.0.0.1
PORTAL_TARGET=7080
ADMIN_TARGET=7081
DOMAIN=billing.example.com
ADMIN_DOMAIN=admin.example.com
POSTGRES_DB=vpsbill
POSTGRES_USER=vpsbill
POSTGRES_PASSWORD=$(openssl rand -hex 24)
SESSION_SECRET=$(openssl rand -hex 32)
ENCRYPTION_KEY=$(openssl rand -hex 32)
EOF
```

在 Compose 和 `.env` 所在目录执行：

```sh
docker compose --env-file .env -f docker-compose.yml --profile tls up -d --pull always
```

该命令会拉取新镜像、创建 PostgreSQL/API/Web/Caddy 容器、等待数据库和 API 健康后启动入口。首次打开后台域名会进入图形化安装页，其余参数无需写入 `.env`。验证：

```sh
docker compose --env-file .env -f docker-compose.yml ps
curl --fail https://billing.example.com/health/ready
```

不使用域名的临时 HTTP 模式去掉上面四个 `*_BIND` / `*_TARGET` 变量和 `--profile tls`，前台在 8080、后台在 8081。Cloudflare Tunnel 和自己的反向代理见 [访问方式](docs/ACCESS.md)：cloudflared 跑在 Docker 里时隧道指向 `web:7080` / `web:7081`；装在宿主机上（例如 Mac mini 上用 OrbStack 跑计费站、cloudflared 装在 macOS 里）时用 `ACCESS_MODE=proxy`，隧道指向 `localhost:8080` / `localhost:8081`。HTTP 模式不适合正式业务。容器启动后访问页面完成图形化安装。

更新 `latest` 镜像仍使用同一条 `up -d --pull always` 命令。固定 SHA 标签时，应先完成数据库备份，再把 `IMAGE_TAG` 修改为新的 Actions 提交标签。数据库卷不会因容器更新而删除；不要运行 `down -v`。

## 本地开发

API 需要 PostgreSQL：

```sh
export DATABASE_URL='postgres://vpsbill:password@localhost:5432/vpsbill?sslmode=disable'
export SESSION_SECRET='development-only-secret'
go run ./cmd/server
```

前端：

```sh
cd web
npm install
npm run dev
```

完整验证（安装 Docker 时会额外运行隔离 PostgreSQL 集成测试并构建镜像）：

```sh
chmod +x verify.sh integration-test.sh
./verify.sh
```

## GitHub Actions Docker 验收

推送到 GitHub 后，`.github/workflows/ci.yml` 会在真实 Linux Docker 环境中自动完成：

- Go 单元测试、真实 PostgreSQL 集成测试、静态检查与前后端构建；
- API/Web 生产镜像构建、非 root 用户检查、Compose 与 Caddy 配置检查；
- 启动完整 Compose 栈并执行存活/就绪探测；
- 实际执行数据库备份、校验、恢复和加密主密钥轮换；
- 验证最终运行中的服务集合，并在失败时输出容器日志。

该工作流不需要仓库 Secrets。通过后证明镜像和单机 Compose 部署链路可运行；正式上线仍需在生产主机验证域名解析、80/443 端口、HTTPS 证书签发、支付回调和真实 CLICD 节点权限。

## 设计约束

- 金额统一存储为最小货币单位的整数，不使用浮点数。
- 支付事件通过 `(provider, provider_event_id)` 唯一约束防止重复入账。
- 自动化任务拥有独立去重键和显式状态。
- 节点调度通过行锁和库存预留防止并发超卖；可售资源 = Agent 检测值 × 公开的超售倍数（有平台上限），同机多个节点合并计算，母机持续过载自动暂停销售。
- 工作任务使用租约领取，失败按指数退避，进程重启会回收超时任务；最终失败可在后台人工重试。
- CLICD 同步创建使用确定性实例名，并在错误后查询实际状态，避免重试创建重复 VPS。
- 定时对账只记录 CLICD 实际运行状态；实例缺失会告警，不会静默重建或删除。
- API Key 与 TOTP 密钥只存储 AES-256-GCM 密文，并提供事务化主密钥轮换工具。
- 登录身份只放在 HttpOnly、SameSite Cookie 里。浏览器存储只放界面偏好（主题、Logo 形状）和当前标签页已加载的页面数据（sessionStorage，只属于当前账号，退出登录或关闭标签页即清除），实例 root 密码不缓存。

详细实施状态见 [开发路线图](docs/ROADMAP.md)。

支付服务商对接约定见 [通用支付回调](docs/PAYMENT-WEBHOOK.md)。

收银台跳转和签名约定见 [在线收银台对接协议](docs/PAYMENT-CHECKOUT.md)。

账户余额、托管规则、资金结算、清退、退款、优惠码与交易市场见 [账户余额与托管中心](docs/HOSTING.md)。

母鸡对接层契约与各后端接入计划见 [母鸡对接层](docs/PROVIDERS.md)；Hatch Agent 安装与宿主机准备见 [Hatch Agent](docs/HATCH-AGENT.md)；CLICD 节点权限和接口约定见 [CLICD 集成说明](docs/CLICD-INTEGRATION.md)。

邮件、短信或即时通讯适配器可接入 [通知事件 Webhook](docs/NOTIFICATION-WEBHOOK.md)。

生产安全配置、二步验证和 Prometheus 指标见 [安全运行说明](docs/SECURITY.md)。

自动续费账单、逾期停机、付款恢复与延期删除见 [服务生命周期](docs/SERVICE-LIFECYCLE.md)。

HTTPS、备份恢复和 AES 主密钥轮换见 [运维手册](docs/OPERATIONS.md)。

从空服务器到客户开通 VPS 的完整步骤见 [全新服务器部署教程](docs/DEPLOYMENT.md)。
