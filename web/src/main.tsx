import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './styles.css'
import { initTheme } from './ThemeToggle'
import { initTableLabels } from './shared/tableLabels'
import { loadBoot } from './shared/boot'
import { initFormValidation } from './shared/validation'
import { loadLocale } from './shared/i18n'

initTheme()
initTableLabels()
initFormValidation()
// Without inline boot data (development), ask for it while the app code
// for this side of the site is still loading.
loadBoot().catch(() => undefined)

// On HTTPS a service worker opens repeat visits from the browser's copy of
// the page (public/sw.js); plain HTTP pages cannot have one.
if (import.meta.env.PROD && 'serviceWorker' in navigator && window.isSecureContext) {
  window.addEventListener('load', () => void navigator.serviceWorker.register('/sw.js').catch(() => undefined))
}

// A page from before a release may ask for a bundle the server no longer
// has; load the current release instead (once, so it cannot loop).
window.addEventListener('vite:preloadError', event => {
  try {
    const last = Number(sessionStorage.getItem('vpsbill:reloaded') || 0)
    if (Date.now() - last < 60000) return
    sessionStorage.setItem('vpsbill:reloaded', String(Date.now()))
  } catch {
    return
  }
  event.preventDefault()
  window.location.reload()
})

// The page's language is settled before any page code loads, because
// modules build their label tables as they load (see shared/i18n.ts).
void loadLocale()
  .catch(() => undefined)
  .then(() => import('./App'))
  .then(({ App }) => {
    createRoot(document.getElementById('root')!).render(
      <StrictMode>
        <App />
      </StrictMode>,
    )
  })

