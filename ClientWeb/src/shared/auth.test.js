// ClientWeb/src/shared/auth.test.js
//
// 阶段BP（20260903）：登录凭据本地存储（lsm_agent_creds）自检脚本（无第三方测试框架）。
// 覆盖：v6 手机号加密读写 / 空手机号保留策略 / v5 明文向后兼容 / v4 兼容 / v3 迁移 / 双 Tab 独立 / 清除。
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

// Node 22+ 原生支持 Web Crypto API（globalThis.crypto.subtle），无需 mock

const { saveCredentials, loadCredentials, clearCredentials } = await import('./auth.js')
const { encrypt, decrypt, isEncrypted } = await import('./crypto.js')

const KEY = 'lsm_agent_creds'
const raw = () => JSON.parse(localStorage.getItem(KEY))

// ---- 阶段BP：加密/解密基础测试 ----
console.log('\n== 阶段BP：加密工具测试 ==')
{
  const plaintext = '13812345678'
  const ciphertext = await encrypt(plaintext)
  ok(typeof ciphertext === 'string', 'encrypt 返回字符串')
  ok(isEncrypted(ciphertext), '密文可被 isEncrypted 识别')
  ok(ciphertext !== plaintext, '密文不等于明文')

  const decrypted = await decrypt(ciphertext)
  eq(decrypted, plaintext, 'decrypt 还原明文正确')

  // 同一明文两次加密结果不同（随机 IV）
  const ciphertext2 = await encrypt(plaintext)
  ok(ciphertext !== ciphertext2, '同一明文两次加密结果不同（随机 IV）')
  eq(await decrypt(ciphertext2), plaintext, '第二次密文解密正确')

  // 空值处理
  eq(await encrypt(''), '', 'encrypt 空字符串返回空')
  eq(await decrypt(''), '', 'decrypt 空字符串返回空')
  ok(!isEncrypted(''), '空字符串不被识别为密文')
  ok(!isEncrypted('13812345678'), '明文手机号不被识别为密文')
}

// ---- 阶段BP：v6 加密存储读写测试 ----
console.log('\n== 阶段BP：v6 加密存储读写测试 ==')
localStorage.clear()
await saveCredentials('user', 'alice', '13812345678')
{
  const d = raw()
  eq(d.v, 6, '保存后存储版本为 v6')
  eq(d.v4, true, 'v4 兼容标志保留')
  eq(d.active, 'user', 'active 为 user')
  eq(d.user.mn, 'alice', '用户名落库')
  ok(isEncrypted(d.user.ph), '手机号为加密密文（非明文）')
  ok(d.user.ph !== '13812345678', '手机号不明文存储')

  const creds = await loadCredentials()
  eq(creds.loginType, 'user', '读取：登录类型 user')
  eq(creds.userData.mn, 'alice', '读取：用户名回填值')
  eq(creds.userData.ph, '13812345678', '读取：手机号解密后回填正确')
  eq(creds.userSaved, true, '读取：userSaved 为 true')
}

// 空手机号保留策略：未填 phone 不清空已存手机号（保留密文）
await saveCredentials('user', 'alice', '')
ok(isEncrypted(raw().user.ph), '空手机号保留策略：密文保留')
{
  const creds = await loadCredentials()
  eq(creds.userData.ph, '13812345678', '空手机号保留策略：解密后仍为原值')
}

// 显式填写新手机号 → 覆盖
await saveCredentials('user', 'alice', '13900000000')
{
  const creds = await loadCredentials()
  eq(creds.userData.ph, '13900000000', '新手机号覆盖旧值')
}

// 双 Tab 独立：保存模型名不影响 user 条目（含加密 ph）
await saveCredentials('model', 'gpt-x')
{
  const d = raw()
  eq(d.model.mn, 'gpt-x', '模型名落库')
  eq(d.active, 'model', 'active 切换为 model')
  eq(d.user.mn, 'alice', 'user 条目不被模型登录覆盖（用户名保留）')
  ok(isEncrypted(d.user.ph), 'user 条目手机号密文保留')
  const creds = await loadCredentials()
  eq(creds.loginType, 'model', '读取：最近 active 为 model')
  eq(creds.modelData.mn, 'gpt-x', '读取：模型名')
  eq(creds.userData.ph, '13900000000', '读取：手机号解密后保留')
}

// 未勾选记住我场景：仅存手机号（mn 置空），模型 Tab 数据保留
await saveCredentials('user', '', '13700001111')
{
  const d = raw()
  eq(d.user.mn, '', 'mn 置空')
  ok(isEncrypted(d.user.ph), '仅存手机号（加密）')
  eq(d.model.mn, 'gpt-x', '模型 Tab 已存名称保留（不被整体清除）')
  const creds = await loadCredentials()
  eq(creds.userSaved, false, 'mn 为空时 userSaved 为 false')
  eq(creds.userData.ph, '13700001111', '手机号仍可读取回填')
}

// ---- 阶段BP：v5 明文数据向后兼容（升级 v6 加密） ----
console.log('\n== 阶段BP：v5 明文兼容测试 ==')
localStorage.setItem(KEY, JSON.stringify({
  v: 5, v4: true, active: 'user',
  model: null, user: { mn: 'bob', ph: '13600002222', ts: 1 },
}))
{
  const creds = await loadCredentials()
  eq(creds.loginType, 'user', 'v5 明文数据：登录类型读取正常')
  eq(creds.userData.mn, 'bob', 'v5 明文数据：用户名读取正常')
  eq(creds.userData.ph, '13600002222', 'v5 明文数据：手机号明文直接返回')
  // v5 明文数据上执行保存 → 升级 v6 且加密
  await saveCredentials('user', 'bob', '13600003333')
  const d = raw()
  eq(d.v, 6, 'v5 明文数据保存后升级 v6')
  ok(isEncrypted(d.user.ph), 'v5 明文数据保存后手机号加密')
  const creds2 = await loadCredentials()
  eq(creds2.userData.ph, '13600003333', 'v5→v6 升级后手机号解密正确')
}

// ---- 阶段AS v4 旧数据向后兼容（无 ph 字段） ----
console.log('\n== 阶段AS v4 兼容测试 ==')
localStorage.setItem(KEY, JSON.stringify({
  v: 4, v4: true, active: 'user',
  model: null, user: { mn: 'carol', ts: 1 },
}))
{
  const creds = await loadCredentials()
  eq(creds.loginType, 'user', 'v4 数据：登录类型读取正常')
  eq(creds.userData.mn, 'carol', 'v4 数据：用户名读取正常')
  ok(!creds.userData.ph, 'v4 数据：无 ph 字段视为空，不报错')
  // v4 数据上执行保存 → 升级 v6 且加密
  await saveCredentials('user', 'carol', '13500004444')
  const d = raw()
  eq(d.v, 6, 'v4 数据保存后升级 v6')
  ok(isEncrypted(d.user.ph), 'v4 数据保存后手机号加密')
}

// ---- v3 旧数据迁移（单份 → v6 双份） ----
console.log('\n== v3 迁移测试 ==')
localStorage.setItem(KEY, JSON.stringify({ v: 3, loginType: 'user', mn: 'dave', ts: 2 }))
{
  const creds = await loadCredentials()
  eq(creds.loginType, 'user', 'v3 迁移：登录类型 user')
  eq(creds.userData.mn, 'dave', 'v3 迁移：用户名保留')
  eq(creds.userData.ph, '', 'v3 迁移：手机号补空串')
  eq(raw().v, 6, 'v3 迁移后存储升级 v6')
}

// ---- v1/v2 无效数据 → 清除并返回 null ----
console.log('\n== v1/v2 无效数据测试 ==')
localStorage.setItem(KEY, JSON.stringify({ v: 2, ak: 'xxx' }))
{
  const creds = await loadCredentials()
  eq(creds, null, 'v1/v2 无效数据返回 null')
  eq(localStorage.getItem(KEY), null, 'v1/v2 无效数据被清除')
}

// ---- clearCredentials 全量清除（登出语义） ----
console.log('\n== 清除测试 ==')
await saveCredentials('user', 'eve', '13300005555')
clearCredentials()
eq(await loadCredentials(), null, '清除后读取为 null')
eq(localStorage.getItem(KEY), null, '清除后存储为空')

// ---- 损坏的 JSON → 静默返回 null（不抛异常） ----
console.log('\n== 异常容错测试 ==')
localStorage.setItem(KEY, '{bad-json')
{
  let creds = 'sentinel'
  try { creds = await loadCredentials() } catch { creds = 'threw' }
  eq(creds, null, '损坏 JSON 静默返回 null')
}

// ---- 损坏的密文 → 解密失败时视为空（不破坏整体读取） ----
console.log('\n== 密文损坏容错测试 ==')
localStorage.setItem(KEY, JSON.stringify({
  v: 6, v4: true, active: 'user',
  model: null, user: { mn: 'frank', ph: '{"iv":"xxx","data":"yyy"}', ts: 1 },
}))
{
  const creds = await loadCredentials()
  ok(creds !== null, '密文损坏时不返回 null（整体读取成功）')
  eq(creds.userData.mn, 'frank', '密文损坏时用户名仍正常读取')
  eq(creds.userData.ph, '', '密文损坏时手机号降级为空')
}

console.log(`\nauth.test.js 完成：${pass} 通过，${fail} 失败`)
if (fail > 0) process.exit(1)
