import { useEffect, useState } from 'react'
import { Activity, CircleDollarSign, Headphones, ReceiptText } from 'lucide-react'
import { api, cached, OperationsOverviewRecord } from '../api'
import { money, CapacityBar } from '../shared/ui'

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
              <strong style={{ color: data.overdue_services > 0 ? 'var(--danger-text)' : 'inherit' }}>
                {data.overdue_services}
              </strong>
            </span>
            <span>
              执行失败任务
              <strong style={{ color: data.failed_jobs > 0 ? 'var(--danger-text)' : 'inherit' }}>
                {data.failed_jobs}
              </strong>
            </span>
            <span>
              排队处理任务
              <strong>{data.pending_jobs}</strong>
            </span>
            <span>
              离线集群节点
              <strong style={{ color: data.nodes - data.online_nodes > 0 ? 'var(--warning-text)' : 'inherit' }}>
                {data.nodes - data.online_nodes}
              </strong>
            </span>
          </div>
        </section>
      </div>
    </section>
  )
}
