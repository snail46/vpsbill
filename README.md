# CLICD Billing

面向 CLICD LXC/KVM 节点的 VPS 商家财务、客户服务与自动化控制系统。

## 当前状态

项目功能开发完成：包含客户门户、商家后台、订单账单、幂等支付、自动开通、续费逾期、工单通知、审计安全、备份恢复和生产部署。持续集成会执行单元测试、PostgreSQL 并发集成测试、前后端构建及 Docker 镜像构建。

## 一键部署

生产 Linux 主机安装 Docker Engine 和 Compose 插件后运行：

```sh
chmod +x deploy.sh
./deploy.sh
```

脚本首次运行会从 `.env.example` 生成 `.env` 和随机密钥，然后构建并启动 PostgreSQL、API 和 Web 容器。默认入口为 `http://服务器IP:8080`；填写 `DOMAIN` 后自动启用 Caddy HTTPS。

首次打开页面时，系统会要求创建唯一的初始超级管理员。CLICD 节点的 API Key 使用独立 AES-256-GCM 主密钥加密后保存。

客户中心入口为 `http://服务器IP:8080/portal`。客户身份与商家管理员身份在服务端分别校验；客户只能读取和操作所属账户的服务。

在线支付默认关闭。配置 `PAYMENT_CHECKOUT_URL`、`PAYMENT_PROVIDER_NAME` 和公开 HTTPS `PUBLIC_URL` 后，客户可以从账单进入外部收银台；未配置时仅允许后台人工确认到账。

## 本地开发

API 需要 PostgreSQL：

```sh
export DATABASE_URL='postgres://clicd:password@localhost:5432/clicd_billing?sslmode=disable'
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
- 节点调度通过行锁和库存预留防止并发超卖；CPU、内存和磁盘在 MVP 中均不超售。
- 工作任务使用租约领取，失败按指数退避，进程重启会回收超时任务；最终失败可在后台人工重试。
- CLICD 同步创建使用确定性实例名，并在错误后查询实际状态，避免重试创建重复 VPS。
- 定时对账只记录 CLICD 实际运行状态；实例缺失会告警，不会静默重建或删除。
- API Key 与 TOTP 密钥只存储 AES-256-GCM 密文，并提供事务化主密钥轮换工具。
- 浏览器只保存 SameSite Cookie，不使用 localStorage 保存身份或业务数据。

详细实施状态见 [开发路线图](docs/ROADMAP.md)。

支付服务商对接约定见 [通用支付回调](docs/PAYMENT-WEBHOOK.md)。

收银台跳转和签名约定见 [在线收银台对接协议](docs/PAYMENT-CHECKOUT.md)。

节点权限和接口约定见 [CLICD 集成说明](docs/CLICD-INTEGRATION.md)。

邮件、短信或即时通讯适配器可接入 [通知事件 Webhook](docs/NOTIFICATION-WEBHOOK.md)。

生产安全配置、二步验证和 Prometheus 指标见 [安全运行说明](docs/SECURITY.md)。

自动续费账单、逾期停机、付款恢复与延期删除见 [服务生命周期](docs/SERVICE-LIFECYCLE.md)。

HTTPS、备份恢复和 AES 主密钥轮换见 [运维手册](docs/OPERATIONS.md)。
