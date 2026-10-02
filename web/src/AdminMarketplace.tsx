import { Fragment, useEffect, useState } from 'react'
import { Flag, MessagesSquare, RefreshCw, Server, X } from 'lucide-react'
import { api, cached, type ChatRoomRecord, type ClearanceRecord, type HostedNodeRecord } from './api'
import ChatRoom from './ChatRoom'
import { AdminReports } from './Reports'
import { walletMoney } from './Wallet'
import { formatTime } from './shared/time'
import { StatusBadge } from './shared/ui'
import { overcommitText } from './Supply'
import { promptDialog } from './shared/dialog'
import { t, tr } from './shared/i18n'

function offlineFor(node: HostedNodeRecord) {
  if (node.status === 'online' || !node.last_seen_at) return ''
  const hours = (Date.now() - new Date(node.last_seen_at).getTime()) / 3_600_000
  return hours < 1 ? t('{0} 分钟', Math.round(hours * 60)) : t('{0} 小时', hours.toFixed(1))
}

export default function AdminMarketplace() {
  const [tab, setTab] = useState<'nodes' | 'chat' | 'reports'>('nodes')
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">HOSTING MARKETPLACE</p>
          <h2>{t('托管管理')}</h2>
          <p>{t('用户托管的母机、托管资金与手续费、清退处理，以及每台母机的聊天室。')}</p>
        </div>
      </div>
      <div className="segmented" role="tablist">
        <button role="tab" aria-selected={tab === 'nodes'} className={tab === 'nodes' ? 'active' : ''} onClick={() => setTab('nodes')}>
          <Server size={15} />{t('托管母机')}
        </button>
        <button role="tab" aria-selected={tab === 'chat'} className={tab === 'chat' ? 'active' : ''} onClick={() => setTab('chat')}>
          <MessagesSquare size={15} />{t('聊天室')}
        </button>
        <button role="tab" aria-selected={tab === 'reports'} className={tab === 'reports' ? 'active' : ''} onClick={() => setTab('reports')}>
          <Flag size={15} />{t('用户举报')}
        </button>
      </div>
      {tab === 'nodes' && <HostedNodes />}
      {tab === 'chat' && <AdminChat />}
      {tab === 'reports' && <AdminReports />}
    </section>
  )
}

function HostedNodes() {
  const [nodes, setNodes] = useState<HostedNodeRecord[]>(() => cached<HostedNodeRecord[]>('/api/v1/admin/marketplace/nodes') ?? [])
  const [expanded, setExpanded] = useState('')
  const [clearing, setClearing] = useState<HostedNodeRecord | null>(null)
  const [capping, setCapping] = useState<HostedNodeRecord | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () =>
    api<HostedNodeRecord[]>('/api/v1/admin/marketplace/nodes')
      .then(setNodes)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
  }, [])

  async function call(path: string, method: string, body: unknown, message: string) {
    setError('')
    setNotice('')
    try {
      await api(path, { method, body: JSON.stringify(body) })
      setNotice(message)
      void load()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('操作失败'))
    }
  }

  async function hold(node: HostedNodeRecord) {
    const hours = await promptDialog({
      title: t('暂缓清退'),
      message: t('在这段时间内不会自动清退该母机。填 0 取消暂缓。'),
      label: t('暂缓小时数（0-2160）'),
      inputType: 'number',
      defaultValue: '72',
      confirmText: t('保存'),
      validate: value => (/^d+$/.test(value.trim()) && Number(value) >= 0 && Number(value) <= 2160 ? '' : t('请输入 0 到 2160 之间的整数小时')),
    })
    if (hours === null) return
    const value = Number(hours)
    const until = value === 0 ? null : new Date(Date.now() + value * 3_600_000).toISOString()
    void call(`/api/v1/admin/marketplace/nodes/${node.id}/hold`, 'PUT', { until }, value === 0 ? t('已取消暂缓') : t('已暂缓清退 {0} 小时', value))
  }

  const active = nodes.filter(node => !node.retired_at)
  const totals = {
    holding: nodes.reduce((sum, node) => sum + node.escrow_holding_minor, 0),
    fee: nodes.reduce((sum, node) => sum + node.fee_minor, 0),
    released: nodes.reduce((sum, node) => sum + node.host_released_minor, 0),
  }
  return (
    <>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <section className="metrics">
        <article><span>{t('托管母机')}</span><strong>{active.length}</strong></article>
        <article><span>{t('离线母机')}</span><strong>{active.filter(node => node.status !== 'online').length}</strong></article>
        <article><span>{t('托管中资金')}</span><strong>{walletMoney(totals.holding)}</strong></article>
        <article><span>{t('手续费收入')}</span><strong>{walletMoney(totals.fee)}</strong></article>
        <article><span>{t('已发放机主收益')}</span><strong>{walletMoney(totals.released)}</strong></article>
      </section>
      <div className="panel">
        <div className="panel-heading">
          <h3>{t('托管母机')}</h3>
          <button className="secondary-button" onClick={() => void load()}>
            <RefreshCw size={15} />{t('刷新')}
          </button>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t('母机')}</th>
                <th>{t('机主')}</th>
                <th>{t('状态')}</th>
                <th>{t('实例')}</th>
                <th>{t('托管中 / 手续费')}</th>
                <th>{t('机主余额')}</th>
                <th>{t('操作')}</th>
              </tr>
            </thead>
            <tbody>
              {nodes.map(node => (
                <Fragment key={node.id}>
                  <tr className={node.retired_at ? 'row-muted' : ''}>
                    <td>
                      <button className="link-button" onClick={() => setExpanded(current => (current === node.id ? '' : node.id))}>
                        <strong>{node.name}</strong>
                      </button>
                      <small className="block">{t('{0} · {1} · 到期 {2}', node.region_name, node.location, node.expires_at)}</small>
                      <small className="block">
                        {t('可售 {0} 核 / {1} MB / {2} GB{3}', node.capacity_vcpu, node.capacity_ram_mb, node.capacity_disk_gb, node.capacity_cap_vcpu || node.capacity_cap_ram_mb || node.capacity_cap_disk_gb ? t('（已核定上限）') : '')}
                      </small>
                      <small className="block">{t('检测 {0} 核 / {1} MB / {2} GB · {3}', node.reported_vcpu, node.reported_ram_mb, node.reported_disk_gb, overcommitText(node.overcommit, true))}</small>
                      {node.health_hold_reason && <small className="block danger-text">{t('负载暂停销售：{0}', tr(node.health_hold_reason))}</small>}
                      {node.shared_machine && <small className="block warn-text">{t('与 {0} 同机', node.shared_machine_with?.join(t('、')))}</small>}
                    </td>
                    <td>
                      {node.owner_name}
                      <small className="block">{node.owner_email}</small>
                    </td>
                    <td>
                      {node.retired_at ? (
                        <span className="tag">{t('已清退')}</span>
                      ) : (
                        <>
                          <span className={node.status === 'online' ? 'tag success' : 'tag danger'}>{node.status === 'online' ? t('在线') : t('离线 {0}', offlineFor(node))}</span>
                          <span className={node.listing_status === 'listed' ? 'tag success' : 'tag'}>{node.listing_status === 'listed' ? t('在售') : t('暂停销售')}</span>
                          {node.clearance_hold_until && new Date(node.clearance_hold_until) > new Date() && (
                            <small className="block">{t('暂缓清退至 {0}', formatTime(node.clearance_hold_until))}</small>
                          )}
                        </>
                      )}
                    </td>
                    <td>{node.active_services}</td>
                    <td>{walletMoney(node.escrow_holding_minor)} / {walletMoney(node.fee_minor)}</td>
                    <td className={node.owner_balance_minor < 0 ? 'amount-negative' : ''}>{walletMoney(node.owner_balance_minor)}</td>
                    <td className="row-actions">
                      {!node.retired_at && (
                        <>
                          <button className="secondary-button compact" onClick={() => void call(`/api/v1/admin/marketplace/nodes/${node.id}/listing`, 'POST', { listed: node.listing_status !== 'listed' }, t('已更新销售状态'))}>
                            {node.listing_status === 'listed' ? t('暂停销售') : t('恢复销售')}
                          </button>
                          <button className="secondary-button compact" onClick={() => hold(node)}>{t('暂缓清退')}</button>
                          <button className="secondary-button compact" onClick={() => setCapping(node)}>{t('核定资源')}</button>
                          <button className="danger-button compact" onClick={() => setClearing(node)}>{t('清退')}</button>
                        </>
                      )}
                      {node.retired_at && <small>{tr(node.retired_reason)}</small>}
                    </td>
                  </tr>
                  {expanded === node.id && (
                    <tr>
                      <td colSpan={7}>
                        <p className="muted-text">{t('线路：{0}', node.line_description)}</p>
                        <table className="nested-table">
                          <thead>
                            <tr>
                              <th>{t('实例')}</th>
                              <th>{t('套餐')}</th>
                              <th>{t('买家')}</th>
                              <th>{t('状态')}</th>
                              <th>{t('剩余价值')}</th>
                            </tr>
                          </thead>
                          <tbody>
                            {(node.services || []).map(item => (
                              <tr key={item.id}>
                                <td><code>{item.instance_name}</code></td>
                                <td>{item.plan_name}</td>
                                <td>{item.buyer_name}</td>
                                <td><span className="badge-pair"><StatusBadge status={item.status} /><StatusBadge status={item.runtime_status} /></span></td>
                                <td>{walletMoney(item.remaining_value_minor)}</td>
                              </tr>
                            ))}
                            {!node.services?.length && (
                              <tr>
                                <td colSpan={5} className="empty-state">{t('暂无实例')}</td>
                              </tr>
                            )}
                          </tbody>
                        </table>
                      </td>
                    </tr>
                  )}
                </Fragment>
              ))}
              {!nodes.length && (
                <tr>
                  <td colSpan={7} className="empty-state">{t('还没有用户托管的母机')}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
      {capping && (
        <CapDialog
          node={capping}
          onClose={() => setCapping(null)}
          onSaved={() => {
            setCapping(null)
            setNotice(t('已更新 {0} 的核定资源', capping.name))
            void load()
          }}
        />
      )}
      {clearing && (
        <ClearDialog
          node={clearing}
          onClose={() => setClearing(null)}
          onCleared={result => {
            setClearing(null)
            setNotice(t('已清退 {0}：{1} 个实例，补偿买家 {2}，机主赔付 {3}', result.node_name, result.services.length, walletMoney(result.refund_minor, result.currency), walletMoney(result.penalty_minor, result.currency)))
            void load()
          }}
        />
      )}
    </>
  )
}

function ClearDialog({ node, onClose, onCleared }: { node: HostedNodeRecord; onClose: () => void; onCleared: (result: ClearanceRecord) => void }) {
  const [multiplier, setMultiplier] = useState(2)
  const [reason, setReason] = useState(node.status === 'online' ? '' : t('母机长时间离线'))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const remaining = (node.services || []).filter(item => item.status !== 'terminated').reduce((sum, item) => sum + item.remaining_value_minor, 0)

  async function submit() {
    setBusy(true)
    setError('')
    try {
      onCleared(await api<ClearanceRecord>(`/api/v1/admin/marketplace/nodes/${node.id}/clear`, { method: 'POST', body: JSON.stringify({ multiplier, reason }) }))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('清退失败'))
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <h3>{t('清退母机 {0}', node.name)}</h3>
        </div>
        <p>
          {t('母机上 {0} 个实例将立即停止服务，母机退出市场。当前剩余价值合计 {1}。', node.active_services, walletMoney(remaining))}
        </p>
        <div className="form-grid">
          <label>
            <span>{t('补偿方式')}</span>
            <select value={multiplier} onChange={event => setMultiplier(Number(event.target.value))}>
              <option value={2}>{t('退还剩余价值 + 机主赔付一份（机主责任：离线、到期、故障、主动下架）')}</option>
              <option value={1}>{t('只退还剩余价值（机主已提前说明的特殊原因）')}</option>
            </select>
          </label>
          <label className="wide">
            <span>{t('清退原因（会发给机主和买家）')}</span>
            <input value={reason} maxLength={200} onChange={event => setReason(event.target.value)} />
          </label>
        </div>
        <p className="muted-text">
          {t('买家拿回剩余价值 {0}。{1}', walletMoney(remaining), multiplier === 2 ? t('另从机主余额赔付最多 {0}，以机主当时的余额为限，不会扣成负数。', walletMoney(remaining)) : t('机主不额外赔付。'))}
        </p>
        {error && <div className="form-error">{error}</div>}
        <div className="form-actions">
          <button className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="danger-button" disabled={busy || !reason.trim()} onClick={() => void submit()}>
            {busy ? t('正在清退…') : t('确认清退')}
          </button>
        </div>
      </div>
    </div>
  )
}

function AdminChat() {
  const [rooms, setRooms] = useState<ChatRoomRecord[]>(() => cached<ChatRoomRecord[]>('/api/v1/admin/chat/rooms') ?? [])
  const [selected, setSelected] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    api<ChatRoomRecord[]>('/api/v1/admin/chat/rooms')
      .then(value => {
        setRooms(value)
        if (value.length) setSelected(current => current || value[0].node_id)
      })
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [])

  const room = rooms.find(item => item.node_id === selected)
  return (
    <>
      {error && <div className="form-error">{error}</div>}
      <div className="support-layout">
        <div className="ticket-list">
          {rooms.map(item => (
            <button key={item.node_id} className={item.node_id === selected ? 'ticket-row selected' : 'ticket-row'} onClick={() => setSelected(item.node_id)}>
              <div>
                <strong>{item.node_name}</strong>
                <small>{t('机主 {0} · {1} 人{2}', item.host_name, item.members, item.retired ? t(' · 已清退') : '')}</small>
                {item.last_message && <small className="block">{t('{0}：{1}', item.last_message.author_name, item.last_message.body.slice(0, 40))}</small>}
              </div>
            </button>
          ))}
          {!rooms.length && <div className="empty-state">{t('暂无聊天室')}</div>}
        </div>
        {room ? (
          <ChatRoom staff base="/api/v1/admin/chat/rooms" nodeID={room.node_id} title={t('{0} 聊天室（机主 {1}）', room.node_name, room.host_name)} />
        ) : (
          <div className="panel support-placeholder">{t('暂无聊天室')}</div>
        )}
      </div>
    </>
  )
}

// CapDialog sets the resources a hosted node may sell, whatever its agent
// reports. Empty fields lift the cap.
function CapDialog({ node, onClose, onSaved }: { node: HostedNodeRecord; onClose: () => void; onSaved: () => void }) {
  const [vcpu, setVCPU] = useState(node.capacity_cap_vcpu ? String(node.capacity_cap_vcpu) : '')
  const [ram, setRAM] = useState(node.capacity_cap_ram_mb ? String(node.capacity_cap_ram_mb) : '')
  const [disk, setDisk] = useState(node.capacity_cap_disk_gb ? String(node.capacity_cap_disk_gb) : '')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const value = (text: string) => (text.trim() ? Number(text) : null)

  async function save() {
    setBusy(true)
    setError('')
    try {
      await api(`/api/v1/admin/marketplace/nodes/${node.id}/capacity-cap`, { method: 'PUT', body: JSON.stringify({ vcpu: value(vcpu), ram_mb: value(ram), disk_gb: value(disk) }) })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <h3>{t('核定资源：{0}', node.name)}</h3>
          <button className="icon-button" aria-label={t('关闭')} onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p className="muted-text">
          {t('母机的真实资源由机主的 Agent 检测上报，平台无法直接核实。收到资源不符的举报并核实后，可以在这里核定真实资源上限；之后 Agent 上报更高的数值也按上限计算。 可售资源 = 核定后的真实资源 × 机主设置的超售倍数。留空表示不限制。 当前检测 {0} 核 / {1} MB / {2} GB，可售 {3} 核 / {4} MB / {5} GB。', node.reported_vcpu, node.reported_ram_mb, node.reported_disk_gb, node.capacity_vcpu, node.capacity_ram_mb, node.capacity_disk_gb)}
        </p>
        {error && <div className="form-error">{error}</div>}
        <div className="form-grid">
          <label><span>{t('vCPU 上限')}</span><input type="number" min={1} value={vcpu} onChange={event => setVCPU(event.target.value)} /></label>
          <label><span>{t('内存上限（MB）')}</span><input type="number" min={64} value={ram} onChange={event => setRAM(event.target.value)} /></label>
          <label><span>{t('磁盘上限（GB）')}</span><input type="number" min={1} value={disk} onChange={event => setDisk(event.target.value)} /></label>
        </div>
        <div className="form-actions">
          <button className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button compact" disabled={busy} onClick={() => void save()}>{busy ? t('保存中…') : t('保存')}</button>
        </div>
      </div>
    </div>
  )
}
