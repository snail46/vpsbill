import { useEffect, useRef, useState } from 'react'
import { Terminal } from '@xterm/xterm'
import { FitAddon } from '@xterm/addon-fit'
import '@xterm/xterm/css/xterm.css'
import RFBModule from '@novnc/novnc/lib/rfb'
import { Maximize2, Minimize2, Minus, Monitor, RefreshCw, Send, TerminalSquare, X } from 'lucide-react'
import { api, ConsoleTicketRecord } from './api'
import { t } from './shared/i18n'

type RFBInstance = EventTarget & {
  scaleViewport: boolean
  resizeSession: boolean
  focusOnClick: boolean
  qualityLevel: number
  compressionLevel: number
  background: string
  disconnect(): void
  sendCtrlAltDel(): void
}

type RFBConstructor = new (
  target: HTMLElement,
  url: string,
  options?: { wsProtocols?: string[] }
) => RFBInstance

const RFB = (typeof RFBModule === 'function' ? RFBModule : (RFBModule as { default?: unknown })?.default) as RFBConstructor

function websocketURL(path: string) {
  return `${location.protocol === 'https:' ? 'wss:' : 'ws:'}//${location.host}${path}`
}

export function ServiceConsole({
  serviceID,
  name,
  kind,
  onClose,
}: {
  serviceID: string
  name: string
  kind: 'ssh' | 'vnc'
  onClose: () => void
}) {
  const target = useRef<HTMLDivElement>(null)
  const terminal = useRef<Terminal | null>(null)
  const fit = useRef<FitAddon | null>(null)
  const socket = useRef<WebSocket | null>(null)
  const rfb = useRef<RFBInstance | null>(null)
  const [status, setStatus] = useState(t('正在连接…'))
  const [error, setError] = useState('')
  const panel = useRef<HTMLElement>(null)
  // minimized docks the console in a corner with the session still open;
  // full fills the screen (the browser's full screen where it has one, else
  // the whole window).
  const [minimized, setMinimized] = useState(false)
  const [full, setFull] = useState(false)

  useEffect(() => {
    const sync = () => {
      if (!document.fullscreenElement) setFull(false)
      requestAnimationFrame(() => fit.current?.fit())
    }
    document.addEventListener('fullscreenchange', sync)
    return () => document.removeEventListener('fullscreenchange', sync)
  }, [])

  const refit = () =>
    requestAnimationFrame(() => {
      fit.current?.fit()
      terminal.current?.focus()
    })

  // The layout switches at once; the browser's full screen follows when it
  // grants it (iOS Safari has none, and the window-filling layout stands in).
  function toggleFull() {
    setMinimized(false)
    if (full) {
      setFull(false)
      if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined)
    } else {
      setFull(true)
      void panel.current?.requestFullscreen?.().catch(() => undefined)
    }
    refit()
  }

  function minimize() {
    setFull(false)
    setMinimized(true)
    if (document.fullscreenElement) void document.exitFullscreen().catch(() => undefined)
  }

  function restore() {
    setMinimized(false)
    refit()
  }

  const cleanup = () => {
    socket.current?.close()
    socket.current = null
    terminal.current?.dispose()
    terminal.current = null
    rfb.current?.disconnect()
    rfb.current = null
    if (target.current) target.current.innerHTML = ''
  }

  const connect = async () => {
    if (!target.current) return
    cleanup()
    setStatus(t('正在获取控制台票据…'))
    setError('')

    try {
      const ticket = await api<ConsoleTicketRecord>(
        `/api/v1/customer/services/${serviceID}/console/${kind}/ticket`,
        { method: 'POST' }
      )

      if (kind === 'ssh') {
        const term = new Terminal({
          cursorBlink: true,
          convertEol: true,
          fontFamily: '"JetBrains Mono", Consolas, Menlo, monospace',
          fontSize: 13,
          lineHeight: 1.35,
          theme: {
            background: '#040711',
            foreground: '#e2e8f0',
            cursor: '#34d399',
            selectionBackground: 'rgba(99, 102, 241, 0.4)',
          },
        })
        const addon = new FitAddon()
        term.loadAddon(addon)
        term.open(target.current)
        terminal.current = term
        fit.current = addon
        requestAnimationFrame(() => addon.fit())

        const ws = new WebSocket(websocketURL(ticket.websocket_path), [`clicd-ticket.${ticket.ticket}`])
        ws.binaryType = 'arraybuffer'
        socket.current = ws

        ws.onopen = () => {
          setStatus(t('已连接'))
          addon.fit()
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
          term.focus()
        }

        ws.onmessage = async event => {
          if (event.data instanceof ArrayBuffer) term.write(new Uint8Array(event.data))
          else if (event.data instanceof Blob) term.write(new Uint8Array(await event.data.arrayBuffer()))
          else term.write(String(event.data))
        }

        ws.onerror = () => {
          setStatus(t('连接失败'))
          setError(t('无法建立终端连接，请确认实例正在运行且 SSH 服务正常启动。'))
        }

        ws.onclose = () => setStatus(current => (current === t('连接失败') ? current : t('已断开')))

        term.onData(data => {
          if (ws.readyState === WebSocket.OPEN) ws.send(new TextEncoder().encode(data))
        })

        const observer = new ResizeObserver(() => {
          addon.fit()
          if (ws.readyState === WebSocket.OPEN) {
            ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }))
          }
        })
        observer.observe(target.current)
      } else {
        const instance = new RFB(target.current, websocketURL(ticket.websocket_path), {
          wsProtocols: ['binary', `clicd-vnc-ticket.${ticket.ticket}`],
        })
        rfb.current = instance
        instance.scaleViewport = true
        instance.resizeSession = false
        instance.focusOnClick = true
        instance.qualityLevel = 6
        instance.compressionLevel = 2
        instance.background = '#040711'

        instance.addEventListener('connect', () => setStatus(t('已连接')))
        instance.addEventListener('disconnect', () => setStatus(t('已断开')))
        instance.addEventListener('securityfailure', () => {
          setStatus(t('连接失败'))
          setError(t('VNC 安全认证协商失败'))
        })
      }
    } catch (err) {
      setStatus(t('连接失败'))
      setError(err instanceof Error ? err.message : t('控制台连接失败'))
    }
  }

  useEffect(() => {
    void connect()
    return cleanup
  }, [serviceID, kind])

  return (
    <div className={minimized ? 'console-dock-host' : 'modal-backdrop'}>
      <section ref={panel} className={['console-modal', minimized && 'minimized', full && 'full'].filter(Boolean).join(' ')}>
        <header onDoubleClick={() => (minimized ? restore() : undefined)}>
          <div className="console-title">
            {kind === 'ssh' ? <TerminalSquare size={18} /> : <Monitor size={18} />}
            <strong>{t('{0} 控制台 · {1}', kind.toUpperCase(), name)}</strong>
            <span className={status === t('已连接') ? 'status-badge online' : status === t('连接失败') ? 'status-badge error' : 'status-badge'}>{status}</span>
          </div>
          <div className="console-actions">
            {kind === 'vnc' && !minimized && (
              <button className="secondary-button" onClick={() => rfb.current?.sendCtrlAltDel()}>
                <Send size={13} />Ctrl+Alt+Del
              </button>
            )}
            {!minimized && (
              <button className="icon-button" onClick={() => void connect()} title={t('重新连接')} aria-label={t('重新连接')}>
                <RefreshCw size={15} />
              </button>
            )}
            {minimized ? (
              <button className="icon-button" onClick={restore} title={t('还原')} aria-label={t('还原控制台')}>
                <Maximize2 size={15} />
              </button>
            ) : (
              <>
                <button className="icon-button" onClick={minimize} title={t('缩小（连接保持）')} aria-label={t('缩小控制台')}>
                  <Minus size={16} />
                </button>
                <button className="icon-button" onClick={toggleFull} title={full ? t('退出全屏') : t('全屏')} aria-label={full ? t('退出全屏') : t('全屏')}>
                  {full ? <Minimize2 size={15} /> : <Maximize2 size={15} />}
                </button>
              </>
            )}
            <button className="icon-button" onClick={onClose} title={t('关闭控制台')} aria-label={t('关闭控制台')}>
              <X size={16} />
            </button>
          </div>
        </header>
        <div ref={target} className="console-screen" />
        {error && <div className="console-error">{error}</div>}
      </section>
    </div>
  )
}
