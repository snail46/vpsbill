import { useCallback, useEffect, useRef, useState } from 'react'
import { Bell, Check, Copy, Gift, Mail, Send, X } from 'lucide-react'
import { api, cached } from './api'
import { money } from './shared/currency'
import { formatDate, formatTime } from './shared/time'
import { confirmDialog } from './shared/dialog'
import { toast } from './shared/toast'
import { t } from './shared/i18n'

export type TelegramRecord = {
  enabled: boolean
  email_verified: boolean
  bot_username?: string
  chat_url?: string
  chat_title?: string
  linked: { telegram_id: number; username: string; first_name: string; linked_at: string } | null
  unlink_after?: string
  invite_link?: string
  rules?: {
    bind_reward_minor: number
    checkin_min_minor: number
    checkin_max_minor: number
    invite_reward_minor: number
    invite_hold_hours: number
    invite_require_link: boolean
    invite_daily_cap: number
    rebate_percent?: number
    rebate_max_minor?: number
    streak_bonuses?: { days: number; amount_minor: number }[]
    leaderboards?: boolean
    leaderboard_prizes?: number[]
    tickets?: boolean
  }
  // rewards says when rewards are paid: open now, not started yet, or over.
  rewards?: { state: 'open' | 'upcoming' | 'ended'; from: string | null; until: string | null }
  stats?: { checkins: number; checked_in_today: boolean; invites_rewarded: number; invites_pending: number; earned_minor: number }
}

const path = '/api/v1/customer/telegram'
const dismissKey = 'vpsbill-telegram-prompt'
// The corner prompt stays away this long after it was closed.
const dismissFor = 7 * 24 * 3600 * 1000
// Linking is watched for as long as a bind link works.
const watchFor = 10 * 60 * 1000

// rewardPeriod is the period rewards are paid in as a sentence, or '' when
// it has no bounds.
function rewardPeriod(rewards: TelegramRecord['rewards']) {
  if (rewards?.from && rewards.until) return t('活动时间：{0} 至 {1}（北京时间）', formatTime(rewards.from), formatTime(rewards.until))
  if (rewards?.from) return t('活动自 {0} 开始（北京时间）', formatTime(rewards.from))
  if (rewards?.until) return t('活动截止 {0}（北京时间）', formatTime(rewards.until))
  return ''
}

// checkinRange is what a check-in pays: one amount or a range.
function checkinRange(rules: NonNullable<TelegramRecord['rules']>) {
  return rules.checkin_min_minor === rules.checkin_max_minor
    ? money(rules.checkin_min_minor)
    : `${money(rules.checkin_min_minor)}–${money(rules.checkin_max_minor)}`
}

// useTelegram loads the customer's Telegram link and runs the linking:
// bind() opens the bot with a one-time code, then the link is watched
// until Telegram reports it.
export function useTelegram() {
  const [data, setData] = useState<TelegramRecord | null>(() => cached<TelegramRecord>(path) ?? null)
  const [bindURL, setBindURL] = useState('')
  const [error, setError] = useState('')
  const watching = useRef(0)

  const load = useCallback(() => api<TelegramRecord>(path).then(value => {
    setData(value)
    return value
  }), [])

  useEffect(() => {
    void load().catch(() => undefined)
    return () => window.clearInterval(watching.current)
  }, [load])

  const bind = useCallback(async () => {
    setError('')
    // Opened before the request so the browser takes it for the click's
    // own window; it is pointed at the bot once the link is known.
    const opened = window.open('', '_blank')
    try {
      const result = await api<{ url: string }>(`${path}/bind`, { method: 'POST' })
      setBindURL(result.url)
      if (opened) {
        opened.opener = null
        opened.location.href = result.url
      }
      window.clearInterval(watching.current)
      const started = Date.now()
      watching.current = window.setInterval(() => {
        if (Date.now() - started > watchFor) {
          window.clearInterval(watching.current)
          setBindURL('')
          return
        }
        void load()
          .then(value => {
            if (!value.linked) return
            window.clearInterval(watching.current)
            setBindURL('')
            toast('success', t('Telegram 绑定成功'), value.rules?.bind_reward_minor ? t('绑定奖励 {0} 已存入余额。', money(value.rules.bind_reward_minor)) : undefined)
          })
          .catch(() => undefined)
      }, 3000)
    } catch (err) {
      opened?.close()
      const message = err instanceof Error ? err.message : t('生成绑定链接失败')
      setError(message)
      toast('error', t('无法绑定 Telegram'), message)
    }
  }, [load])

  return { data, load, bind, bindURL, error }
}

// TelegramPrompt is the small card in the bottom-right corner that invites
// a customer who has not linked Telegram yet to do so.
export function TelegramPrompt() {
  const { data, bind, bindURL } = useTelegram()
  const [closed, setClosed] = useState(() => {
    try {
      return Date.now() - Number(window.localStorage.getItem(dismissKey) || 0) < dismissFor
    } catch {
      return false
    }
  })
  // Once the customer started linking here the card stays to say it worked.
  const [started, setStarted] = useState(false)

  if (!data?.enabled || !data.rules || closed) return null
  if (data.linked && !started) return null
  // The card offers rewards, so it only shows while they are paid.
  if (data.rewards && data.rewards.state !== 'open' && !started) return null

  const close = () => {
    try {
      window.localStorage.setItem(dismissKey, String(Date.now()))
    } catch {
      // Private mode: it comes back on the next visit.
    }
    setClosed(true)
  }
  const rules = data.rules

  return (
    <aside className="telegram-prompt" role="complementary" aria-label={t('绑定 Telegram')}>
      <button type="button" className="icon-button telegram-prompt-close" aria-label={t('关闭')} title={t('关闭')} onClick={close}>
        <X size={14} />
      </button>
      <div className="telegram-prompt-head">
        <span className="telegram-prompt-icon"><Send size={18} /></span>
        <strong>{data.linked ? t('Telegram 已绑定') : t('绑定 Telegram，签到领余额')}</strong>
      </div>
      {data.linked ? (
        <>
          <p>{t('已绑定 {0}。现在到交流群里发送「签到」，每天领 {1} 余额。', data.linked.username ? `@${data.linked.username}` : data.linked.first_name, checkinRange(rules))}</p>
          <div className="telegram-prompt-actions">
            {data.chat_url && <a className="primary-button compact" href={data.chat_url} target="_blank" rel="noreferrer">{t('进入交流群')}</a>}
            <button type="button" className="secondary-button compact" onClick={() => setClosed(true)}>{t('知道了')}</button>
          </div>
        </>
      ) : bindURL ? (
        <>
          <p>{t('已打开 Telegram，请在机器人对话里点「开始 / Start」。绑定完成后这里会自动更新。')}</p>
          <div className="telegram-prompt-actions">
            <a className="secondary-button compact" href={bindURL} target="_blank" rel="noreferrer">{t('没有打开？点这里')}</a>
          </div>
        </>
      ) : (
        <>
          <ul>
            {rules.bind_reward_minor > 0 && <li>{t('绑定即送')} <b>{money(rules.bind_reward_minor)}</b></li>}
            <li>{t('每天在交流群发「签到」领')} <b>{checkinRange(rules)}</b></li>
            {rules.invite_reward_minor > 0 && <li>{t('邀请好友进群，每人再得')} <b>{money(rules.invite_reward_minor)}</b></li>}
          </ul>
          {rewardPeriod(data.rewards) && <p className="telegram-period">{rewardPeriod(data.rewards)}</p>}
          {!data.email_verified && <p className="danger-text">{t('请先完成邮箱验证，再绑定 Telegram。')}</p>}
          <div className="telegram-prompt-actions">
            <button type="button" className="primary-button compact" disabled={!data.email_verified} onClick={() => { setStarted(true); void bind() }}>
              <Send size={14} />{t('立即绑定')}
            </button>
            <button type="button" className="secondary-button compact" onClick={close}>{t('以后再说')}</button>
          </div>
        </>
      )}
    </aside>
  )
}

const linkChanged = 'vpsbill-telegram-link'

type NotifyRecord = { email: boolean; telegram: boolean; telegram_linked: boolean; telegram_enabled: boolean; mail_enabled: boolean }

// NotifyPanel lets a customer choose where notices go: mail, the linked
// Telegram account, or both, but never neither.
export function NotifyPanel({ email }: { email: string }) {
  const notifyPath = '/api/v1/customer/notifications'
  const [data, setData] = useState<NotifyRecord | null>(() => cached<NotifyRecord>(notifyPath) ?? null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const load = () => void api<NotifyRecord>(notifyPath).then(setData).catch(() => undefined)
    load()
    window.addEventListener(linkChanged, load)
    return () => window.removeEventListener(linkChanged, load)
  }, [])

  // Without a bot there is nothing to choose: everything goes by mail.
  if (!data?.telegram_enabled) return null

  async function choose(next: { email: boolean; telegram: boolean }) {
    if (!next.email && !next.telegram) {
      toast('info', t('至少保留一种通知方式'), t('邮件和 Telegram 不能同时关闭。'))
      return
    }
    setBusy(true)
    try {
      setData(await api<NotifyRecord>(notifyPath, { method: 'PUT', body: JSON.stringify(next) }))
      toast('success', t('通知方式已保存'))
    } catch (err) {
      toast('error', t('保存失败'), err instanceof Error ? err.message : undefined)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="panel notify-panel" id="notifications">
      <div className="panel-heading">
        <h3><Bell size={16} /> {t('通知方式')}</h3>
        <span className="tag">{data.email && data.telegram ? t('邮件 + Telegram') : data.telegram ? 'Telegram' : t('邮件')}</span>
      </div>
      <p className="muted-text">{t('到期提醒、流量告警、工单回复、交易和清退通知发到这里选择的渠道，至少选一种。邮箱验证和找回密码的邮件始终发到邮箱。')}</p>
      <div className="notify-options">
        <label className="check-row">
          <input type="checkbox" checked={data.email} disabled={busy} onChange={event => void choose({ email: event.target.checked, telegram: data.telegram })} />
          <Mail size={15} />
          <span>{t('邮件')}<small>{email}</small></span>
        </label>
        <label className="check-row">
          <input type="checkbox" checked={data.telegram} disabled={busy || !data.telegram_linked} onChange={event => void choose({ email: data.email, telegram: event.target.checked })} />
          <Send size={15} />
          <span>Telegram<small>{data.telegram_linked ? t('由站点机器人私聊发送') : t('绑定 Telegram 后可选')}</small></span>
        </label>
      </div>
      {data.email && !data.mail_enabled && <p className="muted-text">{t('站点暂未配置发信邮箱，邮件通知暂时发不出去。')}</p>}
    </div>
  )
}

// TelegramPanel is the Telegram section of the profile page: linking, the
// rewards, the customer's own invite link and what they earned.
export function TelegramPanel() {
  const { data, load, bind, bindURL, error } = useTelegram()
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  // The notification panel below follows the link.
  const linked = Boolean(data?.linked)
  useEffect(() => {
    window.dispatchEvent(new Event(linkChanged))
  }, [linked])

  if (!data?.enabled || !data.rules) return null
  const rules = data.rules
  const stats = data.stats

  async function unlink() {
    if (!(await confirmDialog({ title: t('解绑 Telegram？'), message: t('解绑后不能再用这个 Telegram 账号签到和邀请，已获得的余额不受影响。'), confirmText: t('解绑'), danger: true }))) return
    setBusy(true)
    try {
      await api(path, { method: 'DELETE' })
      await load()
      toast('success', t('Telegram 已解绑'))
    } catch (err) {
      toast('error', t('解绑失败'), err instanceof Error ? err.message : undefined)
    } finally {
      setBusy(false)
    }
  }

  async function makeInviteLink() {
    setBusy(true)
    try {
      await api(`${path}/invite-link`, { method: 'POST' })
      await load()
      toast('success', t('专属邀请链接已生成'))
    } catch (err) {
      toast('error', t('生成邀请链接失败'), err instanceof Error ? err.message : undefined)
    } finally {
      setBusy(false)
    }
  }

  const copy = () => {
    void navigator.clipboard?.writeText(data.invite_link || '').then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    })
  }

  const conditions = [
    rules.invite_require_link ? t('绑定站点账号') : '',
    rules.invite_hold_hours > 0 ? t('留在群里满 {0} 小时', rules.invite_hold_hours) : '',
  ].filter(Boolean).join(t('并'))
  const locked = Boolean(data.unlink_after && new Date(data.unlink_after).getTime() > Date.now())

  return (
    <div className="panel telegram-panel" id="telegram">
      <div className="panel-heading">
        <h3><Send size={16} /> Telegram</h3>
        <span className={data.linked ? 'tag success' : 'tag'}>{data.linked ? t('已绑定') : t('未绑定')}</span>
      </div>
      {data.rewards?.state === 'upcoming' && <div className="note-banner warn">{t('活动还没有开始，暂时不发放奖励。{0}。现在可以先绑定账号，用 Telegram 接收通知。', rewardPeriod(data.rewards))}</div>}
      {data.rewards?.state === 'ended' && <div className="note-banner warn">{t('活动已经结束，不再发放奖励。{0}。绑定后仍然可以用 Telegram 接收通知。', rewardPeriod(data.rewards))}</div>}
      {data.rewards?.state === 'open' && rewardPeriod(data.rewards) && <div className="note-banner telegram-period">{rewardPeriod(data.rewards)}{t('，活动时间外不发放奖励。')}</div>}
      <ul className="telegram-rules">
        {rules.bind_reward_minor > 0 && <li><Gift size={14} />{t('绑定 Telegram 账号，一次性奖励')} <b>{money(rules.bind_reward_minor)}</b>{t('。')}</li>}
        <li><Gift size={14} />{t('每天在交流群发送「签到」或 /checkin，领')} <b>{checkinRange(rules)}</b>{t('。')}</li>
        {rules.invite_reward_minor > 0 && (
          <li>
            <Gift size={14} />{t('用你的专属链接邀请新成员进群{0}，奖励', conditions && t('，对方{0}后', conditions))} <b>{money(rules.invite_reward_minor)}</b>
            {t('{0}。', rules.invite_daily_cap > 0 && t('（每天最多 {0} 人，超出的顺延到次日）', rules.invite_daily_cap))}
          </li>
        )}
        {!!rules.rebate_percent && (
          <li>
            <Gift size={14} />{t('你邀请的成员首次充值或在线付款后，你再得该笔金额的')} <b>{rules.rebate_percent}%</b>
            {t('{0}。', !!rules.rebate_max_minor && t('（单笔最多 {0}）', money(rules.rebate_max_minor)))}
          </li>
        )}
        {!!rules.streak_bonuses?.some(item => item.amount_minor > 0) && (
          <li>
            <Gift size={14} />{t('连续签到额外奖励：')}
            {rules.streak_bonuses.filter(item => item.amount_minor > 0).map(item => t('满 {0} 天 {1}', item.days, money(item.amount_minor))).join(t('、'))}
            {t('。')}
          </li>
        )}
        {rules.leaderboards && !!rules.leaderboard_prizes?.some(prize => prize > 0) && (
          <li>
            <Gift size={14} />{t('每周一在群里公布上周签到榜和邀请榜，前 {0} 名分别奖励 {1}；群里发 /rank 查看本周排名。', rules.leaderboard_prizes.filter(prize => prize > 0).length, rules.leaderboard_prizes.filter(prize => prize > 0).map(prize => money(prize)).join(' / '))}
          </li>
        )}
        <li><Gift size={14} />{t('管理员不定期在群里发红包，绑定后即可领取。')}</li>
        <li className="muted-text">{t('奖励存入账户余额，可用于购买和续费，不能提现。')}</li>
        <li className="muted-text">{t('绑定后私聊机器人：/services 查看 VPS，/balance 查看余额，/invoices 用余额支付待付账单；到期提醒里可以直接点按钮续费。')}</li>
        {rules.tickets && <li className="muted-text">{t('也可以私聊机器人发送 /ticket 提交工单；收到工单回复后，直接回复那条消息即可继续沟通。')}</li>}
      </ul>

      {!data.linked && (
        <>
          {!data.email_verified && <div className="note-banner warn">{t('请先完成邮箱验证，再绑定 Telegram。')}</div>}
          {bindURL && <div className="note-banner">{t('已打开 Telegram，请在机器人对话里点「开始 / Start」，绑定完成后这里会自动更新。')}<a href={bindURL} target="_blank" rel="noreferrer">{t('没有打开？点这里')}</a></div>}
          {error && <div className="form-error">{error}</div>}
          <div className="form-actions">
            <button type="button" className="primary-button" disabled={!data.email_verified} onClick={() => void bind()}>
              <Send size={15} />{t('绑定 Telegram')}
            </button>
            {data.chat_url && <a className="secondary-button" href={data.chat_url} target="_blank" rel="noreferrer">{t('先看看交流群')}</a>}
          </div>
        </>
      )}

      {data.linked && (
        <>
          <div className="profile-panel telegram-facts">
            <div>
              <span>{t('Telegram 账号')}</span>
              <strong>{data.linked.username ? `@${data.linked.username}` : data.linked.first_name || data.linked.telegram_id}</strong>
            </div>
            <div>
              <span>{t('绑定时间')}</span>
              <strong>{formatTime(data.linked.linked_at)}</strong>
            </div>
            <div>
              <span>{t('累计签到')}</span>
              <strong>{t('{0} 天{1}', stats?.checkins ?? 0, stats?.checked_in_today ? t('（今天已签到）') : t('（今天还没签到）'))}</strong>
            </div>
            <div>
              <span>{t('邀请')}</span>
              <strong>{t('已奖励 {0} 人，待结算 {1} 人', stats?.invites_rewarded ?? 0, stats?.invites_pending ?? 0)}</strong>
            </div>
            <div>
              <span>{t('累计奖励')}</span>
              <strong>{money(stats?.earned_minor ?? 0)}</strong>
            </div>
          </div>
          {rules.invite_reward_minor > 0 && (
            <div className="telegram-invite">
              <span>{t('专属邀请链接')}</span>
              {data.invite_link ? (
                <div className="copy-row">
                  <code>{data.invite_link}</code>
                  <button type="button" className="secondary-button compact" onClick={copy}>
                    {copied ? <Check size={14} /> : <Copy size={14} />}{copied ? t('已复制') : t('复制')}
                  </button>
                </div>
              ) : (
                <button type="button" className="secondary-button compact" disabled={busy} onClick={() => void makeInviteLink()}>{t('生成邀请链接')}</button>
              )}
            </div>
          )}
          <div className="form-actions">
            {data.chat_url && <a className="primary-button" href={data.chat_url} target="_blank" rel="noreferrer"><Send size={15} />{t('进入交流群签到')}</a>}
            <button type="button" className="secondary-button" disabled={busy || locked} title={locked ? t('绑定满 7 天后才能解绑（{0} 起）', formatDate(data.unlink_after!)) : ''} onClick={() => void unlink()}>
              {t('解绑')}
            </button>
          </div>
          {locked && <p className="muted-text">{t('绑定满 7 天后才能解绑（{0} 起）。', formatDate(data.unlink_after!))}</p>}
        </>
      )}
    </div>
  )
}
