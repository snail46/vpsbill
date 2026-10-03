import { FormEvent, Fragment, useEffect, useMemo, useState } from 'react'
import { ArrowLeftRight, Boxes, DatabaseBackup, CheckCircle2, ChevronRight, CircleDollarSign, Cpu, CreditCard, Headphones, LayoutDashboard, Megaphone, LogOut, Menu, X, PackageOpen, ReceiptText, ScrollText, ServerCog, Settings, SlidersHorizontal, ShieldCheck, Store, UserCog, Users } from 'lucide-react'
import { adoptCache, api, clearCached, StaffUser } from '../api'
import { prefetchPage, usePrefetch } from '../shared/prefetch'
import HostDetailPanel from '../HostDetail'
import AdminMarketplace from '../AdminMarketplace'
import { AdminTradeListings } from '../Trade'
import { ThemeToggle } from '../ThemeToggle'
import { LocaleMenu } from '../shared/LocaleMenu'
import { Meta, SessionLoading, Field, SecuritySettings, Brand, BrandMark } from '../shared/ui'
import { StaffTelegram } from './TelegramCommunity'
import { Boot, freshBoot, inlineBoot, loadBoot } from '../shared/boot'
import { Overview } from './Overview'
import { CustomersView } from './Customers'
import { OrdersView, BillingView } from './Orders'
import { PaymentSettingsView, SiteSettingsView } from './Settings'
import { BackupsView } from './Backups'
import { ServicesView } from './Services'
import { NodesView, HostsView } from './Nodes'
import { PlansView } from './Plans'
import { AdminSupport, AuditView } from './Support'
import { AnnouncementsView } from './Announcements'
import { StaffView, can, roleName } from './Staff'
import { t } from '../shared/i18n'

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
  | 'backups'
  | 'staff'
  | 'security'

export type AuthScreen = 'loading' | 'install' | 'login' | 'ready'

export const navItems: Array<{ id: View; label: string; icon: typeof LayoutDashboard }> = [
  { id: 'overview', label: t('运营概览'), icon: LayoutDashboard },
  { id: 'customers', label: t('客户管理'), icon: Users },
  { id: 'orders', label: t('销售订单'), icon: ReceiptText },
  { id: 'billing', label: t('账单与交易'), icon: CircleDollarSign },
  { id: 'payment', label: t('支付网关'), icon: CreditCard },
  { id: 'services', label: t('VPS 服务'), icon: Boxes },
  { id: 'plans', label: t('商品套餐'), icon: PackageOpen },
  { id: 'nodes', label: t('节点对接'), icon: ServerCog },
  { id: 'hosts', label: t('宿主机探针'), icon: Cpu },
  { id: 'support', label: t('客户工单'), icon: Headphones },
  { id: 'marketplace', label: t('托管管理'), icon: Store },
  { id: 'trade', label: t('交易市场'), icon: ArrowLeftRight },
  { id: 'audit', label: t('审计日志'), icon: ScrollText },
  { id: 'announcements', label: t('平台公告'), icon: Megaphone },
  { id: 'settings', label: t('站点设置'), icon: SlidersHorizontal },
  { id: 'backups', label: t('数据备份'), icon: DatabaseBackup },
  { id: 'staff', label: t('管理员与角色'), icon: UserCog },
  { id: 'security', label: t('安全中心'), icon: Settings },
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
  'backups',
  'staff',
  'security',
]

// viewPermissions is what a role needs to open each page: everything the
// page loads. Pages a role cannot open are left out of its menu.
const viewPermissions: Record<View, string[]> = {
  overview: ['customers:read'],
  customers: ['customers:read'],
  orders: ['orders:read', 'customers:read', 'plans:read', 'nodes:read'],
  billing: ['billing:read'],
  payment: ['settings:read'],
  services: ['services:read'],
  plans: ['plans:read', 'nodes:read'],
  nodes: ['nodes:read'],
  hosts: ['nodes:read'],
  support: ['tickets:read'],
  marketplace: ['nodes:read', 'tickets:read'],
  trade: ['services:read'],
  audit: ['audit:read'],
  announcements: ['settings:read'],
  settings: ['settings:read'],
  backups: ['backups:manage'],
  staff: ['staff:manage'],
  security: [],
}

export function canOpen(user: StaffUser, view: View) {
  return can(user, ...viewPermissions[view])
}

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
  backups: ['/api/v1/admin/backups'],
  staff: ['/api/v1/admin/staff'],
}

// An admin route is a page, with a host for the probe detail and the page's
// filters as a query string (see admin/filters.tsx).
export type AdminRoute = { view: View; hostID?: string; query?: string; stamp?: number }

export function adminRouteFromPath(): AdminRoute {
  const parts = window.location.pathname.split('/').filter(Boolean)
  if (parts[0] === 'admin' && parts[1] === 'hosts' && parts[2]) {
    return { view: 'hosts', hostID: decodeURIComponent(parts[2]) }
  }
  const candidate = (parts[0] === 'admin' ? parts[1] : undefined) as View
  if (!adminViews.includes(candidate)) return { view: 'overview' }
  return { view: candidate, query: window.location.search.replace(/^\?/, '') }
}

export function adminRoutePath(route: AdminRoute) {
  if (route.hostID) return `/admin/hosts/${encodeURIComponent(route.hostID)}`
  return `/admin/${route.view}${route.query ? `?${route.query}` : ''}`
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
    const inline = inlineBoot()
    if (inline) {
      // A stored copy of the page (service worker): correct it if the
      // session changed since.
      void freshBoot()
        .then(boot => {
          if (!boot) return
          setMeta(boot.meta)
          if ((boot.staff?.id ?? '') !== (inline.staff?.id ?? '') || boot.install_required !== inline.install_required) {
            clearCached()
            setUser(boot.staff)
            setAuthScreen(adminScreen(boot))
          }
        })
        .catch(() => undefined)
      return
    }
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
      setError(requestError instanceof Error ? requestError.message : t('登录失败'))
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="auth-page">
      <div className="auth-theme"><LocaleMenu /><ThemeToggle /></div>
      <section className="auth-brand-panel">
        <Brand className="auth-brand" name={appName} subtitle={t('商家控制中心')} />
        <div>
          <p className="eyebrow">OPERATIONS CONSOLE</p>
          <h1>{t('自营销售、机主托管和交易市场，在一处运营。')}</h1>
          <p>{t('套餐与库存、节点与宿主机、订单账单与余额、工单与聊天室、托管清退与举报，都从这里处理。')}</p>
          <ul className="auth-points">
            <li><ServerCog size={16} />{t('对接 Hatch Agent、CLICD 和 LXD API 节点，开通、续费和到期回收自动执行')}</li>
            <li><CreditCard size={16} />{t('支付网关、邮件与 Telegram 通知、中英文界面，人民币或美元记账')}</li>
            <li><UserCog size={16} />{t('六级管理员角色，各司其职，操作写入审计日志')}</li>
          </ul>
        </div>
        <div className="auth-proof">
          <ShieldCheck size={18} />
          <span>{t('Argon2id · 二步验证 · CSRF 防护 · AES-256-GCM 凭据加密')}</span>
        </div>
      </section>
      <section className="auth-form-panel">
        <form className="auth-form" onSubmit={submit}>
          <p className="eyebrow">ADMIN ACCESS</p>
          <h2>{t('登录商家控制中心')}</h2>
          <p>{t('请输入管理员账号凭证以继续管理系统。')}</p>
          <Field label={t('管理员邮箱')} value={email} onChange={setEmail} type="email" autoComplete="email" />
          <Field label={t('登录密码')} value={password} onChange={setPassword} type="password" autoComplete="current-password" />
          <Field
            label={t('二步验证码（已启用时填写）')}
            value={totpCode}
            onChange={setTotpCode}
            autoComplete="one-time-code"
            required={false}
          />
          {error && <div className="form-error" role="alert">{error}</div>}
          <button className="primary-button" style={{ width: '100%', marginTop: '6px' }} disabled={submitting || mode === 'loading'}>
            {mode === 'loading' ? t('检查系统状态…') : submitting ? t('正在验证…') : t('登录控制中心')}
          </button>
          <a className="auth-switch" href={portalURL}>
            <Users size={15} />{t('切换至客户中心')}
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
      setError(requestError instanceof Error ? requestError.message : t('安装失败'))
    } finally {
      setSubmitting(false)
    }
  }

  if (result) {
    return (
      <main className="installer-page">
        <section className="installer-card installer-complete">
          <Brand name={form.app_name} subtitle={t('首次初始化已完成')} />
          <CheckCircle2 size={48} />
          <h1>{t('系统初始化成功')}</h1>
          <p>{t('数据库结构、基础参数与超级管理员已原子写入。以下自动生成的安全密钥仅显示一次，请妥善保存至密码管理器。')}</p>
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
            {t('进入商家控制中心')}
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
            <h1>{t('初始化 VPSBill')}</h1>
            <p>{t('系统基础环境与数据表已就绪。在此配置站点基础参数与初始超级管理员账号。')}</p>
          </div>
          <div className="installer-step">
            {t('原子写入')}
            <br />
            <strong>{t('事务安装向导')}</strong>
          </div>
        </header>

        <section className="installer-section">
          <h3>{t('1. 站点基础设置')}</h3>
          <div className="installer-grid">
            <Field label={t('站点名称')} value={form.app_name} onChange={update('app_name')} />
            <Field label={t('公开访问域名')} value={form.public_url} onChange={update('public_url')} type="url" hint={t('用于外部支付与回调')} />
          </div>
        </section>

        <section className="installer-section">
          <h3>{t('2. 事件通知与指标监控')}</h3>
          <div className="installer-grid">
            <Field label={t('通知 Webhook 地址（可选）')} value={form.notification_webhook_url} onChange={update('notification_webhook_url')} type="url" required={false} />
            <Field label={t('通知签名密钥（留空自动生成）')} value={form.notification_webhook_secret} onChange={update('notification_webhook_secret')} type="password" required={false} />
            <Field label={t('Prometheus Token（留空自动生成）')} value={form.metrics_token} onChange={update('metrics_token')} type="password" required={false} />
          </div>
        </section>

        <details className="installer-section">
          <summary>{t('3. 自动化调度与账期参数（默认已针对生产调优）')}</summary>
          <div className="installer-grid advanced-grid">
            <Field label={t('任务队列轮询')} value={form.worker_poll_interval} onChange={update('worker_poll_interval')} />
            <Field label={t('节点状态对账')} value={form.reconcile_interval} onChange={update('reconcile_interval')} />
            <Field label={t('账务生命周期扫描')} value={form.lifecycle_interval} onChange={update('lifecycle_interval')} />
            <Field label={t('提前续费账单生成')} value={form.renewal_lead_time} onChange={update('renewal_lead_time')} />
            <Field label={t('逾期关机宽限期')} value={form.overdue_grace_period} onChange={update('overdue_grace_period')} />
            <Field label={t('保留数据终止期')} value={form.termination_retention} onChange={update('termination_retention')} />
          </div>
        </details>

        <section className="installer-section">
          <h3>{t('4. 初始超级管理员账号')}</h3>
          <div className="installer-grid">
            <Field label={t('管理员姓名')} value={form.admin_display_name} onChange={update('admin_display_name')} autoComplete="name" />
            <Field label={t('管理员邮箱')} value={form.admin_email} onChange={update('admin_email')} type="email" autoComplete="email" />
            <Field label={t('管理员密码')} value={form.admin_password} onChange={update('admin_password')} type="password" autoComplete="new-password" hint={t('至少 12 个字符')} />
          </div>
        </section>

        {error && <div className="form-error" role="alert">{error}</div>}

        <footer className="installer-footer">
          <span>
            <ShieldCheck size={16} />{t('敏感配置将使用 AES-256-GCM 硬件加密持久化')}
          </span>
          <button className="primary-button compact" disabled={submitting}>
            {submitting ? t('正在初始化系统…') : t('完成初始化安装')}
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
  const menu = useMemo(() => navItems.filter(item => canOpen(user, item.id)), [user])
  // Only what the role may read is loaded ahead.
  const pageData = useMemo(() => Object.fromEntries(Object.entries(adminPageData).filter(([page]) => canOpen(user, page as View))), [user])
  usePrefetch(pageData)

  useEffect(() => {
    const initial = adminRouteFromPath()
    const canonical = adminRoutePath(initial)
    if (window.location.pathname + window.location.search !== canonical) {
      window.history.replaceState(null, '', canonical)
    }
    // Each navigation starts the page afresh (stamp), even to the same
    // address: its filters may have changed the address since.
    const pop = () => setRoute({ ...adminRouteFromPath(), stamp: Date.now() })
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [])

  const navigate = (next: AdminRoute) => {
    setMenuOpen(false)
    const path = adminRoutePath(next)
    if (window.location.pathname + window.location.search === path) return
    window.history.pushState(null, '', path)
    setRoute({ ...next, stamp: Date.now() })
  }

  return (
    <div className="app-shell">
      <aside className={menuOpen ? 'sidebar menu-open' : 'sidebar'}>
        <Brand name={meta?.name ?? 'VPSBill'} subtitle={t('商家控制中心')} />
        <button type="button" className="menu-toggle" aria-label={menuOpen ? t('关闭菜单') : t('打开菜单')} aria-expanded={menuOpen} onClick={() => setMenuOpen(open => !open)}>
          {menuOpen ? <X size={20} /> : <Menu size={20} />}
          <span>{navItems.find(item => item.id === view)?.label ?? t('菜单')}</span>
        </button>
        <nav aria-label={t('主导航')}>
          {menu.map(({ id, label, icon: Icon }) => (
            <button
              className={id === view ? 'nav-item active' : 'nav-item'}
              key={label}
              type="button"
              onClick={() => navigate({ view: id })}
              onPointerEnter={() => prefetchPage(pageData, id)}
            >
              <Icon size={18} aria-hidden="true" />
              <span>{label}</span>
              {id === view && <ChevronRight size={16} aria-hidden="true" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-status sidebar-user" title={`${user.display_name} · ${roleName(user.role)}\n${user.email}`}>
          <div className="avatar" aria-hidden="true">{user.display_name.slice(0, 1)}</div>
          <div>
            <strong>{user.display_name}<span className="tag">{roleName(user.role)}</span></strong>
            <span>{user.email}</span>
          </div>
        </div>
      </aside>

      <main>
        <header className="topbar">
          <div>
            <p className="eyebrow">ADMIN CONTROL PLANE</p>
            <h1>{route.hostID ? t('宿主机探针详情') : viewTitle(view)}</h1>
          </div>
          <div className="operator">
            <LocaleMenu />
            <ThemeToggle />
            <span>{user.display_name}</span>
            <div className="avatar">{user.display_name.slice(0, 1)}</div>
            <button className="icon-button" aria-label={t('退出登录')} onClick={onLogout} title={t('退出登录')}>
              <LogOut size={16} />
            </button>
          </div>
        </header>

        {/* A link that opens a page with other filters starts it afresh. */}
        <Fragment key={`${view}?${route.query ?? ''}#${route.stamp ?? 0}`}>
        {!canOpen(user, view) ? (
          <section className="workspace-panel">
            <div className="note-banner warn">
              {t('当前角色（{0}）没有查看「{1}」的权限。需要时请联系超级管理员调整角色。', roleName(user.role), viewTitle(view))}
            </div>
          </section>
        ) : (
        <>
        {view === 'overview' && <Overview />}
        {view === 'customers' && <CustomersView />}
        {view === 'orders' && <OrdersView />}
        {view === 'billing' && <BillingView />}
        {view === 'payment' && <PaymentSettingsView />}
        {view === 'settings' && <SiteSettingsView />}
        {view === 'backups' && <BackupsView />}
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
        {view === 'staff' && <StaffView user={user} />}
        {view === 'security' && (
          <>
            <SecuritySettings enabled={user.mfa_enabled} />
            <StaffTelegram />
          </>
        )}
        </>
        )}
        </Fragment>
      </main>
    </div>
  )
}

export function viewTitle(view: View) {
  return (
    ({
      overview: t('运营概览'),
      customers: t('客户管理'),
      orders: t('销售订单'),
      billing: t('账单与财务流水'),
      payment: t('支付网关配置'),
      services: t('VPS 服务与任务队列'),
      nodes: t('虚拟化节点对接'),
      hosts: t('宿主机探针'),
      plans: t('商品套餐管理'),
      support: t('工单管理'),
      marketplace: t('托管管理'),
      trade: t('交易市场'),
      audit: t('安全审计日志'),
      announcements: t('平台公告'),
      settings: t('站点设置'),
      backups: t('数据备份与还原'),
      staff: t('管理员与角色'),
      security: t('账户安全设置'),
    } as Record<View, string>)[view]
  )
}
