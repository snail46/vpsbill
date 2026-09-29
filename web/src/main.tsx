import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { App } from './App'
import './styles.css'
import { initTheme } from './ThemeToggle'
import { initTableLabels } from './shared/tableLabels'

initTheme()
initTableLabels()

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <App />
  </StrictMode>,
)

