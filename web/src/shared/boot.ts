import { useSyncExternalStore } from 'react'
import { adoptCache, type CustomerIdentity, type StaffUser } from '../api'
import type { Meta } from './ui'

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
let pending: Promise<Boot> | null = null

// inlineBoot is the boot data nginx put into index.html (see
// web/nginx/common.conf), read once: after that, sign-ins and sign-outs
// change what it described. It is null when missing (development, or the
// API was down).
export function inlineBoot(): Boot | null {
  if (inline !== undefined) return inline
  inline = null
  try {
    const parsed = JSON.parse(document.getElementById('vpsbill-boot')?.textContent || 'null') as { data?: Boot } | null
    if (parsed?.data?.meta) inline = parsed.data
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
    pending = data
      ? Promise.resolve(data)
      : fetch(`/api/v1/boot?surface=${surfaceFromPath()}`, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
          .then(response => {
            if (response.status === 404) return { data: null }
            return response.ok ? response.json() : Promise.reject(new Error('boot failed'))
          })
          .then(async (payload: { data: Boot | null }) => {
            const boot = payload.data ?? (await legacyBoot())
            applyBoot(boot)
            return boot
          })
    // A failed request may be retried by the next caller.
    pending.catch(() => {
      pending = null
    })
  }
  return pending
}

// legacyBoot asks an API from before /api/v1/boot, for the moments of an
// upgrade when the web image is newer than the API.
async function legacyBoot(): Promise<Boot> {
  const read = async <T,>(path: string): Promise<T | null> => {
    const response = await fetch(path, { credentials: 'same-origin', headers: { Accept: 'application/json' } })
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

// applyMeta shows the site name in the browser tab and the site logo.
function applyMeta(meta: Meta) {
  if (meta.name) document.title = meta.name
  setSiteLogo(meta.logo_url ?? '')
}

// The site logo is a small store so changing it in the settings updates
// every mark on the page at once.
let logo = ''
const listeners = new Set<() => void>()

export function setSiteLogo(url: string) {
  if (url === logo) return
  logo = url
  const icon = document.querySelector<HTMLLinkElement>('link[rel="icon"]')
  if (icon) icon.href = url || icon.dataset.default || ''
  listeners.forEach(listener => listener())
}

export function useSiteLogo() {
  return useSyncExternalStore(
    listener => {
      listeners.add(listener)
      return () => listeners.delete(listener)
    },
    () => logo,
  )
}
