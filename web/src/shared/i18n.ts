// The site is written in Chinese; English is a dictionary keyed by the
// Chinese text (src/locale/en.ts). The language is fixed for the life of
// the page: choosing another one reloads it, so text built when a module
// loads (label tables and the like) is in the right language too.

export type Lang = 'zh' | 'en'

const storageKey = 'vpsbill-lang'

// siteLocale is the site's language and currency settings as inlined into
// the page (see boot.ts); empty without inline boot data.
type SiteLocale = { default_lang?: string; usd_enabled?: boolean; usd_rate?: number }

export function inlineSiteLocale(): SiteLocale {
  try {
    const parsed = JSON.parse(document.getElementById('vpsbill-boot')?.textContent || 'null') as { data?: { meta?: { locale?: SiteLocale } } } | null
    return parsed?.data?.meta?.locale ?? {}
  } catch {
    return {}
  }
}

function stored(): Lang | null {
  try {
    const value = window.localStorage.getItem(storageKey)
    return value === 'zh' || value === 'en' ? value : null
  } catch {
    return null
  }
}

// detect: the visitor's choice, else the browser's language when it is
// Chinese or English, else the site's default.
function detect(): Lang {
  const chosen = stored()
  if (chosen) return chosen
  for (const candidate of navigator.languages?.length ? navigator.languages : [navigator.language || '']) {
    const value = candidate.toLowerCase()
    if (value.startsWith('zh')) return 'zh'
    if (value.startsWith('en')) return 'en'
  }
  return inlineSiteLocale().default_lang === 'en' ? 'en' : 'zh'
}

export const lang: Lang = detect()

// numberLocale is the locale for dates and numbers in this language.
export const numberLocale = lang === 'en' ? 'en-US' : 'zh-CN'

let dictionary: Record<string, string> = {}
// patterns translate text with changing parts, mostly messages from the
// server: $1 puts a captured group as it is, %1 translates it first.
let patterns: [RegExp, string][] = []

export function registerLocale(words: Record<string, string>, rules: [RegExp, string][]) {
  dictionary = words
  patterns = rules
}

// loadLocale fetches the dictionary of the page's language; main.tsx waits
// for it before any page code runs.
export async function loadLocale() {
  document.documentElement.lang = lang === 'en' ? 'en' : 'zh-CN'
  if (lang !== 'en') return
  const module = await import('../locale/en')
  registerLocale(module.words, module.rules)
}

// fill puts args into {0}, {1}…; like React, it shows nothing for a
// boolean, null or undefined.
function fill(text: string, args: unknown[]) {
  if (!args.length) return text
  return text.replace(/\{(\d+)\}/g, (_, index: string) => {
    const value = args[Number(index)]
    return value == null || typeof value === 'boolean' ? '' : String(value)
  })
}

// t translates interface text; {0}, {1}… are replaced by args.
export function t(text: string, ...args: unknown[]): string {
  if (lang !== 'en') return fill(text, args)
  const found = dictionary[text]
  if (found === undefined && import.meta.env.DEV) missing.add(text)
  return fill(found ?? text, args)
}

// missing collects untranslated text in development (window.__missing).
const missing = new Set<string>()
if (import.meta.env.DEV) (window as unknown as { __missing: Set<string> }).__missing = missing

const chinese = /[一-鿿]/

// tr translates text that was not written in this code: server messages
// and descriptions stored with the data. Text it does not know stays as
// it is.
export function tr(text: string | undefined | null): string {
  if (!text) return ''
  if (lang !== 'en' || !chinese.test(text)) return text
  const exact = dictionary[text]
  if (exact !== undefined) return exact
  for (const [pattern, output] of patterns) {
    const match = pattern.exec(text)
    if (!match) continue
    return output.replace(/([$%])(\d)/g, (_, kind: string, index: string) => {
      const group = match[Number(index)] ?? ''
      return kind === '%' ? tr(group) : group
    })
  }
  if (import.meta.env.DEV) missing.add(text)
  return text
}

// setLang stores the choice and reloads the page in it.
export function setLang(next: Lang) {
  if (next === lang) return
  try {
    window.localStorage.setItem(storageKey, next)
  } catch {
    // Private mode: the language cannot be kept.
    return
  }
  window.location.reload()
}
