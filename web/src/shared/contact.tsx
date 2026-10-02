import { Link2, Mail, MessageCircle, MessagesSquare, Phone, Send, Users, type LucideIcon } from 'lucide-react'
import { t } from './i18n'

// ContactLink is one way to reach the platform, set in 站点设置 and shown
// on the portal's "联系我们" page.
export type ContactLink = { kind: ContactKind; label: string; value: string; note?: string }
export type ContactKind = 'telegram' | 'qq' | 'wechat' | 'email' | 'phone' | 'discord' | 'link'
export type ContactInfo = { intro: string; links: ContactLink[] }

export const contactKinds: { kind: ContactKind; label: string; Icon: LucideIcon; placeholder: string; example: string }[] = [
  { kind: 'telegram', label: 'Telegram', Icon: Send, placeholder: t('@用户名 或 https://t.me/群组'), example: t('TG 客服') },
  { kind: 'qq', label: t('QQ / QQ 群'), Icon: Users, placeholder: t('群号 或 加群链接'), example: t('QQ 交流群') },
  { kind: 'wechat', label: t('微信'), Icon: MessageCircle, placeholder: t('微信号'), example: t('微信客服') },
  { kind: 'email', label: t('邮箱'), Icon: Mail, placeholder: 'support@example.com', example: t('品牌邮箱') },
  { kind: 'phone', label: t('电话'), Icon: Phone, placeholder: '+86 400 000 0000', example: t('客服电话') },
  { kind: 'discord', label: 'Discord', Icon: MessagesSquare, placeholder: t('https://discord.gg/邀请码'), example: t('Discord 社区') },
  { kind: 'link', label: t('其他链接'), Icon: Link2, placeholder: 'https://', example: t('官方博客') },
]

export function contactKind(kind: string) {
  return contactKinds.find(item => item.kind === kind) ?? contactKinds[contactKinds.length - 1]
}

const webLink = (value: string) => /^https?:\/\/[^\s]+$/i.test(value)

// contactHref is where a contact opens, or '' when it can only be copied
// (a QQ group number, a WeChat ID). Only web, mail and phone links are made.
export function contactHref(link: ContactLink) {
  const value = link.value.trim()
  if (webLink(value)) return value
  switch (link.kind) {
    case 'telegram': {
      const name = value.replace(/^@/, '')
      return /^[A-Za-z0-9_]{4,64}$/.test(name) ? `https://t.me/${name}` : ''
    }
    case 'email':
      return /^[^\s@]+@[^\s@]+$/.test(value) ? `mailto:${value}` : ''
    case 'phone': {
      const digits = value.replace(/[^\d+]/g, '')
      return digits.length >= 5 ? `tel:${digits}` : ''
    }
  }
  return ''
}
