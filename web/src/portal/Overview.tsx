import { useEffect, useState, type ReactNode } from 'react'
import { Activity, Boxes, CalendarClock, CircleDollarSign, Coins, Megaphone, Pin, ReceiptText, Store } from 'lucide-react'
import { api, cached, AnnouncementRecord, CustomerIdentity, CustomerInvoiceRecord, CustomerOverviewRecord, CustomerServiceRecord } from '../api'
import { money } from '../shared/ui'
import { navigatePortal } from '../shared/nav'
import { formatDate, formatTime } from '../shared/time'
import { t } from '../shared/i18n'

const soon = 7 * 24 * 3600 * 1000

// MetricCard is an overview figure that opens the page behind it.
function MetricCard({ to, icon, label, value, note, action }: { to: string; icon: ReactNode; label: string; value: ReactNode; note?: ReactNode; action?: ReactNode }) {
  return (
    <article
      className="metric-link"
      role="link"
      tabIndex={0}
      onClick={() => navigatePortal(to)}
      onKeyDown={event => {
        if (event.key === 'Enter') navigatePortal(to)
      }}
    >
      {icon}
      <span>{label}</span>
      <strong>{value}</strong>
      {note && <small>{note}</small>}
      {action}
    </article>
  )
}

export function CustomerOverview({ customer }: { customer: CustomerIdentity }) {
  const [services, setServices] = useState<CustomerServiceRecord[]>(() => (cached<CustomerServiceRecord[]>('/api/v1/customer/services') ?? []).filter(item => item.status !== 'terminated'))
  const [invoices, setInvoices] = useState<CustomerInvoiceRecord[]>(() => cached<CustomerInvoiceRecord[]>('/api/v1/customer/invoices') ?? [])
  const [overview, setOverview] = useState<CustomerOverviewRecord | null>(() => cached<CustomerOverviewRecord>('/api/v1/customer/overview') ?? null)

  useEffect(() => {
    void Promise.all([
      api<CustomerServiceRecord[]>('/api/v1/customer/services'),
      api<CustomerInvoiceRecord[]>('/api/v1/customer/invoices'),
      api<CustomerOverviewRecord>('/api/v1/customer/overview'),
    ]).then(([s, i, o]) => {
      // Terminated services stay listed on the services page but are not counted here.
      setServices(s.filter(item => item.status !== 'terminated'))
      setInvoices(i)
      setOverview(o)
    })
  }, [])

  const online = services.filter(item => item.runtime_status === 'running').length
  const open = invoices.filter(item => item.status === 'open')
  const due = open.reduce((sum, item) => sum + item.balance_minor, 0)
  const expiring = services.filter(item => item.next_due_at && new Date(item.next_due_at).getTime() - Date.now() < soon)
  const currency = overview?.currency || invoices[0]?.currency || 'CNY'
  const hosting = overview?.hosting

  return (
    <section className="workspace-panel">
      <section className="hero-card customer-hero metric-link" role="link" tabIndex={0} onClick={() => navigatePortal('/portal/services')}>
        <div>
          <p className="eyebrow">WELCOME BACK</p>
          <h2>{t('{0}，欢迎回到云服务中心。', customer.display_name)}</h2>
          <p>{t('集中查看所有实例运行状态、快速执行电源和管理操作，并实时跟踪账单结算。')}</p>
        </div>
        <div className="hero-signal">
          <span>{online}/{services.length}</span>
          <small>{t('在线实例')}</small>
        </div>
      </section>

      <section className="metrics overview-metrics">
        <MetricCard to="/portal/services" icon={<Boxes size={20} />} label={t('VPS 总数')} value={services.length} note={t('{0} 台运行中', online)} />
        <MetricCard
          to="/portal/services"
          icon={<CalendarClock size={20} />}
          label={t('7 天内到期')}
          value={expiring.length}
          note={expiring.length ? t('{0} 台已开自动续费', expiring.filter(item => item.auto_renew).length) : t('暂无')}
        />
        <MetricCard to="/portal/billing" icon={<ReceiptText size={20} />} label={t('待支付账单')} value={open.length} note={open.length ? t('共 {0}', money(due, currency)) : t('全部已结清')} />
        <MetricCard
          to="/portal/wallet"
          icon={<Coins size={20} />}
          label={t('可用余额')}
          value={overview ? money(overview.balance_minor, currency) : '—'}
          note={t('可用于购买和自动续费')}
          action={
            <button
              className="primary-button compact"
              onClick={event => {
                event.stopPropagation()
                navigatePortal('/portal/wallet#topup')
              }}
            >
              {t('充值')}
            </button>
          }
        />
        <MetricCard
          to="/portal/hosting"
          icon={<Store size={20} />}
          label={t('托管收益')}
          value={hosting ? money(hosting.released_minor, currency) : '—'}
          note={hosting?.nodes ? t('待结算 {0} · {1} 台母机', money(hosting.pending_minor, currency), hosting.nodes) : t('发布母机后开始计算')}
        />
        <MetricCard to="/portal/billing" icon={<CircleDollarSign size={20} />} label={t('待付总金额')} value={money(due, currency)} note={t('含续费和新购账单')} />
        <MetricCard to="/portal/services" icon={<Activity size={20} />} label={t('正在运行')} value={online} note={t('{0} 台未运行', services.length - online)} />
      </section>

      <section className="panel announcements-panel">
        <div className="panel-heading">
          <h3><Megaphone size={16} /> {t('平台公告')}</h3>
          <button className="text-button" onClick={() => navigatePortal('/portal/announcements')}>{t('查看全部')}</button>
        </div>
        {overview?.announcements.map(item => (
          <button key={item.id} className="announcement-row" onClick={() => navigatePortal(`/portal/announcements#${item.id}`)}>
            {item.pinned && <Pin size={13} />}
            <strong>{item.title}</strong>
            <small>{formatDate(item.created_at)}</small>
          </button>
        ))}
        {overview && !overview.announcements.length && <div className="empty-state">{t('暂无公告')}</div>}
      </section>
    </section>
  )
}

// CustomerAnnouncements lists every published announcement.
export function CustomerAnnouncements() {
  const [items, setItems] = useState<AnnouncementRecord[] | null>(() => cached<AnnouncementRecord[]>('/api/v1/customer/announcements') ?? null)
  const [error, setError] = useState('')
  useEffect(() => {
    api<AnnouncementRecord[]>('/api/v1/customer/announcements')
      .then(value => {
        setItems(value)
        const anchor = window.location.hash.slice(1)
        if (anchor) window.setTimeout(() => document.getElementById(`announcement-${anchor}`)?.scrollIntoView({ block: 'start' }), 0)
      })
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  }, [])
  return (
    <section className="workspace-panel">
      {error && <div className="form-error">{error}</div>}
      {items?.map(item => (
        <article key={item.id} id={`announcement-${item.id}`} className="panel announcement">
          <div className="panel-heading">
            <h3>{item.pinned && <Pin size={14} />} {item.title}</h3>
            <small className="muted-text">{formatTime(item.created_at)}</small>
          </div>
          {item.body && <p className="announcement-body">{item.body}</p>}
        </article>
      ))}
      {items && !items.length && <div className="empty-card">{t('暂无公告')}</div>}
    </section>
  )
}
