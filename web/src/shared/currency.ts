// The ledger is in CNY. A visitor may read amounts in USD instead: every
// CNY amount is converted at the site's rate for display, and payments are
// still charged in CNY (see payNote). The admin console always shows CNY.
import { inlineSiteLocale, lang } from './i18n'

export type DisplayCurrency = 'CNY' | 'USD'

const storageKey = 'vpsbill-currency'

let usdEnabled = false
let usdRate = 0

function stored(): DisplayCurrency | null {
  try {
    const value = window.localStorage.getItem(storageKey)
    return value === 'CNY' || value === 'USD' ? value : null
  } catch {
    return null
  }
}

// setCurrencyRates takes the site's settings (from the boot data).
export function setCurrencyRates(locale: { usd_enabled?: boolean; usd_rate?: number } | undefined) {
  usdEnabled = Boolean(locale?.usd_enabled) && Number(locale?.usd_rate) > 0
  usdRate = Number(locale?.usd_rate) || 0
}

setCurrencyRates(inlineSiteLocale())

export function usdAvailable() {
  return usdEnabled && !window.location.pathname.startsWith('/admin')
}

// usdPerCNY is the site's rate: how many CNY one USD is worth.
export function usdRateValue() {
  return usdRate
}

// displayCurrency is the currency amounts are shown in: the visitor's
// choice, else USD for English readers.
export function displayCurrency(): DisplayCurrency {
  if (!usdAvailable()) return 'CNY'
  return stored() ?? (lang === 'en' ? 'USD' : 'CNY')
}

export function setDisplayCurrency(next: DisplayCurrency) {
  if (next === displayCurrency()) return
  try {
    window.localStorage.setItem(storageKey, next)
  } catch {
    return
  }
  window.location.reload()
}

const symbols: Record<string, string> = { CNY: '¥', USD: '$' }
const amountFormat = new Intl.NumberFormat('en-US', { minimumFractionDigits: 2, maximumFractionDigits: 2 })

function format(amountMinor: number, currency: string) {
  const sign = amountMinor < 0 ? '-' : ''
  const symbol = symbols[currency] ?? `${currency} `
  return `${sign}${symbol}${amountFormat.format(Math.abs(amountMinor) / 100)}`
}

// money shows an amount in the visitor's display currency.
export function money(amountMinor: number, currency = 'CNY') {
  if (currency === 'CNY' && displayCurrency() === 'USD') return format(Math.round(amountMinor / usdRate), 'USD')
  return format(amountMinor, currency)
}

// chargedMoney shows an amount in the currency it is charged in.
export function chargedMoney(amountMinor: number, currency = 'CNY') {
  return format(amountMinor, currency)
}

// converted reports whether money() shows something else than what is
// charged, i.e. whether a payment needs to say so.
export function converted(currency = 'CNY') {
  return currency === 'CNY' && displayCurrency() === 'USD'
}

// usdRateText is the rate as a sentence fragment: 1 USD = 7.20 CNY.
export function usdRateText() {
  return `1 USD = ${usdRate.toFixed(2)} CNY`
}
