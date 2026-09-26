import { FormEvent, lazy, Suspense, useEffect, useState } from 'react'
import {
  Activity,
  Check,
  Copy,
  Eye,
  EyeOff,
  Gauge,
  Globe,
  HardDrive,
  KeyRound,
  Monitor,
  Network,
  Power,
  RefreshCw,
  RotateCw,
  Server,
  Square,
  TerminalSquare,
  Trash2,
  X,
} from 'lucide-react'
import { api, CustomerServiceRecord, PortMappingRecord, ServiceCredentialRecord, ServiceRuntimeRecord } from './api'

const ServiceConsole = lazy(() => import('./ServiceConsole').then(module => ({ default: module.ServiceConsole })))

const bytes = (value = 0) => {
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(2)} GB`
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${Math.round(value)} B`
}
const rate = (value = 0) => `${bytes(value)}/s`
const percent = (value = 0) => Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0))

const statusMap: Record<string, string> = {
  running: '运行中',
  stopped: '已关机',
  active: '正常',
  creating: '创建中',
  provisioning: '开通中',
  reconciling: '同步中',
  error: '异常',
  suspended: '已暂停',
  overdue: '已逾期',
  terminating: '删除中',
  terminated: '已删除',
}

function StatusBadge({ status }: { status: string }) {
  return <span className={`status-badge ${status}`}>{statusMap[status] ?? status}</span>
}

function Meter({ label, value, text, Icon }: { label: string; value: number; text: string; Icon: typeof Gauge }) {
  return (
    <div className="runtime-meter">
      <div>
        <span><Icon size={14} />{label}</span>
        <strong>{text}</strong>
      </div>
      <i><b style={{ width: `${percent(value)}%` }} /></i>
    </div>
  )
}

function ServiceCard({ service, onReload }: { service: CustomerServiceRecord; onReload: () => void }) {
  const [runtime, setRuntime] = useState<ServiceRuntimeRecord | null>(null)
  const [error, setError] = useState('')
  const [acting, setActing] = useState('')
  const [credential, setCredential] = useState<ServiceCredentialRecord | null>(null)
  const [showPassword, setShowPassword] = useState(false)
  const [copiedKey, setCopiedKey] = useState('')
  const [consoleKind, setConsoleKind] = useState<'ssh' | 'vnc' | null>(null)
  const [dialog, setDialog] = useState<'password' | 'reinstall' | 'ports' | null>(null)

  const load = (brief = true) =>
    api<ServiceRuntimeRecord>(`/api/v1/customer/services/${service.id}/runtime${brief ? '?brief=1' : ''}`)
      .then(setRuntime)
      .catch(err => setError(err.message))

  useEffect(() => {
    if (service.status !== 'active') return
    void load()
    const timer = window.setInterval(() => void load(), 10000)
    return () => window.clearInterval(timer)
  }, [service.id, service.status])

  useEffect(() => {
    if (service.status === 'active') {
      void api<ServiceCredentialRecord>(`/api/v1/customer/services/${service.id}/credential`)
        .then(setCredential)
        .catch(() => {})
    }
  }, [service.id, service.status])

  const copyToClipboard = (text: string | undefined, key: string) => {
    if (!text) return
    void navigator.clipboard.writeText(text)
    setCopiedKey(key)
    setTimeout(() => setCopiedKey(''), 2000)
  }

  const action = async (value: string) => {
    setActing(value)
    setError('')
    try {
      await api(`/api/v1/customer/services/${service.id}/actions/${value}`, { method: 'POST' })
      onReload()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setActing('')
    }
  }

  const usage = runtime?.usage || {}
  const traffic = runtime?.traffic || {}
  // Until the runtime loads, show every action; the node still rejects unsupported calls.
  const capabilities = runtime?.capabilities
  const hasConsole = (kind: string) => !capabilities || capabilities.console.includes(kind)
  const memoryTotal = Number(usage.memory_total_bytes) || service.ram_mb * 1024 ** 2
  const memoryUsed = Number(usage.memory_usage_bytes) || 0
  const diskTotal = service.disk_gb * 1024 ** 3
  const diskUsed = Number(usage.disk_usage_bytes) || 0
  const trafficUsed = Number(traffic.total_used_bytes) || 0
  const trafficLimit = (Number(traffic.limit_gb) || service.traffic_gb) * 1024 ** 3
  const busy = Boolean(service.desired_runtime_status)
  const available = service.status === 'active' && !busy

  return (
    <article className="service-card enhanced">
      <div className="service-card-head">
        <div>
          <span className="tag">{service.virtualization.toUpperCase()}</span>
          <h3>{service.instance_name}</h3>
          <p>{service.plan_name} · {service.region_name}</p>
        </div>
        <StatusBadge status={runtime?.container.status || service.runtime_status} />
      </div>

      <div className="service-specs">
        <span><strong>{service.vcpu}</strong>vCPU</span>
        <span><strong>{service.ram_mb}</strong>MB 内存</span>
        <span><strong>{service.disk_gb}</strong>GB 磁盘</span>
        <span><strong>{service.traffic_gb}</strong>GB 流量</span>
      </div>

      <div className="runtime-grid">
        <Meter
          label="CPU"
          Icon={Activity}
          value={Number(usage.cpu_usage_pct) || 0}
          text={`${(Number(usage.cpu_usage_pct) || 0).toFixed(1)}%`}
        />
        <Meter
          label="内存"
          Icon={Gauge}
          value={(memoryUsed / memoryTotal) * 100}
          text={`${bytes(memoryUsed)} / ${bytes(memoryTotal)}`}
        />
        <Meter
          label="磁盘"
          Icon={HardDrive}
          value={(diskUsed / diskTotal) * 100}
          text={`${bytes(diskUsed)} / ${service.disk_gb} GB`}
        />
        <Meter
          label="流量"
          Icon={Network}
          value={trafficLimit ? (trafficUsed / trafficLimit) * 100 : 0}
          text={`${bytes(trafficUsed)} / ${service.traffic_gb || '∞'} GB`}
        />
      </div>

      <div className="live-io">
        <span title="网络接收速率">↓ {rate(Number(usage.network_rx_bps) || 0)}</span>
        <span title="网络发送速率">↑ {rate(Number(usage.network_tx_bps) || 0)}</span>
        <span title="磁盘读取速率">读 {rate(Number(usage.disk_read_bps) || 0)}</span>
        <span title="磁盘写入速率">写 {rate(Number(usage.disk_write_bps) || 0)}</span>
      </div>

      <div className="network-box">
        <div>
          <span>公网 / 映射 IPv4</span>
          <strong>{service.primary_ipv4 || '等待分配'}</strong>
          {service.primary_ipv4 && (
            <button
              type="button"
              className="button-link"
              onClick={() => copyToClipboard(service.primary_ipv4, 'ipv4')}
              title="复制 IPv4"
            >
              {copiedKey === 'ipv4' ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === 'ipv4' ? '已复制' : '复制'}
            </button>
          )}
        </div>
        <div>
          <span>IPv6 地址</span>
          <strong>{service.primary_ipv6 || '未分配'}</strong>
          {service.primary_ipv6 && (
            <button
              type="button"
              className="button-link"
              onClick={() => copyToClipboard(service.primary_ipv6, 'ipv6')}
              title="复制 IPv6"
            >
              {copiedKey === 'ipv6' ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === 'ipv6' ? '已复制' : '复制'}
            </button>
          )}
        </div>
      </div>

      <div className="credential-row">
        <span><KeyRound size={14} />root 密码</span>
        <code>{credential?.stored ? (showPassword ? credential.password : '••••••••••••') : '未留存，请重置后查看'}</code>
        {credential?.stored && (
          <>
            <button
              className="icon-button"
              onClick={() => setShowPassword(v => !v)}
              title={showPassword ? '隐藏密码' : '显示密码'}
            >
              {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
            <button
              className="icon-button"
              onClick={() => copyToClipboard(credential.password, 'pwd')}
              title="复制密码"
            >
              {copiedKey === 'pwd' ? <Check size={14} /> : <Copy size={14} />}
            </button>
          </>
        )}
      </div>

      {busy && (
        <div className="pending-action">
          <RefreshCw size={14} />正在切换至 {service.desired_runtime_status === 'running' ? '运行' : '关机'} 状态…
        </div>
      )}
      {(error || service.last_reconcile_error) && (
        <div className="service-warning">{error || service.last_reconcile_error}</div>
      )}

      <div className="service-actions primary-row">
        {service.runtime_status === 'stopped' ? (
          <button
            className="primary-button compact"
            disabled={!available || !!acting}
            onClick={() => void action('start')}
          >
            <Power size={14} />开机
          </button>
        ) : (
          <button
            className="secondary-button"
            disabled={!available || !!acting}
            onClick={() => void action('stop')}
          >
            <Square size={13} />关机
          </button>
        )}
        <button
          className="secondary-button"
          disabled={!available || service.runtime_status === 'stopped' || !!acting}
          onClick={() => void action('restart')}
        >
          <RotateCw size={13} />重启
        </button>
        {hasConsole('ssh') && (
          <button
            className="secondary-button"
            disabled={!available || service.runtime_status === 'stopped'}
            onClick={() => setConsoleKind('ssh')}
          >
            <TerminalSquare size={14} />WebSSH
          </button>
        )}
        {service.virtualization === 'kvm' && hasConsole('vnc') && (
          <button
            className="secondary-button"
            disabled={!available || service.runtime_status === 'stopped'}
            onClick={() => setConsoleKind('vnc')}
          >
            <Monitor size={14} />VNC 控制台
          </button>
        )}
      </div>

      <div className="service-actions manage-row">
        {(!capabilities || capabilities.reset_password) && (
          <button className="button-link" disabled={!available} onClick={() => setDialog('password')}>
            <KeyRound size={13} />重置密码
          </button>
        )}
        {(!capabilities || capabilities.reinstall) && (
          <button className="button-link danger" disabled={!available} onClick={() => { void load(false); setDialog('reinstall') }}>
            <Server size={13} />重装系统
          </button>
        )}
        {(!capabilities || capabilities.port_mapping) && (
          <button className="button-link" disabled={!available} onClick={() => setDialog('ports')}>
            <Globe size={13} />IPv4 端口映射 ({runtime?.container.port_mappings?.length || 0}/{runtime?.container.port_mapping_limit || 0})
          </button>
        )}
      </div>

      <footer>
        <span>业务状态：<StatusBadge status={service.status} /></span>
        <span>到期时间：{service.next_due_at ? new Date(service.next_due_at).toLocaleDateString() : '—'}</span>
      </footer>

      {consoleKind && (
        <Suspense fallback={<div className="modal-backdrop"><div className="session-loading"><div className="spinner" /><strong>正在加载控制台…</strong></div></div>}>
          <ServiceConsole serviceID={service.id} name={service.instance_name} kind={consoleKind} onClose={() => setConsoleKind(null)} />
        </Suspense>
      )}

      {dialog && (
        <ServiceDialog
          kind={dialog}
          service={service}
          runtime={runtime}
          onClose={() => setDialog(null)}
          onChanged={async () => {
            await load(false)
            setCredential(await api(`/api/v1/customer/services/${service.id}/credential`))
          }}
        />
      )}
    </article>
  )
}

function ServiceDialog({
  kind,
  service,
  runtime,
  onClose,
  onChanged,
}: {
  kind: 'password' | 'reinstall' | 'ports'
  service: CustomerServiceRecord
  runtime: ServiceRuntimeRecord | null
  onClose: () => void
  onChanged: () => Promise<void>
}) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [mappings, setMappings] = useState<PortMappingRecord[]>(runtime?.container.port_mappings || [])

  useEffect(() => setMappings(runtime?.container.port_mappings || []), [runtime])

  const submit = async (e: FormEvent<HTMLFormElement>) => {
    e.preventDefault()
    const data = new FormData(e.currentTarget)
    setSaving(true)
    setError('')
    try {
      if (kind === 'password') {
        await api(`/api/v1/customer/services/${service.id}/reset-password`, {
          method: 'POST',
          body: JSON.stringify({ password: data.get('password') }),
        })
      } else if (kind === 'reinstall') {
        if (data.get('confirm') !== service.instance_name) {
          throw new Error('请输入实例名称以确认重装')
        }
        await api(`/api/v1/customer/services/${service.id}/reinstall`, {
          method: 'POST',
          body: JSON.stringify({ template_id: data.get('template_id'), password: data.get('password') }),
        })
      } else {
        const next = await api<PortMappingRecord[]>(`/api/v1/customer/services/${service.id}/port-mappings`, {
          method: 'POST',
          body: JSON.stringify({
            container_port: Number(data.get('container_port')),
            protocol: data.get('protocol'),
            description: data.get('description'),
          }),
        })
        setMappings(next)
      }
      await onChanged()
      if (kind !== 'ports') onClose()
      else e.currentTarget.reset()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setSaving(false)
    }
  }

  const remove = async (index: number) => {
    if (!confirm('确定删除该端口映射规则？')) return
    try {
      const next = await api<PortMappingRecord[]>(`/api/v1/customer/services/${service.id}/port-mappings/${index}`, {
        method: 'DELETE',
      })
      setMappings(next)
      await onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  const titles = {
    password: '重置 root 密码',
    reinstall: '重装操作系统',
    ports: 'IPv4 端口映射管理',
  }

  return (
    <div className="modal-backdrop">
      <section className="action-modal">
        <header>
          <div>
            <p className="eyebrow">INSTANCE MANAGEMENT</p>
            <h3>{titles[kind]}</h3>
          </div>
          <button className="icon-button" onClick={onClose}><X size={17} /></button>
        </header>

        {error && <div className="form-error" style={{ margin: '16px 24px 0' }}>{error}</div>}

        {kind === 'ports' && (
          <div className="mapping-list">
            {mappings.map((m, index) => (
              <div key={`${m.host_port}-${m.protocol}`}>
                <code>{m.host_ip || service.primary_ipv4 || '宿主 IP'}:{m.host_port}</code>
                <span>→ {m.container_port}/{m.protocol.toUpperCase()} · {m.description || '未命名'}</span>
                <button
                  className="icon-button danger"
                  disabled={m.description.toLowerCase() === 'ssh'}
                  onClick={() => void remove(index)}
                  title={m.description.toLowerCase() === 'ssh' ? '默认 SSH 端口无法删除' : '删除映射'}
                >
                  <Trash2 size={14} />
                </button>
              </div>
            ))}
            {!mappings.length && <p className="empty-state">暂无外部端口映射</p>}
          </div>
        )}

        <form onSubmit={submit} className="dialog-form">
          {kind === 'password' && (
            <>
              <label>
                <span>新 root 密码（留空自动随机生成）</span>
                <input name="password" type="password" placeholder="8-64 位，包含字母与数字" />
              </label>
              <p style={{ color: 'var(--text-muted)', fontSize: '12.5px' }}>
                重置成功后新密码将加密留存，可直接在 VPS 卡片中查看与一键复制。
              </p>
            </>
          )}

          {kind === 'reinstall' && (
            <>
              <div className="danger-note">
                警告：重装系统将彻底抹除该实例磁盘上的全部数据且无法恢复，请先备份重要文件。
              </div>
              <label>
                <span>目标操作系统镜像</span>
                <select name="template_id" required defaultValue="">
                  <option value="" disabled>请选择系统镜像</option>
                  {runtime?.templates.map(t => (
                    <option value={t.id} key={t.id}>{t.name} · {t.release} ({t.arch})</option>
                  ))}
                </select>
              </label>
              <label>
                <span>新 root 密码</span>
                <input name="password" type="password" required placeholder="8-64 位，包含字母与数字" />
              </label>
              <label>
                <span>输入实例名称确认操作</span>
                <input name="confirm" required placeholder={service.instance_name} />
              </label>
            </>
          )}

          {kind === 'ports' && (
            <div className="mapping-form">
              <label>
                <span>内部端口</span>
                <input name="container_port" type="number" min="1" max="65535" placeholder="80" required />
              </label>
              <label>
                <span>协议</span>
                <select name="protocol" defaultValue="tcp">
                  <option value="tcp">TCP</option>
                  <option value="udp">UDP</option>
                </select>
              </label>
              <label>
                <span>用途备注</span>
                <input name="description" maxLength={80} placeholder="例如 Web 服务" />
              </label>
            </div>
          )}

          <div className="form-actions">
            <button type="button" className="secondary-button" onClick={onClose}>取消</button>
            <button
              className={kind === 'reinstall' ? 'danger-button' : 'primary-button'}
              disabled={saving}
            >
              {saving ? '处理中…' : kind === 'ports' ? '添加映射' : '确认执行'}
            </button>
          </div>
        </form>
      </section>
    </div>
  )
}

export default function CustomerServices() {
  const [services, setServices] = useState<CustomerServiceRecord[]>([])
  const [error, setError] = useState('')

  const load = () =>
    api<CustomerServiceRecord[]>('/api/v1/customer/services')
      .then(setServices)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">COMPUTE</p>
          <h2>我的 VPS</h2>
          <p>实时监控指标每 10 秒自动刷新；所有电源管理、控制台与系统操作均受服务端租户隔离保护。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="service-grid">
        {services.map(service => (
          <ServiceCard key={service.id} service={service} onReload={load} />
        ))}
        {!services.length && (
          <div className="empty-card" style={{ gridColumn: '1 / -1' }}>
            当前账户暂无有效 VPS 服务实例，可前往“选购 VPS”挑选心仪配置。
          </div>
        )}
      </div>
    </section>
  )
}
