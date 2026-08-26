import { useEffect, useRef, useState } from 'react'
import { SUPPORTED_LOCALES } from './I18nContext'
import { useI18n } from './useI18n'

export default function LanguageSwitcher() {
  const { locale, setLocale } = useI18n()
  const [open, setOpen] = useState(false)
  const ref = useRef(null)

  useEffect(() => {
    if (!open) return
    const onClick = (e) => {
      if (ref.current && !ref.current.contains(e.target)) setOpen(false)
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [open])

  const current = SUPPORTED_LOCALES.find((l) => l.code === locale) || SUPPORTED_LOCALES[0]

  return (
    <div className="lang-switcher" ref={ref}>
      <button
        className="lang-switcher-btn"
        onClick={() => setOpen(!open)}
        title="语言 / Language / 言語"
        aria-label="language switcher"
      >
        <span className="lang-switcher-flag">{current.flag}</span>
        <span className="lang-switcher-label">{current.label}</span>
        <span className="lang-switcher-arrow">{open ? '▲' : '▼'}</span>
      </button>
      {open && (
        <div className="lang-switcher-dropdown" role="listbox">
          {SUPPORTED_LOCALES.map((l) => (
            <button
              key={l.code}
              className={`lang-switcher-item${l.code === locale ? ' active' : ''}`}
              onClick={() => { setLocale(l.code); setOpen(false) }}
              role="option"
              aria-selected={l.code === locale}
            >
              <span className="lang-switcher-flag">{l.flag}</span>
              <span className="lang-switcher-item-label">{l.label}</span>
              {l.code === locale && <span className="lang-switcher-check">✓</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}
