// 对话分析页面常量定义
// 从 ChatAnalysis.jsx 抽离，集中管理枚举、白名单、工具函数

// 详情字段白名单（与服务端 chatAnalysisDetailFieldColumns 对齐）
export const DETAIL_FIELDS = [
  { key: 'request_body', titleKey: 'chatAnalysis.requestBody' },
  { key: 'response_body', titleKey: 'chatAnalysis.responseBody' },
  { key: 'request_src_protocol_body', titleKey: 'chatAnalysis.requestSrcProtocolBody' },
  { key: 'response_src_protocol_body', titleKey: 'chatAnalysis.responseSrcProtocolBody' },
  { key: 'request_headers', titleKey: 'chatAnalysis.requestHeaders' },
  { key: 'response_headers', titleKey: 'chatAnalysis.responseHeaders' },
]

// 详情视图类型
export const VIEW_RAW = 'raw'
export const VIEW_JSON = 'json'
export const VIEW_SSE = 'sse'
export const VIEW_AGG = 'agg'

// 转发类型常量（与后端 DstEndPointAlgorithmType 对齐）
export const ALGO_TYPE_DIRECT = 1
export const ALGO_TYPE_CONVERTER = 2

// 分页大小选项
export const PAGE_SIZES = [3, 5, 10, 15, 20, 50, 100]

// localStorage 工具（带容错）
export const safeGet = (k) => { try { return window.localStorage.getItem(k) } catch { return null } }
export const safeSet = (k, v) => { try { window.localStorage.setItem(k, v) } catch { /* 忽略 */ } }

// 协议名称
export function protocolName(v, t) {
  return v === 1 ? t('chatAnalysis.anthropic') : v === 2 ? t('chatAnalysis.openai') : '-'
}

// 转发类型徽标
export function protocolBadgeClass(v) {
  if (v === ALGO_TYPE_DIRECT) return 'protocol-badge direct'
  if (v === ALGO_TYPE_CONVERTER) return 'protocol-badge converter'
  return 'protocol-badge unknown'
}

export function protocolBadgeText(v, t) {
  if (v === ALGO_TYPE_DIRECT) return t('chatAnalysis.direct')
  if (v === ALGO_TYPE_CONVERTER) return t('chatAnalysis.converter')
  return t('chatAnalysis.unknown')
}

export function protocolBadgeTitle(v, t) {
  if (v === ALGO_TYPE_DIRECT) return t('chatAnalysis.directTooltip')
  if (v === ALGO_TYPE_CONVERTER) return t('chatAnalysis.converterTooltip')
  return t('chatAnalysis.unknownTooltip')
}

// 视图标签映射（需要 t 函数）
export function viewLabels(t) {
  return {
    [VIEW_RAW]: t('chatAnalysis.raw'),
    [VIEW_JSON]: t('chatAnalysis.jsonBeautify'),
    [VIEW_SSE]: t('chatAnalysis.sseParse'),
    [VIEW_AGG]: t('chatAnalysis.aggParse'),
  }
}
