import { useState } from 'react'
import { X } from 'lucide-react'
import { api, type NodeSupply, type Overcommit } from './api'
import { formatTime } from './shared/time'
import { t, tr } from './shared/i18n'
import { Backdrop } from './shared/backdrop'

const ratio = (value: number) => `${Number(value.toFixed(2))}×`

// overcommitText summarises a node's oversell ratios for buyers and hosts;
// compact suits table cells.
export function overcommitText(overcommit: Overcommit, compact = false) {
  const { cpu, ram, disk, traffic } = overcommit
  if (cpu <= 1 && ram <= 1 && disk <= 1 && traffic <= 1) return t('不超售')
  if (compact) return t('超售 CPU{0} 内存{1} 硬盘{2} 流量{3}', ratio(cpu), ratio(ram), ratio(disk), ratio(traffic))
  return t('超售 CPU {0} · 内存 {1} · 硬盘 {2} · 流量 {3}', ratio(cpu), ratio(ram), ratio(disk), ratio(traffic))
}

function healthText(node: NodeSupply) {
  const health = node.health
  if (!health) return ''
  const parts = [t('负载 {0}（{1} 核）', health.load15.toFixed(2), health.cpus), t('可用内存 {0} / {1} MB', health.mem_available_mb, health.mem_total_mb)]
  if (health.swap_total_mb > 0) parts.push(t('交换 {0} / {1} MB', health.swap_total_mb - health.swap_free_mb, health.swap_total_mb))
  for (const disk of health.disks ?? []) parts.push(`${disk.name.startsWith('host:') ? t('宿主机磁盘 {0}', disk.name.slice(5)) : t('{0} 存储', disk.name)} ${disk.used_gb} / ${disk.total_gb} GB`)
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
        {t('Agent 检测 {0} 核 / {1} MB / {2} GB，{3}，可售 {4} 核 / {5} MB / {6} GB{7}', node.reported_vcpu, node.reported_ram_mb, node.reported_disk_gb, overcommitText(node.overcommit), sellable.vcpu, sellable.ram_mb, sellable.disk_gb, node.sold_traffic_gb > 0 && t('，已售月流量 {0} GB', node.sold_traffic_gb))}
      </small>
      {health && <small className="block muted-text">{health}</small>}
      {node.health_hold_reason && (
        <div className="service-notice danger">
          {t('{0}暂停销售：{1}。恢复正常后自动恢复销售。', node.health_hold_since && t('{0} 起', formatTime(node.health_hold_since)), tr(node.health_hold_reason))}
        </div>
      )}
      {quotaErrors.length > 0 && !node.health_hold_reason && (
        <div className="service-notice danger">{quotaErrors.map(([name, message]) => t('{0}：{1}', name, tr(message))).join(t('；'))}</div>
      )}
      {node.shared_machine && (
        <div className="service-notice warning">
          {t('与{0}运行在同一台机器上，资源合并计算，不会重复出售。', node.shared_machine_with?.length ? ` ${node.shared_machine_with.join(t('、'))} ` : t('其他节点'))}
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
    { key: 'cpu', label: 'CPU', hint: t('分时共享，NAT 机负载普遍较低') },
    { key: 'ram', label: t('内存'), hint: t('超太多容易触发 OOM，实例被杀') },
    { key: 'disk', label: t('硬盘'), hint: t('每台实例都有硬盘限额，超售的是尚未用满的空间') },
    { key: 'traffic', label: t('月流量'), hint: t('相对母机月流量额度；未设额度时不限制') },
  ]

  async function save() {
    setBusy(true)
    setError('')
    try {
      await api(endpoint, { method: 'PUT', body: JSON.stringify(value) })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <Backdrop role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <h3>{t('超售设置：{0}', name)}</h3>
          <button className="icon-button" aria-label={t('关闭')} onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <p className="muted-text">
          {t('可售资源 = Agent 检测到的真实资源 × 超售倍数。倍数会公开显示给买家；1 表示不超售，单台实例的配置不能超过真实资源。 母机持续负载过高（内存、硬盘或 CPU）时会自动暂停销售，恢复后自动继续。')}
        </p>
        {error && <div className="form-error">{error}</div>}
        <div className="form-grid">
          {fields.map(field => (
            <label key={field.key}>
              <span>{t('{0} 倍数（1–{1}）', field.label, limits?.[field.key] ?? '?')}</span>
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
          <button className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button compact" disabled={busy} onClick={() => void save()}>{busy ? t('保存中…') : t('保存')}</button>
        </div>
      </div>
    </Backdrop>
  )
}
