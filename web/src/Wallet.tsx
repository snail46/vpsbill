import { FormEvent, useEffect, useRef, useState } from 'react'
import { RefreshCw, WalletCards, X } from 'lucide-react'
import { api, cached, type CustomerCatalogRecord, type PaymentIntentRecord, type TopupInvoiceRecord, type WalletEntryRecord, type WalletRecord } from './api'
import { formatTime } from './shared/time'
import { confirmDialog } from './shared/dialog'
import { toast } from './shared/toast'
import { chargedMoney, converted, displayCurrency, money, plainMoney, toLedgerMinor } from './shared/currency'
import { Charged, CurrencyNote } from './shared/LocaleMenu'
import { t, tr } from './shared/i18n'

export const walletKindLabels: Record<WalletEntryRecord['kind'], string> = {
  topup: t('充值'),
  earning: t('托管收益'),
  payment: t('余额支付'),
  clearance_refund: t('清退补偿'),
  clearance_penalty: t('清退赔付'),
  adjustment: t('管理员调整'),
  refund: t('退款'),
  trade_purchase: t('交易市场购买'),
  trade_sale: t('交易市场售出'),
  reward: t('活动奖励'),
}

// walletMoney shows an amount in the visitor's display currency.
export const walletMoney = money

export function WalletLedger({ entries }: { entries: WalletEntryRecord[] }) {
  return (
    <div className="table-wrap">
      <table>
        <thead>
          <tr>
            <th>{t('时间')}</th>
            <th>{t('类型')}</th>
            <th>{t('说明')}</th>
            <th>{t('金额')}</th>
            <th>{t('变动后余额')}</th>
          </tr>
        </thead>
        <tbody>
          {entries.map(entry => (
            <tr key={entry.id}>
              <td>{formatTime(entry.created_at)}</td>
              <td>
                <span className={`tag wallet-${entry.kind}`}>{walletKindLabels[entry.kind] || entry.kind}</span>
              </td>
              <td>{tr(entry.description)}</td>
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
              <td colSpan={5} className="empty-state">{t('暂无余额变动')}</td>
            </tr>
          )}
        </tbody>
      </table>
    </div>
  )
}

// Top-ups are typed in the visitor's display currency and charged in CNY.
const quickAmounts = displayCurrency() === 'USD' ? [5, 10, 20, 50, 100] : [10, 50, 100, 200, 500]
const topupMinMinor = 100
const topupMaxMinor = 10_000_000

// topupLink opens the top-up form asking for at least what is short (in
// ledger minor units).
export function topupLink(shortMinor: number) {
  return `/portal/wallet?need=${Math.max(Math.ceil(shortMinor), topupMinMinor)}#topup`
}

// neededAmount is the amount a top-up link asks for, in the display
// currency and rounded up so that it covers what is short.
function neededAmount() {
  const need = Number(new URLSearchParams(window.location.search).get('need'))
  if (!Number.isFinite(need) || need <= 0) return ''
  const rate = toLedgerMinor(1) / 100
  return String(Math.ceil(Math.min(need, topupMaxMinor) / rate) / 100)
}

export default function CustomerWallet() {
  const [wallet, setWallet] = useState<WalletRecord | null>(() => cached<WalletRecord>('/api/v1/customer/wallet') ?? null)
  const [checkoutEnabled, setCheckoutEnabled] = useState(() => cached<CustomerCatalogRecord>('/api/v1/customer/catalog')?.checkout_enabled ?? false)
  const [amount, setAmount] = useState(() => neededAmount() || String(quickAmounts[1]))
  const needed = neededAmount()
  const [created, setCreated] = useState<TopupInvoiceRecord | null>(null)
  const [agreed, setAgreed] = useState(false)
  // nudge marks the terms when someone tries to pay without agreeing.
  const [nudge, setNudge] = useState(0)
  const agreeRef = useRef<HTMLInputElement>(null)
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
      // The overview's top-up button lands here with #topup.
      if (window.location.hash === '#topup') {
        window.setTimeout(() => {
          const form = document.getElementById('topup')
          form?.scrollIntoView({ block: 'center' })
          form?.querySelector<HTMLInputElement>('input[type=number]')?.focus()
        }, 0)
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('加载失败'))
    }
  }

  useEffect(() => {
    void load()
  }, [])

  // The terms block is remounted on each nudge (to replay its shake), so
  // its checkbox is focused once it is back.
  useEffect(() => {
    if (!nudge) return
    agreeRef.current?.focus({ preventScroll: true })
    agreeRef.current?.closest('.topup-terms')?.scrollIntoView({ behavior: 'smooth', block: 'center' })
  }, [nudge])

  async function topup(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const chargeMinor = toLedgerMinor(Number(amount))
    if (!agreed) {
      // The button stays clickable so the reason can be said where it
      // applies: the terms light up and their checkbox gets focus.
      setNudge(value => value + 1)
      toast('info', t('请先同意充值须知'), t('阅读下方充值须知并勾选「我已阅读并同意」后，才能生成充值账单。'))
      return
    }
    if (!Number.isFinite(chargeMinor) || chargeMinor < topupMinMinor || chargeMinor > topupMaxMinor) {
      setError(t('充值金额需在 {0} 到 {1} 之间', money(topupMinMinor), money(topupMaxMinor)))
      return
    }
    setBusy(true)
    setError('')
    try {
      const invoice = await api<TopupInvoiceRecord>('/api/v1/customer/wallet/topup', {
        method: 'POST',
        body: JSON.stringify({ amount_minor: chargeMinor, agree_terms: agreed }),
      })
      setCreated(invoice)
      if (checkoutEnabled) {
        const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${invoice.id}/checkout`, { method: 'POST' })
        window.location.assign(intent.checkout_url)
        return
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : t('创建充值账单失败'))
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
          <h2>{t('账户余额')}</h2>
          <p>{t('充值余额和托管收益只能用于本平台消费（购买、续费），不支持提现。')}</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />{t('刷新')}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}
      <CurrencyNote />

      <section className="metrics">
        <article>
          <WalletCards size={20} />
          <span>{t('当前余额')}</span>
          <strong className={(wallet?.balance_minor || 0) < 0 ? 'amount-negative' : ''}>{walletMoney(wallet?.balance_minor || 0, currency)}</strong>
        </article>
        <article>
          <span>{t('近期托管收益')}</span>
          <strong>{walletMoney(earned, currency)}</strong>
        </article>
      </section>
      {(wallet?.balance_minor || 0) < 0 && (
        <div className="note-banner warn">{t('余额为负，是以前的托管母机清退赔付尚未结清（现行规则下赔付不会再扣成负数）。结清前不能发布新母机，之后的托管收益会先用于抵扣。')}</div>
      )}

      <form className="panel" id="topup" onSubmit={topup}>
        <div className="panel-heading">
          <h3>{t('充值')}</h3>
          <span className="tag">{t('不可提现')}</span>
        </div>
        {needed && <div className="note-banner">{t('待支付的订单还差 {0}，已为你填好充值金额；也可以多充一些。充值到账后回到「账单」用余额支付。', plainMoney(Math.round(Number(needed) * 100), displayCurrency()))}</div>}
        <div className="topup-row">
          {quickAmounts.map(value => (
            <button type="button" key={value} className={amount === String(value) ? 'chip-button active' : 'chip-button'} onClick={() => setAmount(String(value))}>
              {plainMoney(value * 100, displayCurrency())}
            </button>
          ))}
          <label className="topup-amount">
            <span>{displayCurrency() === 'USD' ? t('金额（美元）') : t('金额（人民币 元）')}{converted() && Number(amount) > 0 && <small> {t('实付 {0}', chargedMoney(toLedgerMinor(Number(amount))))}</small>}</span>
            <input type="number" min="0.01" step="0.01" value={amount} onChange={event => setAmount(event.target.value)} />
          </label>
          <button className={agreed ? 'primary-button compact' : 'primary-button compact needs-agree'} disabled={busy}>
            {busy ? t('正在处理…') : checkoutEnabled ? t('前往支付') : t('生成充值账单')}
          </button>
        </div>
        <div key={nudge} className={nudge && !agreed ? 'topup-terms attention' : 'topup-terms'}>
          <strong>{t('充值须知')}</strong>
          <ul>
            <li>{t('请根据您的实际消费需求进行充值。我们建议「用多少充多少」，避免账户余额积压。')}</li>
            <li>{t('充值到账后，余额仅限用于平台服务消费，不支持提现或退款到原支付渠道，请知悉。')}</li>
          </ul>
          <label className="check-row">
            <input ref={agreeRef} type="checkbox" checked={agreed} onChange={event => setAgreed(event.target.checked)} />
            {t('我已阅读并同意以上充值须知')}
          </label>
          {nudge > 0 && !agreed && <small className="danger-text">{t('请先勾选同意充值须知，再生成充值账单。')}</small>}
        </div>
        {created && !checkoutEnabled && (
          <p className="muted-text">
            {t('已生成充值账单 {0}（{1}）。当前未配置在线支付，请联系商家线下付款，商家确认到账后余额自动增加。', created.number, chargedMoney(created.total_minor, created.currency))}
          </p>
        )}
      </form>

      <div className="panel">
        <div className="panel-heading">
          <h3>{t('余额明细')}</h3>
          <span className="tag">{t('{0} 条', entries.length)}</span>
        </div>
        <WalletLedger entries={entries} />
      </div>
    </section>
  )
}

// AdminWalletPanel shows a customer's balance history to staff and lets
// them record a correction with a reason.
export function AdminWalletPanel({ accountID, name, onClose, onChanged }: { accountID: string; name: string; onClose: () => void; onChanged: () => void }) {
  const [wallet, setWallet] = useState<WalletRecord | null>(() => cached<WalletRecord>(`/api/v1/admin/customers/${accountID}/wallet`) ?? null)
  const [amount, setAmount] = useState('')
  const [reason, setReason] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api<WalletRecord>(`/api/v1/admin/customers/${accountID}/wallet`)
      .then(setWallet)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [accountID])

  async function adjust(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const yuan = Number(amount)
    if (!Number.isFinite(yuan) || yuan === 0) {
      setError(t('请输入非零金额，负数表示扣减'))
      return
    }
    if (!(await confirmDialog({ title: t('为「{0}」{1}余额 ¥{2}？', name, yuan > 0 ? t('增加') : t('扣减'), Math.abs(yuan).toFixed(2)), message: t('调整会记入账户流水。'), confirmText: yuan > 0 ? t('增加余额') : t('扣减余额'), danger: yuan < 0 }))) return
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
      setError(err instanceof Error ? err.message : t('调整失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{t('{0} 的账户余额：{1}', name, walletMoney(wallet?.balance_minor || 0, wallet?.currency))}</h3>
          <p>{t('余额只能用于本平台消费，不可提现。调整会记录原因和操作人。')}</p>
        </div>
        <button className="icon-button" onClick={onClose} aria-label={t('关闭')}>
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <form className="topup-row" onSubmit={adjust}>
        <label className="topup-amount">
          <span>{t('调整金额（元，负数为扣减）')}</span>
          <input type="number" step="0.01" value={amount} onChange={event => setAmount(event.target.value)} />
        </label>
        <label className="topup-reason">
          <span>{t('原因')}</span>
          <input value={reason} maxLength={200} required onChange={event => setReason(event.target.value)} placeholder={t('例如 线下转账补录')} />
        </label>
        <button className="primary-button compact" disabled={busy}>{t('调整余额')}</button>
      </form>
      <WalletLedger entries={wallet?.entries || []} />
    </div>
  )
}
