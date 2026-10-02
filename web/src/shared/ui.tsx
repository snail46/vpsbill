import { FormEvent, RefObject, useEffect, useState } from 'react'
import type { ContactInfo } from './contact'
import { Plus, Send, ShieldCheck } from 'lucide-react'
import { api, TicketDetailRecord } from '../api'
import { AttachmentGallery, AttachmentPicker } from '../TicketAttachments'
import { formatTime, startOfDay } from './time'
import { useSiteBrand, useSiteLogo } from './boot'
import { toast } from './toast'
import { t, tr } from './i18n'

export type Meta = {
  name: string
  environment: string
  installed: boolean
  capabilities: string[]
  password_reset_mail?: boolean
  surface?: 'portal' | 'admin' | ''
  admin_url?: string
  public_url?: string
  // logo_url is the site logo; empty shows the default mark.
  logo_url?: string
  logo_mode?: 'auto' | 'icon' | 'wordmark'
  // logo_dark_url is the dark theme's logo; empty uses logo_url.
  logo_dark_url?: string
  // favicon_url is the browser tab icon; empty uses the logo.
  favicon_url?: string
  trade_hold_days?: number
  ticket_attachment_max_mb?: number
  marketplace_enabled?: boolean
  // contact fills the portal's "联系我们" page.
  contact?: ContactInfo
  // telegram_enabled offers linking a Telegram account for rewards.
  telegram_enabled?: boolean
  // locale is the site's default language and the USD display rate.
  locale?: { default_lang?: 'zh' | 'en'; usd_enabled?: boolean; usd_rate?: number }
}

// useReveal brings a form that just opened into view, flashes it and puts
// the cursor in its first empty field, so a button far down the page that
// opens it visibly does something.
// It waits until ready, for forms whose fields arrive after a load.
export function useReveal(ref: RefObject<HTMLElement | null>, ready = true) {
  useEffect(() => {
    const element = ref.current
    if (!element || !ready) return
    const smooth = !window.matchMedia('(prefers-reduced-motion: reduce)').matches
    element.classList.add('reveal-flash')
    element.scrollIntoView({ behavior: smooth ? 'smooth' : 'auto', block: 'start' })
    const field = [...element.querySelectorAll<HTMLInputElement>('input:not([type=hidden]):not([type=checkbox]):not([disabled])')].find(input => !input.value)
    field?.focus({ preventScroll: true })
    const timer = window.setTimeout(() => element.classList.remove('reveal-flash'), 1600)
    return () => window.clearTimeout(timer)
  }, [ref, ready])
}

// BrandMark is the logo in the top-left corner and on the sign-in pages:
// the one set in the site settings, or the default mark.
export function BrandMark() {
  const logo = useSiteLogo()
  const [failed, setFailed] = useState('')
  if (logo && failed !== logo) return <img className="brand-logo" src={logo} alt="" onError={() => setFailed(logo)} />
  return <div className="brand-mark">VB</div>
}

// A logo at least this much wider than tall is taken for a word mark (one
// that carries the brand name) when the logo mode is auto.
const wordmarkRatio = 1.8

function readShape(url: string): boolean | null {
  try {
    const value = localStorage.getItem('vpsbill-logo-shape')
    const [key, wide] = (value ?? '').split('|')
    return key === url ? wide === '1' : null
  } catch {
    return null
  }
}

function saveShape(url: string, wide: boolean) {
  try {
    localStorage.setItem('vpsbill-logo-shape', `${url}|${wide ? '1' : '0'}`)
  } catch {
    // private mode: the shape is measured again next time
  }
}

// Brand is the logo with the site name and a subtitle, as in the top-left
// corner. A logo that already carries the brand name (a word mark) takes
// the name's place instead of repeating it beside the logo.
export function Brand({ name, subtitle, className = '' }: { name: string; subtitle: string; className?: string }) {
  const { logo, mode } = useSiteBrand()
  const [failed, setFailed] = useState('')
  const [measured, setMeasured] = useState<{ url: string; wide: boolean } | null>(null)
  const shown = logo && failed !== logo ? logo : ''
  const wide = measured?.url === shown ? measured.wide : shown ? readShape(shown) : null
  const wordmark = Boolean(shown) && (mode === 'wordmark' || (mode !== 'icon' && wide === true))
  const measure = (image: HTMLImageElement) => {
    if (!image.naturalWidth || !image.naturalHeight) return
    const value = image.naturalWidth / image.naturalHeight >= wordmarkRatio
    saveShape(shown, value)
    setMeasured({ url: shown, wide: value })
  }
  if (wordmark) {
    return (
      <div className={`brand brand-wordmark ${className}`.trim()}>
        <img className="brand-wordmark-logo" src={shown} alt={name} title={name} onLoad={event => measure(event.currentTarget)} onError={() => setFailed(shown)} />
        {subtitle && <span>{subtitle}</span>}
      </div>
    )
  }
  return (
    <div className={`brand ${className}`.trim()}>
      {shown ? (
        <img className="brand-logo" src={shown} alt="" onLoad={event => measure(event.currentTarget)} onError={() => setFailed(shown)} />
      ) : (
        <div className="brand-mark">VB</div>
      )}
      <div>
        <strong>{name}</strong>
        <span>{subtitle}</span>
      </div>
    </div>
  )
}

// SessionLoading shows only when loading takes a while (see styles.css),
// so a fast start does not flash a spinner.
export function SessionLoading({ portal }: { portal: 'admin' | 'customer' }) {
  return (
    <main className="session-loading delayed">
      <BrandMark />
      <div className="spinner" />
      <strong>{t('正在恢复{0}会话…', portal === 'admin' ? t('商家控制中心') : t('客户中心'))}</strong>
    </main>
  )
}

export function Field({
  label,
  value,
  onChange,
  type = 'text',
  autoComplete,
  hint,
  required = true,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  autoComplete?: string
  hint?: string
  required?: boolean
}) {
  return (
    <label className="field">
      <span>
        {label}
        {hint && <small>{hint}</small>}
      </span>
      <input
        required={required}
        value={value}
        type={type}
        autoComplete={autoComplete}
        onChange={event => onChange(event.target.value)}
      />
    </label>
  )
}

export function PageActions({
  eyebrow,
  title,
  description,
  action,
  actionLabel,
}: {
  eyebrow: string
  title: string
  description: string
  action: () => void
  actionLabel: string
}) {
  return (
    <div className="page-actions">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      <button className="primary-button compact" onClick={action}>
        <Plus size={16} />
        {actionLabel}
      </button>
    </div>
  )
}

export function JobError({ value }: { value?: string }) {
  if (!value) return <span className="job-error-empty">—</span>
  value = tr(value)
  if (value.length <= 100) return <span className="job-error-text">{value}</span>
  return (
    <details className="job-error-details">
      <summary>
        {value.slice(0, 100)}…<b>{t('展开诊断详情')}</b>
      </summary>
      <pre>{value}</pre>
    </details>
  )
}

// statusLabels name the statuses of services, jobs, invoices, orders and
// payments.
export const statusLabels: Record<string, string> = {
  online: t('在线'),
  active: t('正常'),
  disabled: t('已下架'),
  offline: t('离线'),
  pending_payment: t('待支付'),
  open: t('待支付'),
  paid: t('已支付'),
  fulfilling: t('开通中'),
  provisioning: t('开通中'),
  completed: t('已完成'),
  succeeded: t('成功'),
  failed: t('等待重试'),
  pending: t('排队中'),
  running: t('运行中'),
  creating: t('创建中'),
  stopped: t('已关机'),
  overdue: t('已逾期'),
  suspended: t('已暂停'),
  terminating: t('待删除'),
  terminated: t('已删除'),
  review: t('需人工审核'),
  uncollectible: t('无法收回'),
  missing: t('实例缺失'),
  unknown: t('未知'),
  error: t('异常'),
  dead: t('需人工处理'),
  cancelled: t('已取消'),
  void: t('已作废'),
  refunded: t('已退款'),
  draft: t('草稿'),
  fraud: t('风险拦截'),
  closed: t('已关闭'),
  locked: t('已锁定'),
}

export function statusLabel(status: string) {
  return statusLabels[status] ?? status
}

export function StatusBadge({ status }: { status: string }) {
  return <span className={`status-badge ${status}`}>{statusLabel(status)}</span>
}

export { cycleUnit as cycleLabel } from './cycles'

export function ticketStatusLabel(status: string) {
  return (
    ({
      open: t('待处理'),
      customer_reply: t('客户已回复'),
      staff_reply: t('已回复'),
      resolved: t('已解决'),
      closed: t('已关闭'),
    } as Record<string, string>)[status] || status
  )
}

export function ticketAuthorLabel(type: string) {
  return (
    ({
      customer: t('客户'),
      staff: t('工作人员'),
      host: t('母机机主'),
      system: t('系统通知'),
    } as Record<string, string>)[type] || type
  )
}

// bandwidthLabel names an instance's bandwidth by its download speed, the
// figure customers compare; 0 means unlimited.
export function bandwidthLabel(downMbps?: number) {
  return downMbps ? `${downMbps} Mbps` : t('不限带宽')
}

// money shows an amount in the visitor's display currency (see currency.ts).
export { money } from './currency'

export function formatBytes(value: number) {
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let size = value
  let index = 0
  while (size >= 1024 && index < units.length - 1) {
    size /= 1024
    index++
  }
  return index === 0 ? `${value} B` : `${size.toFixed(size >= 100 ? 0 : 1)} ${units[index]}`
}

// NodeExpiry shows a host's rental expiry, highlighted in its final week.
export function NodeExpiry({ date }: { date?: string }) {
  if (!date) return <span className="muted-text">{t('未设置到期')}</span>
  const days = Math.ceil((startOfDay(date).getTime() - Date.now()) / 86_400_000)
  const tone = days < 0 ? 'expiry-past' : days <= 7 ? 'expiry-soon' : ''
  return (
    <span className={tone}>
      {date}
      {days < 0 ? t(' · 已到期') : days <= 7 ? t(' · {0} 天后', days) : ''}
    </span>
  )
}

export function CapacityBar({
  label,
  used,
  total,
  suffix = '',
}: {
  label: string
  used: number
  total: number
  suffix?: string
}) {
  const rate = total ? Math.min(100, Math.round((used / total) * 100)) : 0
  return (
    <div className="capacity-row">
      <div>
        <span>{label}</span>
        <strong>
          {used.toLocaleString()}{suffix} / {total.toLocaleString()}{suffix}
        </strong>
      </div>
      <div className="capacity-track">
        <i style={{ width: `${rate}%` }} />
      </div>
      <small>{t('{0}% 已分配预留', rate)}</small>
    </div>
  )
}

export function TicketConversation({
  detail,
  onReply,
  admin = false,
  onStatus,
  maxMB,
  onError,
  attachmentBase,
}: {
  detail: TicketDetailRecord
  onReply: (body: string, internal: boolean, files: File[]) => Promise<boolean>
  admin?: boolean
  // attachmentBase overrides where attachments load from (host view).
  attachmentBase?: string
  onStatus?: (status: string) => void
  maxMB: number
  onError: (message: string) => void
}) {
  const [body, setBody] = useState('')
  const [internal, setInternal] = useState(false)
  const [files, setFiles] = useState<File[]>([])
  const [sending, setSending] = useState(false)

  useEffect(() => {
    setBody('')
    setInternal(false)
    setFiles([])
  }, [detail.ticket.id])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!body.trim() && !files.length) return
    setSending(true)
    if (await onReply(body, internal, files)) {
      setBody('')
      setInternal(false)
      setFiles([])
    }
    setSending(false)
  }

  return (
    <div className="panel conversation">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">{detail.ticket.number}</p>
          <h3>{detail.ticket.subject}</h3>
          <small>
            {[
              detail.ticket.customer_name && t('客户：{0}', detail.ticket.customer_name),
              detail.ticket.instance_name && t('关联实例：{0}', detail.ticket.instance_name),
            ].filter(Boolean).join(' · ') || t('创建于 {0}', formatTime(detail.ticket.created_at))}
          </small>
        </div>
        <span className={`ticket-state ${detail.ticket.status}`}>{ticketStatusLabel(detail.ticket.status)}</span>
      </div>

      <div className="message-list">
        {detail.messages.map(message => (
          <article key={message.id} className={`message ${message.author_type}${message.internal ? ' internal' : ''}`}>
            <header>
              <strong>{message.author_name || ticketAuthorLabel(message.author_type)}</strong>
              {message.author_type === 'host' && <span className="chat-role host">{t('机主')}</span>}
              {message.author_type === 'staff' && detail.ticket.host_account_id && <span className="chat-role staff">{t('平台')}</span>}
              <span>
                {message.internal ? t('内部备忘 · ') : ''}
                {formatTime(message.created_at)}
              </span>
            </header>
            {!(message.attachments?.length && message.body === '（图片附件）') && <p>{message.body}</p>}
            <AttachmentGallery ticketID={detail.ticket.id} attachments={message.attachments} admin={admin} base={attachmentBase} />
          </article>
        ))}
      </div>

      {detail.ticket.status !== 'closed' && (
        <form className="reply-form" onSubmit={submit}>
          <textarea
            name="body"
            rows={4}
            maxLength={10000}
            value={body}
            onChange={event => setBody(event.target.value)}
            placeholder={admin ? t('回复客户，或勾选内部备忘记录后台信息…') : t('请在此输入需要补充的信息…')}
            required={!files.length}
          />
          <AttachmentPicker files={files} onChange={setFiles} maxMB={maxMB} onError={onError} />
          <div className="reply-form-actions">
            {admin && (
              <label className="checkbox">
                <input type="checkbox" checked={internal} onChange={event => setInternal(event.target.checked)} /> {t('仅客服内部可见')}
              </label>
            )}
            <button className="primary-button compact" disabled={sending}>
              <Send size={14} />
              {sending ? t('正在发送…') : t('发送回复')}
            </button>
          </div>
        </form>
      )}

      {admin && (
        <div className="ticket-actions">
          <button className="secondary-button" onClick={() => onStatus?.('open')}>
            {t('重新标记待处理')}
          </button>
          <button className="secondary-button" onClick={() => onStatus?.('resolved')}>
            {t('标记已解决')}
          </button>
          <button className="secondary-button" onClick={() => onStatus?.('closed')}>
            {t('关闭工单')}
          </button>
        </div>
      )}
    </div>
  )
}

export function SecuritySettings({ enabled: initialEnabled, customer = false }: { enabled: boolean; customer?: boolean }) {
  const [enabled, setEnabled] = useState(initialEnabled)
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string } | null>(null)
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const base = customer ? '/api/v1/customer/auth/mfa' : '/api/v1/auth/mfa'

  async function start() {
    try {
      setSetup(await api<{ secret: string; otpauth_uri: string }>(`${base}/setup`, { method: 'POST' }))
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : t('设置失败'))
    }
  }

  async function confirm() {
    try {
      await api(`${base}/confirm`, { method: 'POST', body: JSON.stringify({ code }) })
      setEnabled(true)
      setSetup(null)
      setCode('')
      setError('')
      toast('success', t('二步验证已启用'))
    } catch (err) {
      toast('error', t('启用二步验证失败'), err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : t('验证码无效'))
    }
  }

  async function disable() {
    try {
      await api(`${base}/disable`, { method: 'POST', body: JSON.stringify({ code }) })
      setEnabled(false)
      setCode('')
      setError('')
      toast('success', t('二步验证已关闭'))
    } catch (err) {
      toast('error', t('关闭二步验证失败'), err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : t('验证码无效'))
    }
  }

  return (
    <>
    <PasswordSettings customer={customer} />
    <div className="panel security-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">TWO-FACTOR AUTHENTICATION</p>
          <h3>{t('二步验证（TOTP）')}</h3>
        </div>
        <span className={`status-badge ${enabled ? 'online' : 'disabled'}`}>
          {enabled ? t('已启用防护') : t('未启用')}
        </span>
      </div>

      {error && <div className="form-error">{error}</div>}

      {!enabled && !setup && (
        <>
          <p>
            {t('启用二步验证后，在每次登录时除输入密码外，还需输入验证器应用生成的 6 位动态验证码，有效保护您的云资产与账单安全。')}
          </p>
          <button className="primary-button compact" onClick={() => void start()}>
            <ShieldCheck size={16} />{t('开始配置二步验证')}
          </button>
        </>
      )}

      {setup && (
        <>
          <p>{t('请在 Authenticator 验证器中手动添加以下密钥，或直接点击配置链接：')}</p>
          <code className="mfa-secret">{setup.secret}</code>
          <a className="secondary-button mfa-link" href={setup.otpauth_uri}>
            {t('在系统默认验证器中打开')}
          </a>
          <label className="field" style={{ maxWidth: '320px', marginTop: '16px' }}>
            <span>{t('输入 6 位动态验证码确认绑定')}</span>
            <input
              value={code}
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              onChange={event => setCode(event.target.value)}
            />
          </label>
          <button className="primary-button compact" disabled={code.length !== 6} onClick={() => void confirm()}>
            {t('确认并启用')}
          </button>
        </>
      )}

      {enabled && (
        <>
          <p>{t('二步验证已在当前账号生效。如需停用，请先输入验证器中显示的 6 位验证码以确认身份。')}</p>
          <label className="field" style={{ maxWidth: '320px' }}>
            <span>{t('当前 6 位验证码')}</span>
            <input
              value={code}
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              onChange={event => setCode(event.target.value)}
            />
          </label>
          <button className="secondary-button" disabled={code.length !== 6} onClick={() => void disable()}>
            {t('关闭二步验证')}
          </button>
        </>
      )}
    </div>
    </>
  )
}

export function PasswordSettings({ customer }: { customer: boolean }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    if (next !== repeat) {
      setError(t('两次输入的新密码不一致'))
      return
    }
    setSaving(true)
    setError('')
    try {
      await api(customer ? '/api/v1/customer/auth/password' : '/api/v1/auth/password', {
        method: 'POST',
        body: JSON.stringify({ current_password: current, new_password: next }),
      })
      setCurrent('')
      setNext('')
      setRepeat('')
      setMessage(t('密码已更新，其他设备上的登录已全部退出。'))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('修改失败'))
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="panel security-panel" onSubmit={submit}>
      <div className="panel-heading">
        <div>
          <p className="eyebrow">PASSWORD</p>
          <h3>{t('修改登录密码')}</h3>
        </div>
      </div>
      {error && <div className="form-error">{error}</div>}
      {message && <div className="form-success">{message}</div>}
      <div className="password-fields">
        <Field label={t('当前密码')} value={current} onChange={setCurrent} type="password" autoComplete="current-password" />
        <Field label={t('新密码')} value={next} onChange={setNext} type="password" autoComplete="new-password" hint={t('至少 12 个字符')} />
        <Field label={t('确认新密码')} value={repeat} onChange={setRepeat} type="password" autoComplete="new-password" />
      </div>
      <button className="primary-button compact" disabled={saving || !current || !next || !repeat}>
        {saving ? t('正在保存…') : t('更新密码')}
      </button>
    </form>
  )
}
