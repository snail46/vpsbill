// The hosted market is browsed by country, then host, then plan. This
// module narrows and orders that tree; Hosting.tsx draws it.
import type { HostedNodeRecord, PlanRecord } from '../api'
import { countryName } from './country'
import { convertMinor, displayCurrency } from './currency'
import { cycleOrder, priceLeft } from './cycles'
import { stockLeft } from './stock'

export type MarketSort = 'default' | 'price-asc' | 'price-desc'

export type MarketFilter = {
  query: string
  // min and max are typed in the display currency's whole units.
  min: string
  max: string
  sort: MarketSort
  inStock: boolean
}

export const noFilter: MarketFilter = { query: '', min: '', max: '', sort: 'default', inStock: false }

// narrowing counts the conditions that hide something (sorting hides
// nothing).
export function narrowing(filter: MarketFilter) {
  return [filter.query.trim(), filter.min, filter.max, filter.inStock].filter(Boolean).length
}

// headlinePrice is the price a plan is advertised with: the monthly one,
// else its shortest cycle.
export function headlinePrice(plan: PlanRecord) {
  const sorted = [...plan.prices].sort((a, b) => cycleOrder(a.billing_cycle) - cycleOrder(b.billing_cycle))
  return sorted.find(price => price.billing_cycle === 'monthly') ?? sorted[0]
}

// planAmount is the headline price in the display currency's minor units,
// which is what price limits and ordering compare.
export function planAmount(plan: PlanRecord) {
  const price = headlinePrice(plan)
  return price ? convertMinor(price.amount_minor, price.currency, displayCurrency()) : null
}

// planInStock reports whether the plan can be ordered right now: its host
// is online and not held, has room for it, and neither the plan nor all
// of its prices are sold out.
export function planInStock(node: HostedNodeRecord, plan: PlanRecord) {
  return (
    node.status === 'online' &&
    !node.health_hold_reason &&
    stockLeft(plan) !== 0 &&
    plan.prices.some(price => priceLeft(price) !== 0) &&
    plan.vcpu <= node.free_vcpu &&
    plan.ram_mb <= node.free_ram_mb &&
    plan.disk_gb <= node.free_disk_gb
  )
}

export type MarketNode = { node: HostedNodeRecord; plans: PlanRecord[]; low: number | null; high: number | null; inStock: boolean }
export type MarketCountry = { code: string; nodes: MarketNode[]; plans: number; low: number | null; high: number | null }

const virtWords: Record<string, string> = { lxc: 'lxc 容器 container', podman: 'podman 容器 container', kvm: 'kvm' }

function words(node: HostedNodeRecord, plan: PlanRecord) {
  const code = node.country_code || ''
  return [
    code,
    countryName(code, 'zh'),
    countryName(code, 'en'),
    node.name,
    node.owner_name,
    node.region_name,
    node.location,
    node.line_description,
    plan.name,
    plan.description,
    ...(plan.tags ?? []),
    virtWords[plan.virtualization] ?? plan.virtualization,
  ]
    .filter(Boolean)
    .join('\n')
    .toLowerCase()
}

function range(amounts: (number | null)[]) {
  const known = amounts.filter((amount): amount is number => amount !== null)
  return known.length ? { low: Math.min(...known), high: Math.max(...known) } : { low: null, high: null }
}

// byPrice orders by the lowest price going up and by the highest going
// down; whatever has no price goes last either way.
function byPrice<T extends { low: number | null; high: number | null }>(sort: MarketSort) {
  return (a: T, b: T) => {
    const [x, y] = sort === 'price-asc' ? [a.low, b.low] : [a.high, b.high]
    if (x === null || y === null) return x === y ? 0 : x === null ? 1 : -1
    return sort === 'price-asc' ? x - y : y - x
  }
}

// arrangeMarket keeps the plans that pass the filter, the hosts that still
// have a plan and the countries that still have a host, in the chosen
// order. Every word of the query must appear somewhere in a plan, its host
// or its country.
export function arrangeMarket(nodes: HostedNodeRecord[], filter: MarketFilter): MarketCountry[] {
  const terms = filter.query.toLowerCase().split(/\s+/).filter(Boolean)
  const min = filter.min === '' ? null : Number(filter.min) * 100
  const max = filter.max === '' ? null : Number(filter.max) * 100
  const groups = new Map<string, MarketNode[]>()
  for (const node of nodes) {
    let plans = node.plans.filter(plan => {
      if (filter.inStock && !planInStock(node, plan)) return false
      const amount = planAmount(plan)
      if (min !== null && !Number.isNaN(min) && (amount === null || amount < min)) return false
      if (max !== null && !Number.isNaN(max) && (amount === null || amount > max)) return false
      if (!terms.length) return true
      const text = words(node, plan)
      return terms.every(term => text.includes(term))
    })
    if (!plans.length) continue
    if (filter.sort !== 'default') {
      const order = byPrice<{ low: number | null; high: number | null }>(filter.sort)
      plans = plans
        .map(plan => ({ plan, low: planAmount(plan), high: planAmount(plan) }))
        .sort(order)
        .map(item => item.plan)
    }
    const code = node.country_code || ''
    const listed = groups.get(code) ?? []
    listed.push({ node, plans, ...range(plans.map(planAmount)), inStock: plans.some(plan => planInStock(node, plan)) })
    groups.set(code, listed)
  }
  const countries = [...groups].map(([code, listed]) => ({
    code,
    nodes: filter.sort === 'default' ? listed : [...listed].sort(byPrice(filter.sort)),
    plans: listed.reduce((sum, item) => sum + item.plans.length, 0),
    ...range(listed.flatMap(item => [item.low, item.high])),
  }))
  if (filter.sort !== 'default') return countries.sort(byPrice(filter.sort))
  // By default the busiest country comes first and the unplaced last.
  return countries.sort((a, b) => Number(!a.code) - Number(!b.code) || b.nodes.length - a.nodes.length || a.code.localeCompare(b.code))
}
