import { useEffect, useState } from 'react'
import { RefreshCw } from 'lucide-react'
import { api, cached, ProvisioningJobRecord, ServiceRecord } from '../api'
import { JobError, StatusBadge, statusLabel } from '../shared/ui'
import { formatDate, formatTime } from '../shared/time'
import {
  FilterBar, NumberFilter, SearchFilter, SelectFilter, includesText, matchesAny, uniqueOptions, useCustomerOptions, useSection, useUrlFilters, type Option,
} from './filters'

const serviceStatuses: Option[] = ['provisioning', 'active', 'overdue', 'suspended', 'terminating', 'terminated', 'error']
  .map(status => [status, statusLabel(status)] as Option)
  .concat([['overdue,suspended', '逾期或已暂停']])
const runtimeStatuses: Option[] = ['running', 'stopped', 'creating', 'error', 'missing', 'unknown'].map(status => [status, statusLabel(status)])
const jobStatuses: Option[] = [
  ['pending', statusLabel('pending')],
  ['running', '执行中'],
  ['succeeded', statusLabel('succeeded')],
  ['failed', statusLabel('failed')],
  ['dead', statusLabel('dead')],
  ['pending,running', '排队或执行中'],
  ['failed,dead', '失败（含需人工处理）'],
]

const serviceFilterKeys = ['q', 'account', 'plan', 'node', 'region', 'status', 'runtime', 'ip', 'due', 'job'] as const

// daysLeft is how many days remain until a service is due; negative once
// it is past due.
function daysLeft(service: ServiceRecord) {
  if (!service.next_due_at) return null
  return Math.floor((new Date(service.next_due_at).getTime() - Date.now()) / 86400000)
}

export function ServicesView() {
  const { filters, set, reset, active } = useUrlFilters(serviceFilterKeys)
  const account = filters.account
  const servicesPath = `/api/v1/admin/services${account ? `?account_id=${encodeURIComponent(account)}` : ''}`
  const jobsPath = `/api/v1/admin/jobs${account ? `?account_id=${encodeURIComponent(account)}` : ''}`
  const [services, setServices] = useState<ServiceRecord[]>(() => cached<ServiceRecord[]>(servicesPath) ?? [])
  const [jobs, setJobs] = useState<ProvisioningJobRecord[]>(() => cached<ProvisioningJobRecord[]>(jobsPath) ?? [])
  const customers = useCustomerOptions()
  const [error, setError] = useState('')
  const [retrying, setRetrying] = useState('')
  useSection()

  const load = async () => {
    try {
      const [serviceRows, jobRows] = await Promise.all([api<ServiceRecord[]>(servicesPath), api<ProvisioningJobRecord[]>(jobsPath)])
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
  }, [servicesPath])

  const due = filters.due === '' ? null : Number(filters.due)
  const shown = services.filter(service => {
    const left = daysLeft(service)
    return (
      includesText(filters.q, service.instance_name, service.id, service.external_id) &&
      matchesAny(filters.plan, service.plan_name) &&
      matchesAny(filters.node, service.node_name ?? '') &&
      matchesAny(filters.region, service.region_name) &&
      matchesAny(filters.status, service.status) &&
      matchesAny(filters.runtime, service.runtime_status) &&
      includesText(filters.ip, service.primary_ipv4, service.primary_ipv6) &&
      (due === null || (left !== null && left <= due && service.status !== 'terminated'))
    )
  })
  const shownJobs = jobs.filter(job => matchesAny(filters.job, job.status))

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
        <FilterBar shown={shown.length} total={services.length} active={active - (filters.job ? 1 : 0)} onReset={reset}>
          <SearchFilter label="实例名称" value={filters.q} onChange={value => set('q', value)} placeholder="名称或 ID" />
          <SelectFilter label="客户" value={filters.account} onChange={value => set('account', value)} options={customers} all="全部客户" />
          <SelectFilter label="套餐" value={filters.plan} onChange={value => set('plan', value)} options={uniqueOptions(services.map(item => item.plan_name))} />
          <SelectFilter label="节点" value={filters.node} onChange={value => set('node', value)} options={uniqueOptions(services.map(item => item.node_name))} />
          <SelectFilter label="地域" value={filters.region} onChange={value => set('region', value)} options={uniqueOptions(services.map(item => item.region_name))} />
          <SelectFilter label="业务状态" value={filters.status} onChange={value => set('status', value)} options={serviceStatuses} />
          <SelectFilter label="运行状态" value={filters.runtime} onChange={value => set('runtime', value)} options={runtimeStatuses} />
          <SearchFilter label="分配 IP" value={filters.ip} onChange={value => set('ip', value)} placeholder="IPv4 或 IPv6" />
          <NumberFilter label="到期剩余 ≤" value={filters.due} onChange={value => set('due', value)} placeholder="天数" suffix="天" />
        </FilterBar>
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
              {shown.map(service => {
                const left = daysLeft(service)
                return (
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
                    <td>
                      {service.next_due_at && service.status !== 'terminated' ? formatDate(service.next_due_at) : '—'}
                      {left !== null && service.status !== 'terminated' && (
                        <small className={left < 0 ? 'danger-text' : left <= 7 ? 'warn-text' : ''}>{left < 0 ? `已过期 ${-left} 天` : `剩 ${left} 天`}</small>
                      )}
                    </td>
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
                )
              })}
              {!shown.length && (
                <tr>
                  <td colSpan={8} className="empty-state">{services.length ? '没有符合筛选条件的实例' : '暂无已开通服务，账单支付后会自动进入队列开通'}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>

      <div className="panel" id="jobs">
        <div className="panel-heading">
          <h3>自动化任务队列</h3>
          <span className="tag">WORKER POOL</span>
        </div>
        <FilterBar shown={shownJobs.length} total={jobs.length} active={filters.job ? 1 : 0} onReset={() => set('job', '')}>
          <SelectFilter label="执行状态" value={filters.job} onChange={value => set('job', value)} options={jobStatuses} />
        </FilterBar>
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
              {shownJobs.map(job => (
                <tr key={job.id}>
                  <td>
                    <strong>{job.instance_name}</strong>
                    <small>{job.customer_name}</small>
                  </td>
                  <td><span className="tag">{job.action}</span></td>
                  <td><StatusBadge status={job.status} /></td>
                  <td><code>{job.attempts} / 8</code></td>
                  <td>{formatTime(job.available_at)}</td>
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
              {!shownJobs.length && (
                <tr>
                  <td colSpan={7} className="empty-state">{jobs.length ? '没有该状态的任务' : '当前任务队列无正在积压或异常的任务'}</td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </div>
    </section>
  )
}
