import { FormEvent, lazy, Suspense, useEffect, useState } from 'react'
import {
  Activity,
  ArrowLeft,
  Check,
  Copy,
  Eye,
  EyeOff,
  Gauge,
  Globe,
  HardDrive,
  KeyRound,
  MessagesSquare,
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
import { api, imageLabel, serviceUsable, CustomerServiceRecord, PortMappingRecord, RefundQuoteRecord, ServiceCredentialRecord, ServiceRuntimeRecord } from './api'
import { walletMoney } from './Wallet'
import { ListServiceDialog, tradeEligibleAt } from './Trade'
import { formatDate, formatTime, platformMonth } from './shared/time'
import { cycleLabels, navigatePortal, osLabel, portalPathPart } from './shared/nav'
import ChatRoom from './ChatRoom'

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

// ServiceManager is the live part of the detail page: notices, monitoring,
// access details and every operation on the instance.
function ServiceManager({ service, onReload }: { service: CustomerServiceRecord; onReload: () => void }) {
  const [runtime, setRuntime] = useState<ServiceRuntimeRecord | null>(null)
  const [error, setError] = useState('')
  const [acting, setActing] = useState('')
  const [credential, setCredential] = useState<ServiceCredentialRecord | null>(null)
  const [showPassword, setShowPassword] = useState(false)
  const [copiedKey, setCopiedKey] = useState('')
  const [consoleKind, setConsoleKind] = useState<'ssh' | 'vnc' | null>(null)
  const [dialog, setDialog] = useState<'password' | 'reinstall' | 'ports' | null>(null)

  const [runtimeError, setRuntimeError] = useState('')
  // A restart can finish on the node before the instance has gone down, so
  // the card tracks it until it has seen the instance leave and return to
  // running (or a minute passes).
  const [restart, setRestart] = useState<{ until: number; sawDown: boolean } | null>(null)
  const busy = Boolean(service.desired_runtime_status) || restart !== null

  const load = (brief = true) =>
    api<ServiceRuntimeRecord>(`/api/v1/customer/services/${service.id}/runtime${brief ? '?brief=1' : ''}`)
      .then(value => {
        // Brief polls omit templates and history; keep the last full load's.
        setRuntime(previous =>
          brief && previous ? { ...value, templates: previous.templates, history: previous.history } : value,
        )
        setRuntimeError('')
        const running = value.container.status.toLowerCase() === 'running'
        setRestart(current => {
          // Fast restarts may never be observed down; accept "running" again
          // once the node has had a few seconds to act.
          const settled = current && Date.now() > current.until - 50000
          if (!current || Date.now() > current.until || (running && (current.sawDown || settled))) return null
          return running ? current : { ...current, sawDown: true }
        })
      })
      .catch(err => setRuntimeError(err.message))

  // A listed instance is stopped and frozen for its seller.
  const listed = !!service.listing_id
  const usable = serviceUsable(service.status) && !listed
  const trafficLocked = service.traffic_locked_month === platformMonth()

  useEffect(() => {
    if (!usable) return
    void load()
    // While a power action is pending, poll faster and refresh the service
    // list too: the server clears the pending state once the node reports it.
    const timer = window.setInterval(() => {
      void load()
      if (busy) onReload()
    }, busy ? 3000 : 10000)
    return () => window.clearInterval(timer)
  }, [service.id, usable, busy])

  useEffect(() => {
    if (usable) {
      void api<ServiceCredentialRecord>(`/api/v1/customer/services/${service.id}/credential`)
        .then(setCredential)
        .catch(() => {})
    }
  }, [service.id, usable])

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
      if (value === 'restart') setRestart({ until: Date.now() + 60000, sawDown: false })
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
  // Traffic counts both directions.
  const rxBytes = traffic.rx_bytes == null ? null : Number(traffic.rx_bytes)
  const txBytes = traffic.tx_bytes == null ? null : Number(traffic.tx_bytes)
  const trafficUsed = rxBytes != null && txBytes != null ? rxBytes + txBytes : Number(traffic.total_used_bytes) || 0
  const trafficLimit = (Number(traffic.limit_gb) || service.traffic_gb) * 1024 ** 3
  // Prefer the live node status; the list's copy only refreshes on reload.
  const liveStatus = (runtime?.container.status || service.runtime_status).toLowerCase()
  const available = usable && !busy
  const formatWhen = (value?: string) => (value ? formatTime(value) : '—')

  return (
    <article className="service-card enhanced service-manager">

      {service.termination_reason && (
        <div className="note-banner warn">
          {service.termination_reason === '买家申请退款'
            ? '已按你的申请取消实例并退款，退款已存入账户余额。'
            : `${service.termination_reason}。实例已停止服务，按托管准则计算的补偿已存入账户余额。`}
        </div>
      )}
      {service.status === 'active' && <TradeAction service={service} onDone={onReload} />}
      {listed && (
        <div className="service-notice warning">该实例正在交易市场挂售，已停机，挂售期间不能开机、登录或修改；到期时间照常计算。下架后可以重新开机。</div>
      )}
      {trafficLocked && (
        <div className="service-notice danger">本月流量（上行加下行双向合计）已用尽，实例已停机，下月 1 日（UTC+8）自动恢复并开机。</div>
      )}
      {((service.host_name && ['active', 'overdue', 'suspended'].includes(service.status)) || service.status === 'error') && !listed && <RefundPanel service={service} onDone={onReload} />}

      {service.status === 'overdue' && (
        <div className="service-notice warning">
          续费账单已逾期，请在 {formatWhen(service.grace_until)} 前完成支付，否则实例将被暂停。
          <a href="/portal/billing">前往支付</a>
        </div>
      )}
      {service.status === 'suspended' && (
        <div className="service-notice danger">
          服务已因欠费暂停，支付续费账单后将自动恢复运行；未续费的实例将于 {formatWhen(service.termination_scheduled_at)} 删除。
          <a href="/portal/billing">前往支付</a>
        </div>
      )}
      {(service.status === 'terminating' || service.status === 'terminated') && !service.termination_reason && (
        <div className="service-notice danger">
          {service.status === 'terminating' ? '服务正在终止，实例与数据即将删除。' : '服务已终止，实例与数据已删除。'}
        </div>
      )}
      {(service.status === 'provisioning' || service.status === 'pending_payment') && (
        <div className="service-notice">
          {service.status === 'provisioning' ? '实例正在开通，通常需要 1–2 分钟。' : '订单待支付，支付完成后自动开通。'}
        </div>
      )}

      {usable && (<>
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

      {rxBytes != null && txBytes != null && (
        <div className="traffic-split">
          本月流量按双向合计：下行 {bytes(rxBytes)} + 上行 {bytes(txBytes)} = {bytes(trafficUsed)}
        </div>
      )}
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
        {service.primary_ipv4 && Number(runtime?.container.ssh_port) > 0 && (
          <div>
            <span>SSH 登录</span>
            <strong>{service.primary_ipv4}:{runtime?.container.ssh_port}</strong>
            <button
              type="button"
              className="button-link"
              onClick={() => copyToClipboard(`ssh root@${service.primary_ipv4} -p ${runtime?.container.ssh_port}`, 'ssh')}
              title="复制 SSH 命令"
            >
              {copiedKey === 'ssh' ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === 'ssh' ? '已复制' : '复制命令'}
            </button>
          </div>
        )}
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
          <RefreshCw size={14} />
          {restart ? '正在重启…' : `正在切换至 ${service.desired_runtime_status === 'running' ? '运行' : '关机'} 状态…`}
        </div>
      )}
      {(error || runtimeError || (usable && service.last_reconcile_error)) && (
        <div className="service-warning">{error || runtimeError || '节点状态同步暂时异常，系统会自动重试。'}</div>
      )}

      <div className="service-actions primary-row">
        {liveStatus === 'stopped' ? (
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
          disabled={!available || liveStatus === 'stopped' || !!acting}
          onClick={() => void action('restart')}
        >
          <RotateCw size={13} />重启
        </button>
        {hasConsole('ssh') && (
          <button
            className="secondary-button"
            disabled={!available || liveStatus === 'stopped'}
            onClick={() => setConsoleKind('ssh')}
          >
            <TerminalSquare size={14} />WebSSH
          </button>
        )}
        {service.virtualization === 'kvm' && hasConsole('vnc') && (
          <button
            className="secondary-button"
            disabled={!available || liveStatus === 'stopped'}
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
      </>)}

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
    // React clears currentTarget once the handler yields, so keep the form.
    const form = e.currentTarget
    const data = new FormData(form)
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
      else form.reset()
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
                    <option value={t.id} key={t.id}>{imageLabel(t)}</option>
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

type SourceFilter = 'all' | 'platform' | 'hosted' | 'trade'

const sourceFilters: [SourceFilter, string][] = [
  ['all', '全部'],
  ['platform', '平台自营'],
  ['hosted', '托管市场'],
  ['trade', '交易市场'],
]

const matchesSource = (service: CustomerServiceRecord, filter: SourceFilter) =>
  filter === 'all' || (filter === 'trade' ? service.via_trade : service.source === filter)

// SourceTags tells where an instance came from: the platform's own nodes or
// a hosted node, and whether it was bought in the trading market.
function SourceTags({ service }: { service: CustomerServiceRecord }) {
  return (
    <>
      {service.source === 'hosted' ? <span className="tag source-hosted">托管市场</span> : <span className="tag source-platform">平台自营</span>}
      {service.via_trade && <span className="tag source-trade">交易市场购入</span>}
    </>
  )
}

function renewalText(service: CustomerServiceRecord) {
  if (service.renewal_price_minor == null) return '—'
  return `${walletMoney(service.renewal_price_minor, service.currency)}/${cycleLabels[service.billing_cycle] ?? service.billing_cycle}`
}

const serviceHref = (service: CustomerServiceRecord) => `/portal/services/${service.id}`

// ServiceTile is the compact card on the list; the detail page holds
// everything else.
function ServiceTile({ service }: { service: CustomerServiceRecord }) {
  const status = serviceUsable(service.status) ? service.runtime_status : service.status
  return (
    <a
      className={`service-tile source-${service.source}${service.status === 'terminated' ? ' ended' : ''}`}
      href={serviceHref(service)}
      onClick={event => {
        event.preventDefault()
        navigatePortal(serviceHref(service))
      }}
    >
      <div className="service-tile-head">
        <strong>{service.instance_name}</strong>
        <StatusBadge status={status} />
      </div>
      <div className="service-tile-tags">
        <SourceTags service={service} />
        <span className="tag">{service.virtualization.toUpperCase()}</span>
      </div>
      <dl className="service-tile-facts">
        <div><dt>配置</dt><dd>{service.vcpu} 核 · {service.ram_mb >= 1024 ? `${+(service.ram_mb / 1024).toFixed(1)} GB` : `${service.ram_mb} MB`} · {service.disk_gb} GB</dd></div>
        <div><dt>系统</dt><dd>{osLabel(service.template_id)}</dd></div>
        <div><dt>地域</dt><dd>{service.region_name}</dd></div>
        <div><dt>IP</dt><dd>{service.primary_ipv4 || service.primary_ipv6 || '—'}</dd></div>
        <div><dt>到期</dt><dd>{service.next_due_at ? formatDate(service.next_due_at) : '—'}</dd></div>
        <div>
          <dt>续费</dt>
          <dd>{renewalText(service)}{service.status !== 'terminated' && <small>{service.auto_renew ? ' · 自动' : ' · 手动'}</small>}</dd>
        </div>
      </dl>
    </a>
  )
}

// AutoRenewSwitch turns balance renewal on or off for one instance.
function AutoRenewSwitch({ service, onChanged }: { service: CustomerServiceRecord; onChanged: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  async function toggle() {
    setBusy(true)
    setError('')
    try {
      await api(`/api/v1/customer/services/${service.id}/auto-renew`, { method: 'PUT', body: JSON.stringify({ enabled: !service.auto_renew }) })
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="auto-renew">
      <label className="switch">
        <input type="checkbox" checked={service.auto_renew} disabled={busy} onChange={() => void toggle()} />
        <span />
        自动余额续费
      </label>
      <small className="muted-text">
        {service.auto_renew ? '到期前 24 小时从账户余额扣款续费；余额不足时请手动支付续费账单。' : '已关闭，到期前需要手动支付续费账单。'}
      </small>
      {error && <small className="danger-text">{error}</small>}
    </div>
  )
}

// ServiceDetail is the page for one instance: its facts, renewal and
// every operation.
function ServiceDetail({ service, onReload }: { service: CustomerServiceRecord; onReload: () => void }) {
  const [chat, setChat] = useState(false)
  const ended = service.status === 'terminating' || service.status === 'terminated'
  return (
    <>
      <button className="text-button back-link" onClick={() => navigatePortal('/portal/services')}>
        <ArrowLeft size={14} />返回我的 VPS
      </button>
      <section className="panel service-detail-head">
        <div className="panel-heading">
          <div>
            <h2>{service.instance_name}</h2>
            <div className="service-tile-tags">
              <SourceTags service={service} />
              <span className="tag">{service.virtualization.toUpperCase()}</span>
            </div>
          </div>
          <StatusBadge status={serviceUsable(service.status) ? service.runtime_status : service.status} />
        </div>
        <dl className="detail-facts">
          <div><dt>套餐</dt><dd>{service.plan_name}</dd></div>
          <div><dt>地域</dt><dd>{service.region_name}</dd></div>
          <div><dt>配置</dt><dd>{service.vcpu} 核 · {service.ram_mb} MB · {service.disk_gb} GB · 月流量 {service.traffic_gb || '不限'}{service.traffic_gb ? ' GB' : ''}</dd></div>
          <div><dt>系统</dt><dd>{osLabel(service.template_id)}</dd></div>
          <div><dt>来源</dt><dd>{service.source === 'hosted' ? `托管市场 · 机主 ${service.host_name || '—'}` : '平台自营'}{service.via_trade ? ' · 交易市场购入' : ''}</dd></div>
          <div><dt>业务状态</dt><dd><StatusBadge status={service.status} /></dd></div>
          <div><dt>到期时间</dt><dd>{service.next_due_at ? formatTime(service.next_due_at) : '—'}</dd></div>
          <div><dt>续费价格</dt><dd>{renewalText(service)}</dd></div>
        </dl>
        {!ended && <AutoRenewSwitch service={service} onChanged={onReload} />}
        {service.source === 'hosted' && service.node_id && !ended && (
          <div className="form-actions">
            <button className="secondary-button" onClick={() => setChat(value => !value)}>
              <MessagesSquare size={14} />{chat ? '收起母机聊天室' : '母机聊天室'}
            </button>
          </div>
        )}
      </section>
      {chat && service.node_id && <ChatRoom base="/api/v1/customer/chat/rooms" nodeID={service.node_id} title={`${service.host_name || ''} 的母机聊天室`} />}
      <ServiceManager service={service} onReload={onReload} />
    </>
  )
}

export default function CustomerServices() {
  const [services, setServices] = useState<CustomerServiceRecord[] | null>(null)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(() => portalPathPart(2))
  const [filter, setFilter] = useState<SourceFilter>('all')

  const load = () =>
    api<CustomerServiceRecord[]>('/api/v1/customer/services')
      // Keep terminated services for reference, below the live ones.
      .then(rows => setServices([...rows].sort((a, b) => Number(a.status === 'terminated') - Number(b.status === 'terminated'))))
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
    const follow = () => setSelected(portalPathPart(2))
    window.addEventListener('popstate', follow)
    return () => window.removeEventListener('popstate', follow)
  }, [])

  const current = selected ? services?.find(item => item.id === selected) : undefined
  if (selected) {
    return (
      <section className="workspace-panel">
        {error && <div className="form-error">{error}</div>}
        {current ? (
          <ServiceDetail key={current.id} service={current} onReload={load} />
        ) : (
          services && (
            <div className="empty-card">
              找不到这台实例。<button className="text-button" onClick={() => navigatePortal('/portal/services')}>返回我的 VPS</button>
            </div>
          )
        )}
      </section>
    )
  }

  const visible = (services ?? []).filter(item => matchesSource(item, filter))
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">COMPUTE</p>
          <h2>我的 VPS</h2>
          <p>点击实例进入详情页，查看监控、登录信息并执行开关机、重装等操作。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="filter-chips" role="tablist" aria-label="按来源筛选">
        {sourceFilters.map(([id, label]) => (
          <button key={id} role="tab" aria-selected={filter === id} className={filter === id ? 'chip-button active' : 'chip-button'} onClick={() => setFilter(id)}>
            {label}
            <small>{(services ?? []).filter(item => item.status !== 'terminated' && matchesSource(item, id)).length}</small>
          </button>
        ))}
      </div>

      <div className="service-tiles">
        {visible.map(service => (
          <ServiceTile key={service.id} service={service} />
        ))}
        {services && !visible.length && (
          <div className="empty-card" style={{ gridColumn: '1 / -1' }}>
            {filter === 'all' ? '当前账户暂无 VPS 实例，可前往“选购 VPS”挑选配置。' : '没有这一来源的实例。'}
          </div>
        )}
      </div>
    </section>
  )
}

// RefundPanel lets the buyer of a hosted instance cancel it for a refund to
// the balance, after showing the amount the rules give right now.
function RefundPanel({ service, onDone }: { service: CustomerServiceRecord; onDone: () => void }) {
  const [quote, setQuote] = useState<RefundQuoteRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function load() {
    setBusy(true)
    setError('')
    try {
      setQuote(await api<RefundQuoteRecord>(`/api/v1/customer/services/${service.id}/refund`))
    } catch (err) {
      setError(err instanceof Error ? err.message : '无法计算退款')
    } finally {
      setBusy(false)
    }
  }

  async function confirm() {
    if (!quote) return
    setBusy(true)
    setError('')
    try {
      await api(`/api/v1/customer/services/${service.id}/refund`, { method: 'POST', body: JSON.stringify({ expected_minor: quote.refund_minor }) })
      setQuote(null)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '退款失败')
      void load()
    } finally {
      setBusy(false)
    }
  }

  if (!quote) {
    return (
      <div className="service-refund">
        {error && <div className="form-error">{error}</div>}
        <button className="secondary-button compact" disabled={busy} onClick={() => void load()}>
          {busy ? '正在计算…' : '申请退款'}
        </button>
      </div>
    )
  }
  return (
    <div className="panel nested-panel">
      <div className="panel-heading">
        <h3>申请退款：{quote.instance_name}</h3>
        <button className="icon-button" aria-label="关闭" onClick={() => setQuote(null)}>
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="refund-summary">
        <span>{quote.full ? (service.status === 'error' ? '开通失败，全额退款' : '早期全额退款') : '按剩余天数比例退款'}</span>
        <strong>{walletMoney(quote.refund_minor, quote.currency)}</strong>
        <small className="muted-text">
          已付 {walletMoney(quote.paid_minor, quote.currency)}
          {quote.traffic_bytes !== null ? ` · 已用流量 ${bytes(quote.traffic_bytes)}` : ''}
          {quote.message ? ` · ${quote.message}` : ''}
        </small>
      </div>
      <p className="muted-text">退款存入账户余额（不可提现），实例会立即停止并从母机上删除，数据无法恢复。{quote.full ? '' : '按比例退款时，当天按已使用计算。'}</p>
      <div className="form-actions">
        <button className="secondary-button" onClick={() => setQuote(null)}>取消</button>
        <button className="danger-button compact" disabled={busy || !quote.available} onClick={() => void confirm()}>
          {busy ? '正在处理…' : `确认退款 ${walletMoney(quote.refund_minor, quote.currency)}`}
        </button>
      </div>
    </div>
  )
}

// TradeAction offers the instance on the trading market once it has been
// held long enough, or shows its open listing.
function TradeAction({ service, onDone }: { service: CustomerServiceRecord; onDone: () => void }) {
  const [open, setOpen] = useState(false)
  if (service.listing_id) {
    return (
      <div className="service-refund">
        <span className="tag">交易市场挂售中 · {walletMoney(service.listing_price_minor || 0)}</span>
        <a className="secondary-button compact" href="/portal/trade">管理挂售</a>
      </div>
    )
  }
  const eligibleAt = tradeEligibleAt(service)
  if (eligibleAt.getTime() > Date.now()) {
    return (
      <div className="service-refund">
        <small className="muted-text">持有满 31 天后可在交易市场挂售（{formatDate(eligibleAt)} 起）</small>
      </div>
    )
  }
  return (
    <>
      <div className="service-refund">
        <button className="secondary-button compact" onClick={() => setOpen(true)}>挂售到交易市场</button>
      </div>
      {open && (
        <ListServiceDialog
          service={service}
          onClose={() => setOpen(false)}
          onDone={() => {
            setOpen(false)
            onDone()
          }}
        />
      )}
    </>
  )
}
