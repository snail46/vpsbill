import { FormEvent, useEffect, useState } from 'react'
import { api, cached, CustomerCatalogRecord, CustomerIdentity, OrderRecord, PaymentIntentRecord } from '../api'
import { ArrowLeftRight, ArrowRight, ShieldCheck, Store } from 'lucide-react'
import { navigatePortal, osOptions } from '../shared/nav'
import { CouponField } from '../Coupons'
import { cycleLabel, money, bandwidthLabel } from '../shared/ui'
import { CurrencyNote } from '../shared/LocaleMenu'
import { cycleOrder, priceLeft } from '../shared/cycles'
import { stockLeft, StockTag } from '../shared/stock'
import { t } from '../shared/i18n'

// cardPrice shows the monthly price when the plan sells one, otherwise the
// shortest cycle.
function cardPrice<T extends { currency: string; billing_cycle: string }>(prices: T[], currency: string) {
  const own = prices.filter(price => price.currency === currency)
  return own.find(price => price.billing_cycle === 'monthly') ?? own[0]
}

export function CustomerShop({ customer }: { customer: CustomerIdentity }) {
  const [catalog, setCatalog] = useState<CustomerCatalogRecord | null>(() => cached<CustomerCatalogRecord>('/api/v1/customer/catalog') ?? null)
  const [selectedID, setSelectedID] = useState('')
  const [categoryID, setCategoryID] = useState<string | null>(null)
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
        const usable = value.plans.filter(plan => plan.prices.some(price => price.currency === customer.default_currency))
        // Start in the first category that has something to sell.
        const first = (value.categories ?? []).map(category => usable.find(plan => plan.category_id === category.id)).find(Boolean) ?? usable[0]
        if (first) {
          setSelectedID(first.id)
          const price = cardPrice(first.prices, customer.default_currency)
          if (price) setCycle(price.billing_cycle)
        }
      })
      .catch(err => setError(err.message))
  }, [customer.default_currency])

  const sellable = (catalog?.plans || []).filter(plan =>
    plan.prices.some(price => price.currency === customer.default_currency)
  )
  // Plans are shown by category; plans outside every category go under
  // "其他" once there are categories at all.
  const categories = (catalog?.categories ?? []).filter(category => sellable.some(plan => plan.category_id === category.id))
  const groups = [
    ...categories.map(category => ({ id: category.id, name: category.name, description: category.description })),
    ...(categories.length && sellable.some(plan => !categories.some(category => category.id === plan.category_id))
      ? [{ id: '', name: t('其他'), description: '' }]
      : []),
  ]
  const activeGroup = groups.find(group => group.id === (categoryID ?? sellable.find(plan => plan.id === selectedID)?.category_id ?? groups[0]?.id)) ?? groups[0]
  const inGroup = (plan: { category_id?: string }) =>
    !activeGroup || (activeGroup.id ? plan.category_id === activeGroup.id : !categories.some(category => category.id === plan.category_id))
  const plans = sellable.filter(inGroup)
  const selected = plans.find(plan => plan.id === selectedID)
  // A plan sells where its nodes are; one limited to some nodes offers only
  // their regions.
  const regions = (catalog?.regions ?? []).filter(region =>
    selected?.region_ids?.length ? selected.region_ids.includes(region.id) : selected?.node_selection !== 'nodes'
  )

  function chooseGroup(id: string) {
    setCategoryID(id)
    const first = sellable.find(plan => (id ? plan.category_id === id : !categories.some(category => category.id === plan.category_id)))
    if (first) setSelectedID(first.id)
  }
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
      setError(err instanceof Error ? err.message : t('下单失败'))
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
      setError(err instanceof Error ? err.message : t('创建收银台失败'))
      setPaying(false)
    }
  }

  const currentPrice = prices.find(price => price.billing_cycle === cycle)

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">MARKETPLACE</p>
          <h2>{t('选购 VPS 套餐')}</h2>
          <p>
            <span className="tag source-platform shop-self-tag">{t('平台自营')}</span>{t('本页套餐全部由平台自营：平台的母机、平台定价、平台售后。支付完成后自动开通。')}
          </p>
        </div>
      </div>

      {/* Platform plans are not the only way to get a VPS here. */}
      <div className="shop-markets">
        <button type="button" className="shop-market-link" onClick={() => navigatePortal('/portal/hosting?tab=market')}>
          <Store size={18} aria-hidden />
          <span>
            <strong>{t('托管市场')}</strong>
            <small>{t('机主自营的母机，地区和价格更多样，平台担保资金')}</small>
          </span>
          <ArrowRight size={16} aria-hidden />
        </button>
        <button type="button" className="shop-market-link" onClick={() => navigatePortal('/portal/trade')}>
          <ArrowLeftRight size={18} aria-hidden />
          <span>
            <strong>{t('交易市场')}</strong>
            <small>{t('其他用户转让的现成实例，余额购买，即买即用')}</small>
          </span>
          <ArrowRight size={16} aria-hidden />
        </button>
      </div>

      <CurrencyNote />
      {error && <div className="form-error">{error}</div>}

      <div className={categories.length ? 'shop-body with-categories' : 'shop-body'}>
        {categories.length > 0 && (
          <aside className="shop-categories" aria-label={t('套餐分类')}>
            <p className="eyebrow">{t('套餐分类')}</p>
            <div role="tablist">
              {groups.map(group => (
                <button
                  type="button"
                  role="tab"
                  key={group.id || 'other'}
                  aria-selected={activeGroup?.id === group.id}
                  className={activeGroup?.id === group.id ? 'active' : ''}
                  onClick={() => chooseGroup(group.id)}
                >
                  <span>{group.name}</span>
                  <small>{sellable.filter(plan => (group.id ? plan.category_id === group.id : !categories.some(category => category.id === plan.category_id))).length}</small>
                </button>
              ))}
            </div>
          </aside>
        )}
        <div className="shop-main">
          {/* Every plan here is the platform's own; say so before anyone
              mistakes it for the whole market. */}
          <div className="shop-self-banner">
            <ShieldCheck size={16} aria-hidden />
            <span>
              <strong>{t('平台自营')}</strong>{t('以下套餐由平台直接提供和运维，售后请提交工单。机主出租的母机在「托管市场」，用户转让的实例在「交易市场」。')}
            </span>
          </div>
          {activeGroup?.description && (
            <div className="shop-category-intro">
              <p>{activeGroup.description}</p>
            </div>
          )}

          <div className={selected ? 'shop-layout with-order' : 'shop-layout'}>
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
                      <span className="tag source-platform">{t('自营')}</span>
                      <span className="tag">{plan.virtualization.toUpperCase()}</span>
                      <StockTag plan={plan} />
                      {selectedID === plan.id && <span className="selected-mark">{t('已选定')}</span>}
                    </div>
                    <h3>{plan.name}</h3>
                    <small>{plan.code}</small>
                    {!!plan.tags?.length && (
                      <div className="shop-tags">
                        {plan.tags.map(tag => <span key={tag}>{tag}</span>)}
                      </div>
                    )}
                    {plan.description && <p className="shop-description">{plan.description}</p>}
                    <div className="shop-specs">
                      <span>{plan.vcpu} vCPU</span>
                      <span>{t('{0} MB 内存', plan.ram_mb)}</span>
                      <span>{plan.disk_gb} GB SSD</span>
                      <span>{plan.traffic_gb ? t('{0} GB 流量', plan.traffic_gb) : t('不限流量')}</span>
                      <span>{bandwidthLabel(plan.network_down_mbps)}</span>
                    </div>
                    <div className="shop-price">
                      {price ? money(price.amount_minor, price.currency) : t('暂无报价')}
                      <small>/ {price ? cycleLabel(price.billing_cycle) : ''}</small>
                    </div>
                  </button>
                )
              })}
              {catalog && !plans.length && <div className="empty-card" style={{ gridColumn: '1 / -1' }}>{t('当前币种暂无可售套餐')}</div>}
            </div>

            {selected && (
              <aside className="shop-order">
                <form className="shop-order-card" onSubmit={submit}>
                  <header>
                    <p className="eyebrow">ORDER</p>
                    <h3>{selected.name}</h3>
                    <small>
                      {t('{0} 核 · {1} · {2} GB · {3}', selected.vcpu, selected.ram_mb >= 1024 ? `${+(selected.ram_mb / 1024).toFixed(1)} GB` : `${selected.ram_mb} MB`, selected.disk_gb, bandwidthLabel(selected.network_down_mbps))}
                    </small>
                  </header>
                  <label>
                    <span>{t('地域')}</span>
                    <select key={selected.id} name="region_id" required>
                      {!regions.length && <option value="">{t('该套餐暂无可售地域')}</option>}
                      {regions.map(region => (
                        <option key={region.id} value={region.id}>
                          {region.name}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    <span>{t('计费周期')}</span>
                    <select name="billing_cycle" value={cycle} onChange={event => setCycle(event.target.value)}>
                      {prices.map(price => (
                        <option key={price.billing_cycle} value={price.billing_cycle} disabled={priceLeft(price) === 0}>
                          {cycleLabel(price.billing_cycle)} · {money(price.amount_minor, price.currency)}
                          {priceLeft(price) === 0 ? t('（已达限购次数）') : priceLeft(price) !== null ? t('（限购剩 {0} 次）', priceLeft(price)) : ''}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    <span>{t('系统')}</span>
                    <select key={selected.id} name="template_id" defaultValue={selected.default_template_id}>
                      {osOptions(selected.allowed_template_ids).map(template => (
                        <option key={template.id} value={template.id}>
                          {template.label}
                        </option>
                      ))}
                    </select>
                  </label>
                  <label>
                    <span>{t('数量')}</span>
                    <input name="quantity" type="number" min="1" max={Math.max(1, Math.min(20, stockLeft(selected) ?? 20))} defaultValue="1" />
                  </label>
                  <CouponField planId={selected.id} cycle={cycle} onApplied={(code, discount) => { setCoupon(code); setDiscount(discount) }} />
                  <div className="shop-order-total">
                    <span>{t('每台应付{0}{1}', currentPrice?.setup_fee_minor ? t('（含开通费）') : '', discount ? t('（已扣优惠）') : '')}</span>
                    <strong>
                      {money(
                        (currentPrice?.amount_minor || 0) + (currentPrice?.setup_fee_minor || 0) - discount,
                        customer.default_currency
                      )}
                    </strong>
                  </div>
                  <button className="primary-button" disabled={saving || !regions.length || stockLeft(selected) === 0}>
                    {saving ? t('正在生成订单…') : stockLeft(selected) === 0 ? t('已售罄') : t('立即下单')}
                  </button>
                </form>

                {created && (
                  <div className="checkout-success shop-order-result">
                    <div>
                      <strong>{t('订单 {0} 已生成', created.number)}</strong>
                      <span>{t('应付总额 {0}{1}，关联账单 {2}', money(created.total_minor, created.currency), created.discount_minor ? t('（已优惠 {0}）', money(created.discount_minor, created.currency)) : '', created.invoice_number)}</span>
                    </div>
                    {catalog?.checkout_enabled ? (
                      <button className="primary-button compact" disabled={paying} onClick={checkout}>
                        {paying ? t('正在前往收银台…') : t('前往在线支付')}
                      </button>
                    ) : (
                      <span>{t('当前未配置在线支付渠道，请联系商家后台完成入账。')}</span>
                    )}
                  </div>
                )}
              </aside>
            )}
          </div>
        </div>
      </div>
    </section>
  )
}
