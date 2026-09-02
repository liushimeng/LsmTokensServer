// ClientWeb/src/shared/auth.test.js
//
// 阶段BO（20260902）：登录凭据本地存储（lsm_agent_creds）自检脚本（无第三方测试框架）。
// 覆盖：v5 手机号读写 / 空手机号保留策略 / v4 向后兼容 / v3 迁移 / 双 Tab 独立 / 清除。
//
// 运行：
//   cd ClientWeb && node src/shared/auth.test.js

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) { pass++; console.log('ok -', name) } else { fail++; console.error('FAIL -', name) }
}

function eq(actual, expected, name) {
  const cond = actual === expected
  if (cond) { pass++; console.log('ok -', name) }
  else { fail++; console.error('FAIL -', name, '| actual:', JSON.stringify(actual), '| expected:', JSON.stringify(expected)) }
}

// ---- 浏览器环境 mock（必须在 import auth.js 之前就绪）----
const store = new Map()
globalThis.localStorage = {
  getItem: (k) => (store.has(k) ? store.get(k) : null),
  setItem: (k, v) => { store.set(k, String(v)) },
  removeItem: (k) => { store.delete(k) },
  clear: () => { store.clear() },
}
// auth.js 模块顶层引用构建期常量 __APP_ROLE__（vite define），node 环境需显式注入
globalThis.__APP_ROLE__ = 'user'

const { saveCredentials, loadCredentials, clearCredentials } = await import('./auth.js')

const KEY = 'lsm_agent_creds'
const raw = () => JSON.parse(localStorage.getItem(KEY))

// 1) 用户名登录保存：用户名 + 手机号（v5 结构）
localStorage.clear()
saveCredentials('user', 'alice', '13812345678')
{
  const d = raw()
  eq(d.v, 5, '保存后存储版本为 v5')
  eq(d.v4, true, 'v4 兼容标志保留')
  eq(d.active, 'user', 'active 为 user')
  eq(d.user.mn, 'alice', '用户名落库')
  eq(d.user.ph, '13812345678', '手机号落库')
  const creds = loadCredentials()
  eq(creds.loginType, 'user', '读取：登录类型 user')
  eq(creds.userData.mn, 'alice', '读取：用户名回填值')
  eq(creds.userData.ph, '13812345678', '读取：手机号回填值')
  eq(creds.userSaved, true, '读取：userSaved 为 true')
}

// 2) 空手机号保留策略：未填 phone 不清空已存手机号
saveCredentials('user', 'alice', '')
eq(raw().user.ph, '13812345678', '空手机号不覆盖已存手机号')

// 3) 显式填写新手机号 → 覆盖
saveCredentials('user', 'alice', '13900000000')
eq(raw().user.ph, '13900000000', '新手机号覆盖旧值')

// 4) 双 Tab 独立：保存模型名不影响 user 条目（含 ph）
saveCredentials('model', 'gpt-x')
{
  const d = raw()
  eq(d.model.mn, 'gpt-x', '模型名落库')
  eq(d.active, 'model', 'active 切换为 model')
  eq(d.user.mn, 'alice', 'user 条目不被模型登录覆盖（用户名保留）')
  eq(d.user.ph, '13900000000', 'user 条目不被模型登录覆盖（手机号保留）')
  const creds = loadCredentials()
  eq(creds.loginType, 'model', '读取：最近 active 为 model')
  eq(creds.modelData.mn, 'gpt-x', '读取：模型名')
}

// 5) 未勾选记住我场景：仅存手机号（mn 置空），模型 Tab 数据保留
saveCredentials('user', '', '13700001111')
{
  const d = raw()
  eq(d.user.mn, '', 'mn 置空')
  eq(d.user.ph, '13700001111', '仅存手机号成功')
  eq(d.model.mn, 'gpt-x', '模型 Tab 已存名称保留（不被整体清除）')
  const creds = loadCredentials()
  eq(creds.userSaved, false, 'mn 为空时 userSaved 为 false')
  eq(creds.userData.ph, '13700001111', '手机号仍可读取回填')
}

// 6) v4 旧数据向后兼容（无 ph 字段）
localStorage.setItem(KEY, JSON.stringify({
  v: 4, v4: true, active: 'user',
  model: null, user: { mn: 'bob', ts: 1 },
}))
{
  const creds = loadCredentials()
  eq(creds.loginType, 'user', 'v4 数据：登录类型读取正常')
  eq(creds.userData.mn, 'bob', 'v4 数据：用户名读取正常')
  ok(!creds.userData.ph, 'v4 数据：无 ph 字段视为空，不报错')
  // v4 数据上执行保存 → 升级 v5 且补 ph
  saveCredentials('user', 'bob', '13600002222')
  const d = raw()
  eq(d.v, 5, 'v4 数据保存后升级 v5')
  eq(d.user.ph, '13600002222', 'v4 数据保存后写入手机号')
}

// 7) v3 旧数据迁移（单份 → v5 双份）
localStorage.setItem(KEY, JSON.stringify({ v: 3, loginType: 'user', mn: 'carol', ts: 2 }))
{
  const creds = loadCredentials()
  eq(creds.loginType, 'user', 'v3 迁移：登录类型 user')
  eq(creds.userData.mn, 'carol', 'v3 迁移：用户名保留')
  eq(creds.userData.ph, '', 'v3 迁移：手机号补空串')
  eq(raw().v, 5, 'v3 迁移后存储升级 v5')
}

// 8) v1/v2 无效数据 → 清除并返回 null
localStorage.setItem(KEY, JSON.stringify({ v: 2, ak: 'xxx' }))
{
  const creds = loadCredentials()
  eq(creds, null, 'v1/v2 无效数据返回 null')
  eq(localStorage.getItem(KEY), null, 'v1/v2 无效数据被清除')
}

// 9) clearCredentials 全量清除（登出语义）
saveCredentials('user', 'dave', '13500003333')
clearCredentials()
eq(loadCredentials(), null, '清除后读取为 null')
eq(localStorage.getItem(KEY), null, '清除后存储为空')

// 10) 损坏的 JSON → 静默返回 null（不抛异常）
localStorage.setItem(KEY, '{bad-json')
{
  let creds = 'sentinel'
  try { creds = loadCredentials() } catch { creds = 'threw' }
  eq(creds, null, '损坏 JSON 静默返回 null')
}

console.log(`\nauth.test.js 完成：${pass} 通过，${fail} 失败`)
if (fail > 0) process.exit(1)
