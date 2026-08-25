// ClientWeb/src/shared/sse.js
//
// SSE（Server-Sent Events）解析与聚合共用工具。
// v2.0.7x 阶段AM：从 ChatAnalysis.jsx 原样抽离，零行为变更。
// 兼容旧版 lsmParseSSEEvent / lsmAggregateSSE 契约。
//
// 设计要点：
//   - 纯函数，无 React/DOM 依赖；
//   - 边界兜底：空字符串 / 非 SSE / 非 JSON data / 无空行分隔 全部安全；
//   - 返回值是普通对象/数组，便于其它页面直接 import 使用。

/**
 * 把一段 SSE 文本拆分成事件数组。
 *
 * 解析规则：
 *   - 按行扫描，遇到 `event:` 起始则开新事件；
 *   - `data:` 起始累加到当前事件的 data 数组（多行 data 用 '\n' 拼接）；
 *   - 空行 / 文本结束作为事件边界；
 *   - data 段尝试 JSON.parse；失败则 parsed = null，保留 raw 原文。
 *
 * 边界场景：
 *   - 空字符串 / undefined / null → 返回 []
 *   - 无空行分隔（如实时流尾部）→ 仍正确拆分最后一条
 *   - data 不是 JSON → 不抛异常
 *
 * @param {string} text 原始 SSE 文本
 * @returns {Array<{event: string, raw: string, parsed: any}>}
 */
export function parseSSEEvents(text) {
  if (!text) return []
  const events = []
  let cur = null
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith('event:')) {
      if (cur) events.push(cur)
      cur = { event: line.slice(6).trim(), data: [] }
    } else if (line.startsWith('data:')) {
      if (!cur) cur = { event: '', data: [] }
      cur.data.push(line.slice(5).trim())
    } else if (line === '' && cur) {
      events.push(cur); cur = null
    }
  }
  if (cur) events.push(cur)
  return events.map((e) => {
    const raw = e.data.join('\n')
    let parsed = null
    try { parsed = JSON.parse(raw) } catch { /* 非 JSON data */ }
    return { event: e.event, raw, parsed }
  })
}

/**
 * 把 SSE 流聚合成结构化结果：文本增量拼接 + 累加 usage + 工具调用列表 + 事件类型统计。
 *
 * 兼容协议：
 *   - Anthropic content_block_delta（delta.text / delta.partial_json）
 *   - OpenAI choices[0].delta.content / delta.reasoning_content
 *   - Anthropic content_block_start（tool_use → 工具名）
 *   - usage：顶层 usage 或 message.usage（取 input_tokens/output_tokens 累加；末帧覆盖 _final）
 *
 * @param {string} text 原始 SSE 文本
 * @returns {{textParts: string[], usage: null | {input_tokens: number, output_tokens: number, input_tokens_final?: number, output_tokens_final?: number}, toolCalls: string[], eventTypes: Object<string, number>}}
 */
export function aggregateSSE(text) {
  const events = parseSSEEvents(text)
  const out = { textParts: [], usage: null, toolCalls: [], eventTypes: {} }
  events.forEach((e) => {
    if (e.event) out.eventTypes[e.event] = (out.eventTypes[e.event] || 0) + 1
    const p = e.parsed
    if (!p) return
    if (p.type === 'content_block_delta' && p.delta) {
      if (p.delta.text) out.textParts.push(p.delta.text)
      if (p.delta.partial_json) out.textParts.push(p.delta.partial_json)
    } else if (p.choices && p.choices[0] && p.choices[0].delta) {
      const t = p.choices[0].delta.content || p.choices[0].delta.reasoning_content
      if (t) out.textParts.push(t)
    } else if (p.type === 'content_block_start' && p.content_block && p.content_block.type === 'tool_use') {
      out.toolCalls.push(p.content_block.name || '')
    }
    const u = p.usage || (p.message && p.message.usage)
    if (u) {
      out.usage = out.usage || {}
      out.usage.input_tokens = (out.usage.input_tokens || 0) + (u.input_tokens || 0)
      out.usage.output_tokens = (out.usage.output_tokens || 0) + (u.output_tokens || 0)
      if (u.input_tokens !== undefined) out.usage.input_tokens_final = u.input_tokens
      if (u.output_tokens !== undefined) out.usage.output_tokens_final = u.output_tokens
    }
  })
  return out
}

/**
 * 把 aggregateSSE 的结果序列化为可读文本（用于"复制当前视图"和纯文本视图）。
 *
 * @param {ReturnType<typeof aggregateSSE>} agg
 * @returns {string}
 */
export function aggregateToText(agg) {
  if (!agg) return ''
  const usage = agg.usage || {}
  const eventTypes = Object.entries(agg.eventTypes || {})
    .map(([k, v]) => `${k || '(default)'}×${v}`)
    .join('、') || '无'
  return [
    `事件类型分布: ${eventTypes}`,
    agg.toolCalls && agg.toolCalls.length ? `工具调用: ${agg.toolCalls.join('、')}` : '',
    `usage: input=${usage.input_tokens_final ?? usage.input_tokens ?? 0} output=${usage.output_tokens_final ?? usage.output_tokens ?? 0}`,
    '---- 聚合文本 ----',
    (agg.textParts || []).join('') || '（无文本增量）',
  ].filter(Boolean).join('\n')
}

/**
 * 把 parseSSEEvents 的结果序列化为可读文本（用于纯文本视图与"复制当前视图"）。
 *
 * @param {ReturnType<typeof parseSSEEvents>} events
 * @returns {string}
 */
export function sseEventsToText(events) {
  if (!events || !events.length) return '（未解析出 SSE 事件）'
  return events
    .map((e, i) => `# ${i + 1} event: ${e.event || '(default)'}\n${e.parsed ? JSON.stringify(e.parsed, null, 2) : e.raw}`)
    .join('\n\n')
}