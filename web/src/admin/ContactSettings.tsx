import { FormEvent, useState } from 'react'
import { ArrowDown, ArrowUp, Plus, Trash2 } from 'lucide-react'
import { api } from '../api'
import { contactKind, contactKinds, type ContactInfo, type ContactKind, type ContactLink } from '../shared/contact'
import { t } from '../shared/i18n'

const maxLinks = 20

// ContactSettings edits the portal's "联系我们" page: a short intro and the
// ways to reach the platform, in the order customers see them.
export function ContactSettings({ intro: savedIntro, links: savedLinks }: ContactInfo) {
  const [intro, setIntro] = useState(savedIntro)
  const [links, setLinks] = useState<ContactLink[]>(savedLinks)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState('')
  const [notice, setNotice] = useState('')

  const change = (index: number, patch: Partial<ContactLink>) =>
    setLinks(current => current.map((link, i) => (i === index ? { ...link, ...patch } : link)))
  const move = (index: number, by: number) =>
    setLinks(current => {
      const next = [...current]
      const [item] = next.splice(index, 1)
      next.splice(index + by, 0, item)
      return next
    })
  const add = () => setLinks(current => [...current, { kind: 'telegram', label: '', value: '', note: '' }])

  async function save(event: FormEvent) {
    event.preventDefault()
    setBusy(true)
    setError('')
    setNotice('')
    try {
      const result = await api<ContactInfo>('/api/v1/admin/settings/contact', { method: 'PUT', body: JSON.stringify({ intro, links }) })
      setIntro(result.intro)
      setLinks(result.links)
      setNotice(t('已保存，客户刷新「联系我们」页面后即可看到。'))
    } catch (err) {
      setError(err instanceof Error ? err.message : t('保存失败'))
    } finally {
      setBusy(false)
    }
  }

  return (
    <form className="panel contact-settings" onSubmit={save}>
      <div className="panel-heading">
        <h3>{t('联系我们')}</h3>
        <span className="tag">{t('客户前台边栏')}</span>
      </div>
      <p className="muted-text">
        {t('显示在客户前台边栏底部的「联系我们」页面，按下面的顺序排列。Telegram 填 @用户名 或 t.me 链接，邮箱和电话可直接点开，QQ 群号、微信号提供复制。')}
      </p>
      <label className="field">
        <span>{t('页面说明（可留空）')}</span>
        <textarea rows={2} maxLength={500} value={intro} onChange={event => setIntro(event.target.value)} placeholder={t('如：客服在线时间 9:00–23:00（UTC+8），紧急故障请提交工单。')} />
      </label>
      <div className="contact-rows">
        {links.map((link, index) => {
          const kind = contactKind(link.kind)
          return (
            <div className="contact-row" key={index}>
              <select aria-label={t('类型')} value={link.kind} onChange={event => change(index, { kind: event.target.value as ContactKind })}>
                {contactKinds.map(item => <option key={item.kind} value={item.kind}>{item.label}</option>)}
              </select>
              <input aria-label={t('名称')} value={link.label} maxLength={30} onChange={event => change(index, { label: event.target.value })} placeholder={t('名称，如 {0}', kind.example)} required />
              <input aria-label={t('内容')} value={link.value} maxLength={300} onChange={event => change(index, { value: event.target.value })} placeholder={kind.placeholder} required />
              <input aria-label={t('备注')} value={link.note ?? ''} maxLength={100} onChange={event => change(index, { note: event.target.value })} placeholder={t('备注（可选），如 工作日回复')} />
              <div className="row-actions">
                <button type="button" className="icon-button" title={t('上移')} aria-label={t('上移')} disabled={index === 0} onClick={() => move(index, -1)}><ArrowUp size={14} /></button>
                <button type="button" className="icon-button" title={t('下移')} aria-label={t('下移')} disabled={index === links.length - 1} onClick={() => move(index, 1)}><ArrowDown size={14} /></button>
                <button type="button" className="icon-button" title={t('删除')} aria-label={t('删除')} onClick={() => setLinks(current => current.filter((_, i) => i !== index))}><Trash2 size={14} /></button>
              </div>
            </div>
          )
        })}
        {!links.length && <p className="pending-empty muted-text">{t('还没有联系方式，客户会看到「平台还没有公布联系方式」。')}</p>}
      </div>
      <div className="form-actions">
        <button type="button" className="secondary-button" disabled={links.length >= maxLinks} onClick={add}>
          <Plus size={15} />{t('添加联系方式')}
        </button>
        <button className="primary-button" disabled={busy}>{busy ? t('正在保存…') : t('保存联系方式')}</button>
      </div>
      {error && <div className="form-error">{error}</div>}
      {notice && <div className="success-note">{notice}</div>}
    </form>
  )
}
