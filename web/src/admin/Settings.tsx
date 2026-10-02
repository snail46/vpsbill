import { ChangeEvent, FormEvent, useEffect, useState } from 'react'
import { api, cached, PaymentSettingsRecord } from '../api'
import { ImageUp, RotateCcw } from 'lucide-react'
import { setSiteLogo, type LogoMode } from '../shared/boot'
import { Brand, StatusBadge } from '../shared/ui'
import type { ContactLink } from '../shared/contact'
import { ContactSettings } from './ContactSettings'

export function PaymentSettingsView() {
  const [settings, setSettings] = useState<PaymentSettingsRecord | null>(null)
  const [form, setForm] = useState({
    type: 'disabled',
    generic_base_url: '',
    generic_secret: '',
    alipay_app_id: '',
    alipay_private_key: '',
    alipay_public_key: '',
    alipay_gateway_url: 'https://openapi.alipay.com/gateway.do',
    epay_api_url: '',
    epay_partner_id: '',
    epay_merchant_key: '',
    epay_payment_type: 'alipay',
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    const fill = (value: PaymentSettingsRecord) => {
      setSettings(value)
      setForm(current => ({ ...current, ...value.gateway }))
    }
    // Show what was loaded before at once; the request brings it up to date.
    const known = cached<PaymentSettingsRecord>('/api/v1/admin/settings/payment')
    if (known) fill(known)
    api<PaymentSettingsRecord>('/api/v1/admin/settings/payment')
      .then(fill)
      .catch(err => setError(err.message))
  }, [])

  const update =
    (key: keyof typeof form) =>
    (event: ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
      setForm(current => ({ ...current, [key]: event.target.value }))

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setSaved(false)
    setError('')
    try {
      await api('/api/v1/admin/settings/payment', { method: 'PUT', body: JSON.stringify(form) })
      const value = await api<PaymentSettingsRecord>('/api/v1/admin/settings/payment')
      setSettings(value)
      setForm(current => ({
        ...current,
        ...value.gateway,
        generic_secret: '',
        alipay_private_key: '',
        alipay_public_key: '',
        epay_merchant_key: '',
      }))
      setSaved(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">PAYMENT GATEWAY</p>
          <h2>支付网关配置</h2>
          <p>全局启用一个收款渠道；密钥经 AES-256 加密保存且不回显明文。留空代表保留原密钥。</p>
        </div>
        <StatusBadge status={form.type === 'disabled' ? 'disabled' : 'online'} />
      </div>

      {error && <div className="form-error">{error}</div>}
      {saved && <div className="success-note">支付网关配置已更新并立即生效。</div>}

      <form className="panel payment-settings" onSubmit={submit}>
        <div className="gateway-options">
          <label className={form.type === 'disabled' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'disabled'}
              onChange={() => setForm(v => ({ ...v, type: 'disabled' }))}
            />
            <strong>停用在线支付</strong>
            <span>仅允许管理员在后台确认线下到账</span>
          </label>
          <label className={form.type === 'alipay_f2f' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'alipay_f2f'}
              onChange={() => setForm(v => ({ ...v, type: 'alipay_f2f' }))}
            />
            <strong>支付宝当面付</strong>
            <span>官方 RSA2 签名 / 预下单 Native 二维码</span>
          </label>
          <label className={form.type === 'epay' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'epay'}
              onChange={() => setForm(v => ({ ...v, type: 'epay' }))}
            />
            <strong>易支付（彩虹协议）</strong>
            <span>兼容标准易支付开放接口协议</span>
          </label>
          <label className={form.type === 'generic' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'generic'}
              onChange={() => setForm(v => ({ ...v, type: 'generic' }))}
            />
            <strong>通用 HMAC 收银台</strong>
            <span>对接企业自有或三方定制收银台</span>
          </label>
        </div>

        {form.type === 'generic' && (
          <div className="form-grid">
            <label>
              <span>外部收银台地址</span>
              <input type="url" value={form.generic_base_url} onChange={update('generic_base_url')} required />
            </label>
            <label>
              <span>HMAC 签名密钥 {settings?.gateway.generic_secret_configured ? '（已加密配置）' : ''}</span>
              <input
                type="password"
                value={form.generic_secret}
                onChange={update('generic_secret')}
                placeholder="留空保留已有密钥"
              />
            </label>
          </div>
        )}

        {form.type === 'alipay_f2f' && (
          <div className="form-grid">
            <label>
              <span>应用 App ID</span>
              <input value={form.alipay_app_id} onChange={update('alipay_app_id')} required />
            </label>
            <label>
              <span>网关地址</span>
              <input type="url" value={form.alipay_gateway_url} onChange={update('alipay_gateway_url')} required />
            </label>
            <label className="wide">
              <span>商户应用私钥 {settings?.gateway.alipay_private_key_configured ? '（已加密配置）' : ''}</span>
              <textarea
                rows={4}
                value={form.alipay_private_key}
                onChange={update('alipay_private_key')}
                placeholder="PKCS#1 / PKCS#8 格式，留空保留已有私钥"
              />
            </label>
            <label className="wide">
              <span>支付宝公钥 {settings?.gateway.alipay_public_key_configured ? '（已加密配置）' : ''}</span>
              <textarea
                rows={4}
                value={form.alipay_public_key}
                onChange={update('alipay_public_key')}
                placeholder="留空保留已有支付宝公钥"
              />
            </label>
          </div>
        )}

        {form.type === 'epay' && (
          <div className="form-grid">
            <label>
              <span>易支付接口根地址</span>
              <input
                type="url"
                value={form.epay_api_url}
                onChange={update('epay_api_url')}
                placeholder="https://pay.example.com/"
                required
              />
            </label>
            <label>
              <span>商户 ID（PID）</span>
              <input value={form.epay_partner_id} onChange={update('epay_partner_id')} required />
            </label>
            <label>
              <span>支付通道</span>
              <select value={form.epay_payment_type} onChange={update('epay_payment_type')}>
                <option value="alipay">支付宝</option>
                <option value="wxpay">微信支付</option>
                <option value="qqpay">QQ 钱包</option>
              </select>
            </label>
            <label>
              <span>商户密钥 {settings?.gateway.epay_merchant_key_configured ? '（已加密配置）' : ''}</span>
              <input
                type="password"
                value={form.epay_merchant_key}
                onChange={update('epay_merchant_key')}
                placeholder="留空保留已有密钥"
              />
            </label>
          </div>
        )}

        {form.type !== 'disabled' && settings && (
          <div className="callback-box">
            <span>网关异步回调通知地址（供在支付服务商后台配置）：</span>
            <code>{settings.callbacks[form.type]}</code>
          </div>
        )}

        <div className="form-actions">
          <button className="primary-button compact" disabled={saving}>
            {saving ? '正在保存…' : '保存并应用设置'}
          </button>
        </div>
      </form>
    </section>
  )
}

export type SiteSettingsRecord = {
  app_name: string
  public_url: string
  admin_url: string
  timezone: string
  worker_poll_interval: string
  reconcile_interval: string
  lifecycle_interval: string
  renewal_lead_time: string
  overdue_grace_period: string
  termination_retention: string
  notification_webhook_url: string
  notification_webhook_secret_configured: boolean
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_password_configured: boolean
  smtp_from: string
  smtp_security: string
  password_reset_mail_enabled: boolean
  mail_notifications: MailNotificationSettings
  ticket_attachment_max_mb: number
  marketplace: MarketplaceSettings
  // logo_url is the logo shown; logo_external_url is set when it is linked
  // from another site rather than uploaded.
  logo_url: string
  logo_external_url: string
  logo_mode: LogoMode
  logo_dark_url?: string
  logo_dark_external_url?: string
  // contact_intro and contact_links fill the portal's "联系我们" page.
  contact_intro?: string
  contact_links?: ContactLink[]
  // proxy_warning explains a reverse proxy that hides visitors' addresses.
  proxy_warning?: string
}

export type MarketplaceSettings = {
  enabled: boolean
  fee_percent: number
  offline_hours: number
  trade_fee_percent: number
  trade_hold_days: number
  max_overcommit_cpu: number
  max_overcommit_ram: number
  max_overcommit_disk: number
  max_overcommit_traffic: number
}

export type MailNotificationSettings = {
  admin_emails: string
  customer_expiry: boolean
  customer_traffic: boolean
  customer_ticket_reply: boolean
  admin_node_expiry: boolean
  admin_node_traffic: boolean
  admin_ticket: boolean
  expiry_reminder_days: number
  node_expiry_reminder_days: number
  traffic_alert_percent: number
}

export const defaultMailNotifications: MailNotificationSettings = {
  admin_emails: '',
  customer_expiry: true,
  customer_traffic: true,
  customer_ticket_reply: true,
  admin_node_expiry: true,
  admin_node_traffic: true,
  admin_ticket: true,
  expiry_reminder_days: 3,
  node_expiry_reminder_days: 7,
  traffic_alert_percent: 80,
}

export function SiteSettingsView() {
  const [settings, setSettings] = useState<SiteSettingsRecord | null>(null)
  const [form, setForm] = useState({
    app_name: '',
    public_url: '',
    admin_url: '',
    timezone: 'Asia/Shanghai',
    worker_poll_interval: '',
    reconcile_interval: '',
    lifecycle_interval: '',
    renewal_lead_time: '',
    overdue_grace_period: '',
    termination_retention: '',
    notification_webhook_url: '',
    notification_webhook_secret: '',
    smtp_host: '',
    smtp_port: '587',
    smtp_username: '',
    smtp_password: '',
    smtp_from: '',
    smtp_security: 'starttls',
  })
  const [clearPassword, setClearPassword] = useState(false)
  const [notifications, setNotifications] = useState<MailNotificationSettings>(defaultMailNotifications)
  const [attachmentMB, setAttachmentMB] = useState('5')
  const [marketplace, setMarketplace] = useState<MarketplaceSettings>({
    enabled: true, fee_percent: 20, offline_hours: 24, trade_fee_percent: 20, trade_hold_days: 31,
    max_overcommit_cpu: 4, max_overcommit_ram: 1.5, max_overcommit_disk: 2, max_overcommit_traffic: 3,
  })
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const apply = (value: SiteSettingsRecord) => {
    setSettings(value)
    setForm(current => ({
      ...current,
      app_name: value.app_name,
      public_url: value.public_url,
      admin_url: value.admin_url || '',
      timezone: value.timezone,
      worker_poll_interval: value.worker_poll_interval,
      reconcile_interval: value.reconcile_interval,
      lifecycle_interval: value.lifecycle_interval,
      renewal_lead_time: value.renewal_lead_time,
      overdue_grace_period: value.overdue_grace_period,
      termination_retention: value.termination_retention,
      notification_webhook_url: value.notification_webhook_url,
      notification_webhook_secret: '',
      smtp_host: value.smtp_host,
      smtp_port: String(value.smtp_port || 587),
      smtp_username: value.smtp_username,
      smtp_password: '',
      smtp_from: value.smtp_from,
      smtp_security: value.smtp_security || 'starttls',
    }))
    setClearPassword(false)
    setNotifications({ ...defaultMailNotifications, ...value.mail_notifications })
    setAttachmentMB(String(value.ticket_attachment_max_mb || 5))
    if (value.marketplace) setMarketplace(current => ({ ...current, ...value.marketplace }))
  }

  useEffect(() => {
    // Show what was loaded before at once; the request brings it up to date.
    const known = cached<SiteSettingsRecord>('/api/v1/admin/settings/site')
    if (known) apply(known)
    api<SiteSettingsRecord>('/api/v1/admin/settings/site')
      .then(apply)
      .catch(err => setError(err.message))
  }, [])

  const update =
    (key: keyof typeof form) =>
    (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm(current => ({ ...current, [key]: event.target.value }))

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setNotice('')
    setError('')
    try {
      const value = await api<SiteSettingsRecord>('/api/v1/admin/settings/site', {
        method: 'PUT',
        body: JSON.stringify({
          ...form,
          smtp_port: Number(form.smtp_port) || 0,
          clear_smtp_password: clearPassword,
          mail_notifications: notifications,
          ticket_attachment_max_mb: Number(attachmentMB) || 0,
          marketplace,
        }),
      })
      apply(value)
      setNotice('站点设置已保存并立即生效。')
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  async function sendTest() {
    setTesting(true)
    setNotice('')
    setError('')
    try {
      const result = await api<{ to: string }>('/api/v1/admin/settings/site/test-mail', { method: 'POST' })
      setNotice(`测试邮件已发送到 ${result.to}，请检查收件箱和垃圾邮件。`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '发送失败')
    } finally {
      setTesting(false)
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">SITE SETTINGS</p>
          <h2>站点设置</h2>
          <p>站点名称、公开地址、自动化周期、通知与发信邮箱。支付回调和密码重置链接都基于公开地址，换域名后要同步修改。密钥加密保存且不回显，留空代表保留原值。</p>
        </div>
      </div>

      {error && <div className="form-error">{error}</div>}
      {notice && <div className="success-note">{notice}</div>}

      {settings?.proxy_warning && <div className="note-banner warn" role="alert">{settings.proxy_warning}</div>}
      {settings && (
        <LogoSettings
          key={settings.logo_url + '|' + (settings.logo_dark_url ?? '')}
          current={settings.logo_url}
          external={settings.logo_external_url}
          dark={settings.logo_dark_url ?? ''}
          darkExternal={settings.logo_dark_external_url ?? ''}
          mode={settings.logo_mode || 'auto'}
          siteName={settings.app_name}
        />
      )}
      {settings && <ContactSettings intro={settings.contact_intro ?? ''} links={settings.contact_links ?? []} />}

      <form className="panel site-settings" onSubmit={submit}>
        <div className="form-grid">
          <fieldset className="wide">
            <legend>基本信息</legend>
          </fieldset>
          <label>
            <span>站点名称</span>
            <input value={form.app_name} onChange={update('app_name')} required />
          </label>
          <label>
            <span>公开访问地址</span>
            <input type="url" value={form.public_url} onChange={update('public_url')} placeholder="https://billing.example.com" required />
            <small>客户前台地址，用于邮件链接、支付回调和 Agent 连接。</small>
          </label>
          <label>
            <span>后台访问地址（可选）</span>
            <input type="url" value={form.admin_url} onChange={update('admin_url')} placeholder="https://admin.example.com" />
            <small>管理后台单独使用域名或端口时填写，发给管理员的邮件链接会指向这里。</small>
          </label>

          <fieldset className="wide">
            <legend>续费与自动化（格式如 30s、5m、72h）</legend>
          </fieldset>
          <label>
            <span>续费账单提前生成</span>
            <input value={form.renewal_lead_time} onChange={update('renewal_lead_time')} />
          </label>
          <label>
            <span>逾期宽限期（到期后暂停）</span>
            <input value={form.overdue_grace_period} onChange={update('overdue_grace_period')} />
          </label>
          <label>
            <span>暂停后保留（到期后删除）</span>
            <input value={form.termination_retention} onChange={update('termination_retention')} />
          </label>
          <label>
            <span>任务轮询间隔</span>
            <input value={form.worker_poll_interval} onChange={update('worker_poll_interval')} />
          </label>
          <label>
            <span>对账间隔</span>
            <input value={form.reconcile_interval} onChange={update('reconcile_interval')} />
          </label>
          <label>
            <span>生命周期检查间隔</span>
            <input value={form.lifecycle_interval} onChange={update('lifecycle_interval')} />
          </label>

          <fieldset className="wide">
            <legend>通知 Webhook（可选）</legend>
          </fieldset>
          <label>
            <span>通知地址</span>
            <input type="url" value={form.notification_webhook_url} onChange={update('notification_webhook_url')} placeholder="留空则不投递" />
          </label>
          <label>
            <span>签名密钥 {settings?.notification_webhook_secret_configured ? '（已加密配置）' : ''}</span>
            <input
              type="password"
              value={form.notification_webhook_secret}
              onChange={update('notification_webhook_secret')}
              placeholder="至少 32 位，留空保留或自动生成"
              autoComplete="new-password"
            />
          </label>
          <div />

          <fieldset className="wide">
            <legend>
              发信邮箱（SMTP）· 用于客户找回密码
              {settings && (
                <span className={`tag ${settings.password_reset_mail_enabled ? 'tag-ok' : ''}`}>
                  {settings.password_reset_mail_enabled ? '已启用' : '未配置'}
                </span>
              )}
            </legend>
          </fieldset>
          <label>
            <span>SMTP 服务器</span>
            <input value={form.smtp_host} onChange={update('smtp_host')} placeholder="smtp.example.com，留空不发信" />
          </label>
          <label>
            <span>端口</span>
            <input type="number" min={1} max={65535} value={form.smtp_port} onChange={update('smtp_port')} />
          </label>
          <label>
            <span>加密方式</span>
            <select value={form.smtp_security} onChange={update('smtp_security')}>
              <option value="starttls">STARTTLS（通常 587）</option>
              <option value="tls">SSL/TLS（通常 465）</option>
              <option value="none">不加密（仅限内网中继）</option>
            </select>
          </label>
          <label>
            <span>发件人</span>
            <input value={form.smtp_from} onChange={update('smtp_from')} placeholder="VPSBill <noreply@example.com>" />
          </label>
          <label>
            <span>登录账号</span>
            <input value={form.smtp_username} onChange={update('smtp_username')} autoComplete="off" />
          </label>
          <label>
            <span>登录密码 {settings?.smtp_password_configured ? '（已加密配置）' : ''}</span>
            <input
              type="password"
              value={form.smtp_password}
              onChange={update('smtp_password')}
              placeholder="留空保留已有密码"
              autoComplete="new-password"
              disabled={clearPassword}
            />
          </label>
          {settings?.smtp_password_configured && (
            <label className="checkbox wide">
              <input type="checkbox" checked={clearPassword} onChange={event => setClearPassword(event.target.checked)} />
              清除已保存的 SMTP 密码
            </label>
          )}

          <fieldset className="wide">
            <legend>邮件通知（需要先配置 SMTP）</legend>
          </fieldset>
          <fieldset>
            <legend>发给客户</legend>
            {(
              [
                ['customer_expiry', '实例即将到期（续费账单未支付）'],
                ['customer_traffic', '实例月流量告警'],
                ['customer_ticket_reply', '工单收到客服回复'],
              ] as const
            ).map(([key, label]) => (
              <label key={key} className="checkbox notify-option">
                <input
                  type="checkbox"
                  checked={notifications[key]}
                  onChange={event => setNotifications(current => ({ ...current, [key]: event.target.checked }))}
                />
                {label}
              </label>
            ))}
          </fieldset>
          <fieldset>
            <legend>发给管理员</legend>
            {(
              [
                ['admin_node_expiry', '母鸡即将到期'],
                ['admin_node_traffic', '母鸡月流量告警'],
                ['admin_ticket', '新工单与客户回复'],
              ] as const
            ).map(([key, label]) => (
              <label key={key} className="checkbox notify-option">
                <input
                  type="checkbox"
                  checked={notifications[key]}
                  onChange={event => setNotifications(current => ({ ...current, [key]: event.target.checked }))}
                />
                {label}
              </label>
            ))}
          </fieldset>
          <label>
            <span>管理员通知邮箱</span>
            <input
              value={notifications.admin_emails}
              onChange={event => setNotifications(current => ({ ...current, admin_emails: event.target.value }))}
              placeholder="多个用逗号分隔，留空发给所有管理员"
            />
          </label>
          <label>
            <span>实例到期提前提醒（天）</span>
            <input
              type="number"
              min={1}
              max={30}
              value={notifications.expiry_reminder_days}
              onChange={event => setNotifications(current => ({ ...current, expiry_reminder_days: Number(event.target.value) }))}
            />
          </label>
          <label>
            <span>母鸡到期提前提醒（天）</span>
            <input
              type="number"
              min={1}
              max={60}
              value={notifications.node_expiry_reminder_days}
              onChange={event => setNotifications(current => ({ ...current, node_expiry_reminder_days: Number(event.target.value) }))}
            />
          </label>
          <label>
            <span>流量告警阈值（%，用尽时另发一封）</span>
            <input
              type="number"
              min={50}
              max={99}
              value={notifications.traffic_alert_percent}
              onChange={event => setNotifications(current => ({ ...current, traffic_alert_percent: Number(event.target.value) }))}
            />
          </label>

          <fieldset className="wide">
            <legend>工单附件</legend>
          </fieldset>
          <label>
            <span>单张图片大小上限（MB，1–20）</span>
            <input type="number" min={1} max={20} value={attachmentMB} onChange={event => setAttachmentMB(event.target.value)} />
          </label>

          <fieldset className="wide">
            <legend>托管中心</legend>
            <label className="notify-option">
              <input type="checkbox" checked={marketplace.enabled} onChange={event => setMarketplace(current => ({ ...current, enabled: event.target.checked }))} />
              允许用户发布和购买托管母机
            </label>
          </fieldset>
          <label>
            <span>每笔托管交易手续费（%，0–90）</span>
            <input type="number" min={0} max={90} step={0.5} value={marketplace.fee_percent} onChange={event => setMarketplace(current => ({ ...current, fee_percent: Number(event.target.value) }))} />
          </label>
          <label>
            <span>母鸡离线多少小时后自动清退（1–720）</span>
            <input type="number" min={1} max={720} value={marketplace.offline_hours} onChange={event => setMarketplace(current => ({ ...current, offline_hours: Number(event.target.value) }))} />
          </label>
          <label>
            <span>交易市场每笔成交手续费（%，0–90，卖家承担）</span>
            <input type="number" min={0} max={90} step={0.5} value={marketplace.trade_fee_percent} onChange={event => setMarketplace(current => ({ ...current, trade_fee_percent: Number(event.target.value) }))} />
          </label>
          <label>
            <span>持有满多少天后可在交易市场挂售（0–365，0 为不限）</span>
            <input type="number" min={0} max={365} step={1} value={marketplace.trade_hold_days} onChange={event => setMarketplace(current => ({ ...current, trade_hold_days: Number(event.target.value) }))} />
          </label>

          <fieldset className="wide">
            <legend>超售倍数上限（所有母机，含平台自营）</legend>
            <small>母机可售资源 = Agent 检测的真实资源 × 母机设置的倍数，倍数不能超过这里的上限，并公开显示给买家。</small>
          </fieldset>
          {([
            ['max_overcommit_cpu', 'CPU（建议 4）'],
            ['max_overcommit_ram', '内存（建议 1.5）'],
            ['max_overcommit_disk', '硬盘（建议 2）'],
            ['max_overcommit_traffic', '月流量（建议 3）'],
          ] as const).map(([key, label]) => (
            <label key={key}>
              <span>{label}</span>
              <input type="number" min={1} max={20} step={0.1} value={marketplace[key]} onChange={event => setMarketplace(current => ({ ...current, [key]: Number(event.target.value) }))} />
            </label>
          ))}
        </div>

        <div className="form-actions">
          <button
            type="button"
            className="secondary-button compact"
            disabled={testing || !settings?.password_reset_mail_enabled}
            onClick={() => void sendTest()}
            title={settings?.password_reset_mail_enabled ? '发送到当前管理员邮箱' : '先保存 SMTP 设置'}
          >
            {testing ? '正在发送…' : '发送测试邮件'}
          </button>
          <button className="primary-button compact" disabled={saving}>
            {saving ? '正在保存…' : '保存设置'}
          </button>
        </div>
      </form>
    </section>
  )
}

// LogoSettings changes the logo in the top-left corner on its own, apart
// from the site settings form: upload an image or link one.
type LogoResult = { logo_url: string; logo_external_url: string; logo_mode?: LogoMode; logo_dark_url?: string; logo_dark_external_url?: string }
type LogoVariant = '' | 'dark'

// LogoSettings sets the logo for the light theme and, optionally, a second
// one for the dark theme (a white logo, say); without it the dark theme
// shows the light one.
function LogoSettings(props: { current: string; external: string; dark: string; darkExternal: string; mode: LogoMode; siteName: string }) {
  const [logos, setLogos] = useState({ '': props.current, dark: props.dark })
  const [links, setLinks] = useState({ '': props.external, dark: props.darkExternal })
  const [mode, setMode] = useState<LogoMode>(props.mode)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  async function save(request: Promise<LogoResult>, message: string) {
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const result = await request
      const dark = result.logo_dark_url ?? logos.dark
      setLogos({ '': result.logo_url, dark })
      setLinks({ '': result.logo_external_url, dark: result.logo_dark_external_url ?? links.dark })
      if (result.logo_mode) setMode(result.logo_mode)
      setSiteLogo(result.logo_url, result.logo_mode, dark)
      setNotice(message)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const query = (variant: LogoVariant) => (variant ? `?variant=${variant}` : '')

  function upload(variant: LogoVariant, event: ChangeEvent<HTMLInputElement>) {
    const file = event.target.files?.[0]
    event.target.value = ''
    if (!file) return
    if (file.size > 512 * 1024) {
      setError('图片不能超过 512 KB')
      return
    }
    const body = new FormData()
    body.append('file', file)
    void save(api(`/api/v1/admin/settings/logo${query(variant)}`, { method: 'POST', body }), `${variant ? '夜间' : '白天'}主题 Logo 已上传，前台和后台立即生效。`)
  }

  const linkLogo = (variant: LogoVariant, url: string, message: string) =>
    save(api(`/api/v1/admin/settings/logo${query(variant)}`, { method: 'PUT', body: JSON.stringify({ url }) }), message)

  const changeMode = (value: LogoMode) =>
    save(api('/api/v1/admin/settings/logo/mode', { method: 'PUT', body: JSON.stringify({ mode: value }) }), '显示方式已保存，前台和后台立即生效。')

  const slots: [LogoVariant, string, string][] = [
    ['', '白天主题', '浅色背景下显示，也用作浏览器标签图标。'],
    ['dark', '夜间主题（可选）', '深色背景下显示，例如白色字的 Logo；不设置时夜间也用白天主题的 Logo。'],
  ]

  return (
    <section className="panel logo-settings">
      <div className="panel-heading">
        <h3>站点 Logo</h3>
      </div>
      <p className="muted-text logo-settings-intro">显示在前台和后台左上角、登录页和浏览器标签上。支持 SVG、PNG、JPG、WebP、GIF、ICO，不超过 512 KB；按 36 像素高显示。白天和夜间主题可以各传一张，页面随访客的主题自动切换。</p>
      <div className="logo-variants">
        {slots.map(([variant, title, help]) => {
          const current = logos[variant]
          return (
            <div className={variant ? 'logo-variant dark-preview' : 'logo-variant light-preview'} key={variant || 'light'}>
              <div className="logo-variant-head">
                <strong>{title}</strong>
                <small>{help}</small>
              </div>
              <div className="logo-preview" aria-label={`${title}预览`}>
                {current || (variant && logos['']) ? (
                  <img className="logo-preview-image" src={current || logos['']} alt="" />
                ) : (
                  <div className="brand-mark">VB</div>
                )}
                <span>{props.siteName || 'VPSBill'}</span>
                {variant && !current && <em>沿用白天主题</em>}
              </div>
              <div className="form-actions">
                <label className={busy ? 'primary-button compact disabled' : 'primary-button compact'}>
                  <ImageUp size={15} />
                  上传图片
                  <input type="file" accept=".svg,.png,.jpg,.jpeg,.webp,.gif,.ico,image/*" hidden disabled={busy} onChange={event => upload(variant, event)} />
                </label>
                {current && (
                  <button type="button" className="secondary-button compact" disabled={busy} onClick={() => void linkLogo(variant, '', variant ? '已移除夜间主题 Logo，夜间改用白天主题的 Logo。' : '已恢复默认 Logo。')}>
                    <RotateCcw size={15} />
                    {variant ? '移除' : '恢复默认'}
                  </button>
                )}
              </div>
              <form
                className="input-with-button"
                onSubmit={event => {
                  event.preventDefault()
                  void linkLogo(variant, links[variant], '已改用该地址的 Logo。')
                }}
              >
                <input type="url" value={links[variant]} onChange={event => setLinks(current => ({ ...current, [variant]: event.target.value }))} placeholder="或填写图片地址 https://…" aria-label={`${title} Logo 图片地址`} />
                <button className="secondary-button compact" disabled={busy || !links[variant].trim()}>使用此地址</button>
              </form>
            </div>
          )
        })}
      </div>
      <fieldset className="logo-mode" disabled={busy}>
        <legend>显示方式</legend>
        {([
          ['auto', '自动识别', '横向的长条 Logo 当作已含品牌名，方形 Logo 旁边显示站点名称'],
          ['icon', '图标 + 站点名称', 'Logo 是图标，旁边照常显示站点名称'],
          ['wordmark', 'Logo 已含品牌名', 'Logo 放在站点名称的位置，不再重复显示名称'],
        ] as [LogoMode, string, string][]).map(([value, label, help]) => (
          <label key={value} className="radio-option">
            <input type="radio" name="logo_mode" value={value} checked={mode === value} onChange={() => void changeMode(value)} />
            <span>
              <strong>{label}</strong>
              <small>{help}</small>
            </span>
          </label>
        ))}
      </fieldset>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="success-note">{notice}</div>}
    </section>
  )
}
