import { FormEvent, useEffect, useRef, useState } from 'react'
import { Copy, FolderPlus, Layers, ListChecks, Pencil, Trash2, X } from 'lucide-react'
import { api, cached, NodeRecord, PlanCategoryRecord, PlanPresetRecord, PlanRecord } from '../api'
import { CouponManager } from '../Coupons'
import { PageActions, StatusBadge, bandwidthLabel, cycleLabel } from '../shared/ui'
import { cycleOrder } from '../shared/cycles'
import { StockTag } from '../shared/stock'
import { diskIOText } from '../shared/diskio'
import { confirmDialog } from '../shared/dialog'
import { placementText, PlanForm, PlanSeed } from './PlanForm'
import { PlanBatchEdit } from './PlanBatchEdit'
import { toast } from '../shared/toast'

type FormState = { mode: 'single' | 'batch'; plan: PlanRecord | null; seed: PlanSeed | null; key: number }

// copySeed starts a new plan from a saved one: same settings, a new code,
// no stock and nothing sold yet.
function copySeed(plan: PlanRecord): PlanSeed {
  const { id: _id, stock_held: _held, version: _version, region_ids: _regions, ...rest } = plan
  return {
    ...rest,
    name: `${plan.name}（副本）`,
    code: `${plan.code}-COPY`,
    stock_limit: null,
    enabled: true,
    prices: plan.prices.map(price => ({ ...price, sold: 0 })),
  }
}

export function PlansView() {
  const [plans, setPlans] = useState<PlanRecord[]>(() => cached<PlanRecord[]>('/api/v1/admin/plans') ?? [])
  const [categories, setCategories] = useState<PlanCategoryRecord[]>(() => cached<PlanCategoryRecord[]>('/api/v1/admin/plan-categories') ?? [])
  const [presets, setPresets] = useState<PlanPresetRecord[]>([])
  // null when the admin may not read nodes; the node picker says so.
  const [nodes, setNodes] = useState<NodeRecord[] | null>(() => cached<NodeRecord[]>('/api/v1/admin/nodes') ?? [])
  const [form, setForm] = useState<FormState | null>(null)
  const [categoryForm, setCategoryForm] = useState<PlanCategoryRecord | 'new' | null>(null)
  const [selecting, setSelecting] = useState(false)
  const [selected, setSelected] = useState<string[]>([])
  const [batchEdit, setBatchEdit] = useState(false)
  const [error, setError] = useState('')
  const top = useRef<HTMLDivElement>(null)

  const load = () =>
    api<PlanRecord[]>('/api/v1/admin/plans')
      .then(setPlans)
      .catch(err => setError(err.message))
  const loadCategories = () =>
    api<PlanCategoryRecord[]>('/api/v1/admin/plan-categories')
      .then(setCategories)
      .catch(err => setError(err.message))
  const loadPresets = () =>
    api<PlanPresetRecord[]>('/api/v1/admin/plan-presets')
      .then(setPresets)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
    void loadCategories()
    void loadPresets()
    api<NodeRecord[]>('/api/v1/admin/nodes')
      .then(setNodes)
      .catch(() => setNodes(null))
  }, [])

  // Forms open above the list; bring them into view.
  const reveal = () => window.setTimeout(() => top.current?.scrollIntoView({ behavior: 'smooth', block: 'start' }), 0)
  const openForm = (mode: FormState['mode'], plan: PlanRecord | null = null, seed: PlanSeed | null = null) => {
    setBatchEdit(false)
    setCategoryForm(null)
    setForm(current => ({ mode, plan, seed, key: (current?.key ?? 0) + 1 }))
    reveal()
  }

  async function toggle(plan: PlanRecord) {
    try {
      await api(`/api/v1/admin/plans/${plan.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: !plan.enabled }),
      })
      load()
      toast('success', plan.enabled ? `套餐 ${plan.name} 已停售` : `套餐 ${plan.name} 已上架`)
    } catch (err) {
      toast('error', '更新套餐状态失败', err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : '更新状态失败')
    }
  }

  async function removeCategory(category: PlanCategoryRecord) {
    const confirmed = await confirmDialog({
      title: `删除分类「${category.name}」？`,
      message: category.plans ? `其下 ${category.plans} 个套餐会变成「未分类」，套餐本身和已售实例都不受影响。` : '该分类下没有套餐。',
      confirmText: '删除',
      danger: true,
    })
    if (!confirmed) return
    try {
      await api(`/api/v1/admin/plan-categories/${category.id}`, { method: 'DELETE' })
      await Promise.all([loadCategories(), load()])
      toast('success', `分类「${category.name}」已删除`)
    } catch (err) {
      toast('error', '删除分类失败', err instanceof Error ? err.message : undefined)
      setError(err instanceof Error ? err.message : '删除分类失败')
    }
  }

  const known = new Set(categories.map(category => category.id))
  const groups: { category: PlanCategoryRecord | null; plans: PlanRecord[] }[] = [
    ...categories.map(category => ({ category, plans: plans.filter(plan => plan.category_id === category.id) })),
    { category: null, plans: plans.filter(plan => !plan.category_id || !known.has(plan.category_id)) },
  ].filter(group => group.category || group.plans.length || !categories.length)

  const chosen = plans.filter(plan => selected.includes(plan.id))
  const toggleSelected = (id: string, checked: boolean) => setSelected(state => (checked ? [...new Set([...state, id])] : state.filter(item => item !== id)))
  const stopSelecting = () => {
    setSelecting(false)
    setSelected([])
    setBatchEdit(false)
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="PRODUCT CATALOG"
        title="商品套餐管理"
        description="套餐按分类展示给客户；网络策略与模板随套餐统一下发，套餐修改将自增版本号且不影响历史订单明细。"
        action={() => openForm('single')}
        actionLabel="创建新套餐"
      />

      <div className="plan-toolbar">
        <button className="secondary-button compact" onClick={() => openForm('batch')}>
          <Layers size={15} />
          批量创建
        </button>
        <button
          className="secondary-button compact"
          onClick={() => {
            setForm(null)
            setCategoryForm('new')
            reveal()
          }}
        >
          <FolderPlus size={15} />
          新建分类
        </button>
        <button className={selecting ? 'secondary-button compact active' : 'secondary-button compact'} onClick={() => (selecting ? stopSelecting() : setSelecting(true))}>
          <ListChecks size={15} />
          {selecting ? '退出批量修改' : '批量修改'}
        </button>
      </div>

      {error && <div className="form-error">{error}</div>}

      <div ref={top} />
      {categoryForm && (
        <CategoryForm
          category={categoryForm === 'new' ? null : categoryForm}
          onClose={() => setCategoryForm(null)}
          onSaved={() => {
            setCategoryForm(null)
            loadCategories()
          }}
        />
      )}

      {form && (
        <PlanForm
          key={form.key}
          plan={form.plan}
          seed={form.seed}
          batch={form.mode === 'batch'}
          categories={categories}
          presets={presets}
          nodes={nodes}
          onPresetsChanged={loadPresets}
          onClose={() => setForm(null)}
          onSaved={() => {
            setForm(null)
            load()
            loadCategories()
          }}
        />
      )}

      {selecting && (
        <div className="plan-select-bar">
          <span>已选 {selected.length} 个套餐</span>
          <button className="secondary-button compact" onClick={() => setSelected(plans.map(plan => plan.id))}>全选</button>
          <button className="secondary-button compact" disabled={!selected.length} onClick={() => setSelected([])}>清空</button>
          <button
            className="primary-button compact"
            disabled={!selected.length}
            onClick={() => {
              setForm(null)
              setBatchEdit(true)
              reveal()
            }}
          >
            修改所选套餐
          </button>
        </div>
      )}

      {batchEdit && chosen.length > 0 && (
        <PlanBatchEdit
          plans={chosen}
          categories={categories}
          nodes={nodes}
          onClose={() => setBatchEdit(false)}
          onSaved={() => {
            stopSelecting()
            load()
            loadCategories()
          }}
        />
      )}

      {groups.map(({ category, plans: items }) => (
        <section className="plan-group" key={category?.id ?? 'none'}>
          {(category || categories.length > 0) && (
            <div className="plan-group-head">
              <div>
                <h3>
                  {category ? category.name : '未分类'}
                  <span className="count">{items.length}</span>
                </h3>
                {category?.description && <p>{category.description}</p>}
                {!category && <p>不属于任何分类的套餐，客户在商店的「其他」里看到它们。</p>}
              </div>
              <div className="plan-group-actions">
                {selecting && items.length > 0 && (
                  <button className="secondary-button compact" onClick={() => setSelected(state => [...new Set([...state, ...items.map(plan => plan.id)])])}>
                    选中本组
                  </button>
                )}
                {category && (
                  <>
                    <button
                      className="icon-button"
                      title="编辑分类"
                      aria-label="编辑分类"
                      onClick={() => {
                        setForm(null)
                        setCategoryForm(category)
                        reveal()
                      }}
                    >
                      <Pencil size={15} />
                    </button>
                    <button className="icon-button" title="删除分类" aria-label="删除分类" onClick={() => removeCategory(category)}>
                      <Trash2 size={15} />
                    </button>
                  </>
                )}
              </div>
            </div>
          )}
          <div className="plan-grid">
            {items.map(plan => (
              <article className={['plan-card', !plan.enabled && 'disabled', selected.includes(plan.id) && 'selected'].filter(Boolean).join(' ')} key={plan.id}>
                <div className="plan-card-top">
                  <span className="tag">{plan.provider_type.toUpperCase()} · {plan.virtualization.toUpperCase()}</span>
                  {selecting ? (
                    <label className="plan-card-check">
                      <input type="checkbox" checked={selected.includes(plan.id)} onChange={event => toggleSelected(plan.id, event.target.checked)} />
                      选择
                    </label>
                  ) : (
                    <StatusBadge status={plan.enabled ? 'online' : 'disabled'} />
                  )}
                </div>
                <h3>{plan.name}</h3>
                <small>
                  {plan.code} · 版本 v{plan.version}
                </small>

                <div className="spec-line">
                  <strong>{plan.vcpu}</strong> vCPU
                  <strong>{plan.ram_mb}</strong> MB
                  <strong>{plan.disk_gb}</strong> GB
                </div>

                <small>默认镜像：{plan.default_template_id}</small>
                <small className="plan-network">
                  网络：
                  {[plan.assign_nat && `NAT×${plan.port_mapping_count}`, plan.assign_ipv4 && `IPv4×${plan.ipv4_count}`, plan.assign_ipv6 && `IPv6×${plan.ipv6_count}`]
                    .filter(Boolean)
                    .join(' / ')}
                  {' · '}
                  {bandwidthLabel(plan.network_down_mbps)}
                </small>
                <small className="plan-network">节点：{placementText(plan, nodes)}</small>
                {diskIOText(plan) && <small className="plan-network">{diskIOText(plan)}</small>}

                <StockTag plan={plan} />
                <div className="price-line">
                  {plan.prices.length
                    ? [...plan.prices]
                        .sort((a, b) => cycleOrder(a.billing_cycle) - cycleOrder(b.billing_cycle))
                        .map(price => (
                          <span key={price.billing_cycle} className="price-chip">
                            <strong>¥{(price.amount_minor / 100).toFixed(2)}</strong>
                            <span>/ {cycleLabel(price.billing_cycle)}</span>
                          </span>
                        ))
                    : '暂无报价'}
                </div>

                <div className="plan-actions">
                  <button className="secondary-button" onClick={() => openForm('single', plan)}>
                    编辑
                  </button>
                  <button className="secondary-button" title="以此套餐为基础新建" onClick={() => openForm('single', null, copySeed(plan))}>
                    <Copy size={14} />
                    复制
                  </button>
                  <button className="secondary-button" onClick={() => toggle(plan)}>
                    {plan.enabled ? '下架' : '上架'}
                  </button>
                </div>
              </article>
            ))}
            {!items.length && (
              <div className="empty-card" style={{ gridColumn: '1 / -1' }}>
                {category ? (
                  <span>
                    该分类下暂无套餐。
                    <button className="link-button" onClick={() => openForm('single', null, { category_id: category.id })}>
                      在此分类新建套餐
                    </button>
                  </span>
                ) : (
                  '尚未创建任何商品套餐'
                )}
              </div>
            )}
          </div>
        </section>
      ))}

      <CouponManager
        endpoint="/api/v1/admin/coupons"
        plans={plans.map(plan => ({ id: plan.id, name: plan.name }))}
        intro="平台优惠码适用于上面的平台套餐；托管母机的套餐由机主在托管中心自行发放优惠码。"
      />
    </section>
  )
}

function CategoryForm({ category, onClose, onSaved }: { category: PlanCategoryRecord | null; onClose: () => void; onSaved: () => void }) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const data = new FormData(event.currentTarget)
    setSaving(true)
    setError('')
    try {
      await api(category ? `/api/v1/admin/plan-categories/${category.id}` : '/api/v1/admin/plan-categories', {
        method: category ? 'PUT' : 'POST',
        body: JSON.stringify({
          name: String(data.get('name') ?? '').trim(),
          description: String(data.get('description') ?? '').trim(),
          sort_order: Number(data.get('sort_order') || 0),
        }),
      })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存分类失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{category ? '编辑商品分类' : '新建商品分类'}</h3>
          <p>客户在商店按分类浏览套餐，描述会显示在分类下方。排序值小的排在前面。</p>
        </div>
        <button className="icon-button" onClick={onClose} aria-label="关闭"><X size={18} /></button>
      </div>
      <form className="form-grid" onSubmit={submit}>
        <label>
          <span>分类名称</span>
          <input name="name" required maxLength={60} placeholder="如：香港 NAT" defaultValue={category?.name} />
        </label>
        <label>
          <span>排序</span>
          <input name="sort_order" type="number" min="-10000" max="10000" step="1" defaultValue={category?.sort_order ?? 0} />
        </label>
        <label className="wide">
          <span>备注描述</span>
          <textarea name="description" rows={3} maxLength={500} placeholder="如：CN2 GIA 线路，适合建站和代理" defaultValue={category?.description} />
        </label>
        {error && <div className="form-error wide">{error}</div>}
        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving}>{saving ? '正在保存…' : category ? '保存分类' : '创建分类'}</button>
        </div>
      </form>
    </div>
  )
}
