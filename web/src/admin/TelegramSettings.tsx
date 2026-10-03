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
  // currency is the site's default currency, which the amounts are in.
  currency: 'CNY' | 'USD'
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
  rewards_from: string | null
  rewards_until: string | null
  rebate_percent: number
  rebate_max_minor: number
  rebate_delay_hours: number
  announce_chat_id: number
  announce_chat_title: string
  announce_new: boolean
  announce_restock: boolean
  announce_hosted: boolean
  announce_daily_cap: number
  admin_chat_id: number
  admin_chat_title: string
  tickets: boolean
  streak_bonuses: { days: number; amount_minor: number }[]
  leaderboards: boolean
  leaderboard_prizes: number[]
  red_packet_require_spent: boolean
  red_packet_min_linked_days: number
  verify: '' | 'button' | 'link'
  verify_minutes: number
  filter_links: boolean
  blocked_words: string[]
  unlinked_per_minute: number
  status: { running: boolean; last_error: string; last_poll_at?: string; chats: SeenChat[] }
  stats: { links: number; checkins_today: number; rewarded_today_minor: number; rewarded_total_minor: number; invites_rewarded: number; invites_pending: number; members: number }
}
type CheckRecord = { bot_username: string; chat_id: number; chat_title: string; chat_url: string; warnings: string[] }

// The platform clock is UTC+8: a date-time field shows and takes that.
const toField = (value: string | null) => (value ? formatTime(value).replace(' ', 'T') : '')
const fromField = (value: string) => (value ? new Date(`${value}:00+08:00`).toISOString() : null)

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
  const [from, setFrom] = useState('')
  const [until, setUntil] = useState('')
  const [rebate, setRebate] = useState('0')
  const [rebateMax, setRebateMax] = useState('20')
  const [rebateDelay, setRebateDelay] = useState('72')
  const [announceChat, setAnnounceChat] = useState('')
  const [announceNew, setAnnounceNew] = useState(true)
  const [announceRestock, setAnnounceRestock] = useState(true)
  const [announceHosted, setAnnounceHosted] = useState(false)
  const [announceCap, setAnnounceCap] = useState('10')
  const [adminChat, setAdminChat] = useState('')
  const [tickets, setTickets] = useState(true)
  const [streaks, setStreaks] = useState<{ days: string; amount: string }[]>([])
  const [leaderboards, setLeaderboards] = useState(false)
  const [prizes, setPrizes] = useState('')
  const [packetSpent, setPacketSpent] = useState(false)
  const [packetDays, setPacketDays] = useState('0')
  const [verify, setVerify] = useState<'' | 'button' | 'link'>('')
  const [verifyMinutes, setVerifyMinutes] = useState('10')
  const [filterLinks, setFilterLinks] = useState(false)
  const [blockedWords, setBlockedWords] = useState('')
  const [unlinkedRate, setUnlinkedRate] = useState('0')
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
    setFrom(toField(value.rewards_from))
    setUntil(toField(value.rewards_until))
    setRebate(String(value.rebate_percent))
    setRebateMax(yuan(value.rebate_max_minor))
    setRebateDelay(String(value.rebate_delay_hours))
    setAnnounceChat('')
    setAnnounceNew(value.announce_new)
    setAnnounceRestock(value.announce_restock)
    setAnnounceHosted(value.announce_hosted)
    setAnnounceCap(String(value.announce_daily_cap))
    setAdminChat('')
    setTickets(value.tickets)
    setStreaks((value.streak_bonuses ?? []).map(item => ({ days: String(item.days), amount: yuan(item.amount_minor) })))
    setLeaderboards(value.leaderboards)
    setPrizes((value.leaderboard_prizes ?? []).map(yuan).join(', '))
    setPacketSpent(value.red_packet_require_spent)
    setPacketDays(String(value.red_packet_min_linked_days))
    setVerify(value.verify ?? '')
    setVerifyMinutes(String(value.verify_minutes || 10))
    setFilterLinks(value.filter_links)
    setBlockedWords((value.blocked_words ?? []).join('\n'))
    setUnlinkedRate(String(value.unlinked_per_minute))
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
      rewards_from: fromField(from), rewards_until: fromField(until),
      rebate_percent: Number(rebate), rebate_max_minor: minor(rebateMax), rebate_delay_hours: Number(rebateDelay),
      announce_chat: announceChat, announce_new: announceNew, announce_restock: announceRestock, announce_hosted: announceHosted, announce_daily_cap: Number(announceCap),
      admin_chat: adminChat,
      tickets,
      streak_bonuses: streaks.filter(item => item.days.trim() !== '').map(item => ({ days: Number(item.days), amount_minor: minor(item.amount) })),
      leaderboards,
      leaderboard_prizes: prizes.split(/[,，\s]+/).filter(Boolean).map(minor),
      red_packet_require_spent: packetSpent,
      red_packet_min_linked_days: Number(packetDays),
      verify,
      verify_minutes: Number(verifyMinutes),
      filter_links: filterLinks,
      blocked_words: blockedWords.split(/[\n,，]+/).map(word => word.trim()).filter(Boolean),
      unlinked_per_minute: Number(unlinkedRate),
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
  const unit = saved?.currency === 'USD' ? t('美元') : t('元')
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
        {t('绑定后的客户还可以在「账户资料」里选择用 Telegram 接收到期提醒、工单回复等通知。')}
      </p>
      <p className="muted-text">
        {saved?.currency === 'USD'
          ? t('下面的金额按站点默认币种美元（USD）填写，机器人和前台也按美元展示。')
          : t('下面的金额按站点默认币种人民币（CNY）填写，机器人和前台也按人民币展示。')}
        {t('默认币种在上方「语言与币种」里设置；它和记账币种不同时，奖励在发放时按当前汇率换算后存入余额。')}
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
          <span>{t('绑定奖励（{0}，0 为不奖励）', unit)}</span>
          <input type="number" min="0" max="1000" step="0.01" value={bind} onChange={event => setBind(event.target.value)} required />
          <small>{t('每个站点账号和每个 Telegram 账号各只发一次。')}</small>
        </label>
        <label>
          <span>{t('每日签到奖励（{0}，最小 – 最大）', unit)}</span>
          <div className="inline-fields">
            <input aria-label={t('签到奖励最小值')} type="number" min="0" max="1000" step="0.01" value={checkinMin} onChange={event => setCheckinMin(event.target.value)} required />
            <input aria-label={t('签到奖励最大值')} type="number" min="0" max="1000" step="0.01" value={checkinMax} onChange={event => setCheckinMax(event.target.value)} required />
          </div>
          <small>{t('在两个数之间随机；填相同的数为固定金额。')}</small>
        </label>
        <label>
          <span>{t('邀请奖励（{0} / 人，0 为关闭）', unit)}</span>
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
          <span>{t('全站每日奖励总预算（{0}，0 为不限）', unit)}</span>
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

      <h4 className="settings-subheading">{t('活动时间')}</h4>
      <p className="muted-text">{t('只在这段时间内发放绑定、签到、邀请和返利奖励；前台和机器人会向用户写明活动时间。两项都留空表示长期有效。活动时间外仍然可以绑定账号（用于接收通知），只是不发奖励。时间按北京时间（UTC+8）。')}</p>
      <div className="form-grid">
        <label>
          <span>{t('开始时间（留空为不限）')}</span>
          <input type="datetime-local" value={from} onChange={event => setFrom(event.target.value)} />
        </label>
        <label>
          <span>{t('结束时间（留空为不限）')}</span>
          <input type="datetime-local" value={until} onChange={event => setUntil(event.target.value)} />
        </label>
      </div>

      <h4 className="settings-subheading">{t('首单返利')}</h4>
      <p className="muted-text">{t('被邀请人（通过邀请链接进群并绑定站点账号）第一次在线付款或由管理员确认收款后，邀请人按该笔金额的比例获得返利，每个被邀请账号只算一次。余额支付不算。返利计入每日奖励总预算。')}</p>
      <div className="form-grid">
        <label>
          <span>{t('返利比例 %（0 为关闭）')}</span>
          <input type="number" min="0" max="50" step="1" value={rebate} onChange={event => setRebate(event.target.value)} required />
        </label>
        <label>
          <span>{t('单笔返利上限（{0}，0 为不限）', unit)}</span>
          <input type="number" min="0" max="10000" step="0.01" value={rebateMax} onChange={event => setRebateMax(event.target.value)} required />
        </label>
        <label>
          <span>{t('付款后等待（小时）')}</span>
          <input type="number" min="0" max="720" step="1" value={rebateDelay} onChange={event => setRebateDelay(event.target.value)} required />
          <small>{t('付款满这么久才发返利，用来覆盖退款期。')}</small>
        </label>
      </div>

      <h4 className="settings-subheading">{t('上新与补货推送')}</h4>
      <p className="muted-text">{t('新套餐上架或售罄的套餐补货时，机器人在这里指定的频道或群里发一条带购买按钮的消息。机器人需要是该频道的管理员（有发消息权限）。客户在售罄套餐上点「到货通知我」的提醒不受这里影响，会按各自的通知方式发送。')}</p>
      <div className="form-grid">
        <label>
          <span>{t('推送到的频道或群')}</span>
          <input value={announceChat} onChange={event => setAnnounceChat(event.target.value)} placeholder={saved?.announce_chat_id ? t('{0}（{1}），留空保持不变，填 0 关闭', saved.announce_chat_title, saved.announce_chat_id) : t('@频道用户名 或 ID（-100…），留空为不推送')} />
          {!!status?.chats.length && (
            <small>
              {t('机器人所在的群和频道：')}
              {status.chats.map(item => (
                <button type="button" className="button-link" key={item.id} onClick={() => setAnnounceChat(String(item.id))}>{item.title}</button>
              ))}
            </small>
          )}
        </label>
        <label>
          <span>{t('每日最多推送（条，0 为不限）')}</span>
          <input type="number" min="0" max="200" step="1" value={announceCap} onChange={event => setAnnounceCap(event.target.value)} required />
        </label>
        <label className="check-row">
          <input type="checkbox" checked={announceNew} onChange={event => setAnnounceNew(event.target.checked)} />
          {t('推送新上架的套餐')}
        </label>
        <label className="check-row">
          <input type="checkbox" checked={announceRestock} onChange={event => setAnnounceRestock(event.target.checked)} />
          {t('推送补货')}
        </label>
        <label className="check-row">
          <input type="checkbox" checked={announceHosted} onChange={event => setAnnounceHosted(event.target.checked)} />
          {t('包含机主托管的套餐')}
        </label>
      </div>

      <h4 className="settings-subheading">{t('管理群通知')}</h4>
      <p className="muted-text">{t('把机器人拉进一个只有管理员的群并在这里指定：原本发给商家邮箱的通知（新工单和客户回复、母机负载过高暂停销售、母机到期和流量提醒）会同时发到这个群。各类通知的开关沿用「邮件通知」里的设置。')}</p>
      <div className="form-grid">
        <label>
          <span>{t('管理群')}</span>
          <input value={adminChat} onChange={event => setAdminChat(event.target.value)} placeholder={saved?.admin_chat_id ? t('{0}（{1}），留空保持不变，填 0 关闭', saved.admin_chat_title, saved.admin_chat_id) : t('群 ID（-100…）或 @群用户名，留空为不发送')} />
          {!!status?.chats.length && (
            <small>
              {t('机器人所在的群和频道：')}
              {status.chats.map(item => (
                <button type="button" className="button-link" key={item.id} onClick={() => setAdminChat(String(item.id))}>{item.title}</button>
              ))}
            </small>
          )}
        </label>
      </div>

      <h4 className="settings-subheading">{t('在 Telegram 里收发工单')}</h4>
      <p className="muted-text">{t('开启后，客户可以私聊机器人发送 /ticket 提交工单；工单通知发到 Telegram 时，客户、机主直接回复（引用）那条消息即可回复工单，可以带图片。管理员先在「安全中心」绑定自己的 Telegram，就可以在管理群里回复工单通知、点「认领」。')}</p>
      <div className="form-grid">
        <label className="check-row wide">
          <input type="checkbox" checked={tickets} onChange={event => setTickets(event.target.checked)} />
          {t('允许在 Telegram 里提交和回复工单')}
        </label>
      </div>

      <h4 className="settings-subheading">{t('连续签到奖励')}</h4>
      <p className="muted-text">{t('连续签到满指定天数（以及它的整数倍）时额外奖励，同一天满足多档时发天数最多的一档。计入每日奖励总预算。')}</p>
      <div className="streak-rows">
        {streaks.map((item, index) => (
          <div className="inline-fields" key={index}>
            <label>
              <span>{t('连续天数')}</span>
              <input type="number" min="2" max="365" step="1" value={item.days} onChange={event => setStreaks(rows => rows.map((row, i) => (i === index ? { ...row, days: event.target.value } : row)))} />
            </label>
            <label>
              <span>{t('额外奖励（{0}）', unit)}</span>
              <input type="number" min="0" max="1000" step="0.01" value={item.amount} onChange={event => setStreaks(rows => rows.map((row, i) => (i === index ? { ...row, amount: event.target.value } : row)))} />
            </label>
            <button type="button" className="text-button" onClick={() => setStreaks(rows => rows.filter((_, i) => i !== index))}>{t('删除')}</button>
          </div>
        ))}
        {streaks.length < 5 && (
          <button type="button" className="secondary-button compact" onClick={() => setStreaks(rows => [...rows, { days: '', amount: '1' }])}>{t('添加一档')}</button>
        )}
      </div>

      <h4 className="settings-subheading">{t('每周排行榜')}</h4>
      <p className="muted-text">{t('每周一 10:00（北京时间）在交流群公布上周的签到榜和邀请榜，并按名次发奖金；群成员随时可以发 /rank 查看本周排名。只在活动时间内公布。奖金计入当天已发放的奖励，但不受每日总预算限制。')}</p>
      <div className="form-grid">
        <label className="check-row">
          <input type="checkbox" checked={leaderboards} onChange={event => setLeaderboards(event.target.checked)} />
          {t('公布每周排行榜')}
        </label>
        <label>
          <span>{t('前几名的奖金（{0}，用逗号分隔，依次为第 1、2、3… 名）', unit)}</span>
          <input value={prizes} onChange={event => setPrizes(event.target.value)} placeholder="5, 3, 1" />
        </label>
      </div>

      <h4 className="settings-subheading">{t('红包')}</h4>
      <p className="muted-text">{t('管理员在后台下方的「红包」里发，或者在交流群发送 /redpacket 总金额 个数 [口令]（需要先在「安全中心」绑定 Telegram，且有财务权限）。下面两项是群里发红包时的领取条件。红包计入当天已发放的奖励，但不受每日总预算限制。')}</p>
      <div className="form-grid">
        <label className="check-row">
          <input type="checkbox" checked={packetSpent} onChange={event => setPacketSpent(event.target.checked)} />
          {t('仅限消费过的账号领取')}
        </label>
        <label>
          <span>{t('仅限绑定满几天的账号（0 为不限）')}</span>
          <input type="number" min="0" max="365" step="1" value={packetDays} onChange={event => setPacketDays(event.target.value)} required />
        </label>
      </div>

      <h4 className="settings-subheading">{t('入群验证与群管理')}</h4>
      <p className="muted-text">{t('需要机器人有「封禁用户」和「删除消息」权限。群管理员和已绑定的管理员不受下面的限制。')}</p>
      <div className="form-grid">
        <label>
          <span>{t('新成员入群验证')}</span>
          <select value={verify} onChange={event => setVerify(event.target.value as '' | 'button' | 'link')}>
            <option value="">{t('不验证')}</option>
            <option value="button">{t('点按钮验证（挡机器人）')}</option>
            <option value="link">{t('绑定站点账号后才能发言')}</option>
          </select>
          <small>{t('已绑定站点账号的成员进群不需要验证。')}</small>
        </label>
        <label>
          <span>{t('验证时限（分钟）')}</span>
          <input type="number" min="1" max="1440" step="1" value={verifyMinutes} onChange={event => setVerifyMinutes(event.target.value)} required />
          <small>{t('超时未通过的成员会被移出群（之后仍可重新加入）。')}</small>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={filterLinks} onChange={event => setFilterLinks(event.target.checked)} />
          {t('删除未绑定成员发的链接')}
        </label>
        <label>
          <span>{t('未绑定成员每分钟最多发言（条，0 为不限）')}</span>
          <input type="number" min="0" max="60" step="1" value={unlinkedRate} onChange={event => setUnlinkedRate(event.target.value)} required />
        </label>
        <label className="wide">
          <span>{t('屏蔽词（每行一个，命中即删除消息）')}</span>
          <textarea rows={3} value={blockedWords} onChange={event => setBlockedWords(event.target.value)} placeholder={t('例如：代开发票')} />
        </label>
      </div>
      <p className="muted-text">{t('群头衔：Telegram 只允许给管理员设置头衔，给客户或机主加头衔需要把他们设为管理员，风险太大，所以没有提供。')}</p>

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
