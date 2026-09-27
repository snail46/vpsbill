import { ChangeEvent, FormEvent, useEffect, useState } from 'react'
import {
  Activity,
  AlertCircle,
  ArrowLeft,
  Boxes,
  CheckCircle2,
  ChevronRight,
  CircleDollarSign,
  Cpu,
  CreditCard,
  Headphones,
  KeyRound,
  LayoutDashboard,
  LogOut,
  PackageOpen,
  Plus,
  Power,
  ReceiptText,
  RefreshCw,
  RotateCw,
  ScrollText,
  Send,
  ServerCog,
  Settings,
  SlidersHorizontal,
  ShieldCheck,
  ShoppingCart,
  Square,
  UserCircle,
  Users,
  WalletCards,
  X,
} from 'lucide-react'
import {
  AccountRecord,
  api,
  imageLabel,
  AuditLogRecord,
  AvailableTemplateRecord,
  CustomerCatalogRecord,
  CustomerIdentity,
  CustomerInvoiceRecord,
  CustomerServiceRecord,
  CustomerTransactionRecord,
  HostProbeRecord,
  InvoiceRecord,
  NodeRecord,
  ProviderTypeRecord,
  OperationsOverviewRecord,
  OrderRecord,
  PaymentIntentRecord,
  PaymentSettingsRecord,
  PlanRecord,
  ProvisioningJobRecord,
  RegionRecord,
  ServiceRecord,
  StaffUser,
  TicketDetailRecord,
  TicketRecord,
  TransactionRecord,
} from './api'
import CustomerServicesPanel from './CustomerServices'
import HostDetailPanel from './HostDetail'

type Meta = { name: string; environment: string; installed: boolean; capabilities: string[]; password_reset_mail?: boolean }
type View =
  | 'overview'
  | 'customers'
  | 'orders'
  | 'billing'
  | 'payment'
  | 'services'
  | 'nodes'
  | 'hosts'
  | 'plans'
  | 'support'
  | 'audit'
  | 'settings'
  | 'security'
type AuthScreen = 'loading' | 'install' | 'login' | 'ready'

function SessionLoading({ portal }: { portal: 'admin' | 'customer' }) {
  return (
    <main className="session-loading">
      <div className="brand-mark">VB</div>
      <div className="spinner" />
      <strong>正在恢复{portal === 'admin' ? '商家控制中心' : '客户中心'}会话…</strong>
    </main>
  )
}

const navItems: Array<{ id: View; label: string; icon: typeof LayoutDashboard }> = [
  { id: 'overview', label: '运营概览', icon: LayoutDashboard },
  { id: 'customers', label: '客户管理', icon: Users },
  { id: 'orders', label: '销售订单', icon: ReceiptText },
  { id: 'billing', label: '账单与交易', icon: CircleDollarSign },
  { id: 'payment', label: '支付网关', icon: CreditCard },
  { id: 'services', label: 'VPS 服务', icon: Boxes },
  { id: 'plans', label: '商品套餐', icon: PackageOpen },
  { id: 'nodes', label: '节点对接', icon: ServerCog },
  { id: 'hosts', label: '宿主机探针', icon: Cpu },
  { id: 'support', label: '客户工单', icon: Headphones },
  { id: 'audit', label: '审计日志', icon: ScrollText },
  { id: 'settings', label: '站点设置', icon: SlidersHorizontal },
  { id: 'security', label: '安全中心', icon: Settings },
]

const adminViews: View[] = [
  'overview',
  'customers',
  'orders',
  'billing',
  'payment',
  'services',
  'nodes',
  'hosts',
  'plans',
  'support',
  'audit',
  'settings',
  'security',
]

type AdminRoute = { view: View; hostID?: string }

function adminRouteFromPath(): AdminRoute {
  const parts = window.location.pathname.split('/').filter(Boolean)
  if (parts[0] === 'admin' && parts[1] === 'hosts' && parts[2]) {
    return { view: 'hosts', hostID: decodeURIComponent(parts[2]) }
  }
  const candidate = (parts[0] === 'admin' ? parts[1] : undefined) as View
  return { view: adminViews.includes(candidate) ? candidate : 'overview' }
}

function adminRoutePath(route: AdminRoute) {
  return route.hostID ? `/admin/hosts/${encodeURIComponent(route.hostID)}` : `/admin/${route.view}`
}

export function App() {
  // Customers land on the site root; the merchant console lives under /admin.
  return window.location.pathname.startsWith('/admin') ? <AdminApp /> : <CustomerPortalApp />
}

function AdminApp() {
  const [meta, setMeta] = useState<Meta | null>(null)
  const [authScreen, setAuthScreen] = useState<AuthScreen>('loading')
  const [user, setUser] = useState<StaffUser | null>(null)

  useEffect(() => {
    Promise.all([api<Meta>('/api/v1/meta'), api<{ required: boolean }>('/api/v1/install')])
      .then(async ([currentMeta, installation]) => {
        setMeta(currentMeta)
        if (installation.required) {
          setAuthScreen('install')
          return
        }
        try {
          const current = await api<StaffUser>('/api/v1/auth/me')
          setUser(current)
          setAuthScreen('ready')
        } catch {
          setAuthScreen('login')
        }
      })
      .catch(() => setAuthScreen('login'))
  }, [])

  if (authScreen === 'install') {
    return (
      <InstallPage
        onInstalled={(current, appName) => {
          setMeta(value => (value ? { ...value, name: appName, installed: true } : value))
          setUser(current)
          setAuthScreen('ready')
        }}
      />
    )
  }

  if (authScreen === 'loading') return <SessionLoading portal="admin" />

  if (authScreen !== 'ready' || !user) {
    return (
      <AuthPage
        appName={meta?.name ?? 'VPSBill'}
        mode={authScreen}
        onAuthenticated={current => {
          setUser(current)
          setAuthScreen('ready')
        }}
      />
    )
  }

  return (
    <AdminShell
      meta={meta}
      user={user}
      onLogout={async () => {
        await api<void>('/api/v1/auth/logout', { method: 'POST' })
        setUser(null)
        setAuthScreen('login')
      }}
    />
  )
}

function AuthPage({
  appName,
  mode,
  onAuthenticated,
}: {
  appName: string
  mode: AuthScreen
  onAuthenticated: (user: StaffUser) => void
}) {
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      const current = await api<StaffUser>('/api/v1/auth/login', {
        method: 'POST',
        body: JSON.stringify({ email, password, totp_code: totpCode }),
      })
      onAuthenticated(current)
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '登录失败')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <section className="auth-brand-panel">
        <div className="brand auth-brand">
          <div className="brand-mark">VB</div>
          <div>
            <strong>{appName}</strong>
            <span>VPS 商业运营控制平面</span>
          </div>
        </div>
        <div>
          <p className="eyebrow">SECURE INFRASTRUCTURE</p>
          <h1>账务、客户与自动化虚拟化，在一个可信边界内运行。</h1>
          <p>管理员会话保护、细粒度权限控制与节点密钥全链路 AES-256-GCM 加密已经启用。</p>
        </div>
        <div className="auth-proof">
          <ShieldCheck size={18} />
          <span>Argon2id · CSRF 防护 · AES-256-GCM 凭据加密</span>
        </div>
      </section>
      <section className="auth-form-panel">
        <form className="auth-form" onSubmit={submit}>
          <p className="eyebrow">ADMIN ACCESS</p>
          <h2>登录商家控制中心</h2>
          <p>请输入管理员账号凭证以继续管理系统。</p>
          <Field label="管理员邮箱" value={email} onChange={setEmail} type="email" autoComplete="email" />
          <Field label="登录密码" value={password} onChange={setPassword} type="password" autoComplete="current-password" />
          <Field
            label="二步验证码（已启用时填写）"
            value={totpCode}
            onChange={setTotpCode}
            autoComplete="one-time-code"
            required={false}
          />
          {error && <div className="form-error" role="alert">{error}</div>}
          <button className="primary-button" style={{ width: '100%', marginTop: '6px' }} disabled={submitting || mode === 'loading'}>
            {mode === 'loading' ? '检查系统状态…' : submitting ? '正在验证…' : '登录控制中心'}
          </button>
          <a className="auth-switch" href="/portal">
            <Users size={15} />切换至客户中心
          </a>
        </form>
      </section>
    </main>
  )
}

type InstallResponse = { user: StaffUser; generated_secrets: Record<string, string> }

function InstallPage({ onInstalled }: { onInstalled: (user: StaffUser, appName: string) => void }) {
  const [form, setForm] = useState({
    app_name: 'VPSBill',
    public_url: window.location.origin,
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai',
    notification_webhook_url: '',
    notification_webhook_secret: '',
    metrics_token: '',
    worker_poll_interval: '3s',
    reconcile_interval: '5m',
    lifecycle_interval: '1m',
    renewal_lead_time: '168h',
    overdue_grace_period: '72h',
    termination_retention: '168h',
    admin_display_name: '',
    admin_email: '',
    admin_password: '',
  })
  const [result, setResult] = useState<InstallResponse | null>(null)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const update = (key: keyof typeof form) => (value: string) =>
    setForm(current => ({ ...current, [key]: value }))

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    try {
      setResult(await api<InstallResponse>('/api/v1/install', { method: 'POST', body: JSON.stringify(form) }))
    } catch (requestError) {
      setError(requestError instanceof Error ? requestError.message : '安装失败')
    } finally {
      setSubmitting(false)
    }
  }

  if (result) {
    return (
      <main className="installer-page">
        <section className="installer-card installer-complete">
          <div className="brand">
            <div className="brand-mark">VB</div>
            <div>
              <strong>{form.app_name}</strong>
              <span>首次初始化已完成</span>
            </div>
          </div>
          <CheckCircle2 size={48} />
          <h1>系统初始化成功</h1>
          <p>数据库结构、基础参数与超级管理员已原子写入。以下自动生成的安全密钥仅显示一次，请妥善保存至密码管理器。</p>
          {Object.entries(result.generated_secrets).length > 0 && (
            <div className="generated-secrets">
              {Object.entries(result.generated_secrets).map(([key, value]) => (
                <label key={key}>
                  <span>{key}</span>
                  <code>{value}</code>
                </label>
              ))}
            </div>
          )}
          <button className="primary-button" style={{ margin: '0 auto' }} onClick={() => onInstalled(result.user, form.app_name)}>
            进入商家控制中心
          </button>
        </section>
      </main>
    )
  }

  return (
    <main className="installer-page">
      <form className="installer-card" onSubmit={submit}>
        <header className="installer-header">
          <div>
            <p className="eyebrow">FIRST-RUN INITIALIZATION</p>
            <h1>初始化 VPSBill</h1>
            <p>系统基础环境与数据表已就绪。在此配置站点基础参数与初始超级管理员账号。</p>
          </div>
          <div className="installer-step">
            原子写入
            <br />
            <strong>事务安装向导</strong>
          </div>
        </header>

        <section className="installer-section">
          <h3>1. 站点基础设置</h3>
          <div className="installer-grid">
            <Field label="站点名称" value={form.app_name} onChange={update('app_name')} />
            <Field label="公开访问域名" value={form.public_url} onChange={update('public_url')} type="url" hint="用于外部支付与回调" />
            <Field label="系统默认时区" value={form.timezone} onChange={update('timezone')} />
          </div>
        </section>

        <section className="installer-section">
          <h3>2. 事件通知与指标监控</h3>
          <div className="installer-grid">
            <Field label="通知 Webhook 地址（可选）" value={form.notification_webhook_url} onChange={update('notification_webhook_url')} type="url" required={false} />
            <Field label="通知签名密钥（留空自动生成）" value={form.notification_webhook_secret} onChange={update('notification_webhook_secret')} type="password" required={false} />
            <Field label="Prometheus Token（留空自动生成）" value={form.metrics_token} onChange={update('metrics_token')} type="password" required={false} />
          </div>
        </section>

        <details className="installer-section">
          <summary>3. 自动化调度与账期参数（默认已针对生产调优）</summary>
          <div className="installer-grid advanced-grid">
            <Field label="任务队列轮询" value={form.worker_poll_interval} onChange={update('worker_poll_interval')} />
            <Field label="节点状态对账" value={form.reconcile_interval} onChange={update('reconcile_interval')} />
            <Field label="账务生命周期扫描" value={form.lifecycle_interval} onChange={update('lifecycle_interval')} />
            <Field label="提前续费账单生成" value={form.renewal_lead_time} onChange={update('renewal_lead_time')} />
            <Field label="逾期关机宽限期" value={form.overdue_grace_period} onChange={update('overdue_grace_period')} />
            <Field label="保留数据终止期" value={form.termination_retention} onChange={update('termination_retention')} />
          </div>
        </details>

        <section className="installer-section">
          <h3>4. 初始超级管理员账号</h3>
          <div className="installer-grid">
            <Field label="管理员姓名" value={form.admin_display_name} onChange={update('admin_display_name')} autoComplete="name" />
            <Field label="管理员邮箱" value={form.admin_email} onChange={update('admin_email')} type="email" autoComplete="email" />
            <Field label="管理员密码" value={form.admin_password} onChange={update('admin_password')} type="password" autoComplete="new-password" hint="至少 12 个字符" />
          </div>
        </section>

        {error && <div className="form-error" role="alert">{error}</div>}

        <footer className="installer-footer">
          <span>
            <ShieldCheck size={16} />敏感配置将使用 AES-256-GCM 硬件加密持久化
          </span>
          <button className="primary-button compact" disabled={submitting}>
            {submitting ? '正在初始化系统…' : '完成初始化安装'}
          </button>
        </footer>
      </form>
    </main>
  )
}

function Field({
  label,
  value,
  onChange,
  type = 'text',
  autoComplete,
  hint,
  required = true,
}: {
  label: string
  value: string
  onChange: (value: string) => void
  type?: string
  autoComplete?: string
  hint?: string
  required?: boolean
}) {
  return (
    <label className="field">
      <span>
        {label}
        {hint && <small>{hint}</small>}
      </span>
      <input
        required={required}
        value={value}
        type={type}
        autoComplete={autoComplete}
        onChange={event => onChange(event.target.value)}
      />
    </label>
  )
}

type CustomerAuthScreen = 'loading' | 'login' | 'register' | 'forgot' | 'reset' | 'ready'
type PortalView = 'overview' | 'shop' | 'services' | 'billing' | 'support' | 'profile'
const portalViews: PortalView[] = ['overview', 'shop', 'services', 'billing', 'support', 'profile']

function portalViewFromPath(): PortalView {
  const candidate = window.location.pathname.split('/').filter(Boolean)[1] as PortalView
  return portalViews.includes(candidate) ? candidate : 'overview'
}

function CustomerPortalApp() {
  const [screen, setScreen] = useState<CustomerAuthScreen>('loading')
  const [customer, setCustomer] = useState<CustomerIdentity | null>(null)
  const [meta, setMeta] = useState<Meta | null>(null)

  useEffect(() => {
    api<Meta>('/api/v1/meta').then(setMeta).catch(() => undefined)
    api<{ required: boolean }>('/api/v1/install')
      .then(installation => {
        if (installation.required) {
          window.location.replace('/admin')
          return
        }
        // Reset links open the reset form even when a session exists.
        if (window.location.pathname === '/portal/reset-password') {
          setScreen('reset')
          return
        }
        return api<CustomerIdentity>('/api/v1/customer/auth/me')
          .then(value => {
            setCustomer(value)
            setScreen('ready')
          })
          .catch(() => setScreen('login'))
      })
      .catch(() => setScreen('login'))
  }, [])

  if (screen === 'loading') return <SessionLoading portal="customer" />
  if (screen !== 'ready' || !customer) {
    return (
      <CustomerAuthPage
        mode={screen}
        meta={meta}
        onMode={setScreen}
        onAuthenticated={value => {
          setCustomer(value)
          setScreen('ready')
        }}
      />
    )
  }

  return (
    <CustomerShell
      customer={customer}
      onLogout={async () => {
        await api('/api/v1/customer/auth/logout', { method: 'POST' })
        setCustomer(null)
        setScreen('login')
      }}
    />
  )
}

function CustomerAuthPage({
  mode,
  meta,
  onMode,
  onAuthenticated,
}: {
  mode: CustomerAuthScreen
  meta: Meta | null
  onMode: (mode: CustomerAuthScreen) => void
  onAuthenticated: (customer: CustomerIdentity) => void
}) {
  const register = mode === 'register'
  const forgot = mode === 'forgot'
  const reset = mode === 'reset'
  const [displayName, setDisplayName] = useState('')
  const [email, setEmail] = useState('')
  const [password, setPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [totpCode, setTotpCode] = useState('')
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const siteName = meta?.name || 'VPSBill'

  function switchMode(next: CustomerAuthScreen) {
    setError('')
    setNotice('')
    setPassword('')
    setConfirmPassword('')
    if (window.location.pathname === '/portal/reset-password') window.history.replaceState(null, '', '/')
    onMode(next)
  }

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSubmitting(true)
    setError('')
    setNotice('')
    try {
      if (forgot) {
        const result = await api<{ message: string }>('/api/v1/customer/auth/password-reset', {
          method: 'POST',
          body: JSON.stringify({ email }),
        })
        setNotice(result.message)
        return
      }
      if (reset) {
        if (password !== confirmPassword) {
          setError('两次输入的新密码不一致')
          return
        }
        const token = new URLSearchParams(window.location.search).get('token') ?? ''
        await api('/api/v1/customer/auth/password-reset/confirm', {
          method: 'POST',
          body: JSON.stringify({ token, password }),
        })
        switchMode('login')
        setNotice('密码已重置，请使用新密码登录。')
        return
      }
      const body = register ? { display_name: displayName, email, password } : { email, password, totp_code: totpCode }
      const current = await api<CustomerIdentity>(
        register ? '/api/v1/customer/auth/register' : '/api/v1/customer/auth/login',
        { method: 'POST', body: JSON.stringify(body) }
      )
      onAuthenticated(current)
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setSubmitting(false)
    }
  }

  const title = register ? '创建客户账户' : forgot ? '找回登录密码' : reset ? '设置新密码' : '登录客户中心'
  const subtitle = register
    ? '注册后会自动开通个人财务账本。'
    : forgot
      ? meta?.password_reset_mail === false
        ? '本站暂未开通邮件找回，请通过客服或工单联系商家为你生成重置链接。'
        : '输入注册邮箱，我们会发送一封包含重置链接的邮件，30 分钟内有效。'
      : reset
        ? '新密码至少 12 个字符。设置后，所有已登录的设备都会退出。'
        : '管理你的云资源与服务账单。'
  const submitLabel = register ? '完成注册并登录' : forgot ? '发送重置邮件' : reset ? '保存新密码' : '登录客户中心'

  return (
    <main className="auth-page customer-auth-page">
      <section className="auth-brand-panel customer-brand-panel">
        <div className="brand auth-brand">
          <div className="brand-mark">VB</div>
          <div>
            <strong>{siteName}</strong>
            <span>客户服务中心</span>
          </div>
        </div>
        <div>
          <p className="eyebrow">YOUR CLOUD, UNDER CONTROL</p>
          <h1>随心挑选、配置与管理你的 VPS 实例。</h1>
          <p>实时运行监控、端口映射规则、密码管理与财务账单集中在统一入口。</p>
        </div>
        <div className="auth-proof">
          <ShieldCheck size={18} />
          <span>多租户严格隔离 · 服务端会话 · 全链路审计</span>
        </div>
      </section>
      <section className="auth-form-panel">
        <form className="auth-form" onSubmit={submit}>
          <p className="eyebrow">CUSTOMER PORTAL</p>
          <h2>{title}</h2>
          <p>{subtitle}</p>
          {register && <Field label="姓名 / 昵称" value={displayName} onChange={setDisplayName} autoComplete="name" />}
          {!reset && !(forgot && meta?.password_reset_mail === false) && (
            <Field label="登录邮箱" value={email} onChange={setEmail} type="email" autoComplete="email" />
          )}
          {!forgot && (
            <Field
              label={reset ? '新密码' : '登录密码'}
              value={password}
              onChange={setPassword}
              type="password"
              autoComplete={register || reset ? 'new-password' : 'current-password'}
              hint={register || reset ? '至少 12 个字符' : undefined}
            />
          )}
          {reset && (
            <Field label="再次输入新密码" value={confirmPassword} onChange={setConfirmPassword} type="password" autoComplete="new-password" />
          )}
          {mode === 'login' && (
            <Field
              label="二步验证码（启用后填写）"
              value={totpCode}
              onChange={setTotpCode}
              autoComplete="one-time-code"
              required={false}
            />
          )}
          {error && <div className="form-error" role="alert">{error}</div>}
          {notice && <div className="form-success" role="status">{notice}</div>}
          {!(forgot && meta?.password_reset_mail === false) && (
            <button className="primary-button" style={{ width: '100%', marginTop: '6px' }} disabled={submitting || mode === 'loading'}>
              {mode === 'loading' ? '正在连接…' : submitting ? '正在提交…' : submitLabel}
            </button>
          )}
          <div className="auth-links">
            <button
              type="button"
              className="auth-switch button-link"
              onClick={() => switchMode(mode === 'login' ? 'register' : 'login')}
            >
              {mode === 'login' ? '还没有账号？立即注册' : register ? '已有账号？返回登录' : '返回登录'}
            </button>
            {mode === 'login' && (
              <button type="button" className="auth-switch button-link" onClick={() => switchMode('forgot')}>
                忘记密码？
              </button>
            )}
          </div>
        </form>
      </section>
    </main>
  )
}

function CustomerShell({ customer, onLogout }: { customer: CustomerIdentity; onLogout: () => void }) {
  const [view, setView] = useState<PortalView>(portalViewFromPath)

  useEffect(() => {
    if (window.location.pathname !== `/portal/${view}`) {
      window.history.replaceState(null, '', `/portal/${view}`)
    }
    const pop = () => setView(portalViewFromPath())
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [view])

  const navigate = (next: PortalView) => {
    if (next === view) return
    window.history.pushState(null, '', `/portal/${next}`)
    setView(next)
  }

  const items: [PortalView, string, typeof LayoutDashboard][] = [
    ['overview', '服务概览', LayoutDashboard],
    ['shop', '选购 VPS', ShoppingCart],
    ['services', '我的 VPS', Boxes],
    ['billing', '订单与账单', WalletCards],
    ['support', '支持工单', Headphones],
    ['profile', '账户资料', UserCircle],
  ]

  const titles: Record<PortalView, string> = {
    overview: '服务概览',
    shop: '选购 VPS',
    services: '我的 VPS',
    billing: '订单与账单',
    support: '支持工单',
    profile: '账户资料',
  }

  return (
    <div className="app-shell customer-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">VB</div>
          <div>
            <strong>客户中心</strong>
            <span>Customer Portal</span>
          </div>
        </div>
        <nav aria-label="客户导航">
          {items.map(([id, label, Icon]) => (
            <button
              key={id}
              className={id === view ? 'nav-item active' : 'nav-item'}
              onClick={() => navigate(id)}
            >
              <Icon size={18} />
              <span>{label}</span>
              {id === view && <ChevronRight size={16} />}
            </button>
          ))}
        </nav>
        <div className="sidebar-status">
          <span className="status-dot online" />
          <div>
            <strong>{customer.display_name}</strong>
            <span>账户正常</span>
          </div>
        </div>
      </aside>
      <main>
        <header className="topbar">
          <div>
            <p className="eyebrow">CUSTOMER PORTAL</p>
            <h1>{titles[view]}</h1>
          </div>
          <div className="operator">
            <span>{customer.email}</span>
            <div className="avatar">{customer.display_name.slice(0, 1)}</div>
            <button className="icon-button" aria-label="退出登录" onClick={onLogout} title="退出登录">
              <LogOut size={16} />
            </button>
          </div>
        </header>

        {view === 'overview' && <CustomerOverview customer={customer} />}
        {view === 'shop' && <CustomerShop customer={customer} />}
        {view === 'services' && <CustomerServicesPanel />}
        {view === 'billing' && <CustomerBilling />}
        {view === 'support' && <CustomerSupport />}
        {view === 'profile' && <CustomerProfile customer={customer} />}
      </main>
    </div>
  )
}

function CustomerOverview({ customer }: { customer: CustomerIdentity }) {
  const [services, setServices] = useState<CustomerServiceRecord[]>([])
  const [invoices, setInvoices] = useState<CustomerInvoiceRecord[]>([])

  useEffect(() => {
    void Promise.all([
      api<CustomerServiceRecord[]>('/api/v1/customer/services'),
      api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices'),
    ]).then(([s, i]) => {
      // Terminated services stay listed on the services page but are not counted here.
      setServices(s.filter(item => item.status !== 'terminated'))
      setInvoices(i)
    })
  }, [])

  const online = services.filter(item => item.runtime_status === 'running').length
  const due = invoices.filter(item => item.status === 'open').reduce((sum, item) => sum + item.balance_minor, 0)

  return (
    <section className="workspace-panel">
      <section className="hero-card customer-hero">
        <div>
          <p className="eyebrow">WELCOME BACK</p>
          <h2>{customer.display_name}，欢迎回到云服务中心。</h2>
          <p>集中查看所有实例运行状态、快速执行电源和管理操作，并实时跟踪账单结算。</p>
        </div>
        <div className="hero-signal">
          <span>{online}/{services.length}</span>
          <small>在线实例</small>
        </div>
      </section>

      <section className="metrics">
        <article>
          <Boxes size={20} />
          <span>VPS 总数</span>
          <strong>{services.length}</strong>
        </article>
        <article>
          <Activity size={20} />
          <span>正在运行</span>
          <strong>{online}</strong>
        </article>
        <article>
          <ReceiptText size={20} />
          <span>待支付账单</span>
          <strong>{invoices.filter(item => item.status === 'open').length}</strong>
        </article>
        <article>
          <CircleDollarSign size={20} />
          <span>待付总金额</span>
          <strong>{money(due, invoices[0]?.currency || 'CNY')}</strong>
        </article>
      </section>
    </section>
  )
}

function CustomerShop({ customer }: { customer: CustomerIdentity }) {
  const [catalog, setCatalog] = useState<CustomerCatalogRecord | null>(null)
  const [selectedID, setSelectedID] = useState('')
  const [cycle, setCycle] = useState('monthly')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [created, setCreated] = useState<OrderRecord | null>(null)
  const [paying, setPaying] = useState(false)

  useEffect(() => {
    api<CustomerCatalogRecord>('/api/v1/customer/catalog')
      .then(value => {
        setCatalog(value)
        const first = value.plans.find(plan => plan.prices.some(price => price.currency === customer.default_currency))
        if (first) {
          setSelectedID(first.id)
          const price = first.prices.find(item => item.currency === customer.default_currency)
          if (price) setCycle(price.billing_cycle)
        }
      })
      .catch(err => setError(err.message))
  }, [customer.default_currency])

  const plans = (catalog?.plans || []).filter(plan =>
    plan.prices.some(price => price.currency === customer.default_currency)
  )
  const selected = plans.find(plan => plan.id === selectedID)
  const prices = selected?.prices.filter(price => price.currency === customer.default_currency) || []

  useEffect(() => {
    if (prices.length && !prices.some(price => price.billing_cycle === cycle)) {
      setCycle(prices[0].billing_cycle)
    }
  }, [selectedID])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!selected) return
    const data = new FormData(event.currentTarget)
    setSaving(true)
    setError('')
    setCreated(null)
    try {
      const order = await api<OrderRecord>('/api/v1/customer/orders', {
        method: 'POST',
        body: JSON.stringify({
          items: [
            {
              plan_id: selected.id,
              region_id: data.get('region_id'),
              billing_cycle: data.get('billing_cycle'),
              quantity: Number(data.get('quantity')),
              configuration: { template_id: data.get('template_id') },
            },
          ],
        }),
      })
      setCreated(order)
    } catch (err) {
      setError(err instanceof Error ? err.message : '下单失败')
    } finally {
      setSaving(false)
    }
  }

  async function checkout() {
    if (!created) return
    setPaying(true)
    setError('')
    try {
      const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${created.invoice_id}/checkout`, {
        method: 'POST',
      })
      window.location.assign(intent.checkout_url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建收银台失败')
      setPaying(false)
    }
  }

  const currentPrice = prices.find(price => price.billing_cycle === cycle)

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">MARKETPLACE</p>
          <h2>选购 VPS 套餐</h2>
          <p>配置与定价由服务端实时校验；支付完成后将自动触发集群调度并开通服务。</p>
        </div>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="shop-grid">
        {plans.map(plan => {
          const price = plan.prices.find(item => item.currency === customer.default_currency)
          return (
            <button
              type="button"
              key={plan.id}
              className={selectedID === plan.id ? 'shop-plan selected' : 'shop-plan'}
              onClick={() => setSelectedID(plan.id)}
            >
              <div>
                <span className="tag">{plan.virtualization.toUpperCase()}</span>
                {selectedID === plan.id && <span className="selected-mark">已选定</span>}
              </div>
              <h3>{plan.name}</h3>
              <small>{plan.code}</small>
              <div className="shop-specs">
                <span>{plan.vcpu} vCPU</span>
                <span>{plan.ram_mb} MB 内存</span>
                <span>{plan.disk_gb} GB SSD</span>
                <span>{plan.traffic_gb} GB 流量</span>
              </div>
              <div className="shop-price">
                {price ? money(price.amount_minor, price.currency) : '暂无报价'}
                <small>/ {price ? cycleLabel(price.billing_cycle) : ''}</small>
              </div>
            </button>
          )
        })}
        {!plans.length && <div className="empty-card" style={{ gridColumn: '1 / -1' }}>当前币种暂无可售套餐</div>}
      </div>

      {selected && (
        <form className="checkout-config panel" onSubmit={submit}>
          <div className="panel-heading">
            <div>
              <p className="eyebrow">ORDER CONFIGURATION</p>
              <h3>配置实例选项：{selected.name}</h3>
            </div>
            <span className="tag">SERVER PRICED</span>
          </div>
          <div className="form-grid">
            <label>
              <span>部署地域</span>
              <select name="region_id" required>
                {catalog?.regions.map(region => (
                  <option key={region.id} value={region.id}>
                    {region.name}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>计费周期</span>
              <select name="billing_cycle" value={cycle} onChange={event => setCycle(event.target.value)}>
                {prices.map(price => (
                  <option key={price.billing_cycle} value={price.billing_cycle}>
                    {cycleLabel(price.billing_cycle)} 付款 · {money(price.amount_minor, price.currency)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              <span>购买数量</span>
              <input name="quantity" type="number" min="1" max="20" defaultValue="1" />
            </label>
            <label>
              <span>操作系统镜像</span>
              <select key={selected.id} name="template_id" defaultValue={selected.default_template_id}>
                {selected.allowed_template_ids.map(template => (
                  <option key={template} value={template}>
                    {template}
                  </option>
                ))}
              </select>
            </label>
            <div className="order-total">
              <span>应付金额{currentPrice?.setup_fee_minor ? '（含开通费）' : ''}</span>
              <strong>
                {money(
                  (currentPrice?.amount_minor || 0) + (currentPrice?.setup_fee_minor || 0),
                  customer.default_currency
                )}
              </strong>
            </div>
            <div className="form-actions wide">
              <button className="primary-button compact" disabled={saving || !catalog?.regions.length}>
                {saving ? '正在生成订单…' : '立即下单'}
              </button>
            </div>
          </div>
        </form>
      )}

      {created && (
        <div className="checkout-success">
          <div>
            <strong>订单 {created.number} 已生成</strong>
            <span>应付总额 {money(created.total_minor, created.currency)}，关联账单 {created.invoice_number}</span>
          </div>
          {catalog?.checkout_enabled ? (
            <button className="primary-button compact" disabled={paying} onClick={checkout}>
              {paying ? '正在前往收银台…' : '前往在线支付'}
            </button>
          ) : (
            <span>当前未配置在线支付渠道，请联系商家后台完成入账。</span>
          )}
        </div>
      )}
    </section>
  )
}

function CustomerBilling() {
  const [invoices, setInvoices] = useState<CustomerInvoiceRecord[]>([])
  const [transactions, setTransactions] = useState<CustomerTransactionRecord[]>([])
  const [orders, setOrders] = useState<OrderRecord[]>([])
  const [checkoutEnabled, setCheckoutEnabled] = useState(false)
  const [paying, setPaying] = useState('')
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [i, t, o, c] = await Promise.all([
        api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices'),
        api<CustomerTransactionRecord[]>('/api/v1/customer/transactions'),
        api<OrderRecord[]>('/api/v1/customer/orders'),
        api<CustomerCatalogRecord>('/api/v1/customer/catalog'),
      ])
      setInvoices(i)
      setTransactions(t)
      setOrders(o)
      setCheckoutEnabled(c.checkout_enabled)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function checkout(invoice: CustomerInvoiceRecord) {
    setPaying(invoice.id)
    setError('')
    try {
      const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${invoice.id}/checkout`, {
        method: 'POST',
      })
      window.location.assign(intent.checkout_url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建收银台失败')
      setPaying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">BILLING & LEDGER</p>
          <h2>订单、账单与付款明细</h2>
          <p>所有交易采用不可变账本设计；网关验签完成到账后系统自动调度开通。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="panel">
        <div className="panel-heading">
          <h3>订单记录</h3>
          <span className="tag">{orders.length} 笔订单</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>订单编号</th>
                <th>关联账单</th>
                <th>订单金额</th>
                <th>订单状态</th>
                <th>创建时间</th>
              </tr>
            </thead>
            <tbody>
              {orders.map(item => (
                <tr key={item.id}>
                  <td><strong>{item.number}</strong></td>
                  <td><code>{item.invoice_number}</code></td>
                  <td><strong>{money(item.total_minor, item.currency)}</strong></td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{new Date(item.created_at).toLocaleString()}</td>
                </tr>
              ))}
              {!orders.length && (
                <tr>
                  <td colSpan={5} className="empty-state">暂无订单记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>账单记录</h3>
          <span className="tag">{invoices.length} 张账单</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>账单号</th>
                <th>应付总额</th>
                <th>待结余额</th>
                <th>状态</th>
                <th>到期时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map(item => (
                <tr key={item.id}>
                  <td><strong>{item.number}</strong></td>
                  <td><strong>{money(item.total_minor, item.currency)}</strong></td>
                  <td style={{ color: item.balance_minor > 0 ? '#fbbf24' : 'inherit' }}>
                    <strong>{money(item.balance_minor, item.currency)}</strong>
                  </td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{new Date(item.due_at).toLocaleDateString()}</td>
                  <td>
                    {item.status === 'open' &&
                      (checkoutEnabled ? (
                        <button
                          className="primary-button compact"
                          disabled={paying === item.id}
                          onClick={() => checkout(item)}
                        >
                          {paying === item.id ? '跳转中…' : '立即支付'}
                        </button>
                      ) : (
                        <span style={{ color: 'var(--text-muted)', fontSize: '12px' }}>请联系商家线下结算</span>
                      ))}
                  </td>
                </tr>
              ))}
              {!invoices.length && (
                <tr>
                  <td colSpan={6} className="empty-state">暂无账单记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>付款交易流水</h3>
          <span className="tag">IMMUTABLE LEDGER</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>流水参考号</th>
                <th>关联账单</th>
                <th>支付渠道</th>
                <th>实付金额</th>
                <th>流水状态</th>
                <th>入账时间</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map(item => (
                <tr key={item.id}>
                  <td><strong>{item.provider_transaction_id || item.id}</strong></td>
                  <td>{item.invoice_number || '—'}</td>
                  <td><span className="tag">{item.provider.toUpperCase()}</span></td>
                  <td><strong>{money(item.amount_minor, item.currency)}</strong></td>
                  <td><StatusBadge status={item.status} /></td>
                  <td>{new Date(item.created_at).toLocaleString()}</td>
                </tr>
              ))}
              {!transactions.length && (
                <tr>
                  <td colSpan={6} className="empty-state">暂无付款流水记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}

function CustomerProfile({ customer }: { customer: CustomerIdentity }) {
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">ACCOUNT</p>
          <h2>账户资料与安全</h2>
          <p>管理个人账户身份、登录密码及 TOTP 二步验证设置。</p>
        </div>
      </div>

      <div className="panel profile-panel">
        <div>
          <span>姓名 / 昵称</span>
          <strong>{customer.display_name}</strong>
        </div>
        <div>
          <span>登录邮箱</span>
          <strong>{customer.email}</strong>
        </div>
        <div>
          <span>账户角色</span>
          <strong>{customer.role === 'owner' ? '所有者' : customer.role}</strong>
        </div>
        <div>
          <span>账户状态</span>
          <StatusBadge status={customer.account_status} />
        </div>
        <div>
          <span>账户 ID</span>
          <code>{customer.account_id}</code>
        </div>
        <div>
          <span>默认计费币种</span>
          <code>{customer.default_currency}</code>
        </div>
      </div>

      <SecuritySettings enabled={customer.mfa_enabled} customer />
    </section>
  )
}

function SecuritySettings({ enabled: initialEnabled, customer = false }: { enabled: boolean; customer?: boolean }) {
  const [enabled, setEnabled] = useState(initialEnabled)
  const [setup, setSetup] = useState<{ secret: string; otpauth_uri: string } | null>(null)
  const [code, setCode] = useState('')
  const [error, setError] = useState('')
  const base = customer ? '/api/v1/customer/auth/mfa' : '/api/v1/auth/mfa'

  async function start() {
    try {
      setSetup(await api<{ secret: string; otpauth_uri: string }>(`${base}/setup`, { method: 'POST' }))
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '设置失败')
    }
  }

  async function confirm() {
    try {
      await api(`${base}/confirm`, { method: 'POST', body: JSON.stringify({ code }) })
      setEnabled(true)
      setSetup(null)
      setCode('')
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '验证码无效')
    }
  }

  async function disable() {
    try {
      await api(`${base}/disable`, { method: 'POST', body: JSON.stringify({ code }) })
      setEnabled(false)
      setCode('')
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '验证码无效')
    }
  }

  return (
    <>
    <PasswordSettings customer={customer} />
    <div className="panel security-panel">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">TWO-FACTOR AUTHENTICATION</p>
          <h3>二步验证（TOTP）</h3>
        </div>
        <span className={`status-badge ${enabled ? 'online' : 'disabled'}`}>
          {enabled ? '已启用防护' : '未启用'}
        </span>
      </div>

      {error && <div className="form-error">{error}</div>}

      {!enabled && !setup && (
        <>
          <p>
            启用二步验证后，在每次登录时除输入密码外，还需输入验证器应用生成的 6 位动态验证码，有效保护您的云资产与账单安全。
          </p>
          <button className="primary-button compact" onClick={() => void start()}>
            <ShieldCheck size={16} />开始配置二步验证
          </button>
        </>
      )}

      {setup && (
        <>
          <p>请在 Authenticator 验证器中手动添加以下密钥，或直接点击配置链接：</p>
          <code className="mfa-secret">{setup.secret}</code>
          <a className="secondary-button mfa-link" href={setup.otpauth_uri}>
            在系统默认验证器中打开
          </a>
          <label className="field" style={{ maxWidth: '320px', marginTop: '16px' }}>
            <span>输入 6 位动态验证码确认绑定</span>
            <input
              value={code}
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              onChange={event => setCode(event.target.value)}
            />
          </label>
          <button className="primary-button compact" disabled={code.length !== 6} onClick={() => void confirm()}>
            确认并启用
          </button>
        </>
      )}

      {enabled && (
        <>
          <p>二步验证已在当前账号生效。如需停用，请先输入验证器中显示的 6 位验证码以确认身份。</p>
          <label className="field" style={{ maxWidth: '320px' }}>
            <span>当前 6 位验证码</span>
            <input
              value={code}
              inputMode="numeric"
              maxLength={6}
              placeholder="000000"
              onChange={event => setCode(event.target.value)}
            />
          </label>
          <button className="secondary-button" disabled={code.length !== 6} onClick={() => void disable()}>
            关闭二步验证
          </button>
        </>
      )}
    </div>
    </>
  )
}

function PasswordSettings({ customer }: { customer: boolean }) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [repeat, setRepeat] = useState('')
  const [message, setMessage] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent) {
    event.preventDefault()
    setMessage('')
    if (next !== repeat) {
      setError('两次输入的新密码不一致')
      return
    }
    setSaving(true)
    setError('')
    try {
      await api(customer ? '/api/v1/customer/auth/password' : '/api/v1/auth/password', {
        method: 'POST',
        body: JSON.stringify({ current_password: current, new_password: next }),
      })
      setCurrent('')
      setNext('')
      setRepeat('')
      setMessage('密码已更新，其他设备上的登录已全部退出。')
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <form className="panel security-panel" onSubmit={submit}>
      <div className="panel-heading">
        <div>
          <p className="eyebrow">PASSWORD</p>
          <h3>修改登录密码</h3>
        </div>
      </div>
      {error && <div className="form-error">{error}</div>}
      {message && <div className="form-success">{message}</div>}
      <div className="password-fields">
        <Field label="当前密码" value={current} onChange={setCurrent} type="password" autoComplete="current-password" />
        <Field label="新密码" value={next} onChange={setNext} type="password" autoComplete="new-password" hint="至少 12 个字符" />
        <Field label="确认新密码" value={repeat} onChange={setRepeat} type="password" autoComplete="new-password" />
      </div>
      <button className="primary-button compact" disabled={saving || !current || !next || !repeat}>
        {saving ? '正在保存…' : '更新密码'}
      </button>
    </form>
  )
}

function CustomerSupport() {
  const [tickets, setTickets] = useState<TicketRecord[]>([])
  const [services, setServices] = useState<CustomerServiceRecord[]>([])
  const [detail, setDetail] = useState<TicketDetailRecord | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [ticketRows, serviceRows] = await Promise.all([
        api<TicketRecord[]>('/api/v1/customer/tickets'),
        api<CustomerServiceRecord[]>('/api/v1/customer/services'),
      ])
      setTickets(ticketRows)
      setServices(serviceRows)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function open(id: string) {
    try {
      setDetail(await api<TicketDetailRecord>(`/api/v1/customer/tickets/${id}`))
      setCreating(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    try {
      const result = await api<TicketDetailRecord>('/api/v1/customer/tickets', {
        method: 'POST',
        body: JSON.stringify({
          service_id: data.get('service_id'),
          subject: data.get('subject'),
          priority: data.get('priority'),
          body: data.get('body'),
        }),
      })
      setCreating(false)
      await load()
      // Re-read so the view has the joined customer and instance names.
      await open(result.ticket.id)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    }
  }

  async function reply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!detail) return
    const form = event.currentTarget
    const data = new FormData(form)
    try {
      await api(`/api/v1/customer/tickets/${detail.ticket.id}/messages`, {
        method: 'POST',
        body: JSON.stringify({ body: data.get('body') }),
      })
      form.reset()
      await open(detail.ticket.id)
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '回复失败')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">SUPPORT</p>
          <h2>支持工单</h2>
          <p>工单可与具体 VPS 实例关联，沟通历史与操作均由服务端留痕审计。</p>
        </div>
        <button
          className="primary-button compact"
          onClick={() => {
            setCreating(true)
            setDetail(null)
          }}
        >
          <Plus size={16} />新建工单
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      {creating && (
        <form className="panel form-grid" onSubmit={create}>
          <label>
            <span>工单主题</span>
            <input name="subject" minLength={3} maxLength={160} placeholder="请简述您遇到的问题" required />
          </label>
          <label>
            <span>优先级</span>
            <select name="priority" defaultValue="normal">
              <option value="low">低</option>
              <option value="normal">普通</option>
              <option value="high">高</option>
              <option value="urgent">紧急</option>
            </select>
          </label>
          <label>
            <span>关联 VPS 实例（可选）</span>
            <select name="service_id">
              <option value="">不关联具体实例</option>
              {services.map(service => (
                <option key={service.id} value={service.id}>
                  {service.instance_name}
                </option>
              ))}
            </select>
          </label>
          <label className="wide">
            <span>问题详述</span>
            <textarea name="body" rows={6} maxLength={10000} placeholder="请详细提供现象、报错信息或重现步骤…" required />
          </label>
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={() => setCreating(false)}>
              取消
            </button>
            <button className="primary-button compact">提交工单</button>
          </div>
        </form>
      )}

      <div className="support-layout">
        <div className="ticket-list">
          {tickets.map(ticket => (
            <button
              key={ticket.id}
              className={detail?.ticket.id === ticket.id ? 'ticket-row selected' : 'ticket-row'}
              onClick={() => void open(ticket.id)}
            >
              <div>
                <strong>{ticket.subject}</strong>
                <span>{ticket.number}</span>
              </div>
              <span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span>
              <small>
                {ticket.message_count} 条消息 · 最近更新 {new Date(ticket.last_reply_at).toLocaleString()}
              </small>
            </button>
          ))}
          {!tickets.length && <div className="empty-card">暂无支持工单记录</div>}
        </div>

        {detail ? (
          <TicketConversation detail={detail} onReply={reply} />
        ) : (
          <div className="panel support-placeholder">选择左侧工单查看完整沟通历史与回复</div>
        )}
      </div>
    </section>
  )
}

function TicketConversation({
  detail,
  onReply,
  admin = false,
  onStatus,
}: {
  detail: TicketDetailRecord
  onReply: (event: FormEvent<HTMLFormElement>) => void
  admin?: boolean
  onStatus?: (status: string) => void
}) {
  return (
    <div className="panel conversation">
      <div className="panel-heading">
        <div>
          <p className="eyebrow">{detail.ticket.number}</p>
          <h3>{detail.ticket.subject}</h3>
          <small>
            {[
              detail.ticket.customer_name && `客户：${detail.ticket.customer_name}`,
              detail.ticket.instance_name && `关联实例：${detail.ticket.instance_name}`,
            ].filter(Boolean).join(' · ') || `创建于 ${new Date(detail.ticket.created_at).toLocaleString()}`}
          </small>
        </div>
        <span className={`ticket-state ${detail.ticket.status}`}>{ticketStatusLabel(detail.ticket.status)}</span>
      </div>

      <div className="message-list">
        {detail.messages.map(message => (
          <article key={message.id} className={`message ${message.author_type}${message.internal ? ' internal' : ''}`}>
            <header>
              <strong>{message.author_name || ticketAuthorLabel(message.author_type)}</strong>
              <span>
                {message.internal ? '内部备忘 · ' : ''}
                {new Date(message.created_at).toLocaleString()}
              </span>
            </header>
            <p>{message.body}</p>
          </article>
        ))}
      </div>

      {detail.ticket.status !== 'closed' && (
        <form className="reply-form" onSubmit={onReply}>
          <textarea
            name="body"
            rows={4}
            maxLength={10000}
            placeholder={admin ? '回复客户，或勾选内部备忘记录后台信息…' : '请在此输入需要补充的信息…'}
            required
          />
          {admin && (
            <label className="checkbox">
              <input name="internal" type="checkbox" /> 仅客服内部可见
            </label>
          )}
          <button className="primary-button compact">
            <Send size={14} />发送回复
          </button>
        </form>
      )}

      {admin && (
        <div className="ticket-actions">
          <button className="secondary-button" onClick={() => onStatus?.('open')}>
            重新标记待处理
          </button>
          <button className="secondary-button" onClick={() => onStatus?.('resolved')}>
            标记已解决
          </button>
          <button className="secondary-button" onClick={() => onStatus?.('closed')}>
            关闭工单
          </button>
        </div>
      )}
    </div>
  )
}

function AdminSupport() {
  const [tickets, setTickets] = useState<TicketRecord[]>([])
  const [detail, setDetail] = useState<TicketDetailRecord | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<TicketRecord[]>('/api/v1/admin/tickets')
      .then(setTickets)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function open(id: string) {
    try {
      setDetail(await api<TicketDetailRecord>(`/api/v1/admin/tickets/${id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  async function reply(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!detail) return
    const form = event.currentTarget
    const data = new FormData(form)
    try {
      await api(`/api/v1/admin/tickets/${detail.ticket.id}/messages`, {
        method: 'POST',
        body: JSON.stringify({ body: data.get('body'), internal: data.get('internal') === 'on' }),
      })
      form.reset()
      await open(detail.ticket.id)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '回复失败')
    }
  }

  async function status(value: string) {
    if (!detail) return
    try {
      await api(`/api/v1/admin/tickets/${detail.ticket.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ status: value }),
      })
      await open(detail.ticket.id)
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新失败')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">SUPPORT DESK</p>
          <h2>工单管理</h2>
          <p>集中处理客户咨询、故障报障，支持添加团队内部备忘与工单流转。</p>
        </div>
        <button className="secondary-button" onClick={() => load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="support-layout">
        <div className="ticket-list">
          {tickets.map(ticket => (
            <button
              key={ticket.id}
              className={detail?.ticket.id === ticket.id ? 'ticket-row selected' : 'ticket-row'}
              onClick={() => void open(ticket.id)}
            >
              <div>
                <strong>{ticket.subject}</strong>
                <span>
                  {ticket.customer_name} · {ticket.number}
                </span>
              </div>
              <span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span>
              <small>
                优先级：{ticket.priority.toUpperCase()} · {ticket.message_count} 条消息
              </small>
            </button>
          ))}
          {!tickets.length && <div className="empty-card">当前无工单待处理</div>}
        </div>

        {detail ? (
          <TicketConversation detail={detail} onReply={reply} admin onStatus={status} />
        ) : (
          <div className="panel support-placeholder">选择左侧工单开始回复与流转</div>
        )}
      </div>
    </section>
  )
}

function AuditView() {
  const [rows, setRows] = useState<AuditLogRecord[]>([])
  const [error, setError] = useState('')

  const load = () =>
    api<AuditLogRecord[]>('/api/v1/admin/audit-logs')
      .then(setRows)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">AUDIT TRAIL</p>
          <h2>安全审计日志</h2>
          <p>核心与敏感操作采用服务端只追加（Append-Only）模型记录，展示最近 500 条操作。</p>
        </div>
        <button className="secondary-button" onClick={() => load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>记录时间</th>
              <th>操作者</th>
              <th>执行动作</th>
              <th>目标对象</th>
              <th>来源 IP</th>
              <th>元数据明细</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(row => (
              <tr key={row.id}>
                <td>{new Date(row.created_at).toLocaleString()}</td>
                <td>
                  <strong>{row.actor_type}</strong>
                  <small>{row.actor_id || '—'}</small>
                </td>
                <td>
                  <span className="tag">{row.action}</span>
                </td>
                <td>
                  <strong>{row.target_type}</strong>
                  <small>{row.target_id || '—'}</small>
                </td>
                <td>
                  <code>{row.ip || '—'}</code>
                </td>
                <td className="audit-metadata" title={JSON.stringify(row.metadata)}>
                  {JSON.stringify(row.metadata)}
                </td>
              </tr>
            ))}
            {!rows.length && (
              <tr>
                <td colSpan={6} className="empty-state">暂无审计日志记录</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function AdminShell({ meta, user, onLogout }: { meta: Meta | null; user: StaffUser; onLogout: () => void }) {
  const [route, setRoute] = useState<AdminRoute>(adminRouteFromPath)
  const view = route.view

  useEffect(() => {
    const initial = adminRouteFromPath()
    const canonical = adminRoutePath(initial)
    if (window.location.pathname !== canonical) {
      window.history.replaceState(null, '', canonical)
    }
    const pop = () => setRoute(adminRouteFromPath())
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [])

  const navigate = (next: AdminRoute) => {
    const path = adminRoutePath(next)
    if (window.location.pathname === path) return
    window.history.pushState(null, '', path)
    setRoute(next)
  }

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand">
          <div className="brand-mark">VB</div>
          <div>
            <strong>{meta?.name ?? 'VPSBill'}</strong>
            <span>商家控制中心</span>
          </div>
        </div>
        <nav aria-label="主导航">
          {navItems.map(({ id, label, icon: Icon }) => (
            <button
              className={id === view ? 'nav-item active' : 'nav-item'}
              key={label}
              type="button"
              onClick={() => navigate({ view: id })}
            >
              <Icon size={18} aria-hidden="true" />
              <span>{label}</span>
              {id === view && <ChevronRight size={16} aria-hidden="true" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-status">
          <span className="status-dot online" />
          <div>
            <strong>控制平面在线</strong>
            <span>{meta?.environment ?? 'production'}</span>
          </div>
        </div>
      </aside>

      <main>
        <header className="topbar">
          <div>
            <p className="eyebrow">ADMIN CONTROL PLANE</p>
            <h1>{route.hostID ? '宿主机探针详情' : viewTitle(view)}</h1>
          </div>
          <div className="operator">
            <span>{user.display_name}</span>
            <div className="avatar">{user.display_name.slice(0, 1)}</div>
            <button className="icon-button" aria-label="退出登录" onClick={onLogout} title="退出登录">
              <LogOut size={16} />
            </button>
          </div>
        </header>

        {view === 'overview' && <Overview />}
        {view === 'customers' && <CustomersView />}
        {view === 'orders' && <OrdersView />}
        {view === 'billing' && <BillingView />}
        {view === 'payment' && <PaymentSettingsView />}
        {view === 'settings' && <SiteSettingsView />}
        {view === 'services' && <ServicesView />}
        {view === 'nodes' && <NodesView />}
        {view === 'hosts' &&
          (route.hostID ? (
            <HostDetailPanel id={route.hostID} onBack={() => navigate({ view: 'hosts' })} />
          ) : (
            <HostsView onOpen={id => navigate({ view: 'hosts', hostID: id })} />
          ))}
        {view === 'plans' && <PlansView />}
        {view === 'support' && <AdminSupport />}
        {view === 'audit' && <AuditView />}
        {view === 'security' && <SecuritySettings enabled={user.mfa_enabled} />}
      </main>
    </div>
  )
}

function Overview() {
  const [data, setData] = useState<OperationsOverviewRecord | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<OperationsOverviewRecord>('/api/v1/admin/overview')
      .then(setData)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 30000)
    return () => window.clearInterval(timer)
  }, [])

  if (!data) {
    return (
      <section className="workspace-panel">
        {error ? <div className="form-error">{error}</div> : <div className="empty-card">正在加载运营统计数据…</div>}
      </section>
    )
  }

  const revenue =
    data.revenue_30_days.map(item => money(item.amount_minor, item.currency)).join(' / ') || money(0, 'CNY')
  const outstanding =
    data.outstanding.map(item => money(item.amount_minor, item.currency)).join(' / ') || money(0, 'CNY')
  const nodeRate = data.nodes ? Math.round((data.online_nodes / data.nodes) * 100) : 0

  return (
    <section className="workspace-panel">
      <section className="hero-card operations-hero">
        <div>
          <p className="eyebrow">30-DAY BUSINESS PERFORMANCE</p>
          <h2>{revenue} 实收金额到账</h2>
          <p>
            近 30 天已成交 {data.orders_30_days} 笔销售订单 · 当前保有 {data.accounts} 个有效客户 · 在管 {data.services} 台 VPS 实例
          </p>
        </div>
        <div className="hero-signal">
          <span>{nodeRate}%</span>
          <small>节点在线率</small>
        </div>
      </section>

      <section className="metrics">
        <article>
          <CircleDollarSign size={20} />
          <span>30 天入账收入</span>
          <strong>{revenue}</strong>
        </article>
        <article>
          <ReceiptText size={20} />
          <span>待收金额</span>
          <strong>{outstanding}</strong>
          <small>{data.open_invoices} 张未付账单</small>
        </article>
        <article>
          <Activity size={20} />
          <span>运行中 VPS</span>
          <strong>
            {data.running_services} / {data.services}
          </strong>
        </article>
        <article>
          <Headphones size={20} />
          <span>待处理工单</span>
          <strong>{data.open_tickets}</strong>
        </article>
      </section>

      <div className="content-grid">
        <section className="panel">
          <div className="panel-heading">
            <h3>集群资源总容量与预留</h3>
            <span className="tag">
              {data.online_nodes}/{data.nodes} 节点在线
            </span>
          </div>
          <CapacityBar label="vCPU 计算核心" used={data.reserved_vcpu} total={data.capacity_vcpu} />
          <CapacityBar label="物理内存容量" used={data.reserved_ram_mb} total={data.capacity_ram_mb} suffix=" MB" />
          <CapacityBar label="磁盘存储空间" used={data.reserved_disk_gb} total={data.capacity_disk_gb} suffix=" GB" />
        </section>

        <section className="panel">
          <div className="panel-heading">
            <h3>运维即时待办</h3>
            <span className="tag">REALTIME</span>
          </div>
          <div className="attention-list">
            <span>
              逾期欠费服务
              <strong style={{ color: data.overdue_services > 0 ? '#fb7185' : 'inherit' }}>
                {data.overdue_services}
              </strong>
            </span>
            <span>
              执行失败任务
              <strong style={{ color: data.failed_jobs > 0 ? '#fb7185' : 'inherit' }}>
                {data.failed_jobs}
              </strong>
            </span>
            <span>
              排队处理任务
              <strong>{data.pending_jobs}</strong>
            </span>
            <span>
              离线集群节点
              <strong style={{ color: data.nodes - data.online_nodes > 0 ? '#fbbf24' : 'inherit' }}>
                {data.nodes - data.online_nodes}
              </strong>
            </span>
          </div>
        </section>
      </div>
    </section>
  )
}

function CapacityBar({
  label,
  used,
  total,
  suffix = '',
}: {
  label: string
  used: number
  total: number
  suffix?: string
}) {
  const rate = total ? Math.min(100, Math.round((used / total) * 100)) : 0
  return (
    <div className="capacity-row">
      <div>
        <span>{label}</span>
        <strong>
          {used.toLocaleString()}{suffix} / {total.toLocaleString()}{suffix}
        </strong>
      </div>
      <div className="capacity-track">
        <i style={{ width: `${rate}%` }} />
      </div>
      <small>{rate}% 已分配预留</small>
    </div>
  )
}

function CustomersView() {
  const [customers, setCustomers] = useState<AccountRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [updating, setUpdating] = useState('')
  const [resetLink, setResetLink] = useState<{ name: string; email: string; link: string; expires_at: string } | null>(null)
  const [copied, setCopied] = useState(false)

  async function issueResetLink(customer: AccountRecord) {
    if (!window.confirm(`为客户【${customer.display_name}】生成一次性密码重置链接？此前未使用的链接会失效。`)) return
    setUpdating(customer.id)
    setError('')
    setCopied(false)
    try {
      const result = await api<{ email: string; link: string; expires_at: string }>(
        `/api/v1/admin/customers/${customer.id}/password-reset`,
        { method: 'POST' }
      )
      setResetLink({ name: customer.display_name, ...result })
    } catch (err) {
      setError(err instanceof Error ? err.message : '生成重置链接失败')
    } finally {
      setUpdating('')
    }
  }

  const load = () =>
    api<AccountRecord[]>('/api/v1/admin/customers')
      .then(setCustomers)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function toggleStatus(customer: AccountRecord) {
    const status = customer.status === 'active' ? 'suspended' : 'active'
    if (status === 'suspended' && !window.confirm(`确认暂停客户【${customer.display_name}】的账户？这会撤销其全部登录会话。`)) {
      return
    }
    setUpdating(customer.id)
    try {
      await api(`/api/v1/admin/customers/${customer.id}`, { method: 'PATCH', body: JSON.stringify({ status }) })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新状态失败')
    } finally {
      setUpdating('')
    }
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="CUSTOMER ACCOUNTS"
        title="客户管理"
        description="客户是订单、账单明细、VPS 实例与资金交易流水的统一归属主体。"
        action={() => setShowForm(true)}
        actionLabel="新增客户"
      />

      {error && <div className="form-error">{error}</div>}

      {resetLink && (
        <div className="inline-form reset-link-panel">
          <div className="inline-form-heading">
            <div>
              <h3>{resetLink.name} 的密码重置链接</h3>
              <p>
                登录邮箱 {resetLink.email}，{new Date(resetLink.expires_at).toLocaleString()} 前有效，只能使用一次。请通过工单或其他可信渠道发给客户本人。
              </p>
            </div>
            <button className="icon-button" onClick={() => setResetLink(null)} aria-label="关闭">
              <X size={16} />
            </button>
          </div>
          <div className="reset-link-row">
            <code>{resetLink.link}</code>
            <button
              className="secondary-button compact"
              onClick={() => {
                void navigator.clipboard?.writeText(resetLink.link).then(() => setCopied(true), () => setCopied(false))
              }}
            >
              {copied ? '已复制' : '复制链接'}
            </button>
          </div>
        </div>
      )}

      {showForm && (
        <CustomerForm
          onClose={() => setShowForm(false)}
          onCreated={() => {
            setShowForm(false)
            load()
          }}
        />
      )}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>客户主体</th>
              <th>客户类型</th>
              <th>账单邮箱</th>
              <th>计费币种</th>
              <th>账户状态</th>
              <th>注册时间</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {customers.map(customer => (
              <tr key={customer.id}>
                <td>
                  <strong>{customer.display_name}</strong>
                  <small>{customer.id}</small>
                </td>
                <td>{customer.kind === 'business' ? '企业客户' : '个人客户'}</td>
                <td>{customer.billing_email}</td>
                <td><code>{customer.default_currency}</code></td>
                <td><StatusBadge status={customer.status} /></td>
                <td>{new Date(customer.created_at).toLocaleString()}</td>
                <td>
                  <div className="row-actions">
                    <button
                      className="text-button"
                      disabled={updating === customer.id}
                      onClick={() => void toggleStatus(customer)}
                    >
                      {customer.status === 'active' ? '暂停账户' : '恢复正常'}
                    </button>
                    <button
                      className="text-button"
                      disabled={updating === customer.id}
                      onClick={() => void issueResetLink(customer)}
                    >
                      重置密码链接
                    </button>
                  </div>
                </td>
              </tr>
            ))}
            {!customers.length && (
              <tr>
                <td colSpan={7} className="empty-state">尚未创建任何客户账户</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function CustomerForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    try {
      await api('/api/v1/admin/customers', {
        method: 'POST',
        body: JSON.stringify({
          kind: data.get('kind'),
          display_name: data.get('display_name'),
          billing_email: data.get('billing_email'),
          legal_name: data.get('legal_name'),
          tax_id: data.get('tax_id'),
          country_code: data.get('country_code'),
          default_currency: data.get('default_currency'),
        }),
      })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>创建新客户</h3>
          <p>录入客户基础信息。企业客户可按需选填法定企业全称及纳税人识别号。</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit}>
        <label>
          <span>客户类型</span>
          <select name="kind">
            <option value="individual">个人</option>
            <option value="business">企业</option>
          </select>
        </label>
        <label>
          <span>显示名称 / 昵称</span>
          <input name="display_name" required placeholder="张三 / 某某科技" />
        </label>
        <label>
          <span>账单通知邮箱</span>
          <input name="billing_email" type="email" required placeholder="billing@example.com" />
        </label>
        <label>
          <span>法定名称（企业）</span>
          <input name="legal_name" placeholder="某某网络科技有限公司" />
        </label>
        <label>
          <span>统一社会信用代码 / 税号</span>
          <input name="tax_id" placeholder="91310000XXXXXXXXXX" />
        </label>
        <label>
          <span>国家 / 地区代码</span>
          <input name="country_code" maxLength={2} defaultValue="CN" />
        </label>
        <label>
          <span>默认计费币种</span>
          <select name="default_currency">
            <option value="CNY">CNY 人民币</option>
            <option value="USD">USD 美元</option>
          </select>
        </label>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving}>
            {saving ? '正在创建…' : '保存客户'}
          </button>
        </div>
      </form>
    </div>
  )
}

function OrdersView() {
  const [orders, setOrders] = useState<OrderRecord[]>([])
  const [customers, setCustomers] = useState<AccountRecord[]>([])
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [regions, setRegions] = useState<RegionRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')

  const load = async () => {
    try {
      const [orderRows, customerRows, planRows, regionRows] = await Promise.all([
        api<OrderRecord[]>('/api/v1/admin/orders'),
        api<AccountRecord[]>('/api/v1/admin/customers'),
        api<PlanRecord[]>('/api/v1/admin/plans'),
        api<RegionRecord[]>('/api/v1/admin/regions'),
      ])
      setOrders(orderRows)
      setCustomers(customerRows)
      setPlans(planRows.filter(plan => plan.enabled))
      setRegions(regionRows)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="SALES ORDERS"
        title="销售订单"
        description="所有订单金额由服务端严格根据当前生效套餐价格原子计算与锁价。"
        action={() => setShowForm(true)}
        actionLabel="创建订单"
      />

      {error && <div className="form-error">{error}</div>}

      {showForm && (
        <OrderForm
          customers={customers}
          plans={plans}
          regions={regions}
          onClose={() => setShowForm(false)}
          onCreated={() => {
            setShowForm(false)
            load()
          }}
        />
      )}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>订单号</th>
              <th>客户名称</th>
              <th>账单编号</th>
              <th>订单金额</th>
              <th>订单状态</th>
              <th>下单时间</th>
            </tr>
          </thead>
          <tbody>
            {orders.map(order => (
              <tr key={order.id}>
                <td>
                  <strong>{order.number}</strong>
                  <small>{order.id}</small>
                </td>
                <td>{order.customer_name}</td>
                <td><code>{order.invoice_number}</code></td>
                <td><strong>{money(order.total_minor, order.currency)}</strong></td>
                <td><StatusBadge status={order.status} /></td>
                <td>{new Date(order.created_at).toLocaleString()}</td>
              </tr>
            ))}
            {!orders.length && (
              <tr>
                <td colSpan={6} className="empty-state">尚未创建任何订单</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function OrderForm({
  customers,
  plans,
  regions,
  onClose,
  onCreated,
}: {
  customers: AccountRecord[]
  plans: PlanRecord[]
  regions: RegionRecord[]
  onClose: () => void
  onCreated: () => void
}) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const sellablePlans = plans.filter(plan => plan.enabled)
  const [customerID, setCustomerID] = useState(customers[0]?.id || '')
  const [planID, setPlanID] = useState(sellablePlans[0]?.id || '')
  const customer = customers.find(item => item.id === customerID)
  const plan = sellablePlans.find(item => item.id === planID)
  const prices = plan?.prices.filter(price => price.currency === customer?.default_currency) || []
  const ready = customers.length > 0 && sellablePlans.length > 0 && regions.length > 0

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    try {
      await api('/api/v1/admin/orders', {
        method: 'POST',
        body: JSON.stringify({
          account_id: data.get('account_id'),
          items: [
            {
              plan_id: data.get('plan_id'),
              region_id: data.get('region_id'),
              billing_cycle: data.get('billing_cycle'),
              quantity: Number(data.get('quantity')),
              configuration: { template_id: data.get('template_id') },
            },
          ],
        }),
      })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建订单失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>手工创建订单与账单</h3>
          <p>提交后将自动为目标客户生成待付款订单及防篡改的账单明细。</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      {!ready ? (
        <div className="form-error">请确保系统中已至少存在 1 个有效客户、1 个上架套餐及 1 个节点地域。</div>
      ) : (
        <form className="form-grid" onSubmit={submit}>
          <label>
            <span>归属客户</span>
            <select name="account_id" value={customerID} onChange={event => setCustomerID(event.target.value)}>
              {customers.map(item => (
                <option key={item.id} value={item.id}>
                  {item.display_name} ({item.default_currency})
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>选购套餐</span>
            <select name="plan_id" value={planID} onChange={event => setPlanID(event.target.value)}>
              {sellablePlans.map(item => (
                <option key={item.id} value={item.id}>
                  {item.name} · {item.virtualization.toUpperCase()}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>节点地域</span>
            <select name="region_id">
              {regions.map(region => (
                <option key={region.id} value={region.id}>
                  {region.name}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>计费周期</span>
            <select name="billing_cycle" required>
              {prices.map(price => (
                <option key={price.billing_cycle} value={price.billing_cycle}>
                  {cycleLabel(price.billing_cycle)} 付款 · {money(price.amount_minor + price.setup_fee_minor, price.currency)}
                </option>
              ))}
            </select>
          </label>
          <label>
            <span>购买数量</span>
            <input name="quantity" type="number" min="1" max="20" defaultValue="1" />
          </label>
          <label>
            <span>预设操作系统镜像</span>
            <select key={planID} name="template_id" defaultValue={plan?.default_template_id}>
              {plan?.allowed_template_ids.map(template => (
                <option key={template} value={template}>
                  {template}
                </option>
              ))}
            </select>
          </label>

          {!prices.length && (
            <div className="form-error wide">所选套餐尚未配置该客户币种的价格，请先前往“商品套餐”补充对应币种。</div>
          )}
          {error && <div className="form-error wide">{error}</div>}

          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={onClose}>取消</button>
            <button className="primary-button" disabled={saving || !prices.length}>
              {saving ? '正在生成…' : '生成订单'}
            </button>
          </div>
        </form>
      )}
    </div>
  )
}

function BillingView() {
  const [invoices, setInvoices] = useState<InvoiceRecord[]>([])
  const [transactions, setTransactions] = useState<TransactionRecord[]>([])
  const [error, setError] = useState('')
  const [paying, setPaying] = useState('')

  const load = async () => {
    try {
      const [invoiceRows, transactionRows] = await Promise.all([
        api<InvoiceRecord[]>('/api/v1/admin/invoices'),
        api<TransactionRecord[]>('/api/v1/admin/transactions'),
      ])
      setInvoices(invoiceRows)
      setTransactions(transactionRows)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function pay(invoice: InvoiceRecord) {
    if (
      !window.confirm(
        `确认已线下收到款项 ${money(invoice.balance_minor, invoice.currency)}？此操作将记录不可更改的入账流水并触发 VPS 自动调度开通。`
      )
    ) {
      return
    }
    setPaying(invoice.id)
    setError('')
    try {
      await api(`/api/v1/admin/invoices/${invoice.id}/pay`, { method: 'POST', body: JSON.stringify({ reference: '' }) })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '确认入账失败')
    } finally {
      setPaying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">BILLING & AUDIT LEDGER</p>
          <h2>账单与财务流水</h2>
          <p>采用金融级复式只追加（Append-Only）记账模型；任何退款与冲正均产生反向新流水。</p>
        </div>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="panel">
        <div className="panel-heading">
          <h3>全部账单</h3>
          <span className="tag">{invoices.length} 笔账单</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>账单号</th>
                <th>关联客户</th>
                <th>账单金额</th>
                <th>未付余额</th>
                <th>状态</th>
                <th>到期时间</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {invoices.map(invoice => (
                <tr key={invoice.id}>
                  <td><strong>{invoice.number}</strong></td>
                  <td>{invoice.customer_name}</td>
                  <td><strong>{money(invoice.total_minor, invoice.currency)}</strong></td>
                  <td style={{ color: invoice.balance_minor > 0 ? '#fbbf24' : 'inherit' }}>
                    <strong>{money(invoice.balance_minor, invoice.currency)}</strong>
                  </td>
                  <td><StatusBadge status={invoice.status} /></td>
                  <td>{new Date(invoice.due_at).toLocaleDateString()}</td>
                  <td>
                    {invoice.status === 'open' && (
                      <button
                        className="primary-button compact"
                        disabled={paying === invoice.id}
                        onClick={() => pay(invoice)}
                      >
                        {paying === invoice.id ? '入账处理中…' : '确认到账并开通'}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {!invoices.length && (
                <tr>
                  <td colSpan={7} className="empty-state">暂无账单数据</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>不可变资金交易流水</h3>
          <span className="tag">APPEND ONLY</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>外部交易单号</th>
                <th>账单号</th>
                <th>付款客户</th>
                <th>收款渠道</th>
                <th>实付金额</th>
                <th>流水状态</th>
                <th>入账时间</th>
              </tr>
            </thead>
            <tbody>
              {transactions.map(transaction => (
                <tr key={transaction.id}>
                  <td>
                    <strong>{transaction.provider_transaction_id}</strong>
                    <small>{transaction.id}</small>
                  </td>
                  <td><code>{transaction.invoice_number}</code></td>
                  <td>{transaction.customer_name}</td>
                  <td><span className="tag">{transaction.provider.toUpperCase()}</span></td>
                  <td><strong>{money(transaction.amount_minor, transaction.currency)}</strong></td>
                  <td><StatusBadge status={transaction.status} /></td>
                  <td>{new Date(transaction.created_at).toLocaleString()}</td>
                </tr>
              ))}
              {!transactions.length && (
                <tr>
                  <td colSpan={7} className="empty-state">暂无交易流水记录</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}

function PaymentSettingsView() {
  const [settings, setSettings] = useState<PaymentSettingsRecord | null>(null)
  const [form, setForm] = useState({
    type: 'disabled',
    generic_base_url: '',
    generic_secret: '',
    alipay_app_id: '',
    alipay_private_key: '',
    alipay_public_key: '',
    alipay_gateway_url: 'https://openapi.alipay.com/gateway.do',
    epay_api_url: '',
    epay_partner_id: '',
    epay_merchant_key: '',
    epay_payment_type: 'alipay',
  })
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState('')
  const [saved, setSaved] = useState(false)

  useEffect(() => {
    api<PaymentSettingsRecord>('/api/v1/admin/settings/payment')
      .then(value => {
        setSettings(value)
        setForm(current => ({ ...current, ...value.gateway }))
      })
      .catch(err => setError(err.message))
  }, [])

  const update =
    (key: keyof typeof form) =>
    (event: ChangeEvent<HTMLInputElement | HTMLSelectElement | HTMLTextAreaElement>) =>
      setForm(current => ({ ...current, [key]: event.target.value }))

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setSaved(false)
    setError('')
    try {
      await api('/api/v1/admin/settings/payment', { method: 'PUT', body: JSON.stringify(form) })
      const value = await api<PaymentSettingsRecord>('/api/v1/admin/settings/payment')
      setSettings(value)
      setForm(current => ({
        ...current,
        ...value.gateway,
        generic_secret: '',
        alipay_private_key: '',
        alipay_public_key: '',
        epay_merchant_key: '',
      }))
      setSaved(true)
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">PAYMENT GATEWAY</p>
          <h2>支付网关配置</h2>
          <p>全局启用一个收款渠道；密钥经 AES-256 加密保存且不回显明文。留空代表保留原密钥。</p>
        </div>
        <StatusBadge status={form.type === 'disabled' ? 'disabled' : 'online'} />
      </div>

      {error && <div className="form-error">{error}</div>}
      {saved && <div className="success-note">支付网关配置已更新并立即生效。</div>}

      <form className="panel payment-settings" onSubmit={submit}>
        <div className="gateway-options">
          <label className={form.type === 'disabled' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'disabled'}
              onChange={() => setForm(v => ({ ...v, type: 'disabled' }))}
            />
            <strong>停用在线支付</strong>
            <span>仅允许管理员在后台确认线下到账</span>
          </label>
          <label className={form.type === 'alipay_f2f' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'alipay_f2f'}
              onChange={() => setForm(v => ({ ...v, type: 'alipay_f2f' }))}
            />
            <strong>支付宝当面付</strong>
            <span>官方 RSA2 签名 / 预下单 Native 二维码</span>
          </label>
          <label className={form.type === 'epay' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'epay'}
              onChange={() => setForm(v => ({ ...v, type: 'epay' }))}
            />
            <strong>易支付（彩虹协议）</strong>
            <span>兼容标准易支付开放接口协议</span>
          </label>
          <label className={form.type === 'generic' ? 'selected' : ''}>
            <input
              type="radio"
              name="gateway"
              checked={form.type === 'generic'}
              onChange={() => setForm(v => ({ ...v, type: 'generic' }))}
            />
            <strong>通用 HMAC 收银台</strong>
            <span>对接企业自有或三方定制收银台</span>
          </label>
        </div>

        {form.type === 'generic' && (
          <div className="form-grid">
            <label>
              <span>外部收银台地址</span>
              <input type="url" value={form.generic_base_url} onChange={update('generic_base_url')} required />
            </label>
            <label>
              <span>HMAC 签名密钥 {settings?.gateway.generic_secret_configured ? '（已加密配置）' : ''}</span>
              <input
                type="password"
                value={form.generic_secret}
                onChange={update('generic_secret')}
                placeholder="留空保留已有密钥"
              />
            </label>
          </div>
        )}

        {form.type === 'alipay_f2f' && (
          <div className="form-grid">
            <label>
              <span>应用 App ID</span>
              <input value={form.alipay_app_id} onChange={update('alipay_app_id')} required />
            </label>
            <label>
              <span>网关地址</span>
              <input type="url" value={form.alipay_gateway_url} onChange={update('alipay_gateway_url')} required />
            </label>
            <label className="wide">
              <span>商户应用私钥 {settings?.gateway.alipay_private_key_configured ? '（已加密配置）' : ''}</span>
              <textarea
                rows={4}
                value={form.alipay_private_key}
                onChange={update('alipay_private_key')}
                placeholder="PKCS#1 / PKCS#8 格式，留空保留已有私钥"
              />
            </label>
            <label className="wide">
              <span>支付宝公钥 {settings?.gateway.alipay_public_key_configured ? '（已加密配置）' : ''}</span>
              <textarea
                rows={4}
                value={form.alipay_public_key}
                onChange={update('alipay_public_key')}
                placeholder="留空保留已有支付宝公钥"
              />
            </label>
          </div>
        )}

        {form.type === 'epay' && (
          <div className="form-grid">
            <label>
              <span>易支付接口根地址</span>
              <input
                type="url"
                value={form.epay_api_url}
                onChange={update('epay_api_url')}
                placeholder="https://pay.example.com/"
                required
              />
            </label>
            <label>
              <span>商户 ID（PID）</span>
              <input value={form.epay_partner_id} onChange={update('epay_partner_id')} required />
            </label>
            <label>
              <span>支付通道</span>
              <select value={form.epay_payment_type} onChange={update('epay_payment_type')}>
                <option value="alipay">支付宝</option>
                <option value="wxpay">微信支付</option>
                <option value="qqpay">QQ 钱包</option>
              </select>
            </label>
            <label>
              <span>商户密钥 {settings?.gateway.epay_merchant_key_configured ? '（已加密配置）' : ''}</span>
              <input
                type="password"
                value={form.epay_merchant_key}
                onChange={update('epay_merchant_key')}
                placeholder="留空保留已有密钥"
              />
            </label>
          </div>
        )}

        {form.type !== 'disabled' && settings && (
          <div className="callback-box">
            <span>网关异步回调通知地址（供在支付服务商后台配置）：</span>
            <code>{settings.callbacks[form.type]}</code>
          </div>
        )}

        <div className="form-actions">
          <button className="primary-button compact" disabled={saving}>
            {saving ? '正在保存…' : '保存并应用设置'}
          </button>
        </div>
      </form>
    </section>
  )
}

type SiteSettingsRecord = {
  app_name: string
  public_url: string
  timezone: string
  worker_poll_interval: string
  reconcile_interval: string
  lifecycle_interval: string
  renewal_lead_time: string
  overdue_grace_period: string
  termination_retention: string
  notification_webhook_url: string
  notification_webhook_secret_configured: boolean
  smtp_host: string
  smtp_port: number
  smtp_username: string
  smtp_password_configured: boolean
  smtp_from: string
  smtp_security: string
  password_reset_mail_enabled: boolean
}

function SiteSettingsView() {
  const [settings, setSettings] = useState<SiteSettingsRecord | null>(null)
  const [form, setForm] = useState({
    app_name: '',
    public_url: '',
    timezone: 'Asia/Shanghai',
    worker_poll_interval: '',
    reconcile_interval: '',
    lifecycle_interval: '',
    renewal_lead_time: '',
    overdue_grace_period: '',
    termination_retention: '',
    notification_webhook_url: '',
    notification_webhook_secret: '',
    smtp_host: '',
    smtp_port: '587',
    smtp_username: '',
    smtp_password: '',
    smtp_from: '',
    smtp_security: 'starttls',
  })
  const [clearPassword, setClearPassword] = useState(false)
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const apply = (value: SiteSettingsRecord) => {
    setSettings(value)
    setForm(current => ({
      ...current,
      app_name: value.app_name,
      public_url: value.public_url,
      timezone: value.timezone,
      worker_poll_interval: value.worker_poll_interval,
      reconcile_interval: value.reconcile_interval,
      lifecycle_interval: value.lifecycle_interval,
      renewal_lead_time: value.renewal_lead_time,
      overdue_grace_period: value.overdue_grace_period,
      termination_retention: value.termination_retention,
      notification_webhook_url: value.notification_webhook_url,
      notification_webhook_secret: '',
      smtp_host: value.smtp_host,
      smtp_port: String(value.smtp_port || 587),
      smtp_username: value.smtp_username,
      smtp_password: '',
      smtp_from: value.smtp_from,
      smtp_security: value.smtp_security || 'starttls',
    }))
    setClearPassword(false)
  }

  useEffect(() => {
    api<SiteSettingsRecord>('/api/v1/admin/settings/site')
      .then(apply)
      .catch(err => setError(err.message))
  }, [])

  const update =
    (key: keyof typeof form) =>
    (event: ChangeEvent<HTMLInputElement | HTMLSelectElement>) =>
      setForm(current => ({ ...current, [key]: event.target.value }))

  async function submit(event: FormEvent) {
    event.preventDefault()
    setSaving(true)
    setNotice('')
    setError('')
    try {
      const value = await api<SiteSettingsRecord>('/api/v1/admin/settings/site', {
        method: 'PUT',
        body: JSON.stringify({ ...form, smtp_port: Number(form.smtp_port) || 0, clear_smtp_password: clearPassword }),
      })
      apply(value)
      setNotice('站点设置已保存并立即生效。')
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  async function sendTest() {
    setTesting(true)
    setNotice('')
    setError('')
    try {
      const result = await api<{ to: string }>('/api/v1/admin/settings/site/test-mail', { method: 'POST' })
      setNotice(`测试邮件已发送到 ${result.to}，请检查收件箱和垃圾邮件。`)
    } catch (err) {
      setError(err instanceof Error ? err.message : '发送失败')
    } finally {
      setTesting(false)
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">SITE SETTINGS</p>
          <h2>站点设置</h2>
          <p>站点名称、公开地址、自动化周期、通知与发信邮箱。支付回调和密码重置链接都基于公开地址，换域名后要同步修改。密钥加密保存且不回显，留空代表保留原值。</p>
        </div>
      </div>

      {error && <div className="form-error">{error}</div>}
      {notice && <div className="success-note">{notice}</div>}

      <form className="panel site-settings" onSubmit={submit}>
        <div className="form-grid">
          <fieldset className="wide">
            <legend>基本信息</legend>
          </fieldset>
          <label>
            <span>站点名称</span>
            <input value={form.app_name} onChange={update('app_name')} required />
          </label>
          <label>
            <span>公开访问地址</span>
            <input type="url" value={form.public_url} onChange={update('public_url')} placeholder="https://billing.example.com" required />
          </label>
          <label>
            <span>时区</span>
            <input value={form.timezone} onChange={update('timezone')} placeholder="Asia/Shanghai" required />
          </label>

          <fieldset className="wide">
            <legend>续费与自动化（格式如 30s、5m、72h）</legend>
          </fieldset>
          <label>
            <span>续费账单提前生成</span>
            <input value={form.renewal_lead_time} onChange={update('renewal_lead_time')} />
          </label>
          <label>
            <span>逾期宽限期（到期后暂停）</span>
            <input value={form.overdue_grace_period} onChange={update('overdue_grace_period')} />
          </label>
          <label>
            <span>暂停后保留（到期后删除）</span>
            <input value={form.termination_retention} onChange={update('termination_retention')} />
          </label>
          <label>
            <span>任务轮询间隔</span>
            <input value={form.worker_poll_interval} onChange={update('worker_poll_interval')} />
          </label>
          <label>
            <span>对账间隔</span>
            <input value={form.reconcile_interval} onChange={update('reconcile_interval')} />
          </label>
          <label>
            <span>生命周期检查间隔</span>
            <input value={form.lifecycle_interval} onChange={update('lifecycle_interval')} />
          </label>

          <fieldset className="wide">
            <legend>通知 Webhook（可选）</legend>
          </fieldset>
          <label>
            <span>通知地址</span>
            <input type="url" value={form.notification_webhook_url} onChange={update('notification_webhook_url')} placeholder="留空则不投递" />
          </label>
          <label>
            <span>签名密钥 {settings?.notification_webhook_secret_configured ? '（已加密配置）' : ''}</span>
            <input
              type="password"
              value={form.notification_webhook_secret}
              onChange={update('notification_webhook_secret')}
              placeholder="至少 32 位，留空保留或自动生成"
              autoComplete="new-password"
            />
          </label>
          <div />

          <fieldset className="wide">
            <legend>
              发信邮箱（SMTP）· 用于客户找回密码
              {settings && (
                <span className={`tag ${settings.password_reset_mail_enabled ? 'tag-ok' : ''}`}>
                  {settings.password_reset_mail_enabled ? '已启用' : '未配置'}
                </span>
              )}
            </legend>
          </fieldset>
          <label>
            <span>SMTP 服务器</span>
            <input value={form.smtp_host} onChange={update('smtp_host')} placeholder="smtp.example.com，留空不发信" />
          </label>
          <label>
            <span>端口</span>
            <input type="number" min={1} max={65535} value={form.smtp_port} onChange={update('smtp_port')} />
          </label>
          <label>
            <span>加密方式</span>
            <select value={form.smtp_security} onChange={update('smtp_security')}>
              <option value="starttls">STARTTLS（通常 587）</option>
              <option value="tls">SSL/TLS（通常 465）</option>
              <option value="none">不加密（仅限内网中继）</option>
            </select>
          </label>
          <label>
            <span>发件人</span>
            <input value={form.smtp_from} onChange={update('smtp_from')} placeholder="VPSBill <noreply@example.com>" />
          </label>
          <label>
            <span>登录账号</span>
            <input value={form.smtp_username} onChange={update('smtp_username')} autoComplete="off" />
          </label>
          <label>
            <span>登录密码 {settings?.smtp_password_configured ? '（已加密配置）' : ''}</span>
            <input
              type="password"
              value={form.smtp_password}
              onChange={update('smtp_password')}
              placeholder="留空保留已有密码"
              autoComplete="new-password"
              disabled={clearPassword}
            />
          </label>
          {settings?.smtp_password_configured && (
            <label className="checkbox wide">
              <input type="checkbox" checked={clearPassword} onChange={event => setClearPassword(event.target.checked)} />
              清除已保存的 SMTP 密码
            </label>
          )}
        </div>

        <div className="form-actions">
          <button
            type="button"
            className="secondary-button compact"
            disabled={testing || !settings?.password_reset_mail_enabled}
            onClick={() => void sendTest()}
            title={settings?.password_reset_mail_enabled ? '发送到当前管理员邮箱' : '先保存 SMTP 设置'}
          >
            {testing ? '正在发送…' : '发送测试邮件'}
          </button>
          <button className="primary-button compact" disabled={saving}>
            {saving ? '正在保存…' : '保存设置'}
          </button>
        </div>
      </form>
    </section>
  )
}

function ServicesView() {
  const [services, setServices] = useState<ServiceRecord[]>([])
  const [jobs, setJobs] = useState<ProvisioningJobRecord[]>([])
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState('')

  const load = async () => {
    try {
      const [serviceRows, jobRows] = await Promise.all([
        api<ServiceRecord[]>('/api/v1/admin/services'),
        api<ProvisioningJobRecord[]>('/api/v1/admin/jobs'),
      ])
      setServices(serviceRows)
      setJobs(jobRows)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 10000)
    return () => window.clearInterval(timer)
  }, [])

  async function serviceAction(service: ServiceRecord, action: 'start' | 'stop' | 'restart' | 'terminate') {
    const prompts = {
      start: `确认开机实例【${service.instance_name}】？`,
      stop: `确认关机实例【${service.instance_name}】？`,
      restart: `确认重启实例【${service.instance_name}】？`,
      terminate: `确认立即终止【${service.customer_name}】的实例【${service.instance_name}】？实例与数据将被删除，未付账单作废，此操作不可撤销。`,
    }
    if (!window.confirm(prompts[action])) return
    setRetrying(service.id)
    setError('')
    try {
      await api(`/api/v1/admin/services/${service.id}/actions/${action}`, { method: 'POST' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setRetrying('')
    }
  }

  async function retry(job: ProvisioningJobRecord) {
    if (!window.confirm(`确认重新提交实例【${job.instance_name}】的自动化开通任务？`)) return
    setRetrying(job.id)
    setError('')
    try {
      await api(`/api/v1/admin/jobs/${job.id}/retry`, { method: 'POST' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '重试任务失败')
    } finally {
      setRetrying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">AUTOMATION & INSTANCES</p>
          <h2>VPS 服务与自动化执行队列</h2>
          <p>自动化开通、电源调度、失败重试与节点实际运行状态每 10 秒自动刷新同步。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="panel">
        <div className="panel-heading">
          <h3>服务实例列表</h3>
          <span className="tag">{services.length} SERVICES</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>实例名称</th>
                <th>客户 / 套餐</th>
                <th>节点 / 地域</th>
                <th>业务状态</th>
                <th>运行状态</th>
                <th>分配 IP</th>
                <th>下次到期</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {services.map(service => (
                <tr key={service.id}>
                  <td>
                    <strong>{service.instance_name}</strong>
                    <small>{service.id}</small>
                  </td>
                  <td>
                    {service.customer_name}
                    <small>{service.plan_name}</small>
                  </td>
                  <td>
                    {service.node_name || (service.status === 'terminated' ? '—' : '等待调度')}
                    <small>{service.region_name}</small>
                  </td>
                  <td><StatusBadge status={service.status} /></td>
                  <td>
                    <StatusBadge status={service.runtime_status} />
                    {service.last_reconcile_error && (
                      <small title={service.last_reconcile_error} style={{ color: '#fb7185' }}>
                        对账异常：{service.last_reconcile_error}
                      </small>
                    )}
                  </td>
                  <td>
                    <code>{service.primary_ipv4 || '—'}</code>
                    <small>{service.primary_ipv6}</small>
                  </td>
                  <td>{service.next_due_at && service.status !== 'terminated' ? new Date(service.next_due_at).toLocaleDateString() : '—'}</td>
                  <td>
                    <div className="row-actions">
                      {(service.status === 'active' || service.status === 'overdue') && (
                        service.runtime_status === 'stopped' ? (
                          <button className="text-button" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'start')}>开机</button>
                        ) : (
                          <>
                            <button className="text-button" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'stop')}>关机</button>
                            <button className="text-button" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'restart')}>重启</button>
                          </>
                        )
                      )}
                      {service.status !== 'terminating' && service.status !== 'terminated' && (
                        <button className="text-button danger" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'terminate')}>终止</button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
              {!services.length && (
                <tr>
                  <td colSpan={8} className="empty-state">暂无已开通服务，账单支付后会自动进入队列开通</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>自动化任务队列</h3>
          <span className="tag">WORKER POOL</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>目标实例</th>
                <th>调度动作</th>
                <th>执行状态</th>
                <th>尝试次数</th>
                <th>下次执行时间</th>
                <th>错误诊断</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map(job => (
                <tr key={job.id}>
                  <td>
                    <strong>{job.instance_name}</strong>
                    <small>{job.customer_name}</small>
                  </td>
                  <td><span className="tag">{job.action}</span></td>
                  <td><StatusBadge status={job.status} /></td>
                  <td><code>{job.attempts} / 8</code></td>
                  <td>{new Date(job.available_at).toLocaleString()}</td>
                  <td className="error-cell">
                    <JobError value={job.last_error} />
                  </td>
                  <td>
                    {['failed', 'dead'].includes(job.status) && (
                      <button
                        className="text-button"
                        disabled={retrying === job.id}
                        onClick={() => retry(job)}
                      >
                        <RefreshCw size={13} />
                        {retrying === job.id ? '提交中…' : '人工重试'}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {!jobs.length && (
                <tr>
                  <td colSpan={7} className="empty-state">当前任务队列无正在积压或异常的任务</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}

function NodesView() {
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [testing, setTesting] = useState('')
  const [editing, setEditing] = useState<NodeRecord | null>(null)
  const [providers, setProviders] = useState<ProviderTypeRecord[]>([])

  async function removeNode(node: NodeRecord) {
    if (!window.confirm(`确认删除节点【${node.name}】？仅在节点上没有未终止的服务时才能删除。`)) return
    setError('')
    try {
      await api(`/api/v1/admin/nodes/${node.id}`, { method: 'DELETE' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types').then(setProviders).catch(() => undefined)
  }, [])

  const load = () =>
    api<NodeRecord[]>('/api/v1/admin/nodes')
      .then(setNodes)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function testNode(id: string) {
    setTesting(id)
    setError('')
    try {
      await api(`/api/v1/admin/nodes/${id}/test`, { method: 'POST' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '节点测试失败')
    } finally {
      setTesting('')
    }
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="PROVIDER INTEGRATIONS"
        title="虚拟化节点对接"
        description="统一纳管宿主机：CLICD 与 LXDAPI 通过 API 对接，Hatch Agent 由宿主机主动连入。所有对接方式共用调度与账务模型。"
        action={() => { setEditing(null); setShowForm(true) }}
        actionLabel="新增节点对接"
      />

      {error && <div className="form-error" role="alert">{error}</div>}

      {(showForm || editing) && (
        <NodeForm
          key={editing?.id ?? 'new'}
          node={editing ?? undefined}
          onClose={() => {
            setShowForm(false)
            setEditing(null)
          }}
          onCreated={() => {
            setShowForm(false)
            setEditing(null)
            load()
          }}
        />
      )}

      <div className="provider-strip">
        {providers.map(item => (
          <article className="available" key={item.type}>
            <strong>{item.name}</strong>
            <span>{item.virtualization_types.map(virtualizationLabel).join(' / ')} · {item.agent_managed ? 'Agent 主动连入' : 'API 对接'}</span>
          </article>
        ))}
      </div>

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>节点名称</th>
              <th>适配器类型</th>
              <th>归属地域</th>
              <th>支持虚拟化</th>
              <th>节点总物理容量</th>
              <th>连接状态</th>
              <th>最后心跳在线</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {nodes.map(node => (
              <tr key={node.id}>
                <td>
                  <strong>{node.name}</strong>
                  <small>{node.base_url}</small>
                </td>
                <td><span className="tag">{node.provider_type.toUpperCase()}</span></td>
                <td>
                  {node.region_name}
                  <small>{node.region_code}</small>
                </td>
                <td>{node.virtualization_types.join(' / ').toUpperCase()}</td>
                <td>
                  <strong>{node.capacity_vcpu} vCPU</strong>
                  <small>
                    {node.capacity_ram_mb.toLocaleString()} MB / {node.capacity_disk_gb.toLocaleString()} GB
                  </small>
                </td>
                <td><StatusBadge status={node.status} /></td>
                <td>{node.last_seen_at ? new Date(node.last_seen_at).toLocaleString() : '—'}</td>
                <td>
                  <button
                    className="text-button"
                    onClick={() => testNode(node.id)}
                    disabled={testing === node.id}
                  >
                    <RefreshCw size={13} />
                    {testing === node.id ? '测试连通中…' : '测试连通性'}
                  </button>
                  <div className="row-actions">
                    <button className="text-button" onClick={() => { setShowForm(false); setEditing(node) }}>编辑</button>
                    <button className="text-button danger" onClick={() => void removeNode(node)}>删除</button>
                  </div>
                </td>
              </tr>
            ))}
            {!nodes.length && (
              <tr>
                <td colSpan={8} className="empty-state">尚未接入任何虚拟化计算节点</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}

function NodeForm({ node, onClose, onCreated }: { node?: NodeRecord; onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [types, setTypes] = useState<ProviderTypeRecord[]>([])
  const [selected, setSelected] = useState(node?.provider_type ?? 'clicd')
  const editing = Boolean(node)
  const existingOption = (key: string) => {
    const value = node?.provider_options?.[key]
    return Array.isArray(value) ? value.join(',') : value === undefined || value === null ? '' : String(value)
  }

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types')
      .then((items) => {
        setTypes(items)
        if (!node && items.length > 0 && !items.some((item) => item.type === 'clicd')) setSelected(items[0].type)
      })
      .catch((err: Error) => setError(err.message))
  }, [])

  const descriptor = types.find((item) => item.type === selected)
  const options = descriptor?.options ?? []

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!descriptor) return
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    const providerOptions: Record<string, unknown> = {}
    for (const field of options) {
      const name = `option_${field.key}`
      providerOptions[field.key] = field.kind === 'bool' ? data.get(name) === 'on' : String(data.get(name) ?? '')
    }
    try {
      await api(node ? `/api/v1/admin/nodes/${node.id}` : '/api/v1/admin/nodes', {
        method: node ? 'PUT' : 'POST',
        body: JSON.stringify({
          // Provider and region are fixed once a node exists.
          ...(node ? {} : { provider_type: descriptor.type, region_code: data.get('region_code'), region_name: data.get('region_name') }),
          name: data.get('name'),
          base_url: descriptor.agent_managed ? '' : data.get('base_url'),
          api_key: data.get('api_key'),
          virtualization_types: data.getAll('virtualization_types'),
          provider_options: providerOptions,
        }),
      })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : '节点接入验证失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{editing ? `编辑节点 ${node?.name}` : '新增虚拟化节点对接'}</h3>
          <p>{editing ? '保存前会用新配置重新验证节点连接；对接方式与地域不可修改。' : '配置节点访问端点并验证连通性，所有通信密钥将加密存储。'}</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit} key={`${selected}-${types.length}`}>
        <label>
          <span>对接方式</span>
          <select name="provider_type" value={selected} disabled={editing} onChange={(event) => setSelected(event.target.value)}>
            {types.map((item) => (
              <option key={item.type} value={item.type}>
                {item.name}（{item.virtualization_types.join(' / ').toUpperCase()}）
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>节点标识名称</span>
          <input name="name" required placeholder="node-sha-01" defaultValue={node?.name} />
        </label>
        <label>
          <span>地域标识代号</span>
          <input name="region_code" required placeholder="SHA" defaultValue={node?.region_code} disabled={editing} />
        </label>
        <label>
          <span>地域中文名称</span>
          <input name="region_name" required placeholder="华东上海" defaultValue={node?.region_name} disabled={editing} />
        </label>
        {descriptor && !descriptor.agent_managed && (
          <label>
            <span>API 接口根地址</span>
            <input name="base_url" type="url" required placeholder={descriptor.base_url_hint} defaultValue={node?.base_url} />
          </label>
        )}
        <label>
          <span>{descriptor?.credential_label ?? 'API Key'}{editing ? '（留空保持不变）' : ''}</span>
          <input name="api_key" type="password" required={!editing} autoComplete="off" />
        </label>
        {descriptor?.agent_managed && (
          <p className="form-hint wide">该节点由 Agent 主动连入。先在宿主机安装 Agent 并连接到本站，再填写 Agent 安装时显示的令牌完成接入。</p>
        )}
        {options.map((field) =>
          field.kind === 'bool' ? (
            <label className="checkbox" key={field.key} title={field.help}>
              <input type="checkbox" name={`option_${field.key}`} defaultChecked={node?.provider_options?.[field.key] === true} /> {field.label}
            </label>
          ) : (
            <label key={field.key} title={field.help}>
              <span>{field.label}{field.required ? '' : '（可选）'}</span>
              <input
                name={`option_${field.key}`}
                type={field.kind === 'number' ? 'number' : 'text'}
                required={field.required}
                placeholder={field.placeholder}
                defaultValue={existingOption(field.key)}
              />
              {field.help && <small>{field.help}</small>}
            </label>
          ),
        )}
        <fieldset className="wide">
          <legend>支持的虚拟化技术</legend>
          {(descriptor?.virtualization_types ?? []).map((kind, index) => (
            <label className="checkbox" key={kind}>
              <input type="checkbox" name="virtualization_types" value={kind} defaultChecked={node ? node.virtualization_types.includes(kind) : index === 0} /> {virtualizationLabel(kind)}
            </label>
          ))}
        </fieldset>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving || !descriptor}>
            {saving ? '正在验证…' : editing ? '验证并保存' : '验证并接入节点'}
          </button>
        </div>
      </form>
    </div>
  )
}

function virtualizationLabel(kind: string) {
  switch (kind) {
    case 'lxc': return 'LXC 容器'
    case 'kvm': return 'KVM 硬件虚拟化'
    case 'podman': return 'Podman 容器'
    default: return kind.toUpperCase()
  }
}

function HostsView({ onOpen }: { onOpen?: (id: string) => void }) {
  const [hosts, setHosts] = useState<HostProbeRecord[]>([])
  const [error, setError] = useState('')

  const load = () =>
    api<HostProbeRecord[]>('/api/v1/admin/hosts')
      .then(setHosts)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 30000)
    return () => window.clearInterval(timer)
  }, [])

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">HOST TELEMETRY</p>
          <h2>宿主机硬件探针</h2>
          <p>展示集群节点总容量与分配情况；CLICD 节点可进一步查看 CPU、内存条、磁盘健康度与实时曲线。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="host-grid">
        {hosts.map(host => (
          <article className="host-card" key={host.id}>
            <header>
              <div>
                <span className="tag">{host.provider_type.toUpperCase()}</span>
                <h3>{host.name}</h3>
                <small>
                  {host.region_name} · {host.base_url}
                </small>
              </div>
              <StatusBadge status={host.status} />
            </header>

            <div className="host-summary">
              <span>
                <strong>{host.capacity_vcpu - host.reserved_vcpu}</strong> / {host.capacity_vcpu}
                <small>空闲 vCPU</small>
              </span>
              <span>
                <strong>{(host.capacity_ram_mb - host.reserved_ram_mb).toLocaleString()}</strong> /{' '}
                {host.capacity_ram_mb.toLocaleString()}
                <small>空闲内存 MB</small>
              </span>
              <span>
                <strong>{(host.capacity_disk_gb - host.reserved_disk_gb).toLocaleString()}</strong> /{' '}
                {host.capacity_disk_gb.toLocaleString()}
                <small>空闲磁盘 GB</small>
              </span>
            </div>

            <CapacityBar label="CPU 分配率" used={host.reserved_vcpu} total={host.capacity_vcpu} />
            <CapacityBar label="内存预留率" used={host.reserved_ram_mb} total={host.capacity_ram_mb} suffix=" MB" />

            <footer>
              <span>虚拟化：{host.virtualization_types.join(' / ').toUpperCase()}</span>
              <span>最后心跳：{host.last_seen_at ? new Date(host.last_seen_at).toLocaleString() : '从未'}</span>
            </footer>

            {host.provider_type === 'clicd' && onOpen && (
              <button className="secondary-button host-detail-button" onClick={() => onOpen(host.id)}>
                查看深度硬件探针与采样曲线 <ChevronRight size={15} />
              </button>
            )}
          </article>
        ))}
        {!hosts.length && <div className="empty-card">暂无宿主机探针数据，请先接入虚拟化节点。</div>}
      </div>
    </section>
  )
}

function PlansView() {
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<PlanRecord | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<PlanRecord[]>('/api/v1/admin/plans')
      .then(setPlans)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function toggle(plan: PlanRecord) {
    try {
      await api(`/api/v1/admin/plans/${plan.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: !plan.enabled }),
      })
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新状态失败')
    }
  }

  const closeForm = () => {
    setShowForm(false)
    setEditing(null)
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="PRODUCT CATALOG"
        title="商品套餐管理"
        description="网络策略与模板随套餐统一下发，套餐修改将自增版本号且不影响历史订单明细。"
        action={() => {
          setEditing(null)
          setShowForm(true)
        }}
        actionLabel="创建新套餐"
      />

      {error && <div className="form-error">{error}</div>}

      {showForm && (
        <PlanForm
          plan={editing}
          onClose={closeForm}
          onSaved={() => {
            closeForm()
            load()
          }}
        />
      )}

      <div className="plan-grid">
        {plans.map(plan => (
          <article className={plan.enabled ? 'plan-card' : 'plan-card disabled'} key={plan.id}>
            <div className="plan-card-top">
              <span className="tag">{plan.provider_type.toUpperCase()} · {plan.virtualization.toUpperCase()}</span>
              <StatusBadge status={plan.enabled ? 'online' : 'disabled'} />
            </div>
            <h3>{plan.name}</h3>
            <small>
              {plan.code} · 版本 v{plan.version}
            </small>

            <div className="spec-line">
              <strong>{plan.vcpu}</strong> vCPU
              <strong>{plan.ram_mb}</strong> MB
              <strong>{plan.disk_gb}</strong> GB
            </div>

            <small>默认镜像：{plan.default_template_id}</small>
            <small className="plan-network">
              网络：
              {[
                plan.assign_nat && `NAT×${plan.port_mapping_count}`,
                plan.assign_ipv4 && `IPv4×${plan.ipv4_count}`,
                plan.assign_ipv6 && `IPv6×${plan.ipv6_count}`,
              ]
                .filter(Boolean)
                .join(' / ')}
            </small>

            <div className="price-line">
              {plan.prices[0] ? (
                <>
                  <strong>¥{(plan.prices[0].amount_minor / 100).toFixed(2)}</strong>
                  <span>/ {cycleLabel(plan.prices[0].billing_cycle)}</span>
                </>
              ) : (
                '暂无报价'
              )}
            </div>

            <div className="plan-actions">
              <button
                className="secondary-button"
                onClick={() => {
                  setEditing(plan)
                  setShowForm(true)
                }}
              >
                编辑套餐
              </button>
              <button className="secondary-button" onClick={() => toggle(plan)}>
                {plan.enabled ? '下架套餐' : '重新上架'}
              </button>
            </div>
          </article>
        ))}
        {!plans.length && <div className="empty-card" style={{ gridColumn: '1 / -1' }}>尚未创建任何商品套餐</div>}
      </div>
    </section>
  )
}

function PlanForm({
  plan,
  onClose,
  onSaved,
}: {
  plan: PlanRecord | null
  onClose: () => void
  onSaved: () => void
}) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [templates, setTemplates] = useState<AvailableTemplateRecord[]>([])
  const [loadingTemplates, setLoadingTemplates] = useState(true)

  const [providers, setProviders] = useState<ProviderTypeRecord[]>([])
  const [providerType, setProviderType] = useState(plan?.provider_type || 'clicd')
  const [virtualization, setVirtualization] = useState<'lxc' | 'kvm' | 'podman'>(plan?.virtualization || 'lxc')
  const [allowed, setAllowed] = useState<string[]>(plan?.allowed_template_ids || [])
  const [defaultTemplate, setDefaultTemplate] = useState(plan?.default_template_id || '')

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types').then(setProviders).catch(err => setError(err.message))
  }, [])

  useEffect(() => {
    api<AvailableTemplateRecord[]>('/api/v1/admin/templates')
      .then(setTemplates)
      .catch(err => setError(err.message))
      .finally(() => setLoadingTemplates(false))
  }, [])

  const descriptor = providers.find(item => item.type === providerType)
  const virtualizations = (descriptor?.virtualization_types ?? [virtualization]) as Array<'lxc' | 'kvm' | 'podman'>
  const visibleTemplates = templates.filter(item => item.provider_type === providerType && item.virtualization === virtualization)

  const templateLabel = (id: string) => {
    const item = templates.find(candidate => candidate.id === id)
    return item ? imageLabel(item) : id
  }

  function toggleTemplate(id: string, checked: boolean) {
    const next = checked ? [...new Set([...allowed, id])] : allowed.filter(item => item !== id)
    setAllowed(next)
    if (!next.includes(defaultTemplate)) setDefaultTemplate(next[0] || '')
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!allowed.length || !defaultTemplate) {
      setError('请至少勾选一个可用操作系统模板并指定默认模板')
      return
    }
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    const body = {
      code: data.get('code'),
      name: data.get('name'),
      provider_type: providerType,
      virtualization,
      vcpu: Number(data.get('vcpu')),
      ram_mb: Number(data.get('ram_mb')),
      disk_gb: Number(data.get('disk_gb')),
      traffic_gb: Number(data.get('traffic_gb')),
      network_down_mbps: Number(data.get('network_down_mbps')),
      network_up_mbps: Number(data.get('network_up_mbps')),
      snapshot_limit: Number(data.get('snapshot_limit')),
      assign_nat: data.get('assign_nat') === 'on',
      port_mapping_count: Number(data.get('port_mapping_count')),
      assign_ipv4: data.get('assign_ipv4') === 'on',
      ipv4_count: Number(data.get('ipv4_count')),
      assign_ipv6: data.get('assign_ipv6') === 'on',
      ipv6_count: Number(data.get('ipv6_count')),
      default_template_id: defaultTemplate,
      allowed_template_ids: allowed,
      enabled: plan?.enabled ?? true,
      prices: [
        {
          currency: 'CNY',
          billing_cycle: data.get('billing_cycle'),
          amount_minor: Math.round(Number(data.get('price')) * 100),
          setup_fee_minor: 0,
        },
      ],
    }
    try {
      await api(plan ? `/api/v1/admin/plans/${plan.id}` : '/api/v1/admin/plans', {
        method: plan ? 'PUT' : 'POST',
        body: JSON.stringify(body),
      })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }

  const price = plan?.prices[0]

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{plan ? '编辑' : '创建'} VPS 商品套餐</h3>
          <p>套餐绑定一种对接方式，只会调度到该方式的节点；可用模板从这些在线节点读取，网络分配策略统一下发给实例。</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit}>
        <label>
          <span>套餐唯一编码</span>
          <input name="code" required placeholder="LXC-START" defaultValue={plan?.code} />
        </label>
        <label>
          <span>套餐展示名称</span>
          <input name="name" required placeholder="轻量入门型" defaultValue={plan?.name} />
        </label>
        <label>
          <span>对接方式</span>
          <select
            name="provider_type"
            value={providerType}
            disabled={!!plan}
            onChange={event => {
              const next = providers.find(item => item.type === event.target.value)
              setProviderType(event.target.value)
              setVirtualization((next?.virtualization_types[0] ?? 'lxc') as 'lxc' | 'kvm' | 'podman')
              setAllowed([])
              setDefaultTemplate('')
            }}
          >
            {providers.map(item => <option key={item.type} value={item.type}>{item.name}</option>)}
          </select>
        </label>
        <label>
          <span>底层虚拟化</span>
          <select
            name="virtualization"
            value={virtualization}
            onChange={event => {
              const value = event.target.value as 'lxc' | 'kvm' | 'podman'
              setVirtualization(value)
              setAllowed([])
              setDefaultTemplate('')
            }}
          >
            {virtualizations.map(kind => <option key={kind} value={kind}>{virtualizationLabel(kind)}</option>)}
          </select>
        </label>
        <label>
          <span>vCPU 核心</span>
          <input name="vcpu" type="number" min="1" defaultValue={plan?.vcpu || 1} required />
        </label>
        <label>
          <span>内存容量 MB</span>
          <input name="ram_mb" type="number" min="128" defaultValue={plan?.ram_mb || 512} required />
        </label>
        <label>
          <span>磁盘空间 GB</span>
          <input name="disk_gb" type="number" min="1" defaultValue={plan?.disk_gb || 10} required />
        </label>
        <label>
          <span>月度流量 GB</span>
          <input name="traffic_gb" type="number" min="0" defaultValue={plan?.traffic_gb ?? 1024} />
        </label>
        <label>
          <span>下行带宽 Mbps</span>
          <input name="network_down_mbps" type="number" min="0" defaultValue={plan?.network_down_mbps ?? 100} />
        </label>
        <label>
          <span>上行带宽 Mbps</span>
          <input name="network_up_mbps" type="number" min="0" defaultValue={plan?.network_up_mbps ?? 100} />
        </label>
        <label>
          <span>快照配额</span>
          <input name="snapshot_limit" type="number" min="0" defaultValue={plan?.snapshot_limit ?? 1} />
        </label>
        <label>
          <span>默认计费周期</span>
          <select name="billing_cycle" defaultValue={price?.billing_cycle || 'monthly'}>
            <option value="monthly">月付</option>
            <option value="quarterly">季付</option>
            <option value="semiannual">半年付</option>
            <option value="annual">年付</option>
          </select>
        </label>
        <label>
          <span>销售单价（元）</span>
          <input
            name="price"
            type="number"
            min="0.01"
            step="0.01"
            defaultValue={((price?.amount_minor || 1900) / 100).toFixed(2)}
            required
          />
        </label>

        <fieldset className="wide network-policy">
          <legend>网络策略配置</legend>
          <label className="checkbox">
            <input name="assign_nat" type="checkbox" defaultChecked={plan?.assign_nat ?? true} />
            分配 NAT 共享 IPv4
          </label>
          <label>
            <span>NAT 端口映射配额</span>
            <input
              name="port_mapping_count"
              type="number"
              min="0"
              max="64"
              defaultValue={plan?.port_mapping_count ?? 0}
            />
          </label>
          <label className="checkbox">
            <input name="assign_ipv4" type="checkbox" defaultChecked={plan?.assign_ipv4 ?? false} />
            分配独立公网 IPv4
          </label>
          <label>
            <span>公网 IPv4 数量</span>
            <input name="ipv4_count" type="number" min="1" max="64" defaultValue={plan?.ipv4_count ?? 1} />
          </label>
          <label className="checkbox">
            <input name="assign_ipv6" type="checkbox" defaultChecked={plan?.assign_ipv6 ?? true} />
            分配独立 IPv6
          </label>
          <label>
            <span>独立 IPv6 数量</span>
            <input name="ipv6_count" type="number" min="1" max="64" defaultValue={plan?.ipv6_count ?? 1} />
          </label>
        </fieldset>

        <fieldset className="wide">
          <legend>允许客户选择的系统镜像（动态读取自节点就绪镜像）</legend>
          {loadingTemplates ? (
            <div className="template-empty">正在向在线 CLICD 节点检索可用系统镜像…</div>
          ) : (
            <div className="template-picker">
              {visibleTemplates.map(item => (
                <label
                  className={allowed.includes(item.id) ? 'template-option selected' : 'template-option'}
                  key={`${item.virtualization}:${item.id}`}
                >
                  <input
                    type="checkbox"
                    checked={allowed.includes(item.id)}
                    onChange={event => toggleTemplate(item.id, event.target.checked)}
                  />
                  <span>
                    <strong>{templateLabel(item.id)}</strong>
                    <small>{item.description || item.id}</small>
                    <em>部署节点：{item.node_names.join('、')}</em>
                  </span>
                </label>
              ))}
              {!visibleTemplates.length && (
                <div className="template-empty">
                  在线集群中暂无已启用且已就绪的 {virtualization.toUpperCase()} 系统镜像。
                </div>
              )}
              {plan?.allowed_template_ids
                .filter(id => !visibleTemplates.some(item => item.id === id))
                .map(id => (
                  <label className="template-option unavailable" key={id}>
                    <input
                      type="checkbox"
                      checked={allowed.includes(id)}
                      onChange={event => toggleTemplate(id, event.target.checked)}
                    />
                    <span>
                      <strong>{id}</strong>
                      <small>该镜像当前节点未上报，保留后仍可保存供历史实例使用。</small>
                    </span>
                  </label>
                ))}
            </div>
          )}
        </fieldset>

        <label className="wide">
          <span>默认系统镜像</span>
          <select
            name="default_template_id"
            value={defaultTemplate}
            onChange={event => setDefaultTemplate(event.target.value)}
            required
          >
            <option value="">请先在上方勾选镜像</option>
            {allowed.map(id => (
              <option key={id} value={id}>
                {templateLabel(id)}
              </option>
            ))}
          </select>
        </label>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving}>
            {saving ? '正在保存…' : plan ? '保存修改' : '创建商品套餐'}
          </button>
        </div>
      </form>
    </div>
  )
}

function PageActions({
  eyebrow,
  title,
  description,
  action,
  actionLabel,
}: {
  eyebrow: string
  title: string
  description: string
  action: () => void
  actionLabel: string
}) {
  return (
    <div className="page-actions">
      <div>
        <p className="eyebrow">{eyebrow}</p>
        <h2>{title}</h2>
        <p>{description}</p>
      </div>
      <button className="primary-button compact" onClick={action}>
        <Plus size={16} />
        {actionLabel}
      </button>
    </div>
  )
}

function JobError({ value }: { value?: string }) {
  if (!value) return <span className="job-error-empty">—</span>
  if (value.length <= 100) return <span className="job-error-text">{value}</span>
  return (
    <details className="job-error-details">
      <summary>
        {value.slice(0, 100)}…<b>展开诊断详情</b>
      </summary>
      <pre>{value}</pre>
    </details>
  )
}

function StatusBadge({ status }: { status: string }) {
  const labels: Record<string, string> = {
    online: '在线',
    active: '正常',
    disabled: '已下架',
    offline: '离线',
    pending_payment: '待支付',
    open: '待支付',
    paid: '已支付',
    fulfilling: '开通中',
    provisioning: '开通中',
    completed: '已完成',
    succeeded: '成功',
    failed: '等待重试',
    pending: '排队中',
    running: '运行中',
    creating: '创建中',
    stopped: '已关机',
    overdue: '已逾期',
    suspended: '已暂停',
    terminating: '待删除',
    terminated: '已删除',
    review: '需人工审核',
    uncollectible: '无法收回',
    missing: '实例缺失',
    unknown: '未知',
    error: '异常',
    dead: '需人工处理',
  }
  return <span className={`status-badge ${status}`}>{labels[status] ?? status}</span>
}

function cycleLabel(cycle: string) {
  return (
    ({
      monthly: '月',
      quarterly: '季',
      semiannual: '半年',
      annual: '年',
    } as Record<string, string>)[cycle] ?? cycle
  )
}

function ticketStatusLabel(status: string) {
  return (
    ({
      open: '待处理',
      customer_reply: '客户已回复',
      staff_reply: '客服已回复',
      resolved: '已解决',
      closed: '已关闭',
    } as Record<string, string>)[status] || status
  )
}

function ticketAuthorLabel(type: string) {
  return (
    ({
      customer: '客户',
      staff: '工作人员',
      system: '系统通知',
    } as Record<string, string>)[type] || type
  )
}

function viewTitle(view: View) {
  return (
    ({
      overview: '运营概览',
      customers: '客户管理',
      orders: '销售订单',
      billing: '账单与财务流水',
      payment: '支付网关配置',
      services: 'VPS 服务与任务队列',
      nodes: '虚拟化节点对接',
      hosts: '宿主机探针',
      plans: '商品套餐管理',
      support: '工单管理',
      audit: '安全审计日志',
      settings: '站点设置',
      security: '账户安全设置',
    } as Record<View, string>)[view]
  )
}

function money(amountMinor: number, currency: string) {
  return new Intl.NumberFormat('zh-CN', { style: 'currency', currency }).format(amountMinor / 100)
}
