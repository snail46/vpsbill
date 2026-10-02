import { FormEvent, useEffect, useMemo, useRef, useState } from 'react'
import { ChevronRight, FilterX, MapPin, MessagesSquare, Plus, RefreshCw, Search, Server, Store, Ticket, TicketPercent, X } from 'lucide-react'
import {
  api,
  cached,
  type ChatRoomRecord,
  type CustomerIdentity,
  type HostedNodeRecord,
  type HostingRecord,
  type MarketRecord,
  type NodeImageRecord,
  type OrderRecord,
  type PendingAgentRecord,
  type PlanRecord,
  type StockCapacityRecord,
  type TicketDetailRecord,
  type TicketRecord,
  type WalletRecord,
} from './api'
import { osOptions } from './shared/nav'
import ChatRoom from './ChatRoom'
import { CouponField, CouponManager } from './Coupons'
import { ReportDialog } from './Reports'
import { StatusBadge, TicketConversation, ticketStatusLabel, useReveal , bandwidthLabel } from './shared/ui'
import { ticketRequestBody, useAttachmentLimit } from './TicketAttachments'
import { topupLink, walletMoney } from './Wallet'
import { CurrencyNote } from './shared/LocaleMenu'
import { formatDate, formatTime } from './shared/time'
import { cycleName, cycleOrder, CyclePriceFields, priceLeft, readCyclePrices } from './shared/cycles'
import { readStock, stockLeft, StockField, StockTag, WatchButton } from './shared/stock'
import { DiskIOFields, diskIOText, readDiskIO } from './shared/diskio'
import { OvercommitDialog, SupplyDetails, overcommitText } from './Supply'
import { ConnectSteps, PendingAgents } from './shared/agents'
import { confirmDialog } from './shared/dialog'
import { toast } from './shared/toast'
import { t, tr } from './shared/i18n'
import { CountryLabel, Flag, countryName } from './shared/country'
import { currencySymbol, displayCurrency, plainMoney } from './shared/currency'
import { arrangeMarket, headlinePrice, narrowing, noFilter, type MarketFilter, type MarketNode, type MarketSort } from './shared/market'
import { TagInput } from './shared/tags'

type Tab = 'market' | 'mine' | 'coupons' | 'tickets' | 'chat'
const tabs: [Tab, string, typeof Store][] = [
  ['market', t('托管市场'), Store],
  ['mine', t('我的母机'), Server],
  ['coupons', t('优惠码'), TicketPercent],
  ['tickets', t('托管工单'), Ticket],
  ['chat', t('聊天室'), MessagesSquare],
]

const virtNames: Record<string, string> = { lxc: t('LXC 容器'), podman: t('Podman 容器'), kvm: 'KVM' }

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
          <h2>{t('托管中心')}</h2>
          <p>{t('把闲置服务器通过 Hatch Agent 接入平台，自定套餐和价格出售给其他用户；平台作为中间方托管资金并按日结算。')}</p>
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

// placementNote tells the owner how the host's country was decided when
// it was not simply read from the location they wrote.
function placementNote(node: HostedNodeRecord) {
  if (!node.country_code) return t('未能识别国家/地区，请在「地理位置」里写明国家或城市')
  if (node.country_source === 'ip') return t('未能从填写的地理位置识别，按 IP 归属地归类')
  if (node.ip_country && node.ip_country !== node.country_code) return t('IP 归属地为 {0} {1}，买家可以看到', node.ip_country, countryName(node.ip_country))
  return ''
}

function lastSeen(node: HostedNodeRecord) {
  if (node.status === 'online') return t('在线')
  return node.last_seen_at ? t('离线（最后在线 {0}）', formatTime(node.last_seen_at)) : t('离线')
}

function planPrice(plan: PlanRecord) {
  const first = headlinePrice(plan)
  if (!first) return t('暂无报价')
  const more = plan.prices.length > 1 ? t(' 等 {0} 种周期', plan.prices.length) : ''
  return `${walletMoney(first.amount_minor, first.currency)} / ${cycleName(first.billing_cycle)}${more}`
}

function PlanSpecs({ plan }: { plan: PlanRecord }) {
  return (
    <div className="shop-specs">
      <span>{plan.vcpu} vCPU</span>
      <span>{t('{0} MB 内存', plan.ram_mb)}</span>
      <span>{t('{0} GB 磁盘', plan.disk_gb)}</span>
      <span>{plan.traffic_gb ? t('{0} GB 流量', plan.traffic_gb) : t('不限流量')}</span>
      <span>{bandwidthLabel(plan.network_down_mbps)}</span>
      {diskIOText(plan) && <span>{diskIOText(plan)}</span>}
      <span>NAT × {plan.port_mapping_count}</span>
    </div>
  )
}

function PlanTerms({ plan }: { plan: PlanRecord }) {
  return (
    <div className="plan-terms">
      <span className={plan.early_refund ? 'tag success' : 'tag'}>
        {plan.early_refund ? t('1 小时内且流量未超 1GB 可全额退款') : t('按剩余天数比例退款')}
      </span>
      {!!plan.purchase_limit && <span className="tag">{t('每人限购 {0} 台', plan.purchase_limit)}</span>}
    </div>
  )
}

// ---- Market ----

function Market({ customer }: { customer: CustomerIdentity }) {
  const [market, setMarket] = useState<MarketRecord | null>(() => cached<MarketRecord>('/api/v1/customer/market') ?? null)
  const [buying, setBuying] = useState<{ node: HostedNodeRecord; plan: PlanRecord } | null>(null)
  const [reporting, setReporting] = useState<HostedNodeRecord | null>(null)
  const [error, setError] = useState('')
  const [filter, setFilter] = useState<MarketFilter>(noFilter)
  // The market opens on the countries; country is the one opened (''
  // being the hosts that could not be placed) and nodeID the host opened.
  const [country, setCountry] = useState<string | null>(null)
  const [nodeID, setNodeID] = useState('')

  const load = () =>
    api<MarketRecord>('/api/v1/customer/market')
      .then(setMarket)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
  }, [])

  const countries = useMemo(() => arrangeMarket(market?.nodes ?? [], filter), [market, filter])
  const shownNodes = countries.flatMap(item => item.nodes)
  const narrowed = narrowing(filter)
  const opened = country === null ? null : (countries.find(item => item.code === country) ?? { code: country, nodes: [], plans: 0, low: null, high: null })
  const openedRaw = nodeID ? market?.nodes.find(node => node.id === nodeID) : undefined
  // A host stays open while the filter hides its plans, so typing does not
  // throw the visitor back a level.
  const openedNode: MarketNode | null = openedRaw
    ? (shownNodes.find(item => item.node.id === nodeID) ?? { node: openedRaw, plans: [], low: null, high: null, inStock: false })
    : null
  const set = (patch: Partial<MarketFilter>) => setFilter(current => ({ ...current, ...patch }))
  const open = (node: HostedNodeRecord) => {
    setCountry(node.country_code || '')
    setNodeID(node.id)
  }
  const nodeCards = (items: MarketNode[]) => (
    <div className="market-grid">
      {items.map(item => (
        <NodeCard key={item.node.id} item={item} onOpen={() => open(item.node)} />
      ))}
    </div>
  )
  const nothing = (
    <div className="empty-card">
      {t('没有符合条件的母机或套餐。')}
      <button className="text-button" onClick={() => setFilter({ ...noFilter, sort: filter.sort })}>
        {t('清除筛选')}
      </button>
    </div>
  )

  return (
    <>
      <CurrencyNote />
      {error && <div className="form-error">{error}</div>}
      <div className="note-banner">
        {t('托管母机由其他用户提供，机主拥有服务器的 root 权限。付款由平台托管、按天结算给机主；母机离线满 24 小时或机主下架时实例会被清退：剩余价值退还到您的余额，另由机主按剩余价值额外赔付一份（以机主当时的余额为限）。')}
      </div>
      {!!market?.nodes.length && (
        <div className="filter-bar market-filter">
          <div className="filter-fields">
            <label className="filter-field filter-search">
              <span>{t('搜索')}</span>
              <div>
                <Search size={14} aria-hidden="true" />
                <input type="search" value={filter.query} placeholder={t('母机、套餐、标签、地区、线路')} onChange={event => set({ query: event.target.value })} />
              </div>
            </label>
            <label className="filter-field filter-number market-price-range">
              <span>{t('价格（{0}）', currencySymbol())}</span>
              <div>
                <input type="number" min={0} step="any" inputMode="decimal" value={filter.min} placeholder={t('最低')} aria-label={t('最低价格')} onChange={event => set({ min: event.target.value })} />
                <em>–</em>
                <input type="number" min={0} step="any" inputMode="decimal" value={filter.max} placeholder={t('最高')} aria-label={t('最高价格')} onChange={event => set({ max: event.target.value })} />
              </div>
            </label>
            <label className="filter-field">
              <span>{t('排序')}</span>
              <select value={filter.sort} onChange={event => set({ sort: event.target.value as MarketSort })}>
                <option value="default">{t('默认排序')}</option>
                <option value="price-asc">{t('价格从低到高')}</option>
                <option value="price-desc">{t('价格从高到低')}</option>
              </select>
            </label>
            <label className="switch market-stock-switch">
              <input type="checkbox" checked={filter.inStock} onChange={event => set({ inStock: event.target.checked })} />
              <span />
              {t('仅显示有货')}
            </label>
          </div>
          <div className="filter-summary">
            <span>{t('{0} 个地区 · {1} 台母机 · {2} 个套餐', countries.length, shownNodes.length, countries.reduce((sum, item) => sum + item.plans, 0))}</span>
            {narrowed > 0 && (
              <button type="button" className="text-button" onClick={() => setFilter({ ...noFilter, sort: filter.sort })}>
                <FilterX size={13} />
                {t('清除筛选')}
              </button>
            )}
          </div>
        </div>
      )}
      {opened && (
        <nav className="market-crumbs" aria-label={t('当前位置')}>
          <button
            className="text-button"
            onClick={() => {
              setCountry(null)
              setNodeID('')
            }}
          >
            {t('全部地区')}
          </button>
          <ChevronRight size={14} aria-hidden="true" />
          {openedNode ? (
            <>
              <button className="text-button" onClick={() => setNodeID('')}>
                <CountryLabel code={opened.code} />
              </button>
              <ChevronRight size={14} aria-hidden="true" />
              <strong>{openedNode.node.name}</strong>
            </>
          ) : (
            <strong>
              <CountryLabel code={opened.code} />
            </strong>
          )}
        </nav>
      )}

      {!opened && (
        <>
          <div className="market-countries">
            {countries.map(item => (
              <button key={item.code} className="market-country" onClick={() => setCountry(item.code)}>
                <Flag code={item.code} />
                <span>
                  <strong>
                    {item.code && <b>{item.code}</b>}
                    {countryName(item.code)}
                  </strong>
                  <small>
                    {t('{0} 个套餐', item.plans)}
                    {item.low !== null && ` · ${priceRange(item)}`}
                  </small>
                </span>
                <em title={t('{0} 台母机', item.nodes.length)}>{item.nodes.length}</em>
              </button>
            ))}
          </div>
          {narrowed > 0 && shownNodes.length > 0 && (
            <>
              <h3 className="market-heading">{t('符合条件的母机')}</h3>
              {nodeCards(shownNodes)}
            </>
          )}
          {market && !market.nodes.length && <div className="empty-card">{t('托管市场暂时没有在售母机。')}</div>}
          {!!market?.nodes.length && !countries.length && nothing}
        </>
      )}

      {opened && !openedNode && (opened.nodes.length ? nodeCards(opened.nodes) : nothing)}

      {openedNode && (
        <MarketNodeDetail
          item={openedNode}
          onBuy={plan => setBuying({ node: openedNode.node, plan })}
          onReport={() => setReporting(openedNode.node)}
          onShowAll={() => setFilter({ ...noFilter, sort: filter.sort })}
        />
      )}
      {reporting && <ReportDialog nodeID={reporting.id} nodeName={reporting.name} onClose={() => setReporting(null)} />}
      {buying && <BuyDialog customer={customer} node={buying.node} plan={buying.plan} onClose={() => setBuying(null)} onDone={() => void load()} />}
    </>
  )
}

function priceRange(item: { low: number | null; high: number | null }) {
  if (item.low === null || item.high === null) return t('暂无报价')
  const low = plainMoney(item.low, displayCurrency())
  return item.low === item.high ? low : `${low} – ${plainMoney(item.high, displayCurrency())}`
}

// NodeCard is a host in a country's list; opening it shows its plans.
function NodeCard({ item, onOpen }: { item: MarketNode; onOpen: () => void }) {
  const { node } = item
  return (
    <button className="panel market-node-card" onClick={onOpen}>
      <span className="market-node-card-head">
        <strong>{node.name}</strong>
        <span className={node.status === 'online' ? 'tag success' : 'tag danger'}>{node.status === 'online' ? t('在线') : t('离线')}</span>
      </span>
      <small>
        <CountryLabel code={node.country_code || ''} /> · {node.location}
      </small>
      <small className="market-node-card-line">{node.line_description}</small>
      <span className="market-node-card-foot">
        <span>
          {t('{0} 个套餐', item.plans.length)}
          {!item.inStock && <span className="tag stock-tag sold-out">{t('暂时无货')}</span>}
        </span>
        <strong>{priceRange(item)}</strong>
        <ChevronRight size={16} aria-hidden="true" />
      </span>
    </button>
  )
}

// MarketNodeDetail is a host with the plans the filter left.
function MarketNodeDetail({ item, onBuy, onReport, onShowAll }: { item: MarketNode; onBuy: (plan: PlanRecord) => void; onReport: () => void; onShowAll: () => void }) {
  const { node, plans } = item
  const hidden = node.plans.length - plans.length
  return (
    <article className="panel market-node">
      <div className="panel-heading">
        <div>
          <h3>{node.name}</h3>
          <small>{t('机主 {0}{1}', node.owner_name, node.mine ? t('（我自己）') : '')}</small>
        </div>
        <span className={node.status === 'online' ? 'tag success' : 'tag danger'}>{node.status === 'online' ? t('在线') : t('离线')}</span>
      </div>
      <dl className="market-facts">
        <div>
          <dt><MapPin size={13} /> {t('位置')}</dt>
          <dd>
            {node.region_name} · {node.location}
            {node.ip_country && node.ip_country !== node.country_code && <small className="block">{t('IP 归属地：{0} {1}', node.ip_country, countryName(node.ip_country))}</small>}
          </dd>
        </div>
        <div>
          <dt>{t('线路')}</dt>
          <dd>{node.line_description}</dd>
        </div>
        <div>
          <dt>{t('母机到期')}</dt>
          <dd>{node.expires_at || '—'}</dd>
        </div>
        <div>
          <dt>{t('月流量限额')}</dt>
          <dd>{node.traffic_quota_gb ? t('{0} GB（整机）', node.traffic_quota_gb) : t('不限')}</dd>
        </div>
        <div>
          <dt>{t('剩余可售')}</dt>
          <dd>{t('{0} 核 · {1} MB · {2} GB', node.free_vcpu, node.free_ram_mb, node.free_disk_gb)}</dd>
        </div>
        <div>
          <dt>{t('超售')}</dt>
          <dd>{overcommitText(node.overcommit)}</dd>
        </div>
      </dl>
      <SupplyDetails node={node} sellable={{ vcpu: node.capacity_vcpu, ram_mb: node.capacity_ram_mb, disk_gb: node.capacity_disk_gb }} />
      {!node.mine && (
        <button className="text-button report-link" onClick={onReport}>
          {t('举报资源不符或超售')}
        </button>
      )}
      <div className="market-plans">
        {plans.map(plan => (
          <div key={plan.id} className="market-plan">
            <div>
              <strong>{plan.name}</strong>
              <span className="tag">{virtNames[plan.virtualization] || plan.virtualization}</span>
              <StockTag plan={plan} />
            </div>
            <PlanSpecs plan={plan} />
            {!!plan.tags?.length && (
              <div className="shop-tags">
                {plan.tags.map(tag => (
                  <span key={tag}>{tag}</span>
                ))}
              </div>
            )}
            {plan.description && <p className="plan-description">{plan.description}</p>}
            <PlanTerms plan={plan} />
            <div className="market-plan-buy">
              <strong>{planPrice(plan)}</strong>
              <button
                className="primary-button compact"
                disabled={node.mine || node.status !== 'online' || !!node.health_hold_reason || stockLeft(plan) === 0}
                onClick={() => onBuy(plan)}
              >
                {stockLeft(plan) === 0 ? t('已售罄') : t('购买')}
              </button>
              {stockLeft(plan) === 0 && !node.mine && <WatchButton planID={plan.id} />}
            </div>
          </div>
        ))}
        {hidden > 0 && (
          <p className="market-hidden">
            {plans.length ? t('另有 {0} 个套餐不符合筛选条件。', hidden) : t('这台母机没有符合筛选条件的套餐。')}
            <button className="text-button" onClick={onShowAll}>
              {t('显示全部')}
            </button>
          </p>
        )}
      </div>
    </article>
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
  const [wallet, setWallet] = useState<WalletRecord | null>(() => cached<WalletRecord>('/api/v1/customer/wallet') ?? null)
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
      setError(err instanceof Error ? err.message : t('下单失败'))
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
      setError(err instanceof Error ? err.message : t('支付失败'))
    } finally {
      setBusy(false)
    }
  }

  const enough = wallet && order ? wallet.balance_minor >= order.total_minor : false
  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">{t('{0} · 机主 {1}', node.name, node.owner_name)}</p>
            <h3>{t('购买 {0}', plan.name)}</h3>
          </div>
          <button className="icon-button" aria-label={t('关闭')} onClick={onClose}>
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
              <strong>{t('支付成功，实例正在开通')}</strong>
              <span>{t('可在「我的 VPS」查看进度；遇到问题可以提交工单，或在「聊天室」联系机主。')}</span>
            </div>
            <a className="primary-button compact" href="/portal/services">{t('查看我的 VPS')}</a>
          </div>
        ) : !order ? (
          <form className="form-grid" onSubmit={create}>
            <label>
              <span>{t('计费周期')}</span>
              <select value={cycle} onChange={event => setCycle(event.target.value)}>
                {prices.map(item => (
                  <option key={item.billing_cycle} value={item.billing_cycle} disabled={priceLeft(item) === 0}>
                    {cycleName(item.billing_cycle)} · {walletMoney(item.charge_minor ?? item.amount_minor, item.currency)}
                    {item.charge_minor != null ? t('（按母机到期折算）') : ''}
                    {priceLeft(item) === 0 ? t('（已达限购次数）') : priceLeft(item) !== null ? t('（限购剩 {0} 次）', priceLeft(item)) : ''}
                  </option>
                ))}
              </select>
            </label>
            {price?.charge_minor != null && price.period_end && (
              <p className="notice-text wide">
                {t('母机 {0} 到期，早于{1}周期结束：按剩余时间折算，实付 {2}（原价 {3}），实例随母机在 {4} 当天结束时到期。续费按原价计费，同样不超过母机到期日。', node.expires_at, cycleName(price.billing_cycle), walletMoney(price.charge_minor, price.currency), walletMoney(price.amount_minor, price.currency), node.expires_at)}
              </p>
            )}
            <label>
              <span>{t('系统镜像')}</span>
              <select value={template} onChange={event => setTemplate(event.target.value)}>
                {osOptions(plan.allowed_template_ids).map(item => (
                  <option key={item.id} value={item.id}>{item.label}</option>
                ))}
              </select>
            </label>
            <CouponField planId={plan.id} cycle={cycle} onApplied={(code, discount) => { setCoupon(code); setDiscount(discount) }} />
            <p className="muted-text wide">
              {t('该实例由第三方机主提供，机主拥有服务器 root 权限，请勿存放敏感数据。母机到期日 {0}。 可在「我的 VPS」申请退款：{1}按剩余天数比例退到余额。', node.expires_at || t('未填写'), plan.early_refund ? t('购买 1 小时内且流量未超 1GB 全额退款，否则') : '')}
            </p>
            <div className="form-actions wide">
              <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
              <button className="primary-button compact" disabled={busy || !price}>
                {busy ? t('正在下单…') : t('下单 {0}', price ? walletMoney((price.charge_minor ?? price.amount_minor) + price.setup_fee_minor - discount, price.currency) : '')}
              </button>
            </div>
          </form>
        ) : (
          <div className="pay-choices">
            <p>
              {t('订单 {0} 已生成，应付', order.number)} <strong>{walletMoney(order.total_minor, order.currency)}</strong>
              {t('{0}。当前余额', !!order.discount_minor && t('（已优惠 {0}）', walletMoney(order.discount_minor, order.currency)))}
              <strong>{walletMoney(wallet?.balance_minor || 0, wallet?.currency)}</strong>{t('。')}
            </p>
            <div className="form-actions">
              <button className="primary-button compact" disabled={busy || !enough} onClick={payBalance}>
                {enough ? t('用余额支付') : t('余额不足')}
              </button>
              {!enough && <a className="primary-button compact" href={topupLink(order.total_minor - (wallet?.balance_minor || 0))}>{t('去充值')}</a>}
            </div>
            <p className="muted-text">
              {enough
                ? t('托管产品只支持余额支付。')
                : t('托管产品只支持余额支付，还差 {0}。充值后到「账单」里用余额支付这笔订单即可，订单会一直保留到账单到期。', walletMoney(order.total_minor - (wallet?.balance_minor || 0), order.currency))}
            </p>
          </div>
        )}
      </div>
    </div>
  )
}

// ---- My nodes ----

function MyNodes() {
  const [data, setData] = useState<HostingRecord | null>(() => cached<HostingRecord>('/api/v1/customer/hosting') ?? null)
  // publishing is the pending agent being published, or 'token' for the
  // fallback of pasting an agent token.
  const [publishing, setPublishing] = useState<PendingAgentRecord | 'token' | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () =>
    api<HostingRecord>('/api/v1/customer/hosting')
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
  }, [])
  // Freshly installed hosts show up on their own.
  useEffect(() => {
    const timer = window.setInterval(() => {
      if (!document.hidden) void load()
    }, 20000)
    return () => window.clearInterval(timer)
  }, [])

  async function dismiss(agent: PendingAgentRecord) {
    if (!(await confirmDialog({ title: t('从待接入列表移除 {0}？', agent.hostname || t('这台母机')), message: t('它重新连接后会再次出现。'), confirmText: t('移除') }))) return
    try {
      await api(`/api/v1/customer/hosting/agents/${agent.id}`, { method: 'DELETE' })
      void load()
      toast('success', t('已从待接入列表移除'))
    } catch (err) {
      toast('error', t('移除失败'), err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : t('移除失败'))
    }
  }

  if (!data) return error ? <div className="form-error">{error}</div> : null
  const active = data.nodes.filter(node => !node.retired_at)
  const retired = data.nodes.filter(node => node.retired_at)
  return (
    <>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <section className="metrics">
        <article>
          <span>{t('账户余额')}</span>
          <strong className={data.balance_minor < 0 ? 'amount-negative' : ''}>{walletMoney(data.balance_minor, data.currency)}</strong>
        </article>
        <article>
          <span>{t('待结算（托管中）')}</span>
          <strong>{walletMoney(active.reduce((sum, node) => sum + node.host_pending_minor, 0), data.currency)}</strong>
        </article>
        <article>
          <span>{t('已到账收益')}</span>
          <strong>{walletMoney(data.nodes.reduce((sum, node) => sum + node.host_released_minor, 0), data.currency)}</strong>
        </article>
        <article>
          <span>{t('平台手续费')}</span>
          <strong>{data.fee_percent}%</strong>
        </article>
      </section>

      <div className="page-actions">
        <div>
          <h3>{t('我的托管母机')}</h3>
          <p className="muted-text">{t('收益按天从托管资金释放到余额，只能用于本平台消费，不可提现。')}</p>
        </div>
        <div className="form-actions">
          <button className="secondary-button" onClick={() => void load()}>
            <RefreshCw size={15} />{t('刷新')}
          </button>
        </div>
      </div>
      {!data.enabled && <div className="note-banner warn">{t('托管中心暂未开放发布。')}</div>}
      {data.enabled && !publishing && (
        <section className="panel connect-panel">
          <div className="panel-heading">
            <h3>{t('接入新母机')}</h3>
            <button type="button" className="text-button" onClick={() => setPublishing('token')}>{t('已有 Agent 令牌？手动发布')}</button>
          </div>
          <ConnectSteps command={data.install_command} where={t('下方')} actionLabel={t('发布')} />
          <h4 className="subheading">{t('待接入的母机')}</h4>
          <PendingAgents agents={data.pending_agents ?? []} actionLabel={t('发布')} onAdd={setPublishing} onDismiss={agent => void dismiss(agent)} />
        </section>
      )}
      {publishing && (
        <PublishForm
          data={data}
          agent={publishing === 'token' ? undefined : publishing}
          onClose={() => setPublishing(null)}
          onPublished={() => {
            setPublishing(null)
            setNotice(t('母机已发布，接下来为它创建套餐。'))
            void load()
          }}
        />
      )}
      {active.map(node => (
        <HostedNodeCard key={node.id} node={node} data={data} onChanged={() => void load()} onNotice={setNotice} onError={setError} />
      ))}
      {!active.length && !publishing && <div className="empty-card">{t('还没有托管母机。按上面的步骤安装 Agent 后即可发布。')}</div>}
      {retired.length > 0 && (
        <div className="panel">
          <div className="panel-heading">
            <h3>{t('已清退的母机')}</h3>
          </div>
          <div className="table-wrap">
            <table>
              <thead>
                <tr>
                  <th>{t('母机')}</th>
                  <th>{t('清退时间')}</th>
                  <th>{t('原因')}</th>
                  <th>{t('已到账收益')}</th>
                </tr>
              </thead>
              <tbody>
                {retired.map(node => (
                  <tr key={node.id}>
                    <td>{node.name}</td>
                    <td>{node.retired_at ? formatTime(node.retired_at) : ''}</td>
                    <td>{tr(node.retired_reason)}</td>
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

function NodeInfoFields({
  node,
  regions,
  editing = false,
  defaultName,
}: {
  node?: HostedNodeRecord
  regions: HostingRecord['regions']
  editing?: boolean
  defaultName?: string
}) {
  return (
    <>
      <label>
        <span>{t('母机名称')}</span>
        <input name="name" required minLength={2} maxLength={40} defaultValue={node?.name ?? defaultName} placeholder={t('例如 HK-CN2-01')} />
      </label>
      {!editing && (
        <label>
          <span>{t('地域')}</span>
          <input name="region_name" required maxLength={40} list="hosting-regions" placeholder={t('选择或输入，例如 香港')} autoComplete="off" />
          <datalist id="hosting-regions">
            {regions.map(region => (
              <option key={region.id} value={region.name} />
            ))}
          </datalist>
          <small>{t('可以直接输入新地域，保存时自动创建')}</small>
        </label>
      )}
      <label>
        <span>{t('地理位置（真实填写）')}</span>
        <input name="location" required minLength={2} maxLength={80} defaultValue={node?.location} placeholder={t('例如 香港 葵涌 / 美国 洛杉矶')} />
        <small>{t('请写明国家或地区（如 香港、美国 洛杉矶、Tokyo JP）：托管市场按它把母机归入对应的国家/地区分类。')}</small>
      </label>
      <label>
        <span>{t('母机租约到期日')}</span>
        <input name="expires_at" type="date" required defaultValue={node?.expires_at} />
      </label>
      <label>
        <span>{t('整机月流量限额 GB（0 = 不限）')}</span>
        <input name="traffic_quota_gb" type="number" min="0" required defaultValue={node?.traffic_quota_gb ?? 0} />
      </label>
      <label className="wide">
        <span>{t('线路描述（真实填写）')}</span>
        <textarea name="line_description" required minLength={2} maxLength={500} rows={2} defaultValue={node?.line_description} placeholder={t('例如 三网 CN2 GIA 回程，去程 163，带宽 1Gbps 共享')} />
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

function PublishForm({ data, agent, onClose, onPublished }: { data: HostingRecord; agent?: PendingAgentRecord; onClose: () => void; onPublished: () => void }) {
  const [agreed, setAgreed] = useState<boolean[]>(data.rules.map(() => false))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const all = agreed.every(Boolean)
  const panel = useRef<HTMLFormElement>(null)
  useReveal(panel)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError('')
    try {
      await api('/api/v1/customer/hosting/nodes', {
        method: 'POST',
        body: JSON.stringify({
          ...nodeInfo(form),
          region_name: String(form.get('region_name') || '').trim(),
          enrollment_id: agent?.id ?? '',
          token: String(form.get('token') || '').trim(),
          agree_rules: all,
        }),
      })
      onPublished()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('发布失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel" onSubmit={submit} ref={panel}>
      <div className="panel-heading">
        <h3>{agent ? t('发布母机 {0}', agent.hostname) : t('手动发布托管母机')}</h3>
        <button type="button" className="icon-button" aria-label={t('关闭')} onClick={onClose}>
          <X size={16} />
        </button>
      </div>
      <ol className="hosting-rules">
        {data.rules.map((rule, index) => (
          <li key={index}>
            <label className="checkbox">
              <input type="checkbox" checked={agreed[index]} onChange={event => setAgreed(current => current.map((value, i) => (i === index ? event.target.checked : value)))} />
              <span>{tr(rule)}</span>
            </label>
          </li>
        ))}
      </ol>
      {agent ? (
        <p className="muted-text">
          {t('发布')} <strong>{agent.hostname || t('新母机')}</strong>{t('（{0}）。勾选同意全部准则并填写下面的信息，平台确认 Agent 在线后立即上架。', agent.public_ipv4 || agent.remote_ip)}
        </p>
      ) : (
        <p className="muted-text">{t('手动发布：填写母机信息和安装脚本最后打印的 64 位 Agent 令牌（可在母机上运行')} <code>hatch-agent token</code> {t('查看）。')}</p>
      )}
      {error && <div className="form-error">{error}</div>}
      <div className="form-grid">
        <NodeInfoFields regions={data.regions} defaultName={agent?.hostname} />
        {!agent && (
          <label className="wide">
            <span>{t('Agent 令牌')}</span>
            <input name="token" required pattern="[0-9a-fA-F]{64}" placeholder={t('安装脚本最后打印的 64 位令牌')} autoComplete="off" />
          </label>
        )}
        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button compact" disabled={busy || !all}>
            {busy ? t('正在验证母机…') : all ? t('验证并发布') : t('请先勾选同意全部准则')}
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
      onError(err instanceof Error ? err.message : t('操作失败'))
    }
  }

  async function saveInfo(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    try {
      await api(`/api/v1/customer/hosting/nodes/${node.id}`, { method: 'PUT', body: JSON.stringify({ ...nodeInfo(new FormData(event.currentTarget)), region_id: node.region_id }) })
      setEditing(false)
      onNotice(t('母机信息已更新'))
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : t('保存失败'))
    }
  }

  const remaining = (node.services || []).filter(item => item.status !== 'terminated').reduce((sum, item) => sum + item.remaining_value_minor, 0)
  return (
    <article className="panel hosted-node">
      <div className="panel-heading">
        <div>
          <h3>{node.name}</h3>
          <small>
            {t('{0} · {1} · 到期 {2}', node.region_name, node.location, node.expires_at)}
          </small>
          <small className="hosted-country">
            {t('市场分类：')}
            <CountryLabel code={node.country_code || ''} />
            {placementNote(node) && <span className={node.country_source === 'location' ? '' : 'warn-text'}>{placementNote(node)}</span>}
          </small>
        </div>
        <div className="form-actions">
          <span className={node.status === 'online' ? 'tag success' : 'tag danger'}>{lastSeen(node)}</span>
          <span className={node.listing_status === 'listed' ? 'tag success' : 'tag'}>{node.listing_status === 'listed' ? t('在售') : t('已暂停销售')}</span>
        </div>
      </div>
      {node.status !== 'online' && (
        <div className="note-banner warn">
          {t('母机离线。离线满 {0} 小时将被自动清退：剩余价值退还买家，并从您的余额按剩余价值额外赔付一份（余额不足时扣到 0 为止）。有特殊原因请尽快提交工单联系管理员。{1}', data.offline_hours, node.clearance_hold_until && t(' 管理员已暂缓清退至 {0}。', formatTime(node.clearance_hold_until)))}
        </div>
      )}
      <dl className="market-facts">
        <div><dt>{t('线路')}</dt><dd>{node.line_description}</dd></div>
        <div><dt>{t('可售资源')}</dt><dd>{t('{0} 核 / {1} MB / {2} GB，剩余 {3} 核 / {4} MB / {5} GB', node.capacity_vcpu, node.capacity_ram_mb, node.capacity_disk_gb, node.free_vcpu, node.free_ram_mb, node.free_disk_gb)}</dd></div>
        <div><dt>{t('运行实例')}</dt><dd>{node.active_services}</dd></div>
        <div><dt>{t('托管中 / 待结算')}</dt><dd>{walletMoney(node.escrow_holding_minor, data.currency)} / {walletMoney(node.host_pending_minor, data.currency)}</dd></div>
        <div><dt>{t('已到账')}</dt><dd>{walletMoney(node.host_released_minor, data.currency)}</dd></div>
      </dl>
      <SupplyDetails node={node} sellable={{ vcpu: node.capacity_vcpu, ram_mb: node.capacity_ram_mb, disk_gb: node.capacity_disk_gb }} />
      <div className="form-actions">
        <button className="secondary-button" onClick={() => setEditing(value => !value)}>{t('编辑信息')}</button>
        <button className="secondary-button" onClick={() => setOverselling(true)}>{t('超售设置')}</button>
        <button className="secondary-button" onClick={() => void call(`/api/v1/customer/hosting/nodes/${node.id}/listing`, { listed: node.listing_status !== 'listed' }, node.listing_status === 'listed' ? t('已暂停销售，现有实例不受影响') : t('已恢复销售'))}>
          {node.listing_status === 'listed' ? t('暂停销售') : t('恢复销售')}
        </button>
        <button className="primary-button compact" onClick={() => setPlanForm('new')}>
          <Plus size={14} />{t('新建套餐')}
        </button>
        <button className="danger-button" onClick={() => setRetiring(true)}>{t('下架母机')}</button>
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
            onNotice(t('超售设置已保存'))
            onChanged()
          }}
        />
      )}
      {editing && (
        <form className="form-grid" onSubmit={saveInfo}>
          <NodeInfoFields node={node} regions={data.regions} editing />
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={() => setEditing(false)}>{t('取消')}</button>
            <button className="primary-button compact">{t('保存')}</button>
          </div>
        </form>
      )}
      {retiring && (
        <div className="note-banner danger">
          <p>
            {t('下架后母机立即退出市场，现有 {0} 个实例全部清退：剩余价值（当前约 {1}）退还买家， 并从您的余额额外赔付最多 {2}（余额不足时扣到 0 为止）。此操作不可撤销。', node.active_services, walletMoney(remaining, data.currency), walletMoney(remaining, data.currency))}
          </p>
          <div className="form-actions">
            <button className="secondary-button" onClick={() => setRetiring(false)}>{t('取消')}</button>
            <button className="danger-button" onClick={() => void call(`/api/v1/customer/hosting/nodes/${node.id}/retire`, { confirm: true }, t('母机已下架并完成清退'))}>
              {t('确认下架并清退')}
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
            onNotice(t('套餐已保存'))
            onChanged()
          }}
        />
      )}
      <h4>{t('套餐')}</h4>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('套餐')}</th>
              <th>{t('配置')}</th>
              <th>{t('价格')}</th>
              <th>{t('状态')}</th>
              <th>{t('操作')}</th>
            </tr>
          </thead>
          <tbody>
            {node.plans.map(plan => (
              <tr key={plan.id}>
                <td>
                  <strong>{plan.name}</strong>
                  <small className="block">{virtNames[plan.virtualization]} · {osOptions(plan.allowed_template_ids).map(item => item.label).join(t('、'))}</small>
                  <small className="block">
                    {plan.purchase_limit ? t('每人限购 {0} 台', plan.purchase_limit) : t('不限购')} · {plan.early_refund ? t('允许早期全额退款') : t('按比例退款')}
                  </small>
                </td>
                <td>
                  {t('{0} 核 / {1} MB / {2} GB / {3} GB / NAT×{4}', plan.vcpu, plan.ram_mb, plan.disk_gb, plan.traffic_gb || t('不限'), plan.port_mapping_count)}
                  {diskIOText(plan) && <small className="block">{diskIOText(plan)}</small>}
                </td>
                <td>
                  {plan.prices.map(price => `${cycleName(price.billing_cycle)} ${walletMoney(price.amount_minor, price.currency)}${price.purchase_limit ? t('（限购 {0}，已售 {1}）', price.purchase_limit, price.sold ?? 0) : ''}`).join(t('，'))}
                  <small className="block">{plan.stock_limit == null ? t('库存不限（受资源限制）') : t('库存 {0}，已售及待支付 {1}', plan.stock_limit, plan.stock_held ?? 0)}</small>
                </td>
                <td><span className={plan.enabled ? 'tag success' : 'tag'}>{plan.enabled ? t('在售') : t('已停售')}</span></td>
                <td className="row-actions">
                  <button className="secondary-button compact" onClick={() => setPlanForm(plan)}>{t('编辑')}</button>
                  <button className="secondary-button compact" onClick={() => void call(`/api/v1/customer/hosting/plans/${plan.id}/enabled`, { enabled: !plan.enabled }, plan.enabled ? t('套餐已停售') : t('套餐已上架'))}>
                    {plan.enabled ? t('停售') : t('上架')}
                  </button>
                </td>
              </tr>
            ))}
            {!node.plans.length && (
              <tr>
                <td colSpan={5} className="empty-state">{t('还没有套餐，买家看不到这台母机。')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <h4>{t('母机上的实例')}</h4>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('实例')}</th>
              <th>{t('套餐')}</th>
              <th>{t('买家')}</th>
              <th>{t('状态')}</th>
              <th>{t('到期')}</th>
              <th>{t('剩余价值')}</th>
            </tr>
          </thead>
          <tbody>
            {(node.services || []).map(item => (
              <tr key={item.id}>
                <td><code>{item.instance_name}</code></td>
                <td>{item.plan_name}</td>
                <td>{item.buyer_name}</td>
                <td><span className="badge-pair"><StatusBadge status={item.status} /><StatusBadge status={item.runtime_status} /></span></td>
                <td>{item.next_due_at ? formatDate(item.next_due_at) : '—'}</td>
                <td>{walletMoney(item.remaining_value_minor, data.currency)}</td>
              </tr>
            ))}
            {!node.services?.length && (
              <tr>
                <td colSpan={6} className="empty-state">{t('暂无实例')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </article>
  )
}

function HostedPlanForm({ node, plan, onClose, onSaved }: { node: HostedNodeRecord; plan?: PlanRecord; onClose: () => void; onSaved: () => void }) {
  const [images, setImages] = useState<NodeImageRecord[]>(() => cached<NodeImageRecord[]>(`/api/v1/customer/hosting/nodes/${node.id}/templates`) ?? [])
  const [virtualization, setVirtualization] = useState<string>(plan?.virtualization || node.virtualization_types[0] || 'lxc')
  const [allowed, setAllowed] = useState<string[]>(plan?.allowed_template_ids || [])
  const [fallback, setFallback] = useState(plan?.default_template_id || '')
  const [limited, setLimited] = useState(!!plan?.purchase_limit)
  const [tags, setTags] = useState<string[]>(plan?.tags ?? [])
  const [capacity, setCapacity] = useState<StockCapacityRecord | null>(null)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  useEffect(() => {
    api<NodeImageRecord[]>(`/api/v1/customer/hosting/nodes/${node.id}/templates`)
      .then(setImages)
      .catch(err => setError(err instanceof Error ? err.message : t('无法读取母机镜像')))
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
      tags,
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
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel nested-panel" onSubmit={submit}>
      <div className="panel-heading">
        <h3>{plan ? t('编辑套餐 {0}', plan.name) : t('新建套餐')}</h3>
        <button type="button" className="icon-button" aria-label={t('关闭')} onClick={onClose}>
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <p className="muted-text">
        {t('单个实例配置不能超过母机真实资源（{0} 核 / {1} MB / {2} GB）；可售总量为真实资源乘以「超售设置」里的倍数。', node.reported_vcpu, node.reported_ram_mb, node.reported_disk_gb)}
      </p>
      <div className="form-grid">
        <label>
          <span>{t('套餐名称')}</span>
          <input name="name" required minLength={2} maxLength={40} defaultValue={plan?.name} />
        </label>
        <label>
          <span>{t('虚拟化')}</span>
          <select name="virtualization" value={virtualization} onChange={event => { setVirtualization(event.target.value); setAllowed([]) }}>
            {node.virtualization_types.map(item => (
              <option key={item} value={item}>{virtNames[item] || item}</option>
            ))}
          </select>
        </label>
        <label><span>vCPU</span><input name="vcpu" type="number" min="1" required defaultValue={plan?.vcpu ?? 1} /></label>
        <label><span>{t('内存 MB')}</span><input name="ram_mb" type="number" min="64" required defaultValue={plan?.ram_mb ?? 512} /></label>
        <label><span>{t('磁盘 GB')}</span><input name="disk_gb" type="number" min="1" required defaultValue={plan?.disk_gb ?? 5} /></label>
        <label><span>{t('月流量 GB（0 = 不限）')}</span><input name="traffic_gb" type="number" min="0" required defaultValue={plan?.traffic_gb ?? 100} /></label>
        <label><span>{t('下行 Mbps（0 = 不限）')}</span><input name="network_down_mbps" type="number" min="0" required defaultValue={plan?.network_down_mbps ?? 50} /></label>
        <label><span>{t('上行 Mbps（0 = 不限）')}</span><input name="network_up_mbps" type="number" min="0" required defaultValue={plan?.network_up_mbps ?? 20} /></label>
        <label><span>{t('NAT 端口数')}</span><input name="port_mapping_count" type="number" min="1" max="100" required defaultValue={plan?.port_mapping_count ?? 5} /></label>
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
          hint={t('留空表示不支持该计费周期，至少填写一个。母机到期日早于周期结束时，买家按剩余时间折算付款（例如季付 30、母机只剩 2 个月，买家付 20），实例到期日与母机到期日相同。')}
        />
        <fieldset className="wide template-picker">
          <legend>{t('可选系统镜像（读取自母机）')}</legend>
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
          {!visible.length && <span className="muted-text">{t('母机上没有这种虚拟化的镜像。')}</span>}
        </fieldset>
        <label>
          <span>{t('默认镜像')}</span>
          <select value={allowed.includes(fallback) ? fallback : allowed[0] || ''} onChange={event => setFallback(event.target.value)}>
            {allowed.map(item => (
              <option key={item} value={item}>{item}</option>
            ))}
          </select>
        </label>
        <label className="wide">
          <span>{t('套餐描述（可选，展示在托管市场）')}</span>
          <textarea name="description" maxLength={1000} rows={3} defaultValue={plan?.description} placeholder={t('例如适用场景、线路特点、是否支持某些用途')} />
        </label>
        <label className="wide">
          <span>{t('标签（可选，展示在托管市场，买家可以按标签搜索）')}</span>
          <TagInput value={tags} onChange={setTags} placeholder={t('如：CN2 GIA、原生 IP、解锁流媒体')} />
        </label>
        <div className="wide">
          <label className="checkbox">
            <input type="checkbox" checked={limited} onChange={event => setLimited(event.target.checked)} />
            <span>{t('限购')}</span>
          </label>
          {limited && (
            <label className="inline-field">
              <span>{t('每人最多')}</span>
              <input name="purchase_limit" type="number" min="1" max="100" required defaultValue={plan?.purchase_limit || 1} />
              <span>{t('台（按有效实例计算）')}</span>
            </label>
          )}
        </div>
        <label className="checkbox wide">
          <input name="early_refund" type="checkbox" defaultChecked={plan?.early_refund ?? false} />
          <span>{t('允许早期全额退款：买家在购买 1 小时内且流量未超 1GB 时可申请全额退款；关闭则一律按剩余天数比例退款')}</span>
        </label>
        <label className="checkbox">
          <input name="enabled" type="checkbox" defaultChecked={plan?.enabled ?? true} />
          <span>{t('立即上架')}</span>
        </label>
        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button compact" disabled={busy || !allowed.length}>{busy ? t('正在保存…') : t('保存套餐')}</button>
        </div>
      </div>
    </form>
  )
}

// ---- Host coupons ----

function HostCoupons() {
  const [data, setData] = useState<HostingRecord | null>(() => cached<HostingRecord>('/api/v1/customer/hosting') ?? null)
  const [error, setError] = useState('')
  useEffect(() => {
    api<HostingRecord>('/api/v1/customer/hosting')
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
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
      intro={t('优惠码只对你自己母机上的套餐有效，优惠从实付金额中扣除，平台按实付金额收取手续费。')}
    />
  )
}

// ---- Hosted tickets ----

function HostTickets() {
  const [tickets, setTickets] = useState<TicketRecord[]>(() => cached<TicketRecord[]>('/api/v1/customer/hosting/tickets') ?? [])
  const [detail, setDetail] = useState<TicketDetailRecord | null>(null)
  const [error, setError] = useState('')
  const maxMB = useAttachmentLimit()

  const load = () =>
    api<TicketRecord[]>('/api/v1/customer/hosting/tickets')
      .then(setTickets)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  const open = (id: string) =>
    api<TicketDetailRecord>(`/api/v1/customer/hosting/tickets/${id}`)
      .then(setDetail)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
  }, [])

  return (
    <>
      {error && <div className="form-error">{error}</div>}
      <p className="muted-text">{t('购买您母机实例的用户提交的工单会出现在这里，您是第一处理人；平台管理员也能看到并介入。')}</p>
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
          {!tickets.length && <div className="empty-state">{t('暂无托管工单')}</div>}
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
                setError(err instanceof Error ? err.message : t('回复失败'))
                return false
              }
            }}
          />
        ) : (
          <div className="panel support-placeholder">{t('选择左侧工单查看详情')}</div>
        )}
      </div>
    </>
  )
}

// ---- Chat ----

function ChatRooms() {
  const [rooms, setRooms] = useState<ChatRoomRecord[]>(() => cached<ChatRoomRecord[]>('/api/v1/customer/chat/rooms') ?? [])
  const [selected, setSelected] = useState(() => rooms[0]?.node_id ?? '')
  const [error, setError] = useState('')

  useEffect(() => {
    api<ChatRoomRecord[]>('/api/v1/customer/chat/rooms')
      .then(value => {
        setRooms(value)
        if (value.length) setSelected(current => current || value[0].node_id)
      })
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [])

  const room = rooms.find(item => item.node_id === selected)
  return (
    <>
      {error && <div className="form-error">{error}</div>}
      <p className="muted-text">{t('每台托管母机一个聊天室，成员是机主和在这台母机上有实例的用户，平台管理员可以查看全部记录。')}</p>
      <div className="support-layout">
        <div className="ticket-list">
          {rooms.map(item => (
            <button key={item.node_id} className={item.node_id === selected ? 'ticket-row selected' : 'ticket-row'} onClick={() => setSelected(item.node_id)}>
              <div>
                <strong>{item.node_name}</strong>
                <small>
                  {t('{0} · {1} 人{2}', item.role === 'host' ? t('我是机主') : t('机主 {0}', item.host_name), item.members, item.retired ? t(' · 已清退') : '')}
                </small>
                {item.last_message && <small className="block">{t('{0}：{1}', item.last_message.author_name, item.last_message.body.slice(0, 40))}</small>}
              </div>
            </button>
          ))}
          {!rooms.length && <div className="empty-state">{t('购买托管实例或发布母机后会自动加入对应聊天室。')}</div>}
        </div>
        {room ? (
          <ChatRoom base="/api/v1/customer/chat/rooms" nodeID={room.node_id} title={t('{0} 聊天室', room.node_name)} />
        ) : (
          <div className="panel support-placeholder">{t('暂无聊天室')}</div>
        )}
      </div>
    </>
  )
}
