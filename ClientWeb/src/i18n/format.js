import { useContext } from 'react'
import I18nContext from './context'

// 区域映射：i18n locale → Intl locale
const INTL_LOCALE_MAP = {
  'zh-CN': 'zh-CN',
  'en': 'en-US',
  'ja': 'ja-JP',
}

function getIntlLocale(i18nLocale) {
  return INTL_LOCALE_MAP[i18nLocale] || 'zh-CN'
}

// 基础格式化函数（不依赖 Context，供非 React 场景使用）
function fmtTimeByLocale(v, intlLocale) {
  if (!v && v !== 0) return '-'
  const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v)
  if (isNaN(d.getTime())) return String(v)
  return new Intl.DateTimeFormat(intlLocale, {
    year: 'numeric', month: '2-digit', day: '2-digit',
    hour: '2-digit', minute: '2-digit', second: '2-digit',
    hour12: false,
  }).format(d)
}

function fmtNumByLocale(v, intlLocale) {
  if (v === null || v === undefined || v === '') return '-'
  const n = Number(v)
  if (isNaN(n)) return String(v)
  return new Intl.NumberFormat(intlLocale).format(n)
}

// 向后兼容的默认导出（使用 zh-CN 作为默认）
export function fmtTime(v) {
  return fmtTimeByLocale(v, 'zh-CN')
}

export function fmtNum(v) {
  return fmtNumByLocale(v, 'zh-CN')
}

// 字节数人性化
export function fmtBytes(v) {
  const n = Number(v)
  if (!n || isNaN(n)) return '0 B'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// 毫秒耗时人性化
export function fmtMs(v) {
  const n = Number(v)
  if (n === 0 || isNaN(n)) return '-'
  if (n < 1000) return `${n} ms`
  if (n < 60000) return `${(n / 1000).toFixed(2)} s`
  return `${Math.floor(n / 60000)} 分 ${Math.round((n % 60000) / 1000)} 秒`
}

// 从路由 query 初始化筛选条件
export function pickRouteQuery(query) {
  const q = query || new URLSearchParams()
  return {
    userName: q.get('user_name') || '',
    modelName: q.get('model_name') || '',
  }
}

// ---- 区域感知 Hook（在组件中使用）----

export function useI18nFormat() {
  const { locale } = useContext(I18nContext) || { locale: 'zh-CN' }
  const intlLocale = getIntlLocale(locale)
  return {
    locale,
    intlLocale,
    fmtTime: (v) => fmtTimeByLocale(v, intlLocale),
    fmtNum: (v) => fmtNumByLocale(v, intlLocale),
    fmtBytes,
    fmtMs,
    pickRouteQuery,
  }
}
