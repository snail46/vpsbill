# 通用支付回调

系统提供支付无关的通用回调入口，Stripe、支付宝、微信支付等适配器统一转换成此事件模型。出站收银台参数见 [在线收银台对接协议](PAYMENT-CHECKOUT.md)。

## 地址

```text
POST /api/v1/webhooks/payments/generic
```

## 验签

使用 `.env` 中的 `PAYMENT_WEBHOOK_SECRET` 对原始请求体计算 HMAC-SHA256：

```text
X-Payment-Signature: sha256=<hex-hmac>
```

签名必须基于未经重新格式化的原始 JSON 字节。渠道名来自 `PAYMENT_PROVIDER_NAME`。事件通过 `(provider, id)` 唯一约束实现幂等；重复发送已成功处理的事件会返回成功，但不会重复入账或开通 VPS。

## 事件

```json
{
  "id": "evt_20260921_001",
  "type": "payment.succeeded",
  "transaction_id": "gateway_tx_001",
  "invoice_number": "INV-20260921-XXXXXXXXXX",
  "amount_minor": 1900,
  "currency": "CNY"
}
```

金额使用最小货币单位。例如人民币 `1900` 表示 `19.00 CNY`。

系统只接受金额、币种、账单状态完全匹配的成功支付。处理事务同时完成：

1. 保存并去重支付事件。
2. 写入不可变交易流水。
3. 结清账单、支付意图并更新订单状态。
4. 创建 VPS 服务记录。
5. 创建去重的开通任务和 Outbox 事件。

任一步骤失败，整个数据库事务回滚。
