import { FormEvent, useEffect, useState } from 'react'
import { Flag, RefreshCw, X } from 'lucide-react'
import { api, cached, reportReasons, type ReportRecord } from './api'
import { formatTime } from './shared/time'

const nodeReasons = ['resources', 'oversell', 'false_info', 'other']
const messageReasons = ['abuse', 'spam', 'other']

// ReportDialog files a report about a hosted node or a chat message.
export function ReportDialog({ nodeID, nodeName, messageID, onClose }: { nodeID: string; nodeName: string; messageID?: number; onClose: () => void }) {
  const reasons = messageID ? messageReasons : nodeReasons
  const [reason, setReason] = useState(reasons[0])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [done, setDone] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError('')
    try {
      const result = await api<{ message: string }>('/api/v1/customer/reports', {
        method: 'POST',
        body: JSON.stringify({ target_type: messageID ? 'chat_message' : 'node', node_id: nodeID, message_id: messageID || 0, reason, detail: form.get('detail') }),
      })
      setDone(result.message)
    } catch (err) {
      setError(err instanceof Error ? err.message : '提交失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <form className="modal panel" onSubmit={submit}>
        <div className="panel-heading">
          <h3>
            <Flag size={16} /> 举报{messageID ? '聊天消息' : `母机 ${nodeName}`}
          </h3>
          <button type="button" className="icon-button" aria-label="关闭" onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        {error && <div className="form-error">{error}</div>}
        {done ? (
          <>
            <div className="form-success">{done}</div>
            <div className="form-actions">
              <button type="button" className="primary-button compact" onClick={onClose}>关闭</button>
            </div>
          </>
        ) : (
          <div className="form-grid">
            <label className="wide">
              <span>原因</span>
              <select value={reason} onChange={event => setReason(event.target.value)}>
                {reasons.map(item => (
                  <option key={item} value={item}>{reportReasons[item]}</option>
                ))}
              </select>
            </label>
            <label className="wide">
              <span>说明（可选）</span>
              <textarea name="detail" rows={4} maxLength={1000} placeholder={messageID ? '补充说明' : '例如：实测只有 1 核 512MB，与套餐不符'} />
              {!messageID && <small>请在实例里运行 <code>free -m; df -h /</code>，把结果贴在这里，便于管理员核实。Podman 实例的 CPU 是按配额限制的，<code>nproc</code> 会显示母机核数，属正常现象。</small>}
            </label>
            <p className="muted-text wide">举报只有平台管理员能看到，机主和其他用户看不到举报人。</p>
            <div className="form-actions wide">
              <button type="button" className="secondary-button" onClick={onClose}>取消</button>
              <button className="primary-button compact" disabled={busy}>{busy ? '提交中…' : '提交举报'}</button>
            </div>
          </div>
        )}
      </form>
    </div>
  )
}

const statusNames: Record<ReportRecord['status'], string> = { open: '待处理', resolved: '已处理', dismissed: '已驳回' }

// AdminReports lists customer reports; staff resolve them, and can mute a
// reported chat author or pause a reported node from here.
export function AdminReports() {
  const [reports, setReports] = useState<ReportRecord[]>(() => cached<{ reports: ReportRecord[] }>('/api/v1/admin/reports')?.reports ?? [])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () =>
    api<{ reports: ReportRecord[] }>('/api/v1/admin/reports')
      .then(value => setReports(value.reports))
      .catch(err => setError(err instanceof Error ? err.message : '加载失败'))
  useEffect(() => {
    void load()
  }, [])

  async function act(run: () => Promise<unknown>, message: string) {
    setError('')
    try {
      await run()
      setNotice(message)
      void load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    }
  }

  function resolve(report: ReportRecord, status: 'resolved' | 'dismissed') {
    const resolution = window.prompt(status === 'resolved' ? '处理结果（记录备查）' : '驳回原因（记录备查）', '')
    if (resolution === null) return
    void act(() => api(`/api/v1/admin/reports/${report.id}/resolve`, { method: 'POST', body: JSON.stringify({ status, resolution }) }), statusNames[status])
  }

  function mute(report: ReportRecord) {
    if (!report.message_author_account_id) return
    const hours = Number(window.prompt(`禁言 ${report.message_author} 多少小时？`, '24'))
    if (!hours) return
    void act(
      () =>
        api(`/api/v1/admin/chat/rooms/${report.node_id}/mutes`, {
          method: 'POST',
          body: JSON.stringify({ account_id: report.message_author_account_id, hours, reason: reportReasons[report.reason] }),
        }),
      `已禁言 ${report.message_author} ${hours} 小时`,
    )
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h3>用户举报</h3>
          <p className="muted-text">针对托管母机（资源不符、超售、信息不实）和聊天消息的举报。核实超售后可以在「托管母机」里为母机核定资源上限或暂停销售。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>对象</th>
              <th>原因</th>
              <th>举报人</th>
              <th>状态</th>
              <th>时间</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {reports.map(report => (
              <tr key={report.id}>
                <td>
                  <strong>{report.node_name}</strong>
                  <small className="block">机主 {report.host_name}</small>
                  {report.target_type === 'chat_message' && (
                    <small className="block">
                      消息 · {report.message_author}：{report.message_body?.slice(0, 80)}
                    </small>
                  )}
                </td>
                <td>
                  {reportReasons[report.reason] || report.reason}
                  {report.detail && <small className="block">{report.detail}</small>}
                </td>
                <td>{report.reporter_name}</td>
                <td>
                  <span className={report.status === 'open' ? 'tag danger' : 'tag'}>{statusNames[report.status]}</span>
                  {report.resolution && <small className="block">{report.resolution}</small>}
                </td>
                <td>{formatTime(report.created_at)}</td>
                <td className="row-actions">
                  {report.status === 'open' && (
                    <>
                      {report.target_type === 'chat_message' && report.message_author_account_id && (
                        <button className="secondary-button compact" onClick={() => mute(report)}>禁言作者</button>
                      )}
                      {report.target_type === 'node' && (
                        <button
                          className="secondary-button compact"
                          onClick={() =>
                            void act(
                              () => api(`/api/v1/admin/marketplace/nodes/${report.node_id}/listing`, { method: 'POST', body: JSON.stringify({ listed: false }) }),
                              '已暂停该母机销售',
                            )
                          }
                        >
                          暂停销售
                        </button>
                      )}
                      <button className="secondary-button compact" onClick={() => resolve(report, 'resolved')}>已处理</button>
                      <button className="secondary-button compact" onClick={() => resolve(report, 'dismissed')}>驳回</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
            {!reports.length && (
              <tr>
                <td colSpan={6} className="empty-state">暂无举报</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
