// ClientWeb/src/shared/jsonTree.test.js
//
// JSON 美化树纯数据工具自检脚本（无第三方测试框架）。
// v2.0.74 阶段AS：配额制 buildJsonTreeNodes 废弃，本脚本改为验证
// 惰性渲染配套的纯函数契约（组件层 JsonTree.jsx 为薄渲染层）：
//   1) parseJsonSafely：合法/非法/空/对象直通 4 分支；
//   2) countJsonNodes：容器+后代计数与 cap 早退；
//   3) childPathOf / entriesOf：路径唯一性（特殊字符键 vs 嵌套键不冲突）；
//   4) collectDefaultExpandedPaths：小树全开、大树浅开（大数组默认折叠）；
//   5) collectContainerPaths：按层收集（"展开至 N 层"工具栏的数据源）；
//   6) escapeJsonString：与 JSON.stringify 转义规则对齐（反斜杠/控制字符）。
//
// 运行：
//   cd ClientWeb && node src/shared/jsonTree.test.js

import {
  parseJsonSafely,
  countJsonNodes,
  entriesOf,
  childPathOf,
  collectContainerPaths,
  collectDefaultExpandedPaths,
  escapeJsonString,
  JSON_TREE_PAGE_SIZE,
  JSON_STRING_INLINE_LIMIT,
  JSON_RENDER_BUDGET,
} from './json.js'

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) { pass++; console.log('ok -', name) } else { fail++; console.error('FAIL -', name) }
}

function eq(a, b, name) {
  const good = a === b
  if (good) { pass++; console.log('ok -', name) }
  else {
    fail++
    console.error('FAIL -', name)
    console.error('    expected:', JSON.stringify(b))
    console.error('    actual:  ', JSON.stringify(a))
  }
}

// ===== 1) parseJsonSafely =====
console.log('\n--- parseJsonSafely ---')
ok(parseJsonSafely('{"a":1}').ok === true, '合法 JSON 字符串 → ok')
eq(parseJsonSafely('{"a":1}').data.a, 1, '解析结果可访问')
ok(parseJsonSafely('not a json').ok === false, '非法 JSON → not ok')
eq(parseJsonSafely('not a json').reason, 'parse-error', '非法 JSON reason=parse-error')
eq(parseJsonSafely(null).reason, 'empty', 'null → reason=empty')
eq(parseJsonSafely(undefined).reason, 'empty', 'undefined → reason=empty')
ok(parseJsonSafely({ x: 1 }).ok === true, '已解析对象直通')
eq(parseJsonSafely({ x: 1 }).data.x, 1, '对象直通数据可访问')
ok(parseJsonSafely('  [1,2] ').ok === true, '前后空白容忍')
eq(parseJsonSafely('').ok, false, '空字符串 → 解析失败（组件走原样兜底）')

// ===== 2) countJsonNodes =====
console.log('\n--- countJsonNodes ---')
eq(countJsonNodes({ a: 1, b: { c: 2 } }), 4, '{a:1,b:{c:2}} = 4 节点（根+a+b+c）')
eq(countJsonNodes([1, 2, 3]), 4, '[1,2,3] = 4 节点（根+3 项）')
eq(countJsonNodes('str'), 1, '标量 = 1 节点')
eq(countJsonNodes(null), 1, 'null = 1 节点')
eq(countJsonNodes({}), 1, '空对象 = 1 节点')
// 大数组（每项 {role, content} = 3 节点 × 402 项 + 根 = 1207）
const bigArr = Array.from({ length: 402 }, (_, i) => ({ role: 'user', content: `m${i}` }))
eq(countJsonNodes(bigArr), 402 * 3 + 1, '402 项对象数组 = 1207 节点')
// cap 早退：硬停机，计数精确停在 cap+1（只需判断"是否超限"的场景）
eq(countJsonNodes(bigArr, 60), 61, 'cap=60 早退：计数停在 61')
eq(countJsonNodes(bigArr, 2000), 1207, 'cap 大于全量：返回精确总数')

// ===== 3) entriesOf / childPathOf 路径唯一性 =====
console.log('\n--- entriesOf / childPathOf ---')
eq(entriesOf({ a: 1 }).length, 1, '对象 entries 长度')
eq(entriesOf([7])[0][1], 7, '数组 entries 取值')
eq(entriesOf([7])[0][0], 0, '数组 entries 下标')
eq(childPathOf('$', false, 'model'), '$["model"]', '对象键路径带引号')
eq(childPathOf('$', true, 3), '$[3]', '数组项路径为下标')
// 特殊字符键 vs 嵌套键：路径不冲突（唯一性保证状态 key 不串）
const tricky = { 'a.b': 1, a: { b: 2 } }
const p1 = childPathOf('$', false, 'a.b')
const p2 = childPathOf(childPathOf('$', false, 'a'), false, 'b')
ok(p1 !== p2, `含点键 "${p1}" 与嵌套键 "${p2}" 路径不冲突`)
const trickyPaths = new Set(entriesOf(tricky).map(([k]) => childPathOf('$', false, k)))
eq(trickyPaths.size, 2, '兄弟键路径集合无重复')

// ===== 4) collectDefaultExpandedPaths =====
console.log('\n--- collectDefaultExpandedPaths ---')
const small = collectDefaultExpandedPaths({ a: 1, b: { c: 2 } })
ok(small.has('$'), '小树：根展开')
ok(small.has('$["b"]'), '小树：小容器 b 自动展开（子树 2 节点 ≤ 60）')
eq(small.size, 2, '小树默认展开集 = 根 + b')

const nestedSmall = collectDefaultExpandedPaths({ a: { b: { c: { d: 1 } } } })
ok(nestedSmall.has('$["a"]') && nestedSmall.has('$["a"]["b"]') && nestedSmall.has('$["a"]["b"]["c"]'),
  '全小嵌套树逐层自动展开')

const big = {
  model: 'LongCat-2.0',
  messages: bigArr,                       // 805 节点 > 60 → 默认折叠
  stream_options: { include_usage: true }, // 2 节点 ≤ 60 → 自动展开
}
const bigDefault = collectDefaultExpandedPaths(big)
ok(bigDefault.has('$'), '大树：根始终展开')
ok(!bigDefault.has('$["messages"]'), '大树：805 节点的 messages 默认折叠（防卡顿）')
ok(bigDefault.has('$["stream_options"]'), '大树：小容器 stream_options 仍自动展开')
ok(!bigDefault.has('$["model"]'), '标量 key 不进展开集')

// ===== 5) collectContainerPaths =====
console.log('\n--- collectContainerPaths ---')
const deep = { a: { b: { c: { d: 1 } } } }
const lv1 = collectContainerPaths(deep, 1)
ok(lv1.has('$') && lv1.size === 1, '展开至 1 层 = 仅根')
const lv2 = collectContainerPaths(deep, 2)
ok(lv2.has('$') && lv2.has('$["a"]') && lv2.size === 2, '展开至 2 层 = 根 + a')
const lv3 = collectContainerPaths(deep, 3)
ok(lv3.has('$["a"]["b"]') && lv3.size === 3, '展开至 3 层 = 根 + a + b')
const lvAll = collectContainerPaths(deep, 99)
ok(lvAll.size === 4, '大层数收集全部容器路径（无层级上限）')

// 阶段AU：「展开全部」按钮 = collectContainerPaths(data, Infinity)
const lvInf = collectContainerPaths(deep, Number.POSITIVE_INFINITY)
ok(lvInf.size === 4 && lvInf.has('$["a"]["b"]["c"]'), 'Infinity 收集全部容器路径（展开全部）')

// ===== 6) escapeJsonString（与 JSON.stringify 转义对齐） =====
console.log('\n--- escapeJsonString ---')
eq(escapeJsonString('plain'), 'plain', '普通文本不变')
eq(escapeJsonString('a"b'), 'a\\"b', '双引号转义')
eq(escapeJsonString('a\\b'), 'a\\\\b', '反斜杠转义（阶段AS 修复点）')
eq(escapeJsonString('a\nb'), 'a\\nb', '换行转义')
eq(escapeJsonString('a\tb'), 'a\\tb', 'Tab 转义')
eq(escapeJsonString('a\rb'), 'a\\rb', '回车转义')
eq(escapeJsonString('a\x01b'), 'a\\u0001b', '控制字符 0x01 → \\u0001')
// 与 JSON.stringify 内部转义一致性（去掉首尾引号后逐字符相同）
const samples = ['q"x', 'back\\slash', 'line1\nline2', 'tab\there', 'ctrl\x1f']
samples.forEach((s, i) => {
  const jsonified = JSON.stringify(s).slice(1, -1)
  eq(escapeJsonString(s), jsonified, `与 JSON.stringify 转义一致 #${i + 1}`)
})

// ===== 7) 常量契约（组件分页/截断/预算的默认值不被误改） =====
console.log('\n--- 常量契约 ---')
eq(JSON_TREE_PAGE_SIZE, 100, '分页页大小默认 100')
eq(JSON_STRING_INLINE_LIMIT, 500, '超长字符串阈值默认 500')
ok(JSON_RENDER_BUDGET >= 1000 && JSON_RENDER_BUDGET <= 10000, `渲染预算在合理区间：${JSON_RENDER_BUDGET}`)

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
