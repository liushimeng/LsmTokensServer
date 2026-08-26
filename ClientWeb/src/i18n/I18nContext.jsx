import { useCallback, useMemo, useState } from 'react'
import I18nContext from './context'
import { interpolate } from './interpolate'
import zhCN from './locales/zh-CN'
import en from './locales/en'
import ja from './locales/ja'

const LOCALE_STORAGE_KEY = 'lsm_locale'

export const SUPPORTED_LOCALES = [
  { code: 'zh-CN', label: '中文', flag: '🇨🇳' },
  { code: 'en', label: 'English', flag: '🇬🇧' },
  { code: 'ja', label: '日本語', flag: '🇯🇵' },
]

export { default as I18nContext } from './context'

const TRANSLATIONS = { 'zh-CN': zhCN, en, ja }

function getStoredLocale() {
  try {
    const stored = window.localStorage.getItem(LOCALE_STORAGE_KEY)
    if (stored && TRANSLATIONS[stored]) return stored
  } catch { /* ignore */ }
  const nav = (navigator.language || 'zh-CN').toLowerCase()
  if (nav.startsWith('en')) return 'en'
  if (nav.startsWith('ja')) return 'ja'
  return 'zh-CN'
}

export function I18nProvider({ children }) {
  const [locale, setLocaleState] = useState(getStoredLocale)

  const setLocale = useCallback((next) => {
    if (!TRANSLATIONS[next]) return
    setLocaleState(next)
    try { window.localStorage.setItem(LOCALE_STORAGE_KEY, next) } catch { /* ignore */ }
  }, [])

  const t = useCallback((key, vars) => {
    const dict = TRANSLATIONS[locale] || TRANSLATIONS['zh-CN']
    const fallback = TRANSLATIONS['zh-CN']
    let val = dict[key]
    if (val === undefined) val = fallback[key]
    if (val === undefined) return key
    return interpolate(val, vars)
  }, [locale])

  const value = useMemo(() => ({ locale, setLocale, t }), [locale, setLocale, t])

  return (
    <I18nContext.Provider value={value}>
      {children}
    </I18nContext.Provider>
  )
}
