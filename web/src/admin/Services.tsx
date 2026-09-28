import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api, ProvisioningJobRecord, ServiceRecord } from '../api'
import { JobError, StatusBadge } from '../shared/ui'

export function ServicesView() {
  const [services, setServices] = useState<ServiceRecord[]>([])
  const [jobs, setJobs] = useState<ProvisioningJobRecord[]>([])
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState('')

  const load = async () => {
    try {
      const [serviceRows, jobRows] = await Promise.all([
        api<ServiceRecord[]>('/api/v1/admin/services'),
        api<ProvisioningJobRecord[]>('/api/v1/admin/jobs'),
      ])
      setServices(serviceRows)
      setJobs(jobRows)
      setError('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '加载失败')
    }
  }

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 10000)
    return () => window.clearInterval(timer)
  }, [])

  async function serviceAction(service: ServiceRecord, action: 'start' | 'stop' | 'restart' | 'terminate') {
    const prompts = {
      start: `确认开机实例【${service.instance_name}】？`,
      stop: `确认关机实例【${service.instance_name}】？`,
      restart: `确认重启实例【${service.instance_name}】？`,
      terminate: `确认立即终止【${service.customer_name}】的实例【${service.instance_name}】？实例与数据将被删除，未付账单作废，此操作不可撤销。`,
    }
    if (!window.confirm(prompts[action])) return
    setRetrying(service.id)
    setError('')
    try {
      await api(`/api/v1/admin/services/${service.id}/actions/${action}`, { method: 'POST' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
    } finally {
      setRetrying('')
    }
  }

  async function retry(job: ProvisioningJobRecord) {
    if (!window.confirm(`确认重新提交实例【${job.instance_name}】的自动化开通任务？`)) return
    setRetrying(job.id)
    setError('')
    try {
      await api(`/api/v1/admin/jobs/${job.id}/retry`, { method: 'POST' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '重试任务失败')
    } finally {
      setRetrying('')
    }
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">AUTOMATION & INSTANCES</p>
          <h2>VPS 服务与自动化执行队列</h2>
          <p>自动化开通、电源调度、失败重试与节点实际运行状态每 10 秒自动刷新同步。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="panel">
        <div className="panel-heading">
          <h3>服务实例列表</h3>
          <span className="tag">{services.length} SERVICES</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>实例名称</th>
                <th>客户 / 套餐</th>
                <th>节点 / 地域</th>
                <th>业务状态</th>
                <th>运行状态</th>
                <th>分配 IP</th>
                <th>下次到期</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {services.map(service => (
                <tr key={service.id}>
                  <td>
                    <strong>{service.instance_name}</strong>
                    <small>{service.id}</small>
                  </td>
                  <td>
                    {service.customer_name}
                    <small>{service.plan_name}</small>
                  </td>
                  <td>
                    {service.node_name || (service.status === 'terminated' ? '—' : '等待调度')}
                    <small>{service.region_name}</small>
                  </td>
                  <td><StatusBadge status={service.status} /></td>
                  <td>
                    <StatusBadge status={service.runtime_status} />
                    {service.last_reconcile_error && (
                      <small title={service.last_reconcile_error} style={{ color: 'var(--danger-text)' }}>
                        对账异常：{service.last_reconcile_error}
                      </small>
                    )}
                  </td>
                  <td>
                    <code>{service.primary_ipv4 || '—'}</code>
                    <small>{service.primary_ipv6}</small>
                  </td>
                  <td>{service.next_due_at && service.status !== 'terminated' ? new Date(service.next_due_at).toLocaleDateString() : '—'}</td>
                  <td>
                    <div className="row-actions">
                      {(service.status === 'active' || service.status === 'overdue') && (
                        service.runtime_status === 'stopped' ? (
                          <button className="text-button" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'start')}>开机</button>
                        ) : (
                          <>
                            <button className="text-button" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'stop')}>关机</button>
                            <button className="text-button" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'restart')}>重启</button>
                          </>
                        )
                      )}
                      {service.status !== 'terminating' && service.status !== 'terminated' && (
                        <button className="text-button danger" disabled={retrying === service.id} onClick={() => void serviceAction(service, 'terminate')}>终止</button>
                      )}
                    </div>
                  </td>
                </tr>
              ))}
              {!services.length && (
                <tr>
                  <td colSpan={8} className="empty-state">暂无已开通服务，账单支付后会自动进入队列开通</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel">
        <div className="panel-heading">
          <h3>自动化任务队列</h3>
          <span className="tag">WORKER POOL</span>
        </div>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>目标实例</th>
                <th>调度动作</th>
                <th>执行状态</th>
                <th>尝试次数</th>
                <th>下次执行时间</th>
                <th>错误诊断</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {jobs.map(job => (
                <tr key={job.id}>
                  <td>
                    <strong>{job.instance_name}</strong>
                    <small>{job.customer_name}</small>
                  </td>
                  <td><span className="tag">{job.action}</span></td>
                  <td><StatusBadge status={job.status} /></td>
                  <td><code>{job.attempts} / 8</code></td>
                  <td>{new Date(job.available_at).toLocaleString()}</td>
                  <td className="error-cell">
                    <JobError value={job.last_error} />
                  </td>
                  <td>
                    {['failed', 'dead'].includes(job.status) && (
                      <button
                        className="text-button"
                        disabled={retrying === job.id}
                        onClick={() => retry(job)}
                      >
                        <RefreshCw size={13} />
                        {retrying === job.id ? '提交中…' : '人工重试'}
                      </button>
                    )}
                  </td>
                </tr>
              ))}
              {!jobs.length && (
                <tr>
                  <td colSpan={7} className="empty-state">当前任务队列无正在积压或异常的任务</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
