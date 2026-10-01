import { FormEvent, useEffect, useRef, useState } from 'react'
import { ChevronRight, RefreshCw, ShieldCheck, X } from 'lucide-react'
import { api, cached, HostProbeRecord, NodeRecord, Overcommit, PendingAgentRecord, ProviderTypeRecord, RegionAdminRecord } from '../api'
import { OvercommitDialog, overcommitText } from '../Supply'
import { PageActions, StatusBadge, formatBytes, NodeExpiry, CapacityBar, useReveal } from '../shared/ui'
import { FilterBar, SelectFilter, matchesAny, useUrlFilters, type Option } from './filters'

const nodeStatuses: Option[] = [['online', '在线'], ['offline', '离线'], ['degraded', '降级'], ['maintenance', '维护中'], ['unknown', '未知']]
import { formatTime } from '../shared/time'
import { ConnectSteps, PendingAgents } from '../shared/agents'
import { confirmDialog } from '../shared/dialog'

type Enrollments = { install_command: string; agents: PendingAgentRecord[] }
// FormTarget is what the node form opens for: a pending Hatch agent, a new
// node of a provider type, or an existing node to edit.
type FormTarget = { agent: PendingAgentRecord } | { type: string } | { node: NodeRecord }

export function NodesView() {
  const [nodes, setNodes] = useState<NodeRecord[]>(() => cached<NodeRecord[]>('/api/v1/admin/nodes') ?? [])
  const { filters, set, reset, active } = useUrlFilters(['status'] as const)
  const shownNodes = nodes.filter(node => matchesAny(filters.status, node.status))
  const [form, setForm] = useState<FormTarget | null>(null)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [testing, setTesting] = useState('')
  const [overselling, setOverselling] = useState<NodeRecord | null>(null)
  const [limits, setLimits] = useState<Overcommit | undefined>(() => cached<Overcommit>('/api/v1/admin/overcommit-limits'))
  const [enrollments, setEnrollments] = useState<Enrollments | null>(() => cached<Enrollments>('/api/v1/admin/agent-enrollments') ?? null)
  const [regions, setRegions] = useState<RegionAdminRecord[]>(() => cached<RegionAdminRecord[]>('/api/v1/admin/regions/all') ?? [])

  const load = () =>
    api<NodeRecord[]>('/api/v1/admin/nodes')
      .then(setNodes)
      .catch(err => setError(err.message))
  const loadEnrollments = () => api<Enrollments>('/api/v1/admin/agent-enrollments').then(setEnrollments).catch(() => undefined)
  const loadRegions = () => api<RegionAdminRecord[]>('/api/v1/admin/regions/all').then(setRegions).catch(() => undefined)

  useEffect(() => {
    void load()
    void loadEnrollments()
    void loadRegions()
    api<Overcommit>('/api/v1/admin/overcommit-limits').then(setLimits).catch(() => undefined)
    // Hosts that just ran the install command show up on their own.
    const timer = window.setInterval(() => {
      if (!document.hidden) void loadEnrollments()
    }, 20000)
    return () => window.clearInterval(timer)
  }, [])

  async function removeNode(node: NodeRecord) {
    if (!(await confirmDialog({ title: `删除节点【${node.name}】？`, message: '仅在节点上没有未终止的服务时才能删除。', confirmText: '删除', danger: true }))) return
    setError('')
    try {
      await api(`/api/v1/admin/nodes/${node.id}`, { method: 'DELETE' })
      await load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
    }
  }

  async function dismiss(agent: PendingAgentRecord) {
    if (!(await confirmDialog({ title: `从待接入列表移除 ${agent.hostname || '这台主机'}？`, message: '它重新连接后会再次出现。', confirmText: '移除' }))) return
    try {
      await api(`/api/v1/admin/agent-enrollments/${agent.id}`, { method: 'DELETE' })
      void loadEnrollments()
    } catch (err) {
      setError(err instanceof Error ? err.message : '移除失败')
    }
  }

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
        description="统一纳管宿主机：Hatch Agent 由宿主机主动连入，LXDAPI 与 CLICD 通过 API 对接。所有对接方式共用调度与账务模型。"
        action={() => setForm({ type: 'lxdapi' })}
        actionLabel="手动新增节点"
      />

      {error && <div className="form-error" role="alert">{error}</div>}
      {notice && <div className="form-success" role="status">{notice}</div>}

      {form && (
        <NodeForm
          key={'node' in form ? form.node.id : 'agent' in form ? form.agent.id : form.type}
          target={form}
          regions={regions}
          onClose={() => setForm(null)}
          onCreated={name => {
            setNotice('node' in form ? `节点 ${name} 已保存。` : `节点 ${name} 已接入，现在可以在「套餐」里为它创建套餐。`)
            setForm(null)
            void load()
            void loadEnrollments()
            void loadRegions()
          }}
        />
      )}

      <ConnectGuide enrollments={enrollments} onAdd={agent => {
          setNotice('')
          setForm({ agent })
        }} onDismiss={agent => void dismiss(agent)} onManual={type => setForm({ type })} />

      <div className="section-heading">
        <h3>已接入节点</h3>
        <button className="secondary-button" onClick={() => void load()}>
          <RefreshCw size={15} />刷新
        </button>
      </div>
      <FilterBar shown={shownNodes.length} total={nodes.length} active={active} onReset={reset}>
        <SelectFilter label="连接状态" value={filters.status} onChange={value => set('status', value)} options={nodeStatuses} />
      </FilterBar>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>节点</th>
              <th>地域</th>
              <th>可售容量</th>
              <th>连接状态</th>
              <th>到期 / 本月流量</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {shownNodes.map(node => (
              <tr key={node.id}>
                <td>
                  <strong>{node.name}</strong>
                  <span className="tag-row">
                    <span className="tag">{node.provider_type.toUpperCase()}</span>
                    {node.virtualization_types.map(kind => (
                      <span className="tag muted" key={kind}>{kind.toUpperCase()}</span>
                    ))}
                  </span>
                  <small className="truncate" title={node.base_url}>{node.base_url}</small>
                </td>
                <td>
                  {node.region_name}
                  <small>{node.region_code}</small>
                </td>
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
                  <button className="text-button" onClick={() => testNode(node.id)} disabled={testing === node.id}>
                    <RefreshCw size={13} />
                    {testing === node.id ? '测试连通中…' : '测试连通性'}
                  </button>
                  <div className="row-actions">
                    <button className="text-button" onClick={() => setForm({ node })}>编辑</button>
                    <button className="text-button" onClick={() => setOverselling(node)}>超售</button>
                    <button className="text-button danger" onClick={() => void removeNode(node)}>删除</button>
                  </div>
                </td>
              </tr>
            ))}
            {!shownNodes.length && (
              <tr>
                <td colSpan={6} className="empty-state">{nodes.length ? '没有该状态的节点' : '尚未接入任何虚拟化计算节点，按上面的教程接入第一台。'}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
      <RegionManager regions={regions} onChanged={() => void loadRegions()} onError={setError} />
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

type GuideTab = 'hatch' | 'lxdapi' | 'clicd'

// ConnectGuide is the integration tutorial: one tab per backend, with the
// steps the platform automates marked as such.
function ConnectGuide({
  enrollments,
  onAdd,
  onDismiss,
  onManual,
}: {
  enrollments: Enrollments | null
  onAdd: (agent: PendingAgentRecord) => void
  onDismiss: (agent: PendingAgentRecord) => void
  onManual: (type: string) => void
}) {
  const [tab, setTab] = useState<GuideTab>('hatch')
  const pending = enrollments?.agents.length ?? 0
  return (
    <section className="panel guide-panel">
      <div className="panel-heading">
        <h3>接入教程</h3>
      </div>
      <div className="segmented" role="tablist">
        <button role="tab" aria-selected={tab === 'hatch'} className={tab === 'hatch' ? 'active' : ''} onClick={() => setTab('hatch')}>
          Hatch Agent（推荐）{pending > 0 && <span className="count-badge">{pending}</span>}
        </button>
        <button role="tab" aria-selected={tab === 'lxdapi'} className={tab === 'lxdapi' ? 'active' : ''} onClick={() => setTab('lxdapi')}>
          LXDAPI
        </button>
        <button role="tab" aria-selected={tab === 'clicd'} className={tab === 'clicd' ? 'active' : ''} onClick={() => setTab('clicd')}>
          CLICD
        </button>
      </div>

      {tab === 'hatch' && (
        <div className="guide-body">
          <p>
            自研 Agent，支持 LXC（LXD / Incus）和 Podman 容器，适合 NAT 小鸡。Agent 从母机主动连到本站，母机不用开放任何端口，也不用公网 API。
          </p>
          <ul className="auto-list">
            <li>自动识别公网 IP、CPU / 内存 / 硬盘</li>
            <li>按你选的虚拟化自动安装 Podman 或 Incus（含存储池、网桥和系统镜像），自动做内核网络调优和磁盘测速</li>
            <li>自动出现在下方待接入列表，不用复制令牌；镜像列表和容量自动同步</li>
          </ul>
          {enrollments ? (
            <ConnectSteps command={enrollments.install_command} where="本页" actionLabel="接入" />
          ) : (
            <p className="muted-text">正在读取安装命令…</p>
          )}
          <h4 className="subheading">待接入的母机</h4>
          <PendingAgents agents={enrollments?.agents ?? []} actionLabel="接入" onAdd={onAdd} onDismiss={onDismiss} />
          <p className="muted-text">
            需要 LXC 时，先在母机装好 LXD 或 Incus，并建一个 btrfs 或 lvm 存储池（ZFS 池不能限制磁盘读写速度）。已有 Agent 令牌的老母机可以
            <button type="button" className="link-button inline" onClick={() => onManual('hatch')}>手动填写令牌接入</button>。
          </p>
        </div>
      )}

      {tab === 'lxdapi' && (
        <div className="guide-body">
          <p>
            对接第三方面板 <a href="https://github.com/xkatld/lxdapi-web-server" target="_blank" rel="noreferrer">LXDAPI</a>（LXC）。本站通过它的系统接口开通和管理容器。
          </p>
          <ol className="step-list">
            <li>
              在母机上按 LXDAPI 仓库 <code>Shell/</code> 目录依次运行 <code>lxd_install.sh</code>、<code>lxdapi_install.sh</code>、<code>image_import.sh</code>。
            </li>
            <li>
              LXDAPI 后台「NAT 配置」：填出口网卡（如 <code>eth0</code>）和端口段（如 40000–49999），开启「自动分配 22 端口」。服务商做 1:1 NAT
              （网卡上只有内网 IP，如甲骨文）时，「网卡 IP」填网卡上的内网地址。
            </li>
            <li>在 LXDAPI 后台记下 API Hash。</li>
            <li>
              点下面的按钮填写接口地址（如 <code>https://节点IP:8443</code>）和 API Hash。证书指纹点「自动读取」即可，公网 IP 按接口地址自动填，网卡和端口段已预填默认值，与第 2 步保持一致即可。
            </li>
          </ol>
          <p className="muted-text">
            LXDAPI 的系统接口不提供镜像列表和宿主机容量，所以要手动填写可售镜像别名和可分配的 CPU / 内存 / 硬盘。它自带的 Ubuntu 24.04 镜像禁止密码 SSH，需要按文档修正一次。
          </p>
          <button type="button" className="primary-button compact" onClick={() => onManual('lxdapi')}>新增 LXDAPI 节点</button>
        </div>
      )}

      {tab === 'clicd' && (
        <div className="guide-body">
          <p>
            对接 <a href="https://cli.cd" target="_blank" rel="noreferrer">CLICD</a> 面板（LXC / KVM）。镜像列表、宿主机容量和硬件探针都从 CLICD 自动读取。
          </p>
          <ol className="step-list">
            <li>按 CLICD 官方文档在母机上安装 CLICD，并在其后台启用并下载要出售的镜像。</li>
            <li>在 CLICD 后台创建 API Key。</li>
            <li>点下面的按钮，填写接口地址（如 <code>http://节点IP:8999</code>）和 API Key，勾选要出售的虚拟化类型。</li>
          </ol>
          <button type="button" className="primary-button compact" onClick={() => onManual('clicd')}>新增 CLICD 节点</button>
        </div>
      )}
    </section>
  )
}

// RegionManager renames regions and switches them on or off. Regions are
// created by typing a new name when adding a node or publishing a host.
function RegionManager({ regions, onChanged, onError }: { regions: RegionAdminRecord[]; onChanged: () => void; onError: (message: string) => void }) {
  const [editing, setEditing] = useState<string | null>(null)

  async function save(region: RegionAdminRecord, name: string, enabled: boolean) {
    try {
      await api(`/api/v1/admin/regions/${region.id}`, { method: 'PUT', body: JSON.stringify({ name, enabled }) })
      setEditing(null)
      onChanged()
    } catch (err) {
      onError(err instanceof Error ? err.message : '保存失败')
    }
  }

  return (
    <section className="panel">
      <div className="panel-heading">
        <div>
          <h3>地域管理</h3>
          <small>新增节点或机主发布母机时直接输入新地域名称即可自动创建；停用的地域不能再接入新节点。</small>
        </div>
      </div>
      <div className="region-list">
        {regions.map(region =>
          editing === region.id ? (
            <form
              key={region.id}
              className="region-item editing"
              onSubmit={event => {
                event.preventDefault()
                void save(region, String(new FormData(event.currentTarget).get('name') ?? ''), region.enabled)
              }}
            >
              <input name="name" defaultValue={region.name} maxLength={40} required autoFocus />
              <button className="primary-button compact">保存</button>
              <button type="button" className="text-button" onClick={() => setEditing(null)}>取消</button>
            </form>
          ) : (
            <div key={region.id} className={region.enabled ? 'region-item' : 'region-item disabled'}>
              <div>
                <strong>{region.name}</strong>
                <small>{region.code} · {region.nodes} 个节点{region.enabled ? '' : ' · 已停用'}</small>
              </div>
              <div className="row-actions">
                <button className="text-button" onClick={() => setEditing(region.id)}>改名</button>
                <button className="text-button" onClick={() => void save(region, region.name, !region.enabled)}>{region.enabled ? '停用' : '启用'}</button>
              </div>
            </div>
          ),
        )}
        {!regions.length && <p className="muted-text">还没有地域，接入第一台节点时输入名称即可创建。</p>}
      </div>
    </section>
  )
}

// defaults prefill a new node's provider options with the values the
// tutorial uses.
const optionDefaults: Record<string, string> = {
  nat_interface: 'eth0',
  port_range_start: '40000',
  port_range_end: '49999',
  username: 'vpsbill',
}

function agentVirtualization(agent: PendingAgentRecord) {
  return agent.runtimes.filter(item => item === 'lxc' || item === 'podman')
}

export function NodeForm({
  target,
  regions,
  onClose,
  onCreated,
}: {
  target: FormTarget
  regions: RegionAdminRecord[]
  onClose: () => void
  onCreated: (name: string) => void
}) {
  const node = 'node' in target ? target.node : undefined
  const agent = 'agent' in target ? target.agent : undefined
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [saving, setSaving] = useState(false)
  const [types, setTypes] = useState<ProviderTypeRecord[]>(() => cached<ProviderTypeRecord[]>('/api/v1/admin/provider-types') ?? [])
  const [selected, setSelected] = useState(node?.provider_type ?? (agent ? 'hatch' : 'type' in target ? target.type : 'hatch'))
  const [probing, setProbing] = useState(false)
  const editing = Boolean(node)
  const existingOption = (key: string) => {
    const value = node?.provider_options?.[key]
    if (!node) return optionDefaults[key] ?? ''
    return Array.isArray(value) ? value.join(',') : value === undefined || value === null ? '' : String(value)
  }

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types')
      .then(setTypes)
      .catch((err: Error) => setError(err.message))
  }, [])

  const descriptor = types.find(item => item.type === selected)
  const options = descriptor?.options ?? []
  const panel = useRef<HTMLDivElement>(null)
  useReveal(panel, types.length > 0)

  // readCertificate pins the node's certificate: the server reads it, the
  // administrator sees what was pinned.
  async function readCertificate(form: HTMLFormElement) {
    const baseURL = String(new FormData(form).get('base_url') ?? '')
    setProbing(true)
    setError('')
    try {
      const result = await api<{ fingerprint: string; subject: string; not_after: string; trusted: boolean }>('/api/v1/admin/nodes/tls-probe', {
        method: 'POST',
        body: JSON.stringify({ base_url: baseURL }),
      })
      const input = form.elements.namedItem('option_tls_fingerprint') as HTMLInputElement | null
      if (input) input.value = result.fingerprint
      setNotice(
        `已读取证书（${result.subject}，${formatTime(result.not_after)} 到期）${result.trusted ? '。这是受信任的证书，也可以直接勾选「校验 HTTPS 证书」。' : '，已填入指纹用于固定。'}`,
      )
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取证书失败')
    } finally {
      setProbing(false)
    }
  }

  // The public IPv4 is usually the host in the API address.
  function fillPublicIP(form: HTMLFormElement) {
    const input = form.elements.namedItem('option_public_ipv4') as HTMLInputElement | null
    if (!input || input.value) return
    try {
      const host = new URL(String(new FormData(form).get('base_url') ?? '')).hostname
      if (/^\d+\.\d+\.\d+\.\d+$/.test(host) && !/^(10|127|192\.168|172\.(1[6-9]|2\d|3[01]))\./.test(host)) input.value = host
    } catch {
      // not a URL yet
    }
  }

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
          ...(node ? {} : { provider_type: descriptor.type, region_name: data.get('region_name') }),
          ...(agent ? { enrollment_id: agent.id } : {}),
          name: data.get('name'),
          base_url: descriptor.agent_managed ? '' : data.get('base_url'),
          api_key: agent ? '' : data.get('api_key'),
          virtualization_types: data.getAll('virtualization_types'),
          provider_options: providerOptions,
          expires_at: String(data.get('expires_at') ?? ''),
          traffic_quota_gb: Number(data.get('traffic_quota_gb')) || 0,
        }),
      })
      onCreated(String(data.get('name') ?? ''))
    } catch (err) {
      setError(err instanceof Error ? err.message : '节点接入验证失败')
    } finally {
      setSaving(false)
    }
  }

  const heading = editing ? `编辑节点 ${node?.name}` : agent ? `接入母机 ${agent.hostname || ''}` : `新增 ${descriptor?.name ?? ''} 节点`
  const virtualizationDefaults = agent ? agentVirtualization(agent) : node?.virtualization_types
  return (
    <div className="inline-form" ref={panel}>
      <div className="inline-form-heading">
        <div>
          <h3>{heading}</h3>
          <p>
            {editing
              ? '保存前会用新配置重新验证节点连接；对接方式与地域不可修改。'
              : agent
                ? `${agent.public_ipv4 || agent.remote_ip} · 已检测 ${agent.capacity ? `${agent.capacity.vcpu} 核 / ${agent.capacity.ram_mb} MB / ${agent.capacity.disk_gb} GB` : '容量待读取'}。补充名称和地域即可接入。`
                : '保存时会实时连一次节点，失败会直接提示原因；所有密钥加密存储。'}
          </p>
        </div>
        <button className="icon-button" aria-label="关闭" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit} key={`${selected}-${types.length}`}>
        {!agent && (
          <label>
            <span>对接方式</span>
            <select name="provider_type" value={selected} disabled={editing} onChange={event => setSelected(event.target.value)}>
              {types.map(item => (
                <option key={item.type} value={item.type}>
                  {item.name}（{item.virtualization_types.join(' / ').toUpperCase()}）
                </option>
              ))}
            </select>
          </label>
        )}
        <label>
          <span>节点名称</span>
          <input name="name" required placeholder="例如 hk-nat-01" defaultValue={node?.name ?? agent?.hostname} />
        </label>
        {!editing && (
          <label>
            <span>地域</span>
            <input name="region_name" required maxLength={40} list="admin-regions" placeholder="选择或输入，例如 香港" autoComplete="off" />
            <datalist id="admin-regions">
              {regions.filter(region => region.enabled).map(region => (
                <option key={region.id} value={region.name} />
              ))}
            </datalist>
            <small>输入新名称会自动创建地域</small>
          </label>
        )}
        {descriptor && !descriptor.agent_managed && (
          <label>
            <span>API 接口根地址</span>
            <input
              name="base_url"
              type="url"
              required
              placeholder={descriptor.base_url_hint}
              defaultValue={node?.base_url}
              onBlur={event => event.currentTarget.form && fillPublicIP(event.currentTarget.form)}
            />
          </label>
        )}
        {!agent && (
          <label>
            <span>{descriptor?.credential_label ?? 'API Key'}{editing ? '（留空保持不变）' : ''}</span>
            <input name="api_key" type="password" required={!editing} autoComplete="off" />
            {descriptor?.agent_managed && <small>在母机上运行 hatch-agent token 查看</small>}
          </label>
        )}
        {options.map(field =>
          field.kind === 'bool' ? (
            <label className="checkbox" key={field.key} title={field.help}>
              <input type="checkbox" name={`option_${field.key}`} defaultChecked={node?.provider_options?.[field.key] === true} /> {field.label}
            </label>
          ) : (
            <label key={field.key} title={field.help}>
              <span>{field.label}{field.required ? '' : '（可选）'}</span>
              {field.key === 'tls_fingerprint' ? (
                <div className="input-with-button">
                  <input name={`option_${field.key}`} type="text" placeholder="点「自动读取」" defaultValue={existingOption(field.key)} />
                  <button
                    type="button"
                    className="secondary-button compact"
                    disabled={probing}
                    onClick={event => event.currentTarget.form && void readCertificate(event.currentTarget.form)}
                  >
                    <ShieldCheck size={14} />
                    {probing ? '读取中…' : '自动读取'}
                  </button>
                </div>
              ) : (
                <input
                  name={`option_${field.key}`}
                  type={field.kind === 'number' ? 'number' : 'text'}
                  required={field.required}
                  placeholder={field.placeholder}
                  defaultValue={existingOption(field.key)}
                />
              )}
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
          <legend>出售的虚拟化类型</legend>
          {(descriptor?.virtualization_types ?? []).map((kind, index) => (
            <label className="checkbox" key={kind}>
              <input
                type="checkbox"
                name="virtualization_types"
                value={kind}
                defaultChecked={virtualizationDefaults ? virtualizationDefaults.includes(kind) : index === 0}
              />{' '}
              {virtualizationLabel(kind)}
            </label>
          ))}
        </fieldset>

        {notice && <div className="form-success wide">{notice}</div>}
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
  const [hosts, setHosts] = useState<HostProbeRecord[]>(() => cached<HostProbeRecord[]>('/api/v1/admin/hosts') ?? [])
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
