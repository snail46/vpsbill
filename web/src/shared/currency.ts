// The books are kept in one currency, the ledger currency (CNY or USD):
// every balance, price and invoice is in it. The site chooses which
// currencies visitors may read amounts in and which one they see first;
// an amount shown in the other currency is converted at the site's rate,
// and what is charged is still the ledger amount (see Charged). The admin
// console always shows amounts as they are stored.
import { inlineSiteLocale, t } from './i18n'

export type DisplayCurrency = 'CNY' | 'USD'

type SiteCurrencies = { usd_enabled?: boolean; usd_rate?: number; currencies?: string[]; default_currency?: string; ledger_currency?: string }

const storageKey = 'vpsbill-currency'

let shown: DisplayCurrency[] = ['CNY']
let siteDefault: DisplayCurrency = 'CNY'
let ledger: DisplayCurrency = 'CNY'
// usdRate is how many CNY one USD is worth.
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
  ledger = locale?.ledger_currency === 'USD' ? 'USD' : 'CNY'
  // Without a rate only the ledger currency can be shown.
  const listed = (locale?.currencies ?? (locale?.usd_enabled ? ['CNY', 'USD'] : ['CNY'])).filter(
    (value): value is DisplayCurrency => (value === 'CNY' || value === 'USD') && (usdRate > 0 || value === ledger),
  )
  shown = listed.length ? listed : [ledger]
  siteDefault = locale?.default_currency === 'USD' || locale?.default_currency === 'CNY' ? locale.default_currency : shown[0]
  if (!shown.includes(siteDefault)) siteDefault = shown[0]
}

setCurrencyRates(inlineSiteLocale())

const inAdmin = () => window.location.pathname.startsWith('/admin')

// ledgerCurrency is the currency the books are kept in.
export function ledgerCurrency(): DisplayCurrency {
  return ledger
}

// ledgerUnit names the ledger currency for a label: "元" or "美元".
export function ledgerUnit() {
  return ledger === 'USD' ? t('美元') : t('元')
}

// shownCurrencies lists the currencies a visitor may choose between.
export function shownCurrencies(): DisplayCurrency[] {
  return inAdmin() ? [ledger] : shown
}

// siteCurrency is the site's default currency, whatever the visitor chose.
export function siteCurrency(): DisplayCurrency {
  return siteDefault
}

// displayCurrency is the currency amounts are shown in: the visitor's
// choice among the shown ones, else the site's default; in the admin
// console, the ledger currency.
export function displayCurrency(): DisplayCurrency {
  if (inAdmin()) return ledger
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

// convertMinor moves minor units between CNY and USD at the site's rate.
export function convertMinor(amountMinor: number, from: string, to: string) {
  if (from === to || usdRate <= 0 || (from !== 'USD' && to !== 'USD')) return amountMinor
  return Math.round(from === 'USD' ? amountMinor * usdRate : amountMinor / usdRate)
}

// money shows an amount in the visitor's display currency; without a
// currency the amount is taken to be in the ledger's.
export function money(amountMinor: number, currency: string = ledger) {
  const display = displayCurrency()
  if (inAdmin() || currency === display || (currency !== 'CNY' && currency !== 'USD')) return format(amountMinor, currency)
  return format(convertMinor(amountMinor, currency, display), display)
}

// chargedMoney shows an amount in the currency it is stored and charged in.
export function chargedMoney(amountMinor: number, currency: string = ledger) {
  return format(amountMinor, currency)
}

// plainMoney shows minor units of a currency as they are.
export function plainMoney(amountMinor: number, currency: string) {
  return format(amountMinor, currency)
}

// converted reports whether money() shows something else than what is
// charged, i.e. whether a payment needs to say so.
export function converted(currency: string = ledger) {
  return !inAdmin() && currency !== displayCurrency()
}

// toLedgerMinor turns an amount typed in the display currency (in whole
// units, such as 12.5) into ledger minor units.
export function toLedgerMinor(amount: number) {
  return convertMinor(Math.round(amount * 100), displayCurrency(), ledger)
}

// fromLedgerMinor is the display-currency minor units of a ledger amount.
export function fromLedgerMinor(amountMinor: number) {
  return convertMinor(amountMinor, ledger, displayCurrency())
}

// usdRateText is the rate as a sentence fragment: 1 USD = 7.20 CNY.
export function usdRateText() {
  return `1 USD = ${usdRate.toFixed(2)} CNY`
}

// currencyName names a currency in a sentence.
export function currencyName(currency: string) {
  return currency === 'USD' ? t('美元（USD）') : currency === 'CNY' ? t('人民币（CNY）') : currency
}
