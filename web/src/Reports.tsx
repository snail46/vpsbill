import { FormEvent, useEffect, useState } from 'react'
import { Flag, RefreshCw, X } from 'lucide-react'
import { api, cached, type ReportRecord } from './api'
import { formatTime } from './shared/time'
import { promptDialog } from './shared/dialog'
import { t } from './shared/i18n'

export const reportReasons: Record<string, string> = {
  resources: t('实际资源与宣传不符'),
  oversell: t('性能严重不足（超出公开的超售倍数）'),
  false_info: t('位置、线路等信息不实'),
  abuse: t('辱骂或骚扰'),
  spam: t('广告或刷屏'),
  other: t('其他'),
}

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
      setError(err instanceof Error ? err.message : t('提交失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="modal-backdrop" role="dialog" aria-modal="true">
      <form className="modal panel" onSubmit={submit}>
        <div className="panel-heading">
          <h3>
            <Flag size={16} /> {t('举报{0}', messageID ? t('聊天消息') : t('母机 {0}', nodeName))}
          </h3>
          <button type="button" className="icon-button" aria-label={t('关闭')} onClick={onClose}>
            <X size={16} />
          </button>
        </div>
        {error && <div className="form-error">{error}</div>}
        {done ? (
          <>
            <div className="form-success">{done}</div>
            <div className="form-actions">
              <button type="button" className="primary-button compact" onClick={onClose}>{t('关闭')}</button>
            </div>
          </>
        ) : (
          <div className="form-grid">
            <label className="wide">
              <span>{t('原因')}</span>
              <select value={reason} onChange={event => setReason(event.target.value)}>
                {reasons.map(item => (
                  <option key={item} value={item}>{reportReasons[item]}</option>
                ))}
              </select>
            </label>
            <label className="wide">
              <span>{t('说明（可选）')}</span>
              <textarea name="detail" rows={4} maxLength={1000} placeholder={messageID ? t('补充说明') : t('例如：实测只有 1 核 512MB，与套餐不符')} />
              {!messageID && <small>{t('请在实例里运行')} <code>free -m; df -h /</code>{t('，把结果贴在这里，便于管理员核实。Podman 实例的 CPU 是按配额限制的，')}<code>nproc</code> {t('会显示母机核数，属正常现象。')}</small>}
            </label>
            <p className="muted-text wide">{t('举报只有平台管理员能看到，机主和其他用户看不到举报人。')}</p>
            <div className="form-actions wide">
              <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
              <button className="primary-button compact" disabled={busy}>{busy ? t('提交中…') : t('提交举报')}</button>
            </div>
          </div>
        )}
      </form>
    </div>
  )
}

const statusNames: Record<ReportRecord['status'], string> = { open: t('待处理'), resolved: t('已处理'), dismissed: t('已驳回') }

// AdminReports lists customer reports; staff resolve them, and can mute a
// reported chat author or pause a reported node from here.
export function AdminReports() {
  const [reports, setReports] = useState<ReportRecord[]>(() => cached<{ reports: ReportRecord[] }>('/api/v1/admin/reports')?.reports ?? [])
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const load = () =>
    api<{ reports: ReportRecord[] }>('/api/v1/admin/reports')
      .then(value => setReports(value.reports))
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
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
      setError(err instanceof Error ? err.message : t('操作失败'))
    }
  }

  async function resolve(report: ReportRecord, status: 'resolved' | 'dismissed') {
    const resolution = await promptDialog({
      title: status === 'resolved' ? t('标记为已处理') : t('驳回举报'),
      label: status === 'resolved' ? t('处理结果（记录备查，可留空）') : t('驳回原因（记录备查，可留空）'),
      multiline: true,
      confirmText: status === 'resolved' ? t('标记已处理') : t('驳回'),
    })
    if (resolution === null) return
    void act(() => api(`/api/v1/admin/reports/${report.id}/resolve`, { method: 'POST', body: JSON.stringify({ status, resolution }) }), statusNames[status])
  }

  async function mute(report: ReportRecord) {
    if (!report.message_author_account_id) return
    const answer = await promptDialog({
      title: t('禁言 {0}', report.message_author),
      label: t('禁言小时数（1-8760）'),
      inputType: 'number',
      defaultValue: '24',
      confirmText: t('禁言'),
      danger: true,
      validate: value => (/^\d+$/.test(value.trim()) && Number(value) >= 1 && Number(value) <= 8760 ? '' : t('请输入 1 到 8760 之间的整数小时')),
    })
    if (answer === null) return
    const hours = Number(answer)
    void act(
      () =>
        api(`/api/v1/admin/chat/rooms/${report.node_id}/mutes`, {
          method: 'POST',
          body: JSON.stringify({ account_id: report.message_author_account_id, hours, reason: reportReasons[report.reason] }),
        }),
      t('已禁言 {0} {1} 小时', report.message_author, hours),
    )
  }

  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h3>{t('用户举报')}</h3>
          <p className="muted-text">{t('针对托管母机（资源不符、超售、信息不实）和聊天消息的举报。核实超售后可以在「托管母机」里为母机核定资源上限或暂停销售。')}</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />{t('刷新')}
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="form-success">{notice}</div>}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('对象')}</th>
              <th>{t('原因')}</th>
              <th>{t('举报人')}</th>
              <th>{t('状态')}</th>
              <th>{t('时间')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {reports.map(report => (
              <tr key={report.id}>
                <td>
                  <strong>{report.node_name}</strong>
                  <small className="block">{t('机主 {0}', report.host_name)}</small>
                  {report.target_type === 'chat_message' && (
                    <small className="block">
                      {t('消息 · {0}：{1}', report.message_author, report.message_body?.slice(0, 80))}
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
                        <button className="secondary-button compact" onClick={() => mute(report)}>{t('禁言作者')}</button>
                      )}
                      {report.target_type === 'node' && (
                        <button
                          className="secondary-button compact"
                          onClick={() =>
                            void act(
                              () => api(`/api/v1/admin/marketplace/nodes/${report.node_id}/listing`, { method: 'POST', body: JSON.stringify({ listed: false }) }),
                              t('已暂停该母机销售'),
                            )
                          }
                        >
                          {t('暂停销售')}
                        </button>
                      )}
                      <button className="secondary-button compact" onClick={() => resolve(report, 'resolved')}>{t('已处理')}</button>
                      <button className="secondary-button compact" onClick={() => resolve(report, 'dismissed')}>{t('驳回')}</button>
                    </>
                  )}
                </td>
              </tr>
            ))}
            {!reports.length && (
              <tr>
                <td colSpan={6} className="empty-state">{t('暂无举报')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
