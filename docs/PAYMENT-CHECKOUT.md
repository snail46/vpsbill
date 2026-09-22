# 在线支付网关

登录商家后台，在「支付网关」中选择并配置一个渠道。配置密钥使用 AES-256-GCM 加密，接口只返回“已配置”状态而不回显内容。修改时密钥留空会保留原值。客户下单、计价、账单和支付意图全部由服务端创建，浏览器不能修改成交金额。

## 支付宝当面付

使用支付宝开放平台 `alipay.trade.precreate` 创建预下单，采用 RSA2 签名。需要填写 App ID、应用私钥、支付宝公钥和网关地址；生产网关通常为 `https://openapi.alipay.com/gateway.do`。后台显示的异步通知地址必须配置到可公网访问的 HTTPS 域名。

异步通知会验证支付宝 RSA2 签名、App ID、交易状态、商户订单号和金额，只接受 `TRADE_SUCCESS` 或 `TRADE_FINISHED`。浏览器返回页不会触发入账。

## 易支付

兼容常见彩虹易支付协议，支持 `alipay`、`wxpay`、`qqpay` 通道。需要填写接口根地址（系统会请求 `submit.php`）、商户 PID 和商户密钥。请求参数按键名排序后使用 MD5 签名；异步通知会复核 PID、签名、`TRADE_SUCCESS`、商户订单号和金额，并以纯文本 `success` 应答。

不同易支付服务端可能修改兼容协议，上线前必须使用小额订单验证接口路径、签名规则和回调字段。

## 通用 HMAC 收银台

通用模式向外部收银台追加以下查询参数：`merchant_reference`、`invoice_number`、`amount_minor`、`currency`、`expires_at`、`notify_url`、`return_url` 和 `signature`。签名原文是不含 `signature` 的查询参数经标准 URL 编码和键排序后的结果，算法为 HMAC-SHA256。

外部适配器完成支付后，按 [通用支付回调](PAYMENT-WEBHOOK.md) 通知系统。支付意图有效期为 30 分钟，同一账单和渠道在有效期内会复用。

## 生产要求

- 公开 URL 和支付回调必须使用 HTTPS。
- 商户订单号必须原样传递，不能以浏览器传入金额为准。
- 仅服务端异步成功通知可以入账；同步跳转只用于用户体验。
- 切换渠道前应确认旧渠道不存在尚未送达的成功通知。
