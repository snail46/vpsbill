import { useEffect, useRef, useState } from 'react'
import { Bell } from 'lucide-react'
import { api, type PlanRecord, type StockCapacityRecord } from '../api'
import { toast } from './toast'
import { t } from './i18n'

// stockLeft is how many more instances a plan can sell (null = no stock set).
export function stockLeft(plan: Pick<PlanRecord, 'stock_limit' | 'stock_held'>) {
  return plan.stock_limit == null ? null : Math.max(plan.stock_limit - (plan.stock_held ?? 0), 0)
}

export function StockTag({ plan }: { plan: Pick<PlanRecord, 'stock_limit' | 'stock_held'> }) {
  const left = stockLeft(plan)
  if (left === null) return null
  return <span className={left > 0 ? 'tag stock-tag' : 'tag stock-tag sold-out'}>{left > 0 ? t('库存 {0}', left) : t('已售罄')}</span>
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
  // The form's owner recreates preview on every render with its current
  // state (the provider type, for one); refreshes must use the latest, not
  // the one from the first render.
  const latest = useRef({ preview, onCapacity })
  latest.current = { preview, onCapacity }

  useEffect(() => {
    const form = ref.current?.closest('form')
    if (!form) return
    let timer = 0
    let cancelled = false
    const refresh = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        latest.current
          .preview(form)
          .then(value => {
            if (cancelled) return
            setCapacity(value)
            latest.current.onCapacity?.(value)
            setError('')
            // A new plan defaults to everything its nodes can hold.
            if (!plan && !touched.current && input.current) input.current.value = String(value.max)
          })
          .catch(err => !cancelled && setError(err instanceof Error ? err.message : t('无法计算库存上限')))
      }, 400)
    }
    const onChange = (event: Event) => {
      const target = event.target as HTMLInputElement
      if (target.name === 'stock_limit') touched.current = true
      else if (['vcpu', 'ram_mb', 'disk_gb', 'traffic_gb', 'virtualization', 'provider_type', 'node_selection', 'node_ids'].includes(target.name ?? '') || target.tagName === 'SELECT') refresh()
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
    // The form fields drive refreshes; preview is read through latest.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan?.id])

  return (
    <div ref={ref} className="stock-field">
      <label>
        <span>{t('库存（台，含已售；留空只受资源限制）')}</span>
        <input ref={input} name="stock_limit" type="number" min="0" step="1" placeholder={t('不限')} defaultValue={plan?.stock_limit ?? ''} />
      </label>
      <small className="field-hint">
        {error
          ? error
          : capacity
            ? t('最多 {0} 台（按母机配置、超售倍数、其他套餐占用和本套餐配置计算{1}）{2}', capacity.max, capacity.nodes > 1 ? t('，{0} 个节点', capacity.nodes) : '', plan ? t('；已售及待支付 {0} 台', plan.stock_held ?? 0) : '')
            : t('正在计算库存上限…')}
      </small>
    </div>
  )
}

// readStock returns the stock from the form, null when left empty.
export function readStock(form: FormData): number | null {
  const raw = String(form.get('stock_limit') ?? '').trim()
  return raw === '' ? null : Math.max(0, Math.floor(Number(raw)))
}

// The plans this customer asked to hear about, shared by every button on
// the page.
const watchPath = '/api/v1/customer/plan-watches'
let watched: Set<string> | null = null
let loading: Promise<void> | null = null
const listeners = new Set<() => void>()

function loadWatched() {
  loading ??= api<string[]>(watchPath)
    .then(ids => {
      watched = new Set(ids)
      listeners.forEach(listener => listener())
    })
    .catch(() => {
      loading = null
    })
  return loading
}

// WatchButton asks for one notice when a sold-out plan can be bought
// again; the notice goes to the customer's notification channels.
export function WatchButton({ planID }: { planID: string }) {
  const [, refresh] = useState(0)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    const listener = () => refresh(value => value + 1)
    listeners.add(listener)
    void loadWatched()
    return () => {
      listeners.delete(listener)
    }
  }, [])

  const on = watched?.has(planID) ?? false
  async function toggle() {
    setBusy(true)
    try {
      await api(`/api/v1/customer/plans/${planID}/watch`, { method: on ? 'DELETE' : 'PUT' })
      watched ??= new Set()
      if (on) watched.delete(planID)
      else watched.add(planID)
      listeners.forEach(listener => listener())
      toast('success', on ? t('已取消到货通知') : t('到货后会通知你'), on ? undefined : t('补货时按你的通知方式（邮件或 Telegram）提醒一次，先到先得。'))
    } catch (err) {
      toast('error', t('操作失败'), err instanceof Error ? err.message : undefined)
    } finally {
      setBusy(false)
    }
  }

  return (
    <button type="button" className={on ? 'secondary-button compact watch-button on' : 'secondary-button compact watch-button'} disabled={busy} aria-pressed={on} onClick={() => void toggle()}>
      <Bell size={14} />{on ? t('已订阅到货通知') : t('到货通知我')}
    </button>
  )
}
