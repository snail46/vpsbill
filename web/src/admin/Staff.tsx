import { FormEvent, useEffect, useState } from 'react'
import { KeyRound, Plus, ShieldCheck } from 'lucide-react'
import { api, cached, StaffUser } from '../api'
import { formatTime } from '../shared/time'
import { confirmDialog } from '../shared/dialog'
import { toast } from '../shared/toast'
import { t } from '../shared/i18n'

type StaffMember = {
  id: string
  email: string
  display_name: string
  status: 'active' | 'disabled' | string
  role: string
  role_level: number
  mfa_enabled: boolean
  created_at: string
  last_login_at: string | null
}
type StaffRole = { name: string; level: number; permissions: string[]; members: number }
type StaffData = { members: StaffMember[]; roles: StaffRole[] }

// The roles are fixed (see migration 000047); these are their names and
// what they are for.
const roleNames: Record<string, string> = {
  'Super Administrator': t('超级管理员'),
  Administrator: t('管理员'),
  Operations: t('运营'),
  Finance: t('财务'),
  Support: t('客服'),
  'Read Only': t('只读'),
}
const roleNotes: Record<string, string> = {
  'Super Administrator': t('全部权限，包括管理员与角色、数据备份与还原。'),
  Administrator: t('除管理员管理和数据备份外的全部功能，可以修改站点设置和支付网关。'),
  Operations: t('商品、节点、VPS 服务、订单、工单和托管的日常运营；账单和站点设置只读。'),
  Finance: t('账单、确认收款、调整客户余额和订单；其余只读。'),
  Support: t('处理工单和聊天室、操作 VPS 服务；客户、订单、账单只读。'),
  'Read Only': t('所有页面只读，不能修改任何内容。'),
}

export function roleName(role: string) {
  return roleNames[role] ?? role
}

// can reports whether a staff member holds every permission in the list.
export function can(user: Pick<StaffUser, 'permissions'>, ...required: string[]) {
  return required.every(permission => user.permissions.includes('*') || user.permissions.includes(permission))
}

const areas: Array<[string, string]> = [
  ['customers', t('客户管理')],
  ['orders', t('销售订单')],
  ['billing', t('账单、收款与余额')],
  ['services', t('VPS 服务与交易市场')],
  ['nodes', t('节点、宿主机与托管')],
  ['plans', t('商品套餐与优惠码')],
  ['tickets', t('工单、聊天室与举报')],
  ['settings', t('站点设置、支付网关与公告')],
  ['audit', t('审计日志')],
  ['staff', t('管理员与角色')],
  ['backups', t('数据备份与还原')],
]

function access(role: StaffRole, area: string) {
  // The audit log is only ever read.
  if (area === 'audit') return role.permissions.includes('*') || role.permissions.includes('audit:read') ? t('可看') : '—'
  if (role.permissions.includes('*')) return t('读写')
  if (role.permissions.includes(`${area}:write`) || role.permissions.includes(`${area}:manage`)) return t('读写')
  if (role.permissions.includes(`${area}:read`)) return t('只读')
  return '—'
}

// StaffView is where the super administrator adds staff and gives each a
// role; what a role may do is fixed and shown in the table below.
export function StaffView({ user }: { user: StaffUser }) {
  const path = '/api/v1/admin/staff'
  const [data, setData] = useState<StaffData | null>(() => cached<StaffData>(path) ?? null)
  const [adding, setAdding] = useState(false)
  const [resetting, setResetting] = useState<StaffMember | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  const load = () =>
    api<StaffData>(path)
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
  }, [])

  const roles = data?.roles ?? []
  const members = data?.members ?? []

  async function run(action: () => Promise<unknown>, done: string) {
    setBusy(true)
    setError('')
    try {
      await action()
      await load()
      toast('success', done)
      return true
    } catch (err) {
      const message = err instanceof Error ? err.message : t('操作失败')
      setError(message)
      toast('error', t('操作失败'), message)
      return false
    } finally {
      setBusy(false)
    }
  }

  async function add(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const body = { display_name: form.get('display_name'), email: form.get('email'), password: form.get('password'), role: form.get('role') }
    if (await run(() => api(path, { method: 'POST', body: JSON.stringify(body) }), t('管理员已添加'))) setAdding(false)
  }

  async function changeRole(member: StaffMember, role: string) {
    if (role === member.role) return
    if (!(await confirmDialog({ title: t('把「{0}」的角色改为{1}？', member.display_name, roleName(role)), message: roleNotes[role] ?? '', confirmText: t('修改角色') }))) return
    await run(() => api(`${path}/${member.id}`, { method: 'PATCH', body: JSON.stringify({ role }) }), t('角色已修改'))
  }

  async function setStatus(member: StaffMember, status: 'active' | 'disabled') {
    if (status === 'disabled' && !(await confirmDialog({ title: t('停用「{0}」？', member.display_name), message: t('停用后该管理员立即退出登录，不能再进入后台；可以随时重新启用。'), confirmText: t('停用'), danger: true }))) return
    await run(() => api(`${path}/${member.id}`, { method: 'PATCH', body: JSON.stringify({ status }) }), status === 'active' ? t('已启用') : t('已停用'))
  }

  async function remove(member: StaffMember) {
    if (!(await confirmDialog({ title: t('移除管理员「{0}」？', member.display_name), message: t('移除后该账号不能再登录后台，其操作记录仍保留在审计日志里。'), confirmText: t('移除'), danger: true }))) return
    await run(() => api(`${path}/${member.id}`, { method: 'DELETE' }), t('管理员已移除'))
  }

  async function resetPassword(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!resetting) return
    const form = new FormData(event.currentTarget)
    const body = { password: form.get('password'), reset_mfa: form.get('reset_mfa') === 'on' }
    if (await run(() => api(`${path}/${resetting.id}/password`, { method: 'POST', body: JSON.stringify(body) }), t('密码已重置，该管理员已退出登录'))) setResetting(null)
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">STAFF & ROLES</p>
          <h2>{t('管理员与角色')}</h2>
          <p>{t('为每位管理员分配一个角色，角色决定能看到哪些页面、能做哪些操作。只有超级管理员可以管理管理员。')}</p>
        </div>
        <button className="primary-button compact" onClick={() => { setAdding(true); setResetting(null) }}>
          <Plus size={14} />{t('添加管理员')}
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}

      {adding && (
        <form className="panel form-grid" onSubmit={add}>
          <label>
            <span>{t('姓名')}</span>
            <input name="display_name" required maxLength={60} autoComplete="off" />
          </label>
          <label>
            <span>{t('登录邮箱')}</span>
            <input name="email" type="email" required autoComplete="off" />
          </label>
          <label>
            <span>{t('初始密码')}</span>
            <input name="password" type="password" required minLength={12} autoComplete="new-password" />
            <small>{t('至少 12 个字符。请通过安全的方式告诉对方，并让对方登录后在「安全中心」修改密码、开启二步验证。')}</small>
          </label>
          <label>
            <span>{t('角色')}</span>
            <select name="role" defaultValue="Support" required>
              {roles.map(role => <option key={role.name} value={role.name}>{roleName(role.name)}</option>)}
            </select>
          </label>
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={() => setAdding(false)}>{t('取消')}</button>
            <button className="primary-button compact" disabled={busy}>{t('添加')}</button>
          </div>
        </form>
      )}

      {resetting && (
        <form className="panel form-grid" onSubmit={resetPassword}>
          <label>
            <span>{t('为「{0}」设置新密码', resetting.display_name)}</span>
            <input name="password" type="password" required minLength={12} autoComplete="new-password" />
            <small>{t('至少 12 个字符。保存后该管理员的所有登录会话立即失效。')}</small>
          </label>
          <label className="check-row">
            <input type="checkbox" name="reset_mfa" />
            {t('同时关闭其二步验证（验证器丢失时使用）')}
          </label>
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={() => setResetting(null)}>{t('取消')}</button>
            <button className="primary-button compact" disabled={busy}>{t('重置密码')}</button>
          </div>
        </form>
      )}

      <div className="panel">
        <div className="panel-heading">
          <h3>{t('管理员列表')}</h3>
          <span className="tag">{t('{0} 人', members.length)}</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr><th>{t('姓名与邮箱')}</th><th>{t('角色')}</th><th>{t('二步验证')}</th><th>{t('状态')}</th><th>{t('最近登录')}</th><th>{t('操作')}</th></tr>
            </thead>
            <tbody>
              {members.map(member => {
                const self = member.id === user.id
                return (
                  <tr key={member.id}>
                    <td>
                      <strong>{member.display_name}{self && <span className="tag">{t('我')}</span>}</strong>
                      <small className="block">{member.email}</small>
                    </td>
                    <td>
                      <select className="role-select" aria-label={t('角色')} value={member.role} disabled={self || busy} title={self ? t('不能修改自己的角色') : ''} onChange={event => void changeRole(member, event.target.value)}>
                        {roles.map(role => <option key={role.name} value={role.name}>{roleName(role.name)}</option>)}
                      </select>
                    </td>
                    <td><span className={member.mfa_enabled ? 'tag success' : 'tag'}>{member.mfa_enabled ? t('已开启') : t('未开启')}</span></td>
                    <td><span className={member.status === 'active' ? 'tag success' : 'tag'}>{member.status === 'active' ? t('正常') : t('已停用')}</span></td>
                    <td>{member.last_login_at ? formatTime(member.last_login_at) : '—'}</td>
                    <td className="row-actions">
                      {!self && (
                        <>
                          <button className="text-button" disabled={busy} onClick={() => { setResetting(member); setAdding(false) }}><KeyRound size={13} />{t('重置密码')}</button>
                          {member.status === 'active'
                            ? <button className="text-button" disabled={busy} onClick={() => void setStatus(member, 'disabled')}>{t('停用')}</button>
                            : <button className="text-button" disabled={busy} onClick={() => void setStatus(member, 'active')}>{t('启用')}</button>}
                          <button className="text-button danger" disabled={busy} onClick={() => void remove(member)}>{t('移除')}</button>
                        </>
                      )}
                    </td>
                  </tr>
                )
              })}
              {!members.length && <tr><td colSpan={6} className="empty-state">{t('正在加载…')}</td></tr>}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3><ShieldCheck size={16} /> {t('角色权限')}</h3>
          <span className="tag">{t('{0} 个角色', roles.length)}</span>
        </div>
        <ul className="role-notes">
          {roles.map(role => (
            <li key={role.name}>
              <strong>{roleName(role.name)}</strong>
              <span>{roleNotes[role.name] ?? ''}</span>
              <small>{t('{0} 人', role.members)}</small>
            </li>
          ))}
        </ul>
        <div className="table-wrap">
          <table className="role-matrix">
            <thead>
              <tr>
                <th>{t('功能')}</th>
                {roles.map(role => <th key={role.name}>{roleName(role.name)}</th>)}
              </tr>
            </thead>
            <tbody>
              {areas.map(([area, label]) => (
                <tr key={area}>
                  <td>{label}</td>
                  {roles.map(role => {
                    const value = access(role, area)
                    return <td key={role.name} className={value === '—' ? 'muted-text' : ''}>{value}</td>
                  })}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
