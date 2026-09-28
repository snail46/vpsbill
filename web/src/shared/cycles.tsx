import { useState } from 'react'

// Billing cycles are the four named ones or a custom length: "d<N>" for N
// days (1-365) or "m<N>" for N months (1-60), mirroring the API.
export const namedCycles = ['monthly', 'quarterly', 'semiannual', 'annual'] as const

const names: Record<string, string> = { monthly: '月付', quarterly: '季付', semiannual: '半年付', annual: '年付' }
const units: Record<string, string> = { monthly: '月', quarterly: '季', semiannual: '半年', annual: '年' }

function customLength(cycle: string): { n: number; unit: 'd' | 'm' } | null {
  const match = /^([dm])([1-9]\d{0,2})$/.exec(cycle)
  return match ? { n: Number(match[2]), unit: match[1] as 'd' | 'm' } : null
}

// cycleName reads "季付" or "7 天付".
export function cycleName(cycle: string) {
  if (names[cycle]) return names[cycle]
  const custom = customLength(cycle)
  return custom ? `${custom.n} ${custom.unit === 'd' ? '天' : '个月'}付` : cycle
}

// cycleUnit follows a price: "¥10/季", "¥5/7天".
export function cycleUnit(cycle: string) {
  if (units[cycle]) return units[cycle]
  const custom = customLength(cycle)
  return custom ? `${custom.n}${custom.unit === 'd' ? '天' : '个月'}` : cycle
}

export function cycleOrder(cycle: string) {
  const index = namedCycles.indexOf(cycle as (typeof namedCycles)[number])
  if (index >= 0) return [0, 1, 3, 6, 12][index + 1]
  const custom = customLength(cycle)
  return custom ? (custom.unit === 'd' ? custom.n / 30 : custom.n) : 99
}

type PriceLike = { billing_cycle: string; amount_minor: number }

// CyclePriceFields edits one price per cycle; a cycle left empty is not
// sold. Read the result with readCyclePrices.
export function CyclePriceFields({ prices, hint }: { prices: PriceLike[]; hint?: string }) {
  const existingCustom = prices.find(price => !names[price.billing_cycle] && customLength(price.billing_cycle))
  const custom = existingCustom ? customLength(existingCustom.billing_cycle) : null
  const [unit, setUnit] = useState<'d' | 'm'>(custom?.unit ?? 'd')
  const amount = (cycle: string) => {
    const found = prices.find(price => price.billing_cycle === cycle)
    return found ? (found.amount_minor / 100).toFixed(2) : ''
  }
  return (
    <fieldset className="wide cycle-prices">
      <legend>计费周期与价格（元）</legend>
      <p className="field-hint">{hint ?? '留空表示不支持该计费周期，至少填写一个。'}</p>
      <div className="cycle-price-grid">
        {namedCycles.map(cycle => (
          <label key={cycle}>
            <span>{names[cycle]}</span>
            <input name={`price_${cycle}`} type="number" min="0" step="0.01" placeholder="不支持" defaultValue={amount(cycle)} />
          </label>
        ))}
        <label className="cycle-custom">
          <span>自定义周期（长度、单位、价格）</span>
          <div className="cycle-custom-row">
            <input name="custom_cycle_length" type="number" min="1" max={unit === 'd' ? 365 : 60} placeholder="长度" defaultValue={custom?.n ?? ''} />
            <select name="custom_cycle_unit" value={unit} onChange={event => setUnit(event.target.value as 'd' | 'm')}>
              <option value="d">天</option>
              <option value="m">个月</option>
            </select>
            <input name="price_custom" type="number" min="0" step="0.01" placeholder="价格" defaultValue={existingCustom ? amount(existingCustom.billing_cycle) : ''} />
          </div>
        </label>
      </div>
    </fieldset>
  )
}

// readCyclePrices returns cycle -> amount in minor units for the filled-in
// cycles, or an error message.
export function readCyclePrices(form: FormData): { prices: Record<string, number>; error?: string } {
  const prices: Record<string, number> = {}
  for (const cycle of namedCycles) {
    const value = Number(form.get(`price_${cycle}`) || 0)
    if (value > 0) prices[cycle] = Math.round(value * 100)
  }
  const customPrice = Number(form.get('price_custom') || 0)
  const length = Number(form.get('custom_cycle_length') || 0)
  if (customPrice > 0 || length > 0) {
    const unit = form.get('custom_cycle_unit') === 'm' ? 'm' : 'd'
    if (!Number.isInteger(length) || length < 1 || length > (unit === 'd' ? 365 : 60)) {
      return { prices, error: '自定义周期需为 1-365 天或 1-60 个月' }
    }
    if (customPrice <= 0) return { prices, error: '请填写自定义周期的价格' }
    const cycle = unit === 'm' && [1, 3, 6, 12].includes(length) ? namedCycles[[1, 3, 6, 12].indexOf(length)] : `${unit}${length}`
    if (prices[cycle]) return { prices, error: `自定义周期与${names[cycle]}重复` }
    prices[cycle] = Math.round(customPrice * 100)
  }
  if (!Object.keys(prices).length) return { prices, error: '请至少填写一个计费周期的价格' }
  return { prices }
}
