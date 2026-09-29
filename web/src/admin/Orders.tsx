import { FormEvent, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { AccountRecord, api, cached, InvoiceRecord, OrderRecord, PlanRecord, RegionRecord, TransactionRecord } from '../api'
import { PageActions, StatusBadge, cycleLabel, money } from '../shared/ui'
import { formatDate, formatTime } from '../shared/time'

export function OrdersView() {
  const [orders, setOrders] = useState<OrderRecord[]>(() => cached<OrderRecord[]>('/api/v1/admin/orders') ?? [])
  const [customers, setCustomers] = useState<AccountRecord[]>(() => cached<AccountRecord[]>('/api/v1/admin/customers') ?? [])
  const [plans, setPlans] = useState<PlanRecord[]>(() => (cached<PlanRecord[]>('/api/v1/admin/plans') ?? []).filter(plan => plan.enabled))
  const [regions, setRegions] = useState<RegionRecord[]>(() => cached<RegionRecord[]>('/api/v1/admin/regions') ?? [])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [orderRows, customerRows, planRows, regionRows] = await Promise.all([
        api<OrderRecord[]>('/api/v1/admin/orders'),
        api<AccountRecord[]>('/api/v1/admin/customers'),
        api<PlanRecord[]>('/api/v1/admin/plans'),
        api<RegionRecord[]>('/api/v1/admin/regions'),
      ])
      setOrders(orderRows)
      setCustomers(customerRows)
      setPlans(planRows.filter(plan => plan.enabled))
      setRegions(regionRows)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="SALES ORDERS"
        title="销售订单"
        description="所有订单金额由服务端严格根据当前生效套餐价格原子计算与锁价。"
        action={() => setShowForm(true)}
        actionLabel="创建订单"
      />

      {error && <div className="form-error">{error}</div>}

      {showForm && (
        <OrderForm
          customers={customers}
          plans={plans}
          regions={regions}
          onClose={() => setShowForm(false)}
          onCreated={() => {
            setShowForm(false)
            load()
          }}
        />
      )}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>订单号</th>
              <th>客户名称</th>
              <th>账单编号</th>
              <th>订单金额</th>
              <th>订单状态</th>
              <th>下单时间</th>
            </tr>
          </thead>
          <tbody>
            {orders.map(order => (
              <tr key={order.id}>
                <td>
                  <strong>{order.number}</strong>
                  <small>{order.id}</small>
                </td>
                <td>{order.customer_name}</td>
                <td><code>{order.invoice_number}</code></td>
                <td><strong>{money(order.total_minor, order.currency)}</strong></td>
                <td><StatusBadge status={order.status} /></td>
                <td>{formatTime(order.created_at)}</td>
              </tr>
            ))}
            {!orders.length && (
              <tr>
                <td colSpan={6} className="empty-state">尚未创建任何订单</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

export function OrderForm({
  customers,
  plans,
  regions,
  onClose,
  onCreated,
}: {
  customers: AccountRecord[]
  plans: PlanRecord[]
  regions: RegionRecord[]
  onClose: () => void
  onCreated: () => void
}) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const sellablePlans = plans.filter(plan => plan.enabled)
  const [customerID, setCustomerID] = useState(customers[0]?.id || '')
  const [planID, setPlanID] = useState(sellablePlans[0]?.id || '')
  const customer = customers.find(item => item.id === customerID)
  const plan = sellablePlans.find(item => item.id === planID)
  const prices = plan?.prices.filter(price => price.currency === customer?.default_currency) || []
  const ready = customers.length > 0 && sellablePlans.length > 0 && regions.length > 0

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    try {
      await api('/api/v1/admin/orders', {
        method: 'POST',
        body: JSON.stringify({
          account_id: data.get('account_id'),
          items: [
            {
              plan_id: data.get('plan_id'),
              region_id: data.get('region_id'),
              billing_cycle: data.get('billing_cycle'),
              quantity: Number(data.get('quantity')),
              configuration: { template_id: data.get('template_id') },
            },
          ],
        }),
      })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建订单失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>手工创建订单与账单</h3>
          <p>提交后将自动为目标客户生成待付款订单及防篡改的账单明细。</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      {!ready ? (
        <div className="form-error">请确保系统中已至少存在 1 个有效客户、1 个上架套餐及 1 个节点地域。</div>
      ) : (
        <form className="form-grid" onSubmit={submit}>
          <label>
            <span>归属客户</span>
            <select name="account_id" value={customerID} onChange={event => setCustomerID(event.target.value)}>
              {customers.map(item => (
                <option key={item.id} value={item.id}>
                  {item.display_name} ({item.default_currency})
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>选购套餐</span>
            <select name="plan_id" value={planID} onChange={event => setPlanID(event.target.value)}>
              {sellablePlans.map(item => (
                <option key={item.id} value={item.id}>
                  {item.name} · {item.virtualization.toUpperCase()}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>节点地域</span>
            <select name="region_id">
              {regions.map(region => (
                <option key={region.id} value={region.id}>
                  {region.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>计费周期</span>
            <select name="billing_cycle" required>
              {prices.map(price => (
                <option key={price.billing_cycle} value={price.billing_cycle}>
                  {cycleLabel(price.billing_cycle)} 付款 · {money(price.amount_minor + price.setup_fee_minor, price.currency)}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>购买数量</span>
            <input name="quantity" type="number" min="1" max="20" defaultValue="1" />
          </label>
          <label>
            <span>预设操作系统镜像</span>
            <select key={planID} name="template_id" defaultValue={plan?.default_template_id}>
              {plan?.allowed_template_ids.map(template => (
                <option key={template} value={template}>
                  {template}
                </option>
              ))}
            </select>
          </label>

          {!prices.length && (
            <div className="form-error wide">所选套餐尚未配置该客户币种的价格，请先前往“商品套餐”补充对应币种。</div>
          )}
          {error && <div className="form-error wide">{error}</div>}

          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={onClose}>取消</button>
            <button className="primary-button" disabled={saving || !prices.length}>
              {saving ? '正在生成…' : '生成订单'}
            </button>
          </div>
        </form>
      )}
    </div>
  )
}

export function BillingView() {
  const [invoices, setInvoices] = useState<InvoiceRecord[]>(() => cached<InvoiceRecord[]>('/api/v1/admin/invoices') ?? [])
  const [transactions, setTransactions] = useState<TransactionRecord[]>(() => cached<TransactionRecord[]>('/api/v1/admin/transactions') ?? [])
  const [error, setError] = useState('')
  const [paying, setPaying] = useState('')

  const load = async () => {
    try {
      const [invoiceRows, transactionRows] = await Promise.all([
        api<InvoiceRecord[]>('/api/v1/admin/invoices'),
        api<TransactionRecord[]>('/api/v1/admin/transactions'),
      ])
      setInvoices(invoiceRows)
      setTransactions(transactionRows)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function pay(invoice: InvoiceRecord) {
    if (
      !window.confirm(
        `确认已线下收到款项 ${money(invoice.balance_minor, invoice.currency)}？此操作将记录不可更改的入账流水并触发 VPS 自动调度开通。`
      )
    ) {
      return
    }
    setPaying(invoice.id)
    setError('')
    try {
      await api(`/api/v1/admin/invoices/${invoice.id}/pay`, { method: 'POST', body: JSON.stringify({ reference: '' }) })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '确认入账失败')
    } finally {
      setPaying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">BILLING & AUDIT LEDGER</p>
          <h2>账单与财务流水</h2>
          <p>采用金融级复式只追加（Append-Only）记账模型；任何退款与冲正均产生反向新流水。</p>
        </div>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="panel">
        <div className="panel-heading">
          <h3>全部账单</h3>
          <span className="tag">{invoices.length} 笔账单</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>账单号</th>
                <th>关联客户</th>
                <th>账单金额</th>
                <th>未付余额</th>
                <th>状态</th>
                <th>到期时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map(invoice => (
                <tr key={invoice.id}>
                  <td><strong>{invoice.number}</strong></td>
                  <td>{invoice.customer_name}</td>
                  <td><strong>{money(invoice.total_minor, invoice.currency)}</strong></td>
                  <td style={{ color: invoice.balance_minor > 0 ? 'var(--warning-text)' : 'inherit' }}>
                    <strong>{money(invoice.balance_minor, invoice.currency)}</strong>
                  </td>
                  <td><StatusBadge status={invoice.status} /></td>
                  <td>{formatDate(invoice.due_at)}</td>
                  <td>
                    {invoice.status === 'open' && (
                      <button
                        className="primary-button compact"
                        disabled={paying === invoice.id}
                        onClick={() => pay(invoice)}
                      >
                        {paying === invoice.id ? '入账处理中…' : invoice.kind === 'topup' ? '确认到账（充值）' : '确认到账并开通'}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {!invoices.length && (
                <tr>
                  <td colSpan={7} className="empty-state">暂无账单数据</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>不可变资金交易流水</h3>
          <span className="tag">APPEND ONLY</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>外部交易单号</th>
                <th>账单号</th>
                <th>付款客户</th>
                <th>收款渠道</th>
                <th>实付金额</th>
                <th>流水状态</th>
                <th>入账时间</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map(transaction => (
                <tr key={transaction.id}>
                  <td>
                    <strong>{transaction.provider_transaction_id}</strong>
                    <small>{transaction.id}</small>
                  </td>
                  <td><code>{transaction.invoice_number}</code></td>
                  <td>{transaction.customer_name}</td>
                  <td><span className="tag">{transaction.provider.toUpperCase()}</span></td>
                  <td><strong>{money(transaction.amount_minor, transaction.currency)}</strong></td>
                  <td><StatusBadge status={transaction.status} /></td>
                  <td>{formatTime(transaction.created_at)}</td>
                </tr>
              ))}
              {!transactions.length && (
                <tr>
                  <td colSpan={7} className="empty-state">暂无交易流水记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
