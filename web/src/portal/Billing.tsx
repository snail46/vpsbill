import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api, cached, CustomerCatalogRecord, CustomerInvoiceRecord, CustomerTransactionRecord, OrderRecord, PaymentIntentRecord, WalletRecord } from '../api'
import { StatusBadge, money } from '../shared/ui'
import { Charged, CurrencyNote } from '../shared/LocaleMenu'
import { formatDate, formatTime } from '../shared/time'
import { topupLink } from '../Wallet'
import { t } from '../shared/i18n'

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
      setError(err instanceof Error ? err.message : t('加载失败'))
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
      setError(err instanceof Error ? err.message : t('创建收银台失败'))
      setPaying('')
    }
  }

  async function payWithBalance(invoice: CustomerInvoiceRecord) {
    setPaying(invoice.id)
    setError('')
    setNotice('')
    try {
      await api(`/api/v1/customer/invoices/${invoice.id}/pay-balance`, { method: 'POST' })
      setNotice(t('账单 {0} 已用余额支付', invoice.number))
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('余额支付失败'))
    } finally {
      setPaying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">BILLING & LEDGER</p>
          <h2>{t('订单、账单与付款明细')}</h2>
          <p>{t('所有交易采用不可变账本设计；网关验签完成到账后系统自动调度开通。')}</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />{t('刷新')}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <CurrencyNote />
      <div className="note-banner">
        {t('账户余额')} <strong>{money(balance, invoices[0]?.currency || 'CNY')}</strong>{t('，可直接用于支付新购和续费账单。')}<a href="/portal/wallet">{t('充值或查看明细')}</a>
      </div>
      <div className="note-banner">
        {t('余额充值和平台自营产品可以在线支付；托管产品（新购和续费）只支持余额支付，余额不足时请先充值。平台自营产品非产品问题不退款，退款只退到账户余额。')}
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>{t('订单记录')}</h3>
          <span className="tag">{t('{0} 笔订单', orders.length)}</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t('订单编号')}</th>
                <th>{t('关联账单')}</th>
                <th>{t('订单金额')}</th>
                <th>{t('订单状态')}</th>
                <th>{t('创建时间')}</th>
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
                  <td colSpan={5} className="empty-state">{t('暂无订单记录')}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>{t('账单记录')}</h3>
          <span className="tag">{t('{0} 张账单', invoices.length)}</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t('账单号')}</th>
                <th>{t('应付总额')}</th>
                <th>{t('待结余额')}</th>
                <th>{t('状态')}</th>
                <th>{t('到期时间')}</th>
                <th>{t('操作')}</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map(item => (
                <tr key={item.id}>
                  <td>
                    <strong>{item.number}</strong>
                    <small className="block">{({ initial: t('新购'), renewal: t('续费'), topup: t('余额充值') } as Record<string, string>)[item.kind] || item.kind}</small>
                  </td>
                  <td><strong>{money(item.total_minor, item.currency)}</strong></td>
                  <td style={{ color: item.balance_minor > 0 ? 'var(--warning-text)' : 'inherit' }}>
                    <strong>{money(item.balance_minor, item.currency)}</strong>
                    {item.balance_minor > 0 && <Charged minor={item.balance_minor} currency={item.currency} />}
                  </td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{formatDate(item.due_at)}</td>
                  <td className="row-actions">
                    {item.status === 'open' && item.kind !== 'topup' && balance >= item.balance_minor && (
                      <button className="primary-button compact" disabled={paying === item.id} onClick={() => void payWithBalance(item)}>
                        {t('余额支付')}
                      </button>
                    )}
                    {item.status === 'open' && item.kind !== 'topup' && balance < item.balance_minor && (
                      <a className={item.gateway ? 'secondary-button compact' : 'primary-button compact'} href={topupLink(item.balance_minor - balance)} title={t('余额 {0} 不足', money(balance, item.currency))}>
                        {t('余额不足，去充值')}
                      </a>
                    )}
                    {item.status === 'open' && item.gateway &&
                      (checkoutEnabled ? (
                        <button
                          className="primary-button compact"
                          disabled={paying === item.id}
                          onClick={() => checkout(item)}
                        >
                          {paying === item.id ? t('跳转中…') : t('立即支付')}
                        </button>
                      ) : (
                        <span style={{ color: 'var(--text-muted)', fontSize: '12px' }}>{t('请联系商家线下结算')}</span>
                      ))}
                  </td>
                </tr>
              ))}
              {!invoices.length && (
                <tr>
                  <td colSpan={6} className="empty-state">{t('暂无账单记录')}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>{t('付款交易流水')}</h3>
          <span className="tag">IMMUTABLE LEDGER</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t('流水参考号')}</th>
                <th>{t('关联账单')}</th>
                <th>{t('支付渠道')}</th>
                <th>{t('实付金额')}</th>
                <th>{t('流水状态')}</th>
                <th>{t('入账时间')}</th>
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
                  <td colSpan={6} className="empty-state">{t('暂无付款流水记录')}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
