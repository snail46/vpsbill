import { useEffect, useState } from 'react'
import { Activity, Boxes, CircleDollarSign, ReceiptText } from 'lucide-react'
import { api, CustomerIdentity, CustomerInvoiceRecord, CustomerServiceRecord } from '../api'
import { money } from '../shared/ui'

export function CustomerOverview({ customer }: { customer: CustomerIdentity }) {
  const [services, setServices] = useState<CustomerServiceRecord[]>([])
  const [invoices, setInvoices] = useState<CustomerInvoiceRecord[]>([])

  useEffect(() => {
    void Promise.all([
      api<CustomerServiceRecord[]>('/api/v1/customer/services'),
      api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices'),
    ]).then(([s, i]) => {
      // Terminated services stay listed on the services page but are not counted here.
      setServices(s.filter(item => item.status !== 'terminated'))
      setInvoices(i)
    })
  }, [])

  const online = services.filter(item => item.runtime_status === 'running').length
  const due = invoices.filter(item => item.status === 'open').reduce((sum, item) => sum + item.balance_minor, 0)

  return (
    <section className="workspace-panel">
      <section className="hero-card customer-hero">
        <div>
          <p className="eyebrow">WELCOME BACK</p>
          <h2>{customer.display_name}，欢迎回到云服务中心。</h2>
          <p>集中查看所有实例运行状态、快速执行电源和管理操作，并实时跟踪账单结算。</p>
        </div>
        <div className="hero-signal">
          <span>{online}/{services.length}</span>
          <small>在线实例</small>
        </div>
      </section>

      <section className="metrics">
        <article>
          <Boxes size={20} />
          <span>VPS 总数</span>
          <strong>{services.length}</strong>
        </article>
        <article>
          <Activity size={20} />
          <span>正在运行</span>
          <strong>{online}</strong>
        </article>
        <article>
          <ReceiptText size={20} />
          <span>待支付账单</span>
          <strong>{invoices.filter(item => item.status === 'open').length}</strong>
        </article>
        <article>
          <CircleDollarSign size={20} />
          <span>待付总金额</span>
          <strong>{money(due, invoices[0]?.currency || 'CNY')}</strong>
        </article>
      </section>
    </section>
  )
}
