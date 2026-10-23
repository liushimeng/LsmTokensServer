// prefs.js 纯函数自检（node 直跑）：node src/pages/service-quality/prefs.test.js
// 覆盖：key 生成（角色隔离）/ 白名单校验 / 缺失兜底 / 非法 JSON 兜底 / 裸字符串兼容 / 写入。
import { qosPrefKey, loadQosPref, saveQosPref } from './prefs.js'

let failed = 0
function eq(actual, expected, msg) {
  const a = JSON.stringify(actual)
  const e = JSON.stringify(expected)
  if (a !== e) {
    failed++
    console.error(`  ✗ ${msg}: 期望 ${e}，实际 ${a}`)
  } else {
    console.log(`  ✓ ${msg}`)
  }
}

// ---- 内存版 localStorage 桩 ----
const store = new Map()
globalThis.window = {
  localStorage: {
    getItem: (k) => (store.has(k) ? store.get(k) : null),
    setItem: (k, v) => { store.set(k, String(v)) },
    removeItem: (k) => { store.delete(k) },
  },
}

console.log('prefs.js 自检')

// key 生成：角色隔离
eq(qosPrefKey('manager', 'statWin'), 'lsm:serviceQuality:statWin:manager', '管理端 key')
eq(qosPrefKey('user', 'statWin'), 'lsm:serviceQuality:statWin:user', '用户端 key 与管理端隔离')
eq(qosPrefKey('unknown', 'statWin'), 'lsm:serviceQuality:statWin:user', '非法角色回落 user')

// 读取：白名单命中
const KEY = qosPrefKey('manager', 'statWin')
saveQosPref(KEY, '7d')
eq(loadQosPref(KEY, ['24h', '7d', '30d'], '24h'), '7d', '白名单内的记忆值原样返回')

// 读取：白名单外 → 回落默认
saveQosPref(KEY, '999d')
eq(loadQosPref(KEY, ['24h', '7d', '30d'], '24h'), '24h', '白名单外的记忆值回落默认')

// 读取：key 不存在 → 回落默认
eq(loadQosPref(qosPrefKey('user', 'statWin'), ['24h', '7d', '30d'], '24h'), '24h', '无记忆时回落默认')

// 读取：非法 JSON → 回落默认（不抛异常）
store.set(KEY, '{bad-json')
eq(loadQosPref(KEY, ['24h', '7d', '30d'], '24h'), '24h', '非法 JSON 回落默认')

// 读取：裸字符串（未 JSON.stringify）→ 按原值解析
store.set(KEY, '30d')
eq(loadQosPref(KEY, ['24h', '7d', '30d'], '24h'), '30d', '裸字符串记忆兼容')

// 布尔型记忆（明细表折叠状态）
const BOOL_KEY = qosPrefKey('manager', 'periodTableOpen')
saveQosPref(BOOL_KEY, true)
eq(loadQosPref(BOOL_KEY, null, false), true, '布尔记忆读取')
saveQosPref(BOOL_KEY, false)
eq(loadQosPref(BOOL_KEY, null, true), false, '布尔记忆读取（false 不被当成空）')

// 无白名单时不校验取值
eq(loadQosPref(BOOL_KEY, null, false), false, 'allowed=null 时不做白名单校验')

if (failed) {
  console.error(`❌ prefs.js 自检失败：${failed} 项不通过`)
  process.exit(1)
}
console.log('✅ prefs.js 自检通过')
