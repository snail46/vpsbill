import { useEffect } from 'react'
import { prefetch } from '../api'

// usePrefetch loads what every page in the menu shows once the open page
// has had its turn, so clicking any of them shows it at once (each page
// still reloads its data when it opens).
export function usePrefetch(pages: Record<string, string[]>) {
  useEffect(() => {
    let idle = 0
    const timer = window.setTimeout(() => {
      const run = () => void prefetch(Object.values(pages).flat())
      if ('requestIdleCallback' in window) idle = window.requestIdleCallback(run, { timeout: 2000 })
      else run()
    }, 1500)
    return () => {
      window.clearTimeout(timer)
      if (idle) window.cancelIdleCallback(idle)
    }
  }, [pages])
}

// prefetchPage loads one page's data when the pointer reaches its menu
// item, a moment before the click.
export function prefetchPage(pages: Record<string, string[]>, page: string) {
  void prefetch(pages[page] ?? [])
}
