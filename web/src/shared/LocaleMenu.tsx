import { useEffect, useRef, useState } from 'react'
import { Globe } from 'lucide-react'
import { t, lang, setLang } from './i18n'
import { chargedMoney, converted, currencyName, displayCurrency, ledgerCurrency, setDisplayCurrency, shownCurrencies, usdRateText } from './currency'

// LocaleMenu chooses the language and, in the portal, the currency amounts
// are shown in. Either choice reloads the page.
export function LocaleMenu() {
  const [open, setOpen] = useState(false)
  const ref = useRef<HTMLDivElement>(null)
  const currency = displayCurrency()
  // The currency is a choice only when the site shows more than one.
  const currencies = shownCurrencies().length > 1

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

  const label = currencies ? t('语言与币种') : t('语言')
  return (
    <div className="locale-menu" ref={ref}>
      <button type="button" className="locale-toggle" aria-haspopup="true" aria-expanded={open} aria-label={label} title={label} onClick={() => setOpen(value => !value)}>
        <Globe size={15} />
        <span>{lang === 'en' ? 'EN' : t('中')}{currencies ? ` · ${currency}` : ''}</span>
      </button>
      {open && (
        <div className="locale-popover">
          <p>{t('语言 / Language')}</p>
          <div className="locale-options">
            <button type="button" className={lang === 'zh' ? 'active' : ''} aria-pressed={lang === 'zh'} onClick={() => (lang === 'zh' ? setOpen(false) : setLang('zh'))}>{t('中文')}</button>
            <button type="button" className={lang === 'en' ? 'active' : ''} aria-pressed={lang === 'en'} onClick={() => (lang === 'en' ? setOpen(false) : setLang('en'))}>English</button>
          </div>
          {currencies && (
            <>
              <p>{t('显示币种')}</p>
              <div className="locale-options">
                <button type="button" className={currency === 'CNY' ? 'active' : ''} aria-pressed={currency === 'CNY'} onClick={() => (currency === 'CNY' ? setOpen(false) : setDisplayCurrency('CNY'))}>CNY ¥</button>
                <button type="button" className={currency === 'USD' ? 'active' : ''} aria-pressed={currency === 'USD'} onClick={() => (currency === 'USD' ? setOpen(false) : setDisplayCurrency('USD'))}>USD $</button>
              </div>
              <small>{t('账户按{0}记账；另一种币种的金额按 {1} 换算显示。', currencyName(ledgerCurrency()), usdRateText())}</small>
            </>
          )}
        </div>
      )}
    </div>
  )
}

// CurrencyNote tells a visitor reading amounts in another currency than
// the ledger's that they are converted; it shows nothing otherwise.
export function CurrencyNote() {
  if (!converted()) return null
  return (
    <div className="note-banner currency-note">
      {t('金额按 {0} 换算为{1}显示，仅供参考；账户余额和账单按{2}记账，实际扣款以括号内的金额为准。', usdRateText(), currencyName(displayCurrency()), currencyName(ledgerCurrency()))}
    </div>
  )
}

// Charged adds the amount actually charged beside a converted amount.
export function Charged({ minor, currency = ledgerCurrency() }: { minor: number; currency?: string }) {
  if (!converted(currency)) return null
  return <small className="charged-amount">{t('（{0}）', chargedMoney(minor, currency))}</small>
}
