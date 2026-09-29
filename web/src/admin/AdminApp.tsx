import { FormEvent, useEffect, useState } from 'react'
import { ArrowLeftRight, Boxes, CheckCircle2, ChevronRight, CircleDollarSign, Cpu, CreditCard, Headphones, LayoutDashboard, Megaphone, LogOut, Menu, X, PackageOpen, ReceiptText, ScrollText, ServerCog, Settings, SlidersHorizontal, ShieldCheck, Store, Users } from 'lucide-react'
import { adoptCache, api, clearCached, StaffUser } from '../api'
import { prefetchPage, usePrefetch } from '../shared/prefetch'
import HostDetailPanel from '../HostDetail'
import AdminMarketplace from '../AdminMarketplace'
import { AdminTradeListings } from '../Trade'
import { ThemeToggle } from '../ThemeToggle'
import { Meta, SessionLoading, Field, SecuritySettings, BrandMark } from '../shared/ui'
import { Boot, inlineBoot, loadBoot } from '../shared/boot'
import { Overview } from './Overview'
import { CustomersView } from './Customers'
import { OrdersView, BillingView } from './Orders'
import { PaymentSettingsView, SiteSettingsView } from './Settings'
import { ServicesView } from './Services'
import { NodesView, HostsView } from './Nodes'
import { PlansView } from './Plans'
import { AdminSupport, AuditView } from './Support'
import { AnnouncementsView } from './Announcements'

export type View =
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
  | 'marketplace'
  | 'trade'
  | 'audit'
  | 'announcements'
  | 'settings'
  | 'security'

export type AuthScreen = 'loading' | 'install' | 'login' | 'ready'

export const navItems: Array<{ id: View; label: string; icon: typeof LayoutDashboard }> = [
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
  { id: 'marketplace', label: '托管管理', icon: Store },
  { id: 'trade', label: '交易市场', icon: ArrowLeftRight },
  { id: 'audit', label: '审计日志', icon: ScrollText },
  { id: 'announcements', label: '平台公告', icon: Megaphone },
  { id: 'settings', label: '站点设置', icon: SlidersHorizontal },
  { id: 'security', label: '安全中心', icon: Settings },
]

export const adminViews: View[] = [
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
  'marketplace',
  'trade',
  'audit',
  'announcements',
  'settings',
  'security',
]

// adminPageData is what each page loads first, for prefetching.
const adminPageData: Record<string, string[]> = {
  overview: ['/api/v1/admin/overview'],
  customers: ['/api/v1/admin/customers'],
  orders: ['/api/v1/admin/orders', '/api/v1/admin/customers', '/api/v1/admin/plans', '/api/v1/admin/regions'],
  billing: ['/api/v1/admin/invoices', '/api/v1/admin/transactions'],
  payment: ['/api/v1/admin/settings/payment'],
  services: ['/api/v1/admin/services', '/api/v1/admin/jobs'],
  plans: ['/api/v1/admin/plans', '/api/v1/admin/provider-types'],
  nodes: ['/api/v1/admin/nodes', '/api/v1/admin/agent-enrollments', '/api/v1/admin/regions/all', '/api/v1/admin/overcommit-limits'],
  hosts: ['/api/v1/admin/hosts'],
  support: ['/api/v1/admin/tickets'],
  marketplace: ['/api/v1/admin/marketplace/nodes'],
  trade: ['/api/v1/admin/trade/listings'],
  audit: ['/api/v1/admin/audit-logs'],
  announcements: ['/api/v1/admin/announcements'],
  settings: ['/api/v1/admin/settings/site'],
}

export type AdminRoute = { view: View; hostID?: string }

export function adminRouteFromPath(): AdminRoute {
  const parts = window.location.pathname.split('/').filter(Boolean)
  if (parts[0] === 'admin' && parts[1] === 'hosts' && parts[2]) {
    return { view: 'hosts', hostID: decodeURIComponent(parts[2]) }
  }
  const candidate = (parts[0] === 'admin' ? parts[1] : undefined) as View
  return { view: adminViews.includes(candidate) ? candidate : 'overview' }
}

export function adminRoutePath(route: AdminRoute) {
  return route.hostID ? `/admin/hosts/${encodeURIComponent(route.hostID)}` : `/admin/${route.view}`
}

export function AdminApp() {
  // The boot data usually comes inline with the page, so the first render
  // already knows who is signed in.
  const [meta, setMeta] = useState<Meta | null>(() => inlineBoot()?.meta ?? null)
  const [authScreen, setAuthScreen] = useState<AuthScreen>(() => {
    const boot = inlineBoot()
    return boot ? adminScreen(boot) : 'loading'
  })
  const [user, setUser] = useState<StaffUser | null>(() => inlineBoot()?.staff ?? null)

  useEffect(() => {
    if (inlineBoot()) return
    loadBoot()
      .then(boot => {
        setMeta(boot.meta)
        setUser(boot.staff)
        setAuthScreen(adminScreen(boot))
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
        portalURL={meta?.surface === 'admin' && meta.public_url ? meta.public_url : '/portal'}
        mode={authScreen}
        onAuthenticated={current => {
          clearCached()
          adoptCache(current.id)
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
        clearCached()
        setUser(null)
        setAuthScreen('login')
      }}
    />
  )
}

function adminScreen(boot: Boot): AuthScreen {
  if (boot.install_required) return 'install'
  return boot.staff ? 'ready' : 'login'
}

export function AuthPage({
  appName,
  portalURL,
  mode,
  onAuthenticated,
}: {
  appName: string
  // portalURL is the customer portal, which may have its own address.
  portalURL: string
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
      <div className="auth-theme"><ThemeToggle /></div>
      <section className="auth-brand-panel">
        <div className="brand auth-brand">
          <BrandMark />
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
          <a className="auth-switch" href={portalURL}>
            <Users size={15} />切换至客户中心
          </a>
        </form>
      </section>
    </main>
  )
}

export type InstallResponse = { user: StaffUser; generated_secrets: Record<string, string> }

export function InstallPage({ onInstalled }: { onInstalled: (user: StaffUser, appName: string) => void }) {
  const [form, setForm] = useState({
    app_name: 'VPSBill',
    public_url: window.location.origin,
    timezone: 'Asia/Shanghai',
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
            <BrandMark />
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

export function AdminShell({ meta, user, onLogout }: { meta: Meta | null; user: StaffUser; onLogout: () => void }) {
  const [route, setRoute] = useState<AdminRoute>(adminRouteFromPath)
  const [menuOpen, setMenuOpen] = useState(false)
  const view = route.view
  usePrefetch(adminPageData)

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
    setMenuOpen(false)
    const path = adminRoutePath(next)
    if (window.location.pathname === path) return
    window.history.pushState(null, '', path)
    setRoute(next)
  }

  return (
    <div className="app-shell">
      <aside className={menuOpen ? 'sidebar menu-open' : 'sidebar'}>
        <div className="brand">
          <BrandMark />
          <div>
            <strong>{meta?.name ?? 'VPSBill'}</strong>
            <span>商家控制中心</span>
          </div>
        </div>
        <button type="button" className="menu-toggle" aria-label={menuOpen ? '关闭菜单' : '打开菜单'} aria-expanded={menuOpen} onClick={() => setMenuOpen(open => !open)}>
          {menuOpen ? <X size={20} /> : <Menu size={20} />}
          <span>{navItems.find(item => item.id === view)?.label ?? '菜单'}</span>
        </button>
        <nav aria-label="主导航">
          {navItems.map(({ id, label, icon: Icon }) => (
            <button
              className={id === view ? 'nav-item active' : 'nav-item'}
              key={label}
              type="button"
              onClick={() => navigate({ view: id })}
              onPointerEnter={() => prefetchPage(adminPageData, id)}
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
            <ThemeToggle />
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
        {view === 'marketplace' && <AdminMarketplace />}
        {view === 'trade' && <section className="workspace-panel"><AdminTradeListings /></section>}
        {view === 'audit' && <AuditView />}
        {view === 'announcements' && <AnnouncementsView />}
        {view === 'security' && <SecuritySettings enabled={user.mfa_enabled} />}
      </main>
    </div>
  )
}

export function viewTitle(view: View) {
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
      marketplace: '托管管理',
      trade: '交易市场',
      audit: '安全审计日志',
      announcements: '平台公告',
      settings: '站点设置',
      security: '账户安全设置',
    } as Record<View, string>)[view]
  )
}
