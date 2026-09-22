import { ChangeEvent, FormEvent, useEffect, useState } from 'react'
import {
  Activity,
  ArrowLeft,
  Boxes,
  ChevronRight,
  CircleDollarSign,
	CreditCard,
  Headphones,
  LayoutDashboard,
  LogOut,
  PackageOpen,
  Plus,
  ReceiptText,
  RefreshCw,
  ScrollText,
  Send,
  RotateCw,
  ServerCog,
	Cpu,
  Settings,
  ShieldCheck,
  ShoppingCart,
  Square,
  UserCircle,
  Users,
  WalletCards,
  Power,
  X,
} from 'lucide-react'
import { AccountRecord, api, AuditLogRecord, AvailableTemplateRecord, CustomerCatalogRecord, CustomerIdentity, CustomerInvoiceRecord, CustomerServiceRecord, CustomerTransactionRecord, HostProbeDetailRecord, HostProbeRecord, InvoiceRecord, NodeRecord, OperationsOverviewRecord, OrderRecord, PaymentIntentRecord, PaymentSettingsRecord, PlanRecord, ProvisioningJobRecord, RegionRecord, ServiceRecord, StaffUser, TicketDetailRecord, TicketRecord, TransactionRecord } from './api'

type Meta = { name: string; environment: string; installed: boolean; capabilities: string[] }
type View = 'overview' | 'customers' | 'orders' | 'billing' | 'payment' | 'services' | 'nodes' | 'hosts' | 'plans' | 'support' | 'audit' | 'security'
type AuthScreen = 'loading' | 'install' | 'login' | 'ready'

function SessionLoading({portal}:{portal:'admin'|'customer'}) { return <main className="session-loading"><div className="brand-mark">CB</div><div className="spinner"/><strong>正在恢复{portal==='admin'?'商家后台':'客户中心'}会话…</strong></main> }

const navItems: Array<{ id: View | 'disabled'; label: string; icon: typeof LayoutDashboard }> = [
  { id: 'overview', label: '运营概览', icon: LayoutDashboard },
  { id: 'customers', label: '客户', icon: Users },
  { id: 'orders', label: '订单', icon: ReceiptText },
  { id: 'billing', label: '账单与交易', icon: CircleDollarSign },
	{ id: 'payment', label: '支付网关', icon: CreditCard },
  { id: 'services', label: 'VPS 服务', icon: Boxes },
  { id: 'plans', label: '商品套餐', icon: PackageOpen },
	{ id: 'nodes', label: '节点对接', icon: ServerCog },
	{ id: 'hosts', label: '宿主机探针', icon: Cpu },
  { id: 'support', label: '客户工单', icon: Headphones },
  { id: 'audit', label: '审计日志', icon: ScrollText },
  { id: 'security', label: '登录安全', icon: Settings },
]
const adminViews:View[]=['overview','customers','orders','billing','payment','services','nodes','hosts','plans','support','audit','security']
type AdminRoute={view:View;hostID?:string}
function adminRouteFromPath():AdminRoute { const parts=window.location.pathname.split('/').filter(Boolean);if(parts[0]==='admin'&&parts[1]==='hosts'&&parts[2])return {view:'hosts',hostID:decodeURIComponent(parts[2])};const candidate=(parts[0]==='admin'?parts[1]:undefined) as View;return {view:adminViews.includes(candidate)?candidate:'overview'} }
function adminRoutePath(route:AdminRoute){return route.hostID?`/admin/hosts/${encodeURIComponent(route.hostID)}`:`/admin/${route.view}`}

export function App(){
  return window.location.pathname.startsWith('/portal') ? <CustomerPortalApp/> : <AdminApp/>
}

function AdminApp() {
  const [meta, setMeta] = useState<Meta | null>(null)
  const [authScreen, setAuthScreen] = useState<AuthScreen>('loading')
  const [user, setUser] = useState<StaffUser | null>(null)

  useEffect(() => {
    Promise.all([api<Meta>('/api/v1/meta'), api<{ required: boolean }>('/api/v1/install')])
      .then(async ([currentMeta, installation]) => {
        setMeta(currentMeta)
        if (installation.required) { setAuthScreen('install'); return }
        try {
          const current = await api<StaffUser>('/api/v1/auth/me')
          setUser(current); setAuthScreen('ready')
        } catch { setAuthScreen('login') }
      })
      .catch(() => setAuthScreen('login'))
  }, [])

  if (authScreen === 'install') {
    return <InstallPage onInstalled={(current, appName) => { setMeta((value) => value ? {...value, name: appName, installed: true} : value); setUser(current); setAuthScreen('ready') }}/>
  }

  if (authScreen === 'loading') return <SessionLoading portal="admin" />

  if (authScreen !== 'ready' || !user) {
    return (
      <AuthPage
        appName={meta?.name ?? 'CLICD Billing'}
        mode={authScreen}
        onAuthenticated={(current) => {
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

function AuthPage({ appName, mode, onAuthenticated }: { appName: string; mode: AuthScreen; onAuthenticated: (user: StaffUser) => void }) {
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
        <div className="brand auth-brand"><div className="brand-mark">CB</div><div><strong>{appName}</strong><span>VPS 商业运营控制平面</span></div></div>
        <div>
          <p className="eyebrow">SECURE BY DEFAULT</p>
          <h1>账务、客户与虚拟化生命周期，在一个可信边界内运行。</h1>
          <p>管理员会话、细粒度权限与节点密钥加密已经启用。</p>
        </div>
        <div className="auth-proof"><ShieldCheck size={18} /><span>Argon2id · CSRF 防护 · AES-256-GCM</span></div>
      </section>
      <section className="auth-form-panel">
        <form className="auth-form" onSubmit={submit}>
          <p className="eyebrow">ADMIN ACCESS</p>
          <h2>登录商家后台</h2>
          <p>使用管理员账号继续。</p>
          <Field label="邮箱" value={email} onChange={setEmail} type="email" autoComplete="email" />
          <Field label="密码" value={password} onChange={setPassword} type="password" autoComplete="current-password" />
          <Field label="二步验证码（启用后填写）" value={totpCode} onChange={setTotpCode} autoComplete="one-time-code" required={false} />
          {error && <div className="form-error" role="alert">{error}</div>}
          <button className="primary-button" disabled={submitting || mode === 'loading'}>
            {mode === 'loading' ? '检查系统状态…' : submitting ? '处理中…' : '登录'}
          </button>
          <a className="auth-switch" href="/portal"><Users size={15}/>前往客户中心</a>
        </form>
      </section>
    </main>
  )
}

type InstallResponse = { user: StaffUser; generated_secrets: Record<string, string> }

function InstallPage({ onInstalled }: { onInstalled: (user: StaffUser, appName: string) => void }) {
  const [form, setForm] = useState({
    app_name: 'CLICD Billing', public_url: window.location.origin, timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'Asia/Shanghai',
    notification_webhook_url: '', notification_webhook_secret: '', metrics_token: '',
    worker_poll_interval: '3s', reconcile_interval: '5m', lifecycle_interval: '1m', renewal_lead_time: '168h', overdue_grace_period: '72h', termination_retention: '168h',
    admin_display_name: '', admin_email: '', admin_password: '',
  })
  const [result, setResult] = useState<InstallResponse | null>(null)
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const update = (key: keyof typeof form) => (value: string) => setForm((current) => ({...current, [key]: value}))

  async function submit(event: FormEvent) {
    event.preventDefault(); setSubmitting(true); setError('')
    try { setResult(await api<InstallResponse>('/api/v1/install', { method: 'POST', body: JSON.stringify(form) })) }
    catch (requestError) { setError(requestError instanceof Error ? requestError.message : '安装失败') }
    finally { setSubmitting(false) }
  }

  if (result) {
    return <main className="installer-page"><section className="installer-card installer-complete"><div className="brand"><div className="brand-mark">CB</div><div><strong>{form.app_name}</strong><span>首次安装已完成</span></div></div><ShieldCheck size={52}/><h1>系统已经可以使用</h1><p>数据库初始化、运行参数和超级管理员已原子写入。下面的自动生成密钥只显示一次，请立即保存到密码管理器。</p>{Object.entries(result.generated_secrets).length > 0 && <div className="generated-secrets">{Object.entries(result.generated_secrets).map(([key,value])=><label key={key}><span>{key}</span><code>{value}</code></label>)}</div>}<button className="primary-button" onClick={()=>onInstalled(result.user,form.app_name)}>进入商家后台</button></section></main>
  }

  return <main className="installer-page"><form className="installer-card" onSubmit={submit}>
    <header className="installer-header"><div><p className="eyebrow">FIRST-RUN INSTALLER</p><h1>初始化 CLICD Billing</h1><p>容器基础密钥已经就绪。这里只设置运行所需信息与首个管理员，支付网关请登录后台后配置。</p></div><div className="installer-step">一次提交<br/><strong>事务安装</strong></div></header>
    <section className="installer-section"><h3>1. 站点设置</h3><div className="installer-grid"><Field label="站点名称" value={form.app_name} onChange={update('app_name')}/><Field label="公开访问地址" value={form.public_url} onChange={update('public_url')} type="url" hint="用于支付回调"/><Field label="时区" value={form.timezone} onChange={update('timezone')}/></div></section>
    <section className="installer-section"><h3>2. 通知与监控</h3><div className="installer-grid"><Field label="通知 Webhook（可选）" value={form.notification_webhook_url} onChange={update('notification_webhook_url')} type="url" required={false}/><Field label="通知签名密钥（留空自动生成）" value={form.notification_webhook_secret} onChange={update('notification_webhook_secret')} type="password" required={false}/><Field label="Metrics Token（留空自动生成）" value={form.metrics_token} onChange={update('metrics_token')} type="password" required={false}/></div></section>
    <details className="installer-section"><summary>3. 自动化时间参数（已有安全默认值）</summary><div className="installer-grid advanced-grid"><Field label="任务轮询" value={form.worker_poll_interval} onChange={update('worker_poll_interval')}/><Field label="节点对账" value={form.reconcile_interval} onChange={update('reconcile_interval')}/><Field label="账务扫描" value={form.lifecycle_interval} onChange={update('lifecycle_interval')}/><Field label="提前续费" value={form.renewal_lead_time} onChange={update('renewal_lead_time')}/><Field label="逾期宽限" value={form.overdue_grace_period} onChange={update('overdue_grace_period')}/><Field label="删除保留" value={form.termination_retention} onChange={update('termination_retention')}/></div></details>
    <section className="installer-section"><h3>4. 超级管理员</h3><div className="installer-grid"><Field label="管理员姓名" value={form.admin_display_name} onChange={update('admin_display_name')} autoComplete="name"/><Field label="管理员邮箱" value={form.admin_email} onChange={update('admin_email')} type="email" autoComplete="email"/><Field label="管理员密码" value={form.admin_password} onChange={update('admin_password')} type="password" autoComplete="new-password" hint="至少 12 个字符"/></div></section>
    {error && <div className="form-error" role="alert">{error}</div>}
    <footer className="installer-footer"><span><ShieldCheck size={16}/>敏感配置将使用 AES-256-GCM 加密保存</span><button className="primary-button compact" disabled={submitting}>{submitting?'正在初始化…':'完成安装'}</button></footer>
  </form></main>
}

function Field({ label, value, onChange, type = 'text', autoComplete, hint, required = true }: { label: string; value: string; onChange: (value: string) => void; type?: string; autoComplete?: string; hint?: string; required?: boolean }) {
  return <label className="field"><span>{label}{hint && <small>{hint}</small>}</span><input required={required} value={value} type={type} autoComplete={autoComplete} onChange={(event) => onChange(event.target.value)} /></label>
}

type CustomerAuthScreen = 'loading' | 'login' | 'register' | 'ready'
type PortalView = 'overview' | 'shop' | 'services' | 'billing' | 'support' | 'profile'
const portalViews:PortalView[]=['overview','shop','services','billing','support','profile']

function portalViewFromPath():PortalView { const candidate=window.location.pathname.split('/').filter(Boolean)[1] as PortalView;return portalViews.includes(candidate)?candidate:'overview' }

function CustomerPortalApp(){
  const [screen,setScreen]=useState<CustomerAuthScreen>('loading')
  const [customer,setCustomer]=useState<CustomerIdentity|null>(null)
  useEffect(()=>{api<{required:boolean}>('/api/v1/install').then((installation)=>{if(installation.required){window.location.replace('/');return}return api<CustomerIdentity>('/api/v1/customer/auth/me').then((value)=>{setCustomer(value);setScreen('ready')}).catch(()=>setScreen('login'))}).catch(()=>setScreen('login'))},[])
  if(screen==='loading')return <SessionLoading portal="customer"/>
  if(screen!=='ready'||!customer)return <CustomerAuthPage mode={screen} onMode={setScreen} onAuthenticated={(value)=>{setCustomer(value);setScreen('ready')}}/>
  return <CustomerShell customer={customer} onLogout={async()=>{await api('/api/v1/customer/auth/logout',{method:'POST'});setCustomer(null);setScreen('login')}}/>
}

function CustomerAuthPage({mode,onMode,onAuthenticated}:{mode:CustomerAuthScreen;onMode:(mode:CustomerAuthScreen)=>void;onAuthenticated:(customer:CustomerIdentity)=>void}){
  const register=mode==='register';const [displayName,setDisplayName]=useState('');const [email,setEmail]=useState('');const [password,setPassword]=useState('');const [totpCode,setTotpCode]=useState('');const [error,setError]=useState('');const [submitting,setSubmitting]=useState(false)
  async function submit(event:FormEvent){event.preventDefault();setSubmitting(true);setError('');try{const body=register?{display_name:displayName,email,password}:{email,password,totp_code:totpCode};const current=await api<CustomerIdentity>(register?'/api/v1/customer/auth/register':'/api/v1/customer/auth/login',{method:'POST',body:JSON.stringify(body)});onAuthenticated(current)}catch(err){setError(err instanceof Error?err.message:'操作失败')}finally{setSubmitting(false)}}
  return <main className="auth-page customer-auth-page"><section className="auth-brand-panel customer-brand-panel"><div className="brand auth-brand"><div className="brand-mark">CB</div><div><strong>CLICD Billing</strong><span>客户服务中心</span></div></div><div><p className="eyebrow">YOUR CLOUD, UNDER CONTROL</p><h1>查看账单，管理每一台 VPS。</h1><p>服务状态、网络地址、到期时间和电源操作集中在一个安全入口。</p></div><div className="auth-proof"><ShieldCheck size={18}/><span>租户隔离 · 服务端会话 · 操作审计</span></div></section><section className="auth-form-panel"><form className="auth-form" onSubmit={submit}><p className="eyebrow">CUSTOMER PORTAL</p><h2>{register?'创建客户账户':'登录客户中心'}</h2><p>{register?'注册后会自动建立个人账务账户。':'管理你的 VPS 服务和账单。'}</p>{register&&<Field label="姓名" value={displayName} onChange={setDisplayName} autoComplete="name"/>}<Field label="邮箱" value={email} onChange={setEmail} type="email" autoComplete="email"/><Field label="密码" value={password} onChange={setPassword} type="password" autoComplete={register?'new-password':'current-password'} hint={register?'至少 12 个字符':undefined}/>{!register&&<Field label="二步验证码（启用后填写）" value={totpCode} onChange={setTotpCode} autoComplete="one-time-code"/>}{error&&<div className="form-error" role="alert">{error}</div>}<button className="primary-button" disabled={submitting||mode==='loading'}>{mode==='loading'?'检查登录状态…':submitting?'处理中…':register?'注册并进入':'登录'}</button><button type="button" className="auth-switch button-link" onClick={()=>{setError('');onMode(register?'login':'register')}}>{register?'已有账户？返回登录':'还没有账户？立即注册'}</button><a className="auth-switch" href="/"><ArrowLeft size={15}/>返回商家后台</a></form></section></main>
}

function CustomerShell({customer,onLogout}:{customer:CustomerIdentity;onLogout:()=>void}){
  const [view,setView]=useState<PortalView>(portalViewFromPath)
  useEffect(()=>{if(window.location.pathname!==`/portal/${view}`)window.history.replaceState(null,'',`/portal/${view}`);const pop=()=>setView(portalViewFromPath());window.addEventListener('popstate',pop);return()=>window.removeEventListener('popstate',pop)},[])
  const navigate=(next:PortalView)=>{if(next===view)return;window.history.pushState(null,'',`/portal/${next}`);setView(next)}
  const items:[PortalView,string,typeof LayoutDashboard][]=[['overview','服务概览',LayoutDashboard],['shop','选购 VPS',ShoppingCart],['services','我的 VPS',Boxes],['billing','订单与账单',WalletCards],['support','支持工单',Headphones],['profile','账户资料',UserCircle]]
  const titles:Record<PortalView,string>={overview:'服务概览',shop:'选购 VPS',services:'我的 VPS',billing:'订单与账单',support:'支持工单',profile:'账户资料'}
  return <div className="app-shell customer-shell"><aside className="sidebar"><div className="brand"><div className="brand-mark">CB</div><div><strong>客户中心</strong><span>Customer Portal</span></div></div><nav>{items.map(([id,label,Icon])=><button key={id} className={id===view?'nav-item active':'nav-item'} onClick={()=>navigate(id)}><Icon size={18}/><span>{label}</span>{id===view&&<ChevronRight size={16}/>}</button>)}</nav><div className="sidebar-status"><span className="status-dot online"/><div><strong>{customer.display_name}</strong><span>账户正常</span></div></div></aside><main><header className="topbar"><div><p className="eyebrow">CUSTOMER PORTAL</p><h1>{titles[view]}</h1></div><div className="operator"><span>{customer.email}</span><div className="avatar">{customer.display_name.slice(0,1)}</div><button className="icon-button" aria-label="退出登录" onClick={onLogout}><LogOut size={17}/></button></div></header>{view==='overview'&&<CustomerOverview customer={customer}/>} {view==='shop'&&<CustomerShop customer={customer}/>} {view==='services'&&<CustomerServices/>}{view==='billing'&&<CustomerBilling/>}{view==='support'&&<CustomerSupport/>}{view==='profile'&&<CustomerProfile customer={customer}/>}</main></div>
}

function CustomerOverview({customer}:{customer:CustomerIdentity}){
  const [services,setServices]=useState<CustomerServiceRecord[]>([]);const [invoices,setInvoices]=useState<CustomerInvoiceRecord[]>([])
  useEffect(()=>{void Promise.all([api<CustomerServiceRecord[]>('/api/v1/customer/services'),api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices')]).then(([s,i])=>{setServices(s);setInvoices(i)})},[])
  const online=services.filter((item)=>item.runtime_status==='running').length;const due=invoices.filter((item)=>item.status==='open').reduce((sum,item)=>sum+item.balance_minor,0)
  return <><section className="hero-card customer-hero"><div><p className="eyebrow">WELCOME BACK</p><h2>{customer.display_name}，你的云服务一切尽在掌握。</h2><p>从这里查看实例状态、执行电源操作，并跟踪账单和付款记录。</p></div><div className="hero-signal"><span>{online}/{services.length}</span><small>实例运行中</small></div></section><section className="metrics"><article><Boxes size={20}/><span>VPS 总数</span><strong>{services.length}</strong></article><article><Activity size={20}/><span>运行中</span><strong>{online}</strong></article><article><ReceiptText size={20}/><span>待支付账单</span><strong>{invoices.filter((item)=>item.status==='open').length}</strong></article><article><CircleDollarSign size={20}/><span>待支付金额</span><strong>{money(due,invoices[0]?.currency||'CNY')}</strong></article></section></>
}

function CustomerShop({customer}:{customer:CustomerIdentity}){
  const [catalog,setCatalog]=useState<CustomerCatalogRecord|null>(null);const [selectedID,setSelectedID]=useState('');const [cycle,setCycle]=useState('monthly');const [error,setError]=useState('');const [saving,setSaving]=useState(false);const [created,setCreated]=useState<OrderRecord|null>(null);const [paying,setPaying]=useState(false)
  useEffect(()=>{api<CustomerCatalogRecord>('/api/v1/customer/catalog').then((value)=>{setCatalog(value);const first=value.plans.find((plan)=>plan.prices.some((price)=>price.currency===customer.default_currency));if(first){setSelectedID(first.id);const price=first.prices.find((item)=>item.currency===customer.default_currency);if(price)setCycle(price.billing_cycle)}}).catch((err)=>setError(err.message))},[customer.default_currency])
  const plans=(catalog?.plans||[]).filter((plan)=>plan.prices.some((price)=>price.currency===customer.default_currency));const selected=plans.find((plan)=>plan.id===selectedID);const prices=selected?.prices.filter((price)=>price.currency===customer.default_currency)||[]
  useEffect(()=>{if(prices.length&&!prices.some((price)=>price.billing_cycle===cycle))setCycle(prices[0].billing_cycle)},[selectedID])
  async function submit(event:FormEvent<HTMLFormElement>){event.preventDefault();if(!selected)return;const data=new FormData(event.currentTarget);setSaving(true);setError('');setCreated(null);try{const order=await api<OrderRecord>('/api/v1/customer/orders',{method:'POST',body:JSON.stringify({items:[{plan_id:selected.id,region_id:data.get('region_id'),billing_cycle:data.get('billing_cycle'),quantity:Number(data.get('quantity')),configuration:{template_id:data.get('template_id')}}]})});setCreated(order)}catch(err){setError(err instanceof Error?err.message:'下单失败')}finally{setSaving(false)}}
  async function checkout(){if(!created)return;setPaying(true);setError('');try{const intent=await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${created.invoice_id}/checkout`,{method:'POST'});window.location.assign(intent.checkout_url)}catch(err){setError(err instanceof Error?err.message:'创建收银台失败');setPaying(false)}}
  const currentPrice=prices.find((price)=>price.billing_cycle===cycle)
  return <section className="workspace-panel">
    <div className="page-actions"><div><p className="eyebrow">MARKETPLACE</p><h2>选择适合你的 VPS</h2><p>价格和资源由服务端重新校验，付款成功后自动调度并开通。</p></div></div>
    {error&&<div className="form-error">{error}</div>}
    <div className="shop-grid">
      {plans.map((plan)=>{const price=plan.prices.find((item)=>item.currency===customer.default_currency);return <button type="button" key={plan.id} className={selectedID===plan.id?'shop-plan selected':'shop-plan'} onClick={()=>setSelectedID(plan.id)}><div><span className="tag">{plan.virtualization.toUpperCase()}</span>{selectedID===plan.id&&<span className="selected-mark">已选择</span>}</div><h3>{plan.name}</h3><small>{plan.code}</small><div className="shop-specs"><span>{plan.vcpu} vCPU</span><span>{plan.ram_mb} MB</span><span>{plan.disk_gb} GB SSD</span><span>{plan.traffic_gb} GB 流量</span></div><div className="shop-price">{price?money(price.amount_minor,price.currency):'暂无价格'}<small>/ {price?cycleLabel(price.billing_cycle):''}</small></div></button>})}
      {!plans.length&&<div className="empty-card">当前币种暂无可售套餐</div>}
    </div>
    {selected&&<form className="checkout-config panel" onSubmit={submit}>
      <div className="panel-heading"><div><p className="eyebrow">ORDER CONFIGURATION</p><h3>配置 {selected.name}</h3></div><span className="tag">SERVER PRICED</span></div>
      <div className="form-grid">
        <label><span>地区</span><select name="region_id" required>{catalog?.regions.map((region)=><option key={region.id} value={region.id}>{region.name}</option>)}</select></label>
        <label><span>计费周期</span><select name="billing_cycle" value={cycle} onChange={(event)=>setCycle(event.target.value)}>{prices.map((price)=><option key={price.billing_cycle} value={price.billing_cycle}>{cycleLabel(price.billing_cycle)} · {money(price.amount_minor,price.currency)}</option>)}</select></label>
        <label><span>数量</span><input name="quantity" type="number" min="1" max="20" defaultValue="1"/></label>
        <label><span>系统模板</span><select key={selected.id} name="template_id" defaultValue={selected.default_template_id}>{selected.allowed_template_ids.map((template)=><option key={template} value={template}>{template}</option>)}</select></label>
        <div className="order-total"><span>当前单价{currentPrice?.setup_fee_minor?'（含开通费）':''}</span><strong>{money((currentPrice?.amount_minor||0)+(currentPrice?.setup_fee_minor||0),customer.default_currency)}</strong></div>
        <div className="form-actions wide"><button className="primary-button compact" disabled={saving||!catalog?.regions.length}>{saving?'正在创建账单…':'确认下单'}</button></div>
      </div>
    </form>}
    {created&&<div className="checkout-success"><div><strong>订单 {created.number} 已创建</strong><span>应付 {money(created.total_minor,created.currency)}，账单 {created.invoice_number}</span></div>{catalog?.checkout_enabled?<button className="primary-button compact" disabled={paying} onClick={checkout}>{paying?'正在进入收银台…':'立即付款'}</button>:<span>在线支付尚未配置，请联系商家处理账单。</span>}</div>}
  </section>
}

function CustomerServices(){
  const [services,setServices]=useState<CustomerServiceRecord[]>([]);const [error,setError]=useState('');const [acting,setActing]=useState('')
  const load=()=>api<CustomerServiceRecord[]>('/api/v1/customer/services').then(setServices).catch((err)=>setError(err.message))
  useEffect(()=>{void load();const timer=window.setInterval(()=>void load(),10000);return()=>window.clearInterval(timer)},[])
  async function action(service:CustomerServiceRecord,value:'start'|'stop'|'restart'){
    if(value!=='start'&&!window.confirm(`${value==='stop'?'关机':'重启'}会中断当前连接，确认继续？`))return
    setActing(service.id+value);setError('');try{await api(`/api/v1/customer/services/${service.id}/actions/${value}`,{method:'POST'});await load()}catch(err){setError(err instanceof Error?err.message:'操作失败')}finally{setActing('')}
  }
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">COMPUTE</p><h2>我的 VPS</h2><p>操作先进入安全任务队列，节点完成后状态会自动同步。</p></div><button className="secondary-button" onClick={()=>void load()}><RefreshCw size={15}/>刷新</button></div>{error&&<div className="form-error">{error}</div>}<div className="service-grid">{services.map((service)=>{const busy=Boolean(service.desired_runtime_status);const available=service.status==='active'&&!busy;return <article className="service-card" key={service.id}><div className="service-card-head"><div><span className="tag">{service.virtualization.toUpperCase()}</span><h3>{service.instance_name}</h3><p>{service.plan_name} · {service.region_name}</p></div><StatusBadge status={service.runtime_status}/></div><div className="service-specs"><span><strong>{service.vcpu}</strong>vCPU</span><span><strong>{service.ram_mb}</strong>MB 内存</span><span><strong>{service.disk_gb}</strong>GB 磁盘</span><span><strong>{service.traffic_gb}</strong>GB 流量</span></div><div className="network-box"><span>IPv4<strong>{service.primary_ipv4||'等待分配'}</strong></span><span>IPv6<strong>{service.primary_ipv6||'未分配'}</strong></span></div>{busy&&<div className="pending-action"><RefreshCw size={14}/>正在切换至{service.desired_runtime_status==='running'?'运行':'关机'}状态</div>}{service.last_reconcile_error&&<div className="service-warning">{service.last_reconcile_error}</div>}<div className="service-actions">{service.runtime_status==='stopped'&&<button className="primary-button compact" disabled={!available||acting!==''} onClick={()=>action(service,'start')}><Power size={15}/>开机</button>}{service.runtime_status!=='stopped'&&<button className="secondary-button" disabled={!available||acting!==''} onClick={()=>action(service,'stop')}><Square size={14}/>关机</button>}<button className="secondary-button" disabled={!available||service.runtime_status==='stopped'||acting!==''} onClick={()=>action(service,'restart')}><RotateCw size={14}/>重启</button></div><footer>业务状态：<StatusBadge status={service.status}/><span>到期 {service.next_due_at?new Date(service.next_due_at).toLocaleDateString():'—'}</span></footer></article>})}{!services.length&&<div className="empty-card">当前账户还没有 VPS 服务</div>}</div></section>
}

function CustomerBilling(){
  const [invoices,setInvoices]=useState<CustomerInvoiceRecord[]>([]);const [transactions,setTransactions]=useState<CustomerTransactionRecord[]>([]);const [orders,setOrders]=useState<OrderRecord[]>([]);const [checkoutEnabled,setCheckoutEnabled]=useState(false);const [paying,setPaying]=useState('');const [error,setError]=useState('')
  const load=async()=>{try{const [i,t,o,c]=await Promise.all([api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices'),api<CustomerTransactionRecord[]>('/api/v1/customer/transactions'),api<OrderRecord[]>('/api/v1/customer/orders'),api<CustomerCatalogRecord>('/api/v1/customer/catalog')]);setInvoices(i);setTransactions(t);setOrders(o);setCheckoutEnabled(c.checkout_enabled)}catch(err){setError(err instanceof Error?err.message:'加载失败')}}
  useEffect(()=>{void load()},[])
  async function checkout(invoice:CustomerInvoiceRecord){setPaying(invoice.id);setError('');try{const intent=await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${invoice.id}/checkout`,{method:'POST'});window.location.assign(intent.checkout_url)}catch(err){setError(err instanceof Error?err.message:'创建收银台失败');setPaying('')}}
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">BILLING</p><h2>订单、账单与付款记录</h2><p>所有订单由服务端计价；网关回调验签成功后才会开通服务。</p></div><button className="secondary-button" onClick={()=>void load()}><RefreshCw size={15}/>刷新</button></div>{error&&<div className="form-error">{error}</div>}<div className="panel"><div className="panel-heading"><h3>订单</h3><span className="tag">{orders.length} ORDERS</span></div><div className="table-wrap"><table><thead><tr><th>订单号</th><th>账单号</th><th>金额</th><th>状态</th><th>创建时间</th></tr></thead><tbody>{orders.map((item)=><tr key={item.id}><td><strong>{item.number}</strong></td><td>{item.invoice_number}</td><td>{money(item.total_minor,item.currency)}</td><td><StatusBadge status={item.status}/></td><td>{new Date(item.created_at).toLocaleString()}</td></tr>)}{!orders.length&&<tr><td colSpan={5} className="empty-state">暂无订单</td></tr>}</tbody></table></div></div><div className="panel"><div className="panel-heading"><h3>账单</h3><span className="tag">{invoices.length} TOTAL</span></div><div className="table-wrap"><table><thead><tr><th>账单号</th><th>金额</th><th>未付余额</th><th>状态</th><th>到期</th><th></th></tr></thead><tbody>{invoices.map((item)=><tr key={item.id}><td><strong>{item.number}</strong></td><td>{money(item.total_minor,item.currency)}</td><td>{money(item.balance_minor,item.currency)}</td><td><StatusBadge status={item.status}/></td><td>{new Date(item.due_at).toLocaleDateString()}</td><td>{item.status==='open'&&(checkoutEnabled?<button className="text-button" disabled={paying===item.id} onClick={()=>checkout(item)}>{paying===item.id?'跳转中…':'在线支付'}</button>:<small>请联系商家付款</small>)}</td></tr>)}{!invoices.length&&<tr><td colSpan={6} className="empty-state">暂无账单</td></tr>}</tbody></table></div></div><div className="panel"><div className="panel-heading"><h3>付款记录</h3><span className="tag">IMMUTABLE LEDGER</span></div><div className="table-wrap"><table><thead><tr><th>交易参考</th><th>账单</th><th>渠道</th><th>金额</th><th>状态</th><th>时间</th></tr></thead><tbody>{transactions.map((item)=><tr key={item.id}><td><strong>{item.provider_transaction_id||item.id}</strong></td><td>{item.invoice_number||'—'}</td><td>{item.provider}</td><td>{money(item.amount_minor,item.currency)}</td><td><StatusBadge status={item.status}/></td><td>{new Date(item.created_at).toLocaleString()}</td></tr>)}{!transactions.length&&<tr><td colSpan={6} className="empty-state">暂无付款记录</td></tr>}</tbody></table></div></div></section>
}

function CustomerProfile({customer}:{customer:CustomerIdentity}){return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">ACCOUNT</p><h2>账户资料</h2><p>账户身份和二步验证设置。</p></div></div><div className="panel profile-panel"><div><span>姓名</span><strong>{customer.display_name}</strong></div><div><span>登录邮箱</span><strong>{customer.email}</strong></div><div><span>账户角色</span><strong>{customer.role==='owner'?'所有者':customer.role}</strong></div><div><span>账户状态</span><StatusBadge status={customer.account_status}/></div><div><span>账户 ID</span><code>{customer.account_id}</code></div></div><SecuritySettings enabled={customer.mfa_enabled} customer/></section>}

function SecuritySettings({enabled:initialEnabled,customer=false}:{enabled:boolean;customer?:boolean}){
  const [enabled,setEnabled]=useState(initialEnabled);const [setup,setSetup]=useState<{secret:string;otpauth_uri:string}|null>(null);const [code,setCode]=useState('');const [error,setError]=useState('')
  const base=customer?'/api/v1/customer/auth/mfa':'/api/v1/auth/mfa'
  async function start(){try{setSetup(await api<{secret:string;otpauth_uri:string}>(`${base}/setup`,{method:'POST'}));setError('')}catch(err){setError(err instanceof Error?err.message:'设置失败')}}
  async function confirm(){try{await api(`${base}/confirm`,{method:'POST',body:JSON.stringify({code})});setEnabled(true);setSetup(null);setCode('');setError('')}catch(err){setError(err instanceof Error?err.message:'验证码无效')}}
  async function disable(){try{await api(`${base}/disable`,{method:'POST',body:JSON.stringify({code})});setEnabled(false);setCode('');setError('')}catch(err){setError(err instanceof Error?err.message:'验证码无效')}}
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">ACCOUNT SECURITY</p><h2>二步验证</h2><p>使用兼容 TOTP 的验证器，在密码之外增加一次性验证码。</p></div><span className={`ticket-state ${enabled?'resolved':'closed'}`}>{enabled?'已启用':'未启用'}</span></div>{error&&<div className="form-error">{error}</div>}<div className="panel security-panel">{!enabled&&!setup&&<><h3>保护管理员账户</h3><p>启用后每次登录都必须输入六位动态验证码。</p><button className="primary-button compact" onClick={()=>void start()}><ShieldCheck size={16}/>开始设置</button></>}{setup&&<><h3>添加到验证器</h3><p>在验证器中手工输入密钥，或使用支持 otpauth 链接的应用打开配置。</p><code className="mfa-secret">{setup.secret}</code><a className="secondary-button mfa-link" href={setup.otpauth_uri}>打开验证器</a><label className="field"><span>输入六位验证码确认</span><input value={code} inputMode="numeric" maxLength={6} onChange={(event)=>setCode(event.target.value)}/></label><button className="primary-button compact" disabled={code.length!==6} onClick={()=>void confirm()}>确认启用</button></>}{enabled&&<><h3>二步验证已启用</h3><p>如需关闭，请先输入当前验证器中的六位验证码。</p><label className="field"><span>当前验证码</span><input value={code} inputMode="numeric" maxLength={6} onChange={(event)=>setCode(event.target.value)}/></label><button className="secondary-button" disabled={code.length!==6} onClick={()=>void disable()}>关闭二步验证</button></>}</div></section>
}

function CustomerSupport(){
  const [tickets,setTickets]=useState<TicketRecord[]>([]);const [services,setServices]=useState<CustomerServiceRecord[]>([]);const [detail,setDetail]=useState<TicketDetailRecord|null>(null);const [creating,setCreating]=useState(false);const [error,setError]=useState('')
  const load=async()=>{try{const [ticketRows,serviceRows]=await Promise.all([api<TicketRecord[]>('/api/v1/customer/tickets'),api<CustomerServiceRecord[]>('/api/v1/customer/services')]);setTickets(ticketRows);setServices(serviceRows)}catch(err){setError(err instanceof Error?err.message:'加载失败')}}
  useEffect(()=>{void load()},[])
  async function open(id:string){try{setDetail(await api<TicketDetailRecord>(`/api/v1/customer/tickets/${id}`));setCreating(false)}catch(err){setError(err instanceof Error?err.message:'加载失败')}}
  async function create(event:FormEvent<HTMLFormElement>){event.preventDefault();const data=new FormData(event.currentTarget);try{const result=await api<TicketDetailRecord>('/api/v1/customer/tickets',{method:'POST',body:JSON.stringify({service_id:data.get('service_id'),subject:data.get('subject'),priority:data.get('priority'),body:data.get('body')})});setDetail(result);setCreating(false);await load()}catch(err){setError(err instanceof Error?err.message:'创建失败')}}
  async function reply(event:FormEvent<HTMLFormElement>){event.preventDefault();if(!detail)return;const form=event.currentTarget;const data=new FormData(form);try{await api(`/api/v1/customer/tickets/${detail.ticket.id}/messages`,{method:'POST',body:JSON.stringify({body:data.get('body')})});form.reset();await open(detail.ticket.id);await load()}catch(err){setError(err instanceof Error?err.message:'回复失败')}}
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">SUPPORT</p><h2>支持工单</h2><p>问题与实例关联，所有沟通均保留时间线和操作审计。</p></div><button className="primary-button compact" onClick={()=>{setCreating(true);setDetail(null)}}><Plus size={16}/>新建工单</button></div>{error&&<div className="form-error">{error}</div>}
    {creating&&<form className="panel form-grid" onSubmit={create}><label><span>主题</span><input name="subject" minLength={3} maxLength={160} required/></label><label><span>优先级</span><select name="priority" defaultValue="normal"><option value="low">低</option><option value="normal">普通</option><option value="high">高</option><option value="urgent">紧急</option></select></label><label><span>关联 VPS（可选）</span><select name="service_id"><option value="">不关联</option>{services.map((service)=><option key={service.id} value={service.id}>{service.instance_name}</option>)}</select></label><label className="wide"><span>问题描述</span><textarea name="body" rows={6} maxLength={10000} required/></label><div className="form-actions wide"><button type="button" className="secondary-button" onClick={()=>setCreating(false)}>取消</button><button className="primary-button compact">提交工单</button></div></form>}
    <div className="support-layout"><div className="ticket-list">{tickets.map((ticket)=><button key={ticket.id} className={detail?.ticket.id===ticket.id?'ticket-row selected':'ticket-row'} onClick={()=>void open(ticket.id)}><div><strong>{ticket.subject}</strong><span>{ticket.number}</span></div><span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span><small>{ticket.message_count} 条消息 · {new Date(ticket.last_reply_at).toLocaleString()}</small></button>)}{!tickets.length&&<div className="empty-card">暂无支持工单</div>}</div>{detail?<TicketConversation detail={detail} onReply={reply}/>:<div className="panel support-placeholder">选择工单查看完整沟通记录</div>}</div>
  </section>
}

function TicketConversation({detail,onReply,admin=false,onStatus}:{detail:TicketDetailRecord;onReply:(event:FormEvent<HTMLFormElement>)=>void;admin?:boolean;onStatus?:(status:string)=>void}){
  return <div className="panel conversation"><div className="panel-heading"><div><p className="eyebrow">{detail.ticket.number}</p><h3>{detail.ticket.subject}</h3><small>{detail.ticket.customer_name}{detail.ticket.instance_name?` · ${detail.ticket.instance_name}`:''}</small></div><span className={`ticket-state ${detail.ticket.status}`}>{ticketStatusLabel(detail.ticket.status)}</span></div><div className="message-list">{detail.messages.map((message)=><article key={message.id} className={`message ${message.author_type}${message.internal?' internal':''}`}><header><strong>{message.author_name||ticketAuthorLabel(message.author_type)}</strong><span>{message.internal?'内部备注 · ':''}{new Date(message.created_at).toLocaleString()}</span></header><p>{message.body}</p></article>)}</div>{detail.ticket.status!=='closed'&&<form className="reply-form" onSubmit={onReply}><textarea name="body" rows={4} maxLength={10000} placeholder={admin?'回复客户或记录内部备注':'补充问题信息'} required/>{admin&&<label className="checkbox"><input name="internal" type="checkbox"/> 仅内部可见</label>}<button className="primary-button compact"><Send size={15}/>发送</button></form>}{admin&&<div className="ticket-actions"><button className="secondary-button" onClick={()=>onStatus?.('open')}>重新打开</button><button className="secondary-button" onClick={()=>onStatus?.('resolved')}>标记解决</button><button className="secondary-button" onClick={()=>onStatus?.('closed')}>关闭工单</button></div>}</div>
}

function AdminSupport(){
  const [tickets,setTickets]=useState<TicketRecord[]>([]);const [detail,setDetail]=useState<TicketDetailRecord|null>(null);const [error,setError]=useState('')
  const load=()=>api<TicketRecord[]>('/api/v1/admin/tickets').then(setTickets).catch((err)=>setError(err.message));useEffect(()=>{void load()},[])
  async function open(id:string){try{setDetail(await api<TicketDetailRecord>(`/api/v1/admin/tickets/${id}`))}catch(err){setError(err instanceof Error?err.message:'加载失败')}}
  async function reply(event:FormEvent<HTMLFormElement>){event.preventDefault();if(!detail)return;const form=event.currentTarget;const data=new FormData(form);try{await api(`/api/v1/admin/tickets/${detail.ticket.id}/messages`,{method:'POST',body:JSON.stringify({body:data.get('body'),internal:data.get('internal')==='on'})});form.reset();await open(detail.ticket.id);load()}catch(err){setError(err instanceof Error?err.message:'回复失败')}}
  async function status(value:string){if(!detail)return;try{await api(`/api/v1/admin/tickets/${detail.ticket.id}`,{method:'PATCH',body:JSON.stringify({status:value})});await open(detail.ticket.id);load()}catch(err){setError(err instanceof Error?err.message:'更新失败')}}
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">SUPPORT DESK</p><h2>客户工单</h2><p>处理客户问题、内部备注并追踪解决状态。</p></div><button className="secondary-button" onClick={()=>load()}><RefreshCw size={15}/>刷新</button></div>{error&&<div className="form-error">{error}</div>}<div className="support-layout"><div className="ticket-list">{tickets.map((ticket)=><button key={ticket.id} className={detail?.ticket.id===ticket.id?'ticket-row selected':'ticket-row'} onClick={()=>void open(ticket.id)}><div><strong>{ticket.subject}</strong><span>{ticket.customer_name} · {ticket.number}</span></div><span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span><small>{ticket.priority.toUpperCase()} · {ticket.message_count} 条消息</small></button>)}{!tickets.length&&<div className="empty-card">暂无客户工单</div>}</div>{detail?<TicketConversation detail={detail} onReply={reply} admin onStatus={status}/>:<div className="panel support-placeholder">选择工单开始处理</div>}</div></section>
}

function AuditView(){const [rows,setRows]=useState<AuditLogRecord[]>([]);const [error,setError]=useState('');const load=()=>api<AuditLogRecord[]>('/api/v1/admin/audit-logs').then(setRows).catch((err)=>setError(err.message));useEffect(()=>{void load()},[]);return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">AUDIT TRAIL</p><h2>审计日志</h2><p>安全敏感操作采用服务端只追加记录，默认展示最近 500 条。</p></div><button className="secondary-button" onClick={()=>load()}><RefreshCw size={15}/>刷新</button></div>{error&&<div className="form-error">{error}</div>}<div className="table-wrap"><table><thead><tr><th>时间</th><th>操作者</th><th>动作</th><th>目标</th><th>来源 IP</th><th>元数据</th></tr></thead><tbody>{rows.map((row)=><tr key={row.id}><td>{new Date(row.created_at).toLocaleString()}</td><td>{row.actor_type}<small>{row.actor_id||'—'}</small></td><td><strong>{row.action}</strong></td><td>{row.target_type}<small>{row.target_id||'—'}</small></td><td>{row.ip||'—'}</td><td className="audit-metadata" title={JSON.stringify(row.metadata)}>{JSON.stringify(row.metadata)}</td></tr>)}{!rows.length&&<tr><td colSpan={6} className="empty-state">暂无审计记录</td></tr>}</tbody></table></div></section>}

function AdminShell({ meta, user, onLogout }: { meta: Meta | null; user: StaffUser; onLogout: () => void }) {
  const [route, setRoute] = useState<AdminRoute>(adminRouteFromPath)
  const view=route.view
  useEffect(()=>{const initial=adminRouteFromPath();const canonical=adminRoutePath(initial);if(window.location.pathname!==canonical)window.history.replaceState(null,'',canonical);const pop=()=>setRoute(adminRouteFromPath());window.addEventListener('popstate',pop);return()=>window.removeEventListener('popstate',pop)},[])
  const navigate=(next:AdminRoute)=>{const path=adminRoutePath(next);if(window.location.pathname===path)return;window.history.pushState(null,'',path);setRoute(next)}
  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="brand"><div className="brand-mark">CB</div><div><strong>{meta?.name ?? 'CLICD Billing'}</strong><span>商家控制中心</span></div></div>
        <nav aria-label="主导航">
          {navItems.map(({ id, label, icon: Icon }) => (
            <button className={id === view ? 'nav-item active' : 'nav-item'} key={label} type="button" disabled={id === 'disabled'} onClick={() => id !== 'disabled' && navigate({view:id})}>
              <Icon size={18} aria-hidden="true" /><span>{label}</span>{id === view && <ChevronRight size={16} aria-hidden="true" />}
            </button>
          ))}
        </nav>
        <div className="sidebar-status"><span className="status-dot online" /><div><strong>控制平面在线</strong><span>{meta?.environment ?? 'production'}</span></div></div>
      </aside>
      <main>
        <header className="topbar">
          <div><p className="eyebrow">ADMIN CONTROL PLANE</p><h1>{route.hostID?'宿主机探针详情':viewTitle(view)}</h1></div>
          <div className="operator"><span>{user.display_name}</span><div className="avatar">{user.display_name.slice(0, 1)}</div><button className="icon-button" aria-label="退出登录" onClick={onLogout}><LogOut size={17} /></button></div>
        </header>
        {view === 'overview' && <Overview />}
        {view === 'customers' && <CustomersView />}
        {view === 'orders' && <OrdersView />}
        {view === 'billing' && <BillingView />}
		{view === 'payment' && <PaymentSettingsView />}
        {view === 'services' && <ServicesView />}
        {view === 'nodes' && <NodesView />}
		{view === 'hosts' && (route.hostID?<HostDetailView id={route.hostID} onBack={()=>navigate({view:'hosts'})}/>:<HostsView onOpen={(id)=>navigate({view:'hosts',hostID:id})}/>)}
        {view === 'plans' && <PlansView />}
        {view === 'support' && <AdminSupport />}
        {view === 'audit' && <AuditView />}
        {view === 'security' && <SecuritySettings enabled={user.mfa_enabled} />}
      </main>
    </div>
  )
}

function Overview() {
  const [data,setData]=useState<OperationsOverviewRecord|null>(null);const [error,setError]=useState('')
  const load=()=>api<OperationsOverviewRecord>('/api/v1/admin/overview').then(setData).catch((err)=>setError(err.message));useEffect(()=>{void load();const timer=window.setInterval(()=>void load(),30000);return()=>window.clearInterval(timer)},[])
  if(!data)return <section className="workspace-panel">{error?<div className="form-error">{error}</div>:<div className="empty-card">正在加载运营数据…</div>}</section>
  const revenue=data.revenue_30_days.map((item)=>money(item.amount_minor,item.currency)).join(' / ')||money(0,'CNY');const outstanding=data.outstanding.map((item)=>money(item.amount_minor,item.currency)).join(' / ')||money(0,'CNY');const nodeRate=data.nodes?Math.round(data.online_nodes/data.nodes*100):0
  return <section className="workspace-panel">
    <section className="hero-card operations-hero"><div><p className="eyebrow">最近 30 天</p><h2>{revenue} 已到账</h2><p>{data.orders_30_days} 笔新订单 · {data.accounts} 个有效客户 · {data.services} 台在管 VPS</p></div><div className="hero-signal"><span>{nodeRate}%</span><small>节点在线率</small></div></section>
    <section className="metrics"><article><CircleDollarSign size={20}/><span>30 天收入</span><strong>{revenue}</strong></article><article><ReceiptText size={20}/><span>待收金额</span><strong>{outstanding}</strong><small>{data.open_invoices} 张账单</small></article><article><Activity size={20}/><span>运行中 VPS</span><strong>{data.running_services} / {data.services}</strong></article><article><Headphones size={20}/><span>待处理工单</span><strong>{data.open_tickets}</strong></article></section>
    <div className="content-grid"><section className="panel"><div className="panel-heading"><h3>宿主机与容量</h3><span className="tag">{data.online_nodes}/{data.nodes} ONLINE</span></div><CapacityBar label="vCPU" used={data.reserved_vcpu} total={data.capacity_vcpu}/><CapacityBar label="内存" used={data.reserved_ram_mb} total={data.capacity_ram_mb} suffix=" MB"/><CapacityBar label="磁盘" used={data.reserved_disk_gb} total={data.capacity_disk_gb} suffix=" GB"/></section><section className="panel"><div className="panel-heading"><h3>需要关注</h3><span className="tag">LIVE</span></div><div className="attention-list"><span>逾期服务<strong>{data.overdue_services}</strong></span><span>失败任务<strong>{data.failed_jobs}</strong></span><span>执行中任务<strong>{data.pending_jobs}</strong></span><span>离线节点<strong>{data.nodes-data.online_nodes}</strong></span></div></section></div>
  </section>
}

function CapacityBar({label,used,total,suffix=''}:{label:string;used:number;total:number;suffix?:string}){const rate=total?Math.min(100,Math.round(used/total*100)):0;return <div className="capacity-row"><div><span>{label}</span><strong>{used}{suffix} / {total}{suffix}</strong></div><div className="capacity-track"><i style={{width:`${rate}%`}}/></div><small>{rate}% 已预留</small></div>}

function CustomersView() {
  const [customers, setCustomers] = useState<AccountRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [updating,setUpdating]=useState('')
  const load = () => api<AccountRecord[]>('/api/v1/admin/customers').then(setCustomers).catch((err) => setError(err.message))
  useEffect(() => { void load() }, [])
  async function toggleStatus(customer:AccountRecord){const status=customer.status==='active'?'suspended':'active';if(status==='suspended'&&!window.confirm(`暂停 ${customer.display_name} 的账户并撤销其登录会话？`))return;setUpdating(customer.id);try{await api(`/api/v1/admin/customers/${customer.id}`,{method:'PATCH',body:JSON.stringify({status})});await load()}catch(err){setError(err instanceof Error?err.message:'更新失败')}finally{setUpdating('')}}

  return <section className="workspace-panel">
    <PageActions eyebrow="CUSTOMERS" title="客户账户" description="客户是订单、账单、服务和交易记录的统一归属主体。" action={() => setShowForm(true)} actionLabel="创建客户" />
    {error && <div className="form-error">{error}</div>}
    {showForm && <CustomerForm onClose={() => setShowForm(false)} onCreated={() => { setShowForm(false); load() }} />}
    <div className="table-wrap"><table><thead><tr><th>客户</th><th>类型</th><th>账单邮箱</th><th>币种</th><th>状态</th><th>创建时间</th><th></th></tr></thead><tbody>
      {customers.map((customer) => <tr key={customer.id}><td><strong>{customer.display_name}</strong><small>{customer.id}</small></td><td>{customer.kind === 'business' ? '企业' : '个人'}</td><td>{customer.billing_email}</td><td>{customer.default_currency}</td><td><StatusBadge status={customer.status}/></td><td>{new Date(customer.created_at).toLocaleString()}</td><td><button className="text-button" disabled={updating===customer.id} onClick={()=>void toggleStatus(customer)}>{customer.status==='active'?'暂停账户':'恢复账户'}</button></td></tr>)}
      {!customers.length && <tr><td colSpan={7} className="empty-state">尚未创建客户</td></tr>}
    </tbody></table></div>
  </section>
}

function CustomerForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState(''); const [saving, setSaving] = useState(false)
  async function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); setSaving(true); setError(''); const data = new FormData(event.currentTarget); try { await api('/api/v1/admin/customers', { method: 'POST', body: JSON.stringify({ kind: data.get('kind'), display_name: data.get('display_name'), billing_email: data.get('billing_email'), legal_name: data.get('legal_name'), tax_id: data.get('tax_id'), country_code: data.get('country_code'), default_currency: data.get('default_currency') }) }); onCreated() } catch (err) { setError(err instanceof Error ? err.message : '创建失败') } finally { setSaving(false) } }
  return <div className="inline-form"><div className="inline-form-heading"><div><h3>创建客户</h3><p>企业客户可额外填写法定名称和税号。</p></div><button className="icon-button" onClick={onClose}><X size={18}/></button></div><form className="form-grid" onSubmit={submit}>
    <label><span>客户类型</span><select name="kind"><option value="individual">个人</option><option value="business">企业</option></select></label><label><span>显示名称</span><input name="display_name" required /></label><label><span>账单邮箱</span><input name="billing_email" type="email" required /></label><label><span>法定名称</span><input name="legal_name" /></label><label><span>税号</span><input name="tax_id" /></label><label><span>国家/地区</span><input name="country_code" maxLength={2} defaultValue="CN" /></label><label><span>默认币种</span><select name="default_currency"><option value="CNY">CNY</option><option value="USD">USD</option></select></label>
    {error && <div className="form-error wide">{error}</div>}<div className="form-actions wide"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving}>{saving ? '创建中…' : '创建客户'}</button></div>
  </form></div>
}

function OrdersView() {
  const [orders, setOrders] = useState<OrderRecord[]>([])
  const [customers, setCustomers] = useState<AccountRecord[]>([])
  const [plans, setPlans] = useState<PlanRecord[]>([])
  const [regions, setRegions] = useState<RegionRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const load = async () => { try { const [orderRows, customerRows, planRows, regionRows] = await Promise.all([api<OrderRecord[]>('/api/v1/admin/orders'), api<AccountRecord[]>('/api/v1/admin/customers'), api<PlanRecord[]>('/api/v1/admin/plans'), api<RegionRecord[]>('/api/v1/admin/regions')]); setOrders(orderRows); setCustomers(customerRows); setPlans(planRows.filter((plan) => plan.enabled)); setRegions(regionRows) } catch (err) { setError(err instanceof Error ? err.message : '加载失败') } }
  useEffect(() => { void load() }, [])
  return <section className="workspace-panel"><PageActions eyebrow="ORDERS" title="销售订单" description="订单金额完全由服务端根据当前有效套餐价格计算。" action={() => setShowForm(true)} actionLabel="创建订单" />
    {error && <div className="form-error">{error}</div>}{showForm && <OrderForm customers={customers} plans={plans} regions={regions} onClose={() => setShowForm(false)} onCreated={() => { setShowForm(false); load() }} />}
    <div className="table-wrap"><table><thead><tr><th>订单</th><th>客户</th><th>账单</th><th>金额</th><th>状态</th><th>创建时间</th></tr></thead><tbody>
      {orders.map((order) => <tr key={order.id}><td><strong>{order.number}</strong><small>{order.id}</small></td><td>{order.customer_name}</td><td>{order.invoice_number}</td><td>{money(order.total_minor, order.currency)}</td><td><StatusBadge status={order.status}/></td><td>{new Date(order.created_at).toLocaleString()}</td></tr>)}{!orders.length && <tr><td colSpan={6} className="empty-state">尚未创建订单</td></tr>}
    </tbody></table></div>
  </section>
}

function OrderForm({ customers, plans, regions, onClose, onCreated }: { customers: AccountRecord[]; plans: PlanRecord[]; regions: RegionRecord[]; onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState(''); const [saving, setSaving] = useState(false)
  const sellablePlans = plans.filter((plan) => plan.enabled)
  const [customerID, setCustomerID] = useState(customers[0]?.id || '')
  const [planID, setPlanID] = useState(sellablePlans[0]?.id || '')
  const customer = customers.find((item) => item.id === customerID)
  const plan = sellablePlans.find((item) => item.id === planID)
  const prices = plan?.prices.filter((price) => price.currency === customer?.default_currency) || []
  const ready = customers.length > 0 && sellablePlans.length > 0 && regions.length > 0
  async function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); setSaving(true); setError(''); const data = new FormData(event.currentTarget); try { await api('/api/v1/admin/orders', { method: 'POST', body: JSON.stringify({ account_id: data.get('account_id'), items: [{ plan_id: data.get('plan_id'), region_id: data.get('region_id'), billing_cycle: data.get('billing_cycle'), quantity: Number(data.get('quantity')), configuration: { template_id: data.get('template_id') } }] }) }); onCreated() } catch (err) { setError(err instanceof Error ? err.message : '创建失败') } finally { setSaving(false) } }
  return <div className="inline-form"><div className="inline-form-heading"><div><h3>创建订单与账单</h3><p>保存后生成待支付订单和不可随价格变化的账单明细。</p></div><button className="icon-button" onClick={onClose}><X size={18}/></button></div>{!ready ? <div className="form-error">请先至少创建一个客户、已上架套餐和节点地区。</div> : <form className="form-grid" onSubmit={submit}>
    <label><span>客户</span><select name="account_id" value={customerID} onChange={(event) => setCustomerID(event.target.value)}>{customers.map((item) => <option key={item.id} value={item.id}>{item.display_name} / {item.default_currency}</option>)}</select></label><label><span>套餐</span><select name="plan_id" value={planID} onChange={(event) => setPlanID(event.target.value)}>{sellablePlans.map((item) => <option key={item.id} value={item.id}>{item.name} / {item.virtualization.toUpperCase()}</option>)}</select></label><label><span>地区</span><select name="region_id">{regions.map((region) => <option key={region.id} value={region.id}>{region.name}</option>)}</select></label><label><span>计费周期</span><select name="billing_cycle" required>{prices.map((price) => <option key={price.billing_cycle} value={price.billing_cycle}>{cycleLabel(price.billing_cycle)} · {money(price.amount_minor + price.setup_fee_minor, price.currency)}</option>)}</select></label><label><span>数量</span><input name="quantity" type="number" min="1" max="20" defaultValue="1" /></label><label><span>系统模板</span><select key={planID} name="template_id" defaultValue={plan?.default_template_id}>{plan?.allowed_template_ids.map((template) => <option key={template} value={template}>{template}</option>)}</select></label>
    {!prices.length && <div className="form-error wide">该客户币种没有可用的套餐价格，请先补充价格。</div>}{error && <div className="form-error wide">{error}</div>}<div className="form-actions wide"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving || !prices.length}>{saving ? '创建中…' : '创建订单'}</button></div>
  </form>}</div>
}

function BillingView() {
  const [invoices, setInvoices] = useState<InvoiceRecord[]>([])
  const [transactions, setTransactions] = useState<TransactionRecord[]>([])
  const [error, setError] = useState('')
  const [paying, setPaying] = useState('')
  const load = async () => { try { const [invoiceRows, transactionRows] = await Promise.all([api<InvoiceRecord[]>('/api/v1/admin/invoices'), api<TransactionRecord[]>('/api/v1/admin/transactions')]); setInvoices(invoiceRows); setTransactions(transactionRows) } catch (err) { setError(err instanceof Error ? err.message : '加载失败') } }
  useEffect(() => { void load() }, [])
  async function pay(invoice: InvoiceRecord) { if (!window.confirm(`确认已经收到 ${money(invoice.balance_minor, invoice.currency)}？此操作将创建不可修改的交易流水并触发 VPS 开通。`)) return; setPaying(invoice.id); setError(''); try { await api(`/api/v1/admin/invoices/${invoice.id}/pay`, { method: 'POST', body: JSON.stringify({ reference: '' }) }); await load() } catch (err) { setError(err instanceof Error ? err.message : '入账失败') } finally { setPaying('') } }
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">BILLING LEDGER</p><h2>账单与交易</h2><p>交易流水采用只追加模型；退款和冲正必须新增反向记录。</p></div></div>{error && <div className="form-error">{error}</div>}
    <div className="panel"><div className="panel-heading"><h3>账单</h3><span className="tag">{invoices.length} TOTAL</span></div><div className="table-wrap"><table><thead><tr><th>账单</th><th>客户</th><th>金额</th><th>余额</th><th>状态</th><th>到期</th><th></th></tr></thead><tbody>{invoices.map((invoice) => <tr key={invoice.id}><td><strong>{invoice.number}</strong></td><td>{invoice.customer_name}</td><td>{money(invoice.total_minor, invoice.currency)}</td><td>{money(invoice.balance_minor, invoice.currency)}</td><td><StatusBadge status={invoice.status}/></td><td>{new Date(invoice.due_at).toLocaleDateString()}</td><td>{invoice.status === 'open' && <button className="text-button" disabled={paying === invoice.id} onClick={() => pay(invoice)}>{paying === invoice.id ? '入账中' : '确认到账'}</button>}</td></tr>)}{!invoices.length && <tr><td colSpan={7} className="empty-state">暂无账单</td></tr>}</tbody></table></div></div>
    <div className="panel"><div className="panel-heading"><h3>不可变交易流水</h3><span className="tag">APPEND ONLY</span></div><div className="table-wrap"><table><thead><tr><th>交易</th><th>账单</th><th>客户</th><th>渠道</th><th>金额</th><th>状态</th><th>时间</th></tr></thead><tbody>{transactions.map((transaction) => <tr key={transaction.id}><td><strong>{transaction.provider_transaction_id}</strong><small>{transaction.id}</small></td><td>{transaction.invoice_number}</td><td>{transaction.customer_name}</td><td>{transaction.provider}</td><td>{money(transaction.amount_minor, transaction.currency)}</td><td><StatusBadge status={transaction.status}/></td><td>{new Date(transaction.created_at).toLocaleString()}</td></tr>)}{!transactions.length && <tr><td colSpan={7} className="empty-state">暂无交易</td></tr>}</tbody></table></div></div>
  </section>
}

function PaymentSettingsView(){
  const [settings,setSettings]=useState<PaymentSettingsRecord|null>(null);const [form,setForm]=useState({type:'disabled',generic_base_url:'',generic_secret:'',alipay_app_id:'',alipay_private_key:'',alipay_public_key:'',alipay_gateway_url:'https://openapi.alipay.com/gateway.do',epay_api_url:'',epay_partner_id:'',epay_merchant_key:'',epay_payment_type:'alipay'});const [saving,setSaving]=useState(false);const [error,setError]=useState('');const [saved,setSaved]=useState(false)
  useEffect(()=>{api<PaymentSettingsRecord>('/api/v1/admin/settings/payment').then((value)=>{setSettings(value);setForm((current)=>({...current,...value.gateway}))}).catch((err)=>setError(err.message))},[])
  const update=(key:keyof typeof form)=>(event:ChangeEvent<HTMLInputElement|HTMLSelectElement|HTMLTextAreaElement>)=>setForm((current)=>({...current,[key]:event.target.value}))
  async function submit(event:FormEvent){event.preventDefault();setSaving(true);setSaved(false);setError('');try{await api('/api/v1/admin/settings/payment',{method:'PUT',body:JSON.stringify(form)});const value=await api<PaymentSettingsRecord>('/api/v1/admin/settings/payment');setSettings(value);setForm((current)=>({...current,...value.gateway,generic_secret:'',alipay_private_key:'',alipay_public_key:'',epay_merchant_key:''}));setSaved(true)}catch(err){setError(err instanceof Error?err.message:'保存失败')}finally{setSaving(false)}}
  return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">PAYMENT GATEWAY</p><h2>支付网关</h2><p>一次启用一个收款渠道；密钥加密保存且不会回显。留空代表保留已有密钥。</p></div><StatusBadge status={form.type==='disabled'?'disabled':'online'}/></div>{error&&<div className="form-error">{error}</div>}{saved&&<div className="success-note">支付网关配置已保存并立即生效。</div>}
    <form className="panel payment-settings" onSubmit={submit}><div className="gateway-options"><label className={form.type==='disabled'?'selected':''}><input type="radio" name="gateway" checked={form.type==='disabled'} onChange={()=>setForm((v)=>({...v,type:'disabled'}))}/><strong>关闭在线支付</strong><span>仅允许后台确认到账</span></label><label className={form.type==='alipay_f2f'?'selected':''}><input type="radio" name="gateway" checked={form.type==='alipay_f2f'} onChange={()=>setForm((v)=>({...v,type:'alipay_f2f'}))}/><strong>支付宝当面付</strong><span>官方预下单 / RSA2</span></label><label className={form.type==='epay'?'selected':''}><input type="radio" name="gateway" checked={form.type==='epay'} onChange={()=>setForm((v)=>({...v,type:'epay'}))}/><strong>易支付</strong><span>彩虹易支付兼容协议</span></label><label className={form.type==='generic'?'selected':''}><input type="radio" name="gateway" checked={form.type==='generic'} onChange={()=>setForm((v)=>({...v,type:'generic'}))}/><strong>通用 HMAC</strong><span>自有外部收银台</span></label></div>
      {form.type==='generic'&&<div className="form-grid"><label><span>收银台地址</span><input type="url" value={form.generic_base_url} onChange={update('generic_base_url')} required/></label><label><span>签名密钥 {settings?.gateway.generic_secret_configured?'（已配置）':''}</span><input type="password" value={form.generic_secret} onChange={update('generic_secret')} placeholder="留空保留已有密钥"/></label></div>}
      {form.type==='alipay_f2f'&&<div className="form-grid"><label><span>应用 App ID</span><input value={form.alipay_app_id} onChange={update('alipay_app_id')} required/></label><label><span>网关地址</span><input type="url" value={form.alipay_gateway_url} onChange={update('alipay_gateway_url')} required/></label><label className="wide"><span>应用私钥 {settings?.gateway.alipay_private_key_configured?'（已配置）':''}</span><textarea rows={5} value={form.alipay_private_key} onChange={update('alipay_private_key')} placeholder="PKCS#1 / PKCS#8，留空保留已有密钥"/></label><label className="wide"><span>支付宝公钥 {settings?.gateway.alipay_public_key_configured?'（已配置）':''}</span><textarea rows={5} value={form.alipay_public_key} onChange={update('alipay_public_key')} placeholder="留空保留已有公钥"/></label></div>}
      {form.type==='epay'&&<div className="form-grid"><label><span>接口地址</span><input type="url" value={form.epay_api_url} onChange={update('epay_api_url')} placeholder="https://pay.example.com/" required/></label><label><span>商户 ID（PID）</span><input value={form.epay_partner_id} onChange={update('epay_partner_id')} required/></label><label><span>支付通道</span><select value={form.epay_payment_type} onChange={update('epay_payment_type')}><option value="alipay">支付宝</option><option value="wxpay">微信支付</option><option value="qqpay">QQ 钱包</option></select></label><label><span>商户密钥 {settings?.gateway.epay_merchant_key_configured?'（已配置）':''}</span><input type="password" value={form.epay_merchant_key} onChange={update('epay_merchant_key')} placeholder="留空保留已有密钥"/></label></div>}
      {form.type!=='disabled'&&settings&&<div className="callback-box"><span>异步回调地址</span><code>{settings.callbacks[form.type]}</code></div>}<div className="form-actions"><button className="primary-button compact" disabled={saving}>{saving?'保存中…':'保存并启用'}</button></div></form>
  </section>
}

function ServicesView() {
  const [services, setServices] = useState<ServiceRecord[]>([])
  const [jobs, setJobs] = useState<ProvisioningJobRecord[]>([])
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState('')
  const load = async () => {
    try {
      const [serviceRows, jobRows] = await Promise.all([api<ServiceRecord[]>('/api/v1/admin/services'), api<ProvisioningJobRecord[]>('/api/v1/admin/jobs')])
      setServices(serviceRows); setJobs(jobRows); setError('')
    } catch (err) { setError(err instanceof Error ? err.message : '加载失败') }
  }
  useEffect(() => { void load(); const timer = window.setInterval(() => void load(), 10000); return () => window.clearInterval(timer) }, [])
  async function retry(job: ProvisioningJobRecord) {
    if (!window.confirm(`重新执行 ${job.instance_name} 的开通任务？`)) return
    setRetrying(job.id); setError('')
    try { await api(`/api/v1/admin/jobs/${job.id}/retry`, { method: 'POST' }); await load() }
    catch (err) { setError(err instanceof Error ? err.message : '重试失败') }
    finally { setRetrying('') }
  }
  return <section className="workspace-panel">
    <div className="page-actions"><div><p className="eyebrow">AUTOMATION</p><h2>VPS 服务与自动化任务</h2><p>开通执行、失败重试和 CLICD 实际运行状态每 10 秒自动刷新。</p></div><button className="secondary-button" onClick={() => void load()}><RefreshCw size={15}/>刷新</button></div>
    {error && <div className="form-error">{error}</div>}
    <div className="panel"><div className="panel-heading"><h3>服务实例</h3><span className="tag">{services.length} SERVICES</span></div><div className="table-wrap"><table><thead><tr><th>实例</th><th>客户 / 套餐</th><th>节点 / 地区</th><th>业务状态</th><th>运行状态</th><th>网络</th><th>下次到期</th></tr></thead><tbody>
      {services.map((service) => <tr key={service.id}><td><strong>{service.instance_name}</strong><small>{service.id}</small></td><td>{service.customer_name}<small>{service.plan_name}</small></td><td>{service.node_name || '等待调度'}<small>{service.region_name}</small></td><td><StatusBadge status={service.status}/></td><td><StatusBadge status={service.runtime_status}/>{service.last_reconcile_error && <small title={service.last_reconcile_error}>对账异常：{service.last_reconcile_error}</small>}</td><td>{service.primary_ipv4 || '—'}<small>{service.primary_ipv6}</small></td><td>{service.next_due_at ? new Date(service.next_due_at).toLocaleDateString() : '—'}</td></tr>)}
      {!services.length && <tr><td colSpan={7} className="empty-state">支付订单后，服务会自动进入开通队列</td></tr>}
    </tbody></table></div></div>
    <div className="panel"><div className="panel-heading"><h3>任务队列</h3><span className="tag">LEASED WORKER</span></div><div className="table-wrap"><table><thead><tr><th>实例</th><th>动作</th><th>状态</th><th>尝试</th><th>下次执行</th><th>错误</th><th></th></tr></thead><tbody>
      {jobs.map((job) => <tr key={job.id}><td><strong>{job.instance_name}</strong><small>{job.customer_name}</small></td><td>{job.action}</td><td><StatusBadge status={job.status}/></td><td>{job.attempts} / 8</td><td>{new Date(job.available_at).toLocaleString()}</td><td className="error-cell"><JobError value={job.last_error}/></td><td>{['failed', 'dead'].includes(job.status) && <button className="text-button" disabled={retrying === job.id} onClick={() => retry(job)}><RefreshCw size={14}/>{retrying === job.id ? '提交中' : '人工重试'}</button>}</td></tr>)}
      {!jobs.length && <tr><td colSpan={7} className="empty-state">暂无自动化任务</td></tr>}
    </tbody></table></div></div>
  </section>
}

function NodesView() {
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [testing, setTesting] = useState('')
  const load = () => api<NodeRecord[]>('/api/v1/admin/nodes').then(setNodes).catch((err) => setError(err.message))
  useEffect(() => { void load() }, [])

  async function testNode(id: string) {
    setTesting(id); setError('')
    try { await api(`/api/v1/admin/nodes/${id}/test`, { method: 'POST' }); await load() }
    catch (err) { setError(err instanceof Error ? err.message : '节点测试失败') }
    finally { setTesting('') }
  }

  return <section className="workspace-panel">
    <PageActions eyebrow="PROVIDER INTEGRATIONS" title="节点对接" description="统一管理虚拟化面板连接。当前支持 CLICD，后续适配器不会改变套餐、调度与账务模型。" action={() => setShowForm(true)} actionLabel="新增对接" />
    {error && <div className="form-error" role="alert">{error}</div>}
    {showForm && <NodeForm onClose={() => setShowForm(false)} onCreated={() => { setShowForm(false); load() }} />}
    <div className="provider-strip"><article className="available"><strong>CLICD</strong><span>LXC / KVM · 已可用</span></article><article><strong>Proxmox VE</strong><span>计划适配</span></article><article><strong>Virtualizor</strong><span>计划适配</span></article></div>
    <div className="table-wrap"><table><thead><tr><th>节点</th><th>对接方式</th><th>地区</th><th>虚拟化</th><th>可调度总容量</th><th>状态</th><th>最后在线</th><th></th></tr></thead><tbody>
      {nodes.map((node) => <tr key={node.id}><td><strong>{node.name}</strong><small>{node.base_url}</small></td><td><span className="tag">{node.provider_type.toUpperCase()}</span></td><td>{node.region_name}<small>{node.region_code}</small></td><td>{node.virtualization_types.join(' / ').toUpperCase()}</td><td>{node.capacity_vcpu} vCPU<small>{node.capacity_ram_mb} MB / {node.capacity_disk_gb} GB</small></td><td><StatusBadge status={node.status}/></td><td>{node.last_seen_at ? new Date(node.last_seen_at).toLocaleString() : '—'}</td><td><button className="text-button" onClick={() => testNode(node.id)} disabled={testing === node.id}><RefreshCw size={14}/>{testing === node.id ? '测试中' : '测试连接'}</button></td></tr>)}
      {!nodes.length && <tr><td colSpan={8} className="empty-state">尚未接入节点</td></tr>}
    </tbody></table></div>
  </section>
}

function NodeForm({ onClose, onCreated }: { onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState(''); const [saving, setSaving] = useState(false)
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setSaving(true); setError('')
    const data = new FormData(event.currentTarget)
    try {
      await api('/api/v1/admin/nodes', { method: 'POST', body: JSON.stringify({ provider_type: data.get('provider_type'), region_code: data.get('region_code'), region_name: data.get('region_name'), name: data.get('name'), base_url: data.get('base_url'), api_key: data.get('api_key'), virtualization_types: data.getAll('virtualization_types') }) })
      onCreated()
    } catch (err) { setError(err instanceof Error ? err.message : '接入失败') }
    finally { setSaving(false) }
  }
  return <div className="inline-form"><div className="inline-form-heading"><div><h3>新增节点对接</h3><p>选择适配器并验证连接，凭据只以加密形式保存。</p></div><button className="icon-button" onClick={onClose}><X size={18}/></button></div><form className="form-grid" onSubmit={submit}>
    <label><span>对接方式</span><select name="provider_type"><option value="clicd">CLICD（LXC / KVM）</option></select></label><label><span>节点名称</span><input name="name" required placeholder="node-sha-01" /></label><label><span>地区代码</span><input name="region_code" required placeholder="SHA" /></label><label><span>地区名称</span><input name="region_name" required placeholder="上海" /></label><label><span>API 地址</span><input name="base_url" type="url" required placeholder="https://10.0.0.10:8999" /></label><label><span>API Key</span><input name="api_key" type="password" required autoComplete="off" /></label><fieldset className="wide"><legend>虚拟化类型</legend><label className="checkbox"><input type="checkbox" name="virtualization_types" value="lxc" defaultChecked/> LXC</label><label className="checkbox"><input type="checkbox" name="virtualization_types" value="kvm"/> KVM</label></fieldset>
    {error && <div className="form-error wide">{error}</div>}<div className="form-actions wide"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving}>{saving ? '验证并保存…' : '验证并保存'}</button></div>
  </form></div>
}

function HostsView({onOpen}:{onOpen?:(id:string)=>void}){const [hosts,setHosts]=useState<HostProbeRecord[]>([]);const [error,setError]=useState('');const load=()=>api<HostProbeRecord[]>('/api/v1/admin/hosts').then(setHosts).catch((err)=>setError(err.message));useEffect(()=>{void load();const timer=window.setInterval(()=>void load(),30000);return()=>window.clearInterval(timer)},[]);return <section className="workspace-panel"><div className="page-actions"><div><p className="eyebrow">HOST TELEMETRY</p><h2>宿主机探针</h2><p>一级页面显示调度概览；CLICD 节点可进入详情查看四个官方探针接口的全部数据。</p></div><button className="secondary-button" onClick={()=>void load()}><RefreshCw size={15}/>刷新</button></div>{error&&<div className="form-error">{error}</div>}<div className="host-grid">{hosts.map((host)=><article className="host-card" key={host.id}><header><div><span className="tag">{host.provider_type.toUpperCase()}</span><h3>{host.name}</h3><small>{host.region_name} · {host.base_url}</small></div><StatusBadge status={host.status}/></header><div className="host-summary"><span><strong>{host.capacity_vcpu-host.reserved_vcpu}</strong> / {host.capacity_vcpu}<small>空闲 vCPU</small></span><span><strong>{host.capacity_ram_mb-host.reserved_ram_mb}</strong> / {host.capacity_ram_mb}<small>空闲内存 MB</small></span><span><strong>{host.capacity_disk_gb-host.reserved_disk_gb}</strong> / {host.capacity_disk_gb}<small>空闲磁盘 GB</small></span></div><CapacityBar label="CPU 预留" used={host.reserved_vcpu} total={host.capacity_vcpu}/><CapacityBar label="内存预留" used={host.reserved_ram_mb} total={host.capacity_ram_mb}/><footer><span>{host.virtualization_types.join(' / ').toUpperCase()}</span><span>最后在线：{host.last_seen_at?new Date(host.last_seen_at).toLocaleString():'从未'}</span></footer>{host.provider_type==='clicd'&&onOpen&&<button className="secondary-button host-detail-button" onClick={()=>onOpen(host.id)}>查看完整探针 <ChevronRight size={15}/></button>}</article>)}{!hosts.length&&<div className="empty-card">暂无宿主机探针数据，请先完成节点对接。</div>}</div></section>}

function HostDetailView({id,onBack}:{id:string;onBack:()=>void}){const [detail,setDetail]=useState<HostProbeDetailRecord|null>(null);const [error,setError]=useState('');const [loading,setLoading]=useState(true);const load=()=>{setLoading(true);setError('');api<HostProbeDetailRecord>(`/api/v1/admin/hosts/${id}/probe`).then(setDetail).catch((err)=>setError(err.message)).finally(()=>setLoading(false))};useEffect(()=>{void load()},[id]);const labels:Record<string,string>={dashboard:'容器概览 /api/v1/dashboard',host_info:'实时资源 /api/v1/host-info',host_history:'历史指标 /api/v1/host-history',host_report:'硬件与系统报告 /api/v1/host-report'};return <section className="workspace-panel"><div className="page-actions"><div><button className="button-link detail-back" onClick={onBack}><ArrowLeft size={15}/>返回宿主机探针</button><p className="eyebrow">CLICD HOST DETAIL</p><h2>{detail?.node.name||'宿主机详情'}</h2><p>{detail?`${detail.node.region_name} · ${detail.node.base_url} · 获取于 ${new Date(detail.fetched_at).toLocaleString()}`:'正在读取 CLICD 四个探针接口…'}</p></div><button className="secondary-button" disabled={loading} onClick={load}><RefreshCw size={15}/>{loading?'读取中…':'重新读取'}</button></div>{error&&<div className="form-error">{error}</div>}{detail&&<div className="probe-sections">{Object.entries(labels).map(([key,label])=><details className="probe-section" key={key} open={key==='dashboard'||key==='host_info'}><summary><strong>{label}</strong>{detail.errors[key as keyof typeof detail.errors]?<StatusBadge status="error"/>:<StatusBadge status="online"/>}</summary>{detail.errors[key as keyof typeof detail.errors]?<div className="form-error">{detail.errors[key as keyof typeof detail.errors]}</div>:<pre>{JSON.stringify(detail.sources[key as keyof typeof detail.sources],null,2)}</pre>}</details>)}</div>}</section>}

function PlansView() {
  const [plans, setPlans] = useState<PlanRecord[]>([]); const [showForm, setShowForm] = useState(false); const [editing, setEditing] = useState<PlanRecord|null>(null); const [error, setError] = useState('')
  const load = () => api<PlanRecord[]>('/api/v1/admin/plans').then(setPlans).catch((err) => setError(err.message))
  useEffect(() => { void load() }, [])
  async function toggle(plan: PlanRecord) { try { await api(`/api/v1/admin/plans/${plan.id}`, { method: 'PATCH', body: JSON.stringify({ enabled: !plan.enabled }) }); load() } catch (err) { setError(err instanceof Error ? err.message : '更新失败') } }
  const closeForm=()=>{setShowForm(false);setEditing(null)}
  return <section className="workspace-panel"><PageActions eyebrow="CATALOG" title="商品套餐" description="网络策略随套餐下发，修改会生成新版本且不影响历史订单。" action={() => {setEditing(null);setShowForm(true)}} actionLabel="创建套餐" />
    {error && <div className="form-error">{error}</div>}{showForm && <PlanForm plan={editing} onClose={closeForm} onSaved={() => { closeForm(); load() }} />}
    <div className="plan-grid">{plans.map((plan) => <article className={plan.enabled ? 'plan-card' : 'plan-card disabled'} key={plan.id}><div className="plan-card-top"><span className="tag">{plan.virtualization.toUpperCase()}</span><StatusBadge status={plan.enabled ? 'online' : 'disabled'}/></div><h3>{plan.name}</h3><small>{plan.code} · v{plan.version}</small><div className="spec-line"><strong>{plan.vcpu}</strong> vCPU <strong>{plan.ram_mb}</strong> MB <strong>{plan.disk_gb}</strong> GB</div><small>默认模板：{plan.default_template_id}</small><small className="plan-network">网络：{[plan.assign_nat&&`NAT×${plan.port_mapping_count}`,plan.assign_ipv4&&`IPv4×${plan.ipv4_count}`,plan.assign_ipv6&&`IPv6×${plan.ipv6_count}`].filter(Boolean).join(' / ')}</small><div className="price-line">{plan.prices[0] ? <><strong>¥{(plan.prices[0].amount_minor / 100).toFixed(2)}</strong><span>/ {cycleLabel(plan.prices[0].billing_cycle)}</span></> : '无价格'}</div><div className="plan-actions"><button className="secondary-button" onClick={() => {setEditing(plan);setShowForm(true)}}>编辑套餐</button><button className="secondary-button" onClick={() => toggle(plan)}>{plan.enabled ? '下架套餐' : '重新上架'}</button></div></article>)}{!plans.length && <div className="empty-card">尚未创建套餐</div>}</div>
  </section>
}

function PlanForm({ plan, onClose, onSaved }: { plan: PlanRecord|null; onClose: () => void; onSaved: () => void }) {
  const [error, setError] = useState(''); const [saving, setSaving] = useState(false); const [templates,setTemplates]=useState<AvailableTemplateRecord[]>([]); const [loadingTemplates,setLoadingTemplates]=useState(true)
  const [virtualization,setVirtualization]=useState<'lxc'|'kvm'>(plan?.virtualization||'lxc');const [allowed,setAllowed]=useState<string[]>(plan?.allowed_template_ids||[]);const [defaultTemplate,setDefaultTemplate]=useState(plan?.default_template_id||'')
  useEffect(()=>{api<AvailableTemplateRecord[]>('/api/v1/admin/templates').then(setTemplates).catch((err)=>setError(err.message)).finally(()=>setLoadingTemplates(false))},[])
  const visibleTemplates=templates.filter((item)=>item.virtualization===virtualization)
  const templateLabel=(id:string)=>{const item=templates.find((candidate)=>candidate.id===id);return item?`${item.name}${item.release&&!item.name.includes(item.release)?` ${item.release}`:''} · ${item.arch}`:id}
  function toggleTemplate(id:string,checked:boolean){const next=checked?[...new Set([...allowed,id])]:allowed.filter((item)=>item!==id);setAllowed(next);if(!next.includes(defaultTemplate))setDefaultTemplate(next[0]||'')}
  async function submit(event: FormEvent<HTMLFormElement>) { event.preventDefault(); if(!allowed.length||!defaultTemplate){setError('请至少勾选一个可用模板并设置默认模板');return} setSaving(true); setError(''); const data = new FormData(event.currentTarget); const body={ code: data.get('code'), name: data.get('name'), virtualization, vcpu: Number(data.get('vcpu')), ram_mb: Number(data.get('ram_mb')), disk_gb: Number(data.get('disk_gb')), traffic_gb: Number(data.get('traffic_gb')), network_down_mbps: Number(data.get('network_down_mbps')), network_up_mbps: Number(data.get('network_up_mbps')), snapshot_limit: Number(data.get('snapshot_limit')), assign_nat:data.get('assign_nat')==='on',port_mapping_count:Number(data.get('port_mapping_count')),assign_ipv4:data.get('assign_ipv4')==='on',ipv4_count:Number(data.get('ipv4_count')),assign_ipv6:data.get('assign_ipv6')==='on',ipv6_count:Number(data.get('ipv6_count')),default_template_id: defaultTemplate, allowed_template_ids: allowed, enabled: plan?.enabled??true, prices: [{ currency: 'CNY', billing_cycle: data.get('billing_cycle'), amount_minor: Math.round(Number(data.get('price')) * 100), setup_fee_minor: 0 }] }; try { await api(plan?`/api/v1/admin/plans/${plan.id}`:'/api/v1/admin/plans', { method: plan?'PUT':'POST', body: JSON.stringify(body) }); onSaved() } catch (err) { setError(err instanceof Error ? err.message : '保存失败') } finally { setSaving(false) } }
  const price=plan?.prices[0]
  return <div className="inline-form"><div className="inline-form-heading"><div><h3>{plan?'编辑':'创建'} VPS 套餐</h3><p>模板来自在线 CLICD 节点；网络策略由套餐统一下发给客户实例。</p></div><button className="icon-button" onClick={onClose}><X size={18}/></button></div><form className="form-grid" onSubmit={submit}>
    <label><span>套餐编码</span><input name="code" required placeholder="LXC-START" defaultValue={plan?.code}/></label><label><span>名称</span><input name="name" required placeholder="轻量入门型" defaultValue={plan?.name}/></label><label><span>虚拟化</span><select name="virtualization" value={virtualization} onChange={(event)=>{const value=event.target.value as 'lxc'|'kvm';setVirtualization(value);setAllowed([]);setDefaultTemplate('')}}><option value="lxc">LXC</option><option value="kvm">KVM</option></select></label><label><span>vCPU</span><input name="vcpu" type="number" min="1" defaultValue={plan?.vcpu||1} required /></label><label><span>内存 MB</span><input name="ram_mb" type="number" min="128" defaultValue={plan?.ram_mb||512} required /></label><label><span>磁盘 GB</span><input name="disk_gb" type="number" min="1" defaultValue={plan?.disk_gb||10} required /></label><label><span>月流量 GB</span><input name="traffic_gb" type="number" min="0" defaultValue={plan?.traffic_gb??1024} /></label><label><span>下行 Mbps</span><input name="network_down_mbps" type="number" min="0" defaultValue={plan?.network_down_mbps??100} /></label><label><span>上行 Mbps</span><input name="network_up_mbps" type="number" min="0" defaultValue={plan?.network_up_mbps??100} /></label><label><span>快照数</span><input name="snapshot_limit" type="number" min="0" defaultValue={plan?.snapshot_limit??1} /></label><label><span>计费周期</span><select name="billing_cycle" defaultValue={price?.billing_cycle||'monthly'}><option value="monthly">月付</option><option value="quarterly">季付</option><option value="semiannual">半年付</option><option value="annual">年付</option></select></label><label><span>售价（元）</span><input name="price" type="number" min="0.01" step="0.01" defaultValue={((price?.amount_minor||1900)/100).toFixed(2)} required /></label>
    <fieldset className="wide network-policy"><legend>套餐网络策略</legend><label className="checkbox"><input name="assign_nat" type="checkbox" defaultChecked={plan?.assign_nat??true}/>NAT IPv4</label><label><span>NAT 端口映射数</span><input name="port_mapping_count" type="number" min="0" max="64" defaultValue={plan?.port_mapping_count??0}/></label><label className="checkbox"><input name="assign_ipv4" type="checkbox" defaultChecked={plan?.assign_ipv4??false}/>独立公网 IPv4</label><label><span>IPv4 数量</span><input name="ipv4_count" type="number" min="1" max="64" defaultValue={plan?.ipv4_count??1}/></label><label className="checkbox"><input name="assign_ipv6" type="checkbox" defaultChecked={plan?.assign_ipv6??true}/>IPv6</label><label><span>IPv6 数量</span><input name="ipv6_count" type="number" min="1" max="64" defaultValue={plan?.ipv6_count??1}/></label></fieldset>
    <fieldset className="wide"><legend>允许的系统模板（显示节点实际可用版本）</legend>{loadingTemplates?<div className="template-empty">正在从 CLICD 节点读取模板…</div>:<div className="template-picker">{visibleTemplates.map((item)=><label className={allowed.includes(item.id)?'template-option selected':'template-option'} key={`${item.virtualization}:${item.id}`}><input type="checkbox" checked={allowed.includes(item.id)} onChange={(event)=>toggleTemplate(item.id,event.target.checked)}/><span><strong>{templateLabel(item.id)}</strong><small>{item.description||item.id}</small><em>{item.node_names.join('、')}</em></span></label>)}{!visibleTemplates.length&&<div className="template-empty">在线节点没有已启用且已下载的 {virtualization.toUpperCase()} 模板。</div>}{plan?.allowed_template_ids.filter((id)=>!visibleTemplates.some((item)=>item.id===id)).map((id)=><label className="template-option unavailable" key={id}><input type="checkbox" checked={allowed.includes(id)} onChange={(event)=>toggleTemplate(id,event.target.checked)}/><span><strong>{id}</strong><small>当前节点未返回此模板；保留后仍可保存。</small></span></label>)}</div>}</fieldset>
    <label className="wide"><span>默认系统模板</span><select name="default_template_id" value={defaultTemplate} onChange={(event)=>setDefaultTemplate(event.target.value)} required><option value="">请先勾选模板</option>{allowed.map((id)=><option key={id} value={id}>{templateLabel(id)}</option>)}</select></label>
    {error && <div className="form-error wide">{error}</div>}<div className="form-actions wide"><button type="button" className="secondary-button" onClick={onClose}>取消</button><button className="primary-button" disabled={saving}>{saving ? '保存中…' : plan?'保存修改':'创建套餐'}</button></div>
  </form></div>
}

function PageActions({ eyebrow, title, description, action, actionLabel }: { eyebrow: string; title: string; description: string; action: () => void; actionLabel: string }) { return <div className="page-actions"><div><p className="eyebrow">{eyebrow}</p><h2>{title}</h2><p>{description}</p></div><button className="primary-button compact" onClick={action}><Plus size={16}/>{actionLabel}</button></div> }
function JobError({value}:{value?:string}) { if(!value)return <span className="job-error-empty">—</span>;if(value.length<=100)return <span className="job-error-text">{value}</span>;return <details className="job-error-details"><summary>{value.slice(0,100)}…<b>查看完整错误</b></summary><pre>{value}</pre></details> }
function StatusBadge({ status }: { status: string }) { const labels: Record<string, string> = { online: '在线', active: '正常', disabled: '已下架', offline: '离线', pending_payment: '待支付', open: '待支付', paid: '已支付', fulfilling: '开通中', provisioning: '开通中', completed: '已完成', succeeded: '成功', failed: '等待重试', pending: '排队中', running: '运行中', creating: '创建中', stopped: '已关机', overdue: '已逾期', suspended: '已暂停', terminating: '待删除', terminated: '已删除', review: '需审核', uncollectible: '无法收回', missing: '实例缺失', unknown: '未知', error: '异常', dead: '需人工处理' }; return <span className={`status-badge ${status}`}>{labels[status] ?? status}</span> }
function cycleLabel(cycle: string) { return ({ monthly: '月', quarterly: '季', semiannual: '半年', annual: '年' } as Record<string, string>)[cycle] ?? cycle }
function ticketStatusLabel(status:string){return ({open:'待处理',customer_reply:'客户已回复',staff_reply:'客服已回复',resolved:'已解决',closed:'已关闭'} as Record<string,string>)[status]||status}
function ticketAuthorLabel(type:string){return ({customer:'客户',staff:'客服',system:'系统'} as Record<string,string>)[type]||type}
function viewTitle(view: View) { return ({ overview: '运营概览', customers: '客户', orders: '订单', billing: '账单与交易', payment: '支付网关', services: 'VPS 服务', nodes: '节点对接', hosts: '宿主机探针', plans: '商品套餐', support: '客户工单', audit: '审计日志', security: '登录安全' } as Record<View, string>)[view] }
function money(amountMinor: number, currency: string) { return new Intl.NumberFormat('zh-CN', { style: 'currency', currency }).format(amountMinor / 100) }
