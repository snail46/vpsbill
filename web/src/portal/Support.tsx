import { FormEvent, useEffect, useState } from 'react'
import { Plus } from 'lucide-react'
import { api, CustomerServiceRecord, TicketDetailRecord, TicketRecord } from '../api'
import { AttachmentPicker, ticketRequestBody, useAttachmentLimit } from '../TicketAttachments'
import { ticketStatusLabel, TicketConversation } from '../shared/ui'
import { formatTime } from '../shared/time'

export function CustomerSupport() {
  const [tickets, setTickets] = useState<TicketRecord[]>([])
  const [services, setServices] = useState<CustomerServiceRecord[]>([])
  const [detail, setDetail] = useState<TicketDetailRecord | null>(null)
  const [creating, setCreating] = useState(false)
  const [error, setError] = useState('')
  const [createFiles, setCreateFiles] = useState<File[]>([])
  const maxMB = useAttachmentLimit()

  const load = async () => {
    try {
      const [ticketRows, serviceRows] = await Promise.all([
        api<TicketRecord[]>('/api/v1/customer/tickets'),
        api<CustomerServiceRecord[]>('/api/v1/customer/services'),
      ])
      setTickets(ticketRows)
      setServices(serviceRows)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
  }, [])

  async function open(id: string) {
    try {
      setDetail(await api<TicketDetailRecord>(`/api/v1/customer/tickets/${id}`))
      setCreating(false)
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  async function create(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    try {
      const result = await api<TicketDetailRecord>('/api/v1/customer/tickets', {
        method: 'POST',
        body: ticketRequestBody(
          {
            service_id: data.get('service_id'),
            subject: data.get('subject'),
            priority: data.get('priority'),
            body: data.get('body'),
          },
          createFiles
        ),
      })
      setCreating(false)
      setCreateFiles([])
      await load()
      // Re-read so the view has the joined customer and instance names.
      await open(result.ticket.id)
    } catch (err) {
      setError(err instanceof Error ? err.message : '创建失败')
    }
  }

  async function reply(body: string, _internal: boolean, files: File[]) {
    if (!detail) return false
    try {
      await api(`/api/v1/customer/tickets/${detail.ticket.id}/messages`, {
        method: 'POST',
        body: ticketRequestBody({ body }, files),
      })
      setError('')
      await open(detail.ticket.id)
      await load()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : '回复失败')
      return false
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">SUPPORT</p>
          <h2>支持工单</h2>
          <p>工单可与具体 VPS 实例关联，沟通历史与操作均由服务端留痕审计。</p>
        </div>
        <button
          className="primary-button compact"
          onClick={() => {
            setCreating(true)
            setDetail(null)
          }}
        >
          <Plus size={16} />新建工单
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      {creating && (
        <form className="panel form-grid" onSubmit={create}>
          <label>
            <span>工单主题</span>
            <input name="subject" minLength={3} maxLength={160} placeholder="请简述您遇到的问题" required />
          </label>
          <label>
            <span>优先级</span>
            <select name="priority" defaultValue="normal">
              <option value="low">低</option>
              <option value="normal">普通</option>
              <option value="high">高</option>
              <option value="urgent">紧急</option>
            </select>
          </label>
          <label>
            <span>关联 VPS 实例（可选）</span>
            <select name="service_id">
              <option value="">不关联具体实例</option>
              {services.map(service => (
                <option key={service.id} value={service.id}>
                  {service.instance_name}
                </option>
              ))}
            </select>
          </label>
          <label className="wide">
            <span>问题详述</span>
            <textarea name="body" rows={6} maxLength={10000} placeholder="请详细提供现象、报错信息或重现步骤…" required={!createFiles.length} />
          </label>
          <div className="wide">
            <AttachmentPicker files={createFiles} onChange={setCreateFiles} maxMB={maxMB} onError={setError} />
          </div>
          <div className="form-actions wide">
            <button
              type="button"
              className="secondary-button"
              onClick={() => {
                setCreating(false)
                setCreateFiles([])
              }}
            >
              取消
            </button>
            <button className="primary-button compact">提交工单</button>
          </div>
        </form>
      )}

      <div className="support-layout">
        <div className="ticket-list">
          {tickets.map(ticket => (
            <button
              key={ticket.id}
              className={detail?.ticket.id === ticket.id ? 'ticket-row selected' : 'ticket-row'}
              onClick={() => void open(ticket.id)}
            >
              <div>
                <strong>{ticket.subject}</strong>
                <span>{ticket.number}</span>
              </div>
              <span className={`ticket-state ${ticket.status}`}>{ticketStatusLabel(ticket.status)}</span>
              <small>
                {ticket.message_count} 条消息 · 最近更新 {formatTime(ticket.last_reply_at)}
              </small>
            </button>
          ))}
          {!tickets.length && <div className="empty-card">暂无支持工单记录</div>}
        </div>

        {detail ? (
          <TicketConversation detail={detail} onReply={reply} maxMB={maxMB} onError={setError} />
        ) : (
          <div className="panel support-placeholder">选择左侧工单查看完整沟通历史与回复</div>
        )}
      </div>
    </section>
  )
}
