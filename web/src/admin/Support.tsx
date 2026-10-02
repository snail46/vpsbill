import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api, cached, AuditLogRecord, TicketDetailRecord, TicketRecord } from '../api'
import { ticketRequestBody, useAttachmentLimit } from '../TicketAttachments'
import { ticketStatusLabel, TicketConversation } from '../shared/ui'
import { formatTime } from '../shared/time'
import { useUrlFilters } from './filters'
import { toast } from '../shared/toast'
import { t } from '../shared/i18n'

export function AdminSupport() {
  const [tickets, setTickets] = useState<TicketRecord[]>(() => cached<TicketRecord[]>('/api/v1/admin/tickets') ?? [])
  const [scope, setScope] = useState<'platform' | 'hosted'>('platform')
  // open=1 (from the overview) shows only tickets still waiting.
  const { filters, set } = useUrlFilters(['open'] as const)
  const listed = tickets.filter(ticket => !filters.open || !['resolved', 'closed'].includes(ticket.status))
  const [detail, setDetail] = useState<TicketDetailRecord | null>(null)
  const [error, setError] = useState('')
  const maxMB = useAttachmentLimit()

  const load = () =>
    api<TicketRecord[]>('/api/v1/admin/tickets')
      .then(setTickets)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function open(id: string) {
    try {
      setDetail(await api<TicketDetailRecord>(`/api/v1/admin/tickets/${id}`))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('加载失败'))
    }
  }

  async function reply(body: string, internal: boolean, files: File[]) {
    if (!detail) return false
    try {
      await api(`/api/v1/admin/tickets/${detail.ticket.id}/messages`, {
        method: 'POST',
        body: ticketRequestBody({ body, internal }, files),
      })
      setError('')
      await open(detail.ticket.id)
      load()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : t('回复失败'))
      return false
    }
  }

  async function status(value: string) {
    if (!detail) return
    try {
      await api(`/api/v1/admin/tickets/${detail.ticket.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ status: value }),
      })
      await open(detail.ticket.id)
      load()
      toast('success', t('工单状态已更新'))
    } catch (err) {
      toast('error', t('更新工单状态失败'), err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : t('更新失败'))
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">SUPPORT DESK</p>
          <h2>{t('工单管理')}</h2>
          <p>{t('集中处理客户咨询、故障报障，支持添加团队内部备忘与工单流转。')}</p>
        </div>
        <button className="secondary-button" onClick={() => load()}>
          <RefreshCw size={15} />{t('刷新')}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="segmented" role="tablist">
        <button role="tab" aria-selected={scope === 'platform'} className={scope === 'platform' ? 'active' : ''} onClick={() => setScope('platform')}>
          {t('平台工单')} <span className="count">{listed.filter(ticket => !ticket.host_account_id).length}</span>
        </button>
        <button role="tab" aria-selected={scope === 'hosted'} className={scope === 'hosted' ? 'active' : ''} onClick={() => setScope('hosted')}>
          {t('托管工单')} <span className="count">{listed.filter(ticket => ticket.host_account_id).length}</span>
        </button>
        <label className="checkbox segmented-option">
          <input type="checkbox" checked={Boolean(filters.open)} onChange={event => set('open', event.target.checked ? '1' : '')} /> {t('只看未解决')}
        </label>
      </div>
      {scope === 'hosted' && <p className="muted-text">{t('托管工单由母机机主作为第一处理人，平台可以查看并在必要时介入回复。')}</p>}

      <div className="support-layout">
        <div className="ticket-list">
          {listed.filter(ticket => (scope === 'hosted') === Boolean(ticket.host_account_id)).map(ticket => (
            <button
              key={ticket.id}
              className={detail?.ticket.id === ticket.id ? 'ticket-row selected' : 'ticket-row'}
              onClick={() => void open(ticket.id)}
            >
              <div>
                <strong>{ticket.subject}</strong>
                <span>
                  {ticket.customer_name} · {ticket.number}
                  {ticket.host_account_id ? t(' · 机主 {0}', ticket.host_name) : ''}
                </span>
              </div>
              <span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span>
              <small>
                {t('优先级：{0} · {1} 条消息', ticket.priority.toUpperCase(), ticket.message_count)}
              </small>
            </button>
          ))}
          {!tickets.length && <div className="empty-card">{t('当前无工单待处理')}</div>}
        </div>

        {detail ? (
          <TicketConversation detail={detail} onReply={reply} admin onStatus={status} maxMB={maxMB} onError={setError} />
        ) : (
          <div className="panel support-placeholder">{t('选择左侧工单开始回复与流转')}</div>
        )}
      </div>
    </section>
  )
}

export function AuditView() {
  const [rows, setRows] = useState<AuditLogRecord[]>(() => cached<AuditLogRecord[]>('/api/v1/admin/audit-logs') ?? [])
  const [error, setError] = useState('')

  const load = () =>
    api<AuditLogRecord[]>('/api/v1/admin/audit-logs')
      .then(setRows)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">AUDIT TRAIL</p>
          <h2>{t('安全审计日志')}</h2>
          <p>{t('核心与敏感操作采用服务端只追加（Append-Only）模型记录，展示最近 500 条操作。')}</p>
        </div>
        <button className="secondary-button" onClick={() => load()}>
          <RefreshCw size={15} />{t('刷新')}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('记录时间')}</th>
              <th>{t('操作者')}</th>
              <th>{t('执行动作')}</th>
              <th>{t('目标对象')}</th>
              <th>{t('来源 IP')}</th>
              <th>{t('元数据明细')}</th>
            </tr>
          </thead>
          <tbody>
            {rows.map(row => (
              <tr key={row.id}>
                <td>{formatTime(row.created_at)}</td>
                <td>
                  <strong>{row.actor_type}</strong>
                  <small>{row.actor_id || '—'}</small>
                </td>
                <td>
                  <span className="tag">{row.action}</span>
                </td>
                <td>
                  <strong>{row.target_type}</strong>
                  <small>{row.target_id || '—'}</small>
                </td>
                <td>
                  <code>{row.ip || '—'}</code>
                </td>
                <td className="audit-metadata" title={JSON.stringify(row.metadata)}>
                  {JSON.stringify(row.metadata)}
                </td>
              </tr>
            ))}
            {!rows.length && (
              <tr>
                <td colSpan={6} className="empty-state">{t('暂无审计日志记录')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  )
}
