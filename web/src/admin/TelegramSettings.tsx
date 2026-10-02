import { FormEvent, useEffect, useState } from 'react'
import { PlugZap } from 'lucide-react'
import { api } from '../api'
import { chargedMoney } from '../shared/currency'
import { formatTime } from '../shared/time'
import { toast } from '../shared/toast'
import { t, tr } from '../shared/i18n'

type SeenChat = { id: number; title: string; username: string; admin: boolean }
type TelegramSettingsRecord = {
  enabled: boolean
  bot_token_configured: boolean
  bot_username: string
  chat_id: number
  chat_title: string
  chat_url: string
  api_base: string
  bind_reward_minor: number
  checkin_min_minor: number
  checkin_max_minor: number
  invite_reward_minor: number
  invite_hold_hours: number
  invite_require_link: boolean
  invite_daily_cap: number
  daily_budget_minor: number
  reply_ttl_seconds: number
  welcome: boolean
  status: { running: boolean; last_error: string; last_poll_at?: string; chats: SeenChat[] }
  stats: { links: number; checkins_today: number; rewarded_today_minor: number; rewarded_total_minor: number; invites_rewarded: number; invites_pending: number; members: number }
}
type CheckRecord = { bot_username: string; chat_id: number; chat_title: string; chat_url: string; warnings: string[] }

const yuan = (minor: number) => String(minor / 100)
const minor = (value: string) => Math.round(Number(value) * 100)

// TelegramSettings sets up the Telegram bot: its token and group, and
// what linking, checking in and inviting pay.
export function TelegramSettings() {
  const [saved, setSaved] = useState<TelegramSettingsRecord | null>(null)
  const [enabled, setEnabled] = useState(false)
  const [token, setToken] = useState('')
  const [chat, setChat] = useState('')
  const [chatURL, setChatURL] = useState('')
  const [apiBase, setApiBase] = useState('')
  const [bind, setBind] = useState('1')
  const [checkinMin, setCheckinMin] = useState('0.1')
  const [checkinMax, setCheckinMax] = useState('0.5')
  const [invite, setInvite] = useState('1')
  const [holdHours, setHoldHours] = useState('24')
  const [requireLink, setRequireLink] = useState(true)
  const [dailyCap, setDailyCap] = useState('10')
  const [budget, setBudget] = useState('100')
  const [replyTTL, setReplyTTL] = useState('60')
  const [welcome, setWelcome] = useState(true)
  const [busy, setBusy] = useState(false)
  const [checking, setChecking] = useState(false)
  const [error, setError] = useState('')
  const [warnings, setWarnings] = useState<string[]>([])

  const show = (value: TelegramSettingsRecord) => {
    setSaved(value)
    setEnabled(value.enabled)
    setToken('')
    setChat('')
    setChatURL(value.chat_url)
    setApiBase(value.api_base)
    setBind(yuan(value.bind_reward_minor))
    setCheckinMin(yuan(value.checkin_min_minor))
    setCheckinMax(yuan(value.checkin_max_minor))
    setInvite(yuan(value.invite_reward_minor))
    setHoldHours(String(value.invite_hold_hours))
    setRequireLink(value.invite_require_link)
    setDailyCap(String(value.invite_daily_cap))
    setBudget(yuan(value.daily_budget_minor))
    setReplyTTL(String(value.reply_ttl_seconds))
    setWelcome(value.welcome)
  }

  useEffect(() => {
    api<TelegramSettingsRecord>('/api/v1/admin/settings/telegram')
      .then(show)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [])

  const body = () =>
    JSON.stringify({
      enabled, bot_token: token, chat, chat_url: chatURL, api_base: apiBase,
      bind_reward_minor: minor(bind), checkin_min_minor: minor(checkinMin), checkin_max_minor: minor(checkinMax),
      invite_reward_minor: minor(invite), invite_hold_hours: Number(holdHours), invite_require_link: requireLink,
      invite_daily_cap: Number(dailyCap), daily_budget_minor: minor(budget), reply_ttl_seconds: Number(replyTTL), welcome,
    })

  async function save(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      const result = await api<{ settings: TelegramSettingsRecord; warnings: string[] }>('/api/v1/admin/settings/telegram', { method: 'PUT', body: body() })
      show(result.settings)
      setWarnings(result.warnings ?? [])
      toast('success', t('Telegram 设置已保存'), result.settings.enabled ? t('机器人 @{0} 已启用。', result.settings.bot_username) : t('活动未启用，客户看不到绑定入口。'))
    } catch (err) {
      const message = err instanceof Error ? err.message : t('保存失败')
      setError(message)
      toast('error', t('保存失败'), message)
    } finally {
      setBusy(false)
    }
  }

  async function check() {
    setChecking(true)
    setError('')
    try {
      const result = await api<CheckRecord>('/api/v1/admin/settings/telegram/check', { method: 'POST', body: body() })
      setWarnings(result.warnings ?? [])
      toast(result.warnings?.length ? 'info' : 'success', t('已连接机器人 @{0}', result.bot_username), result.chat_title ? t('群：{0}（{1}）', result.chat_title, result.chat_id) : t('还没有设置群。'))
    } catch (err) {
      const message = err instanceof Error ? err.message : t('连接失败')
      setError(message)
      toast('error', t('连接 Telegram 失败'), message)
    } finally {
      setChecking(false)
    }
  }

  const status = saved?.status
  const stats = saved?.stats
  return (
    <form className="panel telegram-settings" onSubmit={save}>
      <div className="panel-heading">
        <h3>{t('Telegram 绑定与奖励')}</h3>
        <span className={saved?.enabled && status?.running ? 'tag success' : 'tag'}>
          {!saved?.bot_token_configured ? t('未配置') : !saved.enabled ? t('未启用') : status?.running ? t('机器人运行中') : t('机器人未运行')}
        </span>
      </div>
      <p className="muted-text">
        {t('客户把站点账号和 Telegram 账号绑定后，在交流群里签到、邀请新成员可以获得账户余额（只能在本站消费，不能提现）。启用后，未绑定的客户会在前台右下角看到绑定提示。')}
      </p>
      <ol className="telegram-steps muted-text">
        <li>{t('在 Telegram 找 @BotFather 发送 /newbot 创建机器人，把得到的 Token 填到下面并保存。')}</li>
        <li>{t('把机器人拉进交流群并设为管理员，至少勾选「邀请用户」和「删除消息」权限（管理员才能收到普通消息和成员进出消息）。')}</li>
        <li>{t('在下面选择或填写群，点「测试连接」确认无误后勾选启用并保存。')}</li>
      </ol>
      {stats && saved?.bot_token_configured && (
        <section className="metrics telegram-stats">
          <article><span>{t('已绑定账号')}</span><strong>{stats.links}</strong></article>
          <article><span>{t('今日签到')}</span><strong>{stats.checkins_today}</strong></article>
          <article><span>{t('今日已发奖励')}</span><strong>{chargedMoney(stats.rewarded_today_minor)}</strong></article>
          <article><span>{t('累计已发奖励')}</span><strong>{chargedMoney(stats.rewarded_total_minor)}</strong></article>
          <article><span>{t('邀请（已奖励 / 待结算）')}</span><strong>{stats.invites_rewarded} / {stats.invites_pending}</strong></article>
        </section>
      )}
      {status?.last_error && <div className="note-banner warn">{t('机器人最近一次出错：{0}', status.last_error)}</div>}
      {warnings.map(item => <div className="note-banner warn" key={item}>{tr(item)}</div>)}

      <div className="form-grid">
        <label className="check-row wide">
          <input type="checkbox" checked={enabled} onChange={event => setEnabled(event.target.checked)} />
          {t('启用 Telegram 绑定与奖励')}
        </label>
        <label>
          <span>Bot Token</span>
          <input type="password" autoComplete="off" value={token} onChange={event => setToken(event.target.value)} placeholder={saved?.bot_token_configured ? t('已配置（@{0}），留空保持不变', saved.bot_username) : '123456:ABC-DEF…'} />
        </label>
        <label>
          <span>{t('交流群')}</span>
          <input value={chat} onChange={event => setChat(event.target.value)} placeholder={saved?.chat_id ? t('{0}（{1}），留空保持不变', saved.chat_title, saved.chat_id) : t('@群用户名 或 群 ID（-100…）')} />
          {!!status?.chats.length && (
            <small>
              {t('机器人所在的群：')}
              {status.chats.map(item => (
                <button type="button" className="button-link" key={item.id} onClick={() => setChat(String(item.id))}>
                  {item.title}{item.admin ? '' : t('（不是管理员）')}
                </button>
              ))}
            </small>
          )}
          {!status?.chats.length && <small>{t('私有群没有用户名：保存 Token 后把机器人拉进群，这里会列出它所在的群。')}</small>}
        </label>
        <label>
          <span>{t('群链接（客户前台的「进入交流群」）')}</span>
          <input value={chatURL} onChange={event => setChatURL(event.target.value)} placeholder={t('https://t.me/yourgroup，公开群可留空自动填写')} />
        </label>
        <label>
          <span>{t('Bot API 地址（可选）')}</span>
          <input value={apiBase} onChange={event => setApiBase(event.target.value)} placeholder="https://api.telegram.org" />
          <small>{t('服务器无法直接访问 Telegram 时，填反向代理地址。')}</small>
        </label>
        <label>
          <span>{t('绑定奖励（元，0 为不奖励）')}</span>
          <input type="number" min="0" max="1000" step="0.01" value={bind} onChange={event => setBind(event.target.value)} required />
          <small>{t('每个站点账号和每个 Telegram 账号各只发一次。')}</small>
        </label>
        <label>
          <span>{t('每日签到奖励（元，最小 – 最大）')}</span>
          <div className="inline-fields">
            <input aria-label={t('签到奖励最小值')} type="number" min="0" max="1000" step="0.01" value={checkinMin} onChange={event => setCheckinMin(event.target.value)} required />
            <input aria-label={t('签到奖励最大值')} type="number" min="0" max="1000" step="0.01" value={checkinMax} onChange={event => setCheckinMax(event.target.value)} required />
          </div>
          <small>{t('在两个数之间随机；填相同的数为固定金额。')}</small>
        </label>
        <label>
          <span>{t('邀请奖励（元 / 人，0 为关闭）')}</span>
          <input type="number" min="0" max="1000" step="0.01" value={invite} onChange={event => setInvite(event.target.value)} required />
        </label>
        <label>
          <span>{t('被邀请人需留群（小时）')}</span>
          <input type="number" min="0" max="720" step="1" value={holdHours} onChange={event => setHoldHours(event.target.value)} required />
          <small>{t('期间退群的不计奖励。')}</small>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={requireLink} onChange={event => setRequireLink(event.target.checked)} />
          {t('被邀请人绑定站点账号（邮箱已验证）后才计奖励')}
        </label>
        <label>
          <span>{t('每人每日邀请奖励上限（人，0 为不限）')}</span>
          <input type="number" min="0" max="1000" step="1" value={dailyCap} onChange={event => setDailyCap(event.target.value)} required />
        </label>
        <label>
          <span>{t('全站每日奖励总预算（元，0 为不限）')}</span>
          <input type="number" min="0" max="1000000" step="0.01" value={budget} onChange={event => setBudget(event.target.value)} required />
          <small>{t('当天发完后，签到提示「奖励已发完」，邀请奖励顺延到次日。')}</small>
        </label>
        <label>
          <span>{t('群内回复保留（秒，0 为不删除）')}</span>
          <input type="number" min="0" max="3600" step="1" value={replyTTL} onChange={event => setReplyTTL(event.target.value)} required />
        </label>
        <label className="check-row">
          <input type="checkbox" checked={welcome} onChange={event => setWelcome(event.target.checked)} />
          {t('新成员进群时发送欢迎和绑定指引')}
        </label>
      </div>
      <div className="form-actions">
        <button type="button" className="secondary-button" disabled={checking} onClick={() => void check()}>
          <PlugZap size={15} />{checking ? t('正在连接…') : t('测试连接')}
        </button>
        <button className="primary-button" disabled={busy || !saved}>{busy ? t('正在保存…') : t('保存 Telegram 设置')}</button>
      </div>
      {status?.last_poll_at && <p className="muted-text">{t('机器人上次收到 Telegram 响应：{0}', formatTime(status.last_poll_at))}</p>}
      {error && <div className="form-error">{error}</div>}
    </form>
  )
}
