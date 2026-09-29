import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api, cached, CustomerCatalogRecord, CustomerInvoiceRecord, CustomerTransactionRecord, OrderRecord, PaymentIntentRecord, WalletRecord } from '../api'
import { StatusBadge, money } from '../shared/ui'
import { formatDate, formatTime } from '../shared/time'

export function CustomerBilling() {
  const [invoices, setInvoices] = useState<CustomerInvoiceRecord[]>(() => cached<CustomerInvoiceRecord[]>('/api/v1/customer/invoices') ?? [])
  const [transactions, setTransactions] = useState<CustomerTransactionRecord[]>(() => cached<CustomerTransactionRecord[]>('/api/v1/customer/transactions') ?? [])
  const [orders, setOrders] = useState<OrderRecord[]>(() => cached<OrderRecord[]>('/api/v1/customer/orders') ?? [])
  const [checkoutEnabled, setCheckoutEnabled] = useState(() => cached<CustomerCatalogRecord>('/api/v1/customer/catalog')?.checkout_enabled ?? false)
  const [balance, setBalance] = useState(() => cached<WalletRecord>('/api/v1/customer/wallet')?.balance_minor ?? 0)
  const [paying, setPaying] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = async () => {
    try {
      const [i, t, o, c, w] = await Promise.all([
        api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices'),
        api<CustomerTransactionRecord[]>('/api/v1/customer/transactions'),
        api<OrderRecord[]>('/api/v1/customer/orders'),
        api<CustomerCatalogRecord>('/api/v1/customer/catalog'),
        api<WalletRecord>('/api/v1/customer/wallet'),
      ])
      setInvoices(i)
      setTransactions(t)
      setOrders(o)
      setCheckoutEnabled(c.checkout_enabled)
      setBalance(w.balance_minor)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function checkout(invoice: CustomerInvoiceRecord) {
    setPaying(invoice.id)
    setError('')
    try {
      const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${invoice.id}/checkout`, {
        method: 'POST',
      })
      window.location.assign(intent.checkout_url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建收银台失败')
      setPaying('')
    }
  }

  async function payWithBalance(invoice: CustomerInvoiceRecord) {
    setPaying(invoice.id)
    setError('')
    setNotice('')
    try {
      await api(`/api/v1/customer/invoices/${invoice.id}/pay-balance`, { method: 'POST' })
      setNotice(`账单 ${invoice.number} 已用余额支付`)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '余额支付失败')
    } finally {
      setPaying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">BILLING & LEDGER</p>
          <h2>订单、账单与付款明细</h2>
          <p>所有交易采用不可变账本设计；网关验签完成到账后系统自动调度开通。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <div className="note-banner">
        账户余额 <strong>{money(balance, invoices[0]?.currency || 'CNY')}</strong>，可直接用于支付新购和续费账单。<a href="/portal/wallet">充值或查看明细</a>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>订单记录</h3>
          <span className="tag">{orders.length} 笔订单</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>订单编号</th>
                <th>关联账单</th>
                <th>订单金额</th>
                <th>订单状态</th>
                <th>创建时间</th>
              </tr>
            </thead>
            <tbody>
              {orders.map(item => (
                <tr key={item.id}>
                  <td><strong>{item.number}</strong></td>
                  <td><code>{item.invoice_number}</code></td>
                  <td><strong>{money(item.total_minor, item.currency)}</strong></td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{formatTime(item.created_at)}</td>
                </tr>
              ))}
              {!orders.length && (
                <tr>
                  <td colSpan={5} className="empty-state">暂无订单记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>账单记录</h3>
          <span className="tag">{invoices.length} 张账单</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>账单号</th>
                <th>应付总额</th>
                <th>待结余额</th>
                <th>状态</th>
                <th>到期时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map(item => (
                <tr key={item.id}>
                  <td>
                    <strong>{item.number}</strong>
                    <small className="block">{({ initial: '新购', renewal: '续费', topup: '余额充值' } as Record<string, string>)[item.kind] || item.kind}</small>
                  </td>
                  <td><strong>{money(item.total_minor, item.currency)}</strong></td>
                  <td style={{ color: item.balance_minor > 0 ? 'var(--warning-text)' : 'inherit' }}>
                    <strong>{money(item.balance_minor, item.currency)}</strong>
                  </td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{formatDate(item.due_at)}</td>
                  <td className="row-actions">
                    {item.status === 'open' && item.kind !== 'topup' && (
                      <button
                        className="primary-button compact"
                        disabled={paying === item.id || balance < item.balance_minor}
                        title={balance < item.balance_minor ? `余额 ${money(balance, item.currency)} 不足` : ''}
                        onClick={() => void payWithBalance(item)}
                      >
                        余额支付
                      </button>
                    )}
                    {item.status === 'open' &&
                      (checkoutEnabled ? (
                        <button
                          className="primary-button compact"
                          disabled={paying === item.id}
                          onClick={() => checkout(item)}
                        >
                          {paying === item.id ? '跳转中…' : '立即支付'}
                        </button>
                      ) : (
                        <span style={{ color: 'var(--text-muted)', fontSize: '12px' }}>请联系商家线下结算</span>
                      ))}
                  </td>
                </tr>
              ))}
              {!invoices.length && (
                <tr>
                  <td colSpan={6} className="empty-state">暂无账单记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>付款交易流水</h3>
          <span className="tag">IMMUTABLE LEDGER</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>流水参考号</th>
                <th>关联账单</th>
                <th>支付渠道</th>
                <th>实付金额</th>
                <th>流水状态</th>
                <th>入账时间</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map(item => (
                <tr key={item.id}>
                  <td><strong>{item.provider_transaction_id || item.id}</strong></td>
                  <td>{item.invoice_number || '—'}</td>
                  <td><span className="tag">{item.provider.toUpperCase()}</span></td>
                  <td><strong>{money(item.amount_minor, item.currency)}</strong></td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{formatTime(item.created_at)}</td>
                </tr>
              ))}
              {!transactions.length && (
                <tr>
                  <td colSpan={6} className="empty-state">暂无付款流水记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
