import { FormEvent, useEffect, useState } from 'react'
import { ArrowLeftRight, Boxes, ChevronRight, Coins, Contact as ContactIcon, Headphones, LayoutDashboard, LogOut, Menu, X, ShieldCheck, ShoppingCart, Store, UserCircle, WalletCards } from 'lucide-react'
import { adoptCache, api, clearCached, CustomerIdentity } from '../api'
import { prefetchPage, usePrefetch } from '../shared/prefetch'
import CustomerServicesPanel from '../CustomerServices'
import CustomerWallet from '../Wallet'
import HostingCenter from '../Hosting'
import TradeMarket from '../Trade'
import { ThemeToggle } from '../ThemeToggle'
import { EmailVerifyBanner } from '../EmailVerify'
import { Meta, SessionLoading, Field, Brand, BrandMark } from '../shared/ui'
import { Boot, freshBoot, inlineBoot, loadBoot } from '../shared/boot'
import { CustomerAnnouncements, CustomerOverview } from './Overview'
import { navigatePortal } from '../shared/nav'
import { CustomerShop } from './Shop'
import { CustomerBilling } from './Billing'
import { CustomerProfile } from './Profile'
import { CustomerSupport } from './Support'
import { CustomerContact } from './Contact'

export type CustomerAuthScreen = 'loading' | 'install' | 'login' | 'register' | 'forgot' | 'reset' | 'ready' | 'uninstalled'

function portalScreen(boot: Boot): CustomerAuthScreen {
  if (boot.install_required) return boot.meta.surface === 'portal' ? 'uninstalled' : 'install'
  // Reset links open the reset form even when a session exists.
  if (window.location.pathname === '/portal/reset-password') return 'reset'
  return boot.customer ? 'ready' : 'login'
}

export type PortalView = 'overview' | 'shop' | 'services' | 'billing' | 'wallet' | 'hosting' | 'trade' | 'support' | 'profile' | 'announcements' | 'contact'

export const portalViews: PortalView[] = ['overview', 'shop', 'services', 'billing', 'wallet', 'hosting', 'trade', 'support', 'profile', 'announcements', 'contact']

// portalPageData is what each page loads first, for prefetching.
const portalPageData: Record<string, string[]> = {
  overview: ['/api/v1/customer/services', '/api/v1/customer/invoices', '/api/v1/customer/overview'],
  shop: ['/api/v1/customer/catalog'],
  services: ['/api/v1/customer/services'],
  billing: ['/api/v1/customer/invoices', '/api/v1/customer/transactions', '/api/v1/customer/orders', '/api/v1/customer/catalog', '/api/v1/customer/wallet'],
  wallet: ['/api/v1/customer/wallet', '/api/v1/customer/catalog'],
  hosting: ['/api/v1/customer/market', '/api/v1/customer/hosting'],
  trade: ['/api/v1/customer/trade', '/api/v1/customer/wallet'],
  support: ['/api/v1/customer/tickets', '/api/v1/customer/services'],
  announcements: ['/api/v1/customer/announcements'],
}

export function portalViewFromPath(): PortalView {
  const candidate = window.location.pathname.split('/').filter(Boolean)[1] as PortalView
  return portalViews.includes(candidate) ? candidate : 'overview'
}

export function CustomerPortalApp() {
  // The boot data usually comes inline with the page, so the first render
  // already knows who is signed in.
  const [screen, setScreen] = useState<CustomerAuthScreen>(() => {
    const boot = inlineBoot()
    return boot ? portalScreen(boot) : 'loading'
  })
  const [customer, setCustomer] = useState<CustomerIdentity | null>(() => inlineBoot()?.customer ?? null)
  const [meta, setMeta] = useState<Meta | null>(() => inlineBoot()?.meta ?? null)

  useEffect(() => {
    if (screen === 'install') {
      // The installer runs on the admin console, which may live on another
      // port or domain than the portal.
      window.location.replace('/admin')
      return
    }
    if (inlineBoot()) return
    loadBoot()
      .then(boot => {
        setMeta(boot.meta)
        setCustomer(boot.customer)
        setScreen(portalScreen(boot))
      })
      .catch(() => setScreen('login'))
  }, [screen])

  useEffect(() => {
    const inline = inlineBoot()
    if (!inline) return
    // A stored copy of the page (service worker): correct it if the
    // session changed since.
    void freshBoot()
      .then(boot => {
        if (!boot) return
        setMeta(boot.meta)
        if ((boot.customer?.id ?? '') !== (inline.customer?.id ?? '') || boot.install_required !== inline.install_required) {
          clearCached()
          setCustomer(boot.customer)
          setScreen(portalScreen(boot))
        }
      })
      .catch(() => undefined)
  }, [])

  if (screen === 'loading' || screen === 'install') return <SessionLoading portal="customer" />
  if (screen === 'uninstalled') {
    return (
      <main className="session-loading">
        <BrandMark />
        <strong>站点尚未完成安装。请打开管理后台地址（默认是服务器的后台端口）完成 Web 安装向导。</strong>
      </main>
    )
  }
  if (screen !== 'ready' || !customer) {
    return (
      <CustomerAuthPage
        mode={screen}
        meta={meta}
        onMode={setScreen}
        onAuthenticated={value => {
          clearCached()
          adoptCache(value.id)
          setCustomer(value)
          setScreen('ready')
        }}
      />
    )
  }

  return (
    <CustomerShell
      customer={customer}
      siteName={meta?.name}
      onLogout={async () => {
        await api('/api/v1/customer/auth/logout', { method: 'POST' })
        clearCached()
        setCustomer(null)
        setScreen('login')
      }}
    />
  )
}

export function CustomerAuthPage({
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
        <Brand className="auth-brand" name={siteName} subtitle="客户服务中心" />
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

export function CustomerShell({ customer, siteName, onLogout }: { customer: CustomerIdentity; siteName?: string; onLogout: () => void }) {
  const [view, setView] = useState<PortalView>(portalViewFromPath)
  const [menuOpen, setMenuOpen] = useState(false)
  usePrefetch(portalPageData)

  useEffect(() => {
    // Pages may add a segment, e.g. /portal/services/<id>.
    const path = window.location.pathname
    if (path !== `/portal/${view}` && !path.startsWith(`/portal/${view}/`)) {
      window.history.replaceState(null, '', `/portal/${view}${view === 'hosting' ? window.location.search : ''}`)
    }
    const pop = () => setView(portalViewFromPath())
    window.addEventListener('popstate', pop)
    return () => window.removeEventListener('popstate', pop)
  }, [view])

  // Choosing the current page again leaves any item on it (e.g. a service
  // detail) for the page itself.
  const navigate = (next: PortalView) => {
    setMenuOpen(false)
    navigatePortal(`/portal/${next}`)
  }

  const items: [PortalView, string, typeof LayoutDashboard][] = [
    ['overview', '服务概览', LayoutDashboard],
    ['shop', '选购 VPS', ShoppingCart],
    ['services', '我的 VPS', Boxes],
    ['billing', '订单与账单', WalletCards],
    ['wallet', '账户余额', Coins],
    ['hosting', '托管中心', Store],
    ['trade', '交易市场', ArrowLeftRight],
    ['support', '支持工单', Headphones],
    ['profile', '账户资料', UserCircle],
  ]

  const titles: Record<PortalView, string> = {
    overview: '服务概览',
    shop: '选购 VPS',
    services: '我的 VPS',
    billing: '订单与账单',
    wallet: '账户余额',
    hosting: '托管中心',
    trade: '交易市场',
    support: '支持工单',
    profile: '账户资料',
    announcements: '平台公告',
    contact: '联系我们',
  }

  return (
    <div className="app-shell customer-shell">
      <aside className={menuOpen ? 'sidebar menu-open' : 'sidebar'}>
        <Brand name={siteName || '客户中心'} subtitle="客户中心" />
        <button type="button" className="menu-toggle" aria-label={menuOpen ? '关闭菜单' : '打开菜单'} aria-expanded={menuOpen} onClick={() => setMenuOpen(open => !open)}>
          {menuOpen ? <X size={20} /> : <Menu size={20} />}
          <span>{titles[view]}</span>
        </button>
        <nav aria-label="客户导航">
          {items.map(([id, label, Icon]) => (
            <button
              key={id}
              className={id === view ? 'nav-item active' : 'nav-item'}
              onClick={() => navigate(id)}
              onPointerEnter={() => prefetchPage(portalPageData, id)}
            >
              <Icon size={18} />
              <span>{label}</span>
              {id === view && <ChevronRight size={16} />}
            </button>
          ))}
        </nav>
        {/* Contact sits at the foot of the menu, above the account. */}
        <nav className="sidebar-foot" aria-label="联系我们">
          <button className={view === 'contact' ? 'nav-item active' : 'nav-item'} onClick={() => navigate('contact')}>
            <ContactIcon size={18} />
            <span>联系我们</span>
            {view === 'contact' && <ChevronRight size={16} />}
          </button>
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
            <ThemeToggle />
            <span>{customer.email}</span>
            <div className="avatar">{customer.display_name.slice(0, 1)}</div>
            <button className="icon-button" aria-label="退出登录" onClick={onLogout} title="退出登录">
              <LogOut size={16} />
            </button>
          </div>
        </header>

        {!customer.email_verified && <EmailVerifyBanner email={customer.email} />}
        {view === 'overview' && <CustomerOverview customer={customer} />}
        {view === 'announcements' && <CustomerAnnouncements />}
        {view === 'shop' && <CustomerShop customer={customer} />}
        {view === 'services' && <CustomerServicesPanel />}
        {view === 'billing' && <CustomerBilling />}
        {view === 'wallet' && <CustomerWallet />}
        {view === 'hosting' && <HostingCenter customer={customer} />}
        {view === 'trade' && <TradeMarket />}
        {view === 'support' && <CustomerSupport />}
        {view === 'profile' && <CustomerProfile customer={customer} />}
        {view === 'contact' && <CustomerContact />}
      </main>
    </div>
  )
}
