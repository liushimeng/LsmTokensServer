// ClientWeb/src/shared/listOrder.test.js
//
// 「列表顺序调整（置顶 / 上移 / 下移 / 置底）」轻量自检脚本（无第三方测试框架，node 直跑）。
// 对应方案：tmpPlan/智能路由目标源站列表置顶按钮与弹窗布局优化方案_20260923.md
//
// 覆盖：
//   1. shared/listOrder.js 纯函数 —— moveItem / pinToTop / pinToBottom / moveUp / moveDown
//      与 canMoveUp / canMoveDown / isFirst / canReorder 的真值表
//      （幂等、越界钳制、陈旧下标不搬移、入参不被修改、元素集合守恒、其余元素相对顺序不变）
//   2. 三语 i18n key 完整性 —— 本次新增 9 键（置顶/置底/上移/下移/顺序调整/提示/主源站/优先级说明）
//      必须在 zh-CN / en / ja 三份 locale 中同时存在（check:i18n 只校验「用到的键是否存在」，
//      不比较三语键集相等，故这里显式逐份断言）
//   3. 源码静态断言 —— AIRouteManage 接入 OrderButtons + moveItem、弹窗加宽 960、
//      旧 moveEndpoint 不残留；OrderButtons 四个按钮与 a11y 属性齐备；
//      index.css 提供 .sortable-* / .order-btn-group / .primary-tag；
//      DstEndPointManage 复用 .sortable-row 但**有意不含**顺序按钮（时间段顺序无语义）
//
// 运行：
//   cd ClientWeb && node src/shared/listOrder.test.js

import fs from 'node:fs'
import path from 'node:path'
import url from 'node:url'

import {
  moveItem, pinToTop, pinToBottom, moveUp, moveDown,
  canMoveUp, canMoveDown, isFirst, canReorder,
} from './listOrder.js'

const __dirname = path.dirname(url.fileURLToPath(import.meta.url))
const srcDir = path.resolve(__dirname, '..')
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
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    console.error(`  ✗ ${name}`)
  }
}
function eq(actual, expected, name) {
  ok(JSON.stringify(actual) === JSON.stringify(expected), `${name}（期望 ${JSON.stringify(expected)}，实际 ${JSON.stringify(actual)}）`)
}

// ===== 1. listOrder 纯函数 =====
console.log('\n[1] shared/listOrder.js 纯函数')

// 1.1 基本搬移：先摘出再插入，其余元素相对顺序不变
eq(moveItem(['a', 'b', 'c', 'd'], 2, 0), ['c', 'a', 'b', 'd'], 'moveItem 下标 2 → 0')
eq(moveItem(['a', 'b', 'c', 'd'], 1, 3), ['a', 'c', 'd', 'b'], 'moveItem 下标 1 → 3（摘出后插入）')
eq(pinToTop(['a', 'b', 'c', 'd', 'e'], 4), ['e', 'a', 'b', 'c', 'd'], 'pinToTop 末行一次点击直达首位')
eq(pinToBottom(['a', 'b', 'c', 'd'], 0), ['b', 'c', 'd', 'a'], 'pinToBottom 首位 → 末位')
eq(moveUp(['a', 'b', 'c'], 1), ['b', 'a', 'c'], 'moveUp 相邻交换（原语义保持）')
eq(moveDown(['a', 'b', 'c'], 1), ['a', 'c', 'b'], 'moveDown 相邻交换（原语义保持）')

// 1.2 幂等：端点上的置顶/置底不产生任何变化
eq(pinToTop(['a', 'b', 'c'], 0), ['a', 'b', 'c'], 'pinToTop 首位幂等')
eq(pinToBottom(['a', 'b', 'c'], 2), ['a', 'b', 'c'], 'pinToBottom 末位幂等')
eq(moveUp(['a', 'b', 'c'], 0), ['a', 'b', 'c'], 'moveUp 首位幂等（越界 to 钳制回 0）')
eq(moveDown(['a', 'b', 'c'], 2), ['a', 'b', 'c'], 'moveDown 末位幂等（越界 to 钳制回末位）')
eq(moveItem(['a', 'b', 'c'], 1, 1), ['a', 'b', 'c'], 'from === to 原样返回')

// 1.3 越界与异常入参：to 钳制到端点；from 非法（陈旧下标）一律不搬移
eq(moveItem(['a', 'b', 'c'], 1, 999), ['a', 'c', 'b'], 'to 正向越界钳制到末位')
eq(moveItem(['a', 'b', 'c'], 1, -999), ['b', 'a', 'c'], 'to 负向越界钳制到首位')
eq(moveItem(['a', 'b', 'c'], 1, 1.7), ['a', 'b', 'c'], 'to 非整数截断')
eq(moveItem(['a', 'b', 'c'], 9, 0), ['a', 'b', 'c'], 'from 越界（列表已变短）不搬移')
eq(moveItem(['a', 'b', 'c'], -1, 0), ['a', 'b', 'c'], 'from 负下标不搬移')
eq(moveItem(['a', 'b', 'c'], NaN, 0), ['a', 'b', 'c'], 'from NaN 不搬移')
eq(moveItem(['a', 'b', 'c'], 1.5, 0), ['a', 'b', 'c'], 'from 非整数不搬移')
eq(moveItem([], 0, 0), [], '空数组不抛错')
eq(moveItem(null, 0, 2), [], '非数组入参返回空数组')
eq(moveItem(['x'], 0, 0), ['x'], '单元素原样')

// 1.4 React state 安全：绝不修改入参，返回新引用
const origin = ['a', 'b', 'c', 'd']
const moved = pinToTop(origin, 3)
eq(origin, ['a', 'b', 'c', 'd'], '入参数组未被修改')
ok(moved !== origin, '返回新数组引用')
ok(moved !== origin, '置顶结果与入参非同一对象（触发重渲染）')
eq(pinToTop(origin, 0) === origin, false, '幂等路径也返回新引用')

// 1.5 守恒：长度与元素集合不变（多源站列表搬移不得丢记录 / 造重复）
const big = Array.from({ length: 17 }, (_, i) => `ep${i + 1}`)
const pinned = pinToTop(big, 16)
eq(pinned.length, 17, '置顶后长度守恒')
eq([...pinned].sort().join(','), [...big].sort().join(','), '置顶后元素集合守恒')
eq(pinned[0], 'ep17', '置顶后原末位成为主源站（下标 0）')
eq(pinned.slice(1), big.slice(0, 16), '其余元素相对顺序保持不变')

// 1.6 按钮可用性判定（OrderButtons 的 disabled 依据）
eq(canMoveUp(big, 0), false, 'canMoveUp(0) = false（首位禁止上移/置顶）')
eq(canMoveUp(big, 1), true, 'canMoveUp(1) = true')
eq(canMoveDown(big, 16), false, 'canMoveDown(末位) = false（末位禁止下移/置底）')
eq(canMoveDown(big, 15), true, 'canMoveDown(15) = true')
eq(canMoveUp(['a'], 0), false, '单元素列表 canMoveUp = false')
eq(canMoveDown(['a'], 0), false, '单元素列表 canMoveDown = false')
eq(canMoveUp(null, 1), false, '非数组 canMoveUp = false')
eq(isFirst(big, 0), true, 'isFirst(0) = true（主源站标记）')
eq(isFirst(big, 1), false, 'isFirst(1) = false')
eq(isFirst([], 0), false, '空列表 isFirst = false')
eq(canReorder(['a']), false, 'canReorder 单元素 = false')
eq(canReorder(['a', 'b']), true, 'canReorder 双元素 = true')

// 1.7 连续操作语义：置底后再置顶应回到首位（等价于一次置顶）
eq(pinToTop(pinToBottom(['a', 'b', 'c'], 0), 2), ['a', 'b', 'c'], '置底后回置顶 = 原序')

// ===== 2. 三语 i18n key 完整性 =====
console.log('\n[2] i18n 三语 key 完整性')
const requiredKeys = [
  // 新增（阶段CO）
  'common.pinTop', 'common.pinTopHint',
  'common.pinBottom', 'common.pinBottomHint',
  'common.moveUp', 'common.moveDown', 'common.orderOps',
  'aiRouteManage.primaryTag', 'aiRouteManage.orderHint',
  // 既有键不得丢失：orderHint 同行展示的「优先级」标签（此前为零引用死键，本次复活）
  'aiRouteManage.priority', 'aiRouteManage.removeOp', 'aiRouteManage.directConnect',
  'aiRouteManage.converter', 'aiRouteManage.enabledStatus', 'aiRouteManage.disabledStatus',
  'aiRouteManage.noSelectedEndpoints',
]
for (const [label, dict] of [['zh-CN', zhCN], ['en', en], ['ja', ja]]) {
  const missing = requiredKeys.filter((k) => !(k in dict))
  ok(missing.length === 0, `${label} 含全部 ${requiredKeys.length} 个键${missing.length ? '，缺失：' + missing.join(', ') : ''}`)
}
// 新键必须有实际文案（空串会让按钮不可见 / 无障碍失效）
for (const [label, dict] of [['zh-CN', zhCN], ['en', en], ['ja', ja]]) {
  const empties = requiredKeys.filter((k) => !String(dict[k] || '').trim())
  ok(empties.length === 0, `${label} 新键文案非空${empties.length ? '，空值：' + empties.join(', ') : ''}`)
}
// 中文「置顶」两字宽度是行内按钮簇的关键布局约束，独立于「上移」等提示文案
ok(zhCN['common.pinTop'] === '置顶' && zhCN['common.pinBottom'] === '置底', 'zh-CN 置顶/置底 为两字标签（控制按钮簇宽度）')

// ===== 3. 源码静态断言 =====
console.log('\n[3] 源码静态断言')
const airoute = readSrc('pages/AIRouteManage.jsx')
const orderButtons = readSrc('components/OrderButtons.jsx')
const css = readSrc('index.css')
const dst = readSrc('pages/DstEndPointManage.jsx')

// 3.1 AIRouteManage：接入通用组件 + 纯函数，旧实现不残留
ok(/import OrderButtons from '\.\.\/components\/OrderButtons'/.test(airoute), 'AIRouteManage 引入 OrderButtons')
ok(/import \{[^}]*moveItem[^}]*\} from '\.\.\/shared\/listOrder'/.test(airoute), 'AIRouteManage 引入 shared/listOrder.moveItem')
ok(/reorderEndpoint/.test(airoute), 'AIRouteManage 使用 reorderEndpoint(from, to)')
// 词边界必须显式声明：`/moveEndpoint/` 会被 removeEndpoint（删除源站，仍在用）子串命中
ok(!/\bmoveEndpoint\b/.test(airoute), 'AIRouteManage 不再残留旧 moveEndpoint')
ok(/removeEndpoint/.test(airoute), 'AIRouteManage 保留 removeEndpoint（移除源站，与顺序无关）')
ok(/width=\{960\}/.test(airoute), 'AIRouteManage 弹窗宽度 960（760 + 200）')
ok(!/width=\{760\}/.test(airoute), 'AIRouteManage 不再残留旧宽度 760')
ok(/<OrderButtons\s+index=\{i\}\s+total=\{form\.endpoints\.length\}/.test(airoute), 'OrderButtons 绑定 index + total')
ok(/onMove=\{\(to\) => reorderEndpoint\(i, to\)\}/.test(airoute), 'OrderButtons onMove 目标下标回传')
ok(airoute.includes("{t('aiRouteManage.primaryTag')}"), '渲染主源站标记（置顶效果可见）')
ok(airoute.includes("{t('aiRouteManage.orderHint')}"), '渲染顺序即优先级提示')
ok(/className=\{?'sortable-list'/.test(airoute) || airoute.includes('className="sortable-list"'), '列表容器使用 .sortable-list（限高内滚）')

// 3.2 OrderButtons：四个操作 + 无障碍属性齐备
ok(orderButtons.includes("t('common.pinTop')"), 'OrderButtons 渲染 置顶')
ok(orderButtons.includes("t('common.pinBottom')"), 'OrderButtons 渲染 置底')
ok(orderButtons.includes("t('common.moveUp')") && orderButtons.includes("t('common.moveDown')"), 'OrderButtons 渲染 上移/下移 提示')
ok(/↑/.test(orderButtons) && /↓/.test(orderButtons), 'OrderButtons 保留 ↑ ↓ 原图标语义')
ok(/btn\(0,/.test(orderButtons), '置顶目标下标恒为 0')
ok(/count - 1/.test(orderButtons), '置底目标下标为 total-1')
ok(/aria-label=\{t\('common\.orderOps'\)\}/.test(orderButtons), '按钮组带 aria-label（role=group）')
ok(/aria-label=\{hint\}/.test(orderButtons), '每个按钮 aria-label = 完整提示语')
ok(/type="button"/.test(orderButtons), '按钮 type=button（弹窗内不误提交）')
ok(/title=\{hint\}/.test(orderButtons), '按钮 title 悬停提示')
ok(/disabled=\{disabled \|\| !reorderable \|\| off\}/.test(orderButtons), 'disabled 三态合成（整组禁用 / 少于 2 条 / 端点位）')
ok(/role="group"/.test(orderButtons), '顺序按钮成组（role=group）')

// 3.3 CSS：通用列表行视觉语言齐备，AIRouteManage 行内 style 已外移
for (const cls of ['.sortable-list', '.sortable-list-hint', '.sortable-row', '.sortable-row-main',
  '.sortable-row-index', '.sortable-row-actions', '.order-btn-group', '.primary-tag', '.order-divider']) {
  ok(css.includes(cls), `index.css 定义 ${cls}`)
}
ok(/\.sortable-list\s*\{[^}]*max-height:\s*46vh/.test(css), '.sortable-list 限高内滚（长列表不撑破弹窗）')
ok(/\.sortable-row-actions\s*\{[^}]*flex-shrink:\s*0/.test(css), '操作簇 flex-shrink:0（不被长源站名压扁）')
ok(/\.sortable-row\s*\{[^}]*flex-wrap:\s*wrap/.test(css), '.sortable-row 允许换行（≤600px 全屏弹窗按钮不被压扁）')
ok(/\.sortable-row-name\s*\{[^}]*text-overflow:\s*ellipsis/.test(css), '名称区 ellipsis + title 兜底')

// 3.4 DstEndPointManage：复用行布局，但有意不提供顺序按钮（时间段顺序无语义）
ok(dst.includes('className="sortable-row"'), 'DstEndPointManage 工作时间段行复用 .sortable-row')
ok(dst.includes('className="sortable-row-index"'), 'DstEndPointManage 序号复用 .sortable-row-index 缩进')
ok(!/OrderButtons/.test(dst), 'DstEndPointManage 不引入 OrderButtons（顺序不承载业务语义）')

// 3.5 双构建隔离红线（CLAUDE.md §2.5）：共享代码不得引入管理端专属字样
ok(!/ManagerLogin|UserManageInterface|managerPassword/.test(orderButtons), 'OrderButtons 无管理端专属字样（用户端产物纯净）')
ok(!/ManagerLogin|UserManageInterface/.test(css), 'index.css 新增段无管理端专属字样')

// ===== 结果 =====
console.log(`\nlistOrder.test.js: ${pass} passed, ${fail} failed`)
if (fail) process.exit(1)
