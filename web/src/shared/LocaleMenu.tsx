import { useEffect, useRef, useState } from 'react'
import { Globe } from 'lucide-react'
import { lang, setLang } from './i18n'
import { chargedMoney, converted, displayCurrency, setDisplayCurrency, usdAvailable, usdRateText } from './currency'

// LocaleMenu chooses the language and, in the portal, the currency amounts
// are shown in. Either choice reloads the page.
export function LocaleMenu() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const currency = displayCurrency()
  const currencies = usdAvailable()

  useEffect(() => {
    if (!open) return
    const outside = (event: PointerEvent) => {
      if (!ref.current?.contains(event.target as Node)) setOpen(false)
    }
    const escape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('pointerdown', outside)
    document.addEventListener('keydown', escape)
    return () => {
      document.removeEventListener('pointerdown', outside)
      document.removeEventListener('keydown', escape)
    }
  }, [open])

  const label = currencies ? '语言与币种' : '语言'
  return (
    <div className="locale-menu" ref={ref}>
      <button type="button" className="locale-toggle" aria-haspopup="true" aria-expanded={open} aria-label={label} title={label} onClick={() => setOpen(value => !value)}>
        <Globe size={15} />
        <span>{lang === 'en' ? 'EN' : '中'}{currencies ? ` · ${currency}` : ''}</span>
      </button>
      {open && (
        <div className="locale-popover">
          <p>语言 / Language</p>
          <div className="locale-options">
            <button type="button" className={lang === 'zh' ? 'active' : ''} aria-pressed={lang === 'zh'} onClick={() => (lang === 'zh' ? setOpen(false) : setLang('zh'))}>中文</button>
            <button type="button" className={lang === 'en' ? 'active' : ''} aria-pressed={lang === 'en'} onClick={() => (lang === 'en' ? setOpen(false) : setLang('en'))}>English</button>
          </div>
          {currencies && (
            <>
              <p>显示币种</p>
              <div className="locale-options">
                <button type="button" className={currency === 'CNY' ? 'active' : ''} aria-pressed={currency === 'CNY'} onClick={() => (currency === 'CNY' ? setOpen(false) : setDisplayCurrency('CNY'))}>CNY ¥</button>
                <button type="button" className={currency === 'USD' ? 'active' : ''} aria-pressed={currency === 'USD'} onClick={() => (currency === 'USD' ? setOpen(false) : setDisplayCurrency('USD'))}>USD $</button>
              </div>
              <small>美元金额按 {usdRateText()} 换算显示，付款统一按人民币（CNY）结算。</small>
            </>
          )}
        </div>
      )}
    </div>
  )
}

// CurrencyNote tells a visitor reading USD amounts that payments are
// charged in CNY; it shows nothing otherwise.
export function CurrencyNote() {
  if (!converted()) return null
  return (
    <div className="note-banner currency-note">
      金额按 {usdRateText()} 换算为美元显示，仅供参考；充值和付款时统一按人民币（CNY）结算，实付金额以括号内的人民币金额为准。
    </div>
  )
}

// Charged adds the CNY amount actually charged beside a converted amount.
export function Charged({ minor, currency = 'CNY' }: { minor: number; currency?: string }) {
  if (!converted(currency)) return null
  return <small className="charged-amount">（{chargedMoney(minor, currency)}）</small>
}
