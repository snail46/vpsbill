import { FormEvent, useEffect, useState } from 'react'
import { RefreshCw, WalletCards, X } from 'lucide-react'
import { api, type CustomerCatalogRecord, type PaymentIntentRecord, type TopupInvoiceRecord, type WalletEntryRecord, type WalletRecord } from './api'
import { formatTime } from './shared/time'

export const walletKindLabels: Record<WalletEntryRecord['kind'], string> = {
  topup: '充值',
  earning: '托管收益',
  payment: '余额支付',
  clearance_refund: '清退补偿',
  clearance_penalty: '清退赔付',
  adjustment: '管理员调整',
  refund: '退款',
  trade_purchase: '交易市场购买',
  trade_sale: '交易市场售出',
}

export function walletMoney(amountMinor: number, currency = 'CNY') {
  const symbol = currency === 'CNY' ? '¥' : `${currency} `
  const sign = amountMinor < 0 ? '-' : ''
  return `${sign}${symbol}${(Math.abs(amountMinor) / 100).toFixed(2)}`
}

export function WalletLedger({ entries }: { entries: WalletEntryRecord[] }) {
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>时间</th>
            <th>类型</th>
            <th>说明</th>
            <th>金额</th>
            <th>变动后余额</th>
          </tr>
        </thead>
        <tbody>
          {entries.map(entry => (
            <tr key={entry.id}>
              <td>{formatTime(entry.created_at)}</td>
              <td>
                <span className={`tag wallet-${entry.kind}`}>{walletKindLabels[entry.kind] || entry.kind}</span>
              </td>
              <td>{entry.description}</td>
              <td className={entry.amount_minor < 0 ? 'amount-negative' : 'amount-positive'}>
                <strong>
                  {entry.amount_minor > 0 ? '+' : ''}
                  {walletMoney(entry.amount_minor, entry.currency)}
                </strong>
              </td>
              <td>{walletMoney(entry.balance_after_minor, entry.currency)}</td>
            </tr>
          ))}
          {!entries.length && (
            <tr>
              <td colSpan={5} className="empty-state">暂无余额变动</td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

const quickAmounts = [10, 50, 100, 200, 500]

export default function CustomerWallet() {
  const [wallet, setWallet] = useState<WalletRecord | null>(null)
  const [checkoutEnabled, setCheckoutEnabled] = useState(false)
  const [amount, setAmount] = useState('50')
  const [created, setCreated] = useState<TopupInvoiceRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [w, c] = await Promise.all([
        api<WalletRecord>('/api/v1/customer/wallet'),
        api<CustomerCatalogRecord>('/api/v1/customer/catalog'),
      ])
      setWallet(w)
      setCheckoutEnabled(c.checkout_enabled)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function topup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const yuan = Number(amount)
    if (!Number.isFinite(yuan) || yuan < 1 || yuan > 100000) {
      setError('充值金额需在 ¥1 到 ¥100000 之间')
      return
    }
    setBusy(true)
    setError('')
    try {
      const invoice = await api<TopupInvoiceRecord>('/api/v1/customer/wallet/topup', {
        method: 'POST',
        body: JSON.stringify({ amount_minor: Math.round(yuan * 100) }),
      })
      setCreated(invoice)
      if (checkoutEnabled) {
        const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${invoice.id}/checkout`, { method: 'POST' })
        window.location.assign(intent.checkout_url)
        return
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建充值账单失败')
    } finally {
      setBusy(false)
    }
  }

  const currency = wallet?.currency || 'CNY'
  const entries = wallet?.entries || []
  const earned = entries.filter(item => item.kind === 'earning').reduce((sum, item) => sum + item.amount_minor, 0)

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">ACCOUNT BALANCE</p>
          <h2>账户余额</h2>
          <p>充值余额和托管收益只能用于本平台消费（购买、续费），不支持提现。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <section className="metrics">
        <article>
          <WalletCards size={20} />
          <span>当前余额</span>
          <strong className={(wallet?.balance_minor || 0) < 0 ? 'amount-negative' : ''}>{walletMoney(wallet?.balance_minor || 0, currency)}</strong>
        </article>
        <article>
          <span>近期托管收益</span>
          <strong>{walletMoney(earned, currency)}</strong>
        </article>
      </section>
      {(wallet?.balance_minor || 0) < 0 && (
        <div className="note-banner warn">余额为负，是托管母机清退时产生的赔付。结清前不能发布新母机，之后的托管收益会先用于抵扣。</div>
      )}

      <form className="panel" onSubmit={topup}>
        <div className="panel-heading">
          <h3>充值</h3>
          <span className="tag">不可提现</span>
        </div>
        <div className="topup-row">
          {quickAmounts.map(value => (
            <button type="button" key={value} className={amount === String(value) ? 'chip-button active' : 'chip-button'} onClick={() => setAmount(String(value))}>
              ¥{value}
            </button>
          ))}
          <label className="topup-amount">
            <span>金额（元）</span>
            <input type="number" min="1" max="100000" step="0.01" value={amount} onChange={event => setAmount(event.target.value)} />
          </label>
          <button className="primary-button compact" disabled={busy}>
            {busy ? '正在处理…' : checkoutEnabled ? '前往支付' : '生成充值账单'}
          </button>
        </div>
        {created && !checkoutEnabled && (
          <p className="muted-text">
            已生成充值账单 {created.number}（{walletMoney(created.total_minor, created.currency)}）。当前未配置在线支付，请联系商家线下付款，商家确认到账后余额自动增加。
          </p>
        )}
      </form>

      <div className="panel">
        <div className="panel-heading">
          <h3>余额明细</h3>
          <span className="tag">{entries.length} 条</span>
        </div>
        <WalletLedger entries={entries} />
      </div>
    </section>
  )
}

// AdminWalletPanel shows a customer's balance history to staff and lets
// them record a correction with a reason.
export function AdminWalletPanel({ accountID, name, onClose, onChanged }: { accountID: string; name: string; onClose: () => void; onChanged: () => void }) {
  const [wallet, setWallet] = useState<WalletRecord | null>(null)
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api<WalletRecord>(`/api/v1/admin/customers/${accountID}/wallet`)
      .then(setWallet)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  }, [accountID])

  async function adjust(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const yuan = Number(amount)
    if (!Number.isFinite(yuan) || yuan === 0) {
      setError('请输入非零金额，负数表示扣减')
      return
    }
    if (!window.confirm(`确认为「${name}」${yuan > 0 ? '增加' : '扣减'}余额 ¥${Math.abs(yuan).toFixed(2)}？`)) return
    setBusy(true)
    setError('')
    try {
      setWallet(await api<WalletRecord>(`/api/v1/admin/customers/${accountID}/wallet/adjust`, {
        method: 'POST',
        body: JSON.stringify({ amount_minor: Math.round(yuan * 100), reason }),
      }))
      setAmount('')
      setReason('')
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '调整失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{name} 的账户余额：{walletMoney(wallet?.balance_minor || 0, wallet?.currency)}</h3>
          <p>余额只能用于本平台消费，不可提现。调整会记录原因和操作人。</p>
        </div>
        <button className="icon-button" onClick={onClose} aria-label="关闭">
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <form className="topup-row" onSubmit={adjust}>
        <label className="topup-amount">
          <span>调整金额（元，负数为扣减）</span>
          <input type="number" step="0.01" value={amount} onChange={event => setAmount(event.target.value)} />
        </label>
        <label className="topup-reason">
          <span>原因</span>
          <input value={reason} maxLength={200} required onChange={event => setReason(event.target.value)} placeholder="例如 线下转账补录" />
        </label>
        <button className="primary-button compact" disabled={busy}>调整余额</button>
      </form>
      <WalletLedger entries={wallet?.entries || []} />
    </div>
  )
}
