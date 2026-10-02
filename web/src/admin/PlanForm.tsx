import { FormEvent, useEffect, useRef, useState } from 'react'
import { Plus, Trash2, X } from 'lucide-react'
import {
  api,
  cached,
  imageLabel,
  AvailableTemplateRecord,
  NodeRecord,
  NodeSelection,
  PlanCategoryRecord,
  PlanPresetRecord,
  PlanRecord,
  Price,
  ProviderTypeRecord,
  StockCapacityRecord,
} from '../api'
import { cycleName, CyclePriceFields, namedCycles, readCyclePrices } from '../shared/cycles'
import { readStock, StockField } from '../shared/stock'
import { DiskIOFields, readDiskIO } from '../shared/diskio'
import { confirmDialog, promptDialog } from '../shared/dialog'
import { TagInput } from '../shared/tags'
import { virtualizationLabel } from './Nodes'
import { toast } from '../shared/toast'
import { t, tr } from '../shared/i18n'
import { ledgerCurrency, ledgerUnit } from '../shared/currency'

type Virtualization = PlanRecord['virtualization']

// PlanSeed prefills a new plan: a copy of another plan or a preset's
// settings. Presets also carry the batch form's price formula and cycles.
export type PlanSeed = Partial<PlanRecord> & { formula?: PriceFormula; cycles?: string[] }

export const selectionLabels: Record<NodeSelection, string> = {
  nodes: t('指定节点'),
  pack: t('自动 · 集中填充'),
  spread: t('自动 · 分散均衡'),
}

const selectionHints: Record<NodeSelection, string> = {
  nodes: t('只在勾选的节点上开通；勾选多个时，先填满剩余内存最少的一台。客户下单时只能选这些节点所在的地域。'),
  pack: t('在同对接方式、同虚拟化的全部平台节点中，选剩余内存最少但还放得下的节点：先填满一台再用下一台。'),
  spread: t('在同对接方式、同虚拟化的全部平台节点中，选剩余内存最多的节点，让各节点负载更均衡。'),
}

// placementText summarises a plan's node selection for its card.
export function placementText(plan: Pick<PlanRecord, 'node_selection' | 'node_ids'>, nodes: NodeRecord[] | null) {
  const selection = plan.node_selection ?? 'pack'
  if (selection !== 'nodes') return selectionLabels[selection]
  const ids = plan.node_ids ?? []
  const names = nodes ? ids.map(id => nodes.find(node => node.id === id)?.name ?? t('已删除节点')) : []
  return names.length ? t('指定节点：{0}', names.join('、')) : t('指定 {0} 个节点', ids.length)
}

// Settings a series of plans shares; a preset stores these.
const sharedKeys = [
  'provider_type', 'virtualization', 'category_id', 'node_selection', 'node_ids', 'tags',
  'traffic_gb', 'network_down_mbps', 'network_up_mbps', 'snapshot_limit',
  'assign_nat', 'port_mapping_count', 'assign_ipv4', 'ipv4_count', 'assign_ipv6', 'ipv6_count',
  'allowed_template_ids', 'default_template_id',
  'disk_read_mbps', 'disk_write_mbps', 'disk_read_iops', 'disk_write_iops',
] as const

// PriceFormula prices a plan from its size: a monthly price from unit
// prices, longer cycles as months x monthly less a discount, rounded to a
// step in yuan.
export type PriceFormula = {
  base: number
  vcpu: number
  ram_gb: number
  disk_10gb: number
  traffic_100gb: number
  mbps_10: number
  step: number
  discounts: Record<string, number>
}

const defaultFormula: PriceFormula = { base: 0, vcpu: 0, ram_gb: 0, disk_10gb: 0, traffic_100gb: 0, mbps_10: 0, step: 0.1, discounts: { quarterly: 0, semiannual: 5, annual: 10 } }
const cycleMonths: Record<string, number> = { monthly: 1, quarterly: 3, semiannual: 6, annual: 12 }

type Row = {
  key: number
  name: string
  code: string
  vcpu: number
  ram_mb: number
  disk_gb: number
  traffic_gb: number
  down: number
  up: number
  stock: string
  prices: Record<string, string>
}

type RowSize = Pick<Row, 'vcpu' | 'ram_mb' | 'disk_gb' | 'traffic_gb' | 'down'>

export function formulaPrice(formula: PriceFormula, row: RowSize, cycle: string) {
  const monthly =
    formula.base + formula.vcpu * row.vcpu + (formula.ram_gb * row.ram_mb) / 1024 + (formula.disk_10gb * row.disk_gb) / 10 +
    (formula.traffic_100gb * row.traffic_gb) / 100 + (formula.mbps_10 * row.down) / 10
  const total = monthly * (cycleMonths[cycle] ?? 1) * (1 - (cycle === 'monthly' ? 0 : formula.discounts[cycle] ?? 0) / 100)
  const step = formula.step > 0 ? formula.step : 0.01
  const rounded = Math.round(total / step) * step
  return rounded > 0 ? rounded.toFixed(2) : ''
}

// sizeCode turns a size into a code suffix: 2C2G, 1C512M.
function sizeCode(row: Pick<Row, 'vcpu' | 'ram_mb'>) {
  const ram = row.ram_mb >= 1024 && row.ram_mb % 1024 === 0 ? `${row.ram_mb / 1024}G` : `${row.ram_mb}M`
  return `${row.vcpu}C${ram}`
}

let rowKey = 0

function rowFrom(seed: Partial<PlanRecord>, cycles: string[]): Row {
  const prices: Record<string, string> = {}
  for (const cycle of cycles) {
    const price = seed.prices?.find(item => item.billing_cycle === cycle)
    if (price) prices[cycle] = (price.amount_minor / 100).toFixed(2)
  }
  return {
    key: ++rowKey,
    name: '',
    code: '',
    vcpu: seed.vcpu || 1,
    ram_mb: seed.ram_mb || 512,
    disk_gb: seed.disk_gb || 10,
    traffic_gb: seed.traffic_gb ?? 1024,
    down: seed.network_down_mbps ?? 100,
    up: seed.network_up_mbps ?? 100,
    stock: '',
    prices,
  }
}

// nodeFits reports whether a platform plan of this provider and
// virtualization may be placed on node.
export function nodeFits(node: NodeRecord, providerType: string, virtualization: string) {
  return !node.owner_account_id && !node.retired_at && node.provider_type === providerType && node.virtualization_types.includes(virtualization)
}

export function NodePicker({
  nodes,
  providerType,
  virtualization,
  selection,
  onSelection,
  ids,
  onIds,
}: {
  nodes: NodeRecord[] | null
  providerType: string
  virtualization: string
  selection: NodeSelection
  onSelection: (value: NodeSelection) => void
  ids: string[]
  onIds: (value: string[]) => void
}) {
  const eligible = (nodes ?? []).filter(node => nodeFits(node, providerType, virtualization))
  return (
    <fieldset className="wide node-picker">
      <legend>{t('节点选择方式')}</legend>
      <div className="node-picker-modes">
        {(Object.keys(selectionLabels) as NodeSelection[]).map(mode => (
          <label key={mode} className={selection === mode ? 'node-mode selected' : 'node-mode'}>
            <input type="radio" name="node_selection" value={mode} checked={selection === mode} onChange={() => onSelection(mode)} />
            <span>
              <strong>{selectionLabels[mode]}</strong>
              <small>{selectionHints[mode]}</small>
            </span>
          </label>
        ))}
      </div>
      {selection === 'nodes' &&
        (nodes === null ? (
          <div className="template-empty">{t('无法读取节点列表（需要「节点查看」权限）。')}</div>
        ) : eligible.length ? (
          <div className="template-picker">
            {eligible.map(node => (
              <label key={node.id} className={ids.includes(node.id) ? 'template-option selected' : 'template-option'}>
                <input
                  type="checkbox"
                  name="node_ids"
                  value={node.id}
                  checked={ids.includes(node.id)}
                  onChange={event => onIds(event.target.checked ? [...ids, node.id] : ids.filter(id => id !== node.id))}
                />
                <span>
                  <strong>{node.name}</strong>
                  <small>
                    {t('{0} · {1} · 可售 {2} 核 / {3} MB / {4} GB', node.region_name, node.status === 'online' ? t('在线') : t('离线'), node.capacity_vcpu, node.capacity_ram_mb, node.capacity_disk_gb)}
                  </small>
                  {node.health_hold_reason && <em>{t('健康检查暂停开通：{0}', tr(node.health_hold_reason))}</em>}
                </span>
              </label>
            ))}
          </div>
        ) : (
          <div className="template-empty">
            {t('暂无可指定的 {0} · {1} 平台节点。', providerType.toUpperCase(), virtualization.toUpperCase())}
          </div>
        ))}
    </fieldset>
  )
}

export function PlanForm({
  plan,
  seed,
  batch,
  categories,
  presets,
  nodes,
  onPresetsChanged,
  onClose,
  onSaved,
}: {
  plan: PlanRecord | null
  seed?: PlanSeed | null
  batch?: boolean
  categories: PlanCategoryRecord[]
  presets: PlanPresetRecord[]
  nodes: NodeRecord[] | null
  onPresetsChanged: () => void
  onClose: () => void
  onSaved: () => void
}) {
  const formRef = useRef<HTMLFormElement>(null)
  const [init, setInit] = useState<PlanSeed>(() => plan ?? seed ?? {})
  const [formKey, setFormKey] = useState(0)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')
  const [saving, setSaving] = useState(false)
  const [templates, setTemplates] = useState<AvailableTemplateRecord[]>([])
  const [loadingTemplates, setLoadingTemplates] = useState(true)

  const [providers, setProviders] = useState<ProviderTypeRecord[]>(() => cached<ProviderTypeRecord[]>('/api/v1/admin/provider-types') ?? [])
  const [providerType, setProviderType] = useState(init.provider_type || 'hatch')
  const [virtualization, setVirtualization] = useState<Virtualization>(init.virtualization || 'lxc')
  const [allowed, setAllowed] = useState<string[]>(init.allowed_template_ids || [])
  const [capacity, setCapacity] = useState<StockCapacityRecord | null>(null)
  const [defaultTemplate, setDefaultTemplate] = useState(init.default_template_id || '')
  // New plans default to listed nodes; saved plans keep their way.
  const [selection, setSelection] = useState<NodeSelection>(init.node_selection || (plan ? 'pack' : 'nodes'))
  const [nodeIds, setNodeIds] = useState<string[]>(init.node_ids || [])
  const [tags, setTags] = useState<string[]>(init.tags || [])
  const [presetID, setPresetID] = useState('')

  // Batch mode: one row per plan, the cycles they sell and the formula.
  const initialCycles = () => {
    const fromSeed = (init.cycles ?? init.prices?.map(price => price.billing_cycle) ?? []).filter(cycle => cycleMonths[cycle])
    return fromSeed.length ? [...new Set(fromSeed)] : ['monthly']
  }
  const [cycles, setCycles] = useState<string[]>(initialCycles)
  const [rows, setRows] = useState<Row[]>(() => [rowFrom(init, initialCycles())])
  const [formula, setFormula] = useState<PriceFormula>(init.formula ?? defaultFormula)
  const [codePrefix, setCodePrefix] = useState('')

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types').then(setProviders).catch(err => setError(err.message))
  }, [])

  useEffect(() => {
    api<AvailableTemplateRecord[]>('/api/v1/admin/templates')
      .then(setTemplates)
      .catch(err => setError(err.message))
      .finally(() => setLoadingTemplates(false))
  }, [])

  // Nodes that no longer fit the provider or virtualization drop out.
  useEffect(() => {
    if (!nodes) return
    setNodeIds(ids => ids.filter(id => nodes.some(node => node.id === id && nodeFits(node, providerType, virtualization))))
  }, [nodes, providerType, virtualization])

  const descriptor = providers.find(item => item.type === providerType)
  const virtualizations = (descriptor?.virtualization_types ?? [virtualization]) as Virtualization[]
  const visibleTemplates = templates.filter(item => item.provider_type === providerType && item.virtualization === virtualization)

  const templateLabel = (id: string) => {
    const item = templates.find(candidate => candidate.id === id)
    return item ? imageLabel(item) : id
  }

  function toggleTemplate(id: string, checked: boolean) {
    const next = checked ? [...new Set([...allowed, id])] : allowed.filter(item => item !== id)
    setAllowed(next)
    if (!next.includes(defaultTemplate)) setDefaultTemplate(next[0] || '')
  }

  // shared reads the settings every plan of the form gets.
  function shared(data: FormData) {
    return {
      provider_type: providerType,
      virtualization,
      category_id: String(data.get('category_id') || ''),
      sort_order: Number(data.get('sort_order') || 0),
      description: String(data.get('description') || '').trim(),
      tags,
      node_selection: selection,
      node_ids: selection === 'nodes' ? nodeIds : [],
      snapshot_limit: Number(data.get('snapshot_limit')),
      assign_nat: data.get('assign_nat') === 'on',
      port_mapping_count: Number(data.get('port_mapping_count')),
      assign_ipv4: data.get('assign_ipv4') === 'on',
      ipv4_count: Number(data.get('ipv4_count')),
      assign_ipv6: data.get('assign_ipv6') === 'on',
      ipv6_count: Number(data.get('ipv6_count')),
      default_template_id: defaultTemplate,
      allowed_template_ids: allowed,
      ...readDiskIO(data),
    }
  }

  function checkShared() {
    if (!allowed.length || !defaultTemplate) return t('请至少勾选一个可用操作系统模板并指定默认模板')
    if (selection === 'nodes' && !nodeIds.length) return t('「指定节点」方式需至少勾选一个节点')
    return ''
  }

  // singlePrices reads the price fields of the single-plan form.
  function singlePrices(data: FormData): { prices: Price[]; error?: string } {
    const { prices, limits, error: priceError } = readCyclePrices(data)
    return {
      error: priceError,
      prices: Object.entries(prices).map(([billing_cycle, amount_minor]) => ({
        currency: ledgerCurrency(),
        billing_cycle,
        amount_minor,
        setup_fee_minor: 0,
        purchase_limit: limits[billing_cycle] ?? null,
      })),
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const sharedError = checkShared()
    if (sharedError) {
      setError(sharedError)
      return
    }
    const data = new FormData(event.currentTarget)
    let request: { path: string; method: string; body: unknown }
    if (batch) {
      const built = batchPlans(data)
      if (typeof built === 'string') {
        setError(built)
        return
      }
      request = { path: '/api/v1/admin/plans/batch', method: 'POST', body: { plans: built } }
    } else {
      const { prices, error: priceError } = singlePrices(data)
      if (priceError) {
        setError(priceError)
        return
      }
      request = {
        path: plan ? `/api/v1/admin/plans/${plan.id}` : '/api/v1/admin/plans',
        method: plan ? 'PUT' : 'POST',
        body: {
          ...shared(data),
          code: data.get('code'),
          name: data.get('name'),
          vcpu: Number(data.get('vcpu')),
          ram_mb: Number(data.get('ram_mb')),
          disk_gb: Number(data.get('disk_gb')),
          traffic_gb: Number(data.get('traffic_gb')),
          network_down_mbps: Number(data.get('network_down_mbps')),
          network_up_mbps: Number(data.get('network_up_mbps')),
          enabled: plan?.enabled ?? true,
          prices,
          stock_limit: readStock(data),
        },
      }
    }
    setSaving(true)
    setError('')
    try {
      await api(request.path, { method: request.method, body: JSON.stringify(request.body) })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setSaving(false)
    }
  }

  // batchPlans builds one plan per row, or returns why it cannot.
  function batchPlans(data: FormData) {
    if (!rows.length) return t('请至少添加一行套餐')
    if (!cycles.length) return t('请至少选择一个计费周期')
    const base = shared(data)
    const plans = []
    for (const [index, row] of rows.entries()) {
      const label = t('第 {0} 行', index + 1)
      if (!row.name.trim() || !row.code.trim()) return t('{0}：请填写名称和编码', label)
      if (row.vcpu < 1 || row.ram_mb < 64 || row.disk_gb < 1) return t('{0}：CPU 至少 1 核、内存至少 64 MB、磁盘至少 1 GB', label)
      const prices = cycles
        .filter(cycle => Number(row.prices[cycle] || 0) > 0)
        .map(cycle => ({ currency: ledgerCurrency(), billing_cycle: cycle, amount_minor: Math.round(Number(row.prices[cycle]) * 100), setup_fee_minor: 0, purchase_limit: null }))
      if (!prices.length) return t('{0}：至少填写一个价格', label)
      const stock = row.stock.trim()
      if (stock !== '' && !(Number.isInteger(Number(stock)) && Number(stock) >= 0)) return t('{0}：库存需为非负整数，留空不限', label)
      plans.push({
        ...base,
        // Rows keep their order after the number typed.
        sort_order: base.sort_order + index,
        code: row.code.trim(),
        name: row.name.trim(),
        vcpu: row.vcpu,
        ram_mb: row.ram_mb,
        disk_gb: row.disk_gb,
        traffic_gb: row.traffic_gb,
        network_down_mbps: row.down,
        network_up_mbps: row.up,
        enabled: true,
        prices,
        stock_limit: stock === '' ? null : Number(stock),
      })
    }
    return plans
  }

  // currentSeed is what the form holds now, as a seed.
  function currentSeed(): PlanSeed {
    const form = formRef.current
    if (!form) return init
    const data = new FormData(form)
    const values: PlanSeed = { ...init, ...shared(data), cycles, formula }
    if (batch) {
      const first = rows[0]
      if (first) Object.assign(values, { traffic_gb: first.traffic_gb, network_down_mbps: first.down, network_up_mbps: first.up })
    } else {
      Object.assign(values, {
        code: String(data.get('code') ?? ''),
        name: String(data.get('name') ?? ''),
        vcpu: Number(data.get('vcpu')),
        ram_mb: Number(data.get('ram_mb')),
        disk_gb: Number(data.get('disk_gb')),
        traffic_gb: Number(data.get('traffic_gb')),
        network_down_mbps: Number(data.get('network_down_mbps')),
        network_up_mbps: Number(data.get('network_up_mbps')),
        prices: singlePrices(data).prices,
        stock_limit: readStock(data),
      })
    }
    return values
  }

  function applyPreset() {
    const preset = presets.find(item => item.id === presetID)
    if (!preset) return
    const settings = preset.settings as PlanSeed
    const picked: PlanSeed = {}
    for (const key of sharedKeys) if (settings[key] !== undefined) (picked as Record<string, unknown>)[key] = settings[key]
    // The size, name and prices typed so far stay.
    const next = { ...currentSeed(), ...picked }
    setInit(next)
    setProviderType(next.provider_type || 'hatch')
    setVirtualization(next.virtualization || 'lxc')
    setAllowed(next.allowed_template_ids || [])
    setDefaultTemplate(next.default_template_id || '')
    setSelection(next.node_selection || 'nodes')
    setNodeIds(next.node_ids || [])
    setTags(next.tags || [])
    if (settings.formula) setFormula({ ...defaultFormula, ...settings.formula, discounts: { ...defaultFormula.discounts, ...settings.formula.discounts } })
    if (settings.cycles?.length) setCycles(settings.cycles.filter(cycle => cycleMonths[cycle]))
    if (batch && settings.traffic_gb !== undefined) {
      setRows(current => current.map(row => ({ ...row, traffic_gb: settings.traffic_gb ?? row.traffic_gb, down: settings.network_down_mbps ?? row.down, up: settings.network_up_mbps ?? row.up })))
    }
    setFormKey(key => key + 1)
    setError('')
    setNotice(t('已套用模板「{0}」，请检查后保存。', preset.name))
  }

  async function savePreset() {
    const current = presets.find(item => item.id === presetID)
    const name = await promptDialog({
      title: t('存为套餐模板'),
      message: t('保存当前表单里的公共设置（对接方式、虚拟化、分类、节点、网络、镜像、磁盘读写、流量带宽默认值），以及批量创建用的计费周期和单价公式；不含名称、编码、配置和价格。'),
      label: t('模板名称'),
      defaultValue: current?.name ?? '',
      placeholder: t('如：香港 NAT 系列'),
      required: true,
      validate: value => (value.trim().length > 60 ? t('最多 60 个字') : ''),
    })
    if (!name) return
    const seedValues = currentSeed()
    const settings: Record<string, unknown> = { formula: seedValues.formula, cycles: seedValues.cycles }
    for (const key of sharedKeys) settings[key] = seedValues[key]
    const existing = presets.find(item => item.name.trim().toLowerCase() === name.trim().toLowerCase())
    if (existing && !(await confirmDialog({ title: t('覆盖模板「{0}」？', existing.name), message: t('同名模板的内容会被当前设置替换。'), confirmText: t('覆盖') }))) return
    try {
      const saved = await api<PlanPresetRecord>(existing ? `/api/v1/admin/plan-presets/${existing.id}` : '/api/v1/admin/plan-presets', {
        method: existing ? 'PUT' : 'POST',
        body: JSON.stringify({ name: name.trim(), settings }),
      })
      setPresetID(saved.id)
      setNotice(t('模板「{0}」已保存。', saved.name))
      onPresetsChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存模板失败'))
    }
  }

  async function deletePreset() {
    const preset = presets.find(item => item.id === presetID)
    if (!preset || !(await confirmDialog({ title: t('删除模板「{0}」？', preset.name), message: t('只删除模板本身，已创建的套餐不受影响。'), confirmText: t('删除'), danger: true }))) return
    try {
      await api(`/api/v1/admin/plan-presets/${preset.id}`, { method: 'DELETE' })
      setPresetID('')
      onPresetsChanged()
      toast('success', t('模板「{0}」已删除', preset.name))
    } catch (err) {
      toast('error', t('删除模板失败'), err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : t('删除模板失败'))
    }
  }

  const updateRow = (key: number, patch: Partial<Row>) => setRows(current => current.map(row => (row.key === key ? { ...row, ...patch } : row)))
  const updateRowPrice = (key: number, cycle: string, value: string) =>
    setRows(current => current.map(row => (row.key === key ? { ...row, prices: { ...row.prices, [cycle]: value } } : row)))

  function addRow() {
    setRows(current => {
      const last = current[current.length - 1]
      if (!last) return [rowFrom(init, cycles)]
      // The next tier doubles the last one.
      const next: Row = { ...last, key: ++rowKey, name: '', code: '', vcpu: last.vcpu * 2, ram_mb: last.ram_mb * 2, disk_gb: last.disk_gb * 2, stock: '', prices: {} }
      return [...current, next]
    })
  }

  function fillPrices() {
    setRows(current =>
      current.map(row => {
        const prices = { ...row.prices }
        for (const cycle of cycles) prices[cycle] = formulaPrice(formula, row, cycle)
        return { ...row, prices }
      })
    )
  }

  function fillCodes() {
    const prefix = codePrefix.trim().toUpperCase()
    setRows(current =>
      current.map(row => ({
        ...row,
        code: row.code.trim() || (prefix ? `${prefix}-${sizeCode(row)}` : sizeCode(row)),
        name: row.name.trim() || `${codePrefix.trim() ? codePrefix.trim() + ' ' : ''}${sizeCode(row)}`,
      }))
    )
  }

  const setFormulaField = (field: keyof Omit<PriceFormula, 'discounts'>, value: string) => setFormula(current => ({ ...current, [field]: Number(value) || 0 }))

  const title = batch ? t('批量创建套餐') : plan ? t('编辑 VPS 商品套餐') : seed?.code ? t('复制套餐') : t('创建 VPS 商品套餐')
  const intro = batch
    ? t('上面的公共设置对每一行都生效；表格里每行是一个套餐。全部校验通过后一起保存，有一行不通过就都不保存。')
    : t('套餐绑定一种对接方式，只会调度到该方式的节点；可用模板从这些在线节点读取，网络分配策略统一下发给实例。')

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{title}</h3>
          <p>{intro}</p>
        </div>
        <button className="icon-button" onClick={onClose} aria-label={t('关闭')}><X size={18} /></button>
      </div>

      <div className="preset-bar">
        <label>
          <span>{t('套餐模板')}</span>
          <select value={presetID} onChange={event => setPresetID(event.target.value)}>
            <option value="">{presets.length ? t('选择模板…') : t('暂无模板')}</option>
            {presets.map(preset => <option key={preset.id} value={preset.id}>{preset.name}</option>)}
          </select>
        </label>
        <button type="button" className="secondary-button compact" disabled={!presetID} onClick={applyPreset}>{t('套用模板')}</button>
        <button type="button" className="secondary-button compact" onClick={savePreset}>{t('存为模板')}</button>
        {presetID && (
          <button type="button" className="icon-button" title={t('删除模板')} aria-label={t('删除模板')} onClick={deletePreset}><Trash2 size={16} /></button>
        )}
      </div>
      {notice && <div className="form-notice">{notice}</div>}

      <form key={formKey} ref={formRef} className="form-grid" onSubmit={submit}>
        {!batch && (
          <>
            <label>
              <span>{t('套餐唯一编码')}</span>
              <input name="code" required placeholder="LXC-START" defaultValue={init.code} />
            </label>
            <label>
              <span>{t('套餐展示名称')}</span>
              <input name="name" required placeholder={t('轻量入门型')} defaultValue={init.name} />
            </label>
          </>
        )}
        <label>
          <span>{t('商品分类')}</span>
          <select name="category_id" defaultValue={init.category_id ?? ''}>
            <option value="">{t('未分类')}</option>
            {categories.map(category => <option key={category.id} value={category.id}>{category.name}</option>)}
          </select>
        </label>
        <label>
          <span>{t('排序{0}', batch ? t('（起始值，逐行加 1）') : '')}</span>
          <input name="sort_order" type="number" min="-9999" max="9999" step="1" defaultValue={init.sort_order ?? 0} title={t('数字小的排在前面，相同时新建的在前')} />
          <small className="field-hint">{t('数字小的排在前面')}</small>
        </label>
        <label>
          <span>{t('对接方式')}</span>
          <select
            name="provider_type"
            value={providerType}
            disabled={!!plan}
            onChange={event => {
              const next = providers.find(item => item.type === event.target.value)
              setProviderType(event.target.value)
              setVirtualization((next?.virtualization_types[0] ?? 'lxc') as Virtualization)
              setAllowed([])
              setDefaultTemplate('')
            }}
          >
            {providers.map(item => <option key={item.type} value={item.type}>{item.name}</option>)}
          </select>
        </label>
        <label>
          <span>{t('底层虚拟化')}</span>
          <select
            name="virtualization"
            value={virtualization}
            onChange={event => {
              setVirtualization(event.target.value as Virtualization)
              setAllowed([])
              setDefaultTemplate('')
            }}
          >
            {virtualizations.map(kind => <option key={kind} value={kind}>{virtualizationLabel(kind)}</option>)}
          </select>
        </label>
        {!batch && (
          <>
            <label>
              <span>{t('vCPU 核心')}</span>
              <input name="vcpu" type="number" min="1" defaultValue={init.vcpu || 1} required />
            </label>
            <label>
              <span>{t('内存容量 MB')}</span>
              <input name="ram_mb" type="number" min="64" defaultValue={init.ram_mb || 512} required />
            </label>
            <label>
              <span>{t('磁盘空间 GB')}</span>
              <input name="disk_gb" type="number" min="1" defaultValue={init.disk_gb || 10} required />
            </label>
            <label>
              <span>{t('月度流量 GB')}</span>
              <input name="traffic_gb" type="number" min="0" defaultValue={init.traffic_gb ?? 1024} />
            </label>
            <label>
              <span>{t('下行带宽 Mbps')}</span>
              <input name="network_down_mbps" type="number" min="0" defaultValue={init.network_down_mbps ?? 100} />
            </label>
            <label>
              <span>{t('上行带宽 Mbps')}</span>
              <input name="network_up_mbps" type="number" min="0" defaultValue={init.network_up_mbps ?? 100} />
            </label>
          </>
        )}
        <label>
          <span>{t('快照配额')}</span>
          <input name="snapshot_limit" type="number" min="0" defaultValue={init.snapshot_limit ?? 1} />
        </label>

        <label className="wide">
          <span>{t('套餐描述{0}（显示在「选购 VPS」的套餐卡片上，可留空）', batch ? t('（每个套餐都用这段）') : '')}</span>
          <textarea name="description" rows={2} maxLength={500} placeholder={t('如：CN2 GIA 回程，晚高峰稳定，适合建站和代理')} defaultValue={init.description ?? ''} />
        </label>
        <label className="wide">
          <span>{t('标签（显示在套餐卡片上）')}</span>
          <TagInput value={tags} onChange={setTags} placeholder={t('如：CN2 GIA、原生 IP、解锁流媒体')} />
        </label>

        <NodePicker
          nodes={nodes}
          providerType={providerType}
          virtualization={virtualization}
          selection={selection}
          onSelection={setSelection}
          ids={nodeIds}
          onIds={setNodeIds}
        />

        {!batch && (
          <>
            <CyclePriceFields prices={(init.prices ?? (plan ? [] : [{ billing_cycle: 'monthly', amount_minor: 1900 }])).map(price => (plan ? price : { ...price, sold: 0 }))} />
            <StockField
              plan={plan ?? undefined}
              preview={form => {
                const data = new FormData(form)
                const listed = data.get('node_selection') === 'nodes'
                return api<StockCapacityRecord>('/api/v1/admin/plans/stock-capacity', {
                  method: 'POST',
                  body: JSON.stringify({
                    id: plan?.id ?? '',
                    provider_type: data.get('provider_type') || providerType,
                    virtualization: data.get('virtualization') || virtualization,
                    vcpu: Number(data.get('vcpu')),
                    ram_mb: Number(data.get('ram_mb')),
                    disk_gb: Number(data.get('disk_gb')),
                    traffic_gb: Number(data.get('traffic_gb')),
                    node_selection: listed ? 'nodes' : 'pack',
                    node_ids: listed ? data.getAll('node_ids') : [],
                  }),
                })
              }}
              onCapacity={setCapacity}
            />
          </>
        )}
        {providerType === 'hatch' && <DiskIOFields plan={plan ?? (init.disk_read_mbps !== undefined ? init : undefined)} capacity={capacity} />}

        <fieldset className="wide network-policy">
          <legend>{t('网络策略配置')}</legend>
          <label className="checkbox">
            <input name="assign_nat" type="checkbox" defaultChecked={init.assign_nat ?? true} />
            {t('分配 NAT 共享 IPv4')}
          </label>
          <label>
            <span>{t('NAT 端口映射配额')}</span>
            <input name="port_mapping_count" type="number" min="0" max="64" defaultValue={init.port_mapping_count ?? 0} />
          </label>
          <label className="checkbox">
            <input name="assign_ipv4" type="checkbox" defaultChecked={init.assign_ipv4 ?? false} />
            {t('分配独立公网 IPv4')}
          </label>
          <label>
            <span>{t('公网 IPv4 数量')}</span>
            <input name="ipv4_count" type="number" min="1" max="64" defaultValue={init.ipv4_count ?? 1} />
          </label>
          <label className="checkbox">
            <input name="assign_ipv6" type="checkbox" defaultChecked={init.assign_ipv6 ?? true} />
            {t('分配独立 IPv6')}
          </label>
          <label>
            <span>{t('独立 IPv6 数量')}</span>
            <input name="ipv6_count" type="number" min="1" max="64" defaultValue={init.ipv6_count ?? 1} />
          </label>
        </fieldset>

        <fieldset className="wide">
          <legend>{t('允许客户选择的系统镜像（动态读取自节点就绪镜像）')}</legend>
          {loadingTemplates ? (
            <div className="template-empty">{t('正在向在线节点检索可用系统镜像…')}</div>
          ) : (
            <div className="template-picker">
              {visibleTemplates.map(item => (
                <label className={allowed.includes(item.id) ? 'template-option selected' : 'template-option'} key={`${item.virtualization}:${item.id}`}>
                  <input type="checkbox" checked={allowed.includes(item.id)} onChange={event => toggleTemplate(item.id, event.target.checked)} />
                  <span>
                    <strong>{templateLabel(item.id)}</strong>
                    <small>{item.description || item.id}</small>
                    <em>{t('部署节点：{0}', item.node_names.join(t('、')))}</em>
                  </span>
                </label>
              ))}
              {!visibleTemplates.length && (
                <div className="template-empty">{t('在线集群中暂无已启用且已就绪的 {0} 系统镜像。', virtualization.toUpperCase())}</div>
              )}
              {allowed
                .filter(id => !visibleTemplates.some(item => item.id === id))
                .map(id => (
                  <label className="template-option unavailable" key={id}>
                    <input type="checkbox" checked onChange={event => toggleTemplate(id, event.target.checked)} />
                    <span>
                      <strong>{id}</strong>
                      <small>{t('该镜像当前节点未上报，保留后仍可保存供历史实例使用。')}</small>
                    </span>
                  </label>
                ))}
            </div>
          )}
        </fieldset>

        <label className="wide">
          <span>{t('默认系统镜像')}</span>
          <select name="default_template_id" value={defaultTemplate} onChange={event => setDefaultTemplate(event.target.value)} required>
            <option value="">{t('请先在上方勾选镜像')}</option>
            {allowed.map(id => (
              <option key={id} value={id}>
                {templateLabel(id)}
              </option>
            ))}
          </select>
        </label>

        {batch && (
          <>
            <fieldset className="wide price-formula">
              <legend>{t('按单价计算价格（可选）')}</legend>
              <p className="field-hint">{t('月付价 = 基础费 + 每核 × 核数 + 每 GB 内存 × 内存 + 每 10 GB 磁盘 × 磁盘 + 每 100 GB 流量 × 流量 + 每 10 Mbps × 下行带宽；其他周期 = 月付 × 月数 × (1 − 折扣)。算出后仍可在表格里逐个改。')}</p>
              <div className="price-formula-grid">
                {(
                  [
                    ['base', t('基础费 {0}/月', ledgerUnit())],
                    ['vcpu', t('每核 {0}', ledgerUnit())],
                    ['ram_gb', t('每 GB 内存 {0}', ledgerUnit())],
                    ['disk_10gb', t('每 10 GB 磁盘 {0}', ledgerUnit())],
                    ['traffic_100gb', t('每 100 GB 流量 {0}', ledgerUnit())],
                    ['mbps_10', t('每 10 Mbps {0}', ledgerUnit())],
                  ] as const
                ).map(([field, label]) => (
                  <label key={field}>
                    <span>{label}</span>
                    <input type="number" min="0" step="0.01" value={formula[field] || ''} placeholder="0" onChange={event => setFormulaField(field, event.target.value)} />
                  </label>
                ))}
                {namedCycles
                  .filter(cycle => cycle !== 'monthly')
                  .map(cycle => (
                    <label key={cycle}>
                      <span>{t('{0}折扣 %', cycleName(cycle))}</span>
                      <input
                        type="number"
                        min="0"
                        max="90"
                        step="1"
                        value={formula.discounts[cycle] ?? 0}
                        onChange={event => setFormula(current => ({ ...current, discounts: { ...current.discounts, [cycle]: Number(event.target.value) || 0 } }))}
                      />
                    </label>
                  ))}
                <label>
                  <span>{t('价格取整')}</span>
                  <select value={formula.step} onChange={event => setFormulaField('step', event.target.value)}>
                    <option value={0.01}>{t('到分')}</option>
                    <option value={0.1}>{t('到角')}</option>
                    <option value={1}>{t('到整数')}</option>
                  </select>
                </label>
              </div>
              <div className="price-formula-actions">
                <button type="button" className="secondary-button compact" onClick={fillPrices}>{t('按单价填入所有行的价格')}</button>
              </div>
            </fieldset>

            <fieldset className="wide plan-series">
              <legend>{t('套餐系列')}</legend>
              <div className="plan-series-tools">
                <div className="plan-series-cycles">
                  <span>{t('出售周期')}</span>
                  {namedCycles.map(cycle => (
                    <label key={cycle} className="checkbox">
                      <input
                        type="checkbox"
                        checked={cycles.includes(cycle)}
                        onChange={event => setCycles(current => (event.target.checked ? namedCycles.filter(item => item === cycle || current.includes(item)) : current.filter(item => item !== cycle)))}
                      />
                      {cycleName(cycle)}
                    </label>
                  ))}
                </div>
                <div className="plan-series-codes">
                  <input value={codePrefix} placeholder={t('名称/编码前缀，如 HK-NAT')} onChange={event => setCodePrefix(event.target.value)} />
                  <button type="button" className="secondary-button compact" onClick={fillCodes}>{t('补全空白的名称和编码')}</button>
                </div>
              </div>
              <div className="plan-series-table-wrap">
                <table className="plan-series-table">
                  <thead>
                    <tr>
                      <th>{t('名称')}</th>
                      <th>{t('编码')}</th>
                      <th>vCPU</th>
                      <th>{t('内存 MB')}</th>
                      <th>{t('磁盘 GB')}</th>
                      <th>{t('流量 GB')}</th>
                      <th>{t('下行 Mbps')}</th>
                      <th>{t('上行 Mbps')}</th>
                      {cycles.map(cycle => <th key={cycle}>{t('{0}（{1}）', cycleName(cycle), ledgerUnit())}</th>)}
                      <th>{t('库存')}</th>
                      <th aria-label={t('操作')} />
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map(row => (
                      <tr key={row.key}>
                        <td><input value={row.name} placeholder={t('名称')} onChange={event => updateRow(row.key, { name: event.target.value })} /></td>
                        <td><input value={row.code} placeholder={t('编码')} onChange={event => updateRow(row.key, { code: event.target.value })} /></td>
                        {(['vcpu', 'ram_mb', 'disk_gb', 'traffic_gb', 'down', 'up'] as const).map(field => (
                          <td key={field}>
                            <input type="number" min="0" value={row[field]} onChange={event => updateRow(row.key, { [field]: Number(event.target.value) || 0 })} />
                          </td>
                        ))}
                        {cycles.map(cycle => (
                          <td key={cycle}>
                            <input type="number" min="0" step="0.01" placeholder={t('不售')} value={row.prices[cycle] ?? ''} onChange={event => updateRowPrice(row.key, cycle, event.target.value)} />
                          </td>
                        ))}
                        <td><input type="number" min="0" step="1" placeholder={t('不限')} value={row.stock} onChange={event => updateRow(row.key, { stock: event.target.value })} /></td>
                        <td>
                          <button type="button" className="icon-button" aria-label={t('删除这一行')} disabled={rows.length === 1} onClick={() => setRows(current => current.filter(item => item.key !== row.key))}>
                            <Trash2 size={15} />
                          </button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
              <button type="button" className="secondary-button compact" onClick={addRow}>
                <Plus size={15} />
                {t('添加一行（配置翻倍）')}
              </button>
            </fieldset>
          </>
        )}

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button" disabled={saving}>
            {saving ? t('正在保存…') : batch ? t('创建 {0} 个套餐', rows.length) : plan ? t('保存修改') : t('创建商品套餐')}
          </button>
        </div>
      </form>
    </div>
  )
}
