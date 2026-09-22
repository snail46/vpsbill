# 在线收银台对接协议

客户中心不会在浏览器中计算或提交成交金额。创建订单、生成账单和创建支付意图全部由服务端完成；只有配置 `PAYMENT_CHECKOUT_URL` 后，页面才显示“立即付款”。

## 发起支付

系统将客户重定向至 `PAYMENT_CHECKOUT_URL`，并追加以下查询参数：

| 参数 | 说明 |
| --- | --- |
| `merchant_reference` | 本系统生成的唯一支付意图编号 |
| `invoice_number` | 账单号 |
| `amount_minor` | 最小货币单位的整数金额 |
| `currency` | 三位大写币种 |
| `expires_at` | 支付意图过期时间，Unix 秒 |
| `notify_url` | 支付成功后的服务端回调地址 |
| `return_url` | 支付后返回客户中心的地址 |
| `signature` | `sha256=<hex-hmac>` |

签名原文是不含 `signature` 的全部查询参数，使用标准 URL 编码并按键排序后的字符串（Go `url.Values.Encode()` 结果）。密钥为 `PAYMENT_WEBHOOK_SECRET`，算法为 HMAC-SHA256。支付适配器必须先验签，再创建支付会话；不得采用浏览器传入的其他金额或币种。

## 支付结果

支付成功后，适配器向 `notify_url` 发送 [通用支付回调](PAYMENT-WEBHOOK.md)。回调必须使用同一密钥签名原始 JSON 请求体。系统会再次核对账单状态、币种和完整余额，成功后在同一事务内入账并触发 VPS 开通。

`PAYMENT_PROVIDER_NAME` 用于交易、支付事件和支付意图的渠道标识；同一部署应保持稳定。未支付意图有效期为 30 分钟，同一账单和渠道在有效期内会复用已有意图。

## 生产要求

- `PUBLIC_URL`、`PAYMENT_CHECKOUT_URL` 必须使用 HTTPS。
- 支付适配器应把 `merchant_reference` 保存到支付服务商元数据，方便对账。
- 只有最终成功状态才能发送 `payment.succeeded`；支付页面返回不能作为到账依据。
- 禁止把 `PAYMENT_WEBHOOK_SECRET` 或支付服务商私钥下发给前端。
- 留空 `PAYMENT_CHECKOUT_URL` 会安全关闭在线付款入口，商家仍可在后台人工确认到账。
