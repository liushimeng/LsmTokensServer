// ClientWeb/src/pages/chat-analysis/statusFilter.test.js
//
// 阶段CV：「状态」筛选可编辑 List 控件 + 数字净化的轻量自检脚本（无第三方测试框架）。
// 覆盖：
//   1. HTTP_STATUS_OPTIONS 清单完整性（≥20 项、必含 429/529/404、码值全为 3 位纯数字、无重复）
//   2. sanitizeStatusCodeInput 净化函数（纯数字透传 / 非数字剔除 / 超 3 位截断 / null 容错）
//   3. ChatAnalysisToolbar.jsx 已接入 datalist 组合框与净化调用（源码静态断言）
//   4. useChatAnalysisFilters.js 恢复路径已净化（源码静态断言）
//   5. 三语 i18n key（chatAnalysis.statusPlaceholder / chatAnalysis.invert）完整性
//
// 运行：
//   cd ClientWeb && node src/pages/chat-analysis/statusFilter.test.js

import fs from 'node:fs'
import path from 'node:path'
import url from 'node:url'

const __dirname = path.dirname(url.fileURLToPath(import.meta.url))
const pageDir = __dirname
const localesDir = path.resolve(pageDir, '../../i18n/locales')

// 直接加载独立无依赖模块（constants.js 含 vite 风格 extensionless import，node 原生 ESM 无法解析）
const { HTTP_STATUS_OPTIONS, sanitizeStatusCodeInput } = await import('./statusOptions.js')

function loadJson(name) {
  return JSON.parse(fs.readFileSync(path.join(localesDir, name), 'utf8'))
}
const zhCN = loadJson('zh-CN.json')
const en = loadJson('en.json')
const ja = loadJson('ja.json')

function readSrc(rel) {
  return fs.readFileSync(path.join(pageDir, rel), 'utf8')
}

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) {
    pass++
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    console.error(`  ✗ ${name}`)
  }
}

console.log('== 1. HTTP_STATUS_OPTIONS 清单完整性 ==')
ok(Array.isArray(HTTP_STATUS_OPTIONS), 'HTTP_STATUS_OPTIONS 是数组')
ok(HTTP_STATUS_OPTIONS.length >= 20, `清单不少于 20 项（实际 ${HTTP_STATUS_OPTIONS.length} 项）`)
const codes = HTTP_STATUS_OPTIONS.map((o) => o.code)
ok(codes.includes('429'), '必含 429（Too Many Requests）')
ok(codes.includes('529'), '必含 529（Anthropic Overloaded）')
ok(codes.includes('404'), '必含 404（Not Found）')
ok(codes.every((c) => /^\d{3}$/.test(c)), '所有码值均为 3 位纯数字')
ok(new Set(codes).size === codes.length, '码值无重复')
ok(HTTP_STATUS_OPTIONS.every((o) => typeof o.label === 'string' && o.label.length > 0), '每项均带语义短语 label')

console.log('== 2. sanitizeStatusCodeInput 净化函数 ==')
ok(sanitizeStatusCodeInput('200') === '200', "'200' → '200' 纯数字透传")
ok(sanitizeStatusCodeInput('429') === '429', "'429' → '429' 透传")
ok(sanitizeStatusCodeInput('200 OK') === '200', "'200 OK' → '200'（粘贴完整状态文本被净化）")
ok(sanitizeStatusCodeInput('429 Too Many Requests') === '429', "'429 Too Many Requests' → '429'")
ok(sanitizeStatusCodeInput('abc') === '', "'abc' → 空串（纯非数字）")
ok(sanitizeStatusCodeInput('12x3') === '123', "'12x3' → '123'（非数字字符全局剔除后重组，与前导截断在后端收敛一致）")
ok(sanitizeStatusCodeInput('1234') === '123', "'1234' → '123'（超 3 位截断）")
ok(sanitizeStatusCodeInput('  5  ') === '5', "'  5  ' → '5'（空白剔除）")
ok(sanitizeStatusCodeInput('') === '', '空串 → 空串（空值透传）')
ok(sanitizeStatusCodeInput(null) === '', 'null → 空串（容错）')
ok(sanitizeStatusCodeInput(undefined) === '', 'undefined → 空串（容错）')

console.log('== 3. ChatAnalysisToolbar.jsx 接入断言 ==')
const toolbarSrc = readSrc('ChatAnalysisToolbar.jsx')
ok(toolbarSrc.includes('list="chat-analysis-status-options"'), '状态输入框挂接 datalist（list 属性）')
ok(toolbarSrc.includes('<datalist id="chat-analysis-status-options">'), '渲染 datalist 候选清单')
ok(toolbarSrc.includes('HTTP_STATUS_OPTIONS.map'), '候选清单数据源为 HTTP_STATUS_OPTIONS')
ok(toolbarSrc.includes('setFilterStatus(sanitizeStatusCodeInput('), 'onChange 经 sanitizeStatusCodeInput 净化（只能输入数字）')
ok(toolbarSrc.includes('inputMode="numeric"'), 'inputMode=numeric（移动端数字键盘）')
ok(toolbarSrc.includes('filterStatusNot'), '取反勾选（filterStatusNot）仍在工具栏渲染')

console.log('== 4. useChatAnalysisFilters.js 恢复路径净化 ==')
const filtersSrc = readSrc('useChatAnalysisFilters.js')
ok(filtersSrc.includes('sanitizeStatusCodeInput((saved && saved.filterStatus)'), 'localStorage 恢复 filterStatus 时净化')
ok(filtersSrc.includes('filterStatusNot'), 'filterStatusNot 参与 localStorage 保存/恢复')

console.log('== 5. 三语 i18n key 完整性 ==')
for (const [name, dict] of [['zh-CN', zhCN], ['en', en], ['ja', ja]]) {
  ok(dict['chatAnalysis.statusPlaceholder'] !== undefined, `${name}: chatAnalysis.statusPlaceholder 存在`)
  ok(dict['chatAnalysis.invert'] !== undefined, `${name}: chatAnalysis.invert 存在`)
  ok(dict['chatAnalysis.status'] !== undefined, `${name}: chatAnalysis.status 存在`)
}

console.log(`\n结果：${pass} 通过，${fail} 失败`)
if (fail > 0) process.exit(1)
