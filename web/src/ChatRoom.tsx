import { FormEvent, useEffect, useRef, useState } from 'react'
import { Send } from 'lucide-react'
import { api, type ChatHistoryRecord, type ChatMessageRecord } from './api'

const roleLabels: Record<string, string> = { host: '机主', buyer: '用户', staff: '平台', system: '系统' }

function websocketURL(path: string) {
  const scheme = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${scheme}//${window.location.host}${path}`
}

// ChatRoom shows one hosted node's room. History loads over HTTP; new
// messages arrive over a WebSocket and are also fetched after a reconnect,
// so nothing is lost while the socket is down.
export default function ChatRoom({ base, nodeID, title }: { base: string; nodeID: string; title: string }) {
  const [messages, setMessages] = useState<ChatMessageRecord[]>([])
  const [canPost, setCanPost] = useState(true)
  const [body, setBody] = useState('')
  const [sending, setSending] = useState(false)
  const [error, setError] = useState('')
  const [live, setLive] = useState(false)
  const list = useRef<HTMLDivElement>(null)
  const lastID = useRef(0)

  function merge(incoming: ChatMessageRecord[]) {
    if (!incoming.length) return
    setMessages(current => {
      const seen = new Set(current.map(item => item.id))
      const next = [...current, ...incoming.filter(item => !seen.has(item.id))].sort((a, b) => a.id - b.id)
      lastID.current = next.length ? next[next.length - 1].id : 0
      return next
    })
  }

  useEffect(() => {
    let closed = false
    let socket: WebSocket | null = null
    let retry: number | undefined
    let delay = 1000
    setMessages([])
    lastID.current = 0
    setError('')

    const catchUp = () =>
      api<ChatHistoryRecord>(`${base}/${nodeID}/messages${lastID.current ? `?after=${lastID.current}` : ''}`)
        .then(history => {
          setCanPost(history.can_post)
          merge(history.messages)
        })
        .catch(err => setError(err instanceof Error ? err.message : '加载聊天记录失败'))

    const connect = () => {
      if (closed) return
      socket = new WebSocket(websocketURL(`${base}/${nodeID}/stream`))
      socket.onopen = () => {
        delay = 1000
        setLive(true)
        void catchUp()
      }
      socket.onmessage = event => {
        try {
          merge([JSON.parse(String(event.data)) as ChatMessageRecord])
        } catch {
          // Ignore frames that are not messages.
        }
      }
      socket.onclose = () => {
        setLive(false)
        if (!closed) {
          retry = window.setTimeout(connect, delay)
          delay = Math.min(delay * 2, 30000)
        }
      }
    }

    void catchUp().then(connect)
    return () => {
      closed = true
      window.clearTimeout(retry)
      socket?.close()
    }
  }, [base, nodeID])

  useEffect(() => {
    list.current?.scrollTo({ top: list.current.scrollHeight })
  }, [messages.length])

  async function send(event: FormEvent<HTMLFormElement>) {
    event.preventDefault()
    const text = body.trim()
    if (!text) return
    setSending(true)
    setError('')
    try {
      const message = await api<ChatMessageRecord>(`${base}/${nodeID}/messages`, { method: 'POST', body: JSON.stringify({ body: text }) })
      merge([message])
      setBody('')
    } catch (err) {
      setError(err instanceof Error ? err.message : '发送失败')
    } finally {
      setSending(false)
    }
  }

  return (
    <div className="panel chat-room">
      <div className="panel-heading">
        <h3>{title}</h3>
        <span className={live ? 'tag success' : 'tag'}>{live ? '实时连接' : '连接中…'}</span>
      </div>
      <div className="chat-messages" ref={list}>
        {messages.map(message => (
          <div key={message.id} className={`chat-message ${message.author_type}${message.mine ? ' mine' : ''}`}>
            <div className="chat-meta">
              <strong>{message.author_name}</strong>
              <span className={`chat-role ${message.author_type}`}>{roleLabels[message.author_type] || message.author_type}</span>
              <time>{new Date(message.created_at).toLocaleString()}</time>
            </div>
            <p>{message.body}</p>
          </div>
        ))}
        {!messages.length && <div className="empty-state">还没有消息，打个招呼吧。</div>}
      </div>
      {error && <div className="form-error">{error}</div>}
      {canPost ? (
        <form className="chat-compose" onSubmit={send}>
          <textarea
            rows={2}
            maxLength={2000}
            value={body}
            placeholder="输入消息，Enter 发送，Shift+Enter 换行"
            onChange={event => setBody(event.target.value)}
            onKeyDown={event => {
              if (event.key === 'Enter' && !event.shiftKey && !event.nativeEvent.isComposing) {
                event.preventDefault()
                event.currentTarget.form?.requestSubmit()
              }
            }}
          />
          <button className="primary-button compact" disabled={sending || !body.trim()}>
            <Send size={14} />
            发送
          </button>
        </form>
      ) : (
        <p className="muted-text">母机已清退，聊天室只读。</p>
      )}
    </div>
  )
}
