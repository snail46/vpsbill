import { useEffect, useRef, useState } from 'react'
import type { PlanRecord, StockCapacityRecord } from '../api'

// stockLeft is how many more instances a plan can sell (null = no stock set).
export function stockLeft(plan: Pick<PlanRecord, 'stock_limit' | 'stock_held'>) {
  return plan.stock_limit == null ? null : Math.max(plan.stock_limit - (plan.stock_held ?? 0), 0)
}

export function StockTag({ plan }: { plan: Pick<PlanRecord, 'stock_limit' | 'stock_held'> }) {
  const left = stockLeft(plan)
  if (left === null) return null
  return <span className={left > 0 ? 'tag stock-tag' : 'tag stock-tag sold-out'}>{left > 0 ? `库存 ${left}` : '已售罄'}</span>
}

// StockField edits a plan's stock. The ceiling is recomputed from the form's
// current size fields through preview whenever they change, and a new plan
// starts at the ceiling.
export function StockField({
  plan,
  preview,
  onCapacity,
}: {
  plan?: Pick<PlanRecord, 'id' | 'stock_limit' | 'stock_held'>
  preview: (form: HTMLFormElement) => Promise<StockCapacityRecord>
  // onCapacity shares each preview, e.g. for the disk limit suggestion.
  onCapacity?: (capacity: StockCapacityRecord) => void
}) {
  const ref = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const [capacity, setCapacity] = useState<StockCapacityRecord | null>(null)
  const [error, setError] = useState('')
  const touched = useRef(false)

  useEffect(() => {
    const form = ref.current?.closest('form')
    if (!form) return
    let timer = 0
    let cancelled = false
    const refresh = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        preview(form)
          .then(value => {
            if (cancelled) return
            setCapacity(value)
            onCapacity?.(value)
            setError('')
            // A new plan defaults to everything its nodes can hold.
            if (!plan && !touched.current && input.current) input.current.value = String(value.max)
          })
          .catch(err => !cancelled && setError(err instanceof Error ? err.message : '无法计算库存上限'))
      }, 400)
    }
    const onChange = (event: Event) => {
      const target = event.target as HTMLInputElement
      if (target.name === 'stock_limit') touched.current = true
      else if (['vcpu', 'ram_mb', 'disk_gb', 'traffic_gb', 'virtualization', 'provider_type'].includes(target.name ?? '') || target.tagName === 'SELECT') refresh()
    }
    form.addEventListener('input', onChange)
    form.addEventListener('change', onChange)
    refresh()
    return () => {
      cancelled = true
      window.clearTimeout(timer)
      form.removeEventListener('input', onChange)
      form.removeEventListener('change', onChange)
    }
    // preview is recreated on every render; the form fields drive refreshes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan?.id])

  return (
    <div ref={ref} className="stock-field">
      <label>
        <span>库存（台，含已售；留空只受资源限制）</span>
        <input ref={input} name="stock_limit" type="number" min="0" step="1" placeholder="不限" defaultValue={plan?.stock_limit ?? ''} />
      </label>
      <small className="field-hint">
        {error
          ? error
          : capacity
            ? `最多 ${capacity.max} 台（按母机配置、超售倍数、其他套餐占用和本套餐配置计算${capacity.nodes > 1 ? `，${capacity.nodes} 个节点` : ''}）${plan ? `；已售及待支付 ${plan.stock_held ?? 0} 台` : ''}`
            : '正在计算库存上限…'}
      </small>
    </div>
  )
}

// readStock returns the stock from the form, null when left empty.
export function readStock(form: FormData): number | null {
  const raw = String(form.get('stock_limit') ?? '').trim()
  return raw === '' ? null : Math.max(0, Math.floor(Number(raw)))
}
