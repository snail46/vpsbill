import { useState } from 'react'
import { t } from './i18n'
import { ledgerUnit } from './currency'

// Billing cycles are the four named ones or a custom length: "d<N>" for N
// days (1-365) or "m<N>" for N months (1-60), mirroring the API.
export const namedCycles = ['monthly', 'quarterly', 'semiannual', 'annual'] as const

const names: Record<string, string> = { monthly: t('月付'), quarterly: t('季付'), semiannual: t('半年付'), annual: t('年付') }
const units: Record<string, string> = { monthly: t('月'), quarterly: t('季'), semiannual: t('半年'), annual: t('年') }

function customLength(cycle: string): { n: number; unit: 'd' | 'm' } | null {
  const match = /^([dm])([1-9]\d{0,2})$/.exec(cycle)
  return match ? { n: Number(match[2]), unit: match[1] as 'd' | 'm' } : null
}

// cycleName reads "季付" or "7 天付".
export function cycleName(cycle: string) {
  if (names[cycle]) return names[cycle]
  const custom = customLength(cycle)
  return custom ? t('{0} {1}付', custom.n, custom.unit === 'd' ? t('天') : t('个月')) : cycle
}

// cycleUnit follows a price: "¥10/季", "¥5/7天".
export function cycleUnit(cycle: string) {
  if (units[cycle]) return units[cycle]
  const custom = customLength(cycle)
  return custom ? t('{0}{1}', custom.n, custom.unit === 'd' ? t('天') : t('个月')) : cycle
}

export function cycleOrder(cycle: string) {
  const index = namedCycles.indexOf(cycle as (typeof namedCycles)[number])
  if (index >= 0) return [0, 1, 3, 6, 12][index + 1]
  const custom = customLength(cycle)
  return custom ? (custom.unit === 'd' ? custom.n / 30 : custom.n) : 99
}

type PriceLike = { billing_cycle: string; amount_minor: number; purchase_limit?: number | null; sold?: number }

// priceLeft is how many more units a price can sell (null = unlimited).
export function priceLeft(price: PriceLike) {
  return price.purchase_limit ? Math.max(price.purchase_limit - (price.sold ?? 0), 0) : null
}

// CyclePriceFields edits one price per cycle, each with an optional limit on
// how many times it is sold; a cycle left empty is not sold. Read the
// result with readCyclePrices.
export function CyclePriceFields({ prices, hint }: { prices: PriceLike[]; hint?: string }) {
  const existingCustom = prices.find(price => !names[price.billing_cycle] && customLength(price.billing_cycle))
  const custom = existingCustom ? customLength(existingCustom.billing_cycle) : null
  const [unit, setUnit] = useState<'d' | 'm'>(custom?.unit ?? 'd')
  const find = (cycle: string) => prices.find(price => price.billing_cycle === cycle)
  const amount = (price?: PriceLike) => (price ? (price.amount_minor / 100).toFixed(2) : '')
  const sold = (price?: PriceLike) => (price?.sold ? <small className="field-hint">{t('已售 {0} 次', price.sold)}</small> : null)
  return (
    <fieldset className="wide cycle-prices">
      <legend>{t('计费周期与价格（{0}）', ledgerUnit())}</legend>
      <p className="field-hint">
        {t('{0}「限购次数」是该价格累计最多卖出几次（含待支付订单），卖完后该周期自动不可选，已买的实例续费不受影响；留空不限。', hint ?? t('留空表示不支持该计费周期，至少填写一个。'))}
      </p>
      <div className="cycle-price-grid">
        {namedCycles.map(cycle => (
          <div key={cycle} className="cycle-price-cell">
            <label>
              <span>{names[cycle]}</span>
              <input name={`price_${cycle}`} type="number" min="0" step="0.01" placeholder={t('不支持')} defaultValue={amount(find(cycle))} />
            </label>
            <label>
              <span>{t('限购次数')}</span>
              <input name={`limit_${cycle}`} type="number" min="1" step="1" placeholder={t('不限')} defaultValue={find(cycle)?.purchase_limit ?? ''} />
            </label>
            {sold(find(cycle))}
          </div>
        ))}
        <div className="cycle-price-cell cycle-custom">
          <label>
            <span>{t('自定义周期（长度、单位、价格、限购次数）')}</span>
            <div className="cycle-custom-row">
              <input name="custom_cycle_length" type="number" min="1" max={unit === 'd' ? 365 : 60} placeholder={t('长度')} defaultValue={custom?.n ?? ''} />
              <select name="custom_cycle_unit" value={unit} onChange={event => setUnit(event.target.value as 'd' | 'm')}>
                <option value="d">{t('天')}</option>
                <option value="m">{t('个月')}</option>
              </select>
              <input name="price_custom" type="number" min="0" step="0.01" placeholder={t('价格')} defaultValue={amount(existingCustom)} />
              <input name="limit_custom" type="number" min="1" step="1" placeholder={t('限购不限')} defaultValue={existingCustom?.purchase_limit ?? ''} />
            </div>
          </label>
          {sold(existingCustom)}
        </div>
      </div>
    </fieldset>
  )
}

// readCyclePrices returns cycle -> amount in minor units and cycle -> sale
// limit for the filled-in cycles, or an error message.
export function readCyclePrices(form: FormData): { prices: Record<string, number>; limits: Record<string, number>; error?: string } {
  const prices: Record<string, number> = {}
  const limits: Record<string, number> = {}
  const readLimit = (field: string, cycle: string) => {
    const value = Number(form.get(field) || 0)
    if (!Number.isInteger(value) || value < 0) return t('限购次数需为正整数，留空表示不限')
    if (value > 0) limits[cycle] = value
    return ''
  }
  for (const cycle of namedCycles) {
    const value = Number(form.get(`price_${cycle}`) || 0)
    if (value > 0) {
      prices[cycle] = Math.round(value * 100)
      const error = readLimit(`limit_${cycle}`, cycle)
      if (error) return { prices, limits, error }
    }
  }
  const customPrice = Number(form.get('price_custom') || 0)
  const length = Number(form.get('custom_cycle_length') || 0)
  if (customPrice > 0 || length > 0) {
    const unit = form.get('custom_cycle_unit') === 'm' ? 'm' : 'd'
    if (!Number.isInteger(length) || length < 1 || length > (unit === 'd' ? 365 : 60)) {
      return { prices, limits, error: t('自定义周期需为 1-365 天或 1-60 个月') }
    }
    if (customPrice <= 0) return { prices, limits, error: t('请填写自定义周期的价格') }
    const cycle = unit === 'm' && [1, 3, 6, 12].includes(length) ? namedCycles[[1, 3, 6, 12].indexOf(length)] : `${unit}${length}`
    if (prices[cycle]) return { prices, limits, error: t('自定义周期与{0}重复', names[cycle]) }
    prices[cycle] = Math.round(customPrice * 100)
    const error = readLimit('limit_custom', cycle)
    if (error) return { prices, limits, error }
  }
  if (!Object.keys(prices).length) return { prices, limits, error: t('请至少填写一个计费周期的价格') }
  return { prices, limits }
}
