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
import { api, cached, serviceUsable, CustomerServiceRecord, PortMappingRecord, ReinstallRecord, RefundQuoteRecord, ServiceCredentialRecord, ServiceRuntimeRecord } from './api'
import { walletMoney } from './Wallet'
import { PushButton } from './Trade'
import { formatDate, formatTime, platformMonth } from './shared/time'
import { navigatePortal, osLabel, osOptions, portalPathPart } from './shared/nav'
import { cycleUnit } from './shared/cycles'
import ChatRoom from './ChatRoom'
import { confirmDialog } from './shared/dialog'
import { PasswordInput } from './shared/password'
import { bandwidthLabel, statusLabel } from './shared/ui'
import { toast } from './shared/toast'
import { t, tr } from './shared/i18n'
import { Backdrop } from './shared/backdrop'

// The console (xterm.js and noVNC) is loaded on demand; the detail page
// fetches it in the background so the first click opens it at once.
// powerActions describe the start, stop and restart buttons: what the
// confirmation says and which state means the action is done.
const powerActions: Record<string, { title: string; message: string; danger?: boolean; done: string; expect: string }> = {
  start: { title: t('开机'), message: t('实例将启动，通常十几秒后可以登录。'), done: t('实例已开机'), expect: 'running' },
  stop: {
    title: t('关机'),
    message: t('实例将关机，正在运行的程序和连接会中断。关机期间服务照常计费，到期时间不变。'),
    danger: true,
    done: t('实例已关机'),
    expect: 'stopped',
  },
  restart: { title: t('重启'), message: t('实例将重启，正在运行的程序和连接会中断，通常一分钟内恢复。'), danger: true, done: t('实例已重启'), expect: 'running' },
}

// A power action that has not finished after this long is reported as
// still running rather than watched forever.
const powerWatchLimit = 3 * 60 * 1000

const loadConsole = () => import('./ServiceConsole')
const ServiceConsole = lazy(() => loadConsole().then(module => ({ default: module.ServiceConsole })))

const bytes = (value = 0) => {
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(2)} GB`
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${Math.round(value)} B`
}
const rate = (value = 0) => `${bytes(value)}/s`
// shortBytes writes a size with at most three digits ("183 MB", "1.7 GB"),
// and usedOf "183 MB / 512 MB", short enough for one line in a meter.
const shortBytes = (value = 0) => {
  const units: [number, string][] = [[1024 ** 4, 'TB'], [1024 ** 3, 'GB'], [1024 ** 2, 'MB'], [1024, 'KB']]
  const [size, unit] = units.find(([size]) => value >= size) ?? [1, 'B']
  const amount = value / size
  return `${amount >= 100 ? Math.round(amount) : Number(amount.toFixed(1))} ${unit}`
}
const usedOf = (used = 0, total = 0) => `${shortBytes(used)} / ${shortBytes(total)}`
const percent = (value = 0) => Math.max(0, Math.min(100, Number.isFinite(value) ? value : 0))

const statusMap: Record<string, string> = {
  running: t('运行中'),
  stopped: t('已关机'),
  active: t('正常'),
  creating: t('创建中'),
  provisioning: t('开通中'),
  reconciling: t('同步中'),
  error: t('异常'),
  suspended: t('已暂停'),
  overdue: t('已逾期'),
  terminating: t('删除中'),
  terminated: t('已删除'),
}

function StatusBadge({ status }: { status: string }) {
  return <span className={`status-badge ${status}`}>{statusMap[status] ?? status}</span>
}

// Meter shows one resource; without a reading yet (pending) it shows a
// placeholder instead of a misleading 0.
function Meter({ label, value, text, Icon, pending }: { label: string; value: number; text: string; Icon: typeof Gauge; pending?: boolean }) {
  return (
    <div className={pending ? 'runtime-meter pending' : 'runtime-meter'}>
      <div>
        <span><Icon size={14} />{label}</span>
        <strong>{pending ? t('读取中…') : text}</strong>
      </div>
      <i><b style={{ width: pending ? undefined : `${percent(value)}%` }} /></i>
    </div>
  )
}

// ServiceManager is the live part of the detail page: notices, monitoring,
// access details and every operation on the instance.
function ServiceManager({ service, onReload }: { service: CustomerServiceRecord; onReload: () => void }) {
  const runtimePath = (brief: boolean) => `/api/v1/customer/services/${service.id}/runtime${brief ? '?brief=1' : ''}`
  // The last reading (from this page or before a reload) shows at once
  // while the node is asked again.
  const [runtime, setRuntime] = useState<ServiceRuntimeRecord | null>(
    () => cached<ServiceRuntimeRecord>(runtimePath(false)) ?? cached<ServiceRuntimeRecord>(runtimePath(true)) ?? null,
  )
  const [error, setError] = useState('')
  const [acting, setActing] = useState('')
  const [credential, setCredential] = useState<ServiceCredentialRecord | null>(
    () => cached<ServiceCredentialRecord>(`/api/v1/customer/services/${service.id}/credential`) ?? null,
  )
  const [showPassword, setShowPassword] = useState(false)
  const [copiedKey, setCopiedKey] = useState('')
  const [consoleKind, setConsoleKind] = useState<'ssh' | 'vnc' | null>(null)
  const [dialog, setDialog] = useState<'password' | 'reinstall' | 'ports' | null>(null)

  const [runtimeError, setRuntimeError] = useState('')
  // A restart can finish on the node before the instance has gone down, so
  // the card tracks it until it has seen the instance leave and return to
  // running (or a minute passes).
  const [restart, setRestart] = useState<{ until: number; sawDown: boolean } | null>(null)
  // The power action the card is waiting on, to tell the customer how it
  // ended; sawBusy turns true once the pending state has shown up.
  const [watching, setWatching] = useState<{ action: string; since: number; sawBusy: boolean } | null>(null)
  // The latest reinstall: it runs in the background and the card follows it.
  const [reinstall, setReinstall] = useState<ReinstallRecord | null>(null)
  const reinstalling = reinstall?.status === 'running'
  const busy = Boolean(service.desired_runtime_status) || restart !== null || reinstalling

  const load = (brief = true) =>
    api<ServiceRuntimeRecord>(runtimePath(brief))
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
    if (!usable) return
    const idle = window.requestIdleCallback ?? ((callback: () => void) => window.setTimeout(callback, 1500))
    idle(() => void loadConsole().catch(() => {}))
  }, [usable])

  // Pick up a reinstall started earlier (another tab, or before a reload),
  // then follow the running one until it ends.
  useEffect(() => {
    if (!usable) return
    void api<ReinstallRecord | null>(`/api/v1/customer/services/${service.id}/reinstall`)
      .then(value => setReinstall(current => current ?? (value?.status === 'running' ? value : null)))
      .catch(() => {})
  }, [service.id, usable])

  useEffect(() => {
    if (!reinstalling) return
    const timer = window.setInterval(() => {
      void api<ReinstallRecord | null>(`/api/v1/customer/services/${service.id}/reinstall`)
        .then(value => {
          if (!value || value.status === 'running') return
          setReinstall(value)
          if (value.status === 'succeeded') {
            onReload()
            void load(false)
            void api<ServiceCredentialRecord>(`/api/v1/customer/services/${service.id}/credential`).then(setCredential).catch(() => {})
          }
        })
        .catch(() => {})
    }, 3000)
    return () => window.clearInterval(timer)
  }, [service.id, reinstalling])

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

  // The service list already says whether a password is kept, so the
  // masked row shows at once; the password itself arrives a moment later
  // and is only needed to reveal or copy it.
  const passwordStored = credential ? credential.stored : service.password_stored
  const loadPassword = async () => {
    if (credential) return credential
    const value = await api<ServiceCredentialRecord>(`/api/v1/customer/services/${service.id}/credential`)
    setCredential(value)
    return value
  }
  const copyPassword = async () => {
    try {
      copyToClipboard((await loadPassword())?.password, 'pwd')
    } catch (err) {
      toast('error', t('读取密码失败'), err instanceof Error ? err.message : undefined)
    }
  }

  const action = async (value: string) => {
    const spec = powerActions[value]
    if (spec) {
      const confirmed = await confirmDialog({
        title: t('确认{0}「{1}」？', spec.title, service.instance_name),
        message: spec.message,
        confirmText: t('确认{0}', spec.title),
        danger: spec.danger,
      })
      if (!confirmed) return
    }
    setActing(value)
    setError('')
    try {
      await api(`/api/v1/customer/services/${service.id}/actions/${value}`, { method: 'POST' })
      if (value === 'restart') setRestart({ until: Date.now() + 60000, sawDown: false })
      if (spec) setWatching({ action: value, since: Date.now(), sawBusy: value === 'restart' })
      onReload()
    } catch (err) {
      const message = err instanceof Error ? err.message : t('操作失败')
      setError(message)
      if (spec) toast('error', t('{0}失败', spec.title), message)
    } finally {
      setActing('')
    }
  }

  const usage = runtime?.usage || {}
  const traffic = runtime?.traffic || {}
  const pending = !runtime
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

  useEffect(() => {
    if (!watching) return
    const spec = powerActions[watching.action]
    const age = Date.now() - watching.since
    if (busy) {
      if (!watching.sawBusy) setWatching({ ...watching, sawBusy: true })
      if (age > powerWatchLimit) {
        setWatching(null)
        toast('info', t('{0}仍在进行', spec.title), t('节点还在处理，请稍后刷新查看实例状态。'))
      }
      return
    }
    // The list may not show the pending state yet right after the request.
    if (!watching.sawBusy && age < 15000) return
    setWatching(null)
    if (liveStatus === spec.expect) toast('success', spec.done, service.instance_name)
    else toast('error', t('{0}未完成', spec.title), tr(service.last_reconcile_error) || t('实例当前状态：{0}', statusLabel(liveStatus)))
    // runtime is a dependency so each poll re-checks the state.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [busy, liveStatus, runtime, watching])

  return (
    <article className="service-card enhanced service-manager">

      {service.termination_reason && (
        <div className="note-banner warn">
          {service.termination_reason === '买家申请退款'
            ? t('已按你的申请取消实例并退款，退款已存入账户余额。')
            : t('{0}。实例已停止服务，按托管准则计算的补偿已存入账户余额。', tr(service.termination_reason))}
        </div>
      )}
      {service.status === 'active' && <TradeAction service={service} onDone={onReload} />}
      {listed && (
        <div className="service-notice warning">{t('该实例正在交易市场挂售，已停机，挂售期间不能开机、登录或修改；到期时间照常计算。下架后可以重新开机。')}</div>
      )}
      {trafficLocked && (
        <div className="service-notice danger">{t('本月流量（上行加下行双向合计）已用尽，实例已停机，下月 1 日（UTC+8）自动恢复并开机。')}</div>
      )}
      {((service.host_name && ['active', 'overdue', 'suspended'].includes(service.status)) || service.status === 'error') && !listed && <RefundPanel service={service} onDone={onReload} />}

      {service.status === 'overdue' && (
        <div className="service-notice warning">
          {t('续费账单已逾期，请在 {0} 前完成支付，否则实例将被暂停。', formatWhen(service.grace_until))}
          <a href="/portal/billing">{t('前往支付')}</a>
        </div>
      )}
      {service.status === 'suspended' && (
        <div className="service-notice danger">
          {t('服务已因欠费暂停，支付续费账单后将自动恢复运行；未续费的实例将于 {0} 删除。', formatWhen(service.termination_scheduled_at))}
          <a href="/portal/billing">{t('前往支付')}</a>
        </div>
      )}
      {(service.status === 'terminating' || service.status === 'terminated') && !service.termination_reason && (
        <div className="service-notice danger">
          {service.status === 'terminating' ? t('服务正在终止，实例与数据即将删除。') : t('服务已终止，实例与数据已删除。')}
        </div>
      )}
      {(service.status === 'provisioning' || service.status === 'pending_payment') && (
        <div className="service-notice">
          {service.status === 'provisioning' ? t('实例正在开通，通常需要 1–2 分钟。') : t('订单待支付，支付完成后自动开通。')}
        </div>
      )}

      {usable && (<>
      <div className="runtime-grid">
        <Meter
          label="CPU"
          pending={pending}
          Icon={Activity}
          value={Number(usage.cpu_usage_pct) || 0}
          text={`${(Number(usage.cpu_usage_pct) || 0).toFixed(1)}%`}
        />
        <Meter
          label={t('内存')}
          pending={pending}
          Icon={Gauge}
          value={(memoryUsed / memoryTotal) * 100}
          text={usedOf(memoryUsed, memoryTotal)}
        />
        <Meter
          label={t('磁盘')}
          pending={pending}
          Icon={HardDrive}
          value={(diskUsed / diskTotal) * 100}
          text={usedOf(diskUsed, service.disk_gb * 1024 ** 3)}
        />
        <Meter
          label={t('流量')}
          pending={pending}
          Icon={Network}
          value={trafficLimit ? (trafficUsed / trafficLimit) * 100 : 0}
          text={service.traffic_gb ? usedOf(trafficUsed, service.traffic_gb * 1024 ** 3) : t('{0} / 不限', shortBytes(trafficUsed))}
        />
      </div>

      {rxBytes != null && txBytes != null && (
        <div className="traffic-split">
          {t('本月流量按双向合计：下行 {0} + 上行 {1} = {2}', bytes(rxBytes), bytes(txBytes), bytes(trafficUsed))}
        </div>
      )}
      <div className={pending ? "live-io pending" : "live-io"}>
        <span title={t('网络接收速率')}>↓ {rate(Number(usage.network_rx_bps) || 0)}</span>
        <span title={t('网络发送速率')}>↑ {rate(Number(usage.network_tx_bps) || 0)}</span>
        <span title={t('磁盘读取速率')}>{t('读 {0}', rate(Number(usage.disk_read_bps) || 0))}</span>
        <span title={t('磁盘写入速率')}>{t('写 {0}', rate(Number(usage.disk_write_bps) || 0))}</span>
      </div>

      <div className="network-box">
        <div>
          <span>{t('公网 / 映射 IPv4')}</span>
          <strong>{service.primary_ipv4 || t('等待分配')}</strong>
          {service.primary_ipv4 && (
            <button
              type="button"
              className="button-link"
              onClick={() => copyToClipboard(service.primary_ipv4, 'ipv4')}
              title={t('复制 IPv4')}
            >
              {copiedKey === 'ipv4' ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === 'ipv4' ? t('已复制') : t('复制')}
            </button>
          )}
        </div>
        {service.primary_ipv4 && Number(runtime?.container.ssh_port) > 0 && (
          <div>
            <span>{t('SSH 登录')}</span>
            <strong>{service.primary_ipv4}:{runtime?.container.ssh_port}</strong>
            <button
              type="button"
              className="button-link"
              onClick={() => copyToClipboard(`ssh root@${service.primary_ipv4} -p ${runtime?.container.ssh_port}`, 'ssh')}
              title={t('复制 SSH 命令')}
            >
              {copiedKey === 'ssh' ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === 'ssh' ? t('已复制') : t('复制命令')}
            </button>
          </div>
        )}
        <div>
          <span>{t('IPv6 地址')}</span>
          <strong>{service.primary_ipv6 || t('未分配')}</strong>
          {service.primary_ipv6 && (
            <button
              type="button"
              className="button-link"
              onClick={() => copyToClipboard(service.primary_ipv6, 'ipv6')}
              title={t('复制 IPv6')}
            >
              {copiedKey === 'ipv6' ? <Check size={13} /> : <Copy size={13} />}
              {copiedKey === 'ipv6' ? t('已复制') : t('复制')}
            </button>
          )}
        </div>
      </div>

      <div className="credential-row">
        <span><KeyRound size={14} />{t('root 密码')}</span>
        <code>
          {passwordStored === undefined
            ? t('读取中…')
            : !passwordStored
              ? t('未留存，请重置后查看')
              : !showPassword
                ? '••••••••••••'
                : (credential?.password ?? t('读取中…'))}
        </code>
        {passwordStored && (
          <>
            <button
              className="icon-button"
              onClick={() => {
                setShowPassword(v => !v)
                if (!credential) void loadPassword().catch(() => {})
              }}
              title={showPassword ? t('隐藏密码') : t('显示密码')}
            >
              {showPassword ? <EyeOff size={14} /> : <Eye size={14} />}
            </button>
            <button
              className="icon-button"
              onClick={() => void copyPassword()}
              title={t('复制密码')}
            >
              {copiedKey === 'pwd' ? <Check size={14} /> : <Copy size={14} />}
            </button>
          </>
        )}
      </div>

      {reinstall && <ReinstallProgress state={reinstall} virtualization={service.virtualization} onDismiss={() => setReinstall(null)} />}
      {busy && !reinstalling && (
        <div className="pending-action">
          <RefreshCw size={14} />
          {restart ? t('正在重启…') : t('正在切换至 {0} 状态…', service.desired_runtime_status === 'running' ? t('运行') : t('关机'))}
        </div>
      )}
      {(error || runtimeError || (usable && service.last_reconcile_error)) && (
        <div className="service-warning">{error || runtimeError || t('节点状态同步暂时异常，系统会自动重试。')}</div>
      )}

      <div className="service-actions primary-row">
        {liveStatus === 'stopped' ? (
          <button
            className="primary-button compact"
            disabled={!available || !!acting}
            onClick={() => void action('start')}
          >
            <Power size={14} />{t('开机')}
          </button>
        ) : (
          <button
            className="secondary-button"
            disabled={!available || !!acting}
            onClick={() => void action('stop')}
          >
            <Square size={13} />{t('关机')}
          </button>
        )}
        <button
          className="secondary-button"
          disabled={!available || liveStatus === 'stopped' || !!acting}
          onClick={() => void action('restart')}
        >
          <RotateCw size={13} />{t('重启')}
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
            <Monitor size={14} />{t('VNC 控制台')}
          </button>
        )}
      </div>

      <div className="service-actions manage-row">
        {(!capabilities || capabilities.reset_password) && (
          <button className="button-link" disabled={!available} onClick={() => setDialog('password')}>
            <KeyRound size={13} />{t('重置密码')}
          </button>
        )}
        {(!capabilities || capabilities.reinstall) && (
          <button className="button-link danger" disabled={!available} onClick={() => { void load(false); setDialog('reinstall') }}>
            <Server size={13} />{t('重装系统')}
          </button>
        )}
        {(!capabilities || capabilities.port_mapping) && (
          <button className="button-link" disabled={!available} onClick={() => setDialog('ports')}>
            <Globe size={13} />{t('IPv4 端口映射 ({0}/{1})', runtime?.container.port_mappings?.length || 0, runtime?.container.port_mapping_limit || 0)}
          </button>
        )}
      </div>
      </>)}

      {consoleKind && (
        <Suspense fallback={<Backdrop><div className="console-loading"><div className="spinner" />{t('正在打开控制台…')}</div></Backdrop>}>
          <ServiceConsole serviceID={service.id} name={service.instance_name} kind={consoleKind} onClose={() => setConsoleKind(null)} />
        </Suspense>
      )}

      {dialog && (
        <ServiceDialog
          kind={dialog}
          service={service}
          runtime={runtime}
          onClose={() => setDialog(null)}
          onReinstall={setReinstall}
          onChanged={async () => {
            await load(false)
            setCredential(await api(`/api/v1/customer/services/${service.id}/credential`))
          }}
        />
      )}
    </article>
  )
}

// reinstallSeconds is about how long a reinstall takes, for the progress
// bar; the node does not report its steps.
const reinstallSeconds: Record<string, number> = { podman: 40, lxc: 90, kvm: 300 }

const reinstallSteps: [number, string][] = [
  [0, t('正在删除旧系统')],
  [0.2, t('正在创建新系统')],
  [0.6, t('正在设置 root 密码和网络')],
  [0.85, t('正在启动并完成收尾')],
]

// ReinstallProgress follows a background reinstall: an estimated progress
// bar while it runs, then how it ended.
function ReinstallProgress({ state, virtualization, onDismiss }: { state: ReinstallRecord; virtualization: string; onDismiss: () => void }) {
  const [now, setNow] = useState(Date.now())
  const running = state.status === 'running'
  useEffect(() => {
    if (!running) return
    const timer = window.setInterval(() => setNow(Date.now()), 500)
    return () => window.clearInterval(timer)
  }, [running])
  const elapsed = Math.max(0, (now - new Date(state.started_at).getTime()) / 1000)
  const expected = reinstallSeconds[virtualization] ?? 120
  // Approaches but never reaches the end until the node says it is done.
  const share = running ? 0.95 * (1 - Math.exp(-elapsed / (expected * 0.6))) : 1
  const step = [...reinstallSteps].reverse().find(([from]) => share >= from)?.[1] ?? reinstallSteps[0][1]
  const minutes = Math.floor(elapsed / 60)
  const clock = minutes ? t('{0} 分 {1} 秒', minutes, Math.floor(elapsed % 60)) : t('{0} 秒', Math.floor(elapsed))
  if (state.status === 'failed') {
    return (
      <div className="reinstall-progress failed" role="alert">
        <div><strong>{t('重装失败')}</strong><button type="button" className="icon-button" onClick={onDismiss} title={t('关闭')}><X size={14} /></button></div>
        <p>{tr(state.error) || t('节点未能完成重装，请稍后重试或联系客服。')}</p>
      </div>
    )
  }
  if (state.status === 'succeeded') {
    return (
      <div className="reinstall-progress done" role="status">
        <div><strong>{t('重装完成')}</strong><button type="button" className="icon-button" onClick={onDismiss} title={t('关闭')}><X size={14} /></button></div>
        <p>{t('新系统 {0} 已就绪，可以用 root 密码登录。', osLabel(state.template_id))}</p>
        <i><b style={{ width: '100%' }} /></i>
      </div>
    )
  }
  return (
    <div className="reinstall-progress" role="status" aria-live="polite">
      <div>
        <strong><RefreshCw size={14} />{t('重装已发起：{0}', osLabel(state.template_id))}</strong>
        <span>{t('{0}% · 已用 {1}', Math.round(share * 100), clock)}</span>
      </div>
      <i><b style={{ width: `${share * 100}%` }} /></i>
      <p>{t('{0}…通常需要 {1}左右，可以离开此页面，重装会在后台继续。', step, expected < 60 ? t('{0} 秒', expected) : t('{0} 分钟', Math.round(expected / 60)))}</p>
    </div>
  )
}

function ServiceDialog({
  kind,
  service,
  runtime,
  onClose,
  onChanged,
  onReinstall,
}: {
  kind: 'password' | 'reinstall' | 'ports'
  service: CustomerServiceRecord
  runtime: ServiceRuntimeRecord | null
  onClose: () => void
  onChanged: () => Promise<void>
  onReinstall: (state: ReinstallRecord) => void
}) {
  const [confirmed, setConfirmed] = useState(false)
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
        if (!confirmed) throw new Error(t('请先勾选确认：重装会清除所有数据'))
        // The reinstall runs in the background; the card shows its progress.
        const state = await api<ReinstallRecord>(`/api/v1/customer/services/${service.id}/reinstall`, {
          method: 'POST',
          body: JSON.stringify({ template_id: data.get('template_id'), password: data.get('password') }),
        })
        onReinstall(state)
        onClose()
        return
      } else {
        const next = await api<PortMappingRecord[]>(`/api/v1/customer/services/${service.id}/port-mappings`, {
          method: 'POST',
          body: JSON.stringify({
            container_port: Number(data.get('container_port')),
            public_port: Number(data.get('public_port')) || 0,
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
      setError(err instanceof Error ? err.message : t('操作失败'))
    } finally {
      setSaving(false)
    }
  }

  const remove = async (index: number) => {
    if (!(await confirmDialog({ title: t('删除这条端口映射？'), message: t('删除后外部将无法通过该端口访问实例。'), confirmText: t('删除'), danger: true }))) return
    try {
      const next = await api<PortMappingRecord[]>(`/api/v1/customer/services/${service.id}/port-mappings/${index}`, {
        method: 'DELETE',
      })
      setMappings(next)
      await onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('删除失败'))
    }
  }

  const titles = {
    password: t('重置 root 密码'),
    reinstall: t('重装操作系统'),
    ports: t('IPv4 端口映射管理'),
  }

  return (
    <Backdrop>
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
                <code>{m.host_ip || service.primary_ipv4 || t('宿主 IP')}:{m.host_port}</code>
                <span>→ {m.container_port}/{m.protocol === 'both' ? 'TCP+UDP' : m.protocol.toUpperCase()} · {m.description || t('未命名')}</span>
                <button
                  className="icon-button danger"
                  disabled={m.description.toLowerCase() === 'ssh'}
                  onClick={() => void remove(index)}
                  title={m.description.toLowerCase() === 'ssh' ? t('默认 SSH 端口无法删除') : t('删除映射')}
                >
                  <Trash2 size={14} />
                </button>
              </div>
            ))}
            {!mappings.length && <p className="empty-state">{t('暂无外部端口映射')}</p>}
          </div>
        )}

        <form onSubmit={submit} className="dialog-form">
          {kind === 'password' && (
            <>
              <label>
                <span>{t('新 root 密码（留空自动随机生成）')}</span>
                <PasswordInput name="password" placeholder={t('8-64 位，包含字母与数字')} />
              </label>
              <p style={{ color: 'var(--text-muted)', fontSize: '12.5px' }}>
                {t('重置成功后新密码将加密留存，可直接在 VPS 卡片中查看与一键复制。')}
              </p>
            </>
          )}

          {kind === 'reinstall' && (
            <>
              <div className="danger-note">
                {t('警告：重装系统将彻底抹除该实例磁盘上的全部数据且无法恢复，请先备份重要文件。')}
              </div>
              <label>
                <span>{t('目标操作系统镜像')}</span>
                <select name="template_id" required defaultValue="">
                  <option value="" disabled>{t('请选择系统镜像')}</option>
                  {osOptions(runtime?.templates.map(t => t.id) ?? []).map(t => (
                    <option value={t.id} key={t.id}>{t.label}</option>
                  ))}
                </select>
              </label>
              <label>
                <span>{t('新 root 密码（留空沿用当前密码）')}</span>
                <PasswordInput name="password" placeholder={t('8-64 位，包含字母与数字')} />
              </label>
              <label className="confirm-check">
                <input type="checkbox" checked={confirmed} onChange={event => setConfirmed(event.target.checked)} />
                <span>{t('重装系统会清除所有数据。我已完成备份，确认重装')}</span>
              </label>
            </>
          )}

          {kind === 'ports' && (
            <div className="mapping-form">
              <label>
                <span>{t('内部端口')}</span>
                <input name="container_port" type="number" min="1" max="65535" placeholder="80" required />
              </label>
              <label>
                <span>{t('公网端口')}</span>
                <input name="public_port" type="number" min="1" max="65535" placeholder={t('留空自动分配')} />
              </label>
              <label>
                <span>{t('协议')}</span>
                <select name="protocol" defaultValue="tcp">
                  <option value="tcp">TCP</option>
                  <option value="udp">UDP</option>
                  <option value="both">TCP+UDP</option>
                </select>
              </label>
              <label>
                <span>{t('用途备注')}</span>
                <input name="description" maxLength={80} placeholder={t('例如 Web 服务')} />
              </label>
            </div>
          )}

          <div className="form-actions">
            <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
            <button
              className={kind === 'reinstall' ? 'danger-button' : 'primary-button'}
              disabled={saving || (kind === 'reinstall' && !confirmed)}
            >
              {saving ? t('处理中…') : kind === 'ports' ? t('添加映射') : t('确认执行')}
            </button>
          </div>
        </form>
      </section>
    </Backdrop>
  )
}

type SourceFilter = 'all' | 'platform' | 'hosted' | 'trade'

const sourceFilters: [SourceFilter, string][] = [
  ['all', t('全部')],
  ['platform', t('平台自营')],
  ['hosted', t('托管市场')],
  ['trade', t('交易市场')],
]

const matchesSource = (service: CustomerServiceRecord, filter: SourceFilter) =>
  filter === 'all' || (filter === 'trade' ? service.via_trade : service.source === filter)

// SourceTags tells where an instance came from: the platform's own nodes or
// a hosted node, and whether it was bought in the trading market.
function SourceTags({ service }: { service: CustomerServiceRecord }) {
  return (
    <>
      {service.source === 'hosted' ? <span className="tag source-hosted">{t('托管市场')}</span> : <span className="tag source-platform">{t('平台自营')}</span>}
      {service.via_trade && <span className="tag source-trade">{t('交易市场购入')}</span>}
    </>
  )
}

function renewalText(service: CustomerServiceRecord) {
  if (service.renewal_price_minor == null) return '—'
  return `${walletMoney(service.renewal_price_minor, service.currency)}/${cycleUnit(service.billing_cycle)}`
}

const serviceHref = (service: CustomerServiceRecord) => `/portal/services/${service.id}`

// ServiceTile is the compact card on the list; the detail page holds
// everything else.
function ServiceTile({ service, onChanged }: { service: CustomerServiceRecord; onChanged: () => void }) {
  const status = serviceUsable(service.status) ? service.runtime_status : service.status
  const ended = service.status === 'terminating' || service.status === 'terminated'
  return (
    <div className={`service-tile source-${service.source}${service.status === 'terminated' ? ' ended' : ''}`}>
    <a
      className="service-tile-link"
      href={serviceHref(service)}
      onClick={event => {
        event.preventDefault()
        navigatePortal(serviceHref(service))
      }}
    >
      <div className="service-tile-head">
        <div className="service-tile-title">
          <strong>{service.plan_name}</strong>
          <small>{service.instance_name}</small>
        </div>
        <StatusBadge status={status} />
      </div>
      <div className="service-tile-tags">
        <SourceTags service={service} />
        <span className="tag">{service.virtualization.toUpperCase()}</span>
      </div>
      <dl className="service-tile-facts">
        <div><dt>{t('配置')}</dt><dd>{t('{0} 核 · {1} · {2} GB', service.vcpu, service.ram_mb >= 1024 ? `${+(service.ram_mb / 1024).toFixed(1)} GB` : `${service.ram_mb} MB`, service.disk_gb)}</dd></div>
        <div><dt>{t('带宽')}</dt><dd>{bandwidthLabel(service.network_down_mbps)}</dd></div>
        <div><dt>{t('系统')}</dt><dd>{osLabel(service.template_id)}</dd></div>
        <div><dt>{t('地域')}</dt><dd>{service.region_name}</dd></div>
        <div><dt>IP</dt><dd>{service.primary_ipv4 || service.primary_ipv6 || '—'}</dd></div>
        <div><dt>{t('到期')}</dt><dd>{service.next_due_at && !ended ? formatDate(service.next_due_at) : '—'}</dd></div>
        <div className="wide">
          <dt>{t('续费')}</dt>
          <dd>{ended ? '—' : renewalText(service)}{!ended && <small>{service.auto_renew ? t(' · 自动') : t(' · 手动')}</small>}</dd>
        </div>
      </dl>
    </a>
    {!ended && (
      <div className="service-tile-actions">
        <PushButton service={service} onDone={onChanged} />
      </div>
    )}
    </div>
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
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="auto-renew">
      <label className="switch">
        <input type="checkbox" checked={service.auto_renew} disabled={busy} onChange={() => void toggle()} />
        <span />
        {t('自动余额续费')}
      </label>
      <small className="muted-text">
        {service.auto_renew ? t('到期前 24 小时从账户余额扣款续费；余额不足时请手动支付续费账单。') : t('已关闭，到期前需要手动支付续费账单。')}
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
        <ArrowLeft size={14} />{t('返回我的 VPS')}
      </button>
      <div className="service-detail-layout">
        <div className="service-detail-side">
          <section className="panel service-detail-head">
            <div className="panel-heading">
              <div>
                <h2>{service.plan_name}</h2>
                <p className="service-detail-name">{service.instance_name}</p>
                <div className="service-tile-tags">
                  <SourceTags service={service} />
                  <span className="tag">{service.virtualization.toUpperCase()}</span>
                </div>
              </div>
              <StatusBadge status={serviceUsable(service.status) ? service.runtime_status : service.status} />
            </div>
            <dl className="detail-facts">
              <div><dt>{t('实例名')}</dt><dd>{service.instance_name}</dd></div>
              <div><dt>{t('地域')}</dt><dd>{service.region_name}</dd></div>
              <div><dt>{t('配置')}</dt><dd>{t('{0} 核 · {1} MB · {2} GB · 月流量 {3}{4}', service.vcpu, service.ram_mb, service.disk_gb, service.traffic_gb || t('不限'), service.traffic_gb ? ' GB' : '')}</dd></div>
              <div><dt>{t('带宽')}</dt><dd>{bandwidthLabel(service.network_down_mbps)}</dd></div>
              <div><dt>{t('系统')}</dt><dd>{osLabel(service.template_id)}</dd></div>
              <div><dt>{t('来源')}</dt><dd>{service.source === 'hosted' ? t('托管市场 · 机主 {0}', service.host_name || '—') : t('平台自营')}{service.via_trade ? t(' · 交易市场购入') : ''}</dd></div>
              <div><dt>{t('业务状态')}</dt><dd><StatusBadge status={service.status} /></dd></div>
              <div><dt>{t('到期时间')}</dt><dd>{service.next_due_at ? formatTime(service.next_due_at) : '—'}</dd></div>
              <div><dt>{t('续费价格')}</dt><dd>{renewalText(service)}</dd></div>
            </dl>
            {!ended && <AutoRenewSwitch service={service} onChanged={onReload} />}
            {service.source === 'hosted' && service.node_id && !ended && (
              <div className="form-actions">
                <button className="secondary-button" onClick={() => setChat(value => !value)}>
                  <MessagesSquare size={14} />{chat ? t('收起母机聊天室') : t('母机聊天室')}
                </button>
              </div>
            )}
          </section>
          {chat && service.node_id && <ChatRoom base="/api/v1/customer/chat/rooms" nodeID={service.node_id} title={t('{0} 的母机聊天室', service.host_name || '')} />}
        </div>
        <ServiceManager service={service} onReload={onReload} />
      </div>
    </>
  )
}

// Terminated services stay listed for reference, below the live ones.
function sortServices(rows: CustomerServiceRecord[]) {
  return [...rows].sort((a, b) => Number(a.status === 'terminated') - Number(b.status === 'terminated'))
}

export default function CustomerServices() {
  const [services, setServices] = useState<CustomerServiceRecord[] | null>(() => {
    const rows = cached<CustomerServiceRecord[]>('/api/v1/customer/services')
    return rows ? sortServices(rows) : null
  })
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(() => portalPathPart(2))
  const [filter, setFilter] = useState<SourceFilter>('all')
  const [showEnded, setShowEnded] = useState(false)

  const load = () =>
    api<CustomerServiceRecord[]>('/api/v1/customer/services')
      .then(rows => setServices(sortServices(rows)))
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
              {t('找不到这台实例。')}<button className="text-button" onClick={() => navigatePortal('/portal/services')}>{t('返回我的 VPS')}</button>
            </div>
          )
        )}
      </section>
    )
  }

  const ended = (services ?? []).filter(item => item.status === 'terminated')
  const visible = (services ?? []).filter(item => matchesSource(item, filter) && (showEnded || item.status !== 'terminated'))
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">COMPUTE</p>
          <h2>{t('我的 VPS')}</h2>
          <p>{t('点击实例进入详情页，查看监控、登录信息并执行开关机、重装等操作。')}</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />{t('刷新')}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="filter-chips" role="tablist" aria-label={t('按来源筛选')}>
        {sourceFilters.map(([id, label]) => (
          <button key={id} role="tab" aria-selected={filter === id} className={filter === id ? 'chip-button active' : 'chip-button'} onClick={() => setFilter(id)}>
            {label}
            {services && <small>{services.filter(item => item.status !== 'terminated' && matchesSource(item, id)).length}</small>}
          </button>
        ))}
        {ended.length > 0 && (
          <label className="notify-option filter-ended">
            <input type="checkbox" checked={showEnded} onChange={event => setShowEnded(event.target.checked)} />
            {t('显示已删除（{0}）', ended.length)}
          </label>
        )}
      </div>

      <div className="service-tiles">
        {visible.map(service => (
          <ServiceTile key={service.id} service={service} onChanged={() => void load()} />
        ))}
        {!services && !error && <div className="empty-card" style={{ gridColumn: '1 / -1' }}>{t('正在加载实例…')}</div>}
        {services && !visible.length && (
          <div className="empty-card" style={{ gridColumn: '1 / -1' }}>
            {filter === 'all' ? t('当前账户暂无 VPS 实例，可前往“选购 VPS”挑选配置。') : t('没有这一来源的实例。')}
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
      setError(err instanceof Error ? err.message : t('无法计算退款'))
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
      setError(err instanceof Error ? err.message : t('退款失败'))
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
          {busy ? t('正在计算…') : t('申请退款')}
        </button>
      </div>
    )
  }
  return (
    <div className="panel nested-panel">
      <div className="panel-heading">
        <h3>{t('申请退款：{0}', quote.instance_name)}</h3>
        <button className="icon-button" aria-label={t('关闭')} onClick={() => setQuote(null)}>
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="refund-summary">
        <span>{quote.full ? (service.status === 'error' ? t('开通失败，全额退款') : t('早期全额退款')) : t('按剩余天数比例退款')}</span>
        <strong>{walletMoney(quote.refund_minor, quote.currency)}</strong>
        <small className="muted-text">
          {t('已付 {0}{1}{2}', walletMoney(quote.paid_minor, quote.currency), quote.traffic_bytes !== null ? t(' · 已用流量 {0}', bytes(quote.traffic_bytes)) : '', quote.message ? ` · ${tr(quote.message)}` : '')}
        </small>
      </div>
      <p className="muted-text">{t('退款存入账户余额（不可提现），实例会立即停止并从母机上删除，数据无法恢复。{0}', quote.full ? '' : t('按比例退款时，当天按已使用计算。'))}</p>
      <div className="form-actions">
        <button className="secondary-button" onClick={() => setQuote(null)}>{t('取消')}</button>
        <button className="danger-button compact" disabled={busy || !quote.available} onClick={() => void confirm()}>
          {busy ? t('正在处理…') : t('确认退款 {0}', walletMoney(quote.refund_minor, quote.currency))}
        </button>
      </div>
    </div>
  )
}

// TradeAction offers the instance on the trading market once it has been
// held long enough, or shows its open listing.
function TradeAction({ service, onDone }: { service: CustomerServiceRecord; onDone: () => void }) {
  return (
    <div className="service-refund">
      <PushButton service={service} onDone={onDone} />
    </div>
  )
}
