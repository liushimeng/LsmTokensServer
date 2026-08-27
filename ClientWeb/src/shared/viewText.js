// ClientWeb/src/shared/viewText.js
//
// 详情视图文本统一表示：查找高亮与「所见即所得复制」共用的文本化入口。
// 2026-08-27 升级：从 InlineDetailRow.getShownContent 抽离为纯函数，便于复用与测试。
//
// 契约：
//   - 输入：字段名 tab（request_headers/request_body/response_headers/response_body）、
//     视图 view（raw/json/sse/agg）、原始字符串 value；
//   - 输出：当前视图下用户「看到」的完整文本；
//   - headers 类字段与 raw 视图一律原样返回；
//   - 纯函数，无 React/DOM 依赖。

import { prettyJSON } from './json.js'
import { parseSSEEvents, aggregateSSE, aggregateToText, sseEventsToText } from './sse.js'

export const VIEW_RAW = 'raw'
export const VIEW_JSON = 'json'
export const VIEW_SSE = 'sse'
export const VIEW_AGG = 'agg'

/**
 * 计算某字段在指定视图下的展示文本。
 *
 * @param {string} tab  字段名（含 'body' 视为 body 类字段）
 * @param {string} view 视图类型：raw / json / sse / agg
 * @param {string} value 原始内容
 * @returns {string}
 */
export function buildViewText(tab, view, value) {
  const v = value || ''
  if (!tab || !tab.includes('body')) return v
  switch (view) {
    case VIEW_JSON:
      return prettyJSON(v)
    case VIEW_SSE:
      return sseEventsToText(parseSSEEvents(v))
    case VIEW_AGG:
      return aggregateToText(aggregateSSE(v))
    case VIEW_RAW:
    default:
      return v
  }
}
