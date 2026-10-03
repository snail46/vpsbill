import { useSyncExternalStore } from 'react'
import { adoptCache, siteFetch, type CustomerIdentity, type StaffUser } from '../api'
import type { Meta } from './ui'
import { setCurrencyRates } from './currency'

// Boot is what a page needs before it can render: the site facts, whether
// installation is pending and who is signed in on this side of the site.
export type Boot = {
  meta: Meta
  install_required: boolean
  staff: StaffUser | null
  customer: CustomerIdentity | null
}

export type Surface = 'admin' | 'portal'

export function surfaceFromPath(): Surface {
  return window.location.pathname.startsWith('/admin') ? 'admin' : 'portal'
}

let inline: Boot | null | undefined
let inlineStale = false
let pending: Promise<Boot> | null = null

// inlineBoot is the boot data nginx put into index.html (see
// web/nginx/common.conf), read once: after that, sign-ins and sign-outs
// change what it described. It is null when missing (development, or the
// API was down).
export function inlineBoot(): Boot | null {
  if (inline !== undefined) return inline
  inline = null
  try {
    const element = document.getElementById('vpsbill-boot')
    const parsed = JSON.parse(element?.textContent || 'null') as { data?: Boot } | null
    if (parsed?.data?.meta) inline = parsed.data
    // The service worker served a stored copy of the page (public/sw.js):
    // render with it, then check it (freshBoot).
    inlineStale = element?.dataset.stale === '1'
  } catch {
    // an SSI error or an old HTML file; fetch it instead
  }
  if (inline) applyBoot(inline)
  return inline
}

// loadBoot resolves the boot data: inline when present, otherwise from
// the API. main.tsx starts it before the app code loads so the two
// requests overlap.
export function loadBoot(): Promise<Boot> {
  if (!pending) {
    const data = inlineBoot()
    pending = data ? Promise.resolve(data) : fetchBoot()
    // A failed request may be retried by the next caller.
    pending.catch(() => {
      pending = null
    })
  }
  return pending
}

function fetchBoot(): Promise<Boot> {
  return siteFetch(`/api/v1/boot?surface=${surfaceFromPath()}`, { credentials: 'same-origin', headers: { Accept: 'application/json' }, cache: 'no-store' })
    .then(response => {
      if (response.status === 404) return { data: null }
      return response.ok ? response.json() : Promise.reject(new Error('boot failed'))
    })
    .then(async (payload: { data: Boot | null }) => {
      const boot = payload.data ?? (await legacyBoot())
      applyBoot(boot)
      return boot
    })
}

// freshBoot asks the API again when the page was a stored copy, whose boot
// data may be out of date; it resolves null when there is nothing to check.
export function freshBoot(): Promise<Boot | null> {
  inlineBoot()
  return inlineStale ? fetchBoot() : Promise.resolve(null)
}

// legacyBoot asks an API from before /api/v1/boot, for the moments of an
// upgrade when the web image is newer than the API.
async function legacyBoot(): Promise<Boot> {
  const read = async <T,>(path: string): Promise<T | null> => {
    const response = await siteFetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
    return response.ok ? ((await response.json()) as { data: T }).data : null
  }
  const admin = surfaceFromPath() === 'admin'
  const [meta, install, user] = await Promise.all([
    read<Meta>('/api/v1/meta'),
    read<{ required: boolean }>('/api/v1/install'),
    read<StaffUser | CustomerIdentity>(admin ? '/api/v1/auth/me' : '/api/v1/customer/auth/me'),
  ])
  if (!meta) throw new Error('boot failed')
  return {
    meta,
    install_required: Boolean(install?.required),
    staff: admin ? (user as StaffUser | null) : null,
    customer: admin ? null : (user as CustomerIdentity | null),
  }
}

// applyBoot scopes the data cache to whoever is signed in and shows the
// site name and logo.
function applyBoot(boot: Boot) {
  adoptCache(boot.staff?.id ?? boot.customer?.id ?? '')
  applyMeta(boot.meta)
}

let meta: Meta | null = null

// siteMeta is the site facts from the last boot, for settings a page reads
// once (such as the trading market's holding period).
export function siteMeta() {
  return meta
}

// applyMeta shows the site name in the browser tab and the site logo.
function applyMeta(value: Meta) {
  meta = value
  setCurrencyRates(value.locale)
  if (value.name) document.title = value.name
  setSiteLogo(value.logo_url ?? '', value.logo_mode, value.logo_dark_url ?? '')
  setSiteFavicon(value.favicon_url ?? '')
}

// LogoMode is how the logo sits beside the site name: auto decides by the
// image's shape, icon keeps the name beside it, wordmark (a logo that
// already carries the brand name) takes the name's place.
export type LogoMode = 'auto' | 'icon' | 'wordmark'

// The site logo is a small store so changing it in the settings updates
// every mark on the page at once.
// The dark theme shows logoDark when there is one, the light logo
// otherwise; the store follows the page's theme (data-theme on <html>).
let lightLogo = ''
let logoDark = ''
let logoMode: LogoMode = 'auto'
let logo = ''
let brand: { logo: string; mode: LogoMode } = { logo, mode: logoMode }
const listeners = new Set<() => void>()
let watchingTheme = false
// favicon is the browser tab icon set apart from the logo ('' = the logo).
let favicon = ''

function applyFavicon() {
  const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (icon) icon.href = favicon || logo || icon.dataset.default || ''
}

// setSiteFavicon sets the browser tab icon; '' shows the logo there.
export function setSiteFavicon(url: string) {
  favicon = url
  applyFavicon()
}

function darkTheme() {
  return document.documentElement.dataset.theme === 'dark'
}

// refresh picks the logo for the current theme and tells the marks.
function refresh() {
  const next = darkTheme() && logoDark ? logoDark : lightLogo
  if (next === logo && brand.mode === logoMode) return
  const changed = next !== logo
  logo = next
  if (changed) applyFavicon()
  brand = { logo: next, mode: logoMode }
  listeners.forEach(listener => listener())
}

// setSiteLogo sets the light logo, how it shows and, unless undefined, the
// dark theme's logo ('' = use the light one there too).
export function setSiteLogo(url: string, mode?: LogoMode, dark?: string) {
  lightLogo = url
  logoMode = mode ?? logoMode
  if (dark !== undefined) logoDark = dark
  if (!watchingTheme && typeof MutationObserver !== 'undefined') {
    watchingTheme = true
    new MutationObserver(refresh).observe(document.documentElement, { attributes: true, attributeFilter: ['data-theme'] })
  }
  refresh()
}

function subscribe(listener: () => void) {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function useSiteLogo() {
  return useSyncExternalStore(subscribe, () => logo)
}

// useSiteBrand is the logo with how to show it.
export function useSiteBrand() {
  return useSyncExternalStore(subscribe, () => brand)
}
