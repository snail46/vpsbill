import { useCallback, useEffect, useRef, useState } from 'react'
import { Check, Copy, Gift, Send, X } from 'lucide-react'
import { api, cached } from './api'
import { money } from './shared/currency'
import { formatDate, formatTime } from './shared/time'
import { confirmDialog } from './shared/dialog'
import { toast } from './shared/toast'

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
  }
  stats?: { checkins: number; checked_in_today: boolean; invites_rewarded: number; invites_pending: number; earned_minor: number }
}

const path = '/api/v1/customer/telegram'
const dismissKey = 'vpsbill-telegram-prompt'
// The corner prompt stays away this long after it was closed.
const dismissFor = 7 * 24 * 3600 * 1000
// Linking is watched for as long as a bind link works.
const watchFor = 10 * 60 * 1000

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
            toast('success', 'Telegram 绑定成功', value.rules?.bind_reward_minor ? `绑定奖励 ${money(value.rules.bind_reward_minor)} 已存入余额。` : undefined)
          })
          .catch(() => undefined)
      }, 3000)
    } catch (err) {
      opened?.close()
      const message = err instanceof Error ? err.message : '生成绑定链接失败'
      setError(message)
      toast('error', '无法绑定 Telegram', message)
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
    <aside className="telegram-prompt" role="complementary" aria-label="绑定 Telegram">
      <button type="button" className="icon-button telegram-prompt-close" aria-label="关闭" title="关闭" onClick={close}>
        <X size={14} />
      </button>
      <div className="telegram-prompt-head">
        <span className="telegram-prompt-icon"><Send size={18} /></span>
        <strong>{data.linked ? 'Telegram 已绑定' : '绑定 Telegram，签到领余额'}</strong>
      </div>
      {data.linked ? (
        <>
          <p>已绑定 {data.linked.username ? `@${data.linked.username}` : data.linked.first_name}。现在到交流群里发送「签到」，每天领 {checkinRange(rules)} 余额。</p>
          <div className="telegram-prompt-actions">
            {data.chat_url && <a className="primary-button compact" href={data.chat_url} target="_blank" rel="noreferrer">进入交流群</a>}
            <button type="button" className="secondary-button compact" onClick={() => setClosed(true)}>知道了</button>
          </div>
        </>
      ) : bindURL ? (
        <>
          <p>已打开 Telegram，请在机器人对话里点「开始 / Start」。绑定完成后这里会自动更新。</p>
          <div className="telegram-prompt-actions">
            <a className="secondary-button compact" href={bindURL} target="_blank" rel="noreferrer">没有打开？点这里</a>
          </div>
        </>
      ) : (
        <>
          <ul>
            {rules.bind_reward_minor > 0 && <li>绑定即送 <b>{money(rules.bind_reward_minor)}</b></li>}
            <li>每天在交流群发「签到」领 <b>{checkinRange(rules)}</b></li>
            {rules.invite_reward_minor > 0 && <li>邀请好友进群，每人再得 <b>{money(rules.invite_reward_minor)}</b></li>}
          </ul>
          {!data.email_verified && <p className="danger-text">请先完成邮箱验证，再绑定 Telegram。</p>}
          <div className="telegram-prompt-actions">
            <button type="button" className="primary-button compact" disabled={!data.email_verified} onClick={() => { setStarted(true); void bind() }}>
              <Send size={14} />立即绑定
            </button>
            <button type="button" className="secondary-button compact" onClick={close}>以后再说</button>
          </div>
        </>
      )}
    </aside>
  )
}

// TelegramPanel is the Telegram section of the profile page: linking, the
// rewards, the customer's own invite link and what they earned.
export function TelegramPanel() {
  const { data, load, bind, bindURL, error } = useTelegram()
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  if (!data?.enabled || !data.rules) return null
  const rules = data.rules
  const stats = data.stats

  async function unlink() {
    if (!(await confirmDialog({ title: '解绑 Telegram？', message: '解绑后不能再用这个 Telegram 账号签到和邀请，已获得的余额不受影响。', confirmText: '解绑', danger: true }))) return
    setBusy(true)
    try {
      await api(path, { method: 'DELETE' })
      await load()
      toast('success', 'Telegram 已解绑')
    } catch (err) {
      toast('error', '解绑失败', err instanceof Error ? err.message : undefined)
    } finally {
      setBusy(false)
    }
  }

  async function makeInviteLink() {
    setBusy(true)
    try {
      await api(`${path}/invite-link`, { method: 'POST' })
      await load()
      toast('success', '专属邀请链接已生成')
    } catch (err) {
      toast('error', '生成邀请链接失败', err instanceof Error ? err.message : undefined)
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
    rules.invite_require_link ? '绑定站点账号' : '',
    rules.invite_hold_hours > 0 ? `留在群里满 ${rules.invite_hold_hours} 小时` : '',
  ].filter(Boolean).join('并')
  const locked = Boolean(data.unlink_after && new Date(data.unlink_after).getTime() > Date.now())

  return (
    <div className="panel telegram-panel" id="telegram">
      <div className="panel-heading">
        <h3><Send size={16} /> Telegram</h3>
        <span className={data.linked ? 'tag success' : 'tag'}>{data.linked ? '已绑定' : '未绑定'}</span>
      </div>
      <ul className="telegram-rules">
        {rules.bind_reward_minor > 0 && <li><Gift size={14} />绑定 Telegram 账号，一次性奖励 <b>{money(rules.bind_reward_minor)}</b>。</li>}
        <li><Gift size={14} />每天在交流群发送「签到」或 /checkin，领 <b>{checkinRange(rules)}</b>。</li>
        {rules.invite_reward_minor > 0 && (
          <li>
            <Gift size={14} />用你的专属链接邀请新成员进群{conditions && `，对方${conditions}后`}，奖励 <b>{money(rules.invite_reward_minor)}</b>
            {rules.invite_daily_cap > 0 && `（每天最多 ${rules.invite_daily_cap} 人，超出的顺延到次日）`}。
          </li>
        )}
        <li className="muted-text">奖励存入账户余额，可用于购买和续费，不能提现。</li>
      </ul>

      {!data.linked && (
        <>
          {!data.email_verified && <div className="note-banner warn">请先完成邮箱验证，再绑定 Telegram。</div>}
          {bindURL && <div className="note-banner">已打开 Telegram，请在机器人对话里点「开始 / Start」，绑定完成后这里会自动更新。<a href={bindURL} target="_blank" rel="noreferrer">没有打开？点这里</a></div>}
          {error && <div className="form-error">{error}</div>}
          <div className="form-actions">
            <button type="button" className="primary-button" disabled={!data.email_verified} onClick={() => void bind()}>
              <Send size={15} />绑定 Telegram
            </button>
            {data.chat_url && <a className="secondary-button" href={data.chat_url} target="_blank" rel="noreferrer">先看看交流群</a>}
          </div>
        </>
      )}

      {data.linked && (
        <>
          <div className="profile-panel telegram-facts">
            <div>
              <span>Telegram 账号</span>
              <strong>{data.linked.username ? `@${data.linked.username}` : data.linked.first_name || data.linked.telegram_id}</strong>
            </div>
            <div>
              <span>绑定时间</span>
              <strong>{formatTime(data.linked.linked_at)}</strong>
            </div>
            <div>
              <span>累计签到</span>
              <strong>{stats?.checkins ?? 0} 天{stats?.checked_in_today ? '（今天已签到）' : '（今天还没签到）'}</strong>
            </div>
            <div>
              <span>邀请</span>
              <strong>已奖励 {stats?.invites_rewarded ?? 0} 人，待结算 {stats?.invites_pending ?? 0} 人</strong>
            </div>
            <div>
              <span>累计奖励</span>
              <strong>{money(stats?.earned_minor ?? 0)}</strong>
            </div>
          </div>
          {rules.invite_reward_minor > 0 && (
            <div className="telegram-invite">
              <span>专属邀请链接</span>
              {data.invite_link ? (
                <div className="copy-row">
                  <code>{data.invite_link}</code>
                  <button type="button" className="secondary-button compact" onClick={copy}>
                    {copied ? <Check size={14} /> : <Copy size={14} />}{copied ? '已复制' : '复制'}
                  </button>
                </div>
              ) : (
                <button type="button" className="secondary-button compact" disabled={busy} onClick={() => void makeInviteLink()}>生成邀请链接</button>
              )}
            </div>
          )}
          <div className="form-actions">
            {data.chat_url && <a className="primary-button" href={data.chat_url} target="_blank" rel="noreferrer"><Send size={15} />进入交流群签到</a>}
            <button type="button" className="secondary-button" disabled={busy || locked} title={locked ? `绑定满 7 天后才能解绑（${formatDate(data.unlink_after!)} 起）` : ''} onClick={() => void unlink()}>
              解绑
            </button>
          </div>
          {locked && <p className="muted-text">绑定满 7 天后才能解绑（{formatDate(data.unlink_after!)} 起）。</p>}
        </>
      )}
    </div>
  )
}
