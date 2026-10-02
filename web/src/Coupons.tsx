import { FormEvent, useEffect, useState } from 'react'
import { Plus, TicketPercent, X } from 'lucide-react'
import { api, cached, type CouponQuoteRecord, type CouponRecord } from './api'
import { walletMoney } from './Wallet'
import { formatDate } from './shared/time'
import { t } from './shared/i18n'

export function couponDiscountLabel(coupon: Pick<CouponRecord, 'discount_type' | 'discount_value'>) {
  if (coupon.discount_type === 'amount') return t('每台减 {0}', walletMoney(coupon.discount_value))
  const rate = (100 - coupon.discount_value) / 10
  return t('减 {0}%（{1} 折）', coupon.discount_value, Number.isInteger(rate) ? rate : rate.toFixed(1))
}

// localDay is the expiry day on the UTC+8 calendar the server uses.
function localDay(value: string | null) {
  return value ? formatDate(value) : ''
}

// CouponField checks a code against the plan being bought and hands the
// accepted code to the order form.
export function CouponField({ planId, cycle, onApplied }: { planId: string; cycle: string; onApplied: (code: string, discountMinor: number) => void }) {
  const [code, setCode] = useState('')
  const [quote, setQuote] = useState<CouponQuoteRecord | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    setQuote(null)
    onApplied('', 0)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [planId, cycle])

  async function apply() {
    if (!code.trim()) return
    setBusy(true)
    setError('')
    try {
      const result = await api<CouponQuoteRecord>('/api/v1/customer/coupons/quote', {
        method: 'POST',
        body: JSON.stringify({ code: code.trim(), plan_id: planId, billing_cycle: cycle }),
      })
      setQuote(result)
      onApplied(result.code, result.discount_minor)
    } catch (err) {
      setQuote(null)
      onApplied('', 0)
      setError(err instanceof Error ? err.message : t('优惠码无效'))
    } finally {
      setBusy(false)
    }
  }

  function clear() {
    setQuote(null)
    setCode('')
    setError('')
    onApplied('', 0)
  }

  return (
    <div className="coupon-field wide">
      <span>{t('优惠码（可选）')}</span>
      <div className="coupon-input">
        <input value={code} onChange={event => setCode(event.target.value.toUpperCase())} placeholder={t('输入优惠码')} disabled={!!quote} maxLength={32} />
        {quote ? (
          <button type="button" className="secondary-button" onClick={clear}>{t('取消')}</button>
        ) : (
          <button type="button" className="secondary-button" disabled={busy || !code.trim()} onClick={() => void apply()}>
            {busy ? t('验证中…') : t('使用')}
          </button>
        )}
      </div>
      {error && <small className="field-error">{error}</small>}
      {quote && (
        <small className="coupon-ok">
          {t('已使用 {0}：每台优惠 {1}，实付 {2}{3}{4}', quote.code, walletMoney(quote.discount_minor), walletMoney(quote.final_minor), quote.recurring ? t('，续费同价') : t('，仅首期'), quote.description ? ` · ${quote.description}` : '')}
        </small>
      )}
    </div>
  )
}

type PlanOption = { id: string; name: string }

// CouponManager lists and edits one owner's coupons: staff coupons for
// platform plans, or a host's coupons for their own plans.
export function CouponManager({ endpoint, plans, intro, canCreate = true }: { endpoint: string; plans: PlanOption[]; intro: string; canCreate?: boolean }) {
  const [coupons, setCoupons] = useState<CouponRecord[]>(() => cached<CouponRecord[]>(endpoint) ?? [])
  const [editing, setEditing] = useState<CouponRecord | 'new' | null>(null)
  const [error, setError] = useState('')

  const load = () =>
    api<CouponRecord[]>(endpoint)
      .then(setCoupons)
      .catch(err => setError(err instanceof Error ? err.message : t('加载失败')))
  useEffect(() => {
    void load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [endpoint])

  const planName = (id: string) => plans.find(plan => plan.id === id)?.name || t('已删除的套餐')
  const now = Date.now()
  return (
    <div className="panel">
      <div className="panel-heading">
        <div>
          <h3>{t('优惠码')}</h3>
          <p className="muted-text">{intro}</p>
        </div>
        {canCreate && !editing && (
          <button className="primary-button compact" onClick={() => setEditing('new')}>
            <Plus size={15} />{t('新建优惠码')}
          </button>
        )}
      </div>
      {error && <div className="form-error">{error}</div>}
      {editing && (
        <CouponForm
          endpoint={endpoint}
          plans={plans}
          coupon={editing === 'new' ? undefined : editing}
          onClose={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
        />
      )}
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>{t('优惠码')}</th>
              <th>{t('优惠')}</th>
              <th>{t('适用套餐')}</th>
              <th>{t('已用 / 上限')}</th>
              <th>{t('每账号')}</th>
              <th>{t('到期')}</th>
              <th>{t('续费同价')}</th>
              <th>{t('状态')}</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            {coupons.map(coupon => {
              const expired = coupon.expires_at && new Date(coupon.expires_at).getTime() <= now
              const used = coupon.max_uses > 0 && coupon.used_count >= coupon.max_uses
              return (
                <tr key={coupon.id}>
                  <td>
                    <strong className="mono">{coupon.code}</strong>
                    {coupon.description && <small className="block muted-text">{coupon.description}</small>}
                  </td>
                  <td>{couponDiscountLabel(coupon)}</td>
                  <td>{coupon.plan_ids.length ? coupon.plan_ids.map(planName).join(t('、')) : t('全部套餐')}</td>
                  <td>{coupon.used_count} / {coupon.max_uses || t('不限')}</td>
                  <td>{coupon.per_account_limit ? t('{0} 次', coupon.per_account_limit) : t('不限')}</td>
                  <td>{coupon.expires_at ? localDay(coupon.expires_at) : t('长期有效')}</td>
                  <td>{coupon.recurring ? t('是') : t('否')}</td>
                  <td>
                    <span className={!coupon.enabled || expired || used ? 'tag' : 'tag success'}>
                      {!coupon.enabled ? t('已停用') : expired ? t('已过期') : used ? t('已用完') : t('可用')}
                    </span>
                  </td>
                  <td>
                    <button className="secondary-button compact" onClick={() => setEditing(coupon)}>{t('编辑')}</button>
                  </td>
                </tr>
              )
            })}
            {!coupons.length && (
              <tr>
                <td colSpan={8} className="muted-text">{t('还没有优惠码。')}</td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function CouponForm({ endpoint, plans, coupon, onClose, onSaved }: { endpoint: string; plans: PlanOption[]; coupon?: CouponRecord; onClose: () => void; onSaved: () => void }) {
  const [type, setType] = useState<'percent' | 'amount'>(coupon?.discount_type || 'percent')
  const [selected, setSelected] = useState<string[]>(coupon?.plan_ids || [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const form = new FormData(event.currentTarget)
    const raw = Number(form.get('discount_value') || 0)
    const body = {
      code: coupon?.code || String(form.get('code') || ''),
      description: form.get('description'),
      discount_type: type,
      discount_value: type === 'percent' ? Math.round(raw) : Math.round(raw * 100),
      plan_ids: selected,
      max_uses: Number(form.get('max_uses') || 0),
      per_account_limit: Number(form.get('per_account_limit') || 0),
      expires_on: form.get('expires_on') || '',
      recurring: form.get('recurring') === 'on',
      enabled: form.get('enabled') === 'on',
    }
    setBusy(true)
    setError('')
    try {
      await api(coupon ? `${endpoint}/${coupon.id}` : endpoint, { method: coupon ? 'PUT' : 'POST', body: JSON.stringify(body) })
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel nested-panel" onSubmit={submit}>
      <div className="panel-heading">
        <h3>
          <TicketPercent size={16} /> {coupon ? t('编辑优惠码 {0}', coupon.code) : t('新建优惠码')}
        </h3>
        <button type="button" className="icon-button" aria-label={t('关闭')} onClick={onClose}>
          <X size={16} />
        </button>
      </div>
      {error && <div className="form-error">{error}</div>}
      <div className="form-grid">
        <label>
          <span>{t('优惠码')}</span>
          <input name="code" required pattern="[A-Za-z0-9_\-]{3,32}" title={t('3-32 位字母、数字、下划线或短横线')} defaultValue={coupon?.code} disabled={!!coupon} />
        </label>
        <label>
          <span>{t('说明（可选，买家可见）')}</span>
          <input name="description" maxLength={200} defaultValue={coupon?.description} />
        </label>
        <label>
          <span>{t('优惠方式')}</span>
          <select value={type} onChange={event => setType(event.target.value as 'percent' | 'amount')}>
            <option value="percent">{t('按比例减免')}</option>
            <option value="amount">{t('每台固定减免')}</option>
          </select>
        </label>
        <label>
          <span>{type === 'percent' ? t('减免比例 %（1-99）') : t('每台每期减免金额（元）')}</span>
          <input
            key={type}
            name="discount_value"
            type="number"
            required
            min={type === 'percent' ? 1 : 0.01}
            max={type === 'percent' ? 99 : 100000}
            step={type === 'percent' ? 1 : 0.01}
            defaultValue={coupon && coupon.discount_type === type ? (type === 'percent' ? coupon.discount_value : coupon.discount_value / 100) : ''}
          />
        </label>
        <label>
          <span>{t('最大使用次数（0 = 不限，每台实例算一次）')}</span>
          <input name="max_uses" type="number" min="0" max="1000000" required defaultValue={coupon?.max_uses ?? 0} />
        </label>
        <label>
          <span>{t('每个账号限用次数（0 = 不限，每台实例算一次）')}</span>
          <input name="per_account_limit" type="number" min="0" max="10000" required defaultValue={coupon?.per_account_limit ?? 1} />
        </label>
        <label>
          <span>{t('到期日期（可选，当天结束失效）')}</span>
          <input name="expires_on" type="date" defaultValue={localDay(coupon?.expires_at || null)} />
        </label>
        <fieldset className="wide template-picker">
          <legend>{t('适用套餐（都不勾选 = 全部套餐）')}</legend>
          {plans.map(plan => (
            <label key={plan.id} className="checkbox">
              <input
                type="checkbox"
                checked={selected.includes(plan.id)}
                onChange={event => setSelected(current => (event.target.checked ? [...current, plan.id] : current.filter(item => item !== plan.id)))}
              />
              <span>{plan.name}</span>
            </label>
          ))}
          {!plans.length && <span className="muted-text">{t('还没有套餐。')}</span>}
        </fieldset>
        <label className="checkbox">
          <input name="recurring" type="checkbox" defaultChecked={coupon?.recurring ?? false} />
          <span>{t('续费同价：用此码购买的实例续费时沿用相同折扣')}</span>
        </label>
        <label className="checkbox">
          <input name="enabled" type="checkbox" defaultChecked={coupon?.enabled ?? true} />
          <span>{t('启用')}</span>
        </label>
        <div className="form-actions wide">
          <button type="button" className="secondary-button" onClick={onClose}>{t('取消')}</button>
          <button className="primary-button compact" disabled={busy}>{busy ? t('正在保存…') : t('保存')}</button>
        </div>
      </div>
    </form>
  )
}
