# 通知事件 Webhook

系统使用数据库 Outbox 可靠投递订单、工单和服务生命周期事件。在首次安装页或后台「站点设置」配置通知 Webhook 与至少 32 字符的签名密钥后启用；签名密钥留空时由系统生成。URL 留空时不会进行网络投递，事件仍保留在数据库中。

## 请求

通知工作器向配置地址发送 `POST application/json`。主要请求头：

- `X-VPSBill-Event`：事件类型，如 `order.created`、`ticket.created`、`ticket.customer_replied`。
- `Idempotency-Key`：全局唯一去重键，接收端必须据此幂等处理。
- `X-VPSBill-Signature`：`sha256=<hex-hmac>`，对原始 JSON 请求体使用 HMAC-SHA256 计算。

请求体包含 `id`、`aggregate_type`、`aggregate_id`、`event_type`、`deduplication_key`、`payload`、`attempts` 和 `created_at`。

接收端返回任意 `2xx` 表示成功。连接失败、超时或非 `2xx` 响应会触发指数退避，最长间隔一小时；工作器采用租约和 `FOR UPDATE SKIP LOCKED`，支持多副本安全竞争。

推荐把此 Webhook 接到独立通知适配器，再由适配器发送邮件、短信、企业微信或 Telegram，避免把第三方凭证放进财务系统主进程。
