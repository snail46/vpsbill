import { FormEvent, useEffect, useState } from 'react'
import { AlertTriangle, ArrowLeftRight, RefreshCw, Tag, Upload, X } from 'lucide-react'
import { api, cached, type CustomerServiceRecord, type TradeListingRecord, type TradeRecord, type WalletRecord } from './api'
import { walletMoney } from './Wallet'
import { converted } from './shared/currency'
import { Charged, CurrencyNote } from './shared/LocaleMenu'
import { formatDate, formatTime } from './shared/time'
import { cycleName } from './shared/cycles'
import { siteMeta } from './shared/boot'
import { promptDialog } from './shared/dialog'
import { bandwidthLabel } from './shared/ui'
import { toast } from './shared/toast'
import { navigatePortal } from './shared/nav'

const statusNames: Record<TradeListingRecord['status'], string> = { listed: '挂售中', sold: '已售出', cancelled: '已下架' }

// tradeHoldDays is how long an instance must be held before it may be
// listed, set in the site settings.
export function tradeHoldDays() {
  return siteMeta()?.trade_hold_days ?? 31
}

export function formatBytes(value = 0) {
  if (value >= 1024 ** 4) return `${(value / 1024 ** 4).toFixed(2)} TB`
  if (value >= 1024 ** 3) return `${(value / 1024 ** 3).toFixed(2)} GB`
  if (value >= 1024 ** 2) return `${(value / 1024 ** 2).toFixed(1)} MB`
  if (value >= 1024) return `${(value / 1024).toFixed(1)} KB`
  return `${value} B`
}

// trafficText shows a two-way total with its split when known.
export function trafficText(total: number, rx?: number | null, tx?: number | null) {
  return rx != null && tx != null ? `${formatBytes(total)}（下行 ${formatBytes(rx)} / 上行 ${formatBytes(tx)}）` : formatBytes(total)
}

function daysLeft(value: string | null) {
  if (!value) return '—'
  const days = Math.floor((new Date(value).getTime() - Date.now()) / 86400000)
  return days >= 0 ? `${formatDate(value)}（剩 ${days} 天）` : formatDate(value)
}

// tradeEligibleAt is when an instance may be listed, counted from when the
// current owner got it.
export function tradeEligibleAt(service: CustomerServiceRecord) {
  const since = new Date(service.acquired_at || Date.now())
  return new Date(since.getTime() + tradeHoldDays() * 86400000)
}

function RiskBanner() {
  return (
    <div className="note-banner warn trade-risk" role="alert">
      <AlertTriangle size={16} />
      <span>
        <strong>交易有风险，购买不退款。</strong>平台不保证机器的长期稳定性，请谨慎购买。
      </span>
    </div>
  )
}

function ListingSpecs({ listing }: { listing: TradeListingRecord }) {
  return (
    <>
      <div className="shop-specs">
        <span>{listing.vcpu} vCPU</span>
        <span>{listing.ram_mb} MB 内存</span>
        <span>{listing.disk_gb} GB 磁盘</span>
        <span>{listing.traffic_gb ? `${listing.traffic_gb} GB 流量` : '不限流量'}</span>
        <span>{bandwidthLabel(listing.network_down_mbps)}</span>
        <span>NAT × {listing.port_mapping_count}</span>
      </div>
      <dl className="market-facts">
        <div>
          <dt>套餐</dt>
          <dd>{listing.plan_name} · {listing.virtualization.toUpperCase()}</dd>
        </div>
        <div>
          <dt>地域</dt>
          <dd>{listing.region_name}{listing.host_name ? ` · 托管母机（机主 ${listing.host_name}）` : ' · 平台自营'}</dd>
        </div>
        <div>
          <dt>到期</dt>
          <dd>{daysLeft(listing.expires_at)}</dd>
        </div>
        <div>
          <dt>续费</dt>
          <dd>
            {listing.renewal_minor !== null ? `${walletMoney(listing.renewal_minor, listing.currency)} / ${cycleName(listing.billing_cycle)}` : '续费价格暂不可用'}
          </dd>
        </div>
        <div>
          <dt>本月流量</dt>
          <dd>{trafficText(listing.traffic_bytes, listing.traffic_rx_bytes, listing.traffic_tx_bytes)}，上架时读取，挂售期间实例已停机</dd>
        </div>
        <div>
          <dt>开通于</dt>
          <dd>{formatDate(listing.service_created_at)}</dd>
        </div>
      </dl>
      {listing.note && <p className="plan-description">卖家说明：{listing.note}</p>}
    </>
  )
}

export default function TradeMarket() {
  const [data, setData] = useState<TradeRecord | null>(() => cached<TradeRecord>('/api/v1/customer/trade') ?? null)
  const [tab, setTab] = useState<'market' | 'records'>('market')
  const [buying, setBuying] = useState<TradeListingRecord | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () =>
    api<TradeRecord>('/api/v1/customer/trade')
      .then(setData)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  async function cancel(listing: TradeListingRecord) {
    setError('')
    try {
      await api(`/api/v1/customer/trade/listings/${listing.id}/cancel`, { method: 'POST' })
      setNotice('已下架')
      void load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '下架失败')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">TRADING MARKET</p>
          <h2>交易市场</h2>
          <p>
            用户之间转让实例，用账户余额成交。持有满 {data?.hold_days ?? tradeHoldDays()} 天、距到期至少 {data?.min_remaining_days ?? 3} 天的实例可以在「我的 VPS」点「Push 挂售」上架；
            挂售期间实例停机、卖家不能使用，到期时间照常计算。到期仍未售出会自动下架并暂停，3 天内续费可恢复，否则系统回收。平台收取成交价 {data?.fee_percent ?? 20}% 的手续费，由卖家承担。
          </p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>
      <RiskBanner />
      <CurrencyNote />
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <div className="segmented" role="tablist">
        <button role="tab" aria-selected={tab === 'market'} className={tab === 'market' ? 'active' : ''} onClick={() => setTab('market')}>
          <ArrowLeftRight size={15} />在售实例
        </button>
        <button role="tab" aria-selected={tab === 'records'} className={tab === 'records' ? 'active' : ''} onClick={() => setTab('records')}>
          <Tag size={15} />挂售记录
        </button>
      </div>
      {tab === 'market' && (
        <div className="market-grid">
          {data?.listings.map(listing => (
            <article key={listing.id} className="panel market-node">
              <div className="panel-heading">
                <div>
                  <h3>{listing.plan_name}</h3>
                  <small>卖家 {listing.seller_name}{listing.mine ? '（我自己）' : ''} · 挂售于 {formatDate(listing.created_at)}</small>
                </div>
                <strong className="trade-price">{walletMoney(listing.price_minor, listing.currency)}</strong>
              </div>
              <ListingSpecs listing={listing} />
              <div className="market-plan-buy">
                {listing.node_online ? <span /> : <span className="tag danger">母机离线，暂不可购买</span>}
                <button className="primary-button compact" disabled={listing.mine || !listing.node_online} onClick={() => setBuying(listing)}>购买</button>
              </div>
            </article>
          ))}
          {data && !data.listings.length && <div className="empty-card">暂时没有在售的实例。</div>}
        </div>
      )}
      {tab === 'records' && <TradeRecords data={data} onCancel={listing => void cancel(listing)} />}
      {buying && (
        <BuyListing
          listing={buying}
          onClose={() => setBuying(null)}
          onDone={() => {
            setBuying(null)
            setNotice('购买成功，实例已转入「我的 VPS」。请尽快重置 root 密码，必要时重装系统。')
            void load()
          }}
        />
      )}
    </section>
  )
}

const closedByNames: Record<string, string> = { seller: '你已下架', staff: '管理员下架', system: '系统自动下架', expiry: '到期自动下架' }

// TradeRecords is the account's history in the market: what it listed
// (open, sold or closed, and by whom) and what it bought.
function TradeRecords({ data, onCancel }: { data: TradeRecord | null; onCancel: (listing: TradeListingRecord) => void }) {
  const rows = [
    ...(data?.mine ?? []).map(listing => ({ listing, bought: false })),
    ...(data?.purchases ?? []).map(listing => ({ listing, bought: true })),
  ].sort((a, b) => {
    const open = Number(b.listing.status === 'listed') - Number(a.listing.status === 'listed')
    return open || new Date(b.listing.updated_at || b.listing.created_at).getTime() - new Date(a.listing.updated_at || a.listing.created_at).getTime()
  })
  return (
    <div className="panel">
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>类型</th>
              <th>实例</th>
              <th>价格</th>
              <th>状态</th>
              <th>挂售时间</th>
              <th>结束时间</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map(({ listing, bought }) => (
              <tr key={(bought ? 'b' : 's') + listing.id}>
                <td><span className={bought ? 'tag info' : 'tag'}>{bought ? '买入' : '卖出'}</span></td>
                <td>
                  <strong>{listing.instance_name}</strong>
                  <small className="block">{listing.plan_name} · {listing.region_name}</small>
                </td>
                <td>
                  {walletMoney(listing.price_minor, listing.currency)}
                  {!bought && listing.seller_proceeds_minor != null && (
                    <small className="block">到账 {walletMoney(listing.seller_proceeds_minor, listing.currency)}（手续费 {walletMoney(listing.fee_minor || 0, listing.currency)}）</small>
                  )}
                </td>
                <td>
                  <span className={listing.status === 'sold' ? 'tag success' : listing.status === 'listed' ? 'tag info' : 'tag'}>
                    {listing.status === 'sold' ? (bought ? '已买入' : '已售出') : listing.status === 'listed' ? '挂售中' : closedByNames[listing.cancelled_by || 'seller']}
                  </span>
                  {listing.status === 'listed' && !listing.available && <small className="block">实例不是正常运行状态，买家看不到</small>}
                  {listing.status === 'cancelled' && listing.cancel_reason && <small className="block">{listing.cancel_reason}</small>}
                  {bought && <small className="block">卖家 {listing.seller_name}</small>}
                </td>
                <td>{formatTime(listing.created_at)}</td>
                <td>{listing.status === 'sold' && listing.sold_at ? formatTime(listing.sold_at) : listing.status === 'cancelled' && listing.updated_at ? formatTime(listing.updated_at) : '—'}</td>
                <td>
                  {listing.status === 'listed' && !bought && (
                    <button className="secondary-button compact" onClick={() => onCancel(listing)}>下架</button>
                  )}
                </td>
              </tr>
            ))}
            {data && !rows.length && (
              <tr>
                <td colSpan={7} className="muted-text">还没有交易记录。在「我的 VPS」点实例上的「Push 挂售」即可上架。</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function BuyListing({ listing, onClose, onDone }: { listing: TradeListingRecord; onClose: () => void; onDone: () => void }) {
  const [wallet, setWallet] = useState<WalletRecord | null>(() => cached<WalletRecord>('/api/v1/customer/wallet') ?? null)
  const [accepted, setAccepted] = useState(false)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  useEffect(() => {
    api<WalletRecord>('/api/v1/customer/wallet').then(setWallet).catch(() => undefined)
  }, [])
  const enough = wallet ? wallet.balance_minor >= listing.price_minor : false

  async function buy() {
    setBusy(true)
    setError('')
    try {
      await api(`/api/v1/customer/trade/listings/${listing.id}/buy`, {
        method: 'POST',
        body: JSON.stringify({ expected_minor: listing.price_minor, accept_risk: accepted }),
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '购买失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <div className="modal panel">
        <div className="panel-heading">
          <div>
            <p className="eyebrow">交易市场 · 卖家 {listing.seller_name}</p>
            <h3>购买 {listing.plan_name}</h3>
          </div>
          <button className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <RiskBanner />
        <ListingSpecs listing={listing} />
        {error && <div className="form-error">{error}</div>}
        <ul className="trade-terms">
          <li>用账户余额支付 <strong>{walletMoney(listing.price_minor, listing.currency)}</strong><Charged minor={listing.price_minor} currency={listing.currency} />，当前余额 {walletMoney(wallet?.balance_minor || 0, wallet?.currency)}。</li>
          <li>成交后实例连同剩余时长转入你的账户并自动开机，之后按上面的续费价格续费。</li>
          <li>原主人知道这台机器的密码，也可能留下了其他登录方式：请立即重置 root 密码，必要时重装系统。</li>
          <li>交易不退款；托管母机上的实例仍受托管清退规则保障。</li>
        </ul>
        <label className="checkbox">
          <input type="checkbox" checked={accepted} onChange={event => setAccepted(event.target.checked)} />
          <span>我已了解交易风险，购买后不退款</span>
        </label>
        <div className="form-actions">
          <button className="secondary-button" onClick={onClose}>取消</button>
          {!enough && wallet && <a className="secondary-button" href="/portal/wallet">去充值</a>}
          <button className="primary-button compact" disabled={busy || !accepted || !enough} onClick={() => void buy()}>
            {busy ? '正在购买…' : enough ? `确认购买 ${walletMoney(listing.price_minor, listing.currency)}` : '余额不足'}
          </button>
        </div>
      </div>
    </div>
  )
}

// minRemainingDays is how much paid time an instance needs to be listed.
const minRemainingDays = 3

// pushBlocker says why an instance cannot be listed now, or '' when it can.
export function pushBlocker(service: CustomerServiceRecord) {
  if (service.listing_id) return '这台实例已经在交易市场挂售中'
  if (service.status !== 'active') return '只有正常运行中的实例可以挂售'
  const eligibleAt = tradeEligibleAt(service)
  if (eligibleAt.getTime() > Date.now()) return `持有满 ${tradeHoldDays()} 天的实例才能挂售，${formatDate(eligibleAt)} 起可挂售`
  if (!service.next_due_at || new Date(service.next_due_at).getTime() - Date.now() < minRemainingDays * 86400000) {
    return `距到期不足 ${minRemainingDays} 天的实例不能挂售，请先续费`
  }
  if (service.desired_runtime_status) return '实例正在开关机，请稍后再挂售'
  if (service.traffic_locked_month) return '实例因流量用尽已停止，下月恢复后才能挂售'
  return ''
}

// PushButton lists an instance in the trading market in one step: it
// opens the price form, or says why the instance cannot be listed yet.
export function PushButton({ service, onDone, className = 'secondary-button compact' }: { service: CustomerServiceRecord; onDone: () => void; className?: string }) {
  const [open, setOpen] = useState(false)
  if (service.listing_id) {
    return (
      <a className={className} href="/portal/trade" onClick={event => { event.preventDefault(); navigatePortal('/portal/trade') }} title="查看挂售">
        <Tag size={14} />挂售中 · {walletMoney(service.listing_price_minor || 0)}
      </a>
    )
  }
  const blocker = pushBlocker(service)
  return (
    <>
      <button
        type="button"
        className={blocker ? `${className} push-blocked` : className}
        title={blocker || '挂售到交易市场'}
        aria-disabled={Boolean(blocker)}
        onClick={() => (blocker ? toast('info', '暂时不能挂售', blocker) : setOpen(true))}
      >
        <Upload size={14} />Push 挂售
      </button>
      {open && (
        <ListServiceDialog
          service={service}
          onClose={() => setOpen(false)}
          onDone={() => {
            setOpen(false)
            toast('success', '已上架到交易市场', `${service.instance_name} 已挂售，挂售期间实例保持停机。`)
            onDone()
          }}
        />
      )}
    </>
  )
}

// ListServiceDialog puts one of the customer's instances up for sale.
export function ListServiceDialog({ service, onClose, onDone }: { service: CustomerServiceRecord; onClose: () => void; onDone: () => void }) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [feePercent, setFeePercent] = useState<number | null>(() => cached<TradeRecord>('/api/v1/customer/trade')?.fee_percent ?? null)
  const [price, setPrice] = useState(0)
  useEffect(() => {
    api<TradeRecord>('/api/v1/customer/trade').then(value => setFeePercent(value.fee_percent)).catch(() => undefined)
  }, [])

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError('')
    try {
      await api('/api/v1/customer/trade/listings', {
        method: 'POST',
        body: JSON.stringify({ service_id: service.id, price_minor: Math.round(Number(form.get('price')) * 100), note: form.get('note') }),
      })
      onDone()
    } catch (err) {
      setError(err instanceof Error ? err.message : '挂售失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <form className="modal panel" onSubmit={submit}>
        <div className="panel-heading">
          <div>
            <p className="eyebrow">交易市场</p>
            <h3>挂售 {service.instance_name}</h3>
          </div>
          <button type="button" className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        <RiskBanner />
        {error && <div className="form-error">{error}</div>}
        <div className="form-grid">
          <label>
            <span>挂售价格（人民币 元）{converted() && price > 0 && <small> ≈ {walletMoney(Math.round(price * 100))}</small>}</span>
            <input name="price" type="number" min="1" max="100000" step="0.01" required onChange={event => setPrice(Number(event.target.value))} />
          </label>
          <label className="wide">
            <span>说明（可选，买家可见）</span>
            <textarea name="note" maxLength={500} rows={3} placeholder="例如出售原因、用途限制" />
          </label>
          <p className="muted-text wide">
            挂售后实例会立即停机，挂售期间你不能开机、登录、重装或修改；到期时间照常计算，不会因为挂售而延长。实例到期时仍未售出会自动下架并暂停，3 天内续费即可恢复，否则系统回收实例。
            买家看得到套餐配置、地域、到期时间、续费价格和上架时的本月流量，看不到 IP 和密码。成交后扣除 {feePercent ?? 20}% 平台手续费，余下的存入你的余额（不可提现）。可以随时下架，下架后自行开机。
          </p>
          <div className="form-actions wide">
            <button type="button" className="secondary-button" onClick={onClose}>取消</button>
            <button className="primary-button compact" disabled={busy}>{busy ? '正在挂售…' : '挂售'}</button>
          </div>
        </div>
      </form>
    </div>
  )
}

// AdminTradeListings lets staff watch the market and take listings down.
export function AdminTradeListings() {
  const [rows, setRows] = useState<TradeListingRecord[]>(() => cached<TradeListingRecord[]>('/api/v1/admin/trade/listings') ?? [])
  const [error, setError] = useState('')
  const load = () =>
    api<TradeListingRecord[]>('/api/v1/admin/trade/listings')
      .then(setRows)
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  async function cancel(listing: TradeListingRecord) {
    const reason = await promptDialog({
      title: '强制下架',
      message: '下架原因会记录在审计日志，并显示给卖家。',
      label: '下架原因',
      defaultValue: '违反交易规则',
      required: true,
      confirmText: '下架',
      danger: true,
    })
    if (reason === null) return
    try {
      await api(`/api/v1/admin/trade/listings/${listing.id}/cancel`, { method: 'POST', body: JSON.stringify({ reason }) })
      void load()
      toast('success', '已强制下架')
    } catch (err) {
      toast('error', '下架失败', err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : '下架失败')
    }
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h3>交易市场</h3>
          <p className="muted-text">用户之间转让实例的挂售记录。成交用余额结算，平台不收手续费。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>实例</th>
              <th>卖家</th>
              <th>价格</th>
              <th>状态</th>
              <th>挂售时间</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {rows.map(listing => (
              <tr key={listing.id}>
                <td>
                  <strong>{listing.instance_name}</strong>
                  <small className="block">{listing.plan_name} · {listing.region_name}{listing.host_name ? ` · 机主 ${listing.host_name}` : ''}</small>
                </td>
                <td>{listing.seller_name}</td>
                <td>{walletMoney(listing.price_minor, listing.currency)}</td>
                <td>
                  <span className={listing.status === 'sold' ? 'tag success' : 'tag'}>{statusNames[listing.status]}</span>
                  {listing.cancel_reason && <small className="block">{listing.cancel_reason}</small>}
                  {listing.sold_at && <small className="block">成交于 {formatTime(listing.sold_at)}</small>}
                </td>
                <td>{formatTime(listing.created_at)}</td>
                <td>{listing.status === 'listed' && <button className="secondary-button compact" onClick={() => void cancel(listing)}>下架</button>}</td>
              </tr>
            ))}
            {!rows.length && (
              <tr>
                <td colSpan={6} className="muted-text">还没有挂售记录。</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
