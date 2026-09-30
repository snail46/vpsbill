import { KeyboardEvent, useEffect, useState } from 'react'
import { Activity, ChevronRight, CircleDollarSign, Headphones, ReceiptText } from 'lucide-react'
import { api, cached, OperationsOverviewRecord } from '../api'
import { money, CapacityBar } from '../shared/ui'
import { AdminLink, adminHref, daysAgo, navigateAdmin } from './filters'

// Every card opens the list behind its number, already filtered.
const links = {
  revenue: adminHref('billing', { tx_type: 'payment', tx_status: 'succeeded', from: daysAgo(29) }, 'transactions'),
  outstanding: adminHref('billing', { invoice_status: 'open' }),
  running: adminHref('services', { runtime: 'running' }),
  services: adminHref('services'),
  tickets: adminHref('support', { open: 1 }),
  orders: adminHref('orders', { from: daysAgo(29) }),
  customers: adminHref('customers', { status: 'active' }),
  nodes: adminHref('nodes'),
  offlineNodes: adminHref('nodes', { status: 'offline' }),
  overdue: adminHref('services', { status: 'overdue,suspended' }),
  failedJobs: adminHref('services', { job: 'failed,dead' }, 'jobs'),
  pendingJobs: adminHref('services', { job: 'pending,running' }, 'jobs'),
}

// open makes a whole card a link; links inside it keep their own target.
const open = (href: string) => ({
  role: 'link',
  tabIndex: 0,
  className: 'clickable-card',
  onClick: () => navigateAdmin(href),
  onKeyDown: (event: KeyboardEvent) => {
    if (event.key === 'Enter' || event.key === ' ') {
      event.preventDefault()
      navigateAdmin(href)
    }
  },
})

export function Overview() {
  const [data, setData] = useState<OperationsOverviewRecord | null>(() => cached<OperationsOverviewRecord>('/api/v1/admin/overview') ?? null)
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
  const offline = data.nodes - data.online_nodes

  return (
    <section className="workspace-panel">
      <section {...open(links.revenue)} className="hero-card operations-hero clickable-card" title="查看近 30 天入账流水">
        <div>
          <p className="eyebrow">30-DAY BUSINESS PERFORMANCE</p>
          <h2>{revenue} 实收金额到账</h2>
          <p>
            近 30 天已成交 <AdminLink href={links.orders} className="inline-link">{data.orders_30_days} 笔销售订单</AdminLink> · 当前保有{' '}
            <AdminLink href={links.customers} className="inline-link">{data.accounts} 个有效客户</AdminLink> · 在管{' '}
            <AdminLink href={links.services} className="inline-link">{data.services} 台 VPS 实例</AdminLink>
          </p>
        </div>
        <AdminLink href={links.nodes} className="hero-signal" title="查看节点">
          <span>{nodeRate}%</span>
          <small>节点在线率</small>
        </AdminLink>
      </section>

      <section className="metrics">
        <article {...open(links.revenue)} title="查看近 30 天入账流水">
          <CircleDollarSign size={20} />
          <span>30 天入账收入</span>
          <strong>{revenue}</strong>
          <ChevronRight size={16} className="card-arrow" aria-hidden="true" />
        </article>
        <article {...open(links.outstanding)} title="查看未付账单">
          <ReceiptText size={20} />
          <span>待收金额</span>
          <strong>{outstanding}</strong>
          <small>{data.open_invoices} 张未付账单</small>
          <ChevronRight size={16} className="card-arrow" aria-hidden="true" />
        </article>
        <article {...open(links.running)} title="查看运行中的实例">
          <Activity size={20} />
          <span>运行中 VPS</span>
          <strong>
            {data.running_services} / {data.services}
          </strong>
          <ChevronRight size={16} className="card-arrow" aria-hidden="true" />
        </article>
        <article {...open(links.tickets)} title="查看待处理工单">
          <Headphones size={20} />
          <span>待处理工单</span>
          <strong>{data.open_tickets}</strong>
          <ChevronRight size={16} className="card-arrow" aria-hidden="true" />
        </article>
      </section>

      <div className="content-grid">
        <section {...open(links.nodes)} className="panel clickable-card" title="查看节点">
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
            <AdminLink href={links.overdue} title="查看逾期和已暂停的实例">
              逾期欠费服务
              <strong style={{ color: data.overdue_services > 0 ? 'var(--danger-text)' : 'inherit' }}>
                {data.overdue_services}
              </strong>
            </AdminLink>
            <AdminLink href={links.failedJobs} title="查看失败的任务">
              执行失败任务
              <strong style={{ color: data.failed_jobs > 0 ? 'var(--danger-text)' : 'inherit' }}>
                {data.failed_jobs}
              </strong>
            </AdminLink>
            <AdminLink href={links.pendingJobs} title="查看排队中的任务">
              排队处理任务
              <strong>{data.pending_jobs}</strong>
            </AdminLink>
            <AdminLink href={links.offlineNodes} title="查看离线节点">
              离线集群节点
              <strong style={{ color: offline > 0 ? 'var(--warning-text)' : 'inherit' }}>
                {offline}
              </strong>
            </AdminLink>
          </div>
        </section>
      </div>
    </section>
  )
}
