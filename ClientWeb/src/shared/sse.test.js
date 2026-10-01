// ClientWeb/src/shared/sse.test.js
//
// SSE 解析与聚合共用工具的轻量自检脚本（无第三方测试框架）。
//
// 运行：
//   cd ClientWeb && node --experimental-vm-modules src/shared/sse.test.js
// 或在 Node 18+：
//   node src/shared/sse.test.js

import { parseSSEEvents, aggregateSSE, aggregateToText, sseEventsToText, tryParseJsonObject, splitAggregateTextParts, mergeSSEEvents } from './sse.js'

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

// 阶段AV：eventTypes 对无名事件用 '(default)' 兜底，与 SseEventList / aggregateToText 一致
eq(agg1.eventTypes['content_block_delta'], 2, 'Anthropic eventTypes 计数正确（多 delta 累加）')
eq(agg1.eventTypes['(default)'], undefined, 'Anthropic 流无 (default) 键')

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
// 阶段AV：OpenAI data:-only 流 eventTypes 用 '(default)' 兜底（与 SSE 解析 Summary bar 一致）
eq(agg2.eventTypes && agg2.eventTypes['(default)'], 3, 'OpenAI 流 eventTypes 含 (default)×3，与 SSE 视图 Summary 一致')
eq(Object.keys(agg2.eventTypes || {}).length, 1, 'OpenAI 流 eventTypes 仅 (default) 一个键')

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

// ===== 阶段AW：聚合文本片段级 JSON 识别 =====
section('阶段AW：JSON 片段识别（tryParseJsonObject / splitAggregateTextParts）')

// tryParseJsonObject：判定对象/数组
eq(tryParseJsonObject('{"a":1}').ok, true, '合法对象 → ok=true')
eq(tryParseJsonObject('[1,2,3]').ok, true, '合法数组 → ok=true')
eq(tryParseJsonObject('  {"a": 1}  ').ok, true, '前后空白容忍')
eq(tryParseJsonObject('"hello"').ok, false, '字符串标量 → ok=false（不视为 JSON 片段）')
eq(tryParseJsonObject('42').ok, false, '数字标量 → ok=false')
eq(tryParseJsonObject('true').ok, false, '布尔标量 → ok=false')
eq(tryParseJsonObject('null').ok, false, 'null → ok=false')
eq(tryParseJsonObject('{not json}').ok, false, '非法对象语法 → ok=false')
eq(tryParseJsonObject('').ok, false, '空串 → ok=false')
eq(tryParseJsonObject(undefined).ok, false, '非字符串 → ok=false')

// splitAggregateTextParts：按片段切分
const parts1 = splitAggregateTextParts(['hello', '{"a":1}', 'world'])
eq(parts1.length, 3, '3 片段保留顺序')
eq(parts1[0].kind, 'text', '普通文本片段 → kind=text')
eq(parts1[1].kind, 'json', '合法 JSON 片段 → kind=json')
eq(JSON.stringify(parts1[1].value), '{"a":1}', 'JSON 片段 value 为已解析对象')
eq(parts1[2].kind, 'text', '第二段普通文本 → kind=text')

const parts2 = splitAggregateTextParts(['{incomplete', '"scalar"', '[1,2]'])
eq(parts2[0].kind, 'text', '不完整对象 → 文本')
eq(parts2[1].kind, 'text', '字符串标量 → 文本（不美化）')
eq(parts2[2].kind, 'json', '数组 → JSON')

eq(splitAggregateTextParts([]), [], '空数组 → []')
eq(splitAggregateTextParts(null), [], 'null → []')
eq(splitAggregateTextParts(undefined), [], 'undefined → []')

// 混合场景：partial_json 增量片段的独立判定（不做跨片段拼接 —— 跨片段拼接
// 会要求缓冲整段，对单事件大流量场景不友好；按独立片段检测即可覆盖
// content_block_delta.delta.partial_json 一次性是完整 JSON 的常见情况）
const parts3 = splitAggregateTextParts([
  'tool_call start',
  '{"name":"get_weather","args":{"city":"北京"}}',
  'done',
])
eq(parts3[0].kind, 'text', 'partial_json 第 1 段（独立）→ 文本')
eq(parts3[1].kind, 'json', 'partial_json 第 2 段（独立合法 JSON）→ JSON')
eq(parts3[2].kind, 'text', 'partial_json 末尾 → 文本')

// 完整 JSON 片段在尾部
const parts4 = splitAggregateTextParts([
  '普通文本',
  '{"a":1}',
])
eq(parts4[0].kind, 'text', '混合片段第 1 段 → 文本')
eq(parts4[1].kind, 'json', '混合片段第 2 段 → JSON')

// partial_json 增量片段单段不完整时按文本处理
const parts5 = splitAggregateTextParts([
  '{"name":"x",',
  '"args":{"city":"上海"}}',
])
eq(parts5[0].kind, 'text', 'partial_json 单段不完整（不以 { 开头）→ 文本')
eq(parts5[1].kind, 'text', 'partial_json 单段不完整（不以 { 开头）→ 文本')

// ===== 阶段AY：OpenAI 流式 tool_calls 聚合 + 纯工具流场景 =====
// 阶段AY：OpenAI delta.tool_calls[].function.name 应被聚合到 toolCalls
const openaiToolStream = [
  'data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}',
  '',
  'data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"name":"get_weather"},"arguments":"{\\"city\\":\\"北京\\"}"}]},"finish_reason":null}]}',
  '',
  'data: {"choices":[{"delta":{"tool_calls":[{"index":1,"function":{"name":"get_time"}}]},"finish_reason":null}]}',
  '',
  'data: [DONE]',
  '',
].join('\n')
const openaiToolAgg = aggregateSSE(openaiToolStream)
eq(openaiToolAgg.toolCalls, ['get_weather', 'get_time'], 'OpenAI 流式 tool_calls.function.name 聚合')
eq(openaiToolAgg.textParts, [], 'OpenAI 纯工具流 → textParts=[]')
eq(openaiToolAgg.eventTypes['(default)'], 3, 'OpenAI 纯工具流 → 3 条 default 事件（[DONE] 跳过）')

// 阶段AY：纯 message_delta 流（无 content_block_delta）→ textParts=[] 但 eventTypes 非空
const onlyMessageDelta = [
  'event: message_start',
  'data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":0}}}',
  '',
  'event: message_delta',
  'data: {"type":"message_delta","usage":{"output_tokens":5}}',
  '',
  'event: message_stop',
  'data: {"type":"message_stop"}',
  '',
].join('\n')
const msgDeltaAgg = aggregateSSE(onlyMessageDelta)
eq(msgDeltaAgg.textParts, [], '只有 message_delta → textParts=[]')
eq(msgDeltaAgg.eventTypes.message_start, 1, 'message_start 计数')
eq(msgDeltaAgg.eventTypes.message_delta, 1, 'message_delta 计数')
eq(msgDeltaAgg.eventTypes.message_stop, 1, 'message_stop 计数')
eq(msgDeltaAgg.usage.input_tokens_final, 10, 'message_start 末帧 input_tokens 覆盖')
eq(msgDeltaAgg.usage.output_tokens_final, 5, 'message_delta 末帧 output_tokens 覆盖')

// ===== 阶段BF：聚合解析 Text (0) (无) 盲区覆盖 =====

// 1) Anthropic 纯 message_* 流（usage 全 0，无 content_block_delta / tool_use）
//    修复前：hasEventsButNoText 不命中（要求 totalTokens>0 || toolCalls.length>0），
//    UI 落入"Text (0) (无)"盲区；修复后应识别为"有事件无文本"。
const onlyMessageEvents = [
  'event: message_start',
  'data: {"type":"message_start","message":{"usage":{"input_tokens":0,"output_tokens":0}}}',
  '',
  'event: message_delta',
  'data: {"type":"message_delta","usage":{"input_tokens":0,"output_tokens":0}}',
  '',
  'event: message_stop',
  'data: {"type":"message_stop"}',
  '',
].join('\n')
const aggE1 = aggregateSSE(onlyMessageEvents)
eq(aggE1.textParts, [], '纯 message_* 流 → textParts=[]')
eq(aggE1.eventTypes.message_stop, 1, 'message_stop 计数')
// AggregateView 期望的 hasEventsButNoText 判定（去掉了 usage/toolCalls 限制）
const isE1Blind = aggE1.textParts.length === 0 && Object.keys(aggE1.eventTypes).length > 0
eq(isE1Blind, true, '纯 message_* 流被识别为"有事件无文本"（修复前为盲区）')

// 2) OpenAI 纯 role / finish_reason 元数据流（无 content / tool_calls / reasoning_content）
const openaiMetaOnly = [
  'data: {"choices":[{"delta":{"role":"assistant"},"finish_reason":null}]}',
  '',
  'data: {"choices":[{"delta":{},"finish_reason":"stop"}]}',
  '',
].join('\n')
const aggE2 = aggregateSSE(openaiMetaOnly)
eq(aggE2.textParts, [], 'OpenAI 纯元数据流 → textParts=[]')
const isE2Blind = aggE2.textParts.length === 0 && Object.keys(aggE2.eventTypes).length > 0
eq(isE2Blind, true, 'OpenAI 纯元数据流被识别为"有事件无文本"（修复前为盲区）')

// 3) 自定义未知事件类型（聚合逻辑完全不识别）
const unknownStream = [
  'event: custom_event',
  'data: {"foo":"bar","baz":42}',
  '',
  'event: another_custom',
  'data: {"hello":"world"}',
  '',
].join('\n')
const aggE3 = aggregateSSE(unknownStream)
eq(aggE3.textParts, [], '自定义事件流 → textParts=[]')
eq(aggE3.eventTypes.custom_event, 1, '自定义事件计数保留')
eq(aggE3.eventTypes.another_custom, 1, '第二个自定义事件计数保留')
const isE3Blind = aggE3.textParts.length === 0 && Object.keys(aggE3.eventTypes).length > 0
eq(isE3Blind, true, '自定义事件流被识别为"有事件无文本"（修复前为盲区）')

// 4) 回归：纯工具流（已有功能）仍命中 hasEventsButNoText 且命中 toolCalls 分支
const isE4Blind = openaiToolAgg.textParts.length === 0 && Object.keys(openaiToolAgg.eventTypes).length > 0
eq(isE4Blind, true, 'OpenAI 纯工具流仍被识别为"有事件无文本"')
eq(openaiToolAgg.toolCalls.length > 0, true, 'OpenAI 纯工具流 toolCalls 非空（提示文案走 withTools 分支）')

// ===== 阶段BH：完整响应 JSON 重组（merged / mergedProtocol） =====
section('阶段BH：mergeSSEEvents / aggregateSSE.merged')

// 1) Anthropic 流：text + tool_use（partial_json 增量）+ thinking + stop_reason/usage 回填
const anthropicFullStream = [
  'event: message_start',
  'data: {"type":"message_start","message":{"id":"msg_01","type":"message","role":"assistant","model":"claude-x","content":[],"usage":{"input_tokens":25,"output_tokens":1}}}',
  '',
  'event: content_block_start',
  'data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}',
  '',
  'event: content_block_stop',
  'data: {"type":"content_block_stop","index":0}',
  '',
  'event: content_block_start',
  'data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{}}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\\"city\\":"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"\\"北京\\"}"}}',
  '',
  'event: content_block_stop',
  'data: {"type":"content_block_stop","index":1}',
  '',
  'event: message_delta',
  'data: {"type":"message_delta","delta":{"stop_reason":"tool_use","stop_sequence":null},"usage":{"output_tokens":88}}',
  '',
  'event: message_stop',
  'data: {"type":"message_stop"}',
  '',
].join('\n')
const bh1 = aggregateSSE(anthropicFullStream)
eq(bh1.mergedProtocol, 'anthropic', 'Anthropic 流 mergedProtocol=anthropic')
eq(bh1.merged.id, 'msg_01', 'Anthropic merged 骨架取自 message_start（id）')
eq(bh1.merged.role, 'assistant', 'Anthropic merged 骨架 role')
eq(bh1.merged.model, 'claude-x', 'Anthropic merged 骨架 model')
eq(bh1.merged.content.length, 2, 'Anthropic merged content 含 2 个块')
eq(bh1.merged.content[0].text, 'Hello world', 'Anthropic merged 文本块增量拼接')
eq(bh1.merged.content[1].name, 'get_weather', 'Anthropic merged tool_use 块 name')
eq(bh1.merged.content[1].input.city, '北京', 'Anthropic merged tool_use input 由 partial_json 增量拼装为对象')
eq(bh1.merged.stop_reason, 'tool_use', 'Anthropic merged stop_reason 来自 message_delta')
eq(bh1.merged.stop_sequence, null, 'Anthropic merged stop_sequence 来自 message_delta')
eq(bh1.merged.usage.input_tokens, 25, 'Anthropic merged usage.input_tokens 保留自 message_start')
eq(bh1.merged.usage.output_tokens, 88, 'Anthropic merged usage.output_tokens 被 message_delta 覆盖')

// 2) Anthropic thinking 块：thinking_delta / signature_delta 合并
const thinkingStream = [
  'event: content_block_start',
  'data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"let me "}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"think"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-abc"}}',
  '',
].join('\n')
const bh2 = aggregateSSE(thinkingStream)
eq(bh2.merged.content[0].thinking, 'let me think', 'thinking_delta 增量拼接')
eq(bh2.merged.content[0].signature, 'sig-abc', 'signature_delta 写入')
eq(bh2.merged.role, 'assistant', '缺 message_start 的截断流 → 延迟骨架兜底')

// 3) OpenAI 流：content + reasoning_content + tool_calls 累积 + finish_reason + 末帧 usage
const openaiFullStream = [
  'data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1700000000,"model":"gpt-x","system_fingerprint":"fp1","choices":[{"index":0,"delta":{"role":"assistant","content":"Hi "},"finish_reason":null}]}',
  '',
  'data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1700000000,"model":"gpt-x","choices":[{"index":0,"delta":{"content":"there"},"finish_reason":null}]}',
  '',
  'data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1700000000,"model":"gpt-x","choices":[{"index":0,"delta":{"reasoning_content":"thinking..."},"finish_reason":null}]}',
  '',
  'data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1700000000,"model":"gpt-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"get_weather","arguments":"{\\"city\\""}}]},"finish_reason":null}]}',
  '',
  'data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1700000000,"model":"gpt-x","choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":":\\"北京\\"}"}}]},"finish_reason":"tool_calls"}]}',
  '',
  'data: {"id":"chatcmpl-9","object":"chat.completion.chunk","created":1700000000,"model":"gpt-x","choices":[],"usage":{"prompt_tokens":12,"completion_tokens":34,"total_tokens":46}}',
  '',
  'data: [DONE]',
  '',
].join('\n')
const bh3 = aggregateSSE(openaiFullStream)
eq(bh3.mergedProtocol, 'openai', 'OpenAI 流 mergedProtocol=openai')
eq(bh3.merged.id, 'chatcmpl-9', 'OpenAI merged 骨架 id')
eq(bh3.merged.object, 'chat.completion', 'OpenAI merged object 由 chunk 归一为 chat.completion')
eq(bh3.merged.model, 'gpt-x', 'OpenAI merged 骨架 model')
eq(bh3.merged.system_fingerprint, 'fp1', 'OpenAI merged 骨架 system_fingerprint')
eq(bh3.merged.choices[0].message.role, 'assistant', 'OpenAI merged message.role')
eq(bh3.merged.choices[0].message.content, 'Hi there', 'OpenAI merged message.content 拼接')
eq(bh3.merged.choices[0].message.reasoning_content, 'thinking...', 'OpenAI merged reasoning_content 拼接')
eq(bh3.merged.choices[0].message.tool_calls[0].id, 'call_1', 'OpenAI merged tool_calls id 累积')
eq(bh3.merged.choices[0].message.tool_calls[0].function.name, 'get_weather', 'OpenAI merged tool_calls function.name')
eq(bh3.merged.choices[0].message.tool_calls[0].function.arguments, '{"city":"北京"}', 'OpenAI merged tool_calls arguments 逐帧拼接')
eq(bh3.merged.choices[0].finish_reason, 'tool_calls', 'OpenAI merged finish_reason')
eq(bh3.merged.usage.total_tokens, 46, 'OpenAI merged 末帧 usage 覆盖')

// 4) 非流式完整响应：merged 原样透传
const bh4 = aggregateSSE(openaiCompleteStr)
eq(bh4.merged.id, 'chatcmpl-1', '非流式完整响应 merged 原样透传（id）')
eq(bh4.mergedProtocol, 'openai', '非流式 OpenAI 响应 mergedProtocol=openai')
eq(bh4.merged.choices[0].message.content, '你好，世界！', '非流式 merged content 可访问')
const bh4b = aggregateSSE(JSON.stringify(anthropicComplete))
eq(bh4b.mergedProtocol, 'anthropic', '非流式 Anthropic 响应 mergedProtocol=anthropic')
eq(bh4b.merged.content[0].text, 'Hello', '非流式 Anthropic merged content 透传')

// 5) 未知协议流 → merged=null
const bh5 = aggregateSSE(unknownStream)
eq(bh5.merged, null, '自定义未知事件流 → merged=null')
eq(bh5.mergedProtocol, null, '自定义未知事件流 → mergedProtocol=null')
eq(mergeSSEEvents([]).merged, null, 'mergeSSEEvents([]) → merged=null')

// 6) 截断的 Anthropic 工具流：无 content_block_stop → partial_json 收尾兜底解析
const truncatedToolStream = [
  'event: content_block_start',
  'data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_02","name":"get_time"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\\"tz\\":\\"UTC\\"}"}}',
  '',
].join('\n')
const bh6 = aggregateSSE(truncatedToolStream)
eq(bh6.merged.content[0].input.tz, 'UTC', '截断流无 content_block_stop → 收尾兜底解析 partial_json')

// 7) 非法 partial_json（半截 JSON）→ 保留原文字符串，不抛异常
const badPartialJsonStream = [
  'event: content_block_start',
  'data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","name":"x"}}',
  '',
  'event: content_block_delta',
  'data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\\"un"}}',
  '',
  'event: content_block_stop',
  'data: {"type":"content_block_stop","index":0}',
  '',
].join('\n')
const bh7 = aggregateSSE(badPartialJsonStream)
eq(bh7.merged.content[0].input, '{"un', '非法 partial_json → input 保留原文字符串（无损）')

// 8) aggregateToText：merged 段追加 + 旧对象（无 merged）输出不变
const bh8 = aggregateToText(bh1)
eq(bh8.includes('---- 完整响应 JSON (anthropic) ----'), true, 'aggregateToText 追加完整响应 JSON 段（含协议）')
eq(bh8.includes('"stop_reason": "tool_use"'), true, 'aggregateToText 的 merged 段为格式化 JSON')
eq(
  aggregateToText({ textParts: ['A'], usage: null, toolCalls: [], eventTypes: {} }),
  '事件类型分布: 无\nusage: input=0 output=0\n---- 聚合文本 ----\nA',
  'aggregateToText 无 merged 字段 → 输出与旧版一致',
)

// 9) 兼容回归：null 入参返回结构不含 merged（历史断言原样保留）
eq(aggregateSSE(null), { textParts: [], usage: null, toolCalls: [], eventTypes: {} }, 'null → 空结构（回归）')

// ===== 阶段CT：OpenAI Responses 协议（/v1/responses，Codex Agent）=====
section('阶段CT：OpenAI Responses 协议流式聚合 + 重组')

// 1) 文本流（真实样本缩样：MiniMax /v1/responses）
const responsesTextStream = [
  'event: response.created',
  'data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_01","object":"response","created_at":1790758304,"model":"MiniMax-M3.1","status":"in_progress","output":[],"usage":null}}',
  '',
  'event: response.in_progress',
  'data: {"type":"response.in_progress","sequence_number":1,"response":{"id":"resp_01","object":"response","status":"in_progress","output":[],"usage":null}}',
  '',
  'event: response.output_item.added',
  'data: {"type":"response.output_item.added","sequence_number":2,"output_index":0,"item":{"id":"resp_01_msg","type":"message","status":"in_progress","role":"assistant","content":[]}}',
  '',
  'event: response.content_part.added',
  'data: {"type":"response.content_part.added","sequence_number":3,"item_id":"resp_01_msg","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}',
  '',
  'event: response.output_text.delta',
  'data: {"type":"response.output_text.delta","sequence_number":4,"item_id":"resp_01_msg","output_index":0,"content_index":0,"delta":"全部完成。以下是"}',
  '',
  'event: response.output_text.delta',
  'data: {"type":"response.output_text.delta","sequence_number":5,"item_id":"resp_01_msg","output_index":0,"content_index":0,"delta":"本次任务总结。"}',
  '',
  'event: response.output_text.done',
  'data: {"type":"response.output_text.done","sequence_number":6,"item_id":"resp_01_msg","output_index":0,"content_index":0,"text":"全部完成。以下是本次任务总结。"}',
  '',
  'event: response.content_part.done',
  'data: {"type":"response.content_part.done","sequence_number":7,"item_id":"resp_01_msg","output_index":0,"content_index":0,"part":{"type":"output_text","text":"全部完成。以下是本次任务总结。","annotations":[]}}',
  '',
  'event: response.output_item.done',
  'data: {"type":"response.output_item.done","sequence_number":8,"output_index":0,"item":{"id":"resp_01_msg","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"全部完成。以下是本次任务总结。","annotations":[]}]}}',
  '',
  'event: response.completed',
  'data: {"type":"response.completed","sequence_number":9,"response":{"id":"resp_01","object":"response","status":"completed","output":[{"id":"resp_01_msg","type":"message","status":"completed","role":"assistant","content":[{"type":"output_text","text":"全部完成。以下是本次任务总结。","annotations":[]}]}],"usage":{"input_tokens":450083,"output_tokens":687,"total_tokens":450770,"input_tokens_details":{"cached_tokens":449796}},"error":null}}',
  '',
].join('\n')
const ct1 = aggregateSSE(responsesTextStream)
eq(ct1.textParts.join(''), '全部完成。以下是本次任务总结。', 'Responses 流 output_text.delta 文本合并')
eq(ct1.toolCalls, [], 'Responses 纯文本流无工具调用')
eq(ct1.usage && ct1.usage.input_tokens_final, 450083, 'Responses 终态 usage.input_tokens 覆盖')
eq(ct1.usage && ct1.usage.output_tokens_final, 687, 'Responses 终态 usage.output_tokens 覆盖')
eq(ct1.usage && ct1.usage.cache_read_input_tokens, 449796, 'Responses cached_tokens → cache_read_input_tokens')
eq(ct1.eventTypes && ct1.eventTypes['response.output_text.delta'], 2, 'Responses eventTypes 统计')
eq(ct1.eventTypes && ct1.eventTypes['(default)'], undefined, 'Responses 流无 (default) 键')
eq(ct1.mergedProtocol, 'openai-responses', 'Responses 流 mergedProtocol=openai-responses')
eq(ct1.merged.id, 'resp_01', 'Responses merged 取终态权威 response（id）')
eq(ct1.merged.status, 'completed', 'Responses merged status=completed')
eq(ct1.merged.usage.input_tokens, 450083, 'Responses merged usage 完整保留')
const ct1msg = ct1.merged.output && ct1.merged.output[0]
eq(ct1msg && ct1msg.type, 'message', 'Responses merged output[0] 为 message item')
eq(ct1msg && ct1msg.content[0].text, '全部完成。以下是本次任务总结。', 'Responses merged message.content[0].text 完整')

// 2) 纯函数调用流（Codex 真实形态：无 assistant 文本，仅 function_call）
const responsesToolStream = [
  'event: response.created',
  'data: {"type":"response.created","sequence_number":0,"response":{"id":"resp_02","object":"response","status":"in_progress","output":[]}}',
  '',
  'event: response.output_item.added',
  'data: {"type":"response.output_item.added","sequence_number":1,"output_index":0,"item":{"id":"resp_02_fc_0","type":"function_call","status":"in_progress","call_id":"call_1","name":"exec_command","arguments":""}}',
  '',
  'event: response.function_call_arguments.delta',
  'data: {"type":"response.function_call_arguments.delta","sequence_number":2,"output_index":0,"item_id":"resp_02_fc_0","delta":"{\\"cmd\\": \\"cd"}',
  '',
  'event: response.function_call_arguments.delta',
  'data: {"type":"response.function_call_arguments.delta","sequence_number":3,"output_index":0,"item_id":"resp_02_fc_0","delta":" /tmp && ls\\"}"}',
  '',
  'event: response.function_call_arguments.done',
  'data: {"type":"response.function_call_arguments.done","sequence_number":4,"output_index":0,"item_id":"resp_02_fc_0","name":"exec_command","arguments":"{\\"cmd\\": \\"cd /tmp && ls\\"}"}',
  '',
  'event: response.output_item.done',
  'data: {"type":"response.output_item.done","sequence_number":5,"output_index":0,"item":{"id":"resp_02_fc_0","type":"function_call","status":"completed","call_id":"call_1","name":"exec_command","arguments":"{\\"cmd\\": \\"cd /tmp && ls\\"}"}}',
  '',
  'event: response.completed',
  'data: {"type":"response.completed","sequence_number":6,"response":{"id":"resp_02","object":"response","status":"completed","output":[{"id":"resp_02_fc_0","type":"function_call","status":"completed","call_id":"call_1","name":"exec_command","arguments":"{\\"cmd\\": \\"cd /tmp && ls\\"}"}],"usage":{"input_tokens":1000,"output_tokens":50,"total_tokens":1050}}}',
  '',
].join('\n')
const ct2 = aggregateSSE(responsesToolStream)
eq(ct2.toolCalls, ['exec_command'], 'Responses 流 function_call 工具名提取')
eq(ct2.textParts, [], 'Responses 纯函数调用流 → textParts=[]（arguments 不进文本，与 chat 流语义一致）')
eq(ct2.usage && ct2.usage.input_tokens_final, 1000, 'Responses 纯函数调用流 usage 提取')
eq(ct2.mergedProtocol, 'openai-responses', '纯函数调用流 mergedProtocol=openai-responses')
eq(ct2.merged.output[0].name, 'exec_command', '纯函数调用流 merged output[0].name')
eq(ct2.merged.output[0].arguments, '{"cmd": "cd /tmp && ls"}', '纯函数调用流 merged arguments 完整（终态权威）')
// AggregateView 期望的 hasEventsButNoText 判定 → 走 withTools 提示分支
const isCt2Blind = ct2.textParts.length === 0 && Object.keys(ct2.eventTypes).length > 0
eq(isCt2Blind, true, '纯函数调用流被识别为"有事件无文本"（withTools 提示分支）')

// 3) reasoning 流（MiniMax 变体 reasoning_text.delta + summary 变体）
const responsesReasoningStream = [
  'event: response.output_item.added',
  'data: {"type":"response.output_item.added","output_index":0,"item":{"id":"rs_1","type":"reasoning","status":"in_progress","summary":[]}}',
  '',
  'event: response.reasoning_text.delta',
  'data: {"type":"response.reasoning_text.delta","output_index":0,"item_id":"rs_1","delta":"思考片段A"}',
  '',
  'event: response.reasoning_text.delta',
  'data: {"type":"response.reasoning_text.delta","output_index":0,"item_id":"rs_1","delta":" + 思考片段B"}',
  '',
  'event: response.output_item.added',
  'data: {"type":"response.output_item.added","output_index":1,"item":{"id":"msg_1","type":"message","status":"in_progress","role":"assistant","content":[]}}',
  '',
  'event: response.output_text.delta',
  'data: {"type":"response.output_text.delta","output_index":1,"item_id":"msg_1","content_index":0,"delta":"答案文本"}',
  '',
].join('\n')
const ct3 = aggregateSSE(responsesReasoningStream)
eq(ct3.textParts.join(''), '思考片段A + 思考片段B答案文本', 'reasoning_text.delta + output_text.delta 均进 textParts（按到达序）')
eq(ct3.mergedProtocol, 'openai-responses', 'reasoning 流 mergedProtocol=openai-responses')
eq(ct3.merged.output.length, 2, '截断流（无终态）merged output 含 2 个累积 item')
eq(ct3.merged.output[0].summary[0].text, '思考片段A + 思考片段B', 'reasoning item 无 content → summary 兜底累积')
eq(ct3.merged.output[1].content[0].text, '答案文本', 'message item content part 增量拼接')

// 4) 截断流（无 response.completed）：骨架 + items 重组兜底
const responsesTruncated = [
  'event: response.created',
  'data: {"type":"response.created","response":{"id":"resp_03","object":"response","model":"gpt-5","status":"in_progress","output":[],"usage":null}}',
  '',
  'event: response.output_item.added',
  'data: {"type":"response.output_item.added","output_index":0,"item":{"id":"m_0","type":"message","status":"in_progress","role":"assistant","content":[]}}',
  '',
  'event: response.content_part.added',
  'data: {"type":"response.content_part.added","item_id":"m_0","output_index":0,"content_index":0,"part":{"type":"output_text","text":"","annotations":[]}}',
  '',
  'event: response.output_text.delta',
  'data: {"type":"response.output_text.delta","item_id":"m_0","output_index":0,"content_index":0,"delta":"流被中断前的文本"}',
  '',
].join('\n')
const ct4 = aggregateSSE(responsesTruncated)
eq(ct4.merged.id, 'resp_03', '截断流 merged 骨架取自 response.created')
eq(ct4.merged.model, 'gpt-5', '截断流 merged 骨架 model 保留')
eq(ct4.merged.output[0].content[0].text, '流被中断前的文本', '截断流 merged 增量回填到 content part')
eq(ct4.textParts.join(''), '流被中断前的文本', '截断流 textParts 同步提取')

// 5) 非流式完整 Responses 响应（单 JSON）
const responsesComplete = {
  id: 'resp_99',
  object: 'response',
  status: 'completed',
  output: [
    { id: 'rs_0', type: 'reasoning', status: 'completed', summary: [{ type: 'summary_text', text: '推理摘要' }] },
    { id: 'm_1', type: 'message', status: 'completed', role: 'assistant', content: [{ type: 'output_text', text: '最终回答', annotations: [] }] },
    { id: 'fc_1', type: 'function_call', status: 'completed', call_id: 'call_x', name: 'shell', arguments: '{}' },
  ],
  usage: { input_tokens: 88, output_tokens: 66, total_tokens: 154, input_tokens_details: { cached_tokens: 80 } },
}
const ct5 = aggregateSSE(JSON.stringify(responsesComplete))
eq(ct5.isComplete, true, 'Responses 完整响应 isComplete=true')
eq(ct5.mergedProtocol, 'openai-responses', 'Responses 完整响应 mergedProtocol=openai-responses')
eq(ct5.textParts.join(''), '推理摘要最终回答', 'Responses 完整响应 output 提取（summary + output_text）')
eq(ct5.toolCalls, ['shell'], 'Responses 完整响应 function_call 工具名提取')
eq(ct5.usage.input_tokens_final, 88, 'Responses 完整响应 usage input_tokens_final')
eq(ct5.usage.cache_read_input_tokens, 80, 'Responses 完整响应 cached_tokens → cache_read_input_tokens')
eq(ct5.merged.output.length, 3, 'Responses 完整响应 merged 原样透传（output 3 项）')

// 6) aggregateToText：merged 段协议标注 openai-responses
const ct6 = aggregateToText(ct1)
eq(ct6.includes('---- 完整响应 JSON (openai-responses) ----'), true, 'aggregateToText 标注 openai-responses 协议')

// 7) 回归：chat/completions 与 Anthropic 流协议识别不受影响（混合以首识别为准）
eq(mergeSSEEvents(parseSSEEvents(openaiFullStream)).protocol, 'openai', '回归：chat.completion.chunk 流仍识别为 openai')
eq(mergeSSEEvents(parseSSEEvents(anthropicFullStream)).protocol, 'anthropic', '回归：Anthropic 流仍识别为 anthropic')
eq(aggregateSSE(openaiStream).textParts.join(''), 'Hi therethinking', '回归：OpenAI chat 流文本合并不变')
eq(aggregateSSE(anthropicStream).textParts.join(''), 'Hello world', '回归：Anthropic 流文本合并不变')

// 总结
// eslint-disable-next-line no-console
console.log(`\n总计：${pass} 通过 / ${fail} 失败`)
if (fail > 0) process.exit(1)