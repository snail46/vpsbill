# 安全运行说明

- 密码使用 Argon2id；登录失败按邮箱哈希和来源 IP 在 15 分钟窗口限流。
- 管理员可在“登录安全”启用 TOTP 二步验证。TOTP 密钥使用 `ENCRYPTION_KEY` 通过 AES-256-GCM 加密保存。
- 会话仅使用 HttpOnly、SameSite=Strict Cookie；写操作要求双提交 CSRF Token。
- 暂停客户账户会立即撤销该账户全部会话，但不会自动删除或关闭 VPS。
- CLICD API Key、TOTP 密钥和支付密钥不会返回到普通查询接口。
- 前端不依赖第三方字体或脚本；API 返回 CSP、反框架、MIME 嗅探和权限策略响应头。

生产环境必须通过 HTTPS 暴露服务，保持 `SESSION_SECRET`、`ENCRYPTION_KEY`、支付、通知和指标 Token 各自独立。轮换 `ENCRYPTION_KEY` 前必须执行密钥轮换工具，不能直接替换环境变量。

## Prometheus 指标

`GET /metrics` 使用以下请求头：

```text
Authorization: Bearer <METRICS_TOKEN>
```

指标仅包含汇总数量，不包含客户、账单或实例标识。生产模式要求 `METRICS_TOKEN` 至少 32 个字符。
