import { useEffect, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { AlertTriangle, CheckCircle2, Info, X } from 'lucide-react'

// Short notices that appear in a corner and leave by themselves, for the
// outcome of something the user started ("实例已关机"). Like the dialogs,
// the host mounts itself on first use.

export type ToastKind = 'success' | 'error' | 'info'
type Toast = { id: number; kind: ToastKind; title: string; message?: string }

let toasts: Toast[] = []
let render: ((items: Toast[]) => void) | null = null
let mounted = false
let lastID = 0

function publish() {
  render?.([...toasts])
}

export function toast(kind: ToastKind, title: string, message?: string) {
  const id = ++lastID
  toasts = [...toasts, { id, kind, title, message }].slice(-4)
  if (!mounted) {
    mounted = true
    const container = document.createElement('div')
    container.id = 'vpsbill-toasts'
    document.body.appendChild(container)
    createRoot(container).render(<ToastHost />)
  }
  publish()
  // Errors stay a little longer so they can be read.
  window.setTimeout(() => dismiss(id), kind === 'error' ? 8000 : 5000)
}

function dismiss(id: number) {
  toasts = toasts.filter(item => item.id !== id)
  publish()
}

const icons = { success: CheckCircle2, error: AlertTriangle, info: Info }

function ToastHost() {
  const [items, setItems] = useState<Toast[]>(toasts)
  useEffect(() => {
    render = setItems
    setItems([...toasts])
    return () => {
      render = null
    }
  }, [])
  return (
    <div className="toast-stack" role="status" aria-live="polite">
      {items.map(item => {
        const Icon = icons[item.kind]
        return (
          <div key={item.id} className={`toast toast-${item.kind}`}>
            <Icon size={17} aria-hidden />
            <div>
              <strong>{item.title}</strong>
              {item.message && <p>{item.message}</p>}
            </div>
            <button type="button" aria-label="关闭提示" onClick={() => dismiss(item.id)}>
              <X size={14} />
            </button>
          </div>
        )
      })}
    </div>
  )
}
