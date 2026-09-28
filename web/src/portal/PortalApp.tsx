import { FormEvent, useEffect, useState } from 'react'
import { ArrowLeftRight, Boxes, ChevronRight, Coins, Headphones, LayoutDashboard, LogOut, ShieldCheck, ShoppingCart, Store, UserCircle, WalletCards } from 'lucide-react'
import { api, CustomerIdentity } from '../api'
import CustomerServicesPanel from '../CustomerServices'
import CustomerWallet from '../Wallet'
import HostingCenter from '../Hosting'
import TradeMarket from '../Trade'
import { ThemeToggle } from '../ThemeToggle'
import { EmailVerifyBanner } from '../EmailVerify'
import { Meta, SessionLoading, Field } from '../shared/ui'
import { CustomerOverview } from './Overview'
import { CustomerShop } from './Shop'
import { CustomerBilling } from './Billing'
import { CustomerProfile } from './Profile'
import { CustomerSupport } from './Support'

export type CustomerAuthScreen = 'loading' | 'login' | 'register' | 'forgot' | 'reset' | 'ready' | 'uninstalled'

export type PortalView = 'overview' | 'shop' | 'services' | 'billing' | 'wallet' | 'hosting' | 'trade' | 'support' | 'profile'

export const portalViews: PortalView[] = ['overview', 'shop', 'services', 'billing', 'wallet', 'hosting', 'trade', 'support', 'profile']

export function portalViewFromPath(): PortalView {
  const candidate = window.location.pathname.split('/').filter(Boolean)[1] as PortalView
  return portalViews.includes(candidate) ? candidate : 'overview'
}

export function CustomerPortalApp() {
  const [screen, setScreen] = useState<CustomerAuthScreen>('loading')
  const [customer, setCustomer] = useState<CustomerIdentity | null>(null)
  const [meta, setMeta] = useState<Meta | null>(null)

  useEffect(() => {
    api<Meta>('/api/v1/meta').then(setMeta).catch(() => undefined)
    api<{ required: boolean }>('/api/v1/install')
      .then(installation => {
        if (installation.required) {
          // The installer runs on the admin console, which may live on
          // another port or domain than the portal.
          api<Meta>('/api/v1/meta')
            .then(current => {
              if (current.surface === 'portal') setScreen('uninstalled')
              else window.location.replace('/admin')
            })
            .catch(() => window.location.replace('/admin'))
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
  if (screen === 'uninstalled') {
    return (
      <main className="session-loading">
        <div className="brand-mark">VB</div>
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

export function CustomerShell({ customer, onLogout }: { customer: CustomerIdentity; onLogout: () => void }) {
  const [view, setView] = useState<PortalView>(portalViewFromPath)

  useEffect(() => {
    if (window.location.pathname !== `/portal/${view}`) {
      window.history.replaceState(null, '', `/portal/${view}${view === 'hosting' ? window.location.search : ''}`)
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
        {view === 'shop' && <CustomerShop customer={customer} />}
        {view === 'services' && <CustomerServicesPanel />}
        {view === 'billing' && <CustomerBilling />}
        {view === 'wallet' && <CustomerWallet />}
        {view === 'hosting' && <HostingCenter customer={customer} />}
        {view === 'trade' && <TradeMarket />}
        {view === 'support' && <CustomerSupport />}
        {view === 'profile' && <CustomerProfile customer={customer} />}
      </main>
    </div>
  )
}
