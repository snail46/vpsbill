import { FormEvent, useEffect, useState } from 'react'
import { api, cached, CustomerCatalogRecord, CustomerIdentity, OrderRecord, PaymentIntentRecord } from '../api'
import { osOptions } from '../shared/nav'
import { CouponField } from '../Coupons'
import { cycleLabel, money } from '../shared/ui'
import { cycleOrder, priceLeft } from '../shared/cycles'
import { stockLeft, StockTag } from '../shared/stock'

// cardPrice shows the monthly price when the plan sells one, otherwise the
// shortest cycle.
function cardPrice<T extends { currency: string; billing_cycle: string }>(prices: T[], currency: string) {
  const own = prices.filter(price => price.currency === currency)
  return own.find(price => price.billing_cycle === 'monthly') ?? own[0]
}

export function CustomerShop({ customer }: { customer: CustomerIdentity }) {
  const [catalog, setCatalog] = useState<CustomerCatalogRecord | null>(() => cached<CustomerCatalogRecord>('/api/v1/customer/catalog') ?? null)
  const [selectedID, setSelectedID] = useState('')
  const [cycle, setCycle] = useState('monthly')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [created, setCreated] = useState<OrderRecord | null>(null)
  const [paying, setPaying] = useState(false)
  const [coupon, setCoupon] = useState('')
  const [discount, setDiscount] = useState(0)

  useEffect(() => {
    api<CustomerCatalogRecord>('/api/v1/customer/catalog')
      .then(value => {
        for (const plan of value.plans) plan.prices.sort((a, b) => cycleOrder(a.billing_cycle) - cycleOrder(b.billing_cycle))
        setCatalog(value)
        const first = value.plans.find(plan => plan.prices.some(price => price.currency === customer.default_currency))
        if (first) {
          setSelectedID(first.id)
          const price = cardPrice(first.prices, customer.default_currency)
          if (price) setCycle(price.billing_cycle)
        }
      })
      .catch(err => setError(err.message))
  }, [customer.default_currency])

  const plans = (catalog?.plans || []).filter(plan =>
    plan.prices.some(price => price.currency === customer.default_currency)
  )
  const selected = plans.find(plan => plan.id === selectedID)
  const prices = selected?.prices.filter(price => price.currency === customer.default_currency) || []

  useEffect(() => {
    if (prices.length && !prices.some(price => price.billing_cycle === cycle && priceLeft(price) !== 0)) {
      setCycle((prices.find(price => priceLeft(price) !== 0) ?? prices[0]).billing_cycle)
    }
  }, [selectedID])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selected) return
    const data = new FormData(event.currentTarget)
    setSaving(true)
    setError('')
    setCreated(null)
    try {
      const order = await api<OrderRecord>('/api/v1/customer/orders', {
        method: 'POST',
        body: JSON.stringify({
          items: [
            {
              plan_id: selected.id,
              region_id: data.get('region_id'),
              billing_cycle: data.get('billing_cycle'),
              quantity: Number(data.get('quantity')),
              configuration: { template_id: data.get('template_id') },
            },
          ],
          coupon_code: coupon,
        }),
      })
      setCreated(order)
    } catch (err) {
      setError(err instanceof Error ? err.message : '下单失败')
    } finally {
      setSaving(false)
    }
  }

  async function checkout() {
    if (!created) return
    setPaying(true)
    setError('')
    try {
      const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${created.invoice_id}/checkout`, {
        method: 'POST',
      })
      window.location.assign(intent.checkout_url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建收银台失败')
      setPaying(false)
    }
  }

  const currentPrice = prices.find(price => price.billing_cycle === cycle)

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">MARKETPLACE</p>
          <h2>选购 VPS 套餐</h2>
          <p>配置与定价由服务端实时校验；支付完成后将自动触发集群调度并开通服务。</p>
        </div>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="shop-grid">
        {plans.map(plan => {
          const price = cardPrice(plan.prices, customer.default_currency)
          return (
            <button
              type="button"
              key={plan.id}
              className={selectedID === plan.id ? 'shop-plan selected' : 'shop-plan'}
              onClick={() => setSelectedID(plan.id)}
            >
              <div>
                <span className="tag">{plan.virtualization.toUpperCase()}</span>
                <StockTag plan={plan} />
                {selectedID === plan.id && <span className="selected-mark">已选定</span>}
              </div>
              <h3>{plan.name}</h3>
              <small>{plan.code}</small>
              <div className="shop-specs">
                <span>{plan.vcpu} vCPU</span>
                <span>{plan.ram_mb} MB 内存</span>
                <span>{plan.disk_gb} GB SSD</span>
                <span>{plan.traffic_gb} GB 流量</span>
              </div>
              <div className="shop-price">
                {price ? money(price.amount_minor, price.currency) : '暂无报价'}
                <small>/ {price ? cycleLabel(price.billing_cycle) : ''}</small>
              </div>
            </button>
          )
        })}
        {catalog && !plans.length && <div className="empty-card" style={{ gridColumn: '1 / -1' }}>当前币种暂无可售套餐</div>}
      </div>

      {selected && (
        <form className="checkout-config panel" onSubmit={submit}>
          <div className="panel-heading">
            <div>
              <p className="eyebrow">ORDER CONFIGURATION</p>
              <h3>配置实例选项：{selected.name}</h3>
            </div>
            <span className="tag">SERVER PRICED</span>
          </div>
          <div className="form-grid">
            <label>
              <span>部署地域</span>
              <select name="region_id" required>
                {catalog?.regions.map(region => (
                  <option key={region.id} value={region.id}>
                    {region.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>计费周期</span>
              <select name="billing_cycle" value={cycle} onChange={event => setCycle(event.target.value)}>
                {prices.map(price => (
                  <option key={price.billing_cycle} value={price.billing_cycle} disabled={priceLeft(price) === 0}>
                    {cycleLabel(price.billing_cycle)} 付款 · {money(price.amount_minor, price.currency)}
                    {priceLeft(price) === 0 ? '（已达限购次数）' : priceLeft(price) !== null ? `（限购剩 ${priceLeft(price)} 次）` : ''}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>购买数量</span>
              <input name="quantity" type="number" min="1" max={Math.max(1, Math.min(20, stockLeft(selected) ?? 20))} defaultValue="1" />
            </label>
            <label>
              <span>操作系统镜像</span>
              <select key={selected.id} name="template_id" defaultValue={selected.default_template_id}>
                {osOptions(selected.allowed_template_ids).map(template => (
                  <option key={template.id} value={template.id}>
                    {template.label}
                  </option>
                ))}
              </select>
            </label>
            <CouponField planId={selected.id} cycle={cycle} onApplied={(code, discount) => { setCoupon(code); setDiscount(discount) }} />
            <div className="order-total">
              <span>每台应付{currentPrice?.setup_fee_minor ? '（含开通费）' : ''}{discount ? '（已扣优惠）' : ''}</span>
              <strong>
                {money(
                  (currentPrice?.amount_minor || 0) + (currentPrice?.setup_fee_minor || 0) - discount,
                  customer.default_currency
                )}
              </strong>
            </div>
            <div className="form-actions wide">
              <button className="primary-button compact" disabled={saving || !catalog?.regions.length || stockLeft(selected) === 0}>
                {saving ? '正在生成订单…' : '立即下单'}
              </button>
            </div>
          </div>
        </form>
      )}

      {created && (
        <div className="checkout-success">
          <div>
            <strong>订单 {created.number} 已生成</strong>
            <span>应付总额 {money(created.total_minor, created.currency)}{created.discount_minor ? `（已优惠 ${money(created.discount_minor, created.currency)}）` : ''}，关联账单 {created.invoice_number}</span>
          </div>
          {catalog?.checkout_enabled ? (
            <button className="primary-button compact" disabled={paying} onClick={checkout}>
              {paying ? '正在前往收银台…' : '前往在线支付'}
            </button>
          ) : (
            <span>当前未配置在线支付渠道，请联系商家后台完成入账。</span>
          )}
        </div>
      )}
    </section>
  )
}
