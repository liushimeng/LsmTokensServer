// ClientWeb/src/shared/viewText.test.js
//
// buildViewText 轻量自检脚本（无第三方测试框架）。
//
// 运行：
//   cd ClientWeb && node src/shared/viewText.test.js

import { buildViewText, viewsForTab, VIEW_RAW, VIEW_JSON, VIEW_SSE, VIEW_AGG } from './viewText.js'

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

// ===== 阶段BH：agg 视图含「完整响应 JSON」重组段 =====
ok(aggText.includes('---- 完整响应 JSON (anthropic) ----'), 'agg 视图含完整响应 JSON 段（协议标注）')
ok(aggText.includes('"type": "message"'), 'agg 视图 merged 段含重组骨架（type=message）')
ok(aggText.includes('"input_tokens": 10'), 'agg 视图 merged 段含重组 usage')

// 空值安全
ok(buildViewText('request_body', VIEW_JSON, '') === '', '空 body json 安全')
ok(buildViewText('request_body', undefined, 'x') === 'x', '缺省视图按 raw')

// ===== viewsForTab（阶段AU：视图按钮按字段语义裁剪） =====
const wantReq = [VIEW_RAW, VIEW_JSON]
const wantResp = [VIEW_RAW, VIEW_SSE, VIEW_AGG]
ok(JSON.stringify(viewsForTab('request_body')) === JSON.stringify(wantReq), 'request_body 仅 raw/json（无 SSE/聚合）')
ok(JSON.stringify(viewsForTab('response_body')) === JSON.stringify(wantResp), 'response_body 仅 raw/sse/agg（无 JSON 美化）')
ok(JSON.stringify(viewsForTab('request_headers')) === JSON.stringify([VIEW_RAW]), 'headers 仅 raw')
ok(JSON.stringify(viewsForTab('response_headers')) === JSON.stringify([VIEW_RAW]), 'response headers 仅 raw')
ok(viewsForTab('request_body').includes(VIEW_JSON) === true, 'request_body 保留 JSON 美化')
ok(viewsForTab('response_body').includes(VIEW_JSON) === false, 'response_body 裁掉 JSON 美化')
ok(viewsForTab('request_body').includes(VIEW_SSE) === false, 'request_body 裁掉 SSE 解析')

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
