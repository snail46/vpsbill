import { Globe } from 'lucide-react'
import { lang, t } from './i18n'

// Countries and territories are named by the browser; these are the names
// it gives too long or too formal for a label.
const overrides: Record<'zh' | 'en', Record<string, string>> = {
  zh: { AE: '阿联酋', HK: '香港', MO: '澳门', TW: '台湾' },
  en: { HK: 'Hong Kong', MO: 'Macao', TW: 'Taiwan', KR: 'South Korea', RU: 'Russia', VN: 'Vietnam', LA: 'Laos' },
}

function namer(language: 'zh' | 'en') {
  try {
    return new Intl.DisplayNames([language === 'zh' ? 'zh-CN' : 'en'], { type: 'region', style: language === 'zh' ? 'short' : 'long' })
  } catch {
    return null
  }
}

const namers = { zh: namer('zh'), en: namer('en') }

// countryName names a two-letter country code; '' is everything that could
// not be placed.
export function countryName(code: string, language: 'zh' | 'en' = lang) {
  if (!code) return t('其他地区')
  const fixed = overrides[language][code]
  if (fixed) return fixed
  try {
    return namers[language]?.of(code) || code
  } catch {
    return code
  }
}

// Flag draws a country's flag from /flags (flag emoji are missing on
// Windows); a code without a picture shows nothing, '' shows a globe.
export function Flag({ code }: { code: string }) {
  if (!code) return <Globe className="flag flag-none" size={16} aria-hidden="true" />
  return (
    <img
      className="flag"
      src={`/flags/${code.toLowerCase()}.svg`}
      alt=""
      loading="lazy"
      onError={event => {
        event.currentTarget.style.visibility = 'hidden'
      }}
    />
  )
}

// CountryLabel is "🇭🇰 HK 香港".
export function CountryLabel({ code }: { code: string }) {
  return (
    <span className="country-label">
      <Flag code={code} />
      {code && <b>{code}</b>}
      <span>{countryName(code)}</span>
    </span>
  )
}
