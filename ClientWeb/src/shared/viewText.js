// ClientWeb/src/shared/viewText.js
//
// 详情视图文本统一表示：查找高亮与「所见即所得复制」共用的文本化入口。
// 2026-08-27 升级：从 InlineDetailRow.getShownContent 抽离为纯函数，便于复用与测试。
// 2026-08-28 阶段AU：新增 viewsForTab —— 视图按钮按字段语义裁剪：
//   request_body 只有原文/JSON 美化（请求体不是 SSE 流）；
//   response_body 只有原文/SSE 解析/聚合解析（流式原文不是单个 JSON）。
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
 * 各字段允许的视图列表（顺序即 DetailTabs 渲染顺序）。
 * - request_body：请求体是单个 JSON 对象 → 原文 / JSON 美化；
 * - response_body：响应体可能是 SSE 流 → 原文 / SSE 解析 / 聚合解析
 *   （流式原文不是单个 JSON，「JSON 美化」必然解析失败，故不提供）；
 * - headers 类字段：纯文本，仅原文视图。
 *
 * @param {string} tab 字段名
 * @returns {string[]}
 */
export function viewsForTab(tab) {
  if (tab === 'request_body') return [VIEW_RAW, VIEW_JSON]
  if (tab === 'response_body') return [VIEW_RAW, VIEW_SSE, VIEW_AGG]
  return [VIEW_RAW]
}

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
