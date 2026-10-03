import { FormEvent, useEffect, useState } from 'react'
import { Gift, Link2, Unlink } from 'lucide-react'
import { api } from '../api'
import { chargedMoney, currencySymbol, siteCurrency } from '../shared/currency'
import { confirmDialog } from '../shared/dialog'
import { formatTime } from '../shared/time'
import { toast } from '../shared/toast'
import { t } from '../shared/i18n'

type RedPacketRecord = {
  id: string
  created_by: string
  total_minor: number
  count: number
  remaining_minor: number
  remaining_count: number
  password: string
  require_spent: boolean
  min_linked_days: number
  message_id: number
  status: 'active' | 'finished' | 'expired' | 'cancelled'
  expires_at: string
  created_at: string
  best_name?: string
  best_minor?: number
}

const statusNames: Record<RedPacketRecord['status'], string> = {
  active: t('进行中'),
  finished: t('已领完'),
  expired: t('已过期'),
  cancelled: t('已取消'),
}

// RedPackets hands out red packets in the Telegram group and lists them.
// It hides itself for staff who may not see billing.
export function RedPackets() {
  const [packets, setPackets] = useState<RedPacketRecord[] | null>(null)
  const [hidden, setHidden] = useState(false)
  const [amount, setAmount] = useState('10')
  const [count, setCount] = useState('5')
  const [password, setPassword] = useState('')
  const [post, setPost] = useState(true)
  const [spent, setSpent] = useState(false)
  const [days, setDays] = useState('0')
  const [hours, setHours] = useState('24')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = () =>
    api<RedPacketRecord[]>('/api/v1/admin/telegram/red-packets')
      .then(setPackets)
      .catch(err => ((err as { status?: number }).status === 403 ? setHidden(true) : setError(err instanceof Error ? err.message : t('加载失败'))))
  useEffect(() => {
    void load()
  }, [])

  async function send(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      await api('/api/v1/admin/telegram/red-packets', {
        method: 'POST',
        body: JSON.stringify({
          total_minor: Math.round(Number(amount) * 100), count: Number(count), password: password.trim(), post: post || !password.trim(),
          require_spent: spent, min_linked_days: Number(days), hours: Number(hours),
        }),
      })
      toast('success', t('红包已发出'), password.trim() && !post ? t('口令红包没有发到群里，请把口令公布给大家。') : undefined)
      setPassword('')
      void load()
    } catch (err) {
      const message = err instanceof Error ? err.message : t('发送失败')
      setError(message)
      toast('error', t('红包没有发出'), message)
    } finally {
      setBusy(false)
    }
  }

  async function cancel(packet: RedPacketRecord) {
    if (!(await confirmDialog({ title: t('取消红包'), message: t('取消后没领完的 {0} 不再发放，已领取的不受影响。', chargedMoney(packet.remaining_minor)), confirmText: t('取消红包'), danger: true }))) return
    try {
      await api(`/api/v1/admin/telegram/red-packets/${packet.id}/cancel`, { method: 'POST' })
      void load()
    } catch (err) {
      toast('error', t('取消失败'), err instanceof Error ? err.message : undefined)
    }
  }

  if (hidden) return null
  return (
    <section className="panel red-packets">
      <div className="panel-heading">
        <h3><Gift size={17} />{t('红包')}</h3>
      </div>
      <p className="muted-text">
        {t('红包发到 Telegram 交流群，绑定了站点账号的成员点按钮领取，金额随机；填了口令就是口令红包，成员在群里发送口令领取。每人每个红包限领一次，领到的金额直接存入余额。')}
      </p>
      <form className="form-grid" onSubmit={send}>
        <label>
          <span>{t('总金额（{0}）', currencySymbol(siteCurrency()))}</span>
          <input type="number" min="0.01" max="10000" step="0.01" value={amount} onChange={event => setAmount(event.target.value)} required />
        </label>
        <label>
          <span>{t('个数')}</span>
          <input type="number" min="1" max="100" step="1" value={count} onChange={event => setCount(event.target.value)} required />
        </label>
        <label>
          <span>{t('口令（可选，2–32 个字）')}</span>
          <input value={password} maxLength={32} onChange={event => setPassword(event.target.value)} placeholder={t('留空为点按钮领取')} />
        </label>
        <label>
          <span>{t('有效期（小时）')}</span>
          <input type="number" min="1" max="168" step="1" value={hours} onChange={event => setHours(event.target.value)} required />
        </label>
        <label>
          <span>{t('仅限绑定满几天的账号（0 为不限）')}</span>
          <input type="number" min="0" max="365" step="1" value={days} onChange={event => setDays(event.target.value)} required />
        </label>
        <label className="check-row">
          <input type="checkbox" checked={spent} onChange={event => setSpent(event.target.checked)} />
          {t('仅限消费过的账号领取')}
        </label>
        {!!password.trim() && (
          <label className="check-row">
            <input type="checkbox" checked={post} onChange={event => setPost(event.target.checked)} />
            {t('在群里公布这个口令红包')}
          </label>
        )}
        <div className="form-actions wide">
          <button className="primary-button" disabled={busy}>{busy ? t('正在发送…') : t('发红包')}</button>
        </div>
      </form>
      {error && <div className="form-error">{error}</div>}
      {!!packets?.length && (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t('发出')}</th>
                <th>{t('金额')}</th>
                <th>{t('已领')}</th>
                <th>{t('口令 / 条件')}</th>
                <th>{t('状态')}</th>
                <th />
              </tr>
            </thead>
            <tbody>
              {packets.map(packet => (
                <tr key={packet.id}>
                  <td>
                    {formatTime(packet.created_at)}
                    <small className="block">{packet.created_by}</small>
                  </td>
                  <td>{chargedMoney(packet.total_minor)}</td>
                  <td>
                    {packet.count - packet.remaining_count}/{packet.count}
                    {!!packet.best_name && <small className="block">{t('手气最佳 {0}（{1}）', packet.best_name, chargedMoney(packet.best_minor ?? 0))}</small>}
                  </td>
                  <td>
                    {packet.password || t('按钮领取')}
                    {packet.require_spent && <small className="block">{t('仅限消费过的账号')}</small>}
                    {packet.min_linked_days > 0 && <small className="block">{t('绑定满 {0} 天', packet.min_linked_days)}</small>}
                  </td>
                  <td>
                    <span className={packet.status === 'active' ? 'tag success' : 'tag'}>{statusNames[packet.status]}</span>
                    {packet.status === 'active' && <small className="block">{t('{0} 前有效', formatTime(packet.expires_at))}</small>}
                  </td>
                  <td>{packet.status === 'active' && <button className="text-button" onClick={() => void cancel(packet)}>{t('取消')}</button>}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </section>
  )
}

type StaffTelegramRecord = {
  enabled: boolean
  bot_username: string
  admin_chat: string
  linked: { telegram_id: number; username: string; first_name: string; linked_at: string } | null
}

// StaffTelegram links the signed-in staff member's own Telegram account,
// which lets them answer and claim tickets in the staff group and send red
// packets in the group.
export function StaffTelegram() {
  const [data, setData] = useState<StaffTelegramRecord | null>(null)
  const [opened, setOpened] = useState('')
  const [error, setError] = useState('')

  const load = () =>
    api<StaffTelegramRecord>('/api/v1/admin/telegram/me')
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
  }, [])
  // After opening the bot, look again until the link shows up.
  useEffect(() => {
    if (!opened || data?.linked) return
    const timer = window.setInterval(() => void load(), 3000)
    return () => window.clearInterval(timer)
  }, [opened, data?.linked])

  async function bind() {
    setError('')
    try {
      const result = await api<{ url: string }>('/api/v1/admin/telegram/me/bind', { method: 'POST' })
      setOpened(result.url)
      window.open(result.url, '_blank', 'noopener')
    } catch (err) {
      setError(err instanceof Error ? err.message : t('操作失败'))
    }
  }

  async function unlink() {
    if (!(await confirmDialog({ title: t('解除绑定'), message: t('解除后不能再在管理群里回复和认领工单，也不能在群里发红包。'), confirmText: t('解除绑定'), danger: true }))) return
    try {
      await api('/api/v1/admin/telegram/me', { method: 'DELETE' })
      setOpened('')
      void load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('操作失败'))
    }
  }

  if (!data) return error ? <div className="form-error">{error}</div> : null
  return (
    <div className="panel security-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">TELEGRAM</p>
          <h3>{t('我的 Telegram')}</h3>
        </div>
        <span className={`status-badge ${data.linked ? 'online' : 'disabled'}`}>{data.linked ? t('已绑定') : t('未绑定')}</span>
      </div>
      <p>
        {t('绑定后可以在管理群里直接回复机器人转来的工单通知、点「认领」；有财务权限的管理员还可以在交流群发送 /redpacket 发红包。')}
        {data.admin_chat ? t('当前管理群：{0}。', data.admin_chat) : t('还没有设置管理群（站点设置 → Telegram → 管理群通知）。')}
      </p>
      {error && <div className="form-error">{error}</div>}
      {data.linked ? (
        <>
          <p>{t('已绑定 {0}（{1}）。', data.linked.username ? `@${data.linked.username}` : data.linked.first_name || data.linked.telegram_id, formatTime(data.linked.linked_at))}</p>
          <button className="secondary-button compact" onClick={() => void unlink()}><Unlink size={15} />{t('解除绑定')}</button>
        </>
      ) : !data.enabled ? (
        <p className="muted-text">{t('Telegram 机器人还没有启用。')}</p>
      ) : (
        <>
          {opened && <div className="note-banner">{t('已打开 Telegram，请在机器人对话里点「开始 / Start」，绑定完成后这里会自动更新。')}<a href={opened} target="_blank" rel="noreferrer">{t('没有打开？点这里')}</a></div>}
          <button className="primary-button compact" onClick={() => void bind()}><Link2 size={15} />{t('绑定 Telegram（@{0}）', data.bot_username)}</button>
        </>
      )}
    </div>
  )
}
