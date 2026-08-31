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
// v2.0.78 阶段BH：聚合解析新增「完整响应 JSON 重组」（mergeSSEEvents）——
//   1) Anthropic 流：message_start 骨架 + content_block_start/delta/stop 逐块重组
//      （text/thinking/signature 增量合并、tool_use input 由 partial_json 增量拼装成对象）
//      + message_delta 回填 stop_reason/stop_sequence/usage；
//   2) OpenAI 流：chunk 骨架（id/object/created/model）+ delta.role/content/
//      reasoning_content 拼接 + delta.tool_calls 按 index 累积 id/name/arguments
//      + finish_reason + 末帧 usage；
//   3) 非流式完整响应：parsed 原样透传；
//   4) aggregateSSE 结果新增 merged / mergedProtocol 字段（只增不改，向后兼容），
//      aggregateToText 追加「完整响应 JSON」段（merged 为空时输出与旧版逐字节一致）。
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
 * 阶段BH 新增（完整响应 JSON 重组）：
 *   - merged：把流式事件重组为"等价非流式完整响应"的 JSON 对象（无法识别协议时 null）
 *   - mergedProtocol：'anthropic' | 'openai' | null
 *
 * @param {string} text 原始 SSE 文本或非流式 JSON 响应
 * @returns {{textParts: string[], usage: null | {input_tokens: number, output_tokens: number, input_tokens_final?: number, output_tokens_final?: number}, toolCalls: string[], eventTypes: Object<string, number>, isComplete?: boolean, merged: object|null, mergedProtocol: string|null}}
 */
export function aggregateSSE(text) {
  // null/空入参 → 空结构（不带 isComplete/merged 字段，保持与历史断言兼容）
  if (!text) {
    return { textParts: [], usage: null, toolCalls: [], eventTypes: {} }
  }
  const events = parseSSEEvents(text)
  const out = { textParts: [], usage: null, toolCalls: [], eventTypes: {}, isComplete: false, merged: null, mergedProtocol: null }

  // 阶段AR：单条 complete 事件（非流式完整响应）→ 直接从 JSON 提取结构化信息
  if (events.length === 1 && events[0].event === 'complete' && events[0].synthetic) {
    out.isComplete = true
    out.eventTypes['complete'] = 1
    const p = events[0].parsed
    if (p && typeof p === 'object') {
      // 阶段BH：非流式完整响应 → merged 原样透传（协议按结构特征推断）
      out.merged = p
      out.mergedProtocol = Array.isArray(p.choices)
        ? 'openai'
        : (Array.isArray(p.content) || p.type === 'message' ? 'anthropic' : null)
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

  // 阶段BH：流式事件 → 重组完整响应 JSON 对象
  const { merged, protocol } = mergeSSEEvents(events)
  out.merged = merged
  out.mergedProtocol = protocol
  return out
}

// ============================================================================
// 阶段BH：SSE 流式事件 → 完整响应 JSON 重组
// ============================================================================

/**
 * 深拷贝（SSE data 全部来自 JSON.parse，JSON 往返克隆安全且无 undefined 语义）。
 * @param {*} v
 * @returns {*}
 */
function deepClone(v) {
  return v === undefined ? undefined : JSON.parse(JSON.stringify(v))
}

/**
 * 识别单条事件所属协议。
 * @param {*} p 事件 parsed JSON
 * @returns {'anthropic'|'openai'|null}
 */
function detectEventProtocol(p) {
  if (!p || typeof p !== 'object') return null
  if (typeof p.type === 'string' &&
      (p.type.startsWith('message_') || p.type.startsWith('content_block_') || p.type === 'message')) {
    return 'anthropic'
  }
  if (Array.isArray(p.choices) || p.object === 'chat.completion.chunk' || p.object === 'chat.completion') {
    return 'openai'
  }
  return null
}

/**
 * Anthropic Messages 流式事件 → 完整 message 对象重组。
 *
 * 事件语义（依据 Anthropic Messages streaming 规范）：
 *   - message_start.message            → message 骨架（id/type/role/model/content[]/usage）
 *   - content_block_start.content_block → content[index] 块起点（text/tool_use/thinking）
 *   - content_block_delta.delta        → 按 index 定位块增量合并：
 *       text_delta.text / thinking_delta.thinking / signature_delta.signature 直接拼接；
 *       input_json_delta.partial_json 先按原文累积，content_block_stop 时 JSON.parse 为
 *       tool_use.input（解析失败则保留原文字符串，保证无损）
 *   - message_delta.delta              → stop_reason / stop_sequence 回填；usage 覆盖合并
 *
 * 截断流兜底：未见 message_start 时延迟创建默认骨架；未见 content_block_stop 的
 * partial_json 在收尾时统一解析。
 *
 * @param {Array<{parsed:any}>} events
 * @returns {object|null} 完整 message 对象；一条可识别事件都没有时 null
 */
function mergeAnthropicStream(events) {
  let msg = null
  const blocks = new Map()      // index → content block 对象
  const partialJson = new Map() // index → input_json_delta 累积原文
  const ensureMsg = () => {
    if (!msg) msg = { type: 'message', role: 'assistant', content: [] }
    if (!Array.isArray(msg.content)) msg.content = []
    return msg
  }

  for (const e of events) {
    const p = e.parsed
    if (!p || typeof p !== 'object') continue

    if (p.type === 'message_start' && p.message && typeof p.message === 'object') {
      msg = deepClone(p.message)
      // 骨架字段归一（个别实现 message_start.message 缺 type/role）
      if (msg.type === undefined) msg.type = 'message'
      if (msg.role === undefined) msg.role = 'assistant'
      if (!Array.isArray(msg.content)) msg.content = []
      continue
    }
    if (p.type === 'content_block_start' && p.content_block && typeof p.content_block === 'object') {
      ensureMsg()
      // 缺 index 的实现按"追加到末尾"处理
      const idx = typeof p.index === 'number' ? p.index : blocks.size
      const block = deepClone(p.content_block)
      // 增量字段的初值归一，避免后续 += 产生 "undefinedxxx"
      if (block.type === 'text' && typeof block.text !== 'string') block.text = ''
      if (block.type === 'thinking' && typeof block.thinking !== 'string') block.thinking = ''
      if (block.type === 'tool_use' && block.input === undefined) block.input = {}
      blocks.set(idx, block)
      continue
    }
    if (p.type === 'content_block_delta' && p.delta && typeof p.delta === 'object') {
      ensureMsg()
      // 缺 index 的实现按"继续最近一个块"处理（无块时从 0 开始）
      let idx = p.index
      if (typeof idx !== 'number') {
        idx = blocks.size ? Math.max(...blocks.keys()) : 0
      }
      let block = blocks.get(idx)
      if (!block) {
        // 截断流：未见 content_block_start 的文本增量 → 按纯 text 块兜底
        block = { type: 'text', text: '' }
        blocks.set(idx, block)
      }
      const d = p.delta
      if (typeof d.text === 'string') block.text = (block.text || '') + d.text
      else if (typeof d.thinking === 'string') block.thinking = (block.thinking || '') + d.thinking
      else if (typeof d.signature === 'string') block.signature = (block.signature || '') + d.signature
      else if (typeof d.partial_json === 'string') {
        partialJson.set(idx, (partialJson.get(idx) || '') + d.partial_json)
      }
      continue
    }
    if (p.type === 'content_block_stop' && typeof p.index === 'number') {
      finalizeToolInput(blocks.get(p.index), partialJson.get(p.index))
      partialJson.delete(p.index)
      continue
    }
    if (p.type === 'message_delta') {
      ensureMsg()
      if (p.delta && typeof p.delta === 'object') {
        if (p.delta.stop_reason !== undefined) msg.stop_reason = p.delta.stop_reason
        if (p.delta.stop_sequence !== undefined) msg.stop_sequence = p.delta.stop_sequence
      }
      if (p.usage && typeof p.usage === 'object') {
        msg.usage = Object.assign({}, msg.usage, p.usage)
      }
      continue
    }
  }

  if (!msg) return null

  // 截断流兜底：未收到 content_block_stop 的 partial_json 统一在收尾解析
  for (const [idx, block] of blocks) {
    if (partialJson.has(idx)) finalizeToolInput(block, partialJson.get(idx))
  }

  // content 按 index 升序铺回骨架（缺失 index 保留空洞，极少见）
  const content = msg.content
  let maxIdx = -1
  for (const idx of blocks.keys()) if (idx > maxIdx) maxIdx = idx
  for (let i = 0; i <= maxIdx; i++) {
    if (blocks.has(i)) content[i] = blocks.get(i)
  }
  return msg
}

/**
 * tool_use 块收尾：把累积的 partial_json 原文解析为 input 对象（失败保留原文字符串）。
 * @param {object|undefined} block
 * @param {string|undefined} raw
 */
function finalizeToolInput(block, raw) {
  if (!block || block.type !== 'tool_use' || raw === undefined) return
  try { block.input = JSON.parse(raw) } catch { block.input = raw }
}

/**
 * OpenAI chat.completion.chunk 流式事件 → 完整 chat.completion 对象重组。
 *
 * 事件语义：
 *   - 首帧 id/object/created/model/system_fingerprint → 响应骨架（object 归一为 chat.completion）
 *   - choices[i].delta.role / content / reasoning_content → message 字段（content 拼接）
 *   - choices[i].delta.tool_calls[j] → 按 (choiceIndex, toolCallIndex) 累积：
 *       id / function.name 首次出现写入，function.arguments 逐帧拼接
 *   - choices[i].finish_reason → 写回该 choice
 *   - 末帧 usage（stream_options.include_usage）→ 覆盖响应 usage
 *
 * @param {Array<{parsed:any}>} events
 * @returns {object|null} 完整响应对象；一条可识别事件都没有时 null
 */
function mergeOpenAIStream(events) {
  let resp = null
  const choices = new Map() // choiceIndex → { choice, tools: Map<tcIndex, tool> }

  for (const e of events) {
    const p = e.parsed
    if (!p || typeof p !== 'object') continue

    // 骨架字段取首个非空帧（每帧重复携带）
    if (!resp) {
      resp = {}
      if (p.id !== undefined) resp.id = p.id
      if (p.object !== undefined) resp.object = String(p.object).endsWith('chunk') ? 'chat.completion' : p.object
      if (p.created !== undefined) resp.created = p.created
      if (p.model !== undefined) resp.model = p.model
      if (p.system_fingerprint !== undefined) resp.system_fingerprint = p.system_fingerprint
    }
    // 末帧 usage 覆盖（中间帧 usage:null 自动跳过）
    if (p.usage && typeof p.usage === 'object') resp.usage = deepClone(p.usage)

    if (!Array.isArray(p.choices)) continue
    for (const c of p.choices) {
      if (!c || typeof c !== 'object') continue
      const ci = typeof c.index === 'number' ? c.index : 0
      let st = choices.get(ci)
      if (!st) {
        st = { choice: { index: ci, message: {}, finish_reason: null }, tools: new Map() }
        choices.set(ci, st)
      }
      if (c.finish_reason !== undefined && c.finish_reason !== null) {
        st.choice.finish_reason = c.finish_reason
      }
      const d = c.delta
      if (!d || typeof d !== 'object') continue
      const msg = st.choice.message
      if (typeof d.role === 'string' && d.role && !msg.role) msg.role = d.role
      if (typeof d.content === 'string') msg.content = (msg.content || '') + d.content
      if (typeof d.reasoning_content === 'string') {
        msg.reasoning_content = (msg.reasoning_content || '') + d.reasoning_content
      }
      if (Array.isArray(d.tool_calls)) {
        for (const tc of d.tool_calls) {
          if (!tc || typeof tc !== 'object') continue
          const ti = typeof tc.index === 'number' ? tc.index : st.tools.size
          let tool = st.tools.get(ti)
          if (!tool) {
            tool = { id: '', type: 'function', function: { name: '', arguments: '' } }
            st.tools.set(ti, tool)
          }
          if (tc.id) tool.id = tc.id
          if (tc.type) tool.type = tc.type
          if (tc.function && typeof tc.function === 'object') {
            if (tc.function.name) tool.function.name = (tool.function.name || '') + tc.function.name
            if (typeof tc.function.arguments === 'string') {
              tool.function.arguments = (tool.function.arguments || '') + tc.function.arguments
            }
          }
        }
      }
    }
  }

  if (!resp) return null
  if (choices.size > 0) {
    const sorted = [...choices.entries()].sort((a, b) => a[0] - b[0])
    resp.choices = sorted.map(([, st]) => {
      if (st.tools.size > 0) {
        st.choice.message.tool_calls = [...st.tools.entries()]
          .sort((a, b) => a[0] - b[0])
          .map(([, tool]) => tool)
      }
      return st.choice
    })
  }
  return resp
}

/**
 * 把 SSE 事件流重组为"等价非流式完整响应"的 JSON 对象（阶段BH）。
 *
 * 协议识别：扫描全部事件，取首个能识别的事件（Anthropic message_ 或 content_block_
 * 前缀、OpenAI choices 或 chat.completion 对象）；混合协议流以首个识别结果为准。
 * 未知协议 / 一条可识别事件都没有 → merged=null（UI 不渲染该块）。
 *
 * @param {Array<{event: string, raw: string, parsed: any}>} events
 * @returns {{ merged: object|null, protocol: 'anthropic'|'openai'|null }}
 */
export function mergeSSEEvents(events) {
  if (!Array.isArray(events) || events.length === 0) return { merged: null, protocol: null }
  let protocol = null
  for (const e of events) {
    const p = detectEventProtocol(e.parsed)
    if (p) { protocol = p; break }
  }
  if (!protocol) return { merged: null, protocol: null }
  const merged = protocol === 'anthropic' ? mergeAnthropicStream(events) : mergeOpenAIStream(events)
  return { merged, protocol }
}

/**
 * 把 aggregateSSE 的结果序列化为可读文本（用于"复制当前视图"和纯文本视图）。
 *
 * 阶段BH：agg.merged 非空时在末尾追加「完整响应 JSON」段；
 * merged 为空（历史调用方传入无 merged 字段的旧对象）时输出与旧版逐字节一致。
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
  const lines = [
    `事件类型分布: ${eventTypes}${completeLabel}`,
    agg.toolCalls && agg.toolCalls.length ? `工具调用: ${agg.toolCalls.join('、')}` : '',
    `usage: input=${usage.input_tokens_final ?? usage.input_tokens ?? 0} output=${usage.output_tokens_final ?? usage.output_tokens ?? 0}`,
    '---- 聚合文本 ----',
    (agg.textParts || []).join('') || '（无文本增量）',
  ]
  if (agg.merged && typeof agg.merged === 'object') {
    lines.push(`---- 完整响应 JSON (${agg.mergedProtocol || 'unknown'}) ----`)
    lines.push(JSON.stringify(agg.merged, null, 2))
  }
  return lines.filter(Boolean).join('\n')
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