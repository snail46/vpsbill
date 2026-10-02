import { FormEvent, RefObject, useEffect, useState } from 'react'
import type { ContactInfo } from './contact'
import { Plus, Send, ShieldCheck } from 'lucide-react'
import { api, TicketDetailRecord } from '../api'
import { AttachmentGallery, AttachmentPicker } from '../TicketAttachments'
import { formatTime, startOfDay } from './time'
import { useSiteBrand, useSiteLogo } from './boot'
import { toast } from './toast'

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
      <strong>正在恢复{portal === 'admin' ? '商家控制中心' : '客户中心'}会话…</strong>
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
  if (value.length <= 100) return <span className="job-error-text">{value}</span>
  return (
    <details className="job-error-details">
      <summary>
        {value.slice(0, 100)}…<b>展开诊断详情</b>
      </summary>
      <pre>{value}</pre>
    </details>
  )
}

// statusLabels name the statuses of services, jobs, invoices, orders and
// payments.
export const statusLabels: Record<string, string> = {
  online: '在线',
  active: '正常',
  disabled: '已下架',
  offline: '离线',
  pending_payment: '待支付',
  open: '待支付',
  paid: '已支付',
  fulfilling: '开通中',
  provisioning: '开通中',
  completed: '已完成',
  succeeded: '成功',
  failed: '等待重试',
  pending: '排队中',
  running: '运行中',
  creating: '创建中',
  stopped: '已关机',
  overdue: '已逾期',
  suspended: '已暂停',
  terminating: '待删除',
  terminated: '已删除',
  review: '需人工审核',
  uncollectible: '无法收回',
  missing: '实例缺失',
  unknown: '未知',
  error: '异常',
  dead: '需人工处理',
  cancelled: '已取消',
  void: '已作废',
  refunded: '已退款',
  draft: '草稿',
  fraud: '风险拦截',
  closed: '已关闭',
  locked: '已锁定',
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
      open: '待处理',
      customer_reply: '客户已回复',
      staff_reply: '已回复',
      resolved: '已解决',
      closed: '已关闭',
    } as Record<string, string>)[status] || status
  )
}

export function ticketAuthorLabel(type: string) {
  return (
    ({
      customer: '客户',
      staff: '工作人员',
      host: '母机机主',
      system: '系统通知',
    } as Record<string, string>)[type] || type
  )
}

// bandwidthLabel names an instance's bandwidth by its download speed, the
// figure customers compare; 0 means unlimited.
export function bandwidthLabel(downMbps?: number) {
  return downMbps ? `${downMbps} Mbps` : '不限带宽'
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
  if (!date) return <span className="muted-text">未设置到期</span>
  const days = Math.ceil((startOfDay(date).getTime() - Date.now()) / 86_400_000)
  const tone = days < 0 ? 'expiry-past' : days <= 7 ? 'expiry-soon' : ''
  return (
    <span className={tone}>
      {date}
      {days < 0 ? ' · 已到期' : days <= 7 ? ` · ${days} 天后` : ''}
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
      <small>{rate}% 已分配预留</small>
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
              detail.ticket.customer_name && `客户：${detail.ticket.customer_name}`,
              detail.ticket.instance_name && `关联实例：${detail.ticket.instance_name}`,
            ].filter(Boolean).join(' · ') || `创建于 ${formatTime(detail.ticket.created_at)}`}
          </small>
        </div>
        <span className={`ticket-state ${detail.ticket.status}`}>{ticketStatusLabel(detail.ticket.status)}</span>
      </div>

      <div className="message-list">
        {detail.messages.map(message => (
          <article key={message.id} className={`message ${message.author_type}${message.internal ? ' internal' : ''}`}>
            <header>
              <strong>{message.author_name || ticketAuthorLabel(message.author_type)}</strong>
              {message.author_type === 'host' && <span className="chat-role host">机主</span>}
              {message.author_type === 'staff' && detail.ticket.host_account_id && <span className="chat-role staff">平台</span>}
              <span>
                {message.internal ? '内部备忘 · ' : ''}
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
            placeholder={admin ? '回复客户，或勾选内部备忘记录后台信息…' : '请在此输入需要补充的信息…'}
            required={!files.length}
          />
          <AttachmentPicker files={files} onChange={setFiles} maxMB={maxMB} onError={onError} />
          <div className="reply-form-actions">
            {admin && (
              <label className="checkbox">
                <input type="checkbox" checked={internal} onChange={event => setInternal(event.target.checked)} /> 仅客服内部可见
              </label>
            )}
            <button className="primary-button compact" disabled={sending}>
              <Send size={14} />
              {sending ? '正在发送…' : '发送回复'}
            </button>
          </div>
        </form>
      )}

      {admin && (
        <div className="ticket-actions">
          <button className="secondary-button" onClick={() => onStatus?.('open')}>
            重新标记待处理
          </button>
          <button className="secondary-button" onClick={() => onStatus?.('resolved')}>
            标记已解决
          </button>
          <button className="secondary-button" onClick={() => onStatus?.('closed')}>
            关闭工单
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
      setError(err instanceof Error ? err.message : '设置失败')
    }
  }

  async function confirm() {
    try {
      await api(`${base}/confirm`, { method: 'POST', body: JSON.stringify({ code }) })
      setEnabled(true)
      setSetup(null)
      setCode('')
      setError('')
      toast('success', '二步验证已启用')
    } catch (err) {
      toast('error', '启用二步验证失败', err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : '验证码无效')
    }
  }

  async function disable() {
    try {
      await api(`${base}/disable`, { method: 'POST', body: JSON.stringify({ code }) })
      setEnabled(false)
      setCode('')
      setError('')
      toast('success', '二步验证已关闭')
    } catch (err) {
      toast('error', '关闭二步验证失败', err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : '验证码无效')
    }
  }

  return (
    <>
    <PasswordSettings customer={customer} />
    <div className="panel security-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">TWO-FACTOR AUTHENTICATION</p>
          <h3>二步验证（TOTP）</h3>
        </div>
        <span className={`status-badge ${enabled ? 'online' : 'disabled'}`}>
          {enabled ? '已启用防护' : '未启用'}
        </span>
      </div>

      {error && <div className="form-error">{error}</div>}

      {!enabled && !setup && (
        <>
          <p>
            启用二步验证后，在每次登录时除输入密码外，还需输入验证器应用生成的 6 位动态验证码，有效保护您的云资产与账单安全。
          </p>
          <button className="primary-button compact" onClick={() => void start()}>
            <ShieldCheck size={16} />开始配置二步验证
          </button>
        </>
      )}

      {setup && (
        <>
          <p>请在 Authenticator 验证器中手动添加以下密钥，或直接点击配置链接：</p>
          <code className="mfa-secret">{setup.secret}</code>
          <a className="secondary-button mfa-link" href={setup.otpauth_uri}>
            在系统默认验证器中打开
          </a>
          <label className="field" style={{ maxWidth: '320px', marginTop: '16px' }}>
            <span>输入 6 位动态验证码确认绑定</span>
            <input
              value={code}
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              onChange={event => setCode(event.target.value)}
            />
          </label>
          <button className="primary-button compact" disabled={code.length !== 6} onClick={() => void confirm()}>
            确认并启用
          </button>
        </>
      )}

      {enabled && (
        <>
          <p>二步验证已在当前账号生效。如需停用，请先输入验证器中显示的 6 位验证码以确认身份。</p>
          <label className="field" style={{ maxWidth: '320px' }}>
            <span>当前 6 位验证码</span>
            <input
              value={code}
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              onChange={event => setCode(event.target.value)}
            />
          </label>
          <button className="secondary-button" disabled={code.length !== 6} onClick={() => void disable()}>
            关闭二步验证
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
      setError('两次输入的新密码不一致')
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
      setMessage('密码已更新，其他设备上的登录已全部退出。')
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="panel security-panel" onSubmit={submit}>
      <div className="panel-heading">
        <div>
          <p className="eyebrow">PASSWORD</p>
          <h3>修改登录密码</h3>
        </div>
      </div>
      {error && <div className="form-error">{error}</div>}
      {message && <div className="form-success">{message}</div>}
      <div className="password-fields">
        <Field label="当前密码" value={current} onChange={setCurrent} type="password" autoComplete="current-password" />
        <Field label="新密码" value={next} onChange={setNext} type="password" autoComplete="new-password" hint="至少 12 个字符" />
        <Field label="确认新密码" value={repeat} onChange={setRepeat} type="password" autoComplete="new-password" />
      </div>
      <button className="primary-button compact" disabled={saving || !current || !next || !repeat}>
        {saving ? '正在保存…' : '更新密码'}
      </button>
    </form>
  )
}
