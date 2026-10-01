import { ReactNode, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { api, imageLabel, AvailableTemplateRecord, NodeRecord, NodeSelection, PlanCategoryRecord, PlanRecord } from '../api'
import { NodePicker, nodeFits } from './PlanForm'
import { TagInput } from '../shared/tags'

// planBody is what PUT expects for a saved platform plan.
export function planBody(plan: PlanRecord) {
  return {
    id: plan.id,
    code: plan.code,
    name: plan.name,
    provider_type: plan.provider_type,
    virtualization: plan.virtualization,
    vcpu: plan.vcpu,
    ram_mb: plan.ram_mb,
    disk_gb: plan.disk_gb,
    traffic_gb: plan.traffic_gb,
    network_down_mbps: plan.network_down_mbps,
    network_up_mbps: plan.network_up_mbps,
    snapshot_limit: plan.snapshot_limit,
    assign_nat: plan.assign_nat,
    port_mapping_count: plan.port_mapping_count,
    assign_ipv4: plan.assign_ipv4,
    ipv4_count: plan.ipv4_count,
    assign_ipv6: plan.assign_ipv6,
    ipv6_count: plan.ipv6_count,
    default_template_id: plan.default_template_id,
    allowed_template_ids: [...plan.allowed_template_ids],
    enabled: plan.enabled,
    prices: plan.prices.map(price => ({
      currency: price.currency,
      billing_cycle: price.billing_cycle,
      amount_minor: price.amount_minor,
      setup_fee_minor: price.setup_fee_minor,
      purchase_limit: price.purchase_limit ?? null,
    })),
    stock_limit: plan.stock_limit ?? null,
    disk_read_mbps: plan.disk_read_mbps ?? 0,
    disk_write_mbps: plan.disk_write_mbps ?? 0,
    disk_read_iops: plan.disk_read_iops ?? 0,
    disk_write_iops: plan.disk_write_iops ?? 0,
    category_id: plan.category_id ?? '',
    node_selection: plan.node_selection ?? 'pack',
    node_ids: plan.node_ids ?? [],
    description: plan.description ?? '',
    tags: plan.tags ?? [],
  }
}

type NumberField = 'traffic_gb' | 'network_down_mbps' | 'network_up_mbps' | 'snapshot_limit' | 'port_mapping_count'
const numberFields: { field: NumberField; label: string; min: number; max?: number }[] = [
  { field: 'traffic_gb', label: '月度流量 GB（0 不限）', min: 0 },
  { field: 'network_down_mbps', label: '下行带宽 Mbps', min: 0 },
  { field: 'network_up_mbps', label: '上行带宽 Mbps', min: 0 },
  { field: 'snapshot_limit', label: '快照配额', min: 0 },
  { field: 'port_mapping_count', label: 'NAT 端口映射配额', min: 0, max: 64 },
]

// PlanBatchEdit changes the chosen settings of several plans at once. Only
// ticked settings change; the rest of each plan stays as it is.
export function PlanBatchEdit({
  plans,
  categories,
  nodes,
  onClose,
  onSaved,
}: {
  plans: PlanRecord[]
  categories: PlanCategoryRecord[]
  nodes: NodeRecord[] | null
  onClose: () => void
  onSaved: () => void
}) {
  const [on, setOn] = useState<Record<string, boolean>>({})
  const [category, setCategory] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [numbers, setNumbers] = useState<Record<NumberField, string>>({ traffic_gb: '', network_down_mbps: '', network_up_mbps: '', snapshot_limit: '', port_mapping_count: '' })
  const [templates, setTemplates] = useState<AvailableTemplateRecord[]>([])
  const [addImages, setAddImages] = useState<string[]>([])
  const [removeImages, setRemoveImages] = useState<string[]>([])
  const [defaultImage, setDefaultImage] = useState('')
  const [selection, setSelection] = useState<NodeSelection>('nodes')
  const [nodeIds, setNodeIds] = useState<string[]>([])
  const [percent, setPercent] = useState('')
  const [tags, setTags] = useState<string[]>([])
  const [description, setDescription] = useState('')
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  useEffect(() => {
    api<AvailableTemplateRecord[]>('/api/v1/admin/templates').then(setTemplates).catch(err => setError(err.message))
  }, [])

  const kinds = [...new Set(plans.map(plan => `${plan.provider_type}/${plan.virtualization}`))]
  const sameKind = kinds.length === 1
  // Images are offered where at least one chosen plan can use them.
  const addable = templates.filter(item => plans.some(plan => plan.provider_type === item.provider_type && plan.virtualization === item.virtualization))
  const addableIDs = [...new Set(addable.map(item => item.id))]
  const current = [...new Set(plans.flatMap(plan => plan.allowed_template_ids))]
  const label = (id: string) => {
    const item = templates.find(candidate => candidate.id === id)
    return item ? imageLabel(item) : id
  }

  function build() {
    const bodies = []
    for (const plan of plans) {
      const body = planBody(plan)
      if (on.category) body.category_id = category
      if (on.enabled) body.enabled = enabled
      if (on.tags) body.tags = tags
      if (on.description) body.description = description.trim()
      for (const { field } of numberFields) {
        if (!on[field]) continue
        const value = Number(numbers[field])
        if (numbers[field].trim() === '' || !Number.isInteger(value) || value < 0) return `请填写有效的${numberFields.find(item => item.field === field)?.label}`
        body[field] = value
      }
      if (on.images) {
        // An image joins only plans whose provider and virtualization have it.
        const fits = addImages.filter(id => templates.some(item => item.id === id && item.provider_type === plan.provider_type && item.virtualization === plan.virtualization))
        body.allowed_template_ids = [...new Set([...body.allowed_template_ids, ...fits])].filter(id => !removeImages.includes(id))
        if (!body.allowed_template_ids.length) return `套餐「${plan.name}」移除后没有可选镜像`
        if (defaultImage && body.allowed_template_ids.includes(defaultImage)) body.default_template_id = defaultImage
        if (!body.allowed_template_ids.includes(body.default_template_id)) body.default_template_id = body.allowed_template_ids[0]
      }
      if (on.nodes) {
        if (selection === 'nodes' && !nodeIds.length) return '「指定节点」方式需至少勾选一个节点'
        body.node_selection = selection
        body.node_ids = selection === 'nodes' ? nodeIds : []
      }
      if (on.price) {
        const value = Number(percent)
        if (!percent.trim() || !Number.isFinite(value) || value <= -100 || value > 1000) return '价格调整需在 -99% 到 1000% 之间'
        body.prices = body.prices.map(price => ({ ...price, amount_minor: Math.max(1, Math.round((price.amount_minor * (100 + value)) / 100)) }))
      }
      bodies.push(body)
    }
    return bodies
  }

  async function save() {
    if (!Object.values(on).some(Boolean)) {
      setError('请至少勾选一项要修改的设置')
      return
    }
    const bodies = build()
    if (typeof bodies === 'string') {
      setError(bodies)
      return
    }
    setSaving(true)
    setError('')
    try {
      await api('/api/v1/admin/plans/batch', { method: 'PUT', body: JSON.stringify({ plans: bodies }) })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '批量修改失败')
    } finally {
      setSaving(false)
    }
  }

  const toggle = (key: string) => (event: { target: { checked: boolean } }) => setOn(state => ({ ...state, [key]: event.target.checked }))
  // Row is called as a function, not rendered as a component, so its inputs
  // keep focus across renders.
  const Row = ({ id, title, children }: { id: string; title: string; children: ReactNode }) => (
    <div key={id} className={on[id] ? 'batch-edit-row active' : 'batch-edit-row'}>
      <label className="checkbox batch-edit-toggle">
        <input type="checkbox" checked={!!on[id]} onChange={toggle(id)} />
        {title}
      </label>
      {on[id] && <div className="batch-edit-body">{children}</div>}
    </div>
  )

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>批量修改 {plans.length} 个套餐</h3>
          <p>只修改勾选的设置，其余保持各自原样。所有套餐一起保存，有一个不通过就都不保存。修改会让套餐版本号 +1，已购实例不受影响。</p>
        </div>
        <button className="icon-button" onClick={onClose} aria-label="关闭"><X size={18} /></button>
      </div>
      <p className="batch-edit-targets">{plans.map(plan => plan.name).join('、')}</p>

      <div className="batch-edit">
        {Row({
          id: 'category',
          title: '商品分类',
          children: (
            <select value={category} onChange={event => setCategory(event.target.value)}>
              <option value="">未分类</option>
              {categories.map(item => <option key={item.id} value={item.id}>{item.name}</option>)}
            </select>
          ),
        })}
        {Row({
          id: 'enabled',
          title: '上架状态',
          children: (
            <select value={enabled ? 'on' : 'off'} onChange={event => setEnabled(event.target.value === 'on')}>
              <option value="on">上架</option>
              <option value="off">下架</option>
            </select>
          ),
        })}
        {Row({
          id: 'tags',
          title: '标签（替换为）',
          children: <TagInput value={tags} onChange={setTags} placeholder="留空表示清除标签" />,
        })}
        {Row({
          id: 'description',
          title: '套餐描述（替换为）',
          children: <textarea rows={2} maxLength={500} value={description} placeholder="留空表示清除描述" onChange={event => setDescription(event.target.value)} />,
        })}
        {numberFields.map(({ field, label: title, min, max }) =>
          Row({
            id: field,
            title,
            children: (
              <input type="number" min={min} max={max} value={numbers[field]} placeholder="新的值" onChange={event => setNumbers(state => ({ ...state, [field]: event.target.value }))} />
            ),
          })
        )}
        {Row({
          id: 'images',
          title: '系统镜像',
          children: (
            <div className="batch-edit-images">
              <div>
                <strong>添加镜像</strong>
                <small>只加到对接方式和虚拟化都匹配的套餐</small>
                {addableIDs.length ? (
                  addableIDs.map(id => (
                    <label key={id} className="checkbox">
                      <input type="checkbox" checked={addImages.includes(id)} onChange={event => setAddImages(state => (event.target.checked ? [...state, id] : state.filter(item => item !== id)))} />
                      {label(id)}
                    </label>
                  ))
                ) : (
                  <small>在线节点暂无可添加的镜像</small>
                )}
              </div>
              <div>
                <strong>移除镜像</strong>
                <small>已装该镜像的实例不受影响</small>
                {current.map(id => (
                  <label key={id} className="checkbox">
                    <input type="checkbox" checked={removeImages.includes(id)} onChange={event => setRemoveImages(state => (event.target.checked ? [...state, id] : state.filter(item => item !== id)))} />
                    {label(id)}
                  </label>
                ))}
              </div>
              <div>
                <strong>默认镜像</strong>
                <small>套餐可选该镜像时才会改；被移除时改用第一个可选镜像</small>
                <select value={defaultImage} onChange={event => setDefaultImage(event.target.value)}>
                  <option value="">保持不变</option>
                  {[...new Set([...current, ...addImages])]
                    .filter(id => !removeImages.includes(id))
                    .map(id => <option key={id} value={id}>{label(id)}</option>)}
                </select>
              </div>
            </div>
          ),
        })}
        {Row({
          id: 'nodes',
          title: '节点选择方式',
          children: sameKind ? (
            <NodePicker
              nodes={nodes}
              providerType={plans[0].provider_type}
              virtualization={plans[0].virtualization}
              selection={selection}
              onSelection={setSelection}
              ids={nodeIds.filter(id => nodes?.some(node => node.id === id && nodeFits(node, plans[0].provider_type, plans[0].virtualization)))}
              onIds={setNodeIds}
            />
          ) : (
            <small className="field-hint">所选套餐的对接方式或虚拟化不一致（{kinds.join('、')}），请分开修改节点。</small>
          ),
        })}
        {Row({
          id: 'price',
          title: '价格按比例调整',
          children: (
            <label className="batch-edit-inline">
              <input type="number" step="1" value={percent} placeholder="如 10 或 -10" onChange={event => setPercent(event.target.value)} />
              <span>%（所有计费周期，取整到分）。和单独改价一样，已购实例续费也按新价格（锁定了续费价的除外）。</span>
            </label>
          ),
        })}
      </div>

      {error && <div className="form-error">{error}</div>}
      <div className="form-actions">
        <button type="button" className="secondary-button" onClick={onClose}>取消</button>
        <button type="button" className="primary-button" disabled={saving} onClick={save}>
          {saving ? '正在保存…' : `保存到 ${plans.length} 个套餐`}
        </button>
      </div>
    </div>
  )
}
