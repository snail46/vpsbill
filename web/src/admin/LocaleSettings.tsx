import { FormEvent, useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api } from '../api'
import { formatTime } from '../shared/time'
import { toast } from '../shared/toast'

type LocaleSettingsRecord = {
  default_lang: 'zh' | 'en'
  usd_enabled: boolean
  usd_rate: number
  usd_rate_auto: boolean
  usd_rate_updated_at?: string
}

// LocaleSettings sets the portal's languages and display currency: the
// language for visitors whose browser is neither Chinese nor English, and
// the USD rate amounts are converted at (payments stay in CNY).
export function LocaleSettings() {
  const [saved, setSaved] = useState<LocaleSettingsRecord | null>(null)
  const [defaultLang, setDefaultLang] = useState<'zh' | 'en'>('zh')
  const [usdEnabled, setUsdEnabled] = useState(true)
  const [rate, setRate] = useState('')
  const [auto, setAuto] = useState(true)
  const [busy, setBusy] = useState(false)
  const [fetching, setFetching] = useState(false)
  const [error, setError] = useState('')

  const show = (value: LocaleSettingsRecord) => {
    setSaved(value)
    setDefaultLang(value.default_lang)
    setUsdEnabled(value.usd_enabled)
    setRate(String(value.usd_rate))
    setAuto(value.usd_rate_auto)
  }

  useEffect(() => {
    api<LocaleSettingsRecord>('/api/v1/admin/settings/locale')
      .then(show)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  }, [])

  async function save(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      show(await api<LocaleSettingsRecord>('/api/v1/admin/settings/locale', {
        method: 'PUT',
        body: JSON.stringify({ default_lang: defaultLang, usd_enabled: usdEnabled, usd_rate: Number(rate), usd_rate_auto: auto }),
      }))
      toast('success', '语言与币种设置已保存', '客户刷新页面后生效。')
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
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
      toast('success', '已更新为当前汇率', `1 USD = ${value.usd_rate} CNY`)
    } catch (err) {
      const message = err instanceof Error ? err.message : '获取汇率失败'
      setError(message)
      toast('error', '获取汇率失败', message)
    } finally {
      setFetching(false)
    }
  }

  const sample = Number(rate) > 0 ? (100 / Number(rate)).toFixed(2) : '—'
  return (
    <form className="panel" onSubmit={save}>
      <div className="panel-heading">
        <h3>语言与币种</h3>
        <span className="tag">客户前台</span>
      </div>
      <p className="muted-text">
        前台提供中文和 English 两种语言，访客可在页面右上角切换；首次访问按浏览器语言显示。账本、账单和支付始终以人民币（CNY）记账，
        美元只是显示币种：金额按下面的汇率换算给访客看，付款时仍按人民币金额结算。管理后台始终显示人民币。
      </p>
      <div className="form-grid">
        <label>
          <span>默认语言</span>
          <select value={defaultLang} onChange={event => setDefaultLang(event.target.value as 'zh' | 'en')}>
            <option value="zh">中文</option>
            <option value="en">English</option>
          </select>
          <small>浏览器语言既不是中文也不是英文的访客看到的语言。</small>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={usdEnabled} onChange={event => setUsdEnabled(event.target.checked)} />
          允许访客用美元（USD）查看金额
        </label>
        <label>
          <span>美元汇率（1 USD 兑多少 CNY）</span>
          <input type="number" min="1" max="100" step="0.0001" value={rate} onChange={event => setRate(event.target.value)} required disabled={!usdEnabled} />
          <small>
            ¥100.00 显示为 ${sample}
            {saved?.usd_rate_updated_at ? `；上次更新 ${formatTime(saved.usd_rate_updated_at)}` : ''}
          </small>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={auto} onChange={event => setAuto(event.target.checked)} disabled={!usdEnabled} />
          每 6 小时自动更新为市场汇率
        </label>
      </div>
      <div className="form-actions">
        <button type="button" className="secondary-button" disabled={fetching || !usdEnabled} onClick={() => void fetchRate()}>
          <RefreshCw size={15} />{fetching ? '正在获取…' : '立即获取当前汇率'}
        </button>
        <button className="primary-button" disabled={busy || !saved}>{busy ? '正在保存…' : '保存语言与币种'}</button>
      </div>
      {error && <div className="form-error">{error}</div>}
    </form>
  )
}
