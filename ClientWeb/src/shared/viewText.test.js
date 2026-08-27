// ClientWeb/src/shared/viewText.test.js
//
// buildViewText 轻量自检脚本（无第三方测试框架）。
//
// 运行：
//   cd ClientWeb && node src/shared/viewText.test.js

import { buildViewText, VIEW_RAW, VIEW_JSON, VIEW_SSE, VIEW_AGG } from './viewText.js'

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) { pass++; console.log('ok -', name) } else { fail++; console.error('FAIL -', name) }
}

const jsonBody = '{"a":1,"b":{"c":[1,2]}}'
const sseBody = 'event: message_start\ndata: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}\n\nevent: content_block_delta\ndata: {"type":"content_block_delta","delta":{"text":"你好"}}\n\n'

// headers 字段：任何视图都原样返回
ok(buildViewText('request_headers', VIEW_RAW, 'H1') === 'H1', 'headers raw 原样')
ok(buildViewText('request_headers', VIEW_JSON, 'H1') === 'H1', 'headers json 视图仍原样')
ok(buildViewText('response_headers', VIEW_SSE, 'H2') === 'H2', 'headers sse 视图仍原样')

// raw 视图：原样返回
ok(buildViewText('request_body', VIEW_RAW, jsonBody) === jsonBody, 'body raw 原样')

// json 视图：美化缩进
const pretty = buildViewText('request_body', VIEW_JSON, jsonBody)
ok(pretty.includes('\n') && pretty.includes('"a": 1'), 'body json 视图美化缩进')
ok(buildViewText('request_body', VIEW_JSON, 'not-json') === 'not-json', 'body json 非 JSON 兜底原文')

// sse 视图：序列化事件文本
const sseText = buildViewText('response_body', VIEW_SSE, sseBody)
ok(sseText.includes('# 1 event: message_start'), 'sse 视图含事件序号')
ok(sseText.includes('content_block_delta'), 'sse 视图含第二个事件')
ok(buildViewText('response_body', VIEW_SSE, '') === '（未解析出 SSE 事件）', 'sse 空内容兜底')

// agg 视图：聚合文本
const aggText = buildViewText('response_body', VIEW_AGG, sseBody)
ok(aggText.includes('聚合文本'), 'agg 视图含聚合文本段')
ok(aggText.includes('你好'), 'agg 视图聚合出增量文本')
ok(aggText.includes('input=10'), 'agg 视图含 usage')

// 空值安全
ok(buildViewText('request_body', VIEW_JSON, '') === '', '空 body json 安全')
ok(buildViewText('request_body', undefined, 'x') === 'x', '缺省视图按 raw')

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
