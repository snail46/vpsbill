// The ledger is in CNY. The site chooses which currencies visitors may read
// amounts in (CNY, USD or both) and which one they see first; USD amounts
// are converted at the site's rate, and payments are still charged in CNY
// (see Charged). The admin console always shows CNY.
import { inlineSiteLocale } from './i18n'

export type DisplayCurrency = 'CNY' | 'USD'

type SiteCurrencies = { usd_enabled?: boolean; usd_rate?: number; currencies?: string[]; default_currency?: string }

const storageKey = 'vpsbill-currency'

let shown: DisplayCurrency[] = ['CNY']
let siteDefault: DisplayCurrency = 'CNY'
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
export function setCurrencyRates(locale: SiteCurrencies | undefined) {
  usdRate = Number(locale?.usd_rate) || 0
  const listed = (locale?.currencies ?? (locale?.usd_enabled ? ['CNY', 'USD'] : ['CNY'])).filter(
    (value): value is DisplayCurrency => value === 'CNY' || (value === 'USD' && usdRate > 0),
  )
  shown = listed.length ? listed : ['CNY']
  siteDefault = locale?.default_currency === 'USD' && shown.includes('USD') ? 'USD' : shown[0]
}

setCurrencyRates(inlineSiteLocale())

const inAdmin = () => window.location.pathname.startsWith('/admin')

// shownCurrencies lists the currencies a visitor may choose between.
export function shownCurrencies(): DisplayCurrency[] {
  return inAdmin() ? ['CNY'] : shown
}

// siteCurrency is the site's default currency, whatever the visitor chose.
export function siteCurrency(): DisplayCurrency {
  return siteDefault
}

// displayCurrency is the currency amounts are shown in: the visitor's
// choice among the shown ones, else the site's default.
export function displayCurrency(): DisplayCurrency {
  if (inAdmin()) return 'CNY'
  const choice = stored()
  return choice && shown.includes(choice) ? choice : siteDefault
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

export function currencySymbol(currency: string = displayCurrency()) {
  return symbols[currency] ?? `${currency} `
}

function format(amountMinor: number, currency: string) {
  const sign = amountMinor < 0 ? '-' : ''
  return `${sign}${currencySymbol(currency)}${amountFormat.format(Math.abs(amountMinor) / 100)}`
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

// plainMoney shows minor units of a currency as they are.
export function plainMoney(amountMinor: number, currency: string) {
  return format(amountMinor, currency)
}

// converted reports whether money() shows something else than what is
// charged, i.e. whether a payment needs to say so.
export function converted(currency = 'CNY') {
  return currency === 'CNY' && displayCurrency() === 'USD'
}

// toLedgerMinor turns an amount typed in the display currency (in whole
// units, such as 12.5) into ledger (CNY) minor units.
export function toLedgerMinor(amount: number) {
  return Math.round(amount * 100 * (displayCurrency() === 'USD' ? usdRate : 1))
}

// fromLedgerMinor is the display-currency minor units of a ledger amount.
export function fromLedgerMinor(amountMinor: number) {
  return displayCurrency() === 'USD' ? Math.round(amountMinor / usdRate) : amountMinor
}

// usdRateText is the rate as a sentence fragment: 1 USD = 7.20 CNY.
export function usdRateText() {
  return `1 USD = ${usdRate.toFixed(2)} CNY`
}
