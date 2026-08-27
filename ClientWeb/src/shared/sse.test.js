// ClientWeb/src/shared/sse.test.js
//
// SSE 解析与聚合共用工具的轻量自检脚本（无第三方测试框架）。
//
// 运行：
//   cd ClientWeb && node --experimental-vm-modules src/shared/sse.test.js
// 或在 Node 18+：
//   node src/shared/sse.test.js

import { parseSSEEvents, aggregateSSE, aggregateToText, sseEventsToText } from './sse.js'

let pass = 0
let fail = 0

function eq(a, b, name) {
  const sa = JSON.stringify(a)
  const sb = JSON.stringify(b)
  if (sa === sb) {
    pass++
    // eslint-disable-next-line no-console
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    // eslint-disable-next-line no-console
    console.error(`  ✗ ${name}`)
    // eslint-disable-next-line no-console
    console.error(`    expected: ${sb}`)
    // eslint-disable-next-line no-console
    console.error(`    actual:   ${sa}`)
  }
}

function section(t) {
  // eslint-disable-next-line no-console
  console.log(`\n${t}`)
}

// parseSSEEvents
section('parseSSEEvents')
eq(parseSSEEvents(''), [], '空字符串 → []')
eq(parseSSEEvents(null), [], 'null → []')
eq(parseSSEEvents(undefined), [], 'undefined → []')

eq(
  parseSSEEvents('event: message\ndata: {"x":1}\n\n'),
  [{ event: 'message', raw: '{"x":1}', parsed: { x: 1 } }],
  '单 event + JSON data',
)

eq(
  parseSSEEvents('data: hello world\n\n'),
  [{ event: '', raw: 'hello world', parsed: null }],
  'data 非 JSON → parsed=null',
)

eq(
  parseSSEEvents('event: a\ndata: 1\nevent: b\ndata: 2\n'),
  [
    { event: 'a', raw: '1', parsed: 1 },
    { event: 'b', raw: '2', parsed: 2 },
  ],
  '无空行分隔仍拆出 2 条（data 数字字符串按 JSON 解析为 number）',
)

eq(
  parseSSEEvents('event: a\ndata: hello world\nevent: b\ndata: foo bar\n'),
  [
    { event: 'a', raw: 'hello world', parsed: null },
    { event: 'b', raw: 'foo bar', parsed: null },
  ],
  '无空行分隔 + 非 JSON data 仍拆出 2 条',
)

// aggregateSSE
section('aggregateSSE')
const anthropicStream = [
  'event: message_start',
  'data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}',
  '',
  'event: content_block_start',
  'data: {"type":"content_block_start","content_block":{"type":"tool_use","name":"get_weather"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","delta":{"text":"Hello"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","delta":{"text":" world"}}',
  '',
  'event: message_delta',
  'data: {"type":"message_delta","usage":{"output_tokens":5}}',
  '',
].join('\n')
const agg1 = aggregateSSE(anthropicStream)
eq(agg1.textParts.join(''), 'Hello world', 'Anthropic content_block_delta 文本合并')
eq(agg1.toolCalls, ['get_weather'], 'tool_use 提取工具名')
eq(agg1.usage && agg1.usage.input_tokens_final, 10, 'usage.input_tokens_final 末帧覆盖')
eq(agg1.usage && agg1.usage.output_tokens_final, 5, 'usage.output_tokens_final 末帧覆盖')
eq(agg1.eventTypes && agg1.eventTypes.message_start, 1, 'eventTypes 统计')

const openaiStream = [
  'data: {"choices":[{"delta":{"content":"Hi "}}]}',
  '',
  'data: {"choices":[{"delta":{"content":"there"}}]}',
  '',
  'data: {"choices":[{"delta":{"reasoning_content":"thinking"}}]}',
  '',
].join('\n')
const agg2 = aggregateSSE(openaiStream)
eq(agg2.textParts.join(''), 'Hi therethinking', 'OpenAI choices[0].delta.content + reasoning_content 合并')

eq(aggregateSSE('').textParts, [], '空流 → textParts=[]')
eq(aggregateSSE(null), { textParts: [], usage: null, toolCalls: [], eventTypes: {} }, 'null → 空结构')

// aggregateToText
section('aggregateToText / sseEventsToText')
eq(aggregateToText(null), '', 'aggregateToText(null) → ""')
eq(
  aggregateToText({ textParts: ['A', 'B'], usage: { input_tokens_final: 10, output_tokens_final: 5 }, toolCalls: [], eventTypes: { message_start: 1 } }),
  '事件类型分布: message_start×1\nusage: input=10 output=5\n---- 聚合文本 ----\nAB',
  'aggregateToText 结构正确',
)
eq(sseEventsToText([]), '（未解析出 SSE 事件）', '空 events → 兜底提示')
eq(
  sseEventsToText([{ event: 'a', raw: '1', parsed: { x: 1 } }]),
  '# 1 event: a\n{\n  "x": 1\n}',
  'sseEventsToText 单 event',
)

// ===== 阶段AR：非流式完整响应 / [DONE] 终止行 / cache tokens =====
section('阶段AR：非流式完整响应 + [DONE]')

// OpenAI 完整响应（非流式）
const openaiComplete = {
  id: 'chatcmpl-1',
  choices: [{
    message: {
      role: 'assistant',
      content: '你好，世界！',
      tool_calls: [{ function: { name: 'get_weather' } }, { function: { name: 'get_time' } }],
    },
  }],
  usage: { prompt_tokens: 12, completion_tokens: 8, total_tokens: 20 },
}
const openaiCompleteStr = JSON.stringify(openaiComplete)
const openaiCompleteEvents = parseSSEEvents(openaiCompleteStr)
eq(openaiCompleteEvents.length, 1, 'OpenAI 完整响应 → 单条 complete 事件')
eq(openaiCompleteEvents[0].event, 'complete', 'OpenAI 完整响应 event=complete')
eq(openaiCompleteEvents[0].synthetic, true, 'OpenAI 完整响应 synthetic=true')
eq(openaiCompleteEvents[0].parsed.choices[0].message.content, '你好，世界！', 'OpenAI 完整响应 content 可访问')

const openaiCompleteAgg = aggregateSSE(openaiCompleteStr)
eq(openaiCompleteAgg.isComplete, true, 'OpenAI 完整响应 isComplete=true')
eq(openaiCompleteAgg.textParts, ['你好，世界！'], 'OpenAI 完整响应 textParts')
eq(openaiCompleteAgg.toolCalls, ['get_weather', 'get_time'], 'OpenAI 完整响应 toolCalls 提取')
eq(openaiCompleteAgg.usage.input_tokens_final, 12, 'OpenAI 完整响应 usage input_tokens_final')
eq(openaiCompleteAgg.usage.output_tokens_final, 8, 'OpenAI 完整响应 usage output_tokens_final')

// Anthropic 完整响应
const anthropicComplete = {
  id: 'msg-1',
  type: 'message',
  content: [
    { type: 'text', text: 'Hello' },
    { type: 'text', text: ' world' },
    { type: 'tool_use', name: 'get_time', input: {} },
  ],
  stop_reason: 'end_turn',
  usage: { input_tokens: 100, output_tokens: 5, cache_creation_input_tokens: 50, cache_read_input_tokens: 30 },
}
const anthropicCompleteAgg = aggregateSSE(JSON.stringify(anthropicComplete))
eq(anthropicCompleteAgg.isComplete, true, 'Anthropic 完整响应 isComplete=true')
eq(anthropicCompleteAgg.textParts.join(''), 'Hello world', 'Anthropic 完整响应 text 合并')
eq(anthropicCompleteAgg.toolCalls, ['get_time'], 'Anthropic 完整响应 tool_use 提取')
eq(anthropicCompleteAgg.usage.input_tokens_final, 100, 'Anthropic 完整响应 input_tokens')
eq(anthropicCompleteAgg.usage.cache_creation_input_tokens, 50, 'Anthropic cache_creation_input_tokens')
eq(anthropicCompleteAgg.usage.cache_read_input_tokens, 30, 'Anthropic cache_read_input_tokens')

// [DONE] 终止行
const openaiStreamWithDone = [
  'data: {"choices":[{"delta":{"content":"Hi"}}]}',
  '',
  'data: [DONE]',
  '',
].join('\n')
const doneEvents = parseSSEEvents(openaiStreamWithDone)
eq(doneEvents.length, 1, 'OpenAI 流跳过 [DONE] 后仅 1 条事件')
eq(doneEvents[0].parsed.choices[0].delta.content, 'Hi', 'OpenAI 流 [DONE] 前事件内容正确')

// aggregateToText 非流式
const openaiCompleteAggText = aggregateToText(openaiCompleteAgg)
eq(openaiCompleteAggText.includes('（非流式完整响应）'), true, 'aggregateToText 标注"非流式完整响应"')

// 非 JSON 纯文本（非 SSE 格式，非 JSON）→ parseSSEEvents 返回 []
eq(parseSSEEvents('plain text response'), [], '非 JSON 非 SSE 文本 → []')
eq(parseSSEEvents('   '), [], '空白文本 → []')

// 总结
// eslint-disable-next-line no-console
console.log(`\n总计：${pass} 通过 / ${fail} 失败`)
if (fail > 0) process.exit(1)