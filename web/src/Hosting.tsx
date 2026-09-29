import { FormEvent, useEffect, useState } from 'react'
import { Copy, MapPin, MessagesSquare, Plus, RefreshCw, Server, Store, Ticket, TicketPercent, X } from 'lucide-react'
import {
  api,
  type ChatRoomRecord,
  type CustomerIdentity,
  type HostedNodeRecord,
  type HostingRecord,
  type MarketRecord,
  type NodeImageRecord,
  type OrderRecord,
  type PaymentIntentRecord,
  type PlanRecord,
  type StockCapacityRecord,
  type TicketDetailRecord,
  type TicketRecord,
  type WalletRecord,
} from './api'
import ChatRoom from './ChatRoom'
import { CouponField, CouponManager } from './Coupons'
import { ReportDialog } from './Reports'
import { TicketConversation, ticketStatusLabel } from './shared/ui'
import { ticketRequestBody, useAttachmentLimit } from './TicketAttachments'
import { walletMoney } from './Wallet'
import { formatDate, formatTime } from './shared/time'
import { cycleName, cycleOrder, CyclePriceFields, priceLeft, readCyclePrices } from './shared/cycles'
import { readStock, stockLeft, StockField, StockTag } from './shared/stock'
import { DiskIOFields, diskIOText, readDiskIO } from './shared/diskio'
import { OvercommitDialog, SupplyDetails, overcommitText } from './Supply'

type Tab = 'market' | 'mine' | 'coupons' | 'tickets' | 'chat'
const tabs: [Tab, string, typeof Store][] = [
  ['market', '托管市场', Store],
  ['mine', '我的母机', Server],
  ['coupons', '优惠码', TicketPercent],
  ['tickets', '托管工单', Ticket],
  ['chat', '聊天室', MessagesSquare],
]

const virtNames: Record<string, string> = { lxc: 'LXC 容器', podman: 'Podman 容器', kvm: 'KVM' }

function tabFromURL(): Tab {
  const value = new URLSearchParams(window.location.search).get('tab') as Tab
  return tabs.some(([id]) => id === value) ? value : 'market'
}

export default function HostingCenter({ customer }: { customer: CustomerIdentity }) {
  const [tab, setTab] = useState<Tab>(tabFromURL)
  const select = (next: Tab) => {
    setTab(next)
    window.history.replaceState(null, '', `/portal/hosting?tab=${next}`)
  }
  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">HOSTING CENTER</p>
          <h2>托管中心</h2>
          <p>把闲置服务器通过 Hatch Agent 接入平台，自定套餐和价格出售给其他用户；平台作为中间方托管资金并按日结算。</p>
        </div>
      </div>
      <div className="segmented" role="tablist">
        {tabs.map(([id, label, Icon]) => (
          <button key={id} role="tab" aria-selected={tab === id} className={tab === id ? 'active' : ''} onClick={() => select(id)}>
            <Icon size={15} />
            {label}
          </button>
        ))}
      </div>
      {tab === 'market' && <Market customer={customer} />}
      {tab === 'mine' && <MyNodes />}
      {tab === 'coupons' && <HostCoupons />}
      {tab === 'tickets' && <HostTickets />}
      {tab === 'chat' && <ChatRooms />}
    </section>
  )
}

function lastSeen(node: HostedNodeRecord) {
  if (node.status === 'online') return '在线'
  return node.last_seen_at ? `离线（最后在线 ${formatTime(node.last_seen_at)}）` : '离线'
}

function planPrice(plan: PlanRecord) {
  const sorted = [...plan.prices].sort((a, b) => cycleOrder(a.billing_cycle) - cycleOrder(b.billing_cycle))
  const first = sorted.find(price => price.billing_cycle === 'monthly') ?? sorted[0]
  if (!first) return '暂无报价'
  const more = plan.prices.length > 1 ? ` 等 ${plan.prices.length} 种周期` : ''
  return `${walletMoney(first.amount_minor, first.currency)} / ${cycleName(first.billing_cycle)}${more}`
}

function PlanSpecs({ plan }: { plan: PlanRecord }) {
  return (
    <div className="shop-specs">
      <span>{plan.vcpu} vCPU</span>
      <span>{plan.ram_mb} MB 内存</span>
      <span>{plan.disk_gb} GB 磁盘</span>
      <span>{plan.traffic_gb ? `${plan.traffic_gb} GB 流量` : '不限流量'}</span>
      <span>{plan.network_down_mbps ? `${plan.network_down_mbps}/${plan.network_up_mbps} Mbps` : '不限带宽'}</span>
      {diskIOText(plan) && <span>{diskIOText(plan)}</span>}
      <span>NAT × {plan.port_mapping_count}</span>
    </div>
  )
}

function PlanTerms({ plan }: { plan: PlanRecord }) {
  return (
    <div className="plan-terms">
      <span className={plan.early_refund ? 'tag success' : 'tag'}>
        {plan.early_refund ? '1 小时内且流量未超 1GB 可全额退款' : '按剩余天数比例退款'}
      </span>
      {!!plan.purchase_limit && <span className="tag">每人限购 {plan.purchase_limit} 台</span>}
    </div>
  )
}

// ---- Market ----

function Market({ customer }: { customer: CustomerIdentity }) {
  const [market, setMarket] = useState<MarketRecord | null>(null)
  const [buying, setBuying] = useState<{ node: HostedNodeRecord; plan: PlanRecord } | null>(null)
  const [reporting, setReporting] = useState<HostedNodeRecord | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<MarketRecord>('/api/v1/customer/market')
      .then(setMarket)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  return (
    <>
      {error && <div className="form-error">{error}</div>}
      <div className="note-banner">
        托管母机由其他用户提供，机主拥有服务器的 root 权限。付款由平台托管、按天结算给机主；母机离线满 24 小时或机主下架时，按实例剩余价值的 2 倍补偿到您的余额。
      </div>
      <div className="market-grid">
        {market?.nodes.map(node => (
          <article key={node.id} className="panel market-node">
            <div className="panel-heading">
              <div>
                <h3>{node.name}</h3>
                <small>
                  机主 {node.owner_name}
                  {node.mine ? '（我自己）' : ''}
                </small>
              </div>
              <span className={node.status === 'online' ? 'tag success' : 'tag danger'}>{node.status === 'online' ? '在线' : '离线'}</span>
            </div>
            <dl className="market-facts">
              <div>
                <dt><MapPin size={13} /> 位置</dt>
                <dd>{node.region_name} · {node.location}</dd>
              </div>
              <div>
                <dt>线路</dt>
                <dd>{node.line_description}</dd>
              </div>
              <div>
                <dt>母机到期</dt>
                <dd>{node.expires_at || '—'}</dd>
              </div>
              <div>
                <dt>月流量限额</dt>
                <dd>{node.traffic_quota_gb ? `${node.traffic_quota_gb} GB（整机）` : '不限'}</dd>
              </div>
              <div>
                <dt>剩余可售</dt>
                <dd>{node.free_vcpu} 核 · {node.free_ram_mb} MB · {node.free_disk_gb} GB</dd>
              </div>
              <div>
                <dt>超售</dt>
                <dd>{overcommitText(node.overcommit)}</dd>
              </div>
            </dl>
            <SupplyDetails node={node} sellable={{ vcpu: node.capacity_vcpu, ram_mb: node.capacity_ram_mb, disk_gb: node.capacity_disk_gb }} />
            {!node.mine && (
              <button className="text-button report-link" onClick={() => setReporting(node)}>
                举报资源不符或超售
              </button>
            )}
            <div className="market-plans">
              {node.plans.map(plan => (
                <div key={plan.id} className="market-plan">
                  <div>
                    <strong>{plan.name}</strong>
                    <span className="tag">{virtNames[plan.virtualization] || plan.virtualization}</span>
                    <StockTag plan={plan} />
                  </div>
                  <PlanSpecs plan={plan} />
                  {plan.description && <p className="plan-description">{plan.description}</p>}
                  <PlanTerms plan={plan} />
                  <div className="market-plan-buy">
                    <strong>{planPrice(plan)}</strong>
                    <button
                      className="primary-button compact"
                      disabled={node.mine || node.status !== 'online' || !!node.health_hold_reason || stockLeft(plan) === 0}
                      onClick={() => setBuying({ node, plan })}
                    >
                      {stockLeft(plan) === 0 ? '已售罄' : '购买'}
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </article>
        ))}
        {market && !market.nodes.length && <div className="empty-card">托管市场暂时没有在售母机。</div>}
      </div>
      {reporting && <ReportDialog nodeID={reporting.id} nodeName={reporting.name} onClose={() => setReporting(null)} />}
      {buying && <BuyDialog customer={customer} node={buying.node} plan={buying.plan} onClose={() => setBuying(null)} onDone={() => void load()} />}
    </>
  )
}

function BuyDialog({
  customer,
  node,
  plan,
  onClose,
  onDone,
}: {
  customer: CustomerIdentity
  node: HostedNodeRecord
  plan: PlanRecord
  onClose: () => void
  onDone: () => void
}) {
  const prices = plan.prices
    .filter(price => price.currency === customer.default_currency)
    .sort((a, b) => cycleOrder(a.billing_cycle) - cycleOrder(b.billing_cycle))
  const [cycle, setCycle] = useState((prices.find(item => priceLeft(item) !== 0) ?? prices[0])?.billing_cycle || 'monthly')
  const [template, setTemplate] = useState(plan.default_template_id)
  const [coupon, setCoupon] = useState('')
  const [discount, setDiscount] = useState(0)
  const [order, setOrder] = useState<OrderRecord | null>(null)
  const [wallet, setWallet] = useState<WalletRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [done, setDone] = useState(false)
  const [error, setError] = useState('')
  const price = prices.find(item => item.billing_cycle === cycle)

  useEffect(() => {
    api<WalletRecord>('/api/v1/customer/wallet').then(setWallet).catch(() => undefined)
  }, [])

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    setBusy(true)
    setError('')
    try {
      const created = await api<OrderRecord>('/api/v1/customer/orders', {
        method: 'POST',
        body: JSON.stringify({
          items: [{ plan_id: plan.id, region_id: node.region_id, billing_cycle: cycle, quantity: 1, configuration: { template_id: template } }],
          coupon_code: coupon,
        }),
      })
      setOrder(created)
    } catch (err) {
      setError(err instanceof Error ? err.message : '下单失败')
    } finally {
      setBusy(false)
    }
  }

  async function payBalance() {
    if (!order) return
    setBusy(true)
    setError('')
    try {
      await api(`/api/v1/customer/invoices/${order.invoice_id}/pay-balance`, { method: 'POST' })
      setDone(true)
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '支付失败')
    } finally {
      setBusy(false)
    }
  }

  async function payOnline() {
    if (!order) return
    setBusy(true)
    try {
      const intent = await api<PaymentIntentRecord>(`/api/v1/customer/invoices/${order.invoice_id}/checkout`, { method: 'POST' })
      window.location.assign(intent.checkout_url)
    } catch (err) {
      setError(err instanceof Error ? err.message : '在线支付暂不可用')
      setBusy(false)
    }
  }

  const enough = wallet && order ? wallet.balance_minor >= order.total_minor : false
  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">{node.name} · 机主 {node.owner_name}</p>
            <h3>购买 {plan.name}</h3>
          </div>
          <button className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <PlanSpecs plan={plan} />
        {plan.description && <p className="plan-description">{plan.description}</p>}
        <PlanTerms plan={plan} />
        {error && <div className="form-error">{error}</div>}
        {done ? (
          <div className="checkout-success">
            <div>
              <strong>支付成功，实例正在开通</strong>
              <span>可在「我的 VPS」查看进度；遇到问题可以提交工单，或在「聊天室」联系机主。</span>
            </div>
            <a className="primary-button compact" href="/portal/services">查看我的 VPS</a>
          </div>
        ) : !order ? (
          <form className="form-grid" onSubmit={create}>
            <label>
              <span>计费周期</span>
              <select value={cycle} onChange={event => setCycle(event.target.value)}>
                {prices.map(item => (
                  <option key={item.billing_cycle} value={item.billing_cycle} disabled={priceLeft(item) === 0}>
                    {cycleName(item.billing_cycle)} · {walletMoney(item.charge_minor ?? item.amount_minor, item.currency)}
                    {item.charge_minor != null ? '（按母机到期折算）' : ''}
                    {priceLeft(item) === 0 ? '（已达限购次数）' : priceLeft(item) !== null ? `（限购剩 ${priceLeft(item)} 次）` : ''}
                  </option>
                ))}
              </select>
            </label>
            {price?.charge_minor != null && price.period_end && (
              <p className="notice-text wide">
                母机 {node.expires_at} 到期，早于{cycleName(price.billing_cycle)}周期结束：按剩余时间折算，实付 {walletMoney(price.charge_minor, price.currency)}（原价 {walletMoney(price.amount_minor, price.currency)}），实例随母机在 {node.expires_at} 当天结束时到期。续费按原价计费，同样不超过母机到期日。
              </p>
            )}
            <label>
              <span>系统镜像</span>
              <select value={template} onChange={event => setTemplate(event.target.value)}>
                {plan.allowed_template_ids.map(item => (
                  <option key={item} value={item}>{item}</option>
                ))}
              </select>
            </label>
            <CouponField planId={plan.id} cycle={cycle} onApplied={(code, discount) => { setCoupon(code); setDiscount(discount) }} />
            <p className="muted-text wide">
              该实例由第三方机主提供，机主拥有服务器 root 权限，请勿存放敏感数据。母机到期日 {node.expires_at || '未填写'}。
              可在「我的 VPS」申请退款：{plan.early_refund ? '购买 1 小时内且流量未超 1GB 全额退款，否则' : ''}按剩余天数比例退到余额。
            </p>
            <div className="form-actions wide">
              <button type="button" className="secondary-button" onClick={onClose}>取消</button>
              <button className="primary-button compact" disabled={busy || !price}>
                {busy ? '正在下单…' : `下单 ${price ? walletMoney((price.charge_minor ?? price.amount_minor) + price.setup_fee_minor - discount, price.currency) : ''}`}
              </button>
            </div>
          </form>
        ) : (
          <div className="pay-choices">
            <p>
              订单 {order.number} 已生成，应付 <strong>{walletMoney(order.total_minor, order.currency)}</strong>
              {!!order.discount_minor && `（已优惠 ${walletMoney(order.discount_minor, order.currency)}）`}。当前余额{' '}
              <strong>{walletMoney(wallet?.balance_minor || 0, wallet?.currency)}</strong>。
            </p>
            <div className="form-actions">
              <button className="primary-button compact" disabled={busy || !enough} onClick={payBalance}>
                {enough ? '用余额支付' : '余额不足'}
              </button>
              <button className="secondary-button" disabled={busy} onClick={payOnline}>在线支付</button>
              {!enough && <a className="secondary-button" href="/portal/wallet">去充值</a>}
            </div>
          </div>
        )}
      </div>
    </div>
  )
}

// ---- My nodes ----

function MyNodes() {
  const [data, setData] = useState<HostingRecord | null>(null)
  const [publishing, setPublishing] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () =>
    api<HostingRecord>('/api/v1/customer/hosting')
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  if (!data) return error ? <div className="form-error">{error}</div> : null
  const active = data.nodes.filter(node => !node.retired_at)
  const retired = data.nodes.filter(node => node.retired_at)
  return (
    <>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <section className="metrics">
        <article>
          <span>账户余额</span>
          <strong className={data.balance_minor < 0 ? 'amount-negative' : ''}>{walletMoney(data.balance_minor, data.currency)}</strong>
        </article>
        <article>
          <span>待结算（托管中）</span>
          <strong>{walletMoney(active.reduce((sum, node) => sum + node.host_pending_minor, 0), data.currency)}</strong>
        </article>
        <article>
          <span>已到账收益</span>
          <strong>{walletMoney(data.nodes.reduce((sum, node) => sum + node.host_released_minor, 0), data.currency)}</strong>
        </article>
        <article>
          <span>平台手续费</span>
          <strong>{data.fee_percent}%</strong>
        </article>
      </section>

      <div className="page-actions">
        <div>
          <h3>我的托管母机</h3>
          <p className="muted-text">收益按天从托管资金释放到余额，只能用于本平台消费，不可提现。</p>
        </div>
        <div className="form-actions">
          <button className="secondary-button" onClick={() => void load()}>
            <RefreshCw size={15} />刷新
          </button>
          {data.enabled && !publishing && (
            <button className="primary-button compact" onClick={() => setPublishing(true)}>
              <Plus size={15} />发布母机
            </button>
          )}
        </div>
      </div>
      {!data.enabled && <div className="note-banner warn">托管中心暂未开放发布。</div>}
      {publishing && (
        <PublishForm
          data={data}
          onClose={() => setPublishing(false)}
          onPublished={() => {
            setPublishing(false)
            setNotice('母机已发布，接下来为它创建套餐。')
            void load()
          }}
        />
      )}
      {active.map(node => (
        <HostedNodeCard key={node.id} node={node} data={data} onChanged={() => void load()} onNotice={setNotice} onError={setError} />
      ))}
      {!active.length && !publishing && <div className="empty-card">还没有托管母机。点击「发布母机」开始接入。</div>}
      {retired.length > 0 && (
        <div className="panel">
          <div className="panel-heading">
            <h3>已清退的母机</h3>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>母机</th>
                  <th>清退时间</th>
                  <th>原因</th>
                  <th>已到账收益</th>
                </tr>
              </thead>
              <tbody>
                {retired.map(node => (
                  <tr key={node.id}>
                    <td>{node.name}</td>
                    <td>{node.retired_at ? formatTime(node.retired_at) : ''}</td>
                    <td>{node.retired_reason}</td>
                    <td>{walletMoney(node.host_released_minor, data.currency)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}
    </>
  )
}

function NodeInfoFields({ node, regions, editing = false }: { node?: HostedNodeRecord; regions: HostingRecord['regions']; editing?: boolean }) {
  return (
    <>
      <label>
        <span>母机名称</span>
        <input name="name" required minLength={2} maxLength={40} defaultValue={node?.name} placeholder="例如 HK-CN2-01" />
      </label>
      {!editing && (
        <label>
          <span>地域</span>
          <select name="region_id" required>
            {regions.map(region => (
              <option key={region.id} value={region.id}>{region.name}</option>
            ))}
          </select>
        </label>
      )}
      <label>
        <span>地理位置（真实填写）</span>
        <input name="location" required minLength={2} maxLength={80} defaultValue={node?.location} placeholder="例如 香港 葵涌 / 美国 洛杉矶" />
      </label>
      <label>
        <span>母机租约到期日</span>
        <input name="expires_at" type="date" required defaultValue={node?.expires_at} />
      </label>
      <label>
        <span>整机月流量限额 GB（0 = 不限）</span>
        <input name="traffic_quota_gb" type="number" min="0" required defaultValue={node?.traffic_quota_gb ?? 0} />
      </label>
      <label className="wide">
        <span>线路描述（真实填写）</span>
        <textarea name="line_description" required minLength={2} maxLength={500} rows={2} defaultValue={node?.line_description} placeholder="例如 三网 CN2 GIA 回程，去程 163，带宽 1Gbps 共享" />
      </label>
    </>
  )
}

function nodeInfo(data: FormData) {
  return {
    name: String(data.get('name') || ''),
    location: String(data.get('location') || ''),
    line_description: String(data.get('line_description') || ''),
    expires_at: String(data.get('expires_at') || ''),
    traffic_quota_gb: Number(data.get('traffic_quota_gb') || 0),
  }
}

function PublishForm({ data, onClose, onPublished }: { data: HostingRecord; onClose: () => void; onPublished: () => void }) {
  const [agreed, setAgreed] = useState<boolean[]>(data.rules.map(() => false))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const all = agreed.every(Boolean)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError('')
    try {
      await api('/api/v1/customer/hosting/nodes', {
        method: 'POST',
        body: JSON.stringify({ ...nodeInfo(form), region_id: form.get('region_id'), token: String(form.get('token') || '').trim(), agree_rules: all }),
      })
      onPublished()
    } catch (err) {
      setError(err instanceof Error ? err.message : '发布失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel" onSubmit={submit}>
      <div className="panel-heading">
        <h3>发布托管母机</h3>
        <button type="button" className="icon-button" aria-label="关闭" onClick={onClose}>
          <X size={16} />
        </button>
      </div>
      <ol className="hosting-rules">
        {data.rules.map((rule, index) => (
          <li key={index}>
            <label className="checkbox">
              <input type="checkbox" checked={agreed[index]} onChange={event => setAgreed(current => current.map((value, i) => (i === index ? event.target.checked : value)))} />
              <span>{rule}</span>
            </label>
          </li>
        ))}
      </ol>
      <div className="install-step">
        <p>
          <strong>第一步：</strong>在母机上以 root 运行下面的命令安装 Hatch Agent（支持 LXD / Incus / Podman），把 <code>&lt;本机公网 IPv4&gt;</code>
          换成母机的公网 IP。安装脚本最后会打印一行 64 位令牌。
        </p>
        <div className="code-line">
          <code>{data.install_command}</code>
          <button type="button" className="icon-button" aria-label="复制" onClick={() => void navigator.clipboard?.writeText(data.install_command)}>
            <Copy size={14} />
          </button>
        </div>
        {data.install_command.includes('--server http://') && (
          <p className="muted-text">
            当前站点还没有启用 HTTPS。Agent 只允许经回环地址用 HTTP 连接，所以现在只有与计费站同机的服务器能接入；外部母机需要站点先配置域名和 HTTPS。
          </p>
        )}
        <p>
          <strong>第二步：</strong>填写母机信息和令牌。平台会先确认 Agent 已经连上，再上架。
        </p>
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="form-grid">
        <NodeInfoFields regions={data.regions} />
        <label className="wide">
          <span>Agent 令牌</span>
          <input name="token" required pattern="[0-9a-fA-F]{64}" placeholder="安装脚本最后打印的 64 位令牌" autoComplete="off" />
        </label>
        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button compact" disabled={busy || !all}>
            {busy ? '正在验证母机…' : all ? '验证并发布' : '请先勾选同意全部准则'}
          </button>
        </div>
      </div>
    </form>
  )
}

function HostedNodeCard({
  node,
  data,
  onChanged,
  onNotice,
  onError,
}: {
  node: HostedNodeRecord
  data: HostingRecord
  onChanged: () => void
  onNotice: (message: string) => void
  onError: (message: string) => void
}) {
  const [editing, setEditing] = useState(false)
  const [planForm, setPlanForm] = useState<PlanRecord | 'new' | null>(null)
  const [retiring, setRetiring] = useState(false)
  const [overselling, setOverselling] = useState(false)

  async function call(path: string, body: unknown, message: string) {
    onError('')
    try {
      await api(path, { method: 'POST', body: JSON.stringify(body) })
      onNotice(message)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : '操作失败')
    }
  }

  async function saveInfo(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      await api(`/api/v1/customer/hosting/nodes/${node.id}`, { method: 'PUT', body: JSON.stringify({ ...nodeInfo(new FormData(event.currentTarget)), region_id: node.region_id }) })
      setEditing(false)
      onNotice('母机信息已更新')
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : '保存失败')
    }
  }

  const remaining = (node.services || []).filter(item => item.status !== 'terminated').reduce((sum, item) => sum + item.remaining_value_minor, 0)
  return (
    <article className="panel hosted-node">
      <div className="panel-heading">
        <div>
          <h3>{node.name}</h3>
          <small>
            {node.region_name} · {node.location} · 到期 {node.expires_at}
          </small>
        </div>
        <div className="form-actions">
          <span className={node.status === 'online' ? 'tag success' : 'tag danger'}>{lastSeen(node)}</span>
          <span className={node.listing_status === 'listed' ? 'tag success' : 'tag'}>{node.listing_status === 'listed' ? '在售' : '已暂停销售'}</span>
        </div>
      </div>
      {node.status !== 'online' && (
        <div className="note-banner warn">
          母机离线。离线满 {data.offline_hours} 小时将被自动清退，按剩余价值 2 倍补偿买家（其中一份从您的余额扣除）。有特殊原因请尽快提交工单联系管理员。
          {node.clearance_hold_until && ` 管理员已暂缓清退至 ${formatTime(node.clearance_hold_until)}。`}
        </div>
      )}
      <dl className="market-facts">
        <div><dt>线路</dt><dd>{node.line_description}</dd></div>
        <div><dt>可售资源</dt><dd>{node.capacity_vcpu} 核 / {node.capacity_ram_mb} MB / {node.capacity_disk_gb} GB，剩余 {node.free_vcpu} 核 / {node.free_ram_mb} MB / {node.free_disk_gb} GB</dd></div>
        <div><dt>运行实例</dt><dd>{node.active_services}</dd></div>
        <div><dt>托管中 / 待结算</dt><dd>{walletMoney(node.escrow_holding_minor, data.currency)} / {walletMoney(node.host_pending_minor, data.currency)}</dd></div>
        <div><dt>已到账</dt><dd>{walletMoney(node.host_released_minor, data.currency)}</dd></div>
      </dl>
      <SupplyDetails node={node} sellable={{ vcpu: node.capacity_vcpu, ram_mb: node.capacity_ram_mb, disk_gb: node.capacity_disk_gb }} />
      <div className="form-actions">
        <button className="secondary-button" onClick={() => setEditing(value => !value)}>编辑信息</button>
        <button className="secondary-button" onClick={() => setOverselling(true)}>超售设置</button>
        <button className="secondary-button" onClick={() => void call(`/api/v1/customer/hosting/nodes/${node.id}/listing`, { listed: node.listing_status !== 'listed' }, node.listing_status === 'listed' ? '已暂停销售，现有实例不受影响' : '已恢复销售')}>
          {node.listing_status === 'listed' ? '暂停销售' : '恢复销售'}
        </button>
        <button className="primary-button compact" onClick={() => setPlanForm('new')}>
          <Plus size={14} />新建套餐
        </button>
        <button className="danger-button" onClick={() => setRetiring(true)}>下架母机</button>
      </div>
      {overselling && (
        <OvercommitDialog
          node={node}
          name={node.name}
          limits={data.overcommit_limits}
          endpoint={`/api/v1/customer/hosting/nodes/${node.id}/overcommit`}
          onClose={() => setOverselling(false)}
          onSaved={() => {
            setOverselling(false)
            onNotice('超售设置已保存')
            onChanged()
          }}
        />
      )}
      {editing && (
        <form className="form-grid" onSubmit={saveInfo}>
          <NodeInfoFields node={node} regions={data.regions} editing />
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={() => setEditing(false)}>取消</button>
            <button className="primary-button compact">保存</button>
          </div>
        </form>
      )}
      {retiring && (
        <div className="note-banner danger">
          <p>
            下架后母机立即退出市场，现有 {node.active_services} 个实例全部清退：按剩余价值（当前约 {walletMoney(remaining, data.currency)}）的 2 倍补偿买家，
            其中一份约 {walletMoney(remaining, data.currency)} 从您的余额扣除。此操作不可撤销。
          </p>
          <div className="form-actions">
            <button className="secondary-button" onClick={() => setRetiring(false)}>取消</button>
            <button className="danger-button" onClick={() => void call(`/api/v1/customer/hosting/nodes/${node.id}/retire`, { confirm: true }, '母机已下架并完成清退')}>
              确认下架并清退
            </button>
          </div>
        </div>
      )}
      {planForm && (
        <HostedPlanForm
          node={node}
          plan={planForm === 'new' ? undefined : planForm}
          onClose={() => setPlanForm(null)}
          onSaved={() => {
            setPlanForm(null)
            onNotice('套餐已保存')
            onChanged()
          }}
        />
      )}
      <h4>套餐</h4>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>套餐</th>
              <th>配置</th>
              <th>价格</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {node.plans.map(plan => (
              <tr key={plan.id}>
                <td>
                  <strong>{plan.name}</strong>
                  <small className="block">{virtNames[plan.virtualization]} · {plan.allowed_template_ids.join('、')}</small>
                  <small className="block">
                    {plan.purchase_limit ? `每人限购 ${plan.purchase_limit} 台` : '不限购'} · {plan.early_refund ? '允许早期全额退款' : '按比例退款'}
                  </small>
                </td>
                <td>
                  {plan.vcpu} 核 / {plan.ram_mb} MB / {plan.disk_gb} GB / {plan.traffic_gb || '不限'} GB / NAT×{plan.port_mapping_count}
                  {diskIOText(plan) && <small className="block">{diskIOText(plan)}</small>}
                </td>
                <td>
                  {plan.prices.map(price => `${cycleName(price.billing_cycle)} ${walletMoney(price.amount_minor, price.currency)}${price.purchase_limit ? `（限购 ${price.purchase_limit}，已售 ${price.sold ?? 0}）` : ''}`).join('，')}
                  <small className="block">{plan.stock_limit == null ? '库存不限（受资源限制）' : `库存 ${plan.stock_limit}，已售及待支付 ${plan.stock_held ?? 0}`}</small>
                </td>
                <td><span className={plan.enabled ? 'tag success' : 'tag'}>{plan.enabled ? '在售' : '已停售'}</span></td>
                <td className="row-actions">
                  <button className="secondary-button compact" onClick={() => setPlanForm(plan)}>编辑</button>
                  <button className="secondary-button compact" onClick={() => void call(`/api/v1/customer/hosting/plans/${plan.id}/enabled`, { enabled: !plan.enabled }, plan.enabled ? '套餐已停售' : '套餐已上架')}>
                    {plan.enabled ? '停售' : '上架'}
                  </button>
                </td>
              </tr>
            ))}
            {!node.plans.length && (
              <tr>
                <td colSpan={5} className="empty-state">还没有套餐，买家看不到这台母机。</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <h4>母机上的实例</h4>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>实例</th>
              <th>套餐</th>
              <th>买家</th>
              <th>状态</th>
              <th>到期</th>
              <th>剩余价值</th>
            </tr>
          </thead>
          <tbody>
            {(node.services || []).map(item => (
              <tr key={item.id}>
                <td><code>{item.instance_name}</code></td>
                <td>{item.plan_name}</td>
                <td>{item.buyer_name}</td>
                <td>{item.status} · {item.runtime_status}</td>
                <td>{item.next_due_at ? formatDate(item.next_due_at) : '—'}</td>
                <td>{walletMoney(item.remaining_value_minor, data.currency)}</td>
              </tr>
            ))}
            {!node.services?.length && (
              <tr>
                <td colSpan={6} className="empty-state">暂无实例</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </article>
  )
}

function HostedPlanForm({ node, plan, onClose, onSaved }: { node: HostedNodeRecord; plan?: PlanRecord; onClose: () => void; onSaved: () => void }) {
  const [images, setImages] = useState<NodeImageRecord[]>([])
  const [virtualization, setVirtualization] = useState<string>(plan?.virtualization || node.virtualization_types[0] || 'lxc')
  const [allowed, setAllowed] = useState<string[]>(plan?.allowed_template_ids || [])
  const [fallback, setFallback] = useState(plan?.default_template_id || '')
  const [limited, setLimited] = useState(!!plan?.purchase_limit)
  const [capacity, setCapacity] = useState<StockCapacityRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api<NodeImageRecord[]>(`/api/v1/customer/hosting/nodes/${node.id}/templates`)
      .then(setImages)
      .catch(err => setError(err instanceof Error ? err.message : '无法读取母机镜像'))
  }, [node.id])

  const visible = images.filter(image => (image.type || 'lxc') === virtualization)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const { prices, limits, error: priceError } = readCyclePrices(form)
    if (priceError) {
      setError(priceError)
      return
    }
    const body = {
      name: form.get('name'),
      virtualization,
      vcpu: Number(form.get('vcpu')),
      ram_mb: Number(form.get('ram_mb')),
      disk_gb: Number(form.get('disk_gb')),
      traffic_gb: Number(form.get('traffic_gb')),
      network_down_mbps: Number(form.get('network_down_mbps')),
      network_up_mbps: Number(form.get('network_up_mbps')),
      port_mapping_count: Number(form.get('port_mapping_count')),
      allowed_template_ids: allowed,
      default_template_id: allowed.includes(fallback) ? fallback : allowed[0] || '',
      prices,
      price_limits: limits,
      stock_limit: readStock(form),
      ...readDiskIO(form),
      enabled: form.get('enabled') === 'on',
      description: String(form.get('description') || ''),
      purchase_limit: limited ? Number(form.get('purchase_limit') || 0) : 0,
      early_refund: form.get('early_refund') === 'on',
    }
    setBusy(true)
    setError('')
    try {
      await api(plan ? `/api/v1/customer/hosting/plans/${plan.id}` : `/api/v1/customer/hosting/nodes/${node.id}/plans`, {
        method: plan ? 'PUT' : 'POST',
        body: JSON.stringify(body),
      })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel nested-panel" onSubmit={submit}>
      <div className="panel-heading">
        <h3>{plan ? `编辑套餐 ${plan.name}` : '新建套餐'}</h3>
        <button type="button" className="icon-button" aria-label="关闭" onClick={onClose}>
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <p className="muted-text">
        单个实例配置不能超过母机真实资源（{node.reported_vcpu} 核 / {node.reported_ram_mb} MB / {node.reported_disk_gb} GB）；可售总量为真实资源乘以「超售设置」里的倍数。
      </p>
      <div className="form-grid">
        <label>
          <span>套餐名称</span>
          <input name="name" required minLength={2} maxLength={40} defaultValue={plan?.name} />
        </label>
        <label>
          <span>虚拟化</span>
          <select name="virtualization" value={virtualization} onChange={event => { setVirtualization(event.target.value); setAllowed([]) }}>
            {node.virtualization_types.map(item => (
              <option key={item} value={item}>{virtNames[item] || item}</option>
            ))}
          </select>
        </label>
        <label><span>vCPU</span><input name="vcpu" type="number" min="1" required defaultValue={plan?.vcpu ?? 1} /></label>
        <label><span>内存 MB</span><input name="ram_mb" type="number" min="64" required defaultValue={plan?.ram_mb ?? 512} /></label>
        <label><span>磁盘 GB</span><input name="disk_gb" type="number" min="1" required defaultValue={plan?.disk_gb ?? 5} /></label>
        <label><span>月流量 GB（0 = 不限）</span><input name="traffic_gb" type="number" min="0" required defaultValue={plan?.traffic_gb ?? 100} /></label>
        <label><span>下行 Mbps（0 = 不限）</span><input name="network_down_mbps" type="number" min="0" required defaultValue={plan?.network_down_mbps ?? 50} /></label>
        <label><span>上行 Mbps（0 = 不限）</span><input name="network_up_mbps" type="number" min="0" required defaultValue={plan?.network_up_mbps ?? 20} /></label>
        <label><span>NAT 端口数</span><input name="port_mapping_count" type="number" min="1" max="100" required defaultValue={plan?.port_mapping_count ?? 5} /></label>
        <StockField
          plan={plan ?? undefined}
          preview={formElement => {
            const data = new FormData(formElement)
            return api<StockCapacityRecord>(`/api/v1/customer/hosting/nodes/${node.id}/stock-capacity`, {
              method: 'POST',
              body: JSON.stringify({
                plan_id: plan?.id ?? '',
                virtualization: data.get('virtualization'),
                vcpu: Number(data.get('vcpu')),
                ram_mb: Number(data.get('ram_mb')),
                disk_gb: Number(data.get('disk_gb')),
                traffic_gb: Number(data.get('traffic_gb')),
              }),
            })
          }}
          onCapacity={setCapacity}
        />
        <DiskIOFields plan={plan} capacity={capacity} />
        <CyclePriceFields
          prices={plan?.prices ?? []}
          hint="留空表示不支持该计费周期，至少填写一个。母机到期日早于周期结束时，买家按剩余时间折算付款（例如季付 ¥30、母机只剩 2 个月，买家付 ¥20），实例到期日与母机到期日相同。"
        />
        <fieldset className="wide template-picker">
          <legend>可选系统镜像（读取自母机）</legend>
          {visible.map(image => (
            <label key={image.id} className="checkbox">
              <input
                type="checkbox"
                checked={allowed.includes(image.id)}
                onChange={event => setAllowed(current => (event.target.checked ? [...current, image.id] : current.filter(item => item !== image.id)))}
              />
              <span>{image.id}{image.description ? ` · ${image.description}` : ''}</span>
            </label>
          ))}
          {!visible.length && <span className="muted-text">母机上没有这种虚拟化的镜像。</span>}
        </fieldset>
        <label>
          <span>默认镜像</span>
          <select value={allowed.includes(fallback) ? fallback : allowed[0] || ''} onChange={event => setFallback(event.target.value)}>
            {allowed.map(item => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
        </label>
        <label className="wide">
          <span>套餐描述（可选，展示在托管市场）</span>
          <textarea name="description" maxLength={1000} rows={3} defaultValue={plan?.description} placeholder="例如适用场景、线路特点、是否支持某些用途" />
        </label>
        <div className="wide">
          <label className="checkbox">
            <input type="checkbox" checked={limited} onChange={event => setLimited(event.target.checked)} />
            <span>限购</span>
          </label>
          {limited && (
            <label className="inline-field">
              <span>每人最多</span>
              <input name="purchase_limit" type="number" min="1" max="100" required defaultValue={plan?.purchase_limit || 1} />
              <span>台（按有效实例计算）</span>
            </label>
          )}
        </div>
        <label className="checkbox wide">
          <input name="early_refund" type="checkbox" defaultChecked={plan?.early_refund ?? false} />
          <span>允许早期全额退款：买家在购买 1 小时内且流量未超 1GB 时可申请全额退款；关闭则一律按剩余天数比例退款</span>
        </label>
        <label className="checkbox">
          <input name="enabled" type="checkbox" defaultChecked={plan?.enabled ?? true} />
          <span>立即上架</span>
        </label>
        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button compact" disabled={busy || !allowed.length}>{busy ? '正在保存…' : '保存套餐'}</button>
        </div>
      </div>
    </form>
  )
}

// ---- Host coupons ----

function HostCoupons() {
  const [data, setData] = useState<HostingRecord | null>(null)
  const [error, setError] = useState('')
  useEffect(() => {
    api<HostingRecord>('/api/v1/customer/hosting')
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  }, [])
  if (!data) return error ? <div className="form-error">{error}</div> : null
  const plans = data.nodes
    .filter(node => !node.retired_at)
    .flatMap(node => node.plans.map(plan => ({ id: plan.id, name: `${node.name} / ${plan.name}` })))
  return (
    <CouponManager
      endpoint="/api/v1/customer/hosting/coupons"
      plans={plans}
      canCreate={data.enabled}
      intro="优惠码只对你自己母机上的套餐有效，优惠从实付金额中扣除，平台按实付金额收取手续费。"
    />
  )
}

// ---- Hosted tickets ----

function HostTickets() {
  const [tickets, setTickets] = useState<TicketRecord[]>([])
  const [detail, setDetail] = useState<TicketDetailRecord | null>(null)
  const [error, setError] = useState('')
  const maxMB = useAttachmentLimit()

  const load = () =>
    api<TicketRecord[]>('/api/v1/customer/hosting/tickets')
      .then(setTickets)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  const open = (id: string) =>
    api<TicketDetailRecord>(`/api/v1/customer/hosting/tickets/${id}`)
      .then(setDetail)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  return (
    <>
      {error && <div className="form-error">{error}</div>}
      <p className="muted-text">购买您母机实例的用户提交的工单会出现在这里，您是第一处理人；平台管理员也能看到并介入。</p>
      <div className="support-layout">
        <div className="ticket-list">
          {tickets.map(ticket => (
            <button key={ticket.id} className={detail?.ticket.id === ticket.id ? 'ticket-row selected' : 'ticket-row'} onClick={() => void open(ticket.id)}>
              <div>
                <strong>{ticket.subject}</strong>
                <small>{ticket.number} · {ticket.customer_name} · {ticket.instance_name}</small>
              </div>
              <span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span>
            </button>
          ))}
          {!tickets.length && <div className="empty-state">暂无托管工单</div>}
        </div>
        {detail ? (
          <TicketConversation
            detail={detail}
            maxMB={maxMB}
            onError={setError}
            attachmentBase="/api/v1/customer/hosting/tickets"
            onReply={async (body, _internal, files) => {
              try {
                await api(`/api/v1/customer/hosting/tickets/${detail.ticket.id}/messages`, { method: 'POST', body: ticketRequestBody({ body }, files) })
                await open(detail.ticket.id)
                void load()
                return true
              } catch (err) {
                setError(err instanceof Error ? err.message : '回复失败')
                return false
              }
            }}
          />
        ) : (
          <div className="panel support-placeholder">选择左侧工单查看详情</div>
        )}
      </div>
    </>
  )
}

// ---- Chat ----

function ChatRooms() {
  const [rooms, setRooms] = useState<ChatRoomRecord[]>([])
  const [selected, setSelected] = useState('')
  const [error, setError] = useState('')

  useEffect(() => {
    api<ChatRoomRecord[]>('/api/v1/customer/chat/rooms')
      .then(value => {
        setRooms(value)
        if (value.length) setSelected(current => current || value[0].node_id)
      })
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  }, [])

  const room = rooms.find(item => item.node_id === selected)
  return (
    <>
      {error && <div className="form-error">{error}</div>}
      <p className="muted-text">每台托管母机一个聊天室，成员是机主和在这台母机上有实例的用户，平台管理员可以查看全部记录。</p>
      <div className="support-layout">
        <div className="ticket-list">
          {rooms.map(item => (
            <button key={item.node_id} className={item.node_id === selected ? 'ticket-row selected' : 'ticket-row'} onClick={() => setSelected(item.node_id)}>
              <div>
                <strong>{item.node_name}</strong>
                <small>
                  {item.role === 'host' ? '我是机主' : `机主 ${item.host_name}`} · {item.members} 人{item.retired ? ' · 已清退' : ''}
                </small>
                {item.last_message && <small className="block">{item.last_message.author_name}：{item.last_message.body.slice(0, 40)}</small>}
              </div>
            </button>
          ))}
          {!rooms.length && <div className="empty-state">购买托管实例或发布母机后会自动加入对应聊天室。</div>}
        </div>
        {room ? (
          <ChatRoom base="/api/v1/customer/chat/rooms" nodeID={room.node_id} title={`${room.node_name} 聊天室`} />
        ) : (
          <div className="panel support-placeholder">暂无聊天室</div>
        )}
      </div>
    </>
  )
}
