import { FormEvent, useEffect, useState } from 'react'
import { X } from 'lucide-react'
import { api, cached, imageLabel, AvailableTemplateRecord, ProviderTypeRecord, PlanRecord } from '../api'
import { CouponManager } from '../Coupons'
import { PageActions, StatusBadge, cycleLabel } from '../shared/ui'
import { cycleOrder, CyclePriceFields, readCyclePrices } from '../shared/cycles'
import { readStock, StockField, StockTag } from '../shared/stock'
import { DiskIOFields, diskIOText, readDiskIO } from '../shared/diskio'
import type { StockCapacityRecord } from '../api'
import { virtualizationLabel } from './Nodes'

export function PlansView() {
  const [plans, setPlans] = useState<PlanRecord[]>(() => cached<PlanRecord[]>('/api/v1/admin/plans') ?? [])
  const [showForm, setShowForm] = useState(false)
  const [editing, setEditing] = useState<PlanRecord | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<PlanRecord[]>('/api/v1/admin/plans')
      .then(setPlans)
      .catch(err => setError(err.message))

  useEffect(() => {
    void load()
  }, [])

  async function toggle(plan: PlanRecord) {
    try {
      await api(`/api/v1/admin/plans/${plan.id}`, {
        method: 'PATCH',
        body: JSON.stringify({ enabled: !plan.enabled }),
      })
      load()
    } catch (err) {
      setError(err instanceof Error ? err.message : '更新状态失败')
    }
  }

  const closeForm = () => {
    setShowForm(false)
    setEditing(null)
  }

  return (
    <section className="workspace-panel">
      <PageActions
        eyebrow="PRODUCT CATALOG"
        title="商品套餐管理"
        description="网络策略与模板随套餐统一下发，套餐修改将自增版本号且不影响历史订单明细。"
        action={() => {
          setEditing(null)
          setShowForm(true)
        }}
        actionLabel="创建新套餐"
      />

      {error && <div className="form-error">{error}</div>}

      {showForm && (
        <PlanForm
          plan={editing}
          onClose={closeForm}
          onSaved={() => {
            closeForm()
            load()
          }}
        />
      )}

      <div className="plan-grid">
        {plans.map(plan => (
          <article className={plan.enabled ? 'plan-card' : 'plan-card disabled'} key={plan.id}>
            <div className="plan-card-top">
              <span className="tag">{plan.provider_type.toUpperCase()} · {plan.virtualization.toUpperCase()}</span>
              <StatusBadge status={plan.enabled ? 'online' : 'disabled'} />
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
            {diskIOText(plan) && <small>{diskIOText(plan)}</small>}
            <small className="plan-network">
              网络：
              {[
                plan.assign_nat && `NAT×${plan.port_mapping_count}`,
                plan.assign_ipv4 && `IPv4×${plan.ipv4_count}`,
                plan.assign_ipv6 && `IPv6×${plan.ipv6_count}`,
              ]
                .filter(Boolean)
                .join(' / ')}
            </small>

            <StockTag plan={plan} />
            <div className="price-line">
              {plan.prices.length ? (
                [...plan.prices]
                  .sort((a, b) => cycleOrder(a.billing_cycle) - cycleOrder(b.billing_cycle))
                  .map(price => (
                    <span key={price.billing_cycle} className="price-chip">
                      <strong>¥{(price.amount_minor / 100).toFixed(2)}</strong>
                      <span>/ {cycleLabel(price.billing_cycle)}</span>
                    </span>
                  ))
              ) : (
                '暂无报价'
              )}
            </div>

            <div className="plan-actions">
              <button
                className="secondary-button"
                onClick={() => {
                  setEditing(plan)
                  setShowForm(true)
                }}
              >
                编辑套餐
              </button>
              <button className="secondary-button" onClick={() => toggle(plan)}>
                {plan.enabled ? '下架套餐' : '重新上架'}
              </button>
            </div>
          </article>
        ))}
        {!plans.length && <div className="empty-card" style={{ gridColumn: '1 / -1' }}>尚未创建任何商品套餐</div>}
      </div>

      <CouponManager
        endpoint="/api/v1/admin/coupons"
        plans={plans.map(plan => ({ id: plan.id, name: plan.name }))}
        intro="平台优惠码适用于上面的平台套餐；托管母机的套餐由机主在托管中心自行发放优惠码。"
      />
    </section>
  )
}

export function PlanForm({
  plan,
  onClose,
  onSaved,
}: {
  plan: PlanRecord | null
  onClose: () => void
  onSaved: () => void
}) {
  const [error, setError] = useState('')
  const [saving, setSaving] = useState(false)
  const [templates, setTemplates] = useState<AvailableTemplateRecord[]>([])
  const [loadingTemplates, setLoadingTemplates] = useState(true)

  const [providers, setProviders] = useState<ProviderTypeRecord[]>(() => cached<ProviderTypeRecord[]>('/api/v1/admin/provider-types') ?? [])
  const [providerType, setProviderType] = useState(plan?.provider_type || 'clicd')
  const [virtualization, setVirtualization] = useState<'lxc' | 'kvm' | 'podman'>(plan?.virtualization || 'lxc')
  const [allowed, setAllowed] = useState<string[]>(plan?.allowed_template_ids || [])
  const [capacity, setCapacity] = useState<StockCapacityRecord | null>(null)
  const [defaultTemplate, setDefaultTemplate] = useState(plan?.default_template_id || '')

  useEffect(() => {
    api<ProviderTypeRecord[]>('/api/v1/admin/provider-types').then(setProviders).catch(err => setError(err.message))
  }, [])

  useEffect(() => {
    api<AvailableTemplateRecord[]>('/api/v1/admin/templates')
      .then(setTemplates)
      .catch(err => setError(err.message))
      .finally(() => setLoadingTemplates(false))
  }, [])

  const descriptor = providers.find(item => item.type === providerType)
  const virtualizations = (descriptor?.virtualization_types ?? [virtualization]) as Array<'lxc' | 'kvm' | 'podman'>
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

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    if (!allowed.length || !defaultTemplate) {
      setError('请至少勾选一个可用操作系统模板并指定默认模板')
      return
    }
    const data = new FormData(event.currentTarget)
    const { prices, limits, error: priceError } = readCyclePrices(data)
    if (priceError) {
      setError(priceError)
      return
    }
    setSaving(true)
    setError('')
    const body = {
      code: data.get('code'),
      name: data.get('name'),
      provider_type: providerType,
      virtualization,
      vcpu: Number(data.get('vcpu')),
      ram_mb: Number(data.get('ram_mb')),
      disk_gb: Number(data.get('disk_gb')),
      traffic_gb: Number(data.get('traffic_gb')),
      network_down_mbps: Number(data.get('network_down_mbps')),
      network_up_mbps: Number(data.get('network_up_mbps')),
      snapshot_limit: Number(data.get('snapshot_limit')),
      assign_nat: data.get('assign_nat') === 'on',
      port_mapping_count: Number(data.get('port_mapping_count')),
      assign_ipv4: data.get('assign_ipv4') === 'on',
      ipv4_count: Number(data.get('ipv4_count')),
      assign_ipv6: data.get('assign_ipv6') === 'on',
      ipv6_count: Number(data.get('ipv6_count')),
      default_template_id: defaultTemplate,
      allowed_template_ids: allowed,
      enabled: plan?.enabled ?? true,
      prices: Object.entries(prices).map(([billing_cycle, amount_minor]) => ({
        currency: 'CNY',
        billing_cycle,
        amount_minor,
        setup_fee_minor: 0,
        purchase_limit: limits[billing_cycle] ?? null,
      })),
      stock_limit: readStock(data),
      ...readDiskIO(data),
    }
    try {
      await api(plan ? `/api/v1/admin/plans/${plan.id}` : '/api/v1/admin/plans', {
        method: plan ? 'PUT' : 'POST',
        body: JSON.stringify(body),
      })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setSaving(false)
    }
  }


  return (
    <div className="inline-form">
      <div className="inline-form-heading">
        <div>
          <h3>{plan ? '编辑' : '创建'} VPS 商品套餐</h3>
          <p>套餐绑定一种对接方式，只会调度到该方式的节点；可用模板从这些在线节点读取，网络分配策略统一下发给实例。</p>
        </div>
        <button className="icon-button" onClick={onClose}><X size={18} /></button>
      </div>

      <form className="form-grid" onSubmit={submit}>
        <label>
          <span>套餐唯一编码</span>
          <input name="code" required placeholder="LXC-START" defaultValue={plan?.code} />
        </label>
        <label>
          <span>套餐展示名称</span>
          <input name="name" required placeholder="轻量入门型" defaultValue={plan?.name} />
        </label>
        <label>
          <span>对接方式</span>
          <select
            name="provider_type"
            value={providerType}
            disabled={!!plan}
            onChange={event => {
              const next = providers.find(item => item.type === event.target.value)
              setProviderType(event.target.value)
              setVirtualization((next?.virtualization_types[0] ?? 'lxc') as 'lxc' | 'kvm' | 'podman')
              setAllowed([])
              setDefaultTemplate('')
            }}
          >
            {providers.map(item => <option key={item.type} value={item.type}>{item.name}</option>)}
          </select>
        </label>
        <label>
          <span>底层虚拟化</span>
          <select
            name="virtualization"
            value={virtualization}
            onChange={event => {
              const value = event.target.value as 'lxc' | 'kvm' | 'podman'
              setVirtualization(value)
              setAllowed([])
              setDefaultTemplate('')
            }}
          >
            {virtualizations.map(kind => <option key={kind} value={kind}>{virtualizationLabel(kind)}</option>)}
          </select>
        </label>
        <label>
          <span>vCPU 核心</span>
          <input name="vcpu" type="number" min="1" defaultValue={plan?.vcpu || 1} required />
        </label>
        <label>
          <span>内存容量 MB</span>
          <input name="ram_mb" type="number" min="64" defaultValue={plan?.ram_mb || 512} required />
        </label>
        <label>
          <span>磁盘空间 GB</span>
          <input name="disk_gb" type="number" min="1" defaultValue={plan?.disk_gb || 10} required />
        </label>
        <label>
          <span>月度流量 GB</span>
          <input name="traffic_gb" type="number" min="0" defaultValue={plan?.traffic_gb ?? 1024} />
        </label>
        <label>
          <span>下行带宽 Mbps</span>
          <input name="network_down_mbps" type="number" min="0" defaultValue={plan?.network_down_mbps ?? 100} />
        </label>
        <label>
          <span>上行带宽 Mbps</span>
          <input name="network_up_mbps" type="number" min="0" defaultValue={plan?.network_up_mbps ?? 100} />
        </label>
        <label>
          <span>快照配额</span>
          <input name="snapshot_limit" type="number" min="0" defaultValue={plan?.snapshot_limit ?? 1} />
        </label>
        <CyclePriceFields prices={plan?.prices ?? (plan ? [] : [{ billing_cycle: 'monthly', amount_minor: 1900 }])} />
        <StockField
          plan={plan ?? undefined}
          preview={form => {
            const data = new FormData(form)
            return api<StockCapacityRecord>('/api/v1/admin/plans/stock-capacity', {
              method: 'POST',
              body: JSON.stringify({
                id: plan?.id ?? '',
                provider_type: providerType,
                virtualization: data.get('virtualization') || virtualization,
                vcpu: Number(data.get('vcpu')),
                ram_mb: Number(data.get('ram_mb')),
                disk_gb: Number(data.get('disk_gb')),
                traffic_gb: Number(data.get('traffic_gb')),
              }),
            })
          }}
          onCapacity={setCapacity}
        />
        {providerType === 'hatch' && <DiskIOFields plan={plan ?? undefined} capacity={capacity} />}

        <fieldset className="wide network-policy">
          <legend>网络策略配置</legend>
          <label className="checkbox">
            <input name="assign_nat" type="checkbox" defaultChecked={plan?.assign_nat ?? true} />
            分配 NAT 共享 IPv4
          </label>
          <label>
            <span>NAT 端口映射配额</span>
            <input
              name="port_mapping_count"
              type="number"
              min="0"
              max="64"
              defaultValue={plan?.port_mapping_count ?? 0}
            />
          </label>
          <label className="checkbox">
            <input name="assign_ipv4" type="checkbox" defaultChecked={plan?.assign_ipv4 ?? false} />
            分配独立公网 IPv4
          </label>
          <label>
            <span>公网 IPv4 数量</span>
            <input name="ipv4_count" type="number" min="1" max="64" defaultValue={plan?.ipv4_count ?? 1} />
          </label>
          <label className="checkbox">
            <input name="assign_ipv6" type="checkbox" defaultChecked={plan?.assign_ipv6 ?? true} />
            分配独立 IPv6
          </label>
          <label>
            <span>独立 IPv6 数量</span>
            <input name="ipv6_count" type="number" min="1" max="64" defaultValue={plan?.ipv6_count ?? 1} />
          </label>
        </fieldset>

        <fieldset className="wide">
          <legend>允许客户选择的系统镜像（动态读取自节点就绪镜像）</legend>
          {loadingTemplates ? (
            <div className="template-empty">正在向在线 CLICD 节点检索可用系统镜像…</div>
          ) : (
            <div className="template-picker">
              {visibleTemplates.map(item => (
                <label
                  className={allowed.includes(item.id) ? 'template-option selected' : 'template-option'}
                  key={`${item.virtualization}:${item.id}`}
                >
                  <input
                    type="checkbox"
                    checked={allowed.includes(item.id)}
                    onChange={event => toggleTemplate(item.id, event.target.checked)}
                  />
                  <span>
                    <strong>{templateLabel(item.id)}</strong>
                    <small>{item.description || item.id}</small>
                    <em>部署节点：{item.node_names.join('、')}</em>
                  </span>
                </label>
              ))}
              {!visibleTemplates.length && (
                <div className="template-empty">
                  在线集群中暂无已启用且已就绪的 {virtualization.toUpperCase()} 系统镜像。
                </div>
              )}
              {plan?.allowed_template_ids
                .filter(id => !visibleTemplates.some(item => item.id === id))
                .map(id => (
                  <label className="template-option unavailable" key={id}>
                    <input
                      type="checkbox"
                      checked={allowed.includes(id)}
                      onChange={event => toggleTemplate(id, event.target.checked)}
                    />
                    <span>
                      <strong>{id}</strong>
                      <small>该镜像当前节点未上报，保留后仍可保存供历史实例使用。</small>
                    </span>
                  </label>
                ))}
            </div>
          )}
        </fieldset>

        <label className="wide">
          <span>默认系统镜像</span>
          <select
            name="default_template_id"
            value={defaultTemplate}
            onChange={event => setDefaultTemplate(event.target.value)}
            required
          >
            <option value="">请先在上方勾选镜像</option>
            {allowed.map(id => (
              <option key={id} value={id}>
                {templateLabel(id)}
              </option>
            ))}
          </select>
        </label>

        {error && <div className="form-error wide">{error}</div>}

        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>取消</button>
          <button className="primary-button" disabled={saving}>
            {saving ? '正在保存…' : plan ? '保存修改' : '创建商品套餐'}
          </button>
        </div>
      </form>
    </div>
  )
}
