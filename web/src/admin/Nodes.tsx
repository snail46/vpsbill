import { FormEvent, useEffect, useState } from 'react'
import { ChevronRight, RefreshCw, X } from 'lucide-react'
import { api, HostProbeRecord, NodeRecord, Overcommit, ProviderTypeRecord } from '../api'
import { OvercommitDialog, overcommitText } from '../Supply'
import { PageActions, StatusBadge, formatBytes, NodeExpiry, CapacityBar } from '../shared/ui'
import { formatTime } from '../shared/time'

export function NodesView() {
  const [nodes, setNodes] = useState<NodeRecord[]>([])
  const [showForm, setShowForm] = useState(false)
  const [error, setError] = useState('')
  const [testing, setTesting] = useState('')
  const [editing, setEditing] = useState<NodeRecord | null>(null)
  const [providers, setProviders] = useState<ProviderTypeRecord[]>([])
  const [overselling, setOverselling] = useState<NodeRecord | null>(null)
  const [limits, setLimits] = useState<Overcommit>()

  async function removeNode(node: NodeRecord) {
    if (!window.confirm(`确认删除节点【${node.name}】？仅在节点上没有未终止的服务时才能删除。`)) return
    setError('')
    try {
      await api(`/api/v1/admin/nodes/${node.id}`, { method: 'DELETE' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types').then(setProviders).catch(() => undefined)
    api<Overcommit>('/api/v1/admin/overcommit-limits').then(setLimits).catch(() => undefined)
  }, [])

  const load = () =>
    api<NodeRecord[]>('/api/v1/admin/nodes')
      .then(setNodes)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function testNode(id: string) {
    setTesting(id)
    setError('')
    try {
      await api(`/api/v1/admin/nodes/${id}/test`, { method: 'POST' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '节点测试失败')
    } finally {
      setTesting('')
    }
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="PROVIDER INTEGRATIONS"
        title="虚拟化节点对接"
        description="统一纳管宿主机：CLICD 与 LXDAPI 通过 API 对接，Hatch Agent 由宿主机主动连入。所有对接方式共用调度与账务模型。"
        action={() => { setEditing(null); setShowForm(true) }}
        actionLabel="新增节点对接"
      />

      {error && <div className="form-error" role="alert">{error}</div>}

      {(showForm || editing) && (
        <NodeForm
          key={editing?.id ?? 'new'}
          node={editing ?? undefined}
          onClose={() => {
            setShowForm(false)
            setEditing(null)
          }}
          onCreated={() => {
            setShowForm(false)
            setEditing(null)
            load()
          }}
        />
      )}

      <div className="provider-strip">
        {providers.map(item => (
          <article className="available" key={item.type}>
            <strong>{item.name}</strong>
            <span>{item.virtualization_types.map(virtualizationLabel).join(' / ')} · {item.agent_managed ? 'Agent 主动连入' : 'API 对接'}</span>
          </article>
        ))}
      </div>

      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>节点名称</th>
              <th>适配器类型</th>
              <th>归属地域</th>
              <th>支持虚拟化</th>
              <th>可售容量</th>
              <th>连接状态</th>
              <th>到期 / 本月流量</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {nodes.map(node => (
              <tr key={node.id}>
                <td>
                  <strong>{node.name}</strong>
                  <small>{node.base_url}</small>
                </td>
                <td><span className="tag">{node.provider_type.toUpperCase()}</span></td>
                <td>
                  {node.region_name}
                  <small>{node.region_code}</small>
                </td>
                <td>{node.virtualization_types.join(' / ').toUpperCase()}</td>
                <td>
                  <strong className="capacity-cell">{node.capacity_vcpu} vCPU</strong>
                  <small>
                    {node.capacity_ram_mb.toLocaleString()} MB / {node.capacity_disk_gb.toLocaleString()} GB
                  </small>
                  <small>检测 {node.reported_vcpu} 核 / {node.reported_ram_mb} MB / {node.reported_disk_gb} GB</small>
                  <small>{overcommitText(node.overcommit, true)}</small>
                  {node.health_hold_reason && <small className="danger-text">暂停销售：{node.health_hold_reason}</small>}
                  {node.shared_machine && <small className="warn-text">与 {node.shared_machine_with?.join('、')} 同机，资源合并计算</small>}
                </td>
                <td>
                  <StatusBadge status={node.status} />
                  <small>心跳 {node.last_seen_at ? formatTime(node.last_seen_at) : '—'}</small>
                </td>
                <td>
                  <NodeExpiry date={node.expires_at} />
                  <small>
                    {formatBytes(node.traffic_used_bytes ?? 0)}
                    {node.traffic_quota_gb ? ` / ${node.traffic_quota_gb.toLocaleString()} GB` : ' · 不限'}
                  </small>
                </td>
                <td>
                  <button
                    className="text-button"
                    onClick={() => testNode(node.id)}
                    disabled={testing === node.id}
                  >
                    <RefreshCw size={13} />
                    {testing === node.id ? '测试连通中…' : '测试连通性'}
                  </button>
                  <div className="row-actions">
                    <button className="text-button" onClick={() => { setShowForm(false); setEditing(node) }}>编辑</button>
                    <button className="text-button" onClick={() => setOverselling(node)}>超售</button>
                    <button className="text-button danger" onClick={() => void removeNode(node)}>删除</button>
                  </div>
                </td>
              </tr>
            ))}
            {!nodes.length && (
              <tr>
                <td colSpan={8} className="empty-state">尚未接入任何虚拟化计算节点</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      {overselling && (
        <OvercommitDialog
          node={overselling}
          name={overselling.name}
          limits={limits}
          endpoint={`/api/v1/admin/nodes/${overselling.id}/overcommit`}
          onClose={() => setOverselling(null)}
          onSaved={() => {
            setOverselling(null)
            void load()
          }}
        />
      )}
    </section>
  )
}

export function NodeForm({ node, onClose, onCreated }: { node?: NodeRecord; onClose: () => void; onCreated: () => void }) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [types, setTypes] = useState<ProviderTypeRecord[]>([])
  const [selected, setSelected] = useState(node?.provider_type ?? 'clicd')
  const editing = Boolean(node)
  const existingOption = (key: string) => {
    const value = node?.provider_options?.[key]
    return Array.isArray(value) ? value.join(',') : value === undefined || value === null ? '' : String(value)
  }

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types')
      .then((items) => {
        setTypes(items)
        if (!node && items.length > 0 && !items.some((item) => item.type === 'clicd')) setSelected(items[0].type)
      })
      .catch((err: Error) => setError(err.message))
  }, [])

  const descriptor = types.find((item) => item.type === selected)
  const options = descriptor?.options ?? []

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!descriptor) return
    setSaving(true)
    setError('')
    const data = new FormData(event.currentTarget)
    const providerOptions: Record<string, unknown> = {}
    for (const field of options) {
      const name = `option_${field.key}`
      providerOptions[field.key] = field.kind === 'bool' ? data.get(name) === 'on' : String(data.get(name) ?? '')
    }
    try {
      await api(node ? `/api/v1/admin/nodes/${node.id}` : '/api/v1/admin/nodes', {
        method: node ? 'PUT' : 'POST',
        body: JSON.stringify({
          // Provider and region are fixed once a node exists.
          ...(node ? {} : { provider_type: descriptor.type, region_code: data.get('region_code'), region_name: data.get('region_name') }),
          name: data.get('name'),
          base_url: descriptor.agent_managed ? '' : data.get('base_url'),
          api_key: data.get('api_key'),
          virtualization_types: data.getAll('virtualization_types'),
          provider_options: providerOptions,
          expires_at: String(data.get('expires_at') ?? ''),
          traffic_quota_gb: Number(data.get('traffic_quota_gb')) || 0,
        }),
      })
      onCreated()
    } catch (err) {
      setError(err instanceof Error ? err.message : '节点接入验证失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{editing ? `编辑节点 ${node?.name}` : '新增虚拟化节点对接'}</h3>
          <p>{editing ? '保存前会用新配置重新验证节点连接；对接方式与地域不可修改。' : '配置节点访问端点并验证连通性，所有通信密钥将加密存储。'}</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit} key={`${selected}-${types.length}`}>
        <label>
          <span>对接方式</span>
          <select name="provider_type" value={selected} disabled={editing} onChange={(event) => setSelected(event.target.value)}>
            {types.map((item) => (
              <option key={item.type} value={item.type}>
                {item.name}（{item.virtualization_types.join(' / ').toUpperCase()}）
              </option>
            ))}
          </select>
        </label>
        <label>
          <span>节点标识名称</span>
          <input name="name" required placeholder="node-sha-01" defaultValue={node?.name} />
        </label>
        <label>
          <span>地域标识代号</span>
          <input name="region_code" required placeholder="SHA" defaultValue={node?.region_code} disabled={editing} />
        </label>
        <label>
          <span>地域中文名称</span>
          <input name="region_name" required placeholder="华东上海" defaultValue={node?.region_name} disabled={editing} />
        </label>
        {descriptor && !descriptor.agent_managed && (
          <label>
            <span>API 接口根地址</span>
            <input name="base_url" type="url" required placeholder={descriptor.base_url_hint} defaultValue={node?.base_url} />
          </label>
        )}
        <label>
          <span>{descriptor?.credential_label ?? 'API Key'}{editing ? '（留空保持不变）' : ''}</span>
          <input name="api_key" type="password" required={!editing} autoComplete="off" />
        </label>
        {descriptor?.agent_managed && (
          <p className="form-hint wide">该节点由 Agent 主动连入。先在宿主机安装 Agent 并连接到本站，再填写 Agent 安装时显示的令牌完成接入。</p>
        )}
        {options.map((field) =>
          field.kind === 'bool' ? (
            <label className="checkbox" key={field.key} title={field.help}>
              <input type="checkbox" name={`option_${field.key}`} defaultChecked={node?.provider_options?.[field.key] === true} /> {field.label}
            </label>
          ) : (
            <label key={field.key} title={field.help}>
              <span>{field.label}{field.required ? '' : '（可选）'}</span>
              <input
                name={`option_${field.key}`}
                type={field.kind === 'number' ? 'number' : 'text'}
                required={field.required}
                placeholder={field.placeholder}
                defaultValue={existingOption(field.key)}
              />
              {field.help && <small>{field.help}</small>}
            </label>
          ),
        )}
        <label>
          <span>母鸡到期日（可选）</span>
          <input name="expires_at" type="date" defaultValue={node?.expires_at ?? ''} />
          <small>向服务商租用的到期日，用于到期提醒</small>
        </label>
        <label>
          <span>月流量限额 GB（可选）</span>
          <input name="traffic_quota_gb" type="number" min={0} defaultValue={node?.traffic_quota_gb ?? 0} />
          <small>0 表示不限；按本节点实例流量合计告警</small>
        </label>
        <fieldset className="wide">
          <legend>支持的虚拟化技术</legend>
          {(descriptor?.virtualization_types ?? []).map((kind, index) => (
            <label className="checkbox" key={kind}>
              <input type="checkbox" name="virtualization_types" value={kind} defaultChecked={node ? node.virtualization_types.includes(kind) : index === 0} /> {virtualizationLabel(kind)}
            </label>
          ))}
        </fieldset>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving || !descriptor}>
            {saving ? '正在验证…' : editing ? '验证并保存' : '验证并接入节点'}
          </button>
        </div>
      </form>
    </div>
  )
}

export function virtualizationLabel(kind: string) {
  switch (kind) {
    case 'lxc': return 'LXC 容器'
    case 'kvm': return 'KVM 硬件虚拟化'
    case 'podman': return 'Podman 容器'
    default: return kind.toUpperCase()
  }
}

export function HostsView({ onOpen }: { onOpen?: (id: string) => void }) {
  const [hosts, setHosts] = useState<HostProbeRecord[]>([])
  const [error, setError] = useState('')

  const load = () =>
    api<HostProbeRecord[]>('/api/v1/admin/hosts')
      .then(setHosts)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
    const timer = window.setInterval(() => void load(), 30000)
    return () => window.clearInterval(timer)
  }, [])

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">HOST TELEMETRY</p>
          <h2>宿主机硬件探针</h2>
          <p>展示集群节点总容量与分配情况；CLICD 节点可进一步查看 CPU、内存条、磁盘健康度与实时曲线。</p>
        </div>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div className="host-grid">
        {hosts.map(host => (
          <article className="host-card" key={host.id}>
            <header>
              <div>
                <span className="tag">{host.provider_type.toUpperCase()}</span>
                <h3>{host.name}</h3>
                <small>
                  {host.region_name} · {host.base_url}
                </small>
              </div>
              <StatusBadge status={host.status} />
            </header>

            <div className="host-summary">
              <span>
                <strong>{host.capacity_vcpu - host.reserved_vcpu}</strong> / {host.capacity_vcpu}
                <small>空闲 vCPU</small>
              </span>
              <span>
                <strong>{(host.capacity_ram_mb - host.reserved_ram_mb).toLocaleString()}</strong> /{' '}
                {host.capacity_ram_mb.toLocaleString()}
                <small>空闲内存 MB</small>
              </span>
              <span>
                <strong>{(host.capacity_disk_gb - host.reserved_disk_gb).toLocaleString()}</strong> /{' '}
                {host.capacity_disk_gb.toLocaleString()}
                <small>空闲磁盘 GB</small>
              </span>
            </div>

            <CapacityBar label="CPU 分配率" used={host.reserved_vcpu} total={host.capacity_vcpu} />
            <CapacityBar label="内存预留率" used={host.reserved_ram_mb} total={host.capacity_ram_mb} suffix=" MB" />

            <footer>
              <span>虚拟化：{host.virtualization_types.join(' / ').toUpperCase()}</span>
              <span>最后心跳：{host.last_seen_at ? formatTime(host.last_seen_at) : '从未'}</span>
            </footer>

            {host.provider_type === 'clicd' && onOpen && (
              <button className="secondary-button host-detail-button" onClick={() => onOpen(host.id)}>
                查看深度硬件探针与采样曲线 <ChevronRight size={15} />
              </button>
            )}
          </article>
        ))}
        {!hosts.length && <div className="empty-card">暂无宿主机探针数据，请先接入虚拟化节点。</div>}
      </div>
    </section>
  )
}
