import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles.css'
import { initTheme } from './ThemeToggle'
import { initTableLabels } from './shared/tableLabels'
import { loadBoot } from './shared/boot'

initTheme()
initTableLabels()
// Without inline boot data (development), ask for it while the app code
// for this side of the site is still loading.
loadBoot().catch(() => undefined)

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

