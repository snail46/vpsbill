# 安全运行说明

- 密码使用 Argon2id；登录失败按邮箱哈希和来源 IP 在 15 分钟窗口限流。
- 后台可以再加一层 Cloudflare Access（只放行指定邮箱），步骤见 [ACCESS.md「给后台加 Cloudflare Access」](ACCESS.md#给后台加-cloudflare-access)。
- 管理员可在“登录安全”启用 TOTP 二步验证。TOTP 密钥使用 `ENCRYPTION_KEY` 通过 AES-256-GCM 加密保存。
- 会话仅使用 HttpOnly、SameSite=Strict Cookie；写操作要求双提交 CSRF Token。
- 暂停客户账户会立即撤销该账户全部会话，但不会自动删除或关闭 VPS。
- CLICD API Key、TOTP 密钥和支付密钥不会返回到普通查询接口。实例 root 密码以 AES-256-GCM 密文保存，仅所属客户通过单独鉴权接口按需读取。
- WebSSH/VNC 通过本站同源反向代理，浏览器只能取得绑定所属实例、60 秒有效的一次性票据，不能读取节点地址或 API Key。
- 真实访客 IP 只从可信端口（Web 容器的 7080 / 7081，只发布在 `127.0.0.1` 上）的 `CF-Connecting-IP`、`X-Real-IP`、`X-Forwarded-For` 读取；直连端口会覆盖这些头，防止伪造。经 Cloudflare 的请求进了直连端口时，后台会提示，处理方法见 [访问方式](ACCESS.md#常见问题)。
- 前端不依赖第三方字体或脚本；API 返回 CSP、反框架、MIME 嗅探和权限策略响应头。

生产环境必须通过 HTTPS 暴露服务，保持 `SESSION_SECRET`、`ENCRYPTION_KEY`、支付、通知和指标 Token 各自独立。轮换 `ENCRYPTION_KEY` 前必须执行密钥轮换工具，不能直接替换环境变量。

## Prometheus 指标

`GET /metrics` 使用以下请求头：

```text
Authorization: Bearer <安装页生成或填写的 Metrics Token>
```

指标仅包含汇总数量，不包含客户、账单或实例标识。Metrics Token 至少 32 个字符，留空时由首次安装向导生成并只显示一次。
