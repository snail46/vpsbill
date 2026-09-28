import { formatTime } from './shared/time'
import { useEffect, useState } from 'react'
import {
  Activity,
  ArrowDown,
  ArrowLeft,
  ArrowUp,
  Boxes,
  Cpu,
  Database,
  HardDrive,
  MemoryStick,
  RefreshCw,
} from 'lucide-react'
import { api, HostProbeDetailRecord } from './api'

type Dict = Record<string, unknown>
const asObject = (value: unknown): Dict => (value && typeof value === 'object' && !Array.isArray(value) ? (value as Dict) : {})
const num = (obj: Dict, key: string) => Number(obj[key] || 0)
const bytes = (value: number) => {
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(2)} GB`
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${Math.round(value)} B`
}
const bps = (value: number) => `${bytes(value)}/s`

const labels: Record<string, string> = {
  generated_at: '报告生成时间',
  hostname: '主机名',
  kernel: '内核版本',
  os: '操作系统',
  cpu: '处理器',
  ram: '实时内存',
  memory: '物理内存',
  disk: '实时磁盘',
  disks: '磁盘设备',
  disk_io: '磁盘 I/O',
  network: '实时网络',
  network_interfaces: '网络接口',
  runtime: '虚拟化运行时',
  system: '系统与主板',
  environment: '环境检查',
  gpus: 'GPU 设备',
  public_ipv4: '公网 IPv4',
  ipv4_addresses: 'IPv4 地址',
  ipv4_prefixes: 'IPv4 网段',
  ipv6_addresses: 'IPv6 地址',
  ipv6_prefixes: 'IPv6 网段',
  gateways: '网关',
  model: '型号',
  cores: '核心数',
  threads: '线程数',
  architecture: '架构',
  flags: 'CPU 指令集',
  total_mb: '总计 MB',
  used_mb: '已用 MB',
  free_mb: '空闲 MB',
  modules: '内存插条',
  name: '名称',
  path: '路径',
  serial: '序列号',
  size_bytes: '容量',
  type: '类型',
  virtual: '虚拟设备',
  rotational: '机械盘',
  mountpoints: '挂载点',
  health: '健康状态',
  health_detail: '健康详情',
  smart: 'S.M.A.R.T.',
  addresses: 'IP 地址',
  mac: 'MAC 地址',
  speed_mbps: '速率 Mbps',
  driver: '驱动',
  vendor: '厂商',
  device: '设备',
  load: '系统负载',
  load1: '1 分钟负载',
  load5: '5 分钟负载',
  load15: '15 分钟负载',
  public_ipv4_interface: 'IPv4 出口网卡',
  public_ipv6: '公网 IPv6',
  public_ipv6_interface: 'IPv6 出口网卡',
  rx_bytes: '累计接收',
  tx_bytes: '累计发送',
  rx_bps: '实时下行速率',
  tx_bps: '实时上行速率',
  read_bytes: '累计读取',
  write_bytes: '累计写入',
  read_bps: '实时读取速率',
  write_bps: '实时写入速率',
}

const title = (key: string) => labels[key] || key.replaceAll('_', ' ').replace(/\b\w/g, c => c.toUpperCase())

function display(value: unknown, key = ''): string {
  if (value === null || value === undefined || value === '') return '—'
  if (typeof value === 'boolean') return value ? '是' : '否'
  if (typeof value === 'number') {
    if (key.endsWith('_bytes')) return bytes(value)
    if (key.endsWith('_bps')) return bps(value)
    return value.toLocaleString()
  }
  return String(value)
}

function isPrimitive(value: unknown) {
  return value === null || ['string', 'number', 'boolean', 'undefined'].includes(typeof value)
}

function StructuredValue({ value, name, depth = 0 }: { value: unknown; name?: string; depth?: number }) {
  if (isPrimitive(value)) return <span className="probe-value">{display(value, name)}</span>

  if (Array.isArray(value)) {
    if (!value.length) return <span className="probe-empty">无数据</span>
    if (value.every(isPrimitive)) {
      return (
        <div className="probe-chips">
          {value.map((item, index) => (
            <span key={index}>{display(item, name)}</span>
          ))}
        </div>
      )
    }
    const flat = value.every(row => row && typeof row === 'object' && !Array.isArray(row) && Object.values(row as Dict).every(isPrimitive))
    if (flat) {
      const keys = Array.from(new Set(value.flatMap(row => Object.keys(row as Dict))))
      return (
        <div className="probe-table-wrap">
          <table className="probe-table">
            <thead>
              <tr>{keys.map(key => <th key={key}>{title(key)}</th>)}</tr>
            </thead>
            <tbody>
              {value.map((row, index) => (
                <tr key={index}>
                  {keys.map(key => <td key={key}>{display((row as Dict)[key], key)}</td>)}
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )
    }
    return (
      <div className="probe-array">
        {value.map((item, index) => (
          <article key={index}>
            <strong>#{index + 1}</strong>
            <StructuredValue value={item} depth={depth + 1} />
          </article>
        ))}
      </div>
    )
  }

  const entries = Object.entries(value as Dict)
  const primitive = entries.filter(([, item]) => isPrimitive(item))
  const complex = entries.filter(([, item]) => !isPrimitive(item))

  return (
    <div className={`probe-object depth-${Math.min(depth, 3)}`}>
      {primitive.length > 0 && (
        <dl>
          {primitive.map(([key, item]) => (
            <div key={key}>
              <dt>{title(key)}</dt>
              <dd>{display(item, key)}</dd>
            </div>
          ))}
        </dl>
      )}
      {complex.map(([key, item]) => (
        <section className="probe-group" key={key}>
          <h4>
            {title(key)}
            <small>{Array.isArray(item) ? `${item.length} 项记录` : ''}</small>
          </h4>
          <StructuredValue value={item} name={key} depth={depth + 1} />
        </section>
      ))}
    </div>
  )
}

function Stat({ label, value, sub, Icon }: { label: string; value: string | number; sub?: string; Icon: typeof Cpu }) {
  return (
    <article className="host-resource-stat">
      <Icon size={20} />
      <div>
        <span>{label}</span>
        <strong>{value}</strong>
        {sub && <small>{sub}</small>}
      </div>
    </article>
  )
}

function LineChart({
  points,
  field,
  label,
  color,
  format = (v: number) => v.toFixed(1),
}: {
  points: Dict[]
  field: string
  label: string
  color: string
  format?: (v: number) => string
}) {
  const values = points.map(point => Number(point[field] || 0))
  const max = Math.max(...values, 1)
  const width = 560
  const height = 120
  const gradientId = `grad-${field}`

  const path = values
    .map((value, index) => {
      const x = values.length === 1 ? 0 : (index / (values.length - 1)) * width
      const y = height - (value / max) * (height - 14) - 7
      return `${index ? 'L' : 'M'} ${x} ${y}`
    })
    .join(' ')

  const areaPath = values.length > 1 ? `${path} L ${width} ${height} L 0 ${height} Z` : ''
  const latest = values.at(-1) || 0

  return (
    <article className="history-chart">
      <header>
        <span>{label}</span>
        <strong>{format(latest)}</strong>
      </header>
      {points.length ? (
        <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
          <defs>
            <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity="0.25" />
              <stop offset="100%" stopColor={color} stopOpacity="0.0" />
            </linearGradient>
          </defs>
          {areaPath && <path d={areaPath} fill={`url(#${gradientId})`} />}
          <path d={path} fill="none" stroke={color} strokeWidth="2" vectorEffect="non-scaling-stroke" />
        </svg>
      ) : (
        <div className="chart-empty">暂无历史采样数据</div>
      )}
      <footer>
        <span>{points.length} 次连续采样</span>
        <span>周期峰值 {format(max)}</span>
      </footer>
    </article>
  )
}

export default function HostDetailPanel({ id, onBack }: { id: string; onBack: () => void }) {
  const [detail, setDetail] = useState<HostProbeDetailRecord | null>(null)
  const [error, setError] = useState('')
  const [loading, setLoading] = useState(true)

  const load = () => {
    setLoading(true)
    setError('')
    api<HostProbeDetailRecord>(`/api/v1/admin/hosts/${id}/probe`)
      .then(setDetail)
      .catch(err => setError(err.message))
      .finally(() => setLoading(false))
  }

  useEffect(() => {
    void load()
  }, [id])

  const dashboard = asObject(detail?.sources.dashboard)
  const info = asObject(detail?.sources.host_info)
  const cpu = asObject(info.cpu)
  const ram = asObject(info.ram)
  const disk = asObject(info.disk)
  const network = asObject(info.network)
  const diskIO = asObject(info.disk_io)
  const loadInfo = asObject(info.load)
  const history = Array.isArray(detail?.sources.host_history) ? detail?.sources.host_history.map(asObject) : []

  const sections: [keyof HostProbeDetailRecord['sources'], string][] = [
    ['dashboard', '容器与实例统计'],
    ['host_info', '实时硬件与系统资源明细'],
    ['host_history', '历史采样时间序列'],
    ['host_report', '宿主机完整硬件、网络与主板报告'],
  ]

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <button className="button-link detail-back" onClick={onBack}>
            <ArrowLeft size={15} />返回宿主机探针列表
          </button>
          <p className="eyebrow">CLICD HOST TELEMETRY</p>
          <h2>{detail?.node.name || '宿主机详情'}</h2>
          <p>
            {detail
              ? `${detail.node.region_name} · ${detail.node.base_url} · 探针更新于 ${formatTime(detail.fetched_at)}`
              : '正在拉取宿主机硬件探针数据…'}
          </p>
        </div>
        <button className="secondary-button" disabled={loading} onClick={load}>
          <RefreshCw size={15} />{loading ? '获取中…' : '重新读取'}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      {detail && (
        <>
          <div className="host-resource-grid">
            <Stat
              Icon={Boxes}
              label="运行实例"
              value={num(dashboard, 'total_containers')}
              sub={`${num(dashboard, 'running')} 运行 / ${num(dashboard, 'stopped')} 关机`}
            />
            <Stat
              Icon={Cpu}
              label="CPU 使用率"
              value={`${num(cpu, 'usage_pct').toFixed(1)}%`}
              sub={`${num(cpu, 'cores')} 物理核心`}
            />
            <Stat
              Icon={MemoryStick}
              label="物理内存占用"
              value={`${num(ram, 'used_mb').toLocaleString()} MB`}
              sub={`总计 ${num(ram, 'total_mb').toLocaleString()} MB`}
            />
            <Stat
              Icon={HardDrive}
              label="磁盘存储占用"
              value={`${num(disk, 'used_gb').toFixed(1)} GB`}
              sub={`总计 ${num(disk, 'total_gb').toFixed(1)} GB`}
            />
            <Stat
              Icon={ArrowDown}
              label="实时下行网络"
              value={bps(num(network, 'rx_bps'))}
              sub={`累计 ${bytes(num(network, 'rx_bytes'))}`}
            />
            <Stat
              Icon={ArrowUp}
              label="实时上行网络"
              value={bps(num(network, 'tx_bps'))}
              sub={`累计 ${bytes(num(network, 'tx_bytes'))}`}
            />
            <Stat
              Icon={Database}
              label="实时磁盘读取"
              value={bps(num(diskIO, 'read_bps'))}
              sub={`累计 ${bytes(num(diskIO, 'read_bytes'))}`}
            />
            <Stat
              Icon={Activity}
              label="系统平均负载"
              value={num(loadInfo, 'load1').toFixed(2)}
              sub={`5m: ${num(loadInfo, 'load5').toFixed(2)} / 15m: ${num(loadInfo, 'load15').toFixed(2)}`}
            />
          </div>

          <div className="history-grid">
            <LineChart points={history} field="cpu" label="CPU 使用率趋势" color="#10b981" format={v => `${v.toFixed(1)}%`} />
            <LineChart points={history} field="memory" label="内存占用趋势" color="#6366f1" format={v => `${v.toFixed(1)}%`} />
            <LineChart points={history} field="network" label="网络吞吐趋势" color="#0284c7" format={bps} />
            <LineChart points={history} field="disk_io" label="磁盘 I/O 趋势" color="#f59e0b" format={bps} />
          </div>

          <div className="probe-sections structured">
            {sections.map(([key, label]) => (
              <details className="probe-section" key={key} open={key === 'host_report' || key === 'host_info'}>
                <summary>
                  <strong>{label}</strong>
                  {detail.errors[key] ? (
                    <span className="status-badge error">探针异常</span>
                  ) : (
                    <span className="status-badge online">正常</span>
                  )}
                </summary>
                {detail.errors[key] ? (
                  <div className="form-error" style={{ margin: '16px' }}>{detail.errors[key]}</div>
                ) : (
                  <div className="probe-content">
                    <StructuredValue value={detail.sources[key]} />
                  </div>
                )}
              </details>
            ))}
          </div>
        </>
      )}
    </section>
  )
}
