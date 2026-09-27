import { useEffect, useMemo, useRef, useState } from 'react'
import { ImagePlus, X } from 'lucide-react'
import { api, type TicketAttachmentRecord } from './api'

// Matches the server: a message carries at most this many images.
export const maxAttachments = 5
const imageTypes = ['image/png', 'image/jpeg', 'image/gif', 'image/webp']

// useAttachmentLimit reads the per-image size limit the site allows.
export function useAttachmentLimit() {
  const [maxMB, setMaxMB] = useState(5)
  useEffect(() => {
    api<{ ticket_attachment_max_mb?: number }>('/api/v1/meta')
      .then(meta => {
        if (meta.ticket_attachment_max_mb) setMaxMB(meta.ticket_attachment_max_mb)
      })
      .catch(() => undefined)
  }, [])
  return maxMB
}

// ticketRequestBody sends plain JSON without images, and multipart with the
// same JSON in a "payload" field when images are attached.
export function ticketRequestBody(payload: Record<string, unknown>, files: File[]): string | FormData {
  if (!files.length) return JSON.stringify(payload)
  const form = new FormData()
  form.append('payload', JSON.stringify(payload))
  for (const file of files) form.append('attachments', file, file.name)
  return form
}

function formatSize(bytes: number) {
  return bytes >= 1024 * 1024 ? `${(bytes / 1024 / 1024).toFixed(1)} MB` : `${Math.max(1, Math.round(bytes / 1024))} KB`
}

export function AttachmentPicker({
  files,
  onChange,
  maxMB,
  onError,
}: {
  files: File[]
  onChange: (files: File[]) => void
  maxMB: number
  onError: (message: string) => void
}) {
  const input = useRef<HTMLInputElement>(null)
  const previews = useMemo(() => files.map(file => URL.createObjectURL(file)), [files])
  useEffect(() => () => previews.forEach(url => URL.revokeObjectURL(url)), [previews])

  function add(list: FileList | null) {
    if (!list) return
    const next = [...files]
    for (const file of Array.from(list)) {
      if (!imageTypes.includes(file.type)) {
        onError(`${file.name} 不是支持的图片格式（PNG、JPEG、GIF、WebP）`)
        continue
      }
      if (file.size > maxMB * 1024 * 1024) {
        onError(`${file.name} 超过 ${maxMB} MB`)
        continue
      }
      if (next.length >= maxAttachments) {
        onError(`每条消息最多 ${maxAttachments} 张图片`)
        break
      }
      next.push(file)
    }
    onChange(next)
    if (input.current) input.current.value = ''
  }

  return (
    <div className="attachment-picker">
      <div className="attachment-thumbs">
        {files.map((file, index) => (
          <figure key={`${file.name}-${index}`}>
            <img src={previews[index]} alt={file.name} />
            <figcaption>{formatSize(file.size)}</figcaption>
            <button type="button" aria-label={`移除 ${file.name}`} onClick={() => onChange(files.filter((_, i) => i !== index))}>
              <X size={12} />
            </button>
          </figure>
        ))}
        {files.length < maxAttachments && (
          <button type="button" className="attachment-add" onClick={() => input.current?.click()}>
            <ImagePlus size={18} />
            <span>添加图片</span>
          </button>
        )}
      </div>
      <small>
        最多 {maxAttachments} 张，每张不超过 {maxMB} MB，支持 PNG、JPEG、GIF、WebP
      </small>
      <input
        ref={input}
        type="file"
        accept={imageTypes.join(',')}
        multiple
        hidden
        onChange={event => add(event.target.files)}
      />
    </div>
  )
}

export function AttachmentGallery({
  ticketID,
  attachments,
  admin,
  base: override,
}: {
  ticketID: string
  attachments?: TicketAttachmentRecord[]
  admin: boolean
  base?: string
}) {
  if (!attachments?.length) return null
  const base = override || (admin ? '/api/v1/admin/tickets' : '/api/v1/customer/tickets')
  return (
    <div className="attachment-gallery">
      {attachments.map(item => {
        const url = `${base}/${ticketID}/attachments/${item.id}`
        return (
          <a key={item.id} href={url} target="_blank" rel="noopener" title={`${item.file_name} · ${formatSize(item.size_bytes)}`}>
            <img src={url} alt={item.file_name} loading="lazy" />
          </a>
        )
      })}
    </div>
  )
}
