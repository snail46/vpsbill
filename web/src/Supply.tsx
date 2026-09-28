import { useState } from 'react'
import { X } from 'lucide-react'
import { api, type NodeSupply, type Overcommit } from './api'
import { formatTime } from './shared/time'

const ratio = (value: number) => `${Number(value.toFixed(2))}×`

// overcommitText summarises a node's oversell ratios for buyers and hosts;
// compact suits table cells.
export function overcommitText(overcommit: Overcommit, compact = false) {
  const { cpu, ram, disk, traffic } = overcommit
  if (cpu <= 1 && ram <= 1 && disk <= 1 && traffic <= 1) return '不超售'
  if (compact) return `超售 CPU${ratio(cpu)} 内存${ratio(ram)} 硬盘${ratio(disk)} 流量${ratio(traffic)}`
  return `超售 CPU ${ratio(cpu)} · 内存 ${ratio(ram)} · 硬盘 ${ratio(disk)} · 流量 ${ratio(traffic)}`
}

function healthText(node: NodeSupply) {
  const health = node.health
  if (!health) return ''
  const parts = [`负载 ${health.load15.toFixed(2)}（${health.cpus} 核）`, `可用内存 ${health.mem_available_mb} / ${health.mem_total_mb} MB`]
  if (health.swap_total_mb > 0) parts.push(`交换 ${health.swap_total_mb - health.swap_free_mb} / ${health.swap_total_mb} MB`)
  for (const disk of health.disks ?? []) parts.push(`${disk.name.startsWith('host:') ? `宿主机磁盘 ${disk.name.slice(5)}` : `${disk.name} 存储`} ${disk.used_gb} / ${disk.total_gb} GB`)
  return parts.join(' · ')
}

// SupplyDetails shows where a node's sellable capacity comes from, its load
// and why it may be held from sale. Staff also see which nodes share the
// machine.
export function SupplyDetails({ node, sellable }: { node: NodeSupply; sellable: { vcpu: number; ram_mb: number; disk_gb: number } }) {
  const health = healthText(node)
  const quotaErrors = Object.entries(node.health?.quota_errors ?? {})
  return (
    <div className="supply-details">
      <small className="block muted-text">
        Agent 检测 {node.reported_vcpu} 核 / {node.reported_ram_mb} MB / {node.reported_disk_gb} GB，
        {overcommitText(node.overcommit)}，可售 {sellable.vcpu} 核 / {sellable.ram_mb} MB / {sellable.disk_gb} GB
        {node.sold_traffic_gb > 0 && `，已售月流量 ${node.sold_traffic_gb} GB`}
      </small>
      {health && <small className="block muted-text">{health}</small>}
      {node.health_hold_reason && (
        <div className="service-notice danger">
          {node.health_hold_since && `${formatTime(node.health_hold_since)} 起`}暂停销售：{node.health_hold_reason}。恢复正常后自动恢复销售。
        </div>
      )}
      {quotaErrors.length > 0 && !node.health_hold_reason && (
        <div className="service-notice danger">{quotaErrors.map(([name, message]) => `${name}：${message}`).join('；')}</div>
      )}
      {node.shared_machine && (
        <div className="service-notice warning">
          与{node.shared_machine_with?.length ? ` ${node.shared_machine_with.join('、')} ` : '其他节点'}运行在同一台机器上，资源合并计算，不会重复出售。
        </div>
      )}
    </div>
  )
}

// OvercommitDialog edits a node's oversell ratios within the platform
// maximums.
export function OvercommitDialog({ node, name, limits, endpoint, onClose, onSaved }: {
  node: NodeSupply
  name: string
  limits?: Overcommit
  endpoint: string
  onClose: () => void
  onSaved: () => void
}) {
  const [value, setValue] = useState<Overcommit>(node.overcommit)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const fields: { key: keyof Overcommit; label: string; hint: string }[] = [
    { key: 'cpu', label: 'CPU', hint: '分时共享，NAT 机负载普遍较低' },
    { key: 'ram', label: '内存', hint: '超太多容易触发 OOM，实例被杀' },
    { key: 'disk', label: '硬盘', hint: '每台实例都有硬盘限额，超售的是尚未用满的空间' },
    { key: 'traffic', label: '月流量', hint: '相对母机月流量额度；未设额度时不限制' },
  ]

  async function save() {
    setBusy(true)
    setError('')
    try {
      await api(endpoint, { method: 'PUT', body: JSON.stringify(value) })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <h3>超售设置：{name}</h3>
          <button className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p className="muted-text">
          可售资源 = Agent 检测到的真实资源 × 超售倍数。倍数会公开显示给买家；1 表示不超售，单台实例的配置不能超过真实资源。
          母机持续负载过高（内存、硬盘或 CPU）时会自动暂停销售，恢复后自动继续。
        </p>
        {error && <div className="form-error">{error}</div>}
        <div className="form-grid">
          {fields.map(field => (
            <label key={field.key}>
              <span>{field.label} 倍数（1–{limits?.[field.key] ?? '?'}）</span>
              <input
                type="number"
                min={1}
                max={limits?.[field.key]}
                step={0.1}
                value={value[field.key]}
                onChange={event => setValue(current => ({ ...current, [field.key]: Number(event.target.value) }))}
              />
              <small>{field.hint}</small>
            </label>
          ))}
        </div>
        <div className="form-actions">
          <button className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button compact" disabled={busy} onClick={() => void save()}>{busy ? '保存中…' : '保存'}</button>
        </div>
      </div>
    </div>
  )
}
