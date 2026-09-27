// ClientWeb/src/shared/listMemory.test.js
//
// 列表记忆工具自检脚本（无第三方测试框架，localStorage mock）。
//
// 运行：
//   node src/shared/listMemory.test.js

import { roleSuffix, loadListMemory, saveListMemory, resolveSpanTarget } from './listMemory.js'

// localStorage mock（node 环境无 window）
const store = new Map()
globalThis.window = {
  localStorage: {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => { store.set(k, String(v)) },
    removeItem: (k) => { store.delete(k) },
  },
}

let pass = 0
let fail = 0
function eq(a, b, name) {
  if (a === b) { pass++; console.log(`ok   ${name}`) }
  else { fail++; console.error(`FAIL ${name}: expect ${b}, got ${a}`) }
}

// ---- roleSuffix ----
eq(roleSuffix(true), 'manager', 'roleSuffix 管理端')
eq(roleSuffix(false), 'user', 'roleSuffix 用户端')

// ---- save/load 往返 ----
const key = `lsm:airoute:span:${roleSuffix(true)}`
saveListMemory(key, { span: -6 })
eq(loadListMemory(key).span, -6, 'span 记忆写读往返（最近6小时=-6）')

// ---- 容错 ----
eq(loadListMemory('lsm:not-exist'), null, '缺失 key 返回 null')
store.set('lsm:bad', '{not-json')
eq(loadListMemory('lsm:bad'), null, '非法 JSON 返回 null')
store.set('lsm:num', '123')
eq(loadListMemory('lsm:num'), null, '非对象 JSON 返回 null')

// ---- resolveSpanTarget 优先级 ----
eq(resolveSpanTarget(-6, 30, 3), -6, '记忆值优先（小时档）')
eq(resolveSpanTarget(null, 30, 3), 30, '无记忆回退 legacy 兼容值')
eq(resolveSpanTarget(undefined, null, 3), 3, '全缺失回退默认档')
eq(resolveSpanTarget(NaN, null, 7), 7, 'NaN 视为无效')
eq(resolveSpanTarget(0, null, 3), 0, '0（无限制）为有效值')

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
