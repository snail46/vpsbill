import { FormEvent, useEffect, useState } from 'react'
import { ArrowLeftRight, RefreshCw } from 'lucide-react'
import { api } from '../api'
import { formatTime } from '../shared/time'
import { toast } from '../shared/toast'
import { confirmDialog } from '../shared/dialog'
import { convertMinor, currencyName, plainMoney } from '../shared/currency'
import { t } from '../shared/i18n'

type LocaleSettingsRecord = {
  default_lang: 'zh' | 'en'
  cny_hidden: boolean
  usd_enabled: boolean
  default_currency: 'CNY' | 'USD'
  usd_rate: number
  usd_rate_auto: boolean
  usd_rate_updated_at?: string
}

// LocaleSettings sets the portal's languages and currencies: the language
// for visitors whose browser is neither Chinese nor English, which
// currencies are shown, the default one, and the USD rate amounts are
// converted at (payments stay in CNY).
export function LocaleSettings() {
  const [saved, setSaved] = useState<LocaleSettingsRecord | null>(null)
  const [defaultLang, setDefaultLang] = useState<'zh' | 'en'>('zh')
  const [cnyShown, setCnyShown] = useState(true)
  const [usdEnabled, setUsdEnabled] = useState(true)
  const [defaultCurrency, setDefaultCurrency] = useState<'CNY' | 'USD'>('CNY')
  const [rate, setRate] = useState('')
  const [auto, setAuto] = useState(true)
  const [busy, setBusy] = useState(false)
  const [fetching, setFetching] = useState(false)
  const [error, setError] = useState('')

  const show = (value: LocaleSettingsRecord) => {
    setSaved(value)
    setDefaultLang(value.default_lang)
    setCnyShown(!value.cny_hidden)
    setUsdEnabled(value.usd_enabled)
    setDefaultCurrency(value.default_currency)
    setRate(String(value.usd_rate))
    setAuto(value.usd_rate_auto)
  }

  useEffect(() => {
    api<LocaleSettingsRecord>('/api/v1/admin/settings/locale')
      .then(show)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [])

  // The default is always one of the shown currencies.
  const effectiveDefault = !cnyShown ? 'USD' : !usdEnabled ? 'CNY' : defaultCurrency

  async function save(event: FormEvent) {
    event.preventDefault()
    if (!cnyShown && !usdEnabled) {
      setError(t('至少要显示一种币种'))
      return
    }
    setBusy(true)
    setError('')
    try {
      show(await api<LocaleSettingsRecord>('/api/v1/admin/settings/locale', {
        method: 'PUT',
        body: JSON.stringify({ default_lang: defaultLang, cny_hidden: !cnyShown, usd_enabled: usdEnabled, default_currency: effectiveDefault, usd_rate: Number(rate), usd_rate_auto: auto }),
      }))
      toast('success', t('语言与币种设置已保存'), t('客户刷新页面后生效。'))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }

  async function fetchRate() {
    setFetching(true)
    setError('')
    try {
      const value = await api<LocaleSettingsRecord>('/api/v1/admin/settings/locale/rate', { method: 'POST' })
      show(value)
      toast('success', t('已更新为当前汇率'), `1 USD = ${value.usd_rate} CNY`)
    } catch (err) {
      const message = err instanceof Error ? err.message : t('获取汇率失败')
      setError(message)
      toast('error', t('获取汇率失败'), message)
    } finally {
      setFetching(false)
    }
  }

  const sample = Number(rate) > 0 ? (100 / Number(rate)).toFixed(2) : '—'
  return (
    <form className="panel" onSubmit={save}>
      <div className="panel-heading">
        <h3>{t('语言与币种')}</h3>
        <span className="tag">{t('客户前台')}</span>
      </div>
      <p className="muted-text">
        {t('前台提供中文和 English 两种语言，访客可在页面右上角切换；首次访问按浏览器语言显示。')}
        {t('币种可以只显示人民币、只显示美元，或两种都显示让访客切换。访客首次访问看到默认币种；充值金额、Telegram 绑定与签到邀请奖励都按默认币种填写和展示。')}
        {t('余额、套餐价格和账单按下方「记账币种」记账；用另一种币种查看时，金额按这里的汇率换算显示。管理后台始终按记账币种显示。')}
      </p>
      <div className="form-grid">
        <label>
          <span>{t('默认语言')}</span>
          <select value={defaultLang} onChange={event => setDefaultLang(event.target.value as 'zh' | 'en')}>
            <option value="zh">{t('中文')}</option>
            <option value="en">English</option>
          </select>
          <small>{t('浏览器语言既不是中文也不是英文的访客看到的语言。')}</small>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={cnyShown} onChange={event => setCnyShown(event.target.checked)} />
          {t('显示人民币（CNY）')}
        </label>
        <label className="check-row">
          <input type="checkbox" checked={usdEnabled} onChange={event => setUsdEnabled(event.target.checked)} />
          {t('显示美元（USD）')}
        </label>
        <label>
          <span>{t('默认币种')}</span>
          <select value={effectiveDefault} onChange={event => setDefaultCurrency(event.target.value as 'CNY' | 'USD')} disabled={!cnyShown || !usdEnabled}>
            {cnyShown && <option value="CNY">{t('人民币（CNY ¥）')}</option>}
            {usdEnabled && <option value="USD">{t('美元（USD $）')}</option>}
          </select>
          <small>{t('访客首次访问看到的币种；充值和 Telegram 奖励按它填写和展示。只显示一种币种时，前台不出现币种切换。')}</small>
        </label>
        <label>
          <span>{t('美元汇率（1 USD 兑多少 CNY）')}</span>
          <input type="number" min="1" max="100" step="0.0001" value={rate} onChange={event => setRate(event.target.value)} required disabled={!usdEnabled} />
          <small>
            {t('¥100.00 显示为 ${0}{1}', sample, saved?.usd_rate_updated_at ? t('；上次更新 {0}', formatTime(saved.usd_rate_updated_at)) : '')}
          </small>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={auto} onChange={event => setAuto(event.target.checked)} disabled={!usdEnabled} />
          {t('每 6 小时自动更新为市场汇率')}
        </label>
      </div>
      <div className="form-actions">
        <button type="button" className="secondary-button" disabled={fetching || !usdEnabled} onClick={() => void fetchRate()}>
          <RefreshCw size={15} />{fetching ? t('正在获取…') : t('立即获取当前汇率')}
        </button>
        <button className="primary-button" disabled={busy || !saved}>{busy ? t('正在保存…') : t('保存语言与币种')}</button>
      </div>
      {error && <div className="form-error">{error}</div>}
    </form>
  )
}

type LedgerRecord = {
  ledger_currency: 'CNY' | 'USD'
  usd_rate: number
  state: {
    balances: number
    balance_minor: number
    plan_prices: number
    open_invoices: number
    live_payments: number
    history: { from: string; to: string; usd_rate: number; created_at: string }[]
  }
}

// LedgerSettings shows the currency the books are kept in and lets the
// super administrator switch it: every balance, price and record is then
// converted at the current rate.
export function LedgerSettings() {
  const path = '/api/v1/admin/settings/ledger'
  const [data, setData] = useState<LedgerRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api<LedgerRecord>(path)
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [])

  if (!data) return error ? <div className="form-error">{error}</div> : null
  const from = data.ledger_currency
  const to = from === 'CNY' ? 'USD' : 'CNY'
  // What a hundred of the current currency becomes.
  const sample = `${plainMoney(10000, from)} → ${plainMoney(convertMinor(10000, from, to), to)}`

  async function change() {
    const sure = await confirmDialog({
      title: t('把记账币种从{0}切换为{1}？', currencyName(from), currencyName(to)),
      message: t('将按当前汇率 1 USD = {0} CNY 换算全部金额（{1}）：{2} 个账户的余额、{3} 个套餐价格、{4} 张待支付账单，以及历史订单、账单和流水。换算四舍五入到分，切换后请检查并调整套餐价格。建议先在「数据备份」里做一次备份。以后可以用同样的方式切换回来。', data!.usd_rate, sample, data!.state.balances, data!.state.plan_prices, data!.state.open_invoices),
      confirmText: t('切换为{0}', currencyName(to)),
      danger: true,
    })
    if (!sure) return
    setBusy(true)
    setError('')
    try {
      await api(path, { method: 'POST', body: JSON.stringify({ from, to }) })
      toast('success', t('记账币种已切换为{0}', currencyName(to)), t('页面即将刷新。'))
      window.setTimeout(() => window.location.reload(), 1200)
    } catch (err) {
      const message = err instanceof Error ? err.message : t('切换失败')
      setError(message)
      toast('error', t('切换失败'), message)
      setBusy(false)
    }
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <h3>{t('记账币种')}</h3>
        <span className="tag success">{currencyName(from)}</span>
      </div>
      <p className="muted-text">
        {t('账本只用一种币种：客户余额、套餐价格、订单、账单和流水都按它记账和扣款。切换时全部金额按当前汇率一次性换算，之后新的价格和充值都按新币种。')}
        {t('支付宝和易支付只收人民币：记账币种是美元时，在线支付按付款时的汇率折算成人民币收取，到账仍按美元记入。')}
      </p>
      <div className="profile-panel">
        <div><span>{t('当前记账币种')}</span><strong>{currencyName(from)}</strong></div>
        <div><span>{t('当前汇率')}</span><strong>1 USD = {data.usd_rate} CNY</strong></div>
        <div><span>{t('有余额的账户')}</span><strong>{t('{0} 个，合计 {1}', data.state.balances, plainMoney(data.state.balance_minor, from))}</strong></div>
        <div><span>{t('在售价格 / 待支付账单')}</span><strong>{data.state.plan_prices} / {data.state.open_invoices}</strong></div>
      </div>
      {data.state.live_payments > 0 && (
        <div className="note-banner warn">{t('有 {0} 笔在线支付正在进行，暂时不能切换。请先停用支付网关，等未完成的支付过期（30 分钟）后再切换。', data.state.live_payments)}</div>
      )}
      {data.state.history.length > 0 && (
        <p className="muted-text">
          {t('切换记录：')}
          {data.state.history.map(item => t('{0} {1} → {2}（1 USD = {3} CNY）', formatTime(item.created_at), item.from, item.to, item.usd_rate)).join(t('；'))}
        </p>
      )}
      <div className="form-actions">
        <button type="button" className="secondary-button" disabled={busy || data.state.live_payments > 0} onClick={() => void change()}>
          <ArrowLeftRight size={15} />{busy ? t('正在切换…') : t('切换为{0}记账', currencyName(to))}
        </button>
      </div>
      <p className="muted-text">{t('只有超级管理员可以切换。切换前请确认上方的汇率是你要用的汇率。')}</p>
      {error && <div className="form-error">{error}</div>}
    </div>
  )
}
