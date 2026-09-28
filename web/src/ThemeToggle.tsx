import { useEffect, useState } from 'react'
import { Moon, Sun } from 'lucide-react'

export type Theme = 'light' | 'dark'

const storageKey = 'vpsbill-theme'
const lightQuery = '(prefers-color-scheme: light)'

function storedTheme(): Theme | null {
  try {
    const value = window.localStorage.getItem(storageKey)
    return value === 'light' || value === 'dark' ? value : null
  } catch {
    return null
  }
}

function systemTheme(): Theme {
  return window.matchMedia?.(lightQuery).matches ? 'light' : 'dark'
}

function apply(theme: Theme) {
  document.documentElement.dataset.theme = theme
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', theme === 'light' ? '#f4f6fb' : '#0b1020')
}

// initTheme runs before the first render: the visitor's choice wins,
// otherwise the page follows the system setting.
export function initTheme() {
  apply(storedTheme() ?? systemTheme())
}

export function ThemeToggle() {
  const [theme, setTheme] = useState<Theme>(() => storedTheme() ?? systemTheme())

  useEffect(() => {
    if (storedTheme()) return
    const media = window.matchMedia?.(lightQuery)
    const follow = () => {
      if (storedTheme()) return
      const next = systemTheme()
      apply(next)
      setTheme(next)
    }
    media?.addEventListener('change', follow)
    return () => media?.removeEventListener('change', follow)
  }, [])

  const toggle = () => {
    const next: Theme = theme === 'light' ? 'dark' : 'light'
    try {
      window.localStorage.setItem(storageKey, next)
    } catch {
      // Private mode: the choice lasts for this page only.
    }
    apply(next)
    setTheme(next)
  }

  const label = theme === 'light' ? '切换到夜间主题' : '切换到白天主题'
  return (
    <button type="button" className="theme-toggle" onClick={toggle} aria-label={label} title={label}>
      {theme === 'light' ? <Moon size={16} /> : <Sun size={16} />}
    </button>
  )
}
