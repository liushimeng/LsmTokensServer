// ClientWeb/src/pages/chat-analysis/detailViewEnhance.test.js
//
// 「对话详情」显示优化轻量自检脚本（无第三方测试框架，node 直跑）。
// 对应方案：tmpPlan/对话详情自动展开与IP显示与真全屏优化方案_20260923.md
// 覆盖：
//   1. 三语 i18n key 完整性（IP 地址 / 端口 / 全屏降级提示）
//   2. shared/remoteAddr.js —— splitHostPort / fmtRemoteAddr 四类落库形态 + 异常兜底
//   3. shared/fullscreen.js —— pickFullscreenApi 标准/前缀/不支持三桩，enterFullscreen
//      resolve/reject/返回 undefined 三类实现，webkit 前缀不传 options
//   4. 源码静态断言 —— DataTable 展开行 API、详情头部第 5 张 IP 卡、
//      InlineDetailRow 改用 useFullscreen、CSS 含 :fullscreen 与展开行样式
//
// 运行：
//   cd ClientWeb && node src/pages/chat-analysis/detailViewEnhance.test.js

import fs from 'node:fs'
import path from 'node:path'
import url from 'node:url'

import { splitHostPort, fmtRemoteAddr } from '../../shared/remoteAddr.js'
import { pickFullscreenApi, getActiveElement, isTargetActive, enterFullscreen, exitFullscreen } from '../../shared/fullscreen.js'

const __dirname = path.dirname(url.fileURLToPath(import.meta.url))
const srcDir = path.resolve(__dirname, '../..')
const localesDir = path.join(srcDir, 'i18n/locales')

function loadJson(name) {
  return JSON.parse(fs.readFileSync(path.join(localesDir, name), 'utf8'))
}
function readSrc(rel) {
  return fs.readFileSync(path.join(srcDir, rel), 'utf8')
}
const zhCN = loadJson('zh-CN.json')
const en = loadJson('en.json')
const ja = loadJson('ja.json')

let pass = 0
let fail = 0
function ok(cond, name) {
  if (cond) {
    pass++
    // eslint-disable-next-line no-console
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    // eslint-disable-next-line no-console
    console.error(`  ✗ ${name}`)
  }
}
function eq(actual, expected, name) {
  ok(JSON.stringify(actual) === JSON.stringify(expected), `${name}（期望 ${JSON.stringify(expected)}，实际 ${JSON.stringify(actual)}）`)
}

// ===== 1. 三语 i18n key 完整性 =====
console.log('\n[1] i18n 三语 key')
const requiredKeys = [
  'chatAnalysis.clientIp',
  'chatAnalysis.clientPort',
  'chatAnalysis.fullscreenUnsupported',
  // 既有键不得丢失（全屏按钮两态文案）
  'chatAnalysis.fullscreen',
  'chatAnalysis.exitFullscreen',
]
for (const key of requiredKeys) {
  ok(typeof zhCN[key] === 'string' && zhCN[key].length > 0, `[zh-CN] ${key} 非空`)
  ok(typeof en[key] === 'string' && en[key].length > 0, `[en]    ${key} 非空`)
  ok(typeof ja[key] === 'string' && ja[key].length > 0, `[ja]    ${key} 非空`)
}
ok(/端口|Port|ポート/.test(zhCN['chatAnalysis.clientPort'] + en['chatAnalysis.clientPort'] + ja['chatAnalysis.clientPort']),
  'clientPort 含 {port} 插值语义')
// 插值实现只认单花括号 {var}（src/i18n/interpolate.js），写成 {{port}} 会原样显示
for (const [lang, dict] of [['zh-CN', zhCN], ['en', en], ['ja', ja]]) {
  ok(/\{port\}/.test(dict['chatAnalysis.clientPort']), `[${lang}] clientPort 使用单花括号 {port}`)
}

// ===== 2. remoteAddr 解析 =====
console.log('\n[2] splitHostPort / fmtRemoteAddr')
eq(splitHostPort('10.0.0.5:54321'), { host: '10.0.0.5', port: '54321' }, 'IPv4:port')
eq(splitHostPort('[2408:8207::1]:443'), { host: '2408:8207::1', port: '443' }, '[IPv6]:port')
eq(splitHostPort('2408:8207::1'), { host: '2408:8207::1', port: '' }, '裸 IPv6 不误拆端口')
eq(splitHostPort('::1'), { host: '::1', port: '' }, '环回 IPv6 ::1')
eq(splitHostPort('::1:2'), { host: '::1:2', port: '' }, '多段冒号一律视为地址')
eq(splitHostPort('10.0.0.5'), { host: '10.0.0.5', port: '' }, '无端口 IPv4')
eq(splitHostPort('10.0.0.5:'), { host: '10.0.0.5:', port: '' }, '结尾裸冒号整体作为 host 展示')
eq(splitHostPort('10.0.0.5:99999'), { host: '10.0.0.5:99999', port: '' }, '越界端口不拆')
eq(splitHostPort(''), { host: '', port: '' }, '空串')
eq(splitHostPort(null), { host: '', port: '' }, 'null')
eq(splitHostPort(undefined), { host: '', port: '' }, 'undefined')
eq(splitHostPort('  10.0.0.5:80  '), { host: '10.0.0.5', port: '80' }, '首尾空白容错')
eq(fmtRemoteAddr('10.0.0.5:54321'), { host: '10.0.0.5', port: '54321', display: '10.0.0.5:54321' }, 'fmt 带端口')
eq(fmtRemoteAddr('[::1]:8080'), { host: '::1', port: '8080', display: '::1:8080' }, 'fmt IPv6')
eq(fmtRemoteAddr(''), { host: '', port: '', display: '-' }, 'fmt 空值兜底 -')

// ===== 3. Fullscreen API 适配层 =====
console.log('\n[3] pickFullscreenApi / enterFullscreen / exitFullscreen')
const docStandard = {
  documentElement: { requestFullscreen() {} },
  fullscreenElement: null,
  exitFullscreen() { this.fullscreenElement = null; return Promise.resolve() },
}
const apiStd = pickFullscreenApi(docStandard)
ok(apiStd.supported === true, '标准实现 supported=true')
eq([apiStd.requestName, apiStd.exitName, apiStd.elementName, apiStd.changeEvent],
  ['requestFullscreen', 'exitFullscreen', 'fullscreenElement', 'fullscreenchange'], '标准实现方法名/事件名')
ok(apiStd.prefixed === false, '标准实现 prefixed=false')

const docWebkit = { documentElement: { webkitRequestFullscreen() {} }, webkitFullscreenElement: null }
const apiWk = pickFullscreenApi(docWebkit)
ok(apiWk.supported === true && apiWk.requestName === 'webkitRequestFullscreen' && apiWk.prefixed === true,
  '旧 WebKit 前缀回退')

const apiNone = pickFullscreenApi({ documentElement: {} })
ok(apiNone.supported === false, '全不支持 supported=false')
ok(pickFullscreenApi(null).supported === false, 'doc 为 null 不抛异常')
ok(getActiveElement(apiNone, docStandard) === null, '不支持时 getActiveElement=null')

// enterFullscreen：resolve / reject / 返回 undefined
async function runFullscreenCases() {
  const el1 = { requestFullscreen: () => Promise.resolve() }
  eq(await enterFullscreen(apiStd, el1, { ...docStandard, fullscreenElement: el1 }), { ok: true, error: '' }, 'enter resolve → ok')

  const el2 = { requestFullscreen: () => Promise.reject(new Error('denied')) }
  const r2 = await enterFullscreen(apiStd, el2, docStandard)
  ok(r2.ok === false && /denied/.test(r2.error), 'enter reject → ok=false 且不抛异常（可降级）')

  let received = 'none'
  const el3 = { requestFullscreen: (opts) => { received = opts === undefined ? 'undefined' : JSON.stringify(opts) } }
  const doc3 = { ...docStandard, fullscreenElement: el3 }
  const r3 = await enterFullscreen(apiStd, el3, doc3)
  ok(r3.ok === true, 'enter 返回 undefined 时以 fullscreenElement 判定 ok')
  eq(received, '{"navigationUI":"hide"}', '标准实现传 navigationUI 选项')

  const el4 = { webkitRequestFullscreen: function () { received = 'wk-args:' + arguments.length } }
  const doc4 = { ...docWebkit, webkitFullscreenElement: el4 }
  const r4 = await enterFullscreen(apiWk, el4, doc4)
  ok(r4.ok === true, 'webkit 前缀实现可进入全屏')
  ok(received === 'wk-args:0', 'webkit 前缀实现不传 options 参数')
  ok(isTargetActive(apiWk, doc4, el4) === true, 'isTargetActive 只认自己')
  ok(isTargetActive(apiWk, doc4, { other: 1 }) === false, 'isTargetActive 不误判他人')

  const el5 = {}
  eq(await enterFullscreen(apiNone, el5, docStandard), { ok: false, error: 'unsupported' }, '不支持时 enter → ok=false')

  // exitFullscreen：无全屏元素视为成功；有元素则调用退出
  eq(await exitFullscreen(apiStd, { ...docStandard, fullscreenElement: null }), { ok: true, error: '' }, 'exit 无全屏元素 → ok')
  let exited = false
  eq(await exitFullscreen(apiStd, {
    documentElement: docStandard.documentElement,
    fullscreenElement: { x: 1 },
    exitFullscreen() { exited = true; return Promise.resolve() },
  }), { ok: true, error: '' }, 'exit 有全屏元素 → ok')
  ok(exited === true, 'exit 实际调用了 exitFullscreen')
  eq(await exitFullscreen(apiNone, docStandard), { ok: false, error: 'unsupported' }, '不支持时 exit → ok=false')
}

// ===== 4. 源码静态断言 =====
console.log('\n[4] 源码结构与样式')
const dataTable = readSrc('components/DataTable.jsx')
ok(/expandedIds/.test(dataTable) && /renderExpandedRow/.test(dataTable), 'DataTable 暴露展开行 API（expandedIds/renderExpandedRow）')
ok(/cell-expanded-row/.test(dataTable), 'DataTable 渲染 cell-expanded-row 整行')
ok(/row-expanded/.test(dataTable), 'DataTable 高亮来源数据行（row-expanded）')
ok(/collapsedIds/.test(dataTable) && /renderCollapsedRow/.test(dataTable), 'DataTable 保留旧折叠语义（AIRouteManage 零回归）')
ok(/Fragment/.test(dataTable), 'DataTable 使用 Fragment 输出「数据行 + 详情行」两行')

const pageIdx = readSrc('pages/chat-analysis/index.jsx')
ok(/expandedIds=\{expandedIds\}/.test(pageIdx) && /renderExpandedRow=/.test(pageIdx), 'ChatAnalysis 改用展开行（原数据行不再被替换）')
ok(!/collapsedIds=\{expandedIds\}/.test(pageIdx), 'ChatAnalysis 不再把 expandedIds 塞给 collapsedIds')
ok(/copyOkId/.test(pageIdx) && /markCopied/.test(pageIdx), '复制反馈按行隔离（copyOkId/markCopied）')

const dataHook = readSrc('pages/chat-analysis/useChatAnalysisData.js')
ok(/const \[copyOkId, setCopyOkId\] = useState\(null\)/.test(dataHook), 'useChatAnalysisData 暴露按行 copyOkId')
ok(!/const \[copyOk, setCopyOk\]/.test(dataHook), '全局 copyOk 布尔已移除')

const header = readSrc('pages/chat-analysis/DetailHeader.jsx')
const cardCount = (header.match(/className="detail-head-card"/g) || []).length
ok(cardCount === 5, `详情头部 KPI 为 5 张卡同行（实际 ${cardCount}）`)
ok(/request_remote_addr/.test(header), 'IP 卡数据源为 request_remote_addr')
ok(/t\('chatAnalysis\.clientIp'\)/.test(header), 'IP 卡使用 chatAnalysis.clientIp 文案')
ok(/fmtRemoteAddr/.test(header), 'IP 卡走 fmtRemoteAddr 解析 host/port')
ok(/dhc-value-wrap/.test(header), 'IPv6 过长可折行完整展示')

const inline = readSrc('pages/chat-analysis/InlineDetailRow.jsx')
ok(/useFullscreen\(rootRef\)/.test(inline), '详情面板接入 useFullscreen(rootRef)')
ok(/inline-detail-native-fullscreen/.test(inline), '原生全屏与降级全屏样式类分离')
ok(!/setFullscreen/.test(inline), '本地 fullscreen 布尔状态已移除（以 fullscreenElement 为事实来源）')
ok(/scrollIntoView/.test(inline) && /focus\(/.test(inline), '展开后自动定位聚焦（点了就有反馈）')
ok(/tabIndex=\{-1\}/.test(inline), '面板根节点可聚焦（键盘可用）')
ok(/isFallback/.test(inline), '仅降级态才锁背景滚动 / 接管 Esc')

const css = readSrc('index.css')
ok(/\.cell-expanded-row/.test(css), 'CSS 展开行样式存在')
ok(/:fullscreen/.test(css) && /:-webkit-full-screen/.test(css), 'CSS 含 :fullscreen 兜底（含 webkit 前缀）')
ok(/repeat\(5, minmax\(0, 1fr\)\)/.test(css), '≥1000px 固定 5 列栅格保证 KPI 同行')
ok(/\.detail-head-card \.dhc-value-wrap/.test(css), 'CSS 有 IP 折行样式')

// ===== 汇总 =====
runFullscreenCases().then(() => {
  // eslint-disable-next-line no-console
  console.log(`\n结果：${pass} 通过 / ${fail} 失败`)
  process.exit(fail === 0 ? 0 : 1)
})
