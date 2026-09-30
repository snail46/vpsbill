import { FormEvent, useEffect, useRef, useState } from 'react'
import { createRoot } from 'react-dom/client'
import { AlertTriangle, HelpCircle, PenLine } from 'lucide-react'

// In-page replacements for window.confirm and window.prompt. Each call
// returns a promise; the dialogs share one host that mounts itself on first
// use, so no shell has to render anything.

type DialogOptions = {
  title: string
  message?: string
  confirmText?: string
  cancelText?: string
  // danger styles the confirm button red, for destructive actions.
  danger?: boolean
}

export type PromptOptions = DialogOptions & {
  label?: string
  defaultValue?: string
  placeholder?: string
  inputType?: 'text' | 'number'
  multiline?: boolean
  // required keeps the confirm button off while the input is blank.
  required?: boolean
  // validate returns a message shown under the input, or '' when fine.
  validate?: (value: string) => string
}

type Request = { id: number } & (
  | ({ kind: 'confirm'; resolve: (value: boolean) => void } & DialogOptions)
  | ({ kind: 'prompt'; resolve: (value: string | null) => void } & PromptOptions)
)

const queue: Request[] = []
let wake: (() => void) | null = null
let mounted = false
let lastID = 0

type NewRequest =
  | ({ kind: 'confirm'; resolve: (value: boolean) => void } & DialogOptions)
  | ({ kind: 'prompt'; resolve: (value: string | null) => void } & PromptOptions)

function enqueue(request: NewRequest) {
  queue.push({ ...request, id: ++lastID })
  if (!mounted) {
    mounted = true
    const container = document.createElement('div')
    container.id = 'vpsbill-dialogs'
    document.body.appendChild(container)
    createRoot(container).render(<DialogHost />)
  }
  wake?.()
}

// confirmDialog resolves true when the user confirms.
export function confirmDialog(options: DialogOptions | string): Promise<boolean> {
  const value = typeof options === 'string' ? { title: options } : options
  return new Promise(resolve => enqueue({ kind: 'confirm', resolve, ...value }))
}

// promptDialog resolves the entered text, or null when cancelled.
export function promptDialog(options: PromptOptions): Promise<string | null> {
  return new Promise(resolve => enqueue({ kind: 'prompt', resolve, ...options }))
}

function DialogHost() {
  const [current, setCurrent] = useState<Request | null>(null)

  useEffect(() => {
    const next = () => setCurrent(active => active ?? queue.shift() ?? null)
    wake = next
    next()
    return () => {
      wake = null
    }
  }, [])

  if (!current) return null
  const close = (result: boolean | string | null) => {
    if (current.kind === 'confirm') current.resolve(result === true)
    else current.resolve(typeof result === 'string' ? result : null)
    setCurrent(queue.shift() ?? null)
  }
  // A key per request remounts the dialog, so each starts fresh.
  return <Dialog key={current.id} request={current} onClose={close} />
}

function Dialog({ request, onClose }: { request: Request; onClose: (result: boolean | string | null) => void }) {
  const prompt = request.kind === 'prompt' ? request : null
  const [value, setValue] = useState(prompt?.defaultValue ?? '')
  const [touched, setTouched] = useState(false)
  const field = useRef<HTMLInputElement & HTMLTextAreaElement>(null)
  const confirmButton = useRef<HTMLButtonElement>(null)
  const problem = prompt?.validate?.(value) || (prompt?.required && !value.trim() ? '请填写内容' : '')

  useEffect(() => {
    const focus = field.current ?? confirmButton.current
    focus?.focus()
    if (field.current && prompt?.defaultValue) field.current.select()
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        event.preventDefault()
        onClose(null)
      }
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const submit = (event: FormEvent) => {
    event.preventDefault()
    if (!prompt) return onClose(true)
    setTouched(true)
    if (!problem) onClose(value.trim())
  }

  const Icon = request.danger ? AlertTriangle : prompt ? PenLine : HelpCircle
  return (
    <div className="modal-backdrop dialog-backdrop" onMouseDown={event => event.target === event.currentTarget && onClose(null)}>
      <form
        className={request.danger ? 'app-dialog danger' : 'app-dialog'}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby="app-dialog-title"
        onSubmit={submit}
      >
        <div className="app-dialog-body">
          <span className="app-dialog-icon" aria-hidden="true">
            <Icon size={20} />
          </span>
          <div>
            <h3 id="app-dialog-title">{request.title}</h3>
            {request.message && <p>{request.message}</p>}
            {prompt && (
              <label className="app-dialog-field">
                {prompt.label && <span>{prompt.label}</span>}
                {prompt.multiline ? (
                  <textarea ref={field} rows={3} value={value} placeholder={prompt.placeholder} onChange={event => setValue(event.target.value)} />
                ) : (
                  <input
                    ref={field}
                    type={prompt.inputType ?? 'text'}
                    value={value}
                    placeholder={prompt.placeholder}
                    onChange={event => setValue(event.target.value)}
                  />
                )}
                {touched && problem && <small className="app-dialog-error">{problem}</small>}
              </label>
            )}
          </div>
        </div>
        <footer>
          <button type="button" className="secondary-button compact" onClick={() => onClose(null)}>
            {request.cancelText ?? '取消'}
          </button>
          <button ref={confirmButton} type="submit" className={request.danger ? 'danger-button compact' : 'primary-button compact'}>
            {request.confirmText ?? '确定'}
          </button>
        </footer>
      </form>
    </div>
  )
}
