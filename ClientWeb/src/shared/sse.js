// ClientWeb/src/shared/sse.js
//
// SSE（Server-Sent Events）解析与聚合共用工具。
// v2.0.7x 阶段AM：从 ChatAnalysis.jsx 原样抽离，零行为变更。
// 兼容旧版 lsmParseSSEEvent / lsmAggregateSSE 契约。
// v2.0.7x 阶段AR：全面增强 SSE/聚合解析的兼容性 ——
//   1) parseSSEEvents：非流式纯 JSON 响应自动封装为单条 complete 事件，
//      避免"暂无数据"误导；跳过 data: [DONE] 终止行。
//   2) aggregateSSE：支持非流式完整响应的 usage / text / tool_calls 提取；
//      兼容 Anthropic 完整响应、OpenAI 完整响应。
//   3) sseEventsToText / aggregateToText 同步适配新事件格式。
//
// 设计要点：
//   - 纯函数，无 React/DOM 依赖；
//   - 边界兜底：空字符串 / 非 SSE / 非 JSON data / 无空行分隔 全部安全；
//   - 返回值是普通对象/数组，便于其它页面直接 import 使用。

/**
 * 判断一段文本是否是 SSE 流格式。
 * 判定标准：包含至少一行以 "data:" 或 "event:" 开头，且有空行分隔多个事件。
 *
 * @param {string} text
 * @returns {boolean}
 */
function isSSEFormat(text) {
  if (!text) return false
  // 至少有一行 data: 或 event: 前缀，且包含空行分隔（或结尾有典型终止符）
  const lines = text.split(/\r?\n/)
  let hasDataOrEvent = false
  let hasBlank = false
  for (const line of lines) {
    if (line.startsWith('data:') || line.startsWith('event:')) hasDataOrEvent = true
    if (line === '') hasBlank = true
  }
  // 单条事件也可能没有空行；宽松判定：有 data:/event: 开头即视为 SSE 格式
  return hasDataOrEvent
}

/**
 * 把一段 SSE 文本拆分成事件数组。
 *
 * 解析规则：
 *   - 按行扫描，遇到 `event:` 起始则开新事件；
 *   - `data:` 起始累加到当前事件的 data 数组（多行 data 用 '\n' 拼接）；
 *   - 空行 / 文本结束作为事件边界；
 *   - data 段尝试 JSON.parse；失败则 parsed = null，保留 raw 原文。
 *
 * 阶段AR 增强：
 *   - 跳过 `data: [DONE]` / `data: [done]` 终止行（OpenAI 流常见）；
 *   - 非流式纯 JSON 响应：自动封装为单条 `event: 'complete'` 事件，
 *     方便 SSE 列表与聚合视图统一展示，避免"暂无数据"。
 *
 * 边界场景：
 *   - 空字符串 / undefined / null → 返回 []
 *   - 无空行分隔（如实时流尾部）→ 仍正确拆分最后一条
 *   - data 不是 JSON → 不抛异常
 *
 * @param {string} text 原始 SSE 文本或非流式 JSON 响应
 * @returns {Array<{event: string, raw: string, parsed: any, synthetic?: boolean}>}
 */
export function parseSSEEvents(text) {
  if (!text) return []

  // 阶段AR：非 SSE 格式的纯文本 → 尝试作为完整 JSON 响应封装为单条 complete 事件
  if (!isSSEFormat(text)) {
    const trimmed = text.trim()
    if (trimmed.length === 0) return []
    let parsed = null
    try { parsed = JSON.parse(trimmed) } catch { /* 非 JSON 文本，直接返回空 */ }
    if (parsed === null) return []
    return [{
      event: 'complete',
      raw: trimmed,
      parsed,
      synthetic: true, // 标记为"非流式完整响应"，UI 可特殊展示
    }]
  }

  const events = []
  let cur = null
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith('event:')) {
      if (cur) events.push(cur)
      cur = { event: line.slice(6).trim(), data: [] }
    } else if (line.startsWith('data:')) {
      const dataContent = line.slice(5).trim()
      // 阶段AR：跳过 [DONE] 终止行
      if (dataContent === '[DONE]' || dataContent === '[done]') continue
      if (!cur) cur = { event: '', data: [] }
      cur.data.push(dataContent)
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
 * 兼容协议（流式）：
 *   - Anthropic content_block_delta（delta.text / delta.partial_json）
 *   - OpenAI choices[0].delta.content / delta.reasoning_content
 *   - Anthropic content_block_start（tool_use → 工具名）
 *   - usage：顶层 usage 或 message.usage（取 input_tokens/output_tokens 累加；末帧覆盖 _final）
 *
 * 阶段AR 新增（非流式完整响应）：
 *   - Anthropic 完整响应：content[].text / content[].input / usage / stop_reason
 *   - OpenAI 完整响应：choices[0].message.content / choices[0].message.tool_calls / usage
 *   - 事件类型显示为 `complete`
 *
 * @param {string} text 原始 SSE 文本或非流式 JSON 响应
 * @returns {{textParts: string[], usage: null | {input_tokens: number, output_tokens: number, input_tokens_final?: number, output_tokens_final?: number}, toolCalls: string[], eventTypes: Object<string, number>, isComplete?: boolean}}
 */
export function aggregateSSE(text) {
  // null/空入参 → 空结构（不带 isComplete 字段，保持与历史断言兼容）
  if (!text) {
    return { textParts: [], usage: null, toolCalls: [], eventTypes: {} }
  }
  const events = parseSSEEvents(text)
  const out = { textParts: [], usage: null, toolCalls: [], eventTypes: {}, isComplete: false }

  // 阶段AR：单条 complete 事件（非流式完整响应）→ 直接从 JSON 提取结构化信息
  if (events.length === 1 && events[0].event === 'complete' && events[0].synthetic) {
    out.isComplete = true
    out.eventTypes['complete'] = 1
    const p = events[0].parsed
    if (p && typeof p === 'object') {
      // Anthropic 完整响应格式：content: [{type: 'text', text: '...'}]
      if (Array.isArray(p.content)) {
        for (const block of p.content) {
          if (block && typeof block === 'object') {
            if (block.type === 'text' && typeof block.text === 'string') out.textParts.push(block.text)
            else if (block.type === 'tool_use' && block.name) out.toolCalls.push(String(block.name))
            else if (block.type === 'input_json' && block.input && typeof block.input === 'string') {
              out.textParts.push(block.input)
            }
          }
        }
      }
      // OpenAI 完整响应格式：choices[0].message.content / tool_calls
      if (Array.isArray(p.choices) && p.choices[0] && p.choices[0].message) {
        const msg = p.choices[0].message
        if (typeof msg.content === 'string' && msg.content) out.textParts.push(msg.content)
        if (Array.isArray(msg.tool_calls)) {
          for (const tc of msg.tool_calls) {
            if (tc && tc.function && tc.function.name) out.toolCalls.push(String(tc.function.name))
          }
        }
        // reasoning_content（推理模型）
        if (typeof msg.reasoning_content === 'string' && msg.reasoning_content) {
          out.textParts.push(`\n\n<think>\n${msg.reasoning_content}\n</think>\n`)
        }
      }
      // usage：Anthropic 在顶层 (input_tokens/output_tokens)，
      // OpenAI 在顶层或 choices[0]，字段名 prompt_tokens/completion_tokens
      let usageObj = null
      if (p.usage && typeof p.usage === 'object') usageObj = p.usage
      else if (Array.isArray(p.choices) && p.choices[0] && p.choices[0].usage) usageObj = p.choices[0].usage
      if (usageObj) {
        out.usage = {}
        // 兼容 Anthropic (input_tokens/output_tokens) 与 OpenAI (prompt_tokens/completion_tokens)
        const inTok = Number(usageObj.input_tokens || usageObj.prompt_tokens || 0) || 0
        const outTok = Number(usageObj.output_tokens || usageObj.completion_tokens || 0) || 0
        out.usage.input_tokens = inTok
        out.usage.output_tokens = outTok
        out.usage.input_tokens_final = inTok
        out.usage.output_tokens_final = outTok
        // 兼容 Anthropic 的 cache_creation_input_tokens 等扩展字段
        if (usageObj.cache_creation_input_tokens !== undefined) {
          out.usage.cache_creation_input_tokens = Number(usageObj.cache_creation_input_tokens) || 0
        }
        if (usageObj.cache_read_input_tokens !== undefined) {
          out.usage.cache_read_input_tokens = Number(usageObj.cache_read_input_tokens) || 0
        }
      }
    }
    return out
  }

  // 流式 SSE 聚合（原有逻辑）
  events.forEach((e) => {
    // 阶段AV：与 SseEventList / aggregateToText 对齐 —— OpenAI 协议等 data:-only
    // 流（无 event: 行）的事件用 '(default)' 兜底计入 eventTypes，避免同一段流
    // 在 SSE 解析（Summary bar 显示 (default) ×N）与聚合解析（之前显示 none），
    // 两视图给出不同的"统计"。
    const eventName = e.event || '(default)'
    out.eventTypes[eventName] = (out.eventTypes[eventName] || 0) + 1
    const p = e.parsed
    if (!p) return
    if (p.type === 'content_block_delta' && p.delta) {
      if (p.delta.text) out.textParts.push(p.delta.text)
      if (p.delta.partial_json) out.textParts.push(p.delta.partial_json)
    } else if (p.choices && p.choices[0] && p.choices[0].delta) {
      const t = p.choices[0].delta.content || p.choices[0].delta.reasoning_content
      if (t) out.textParts.push(t)
      // 阶段AY：OpenAI 流式工具调用聚合（delta.tool_calls[].function.name）
      const dToolCalls = p.choices[0].delta.tool_calls
      if (Array.isArray(dToolCalls)) {
        for (const tc of dToolCalls) {
          if (tc && tc.function && tc.function.name) out.toolCalls.push(String(tc.function.name))
        }
      }
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
  const completeLabel = agg.isComplete ? '（非流式完整响应）' : ''
  return [
    `事件类型分布: ${eventTypes}${completeLabel}`,
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
    .map((e, i) => {
      const label = e.synthetic ? '完整响应 (non-stream)' : `# ${i + 1} event: ${e.event || '(default)'}`
      return `${label}\n${e.parsed ? JSON.stringify(e.parsed, null, 2) : e.raw}`
    })
    .join('\n\n')
}

/**
 * 阶段AW：判定一段字符串是否为合法 JSON object/array。
 *
 * 与 `parseJsonSafely` 同源判定 —— 必须是 object / array 才算 JSON 片段，
 * 避免把纯数字 / 字符串文本误识别为 JSON（例如 textParts 里夹带的 "42" 或
 * "true" 不应触发美化）。
 *
 * @param {string} raw
 * @returns {{ ok: true, data: object|any[] } | { ok: false }}
 */
export function tryParseJsonObject(raw) {
  if (typeof raw !== 'string') return { ok: false }
  const s = raw.trim()
  if (!s) return { ok: false }
  // 第一个非空白字符必须为 { 或 [ —— 快速过滤标量与格式不一致的输入
  const head = s[0]
  if (head !== '{' && head !== '[') return { ok: false }
  let parsed
  try { parsed = JSON.parse(s) } catch { return { ok: false } }
  if (parsed === null || typeof parsed !== 'object') return { ok: false }
  return { ok: true, data: parsed }
}

/**
 * 阶段AW：把聚合出的文本片段数组按"是否为合法 JSON object/array"切分为带类型的
 * 片段序列，供 AggregateView 渲染时按片段类型选择渲染器（普通片段走 <pre>，
 * JSON 片段走 JsonTree —— 与 JSON 美化按钮行为一致）。
 *
 * 片段顺序与输入一致；不修改原数组；空串片段归为文本片段（不会触发美化）。
 *
 * @param {string[]} textParts
 * @returns {Array<{ kind: 'text'|'json', value: string|object }>}
 */
export function splitAggregateTextParts(textParts) {
  if (!Array.isArray(textParts)) return []
  const out = []
  for (const part of textParts) {
    if (typeof part !== 'string') {
      out.push({ kind: 'text', value: String(part == null ? '' : part) })
      continue
    }
    const r = tryParseJsonObject(part)
    if (r.ok) {
      out.push({ kind: 'json', value: r.data })
    } else {
      out.push({ kind: 'text', value: part })
    }
  }
  return out
}