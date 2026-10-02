import { FormEvent, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api } from '../api'
import { formatTime } from '../shared/time'
import { toast } from '../shared/toast'
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
        {t('账本、账单和支付始终以人民币（CNY）记账：美元金额按下面的汇率换算，付款时按人民币金额结算。管理后台始终显示人民币。')}
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
