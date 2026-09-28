import { useEffect, useState } from 'react'
import { MailCheck } from 'lucide-react'
import { api } from './api'

// EmailVerifyBanner reminds a customer to confirm their address; buying,
// top-ups and publishing stay locked until they do.
export function EmailVerifyBanner({ email }: { email: string }) {
  const [message, setMessage] = useState('')
  const [busy, setBusy] = useState(false)
  async function resend() {
    setBusy(true)
    try {
      const result = await api<{ message?: string; email_verified?: boolean }>('/api/v1/customer/auth/verify-email/resend', { method: 'POST' })
      if (result.email_verified) {
        window.location.reload()
        return
      }
      setMessage(result.message || '验证邮件已发送')
    } catch (err) {
      setMessage(err instanceof Error ? err.message : '发送失败')
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="note-banner warn verify-banner">
      <MailCheck size={16} />
      <span>
        邮箱 {email} 还没有验证。请打开注册时收到的验证邮件；验证前不能下单、充值、发布母机或参与交易。
        {message && <strong> {message}</strong>}
      </span>
      <button className="secondary-button compact" disabled={busy} onClick={() => void resend()}>
        {busy ? '发送中…' : '重新发送'}
      </button>
    </div>
  )
}

// VerifyEmailPage handles the link from the verification mail.
export function VerifyEmailPage() {
  const [state, setState] = useState<'working' | 'done' | 'failed'>('working')
  const [message, setMessage] = useState('')
  useEffect(() => {
    const token = new URLSearchParams(window.location.search).get('token') || ''
    api('/api/v1/customer/auth/verify-email', { method: 'POST', body: JSON.stringify({ token }) })
      .then(() => setState('done'))
      .catch(err => {
        setState('failed')
        setMessage(err instanceof Error ? err.message : '验证失败')
      })
  }, [])
  return (
    <main className="session-loading">
      <div className="brand-mark">VB</div>
      {state === 'working' && <strong>正在验证邮箱…</strong>}
      {state === 'done' && <strong>邮箱已验证，现在可以正常下单和充值了。</strong>}
      {state === 'failed' && <strong>{message}</strong>}
      {state !== 'working' && (
        <a className="primary-button compact" href="/portal">进入客户中心</a>
      )}
    </main>
  )
}
