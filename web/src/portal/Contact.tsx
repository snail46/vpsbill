import { useEffect, useState } from 'react'
import { Check, Copy, ExternalLink, Headphones } from 'lucide-react'
import { freshBoot, siteMeta } from '../shared/boot'
import { contactHref, contactKind, type ContactInfo } from '../shared/contact'
import { navigatePortal } from '../shared/nav'

// CustomerContact lists the ways to reach the platform that staff set in
// 站点设置. They arrive with the boot data, so the page needs no request.
export function CustomerContact() {
  const [contact, setContact] = useState<ContactInfo>(() => siteMeta()?.contact ?? { intro: '', links: [] })
  const [copied, setCopied] = useState(-1)

  useEffect(() => {
    // A page restored from the offline copy may hold older boot data.
    void freshBoot().then(boot => boot?.meta.contact && setContact(boot.meta.contact)).catch(() => {})
  }, [])

  const copy = (value: string, index: number) => {
    void navigator.clipboard.writeText(value)
    setCopied(index)
    window.setTimeout(() => setCopied(current => (current === index ? -1 : current)), 2000)
  }

  return (
    <section className="workspace-panel">
      <div className="page-actions">
        <div>
          <p className="eyebrow">CONTACT</p>
          <h2>联系我们</h2>
          <p className="contact-intro">{contact.intro || '售前咨询、使用问题或合作洽谈，都可以通过下面的方式联系我们。'}</p>
        </div>
      </div>

      {contact.links.length ? (
        <div className="contact-grid">
          {contact.links.map((link, index) => {
            const { Icon, label } = contactKind(link.kind)
            const href = contactHref(link)
            return (
              <article className="contact-card" key={index}>
                <span className="contact-icon"><Icon size={18} aria-hidden /></span>
                <div className="contact-body">
                  <small>{label}</small>
                  <strong>{link.label}</strong>
                  <code>{link.value}</code>
                  {link.note && <p>{link.note}</p>}
                </div>
                <div className="contact-actions">
                  {href && (
                    <a className="secondary-button compact" href={href} target={href.startsWith('http') ? '_blank' : undefined} rel="noopener noreferrer">
                      <ExternalLink size={14} />打开
                    </a>
                  )}
                  <button type="button" className="text-button" onClick={() => copy(link.value, index)}>
                    {copied === index ? <Check size={14} /> : <Copy size={14} />}
                    {copied === index ? '已复制' : '复制'}
                  </button>
                </div>
              </article>
            )
          })}
        </div>
      ) : (
        <div className="empty-card">平台还没有公布联系方式。</div>
      )}

      <div className="contact-ticket">
        <Headphones size={16} aria-hidden />
        <span>实例故障、账单和退款等需要核对账户的问题，请提交工单，处理进度可随时查看。</span>
        <button type="button" className="secondary-button compact" onClick={() => navigatePortal('/portal/support')}>提交工单</button>
      </div>
    </section>
  )
}
