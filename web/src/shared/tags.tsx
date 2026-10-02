import { KeyboardEvent, useState } from 'react'
import { X } from 'lucide-react'
import { t } from './i18n'

// The server keeps at most this many tags of this length (see
// cleanPlanLabels).
export const maxTags = 8
export const maxTagLength = 16

// TagInput edits a short list of labels: type one and press Enter (or a
// comma), click × to drop one. Pasting "a, b, c" adds all three.
export function TagInput({ value, onChange, placeholder }: { value: string[]; onChange: (tags: string[]) => void; placeholder?: string }) {
  const [draft, setDraft] = useState('')
  const [hint, setHint] = useState('')

  function add(text: string) {
    const next = [...value]
    for (const raw of text.split(/[,，、\n]/)) {
      const tag = raw.trim().replace(/\s+/g, ' ')
      if (!tag || next.includes(tag)) continue
      if ([...tag].length > maxTagLength) {
        setHint(t('每个标签最多 {0} 个字', maxTagLength))
        return
      }
      if (next.length >= maxTags) {
        setHint(t('最多 {0} 个标签', maxTags))
        break
      }
      next.push(tag)
    }
    onChange(next)
    setDraft('')
  }

  function onKeyDown(event: KeyboardEvent<HTMLInputElement>) {
    if (event.key === 'Enter' || event.key === ',' || event.key === '，') {
      event.preventDefault()
      add(draft)
    } else if (event.key === 'Backspace' && !draft && value.length) {
      onChange(value.slice(0, -1))
    }
  }

  return (
    <div className="tag-input">
      <div className="tag-input-box">
        {value.map(tag => (
          <span key={tag} className="tag-chip">
            {tag}
            <button type="button" aria-label={t('删除标签 {0}', tag)} onClick={() => onChange(value.filter(item => item !== tag))}>
              <X size={12} />
            </button>
          </span>
        ))}
        <input
          value={draft}
          placeholder={value.length ? '' : placeholder}
          onChange={event => {
            setHint('')
            if (/[,，、]/.test(event.target.value)) add(event.target.value)
            else setDraft(event.target.value)
          }}
          onKeyDown={onKeyDown}
          onBlur={() => draft.trim() && add(draft)}
        />
      </div>
      <small className="field-hint">{hint || t('回车或逗号添加，最多 {0} 个，每个不超过 {1} 字', maxTags, maxTagLength)}</small>
    </div>
  )
}
