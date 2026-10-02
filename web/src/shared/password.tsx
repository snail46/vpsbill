import { useState } from 'react'
import { Check, Copy, Eye, EyeOff, Sparkles } from 'lucide-react'
import { t } from './i18n'

// Letters and digits without look-alikes (0/O, 1/l/I): nothing a node's
// password tooling must escape, and easy to read back from the screen.
const lower = 'abcdefghijkmnpqrstuvwxyz'
const upper = 'ABCDEFGHJKLMNPQRSTUVWXYZ'
const digits = '23456789'
const alphabet = lower + upper + digits

function pick(chars: string, count: number): string[] {
  const values = new Uint32Array(count)
  crypto.getRandomValues(values)
  // 2^32 is not a multiple of the alphabet sizes; the bias this leaves is
  // below 1e-8 and does not matter for a password.
  return Array.from(values, value => chars[value % chars.length])
}

// strongPassword is 20 random characters (about 117 bits) with at least
// one lowercase letter, one uppercase letter and one digit.
export function strongPassword(length = 20): string {
  const chars = [...pick(lower, 1), ...pick(upper, 1), ...pick(digits, 1), ...pick(alphabet, length - 3)]
  // Shuffle so the guaranteed characters are not always first.
  const order = new Uint32Array(chars.length)
  crypto.getRandomValues(order)
  for (let i = chars.length - 1; i > 0; i--) {
    const j = order[i] % (i + 1)
    ;[chars[i], chars[j]] = [chars[j], chars[i]]
  }
  return chars.join('')
}

// PasswordInput is a password field with show/hide, a generator and, once
// a password was generated, a copy button. It stays an uncontrolled form
// field named `name`.
export function PasswordInput({ name, placeholder, autoComplete = 'new-password' }: { name: string; placeholder?: string; autoComplete?: string }) {
  const [value, setValue] = useState('')
  const [visible, setVisible] = useState(false)
  const [copied, setCopied] = useState(false)

  const generate = () => {
    setValue(strongPassword())
    // Show what was generated, so it can be noted down.
    setVisible(true)
    setCopied(false)
  }
  const copy = () => {
    void navigator.clipboard?.writeText(value).then(() => {
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1600)
    })
  }

  return (
    <div className="password-input">
      <input
        name={name}
        type={visible ? 'text' : 'password'}
        value={value}
        placeholder={placeholder}
        autoComplete={autoComplete}
        spellCheck={false}
        onChange={event => setValue(event.target.value)}
      />
      <button type="button" className="icon-button" title={visible ? t('隐藏密码') : t('显示密码')} aria-label={visible ? t('隐藏密码') : t('显示密码')} onClick={() => setVisible(show => !show)}>
        {visible ? <EyeOff size={15} /> : <Eye size={15} />}
      </button>
      {value && visible && (
        <button type="button" className="icon-button" title={t('复制密码')} aria-label={t('复制密码')} onClick={copy}>
          {copied ? <Check size={15} /> : <Copy size={15} />}
        </button>
      )}
      <button type="button" className="secondary-button compact password-generate" onClick={generate}>
        <Sparkles size={14} />{t('随机生成')}
      </button>
    </div>
  )
}
