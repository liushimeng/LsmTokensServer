// ClientWeb/src/shared/auth.test.js
//
// 阶段BQ（20260903）：登录凭据本地存储（lsm_agent_creds）自检脚本（无第三方测试框架）。
// 覆盖：v7 结构（ak/pw 加密）/ 双 Tab 独立 remember / 按类型清除 / v6/v5/v4/v3 兼容 / 加密工具。
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
globalThis.__APP_ROLE__ = 'user'

const { saveCredentials, loadCredentials, clearCredentials } = await import('./auth.js')
const { encrypt, decrypt, isEncrypted } = await import('./crypto.js')

const KEY = 'lsm_agent_creds'
const raw = () => JSON.parse(localStorage.getItem(KEY))

// ---- 阶段BP：加密/解密基础测试 ----
console.log('\n== 加密工具测试 ==')
{
  const plaintext = '13812345678'
  const ciphertext = await encrypt(plaintext)
  ok(typeof ciphertext === 'string', 'encrypt 返回字符串')
  ok(isEncrypted(ciphertext), '密文可被 isEncrypted 识别')
  ok(ciphertext !== plaintext, '密文不等于明文')
  eq(await decrypt(ciphertext), plaintext, 'decrypt 还原明文正确')
  const ciphertext2 = await encrypt(plaintext)
  ok(ciphertext !== ciphertext2, '同一明文两次加密结果不同（随机 IV）')
  eq(await decrypt(''), '', 'decrypt 空字符串返回空')
  ok(!isEncrypted('13812345678'), '明文手机号不被识别为密文')
}

// ---- 阶段BQ：v7 结构（ak/pw 加密）读写测试 ----
console.log('\n== v7 结构（ak/pw 加密）测试 ==')
localStorage.clear()
await saveCredentials('user', 'alice', { password: 'secret-pw', phone: '13812345678' })
{
  const d = raw()
  eq(d.v, 7, '保存后存储版本为 v7')
  eq(d.v4, true, 'v4 兼容标志保留')
  eq(d.active, 'user', 'active 为 user')
  eq(d.user.mn, 'alice', '用户名落库')
  ok(isEncrypted(d.user.pw), '密码为加密密文')
  ok(d.user.pw !== 'secret-pw', '密码不明文存储')
  ok(isEncrypted(d.user.ph), '手机号为加密密文')

  const creds = await loadCredentials()
  eq(creds.loginType, 'user', '读取：登录类型 user')
  eq(creds.userData.mn, 'alice', '读取：用户名回填')
  eq(creds.userData.pw, 'secret-pw', '读取：密码解密回填正确')
  eq(creds.userData.ph, '13812345678', '读取：手机号解密回填正确')
  eq(creds.userSaved, true, '读取：userSaved 为 true')
}

// 模型 Tab：保存模型名 + API Key
await saveCredentials('model', 'gpt-x', { apiKey: 'sk-abcdef123456' })
{
  const d = raw()
  eq(d.model.mn, 'gpt-x', '模型名落库')
  ok(isEncrypted(d.model.ak), 'API Key 为加密密文')
  ok(d.model.ak !== 'sk-abcdef123456', 'API Key 不明文存储')
  eq(d.active, 'model', 'active 切换为 model')
  // user 条目保留
  eq(d.user.mn, 'alice', 'user 条目用户名保留')
  ok(isEncrypted(d.user.pw), 'user 条目密码密文保留')

  const creds = await loadCredentials()
  eq(creds.loginType, 'model', '读取：最近 active 为 model')
  eq(creds.modelData.mn, 'gpt-x', '读取：模型名')
  eq(creds.modelData.ak, 'sk-abcdef123456', '读取：API Key 解密回填正确')
  eq(creds.userData.pw, 'secret-pw', '读取：user 密码仍保留')
}

// ---- 阶段BQ：按类型清除测试 ----
console.log('\n== 按类型清除测试 ==')
{
  // 清除 model 条目，user 保留
  clearCredentials('model')
  let d = raw()
  ok(!d.model, 'clearCredentials(model) 清除 model 条目')
  ok(d.user, 'user 条目保留')
  eq(d.user.mn, 'alice', 'user 条目用户名保留')
  // 清除 user 条目，两侧为空 → 全量移除
  clearCredentials('user')
  eq(localStorage.getItem(KEY), null, '两侧为空时全量移除 key')
  // 全量清除（logout 语义）
  await saveCredentials('model', 'm1', { apiKey: 'ak1' })
  clearCredentials()
  eq(localStorage.getItem(KEY), null, 'clearCredentials() 全量清除')
}

// ---- 阶段BQ：双 Tab 独立 remember 场景 ----
console.log('\n== 双 Tab 独立 remember 场景 ==')
localStorage.clear()
// 模型 Tab 勾选记住 → 保存 mn+ak
await saveCredentials('model', 'gpt-y', { apiKey: 'sk-model-key' })
// 用户 Tab 未勾选 → 仅清除 user 条目（模拟 submit 未勾选）
clearCredentials('user')
{
  const d = raw()
  eq(d.model.mn, 'gpt-y', '模型 Tab 凭据保留（不受 user 清除影响）')
  ok(isEncrypted(d.model.ak), '模型 Tab API Key 保留')
  ok(!d.user, 'user 条目已清除')
}

// ---- 阶段BP：v6 明文兼容（升级 v7 加密） ----
console.log('\n== v6 明文兼容测试 ==')
localStorage.setItem(KEY, JSON.stringify({
  v: 6, v4: true, active: 'user',
  model: null, user: { mn: 'bob', ph: '13600002222', ts: 1 },
}))
{
  const creds = await loadCredentials()
  eq(creds.userData.mn, 'bob', 'v6 明文：用户名读取正常')
  eq(creds.userData.ph, '13600002222', 'v6 明文：手机号明文直接返回')
  // v6 保存 → 升级 v7 且加密
  await saveCredentials('user', 'bob', { password: 'bob-pw', phone: '13600003333' })
  const d = raw()
  eq(d.v, 7, 'v6 保存后升级 v7')
  ok(isEncrypted(d.user.pw), 'v6 保存后密码加密')
  ok(isEncrypted(d.user.ph), 'v6 保存后手机号加密')
  const creds2 = await loadCredentials()
  eq(creds2.userData.pw, 'bob-pw', 'v6→v7 升级后密码解密正确')
  eq(creds2.userData.ph, '13600003333', 'v6→v7 升级后手机号解密正确')
}

// ---- 阶段AS v4 旧数据兼容 ----
console.log('\n== v4 兼容测试 ==')
localStorage.setItem(KEY, JSON.stringify({
  v: 4, v4: true, active: 'user',
  model: null, user: { mn: 'carol', ts: 1 },
}))
{
  const creds = await loadCredentials()
  eq(creds.loginType, 'user', 'v4：登录类型读取正常')
  eq(creds.userData.mn, 'carol', 'v4：用户名读取正常')
  ok(!creds.userData.pw, 'v4：无 pw 字段视为空')
  ok(!creds.userData.ph, 'v4：无 ph 字段视为空')
  await saveCredentials('user', 'carol', { password: 'cpw', phone: '13500004444' })
  eq(raw().v, 7, 'v4 保存后升级 v7')
  ok(isEncrypted(raw().user.pw), 'v4 保存后密码加密')
}

// ---- v3 旧数据迁移 ----
console.log('\n== v3 迁移测试 ==')
localStorage.setItem(KEY, JSON.stringify({ v: 3, loginType: 'user', mn: 'dave', ts: 2 }))
{
  const creds = await loadCredentials()
  eq(creds.loginType, 'user', 'v3 迁移：登录类型 user')
  eq(creds.userData.mn, 'dave', 'v3 迁移：用户名保留')
  eq(creds.userData.pw, '', 'v3 迁移：密码补空串')
  eq(raw().v, 7, 'v3 迁移后存储升级 v7')
}

// ---- v1/v2 无效数据 ----
console.log('\n== v1/v2 无效数据测试 ==')
localStorage.setItem(KEY, JSON.stringify({ v: 2, ak: 'xxx' }))
{
  const creds = await loadCredentials()
  eq(creds, null, 'v1/v2 无效数据返回 null')
  eq(localStorage.getItem(KEY), null, 'v1/v2 无效数据被清除')
}

// ---- 清除与异常容错 ----
console.log('\n== 清除与异常容错测试 ==')
await saveCredentials('user', 'eve', { password: 'epw', phone: '13300005555' })
clearCredentials()
eq(await loadCredentials(), null, '清除后读取为 null')

localStorage.setItem(KEY, '{bad-json')
{
  let creds = 'sentinel'
  try { creds = await loadCredentials() } catch { creds = 'threw' }
  eq(creds, null, '损坏 JSON 静默返回 null')
}

// 密文损坏 → 解密失败时视为空（不破坏整体读取）
localStorage.setItem(KEY, JSON.stringify({
  v: 7, v4: true, active: 'user',
  model: null, user: { mn: 'frank', pw: '{"iv":"xxx","data":"yyy"}', ph: '13400006666', ts: 1 },
}))
{
  const creds = await loadCredentials()
  ok(creds !== null, '密文损坏时不返回 null（整体读取成功）')
  eq(creds.userData.mn, 'frank', '密文损坏时用户名仍正常读取')
  eq(creds.userData.pw, '', '密文损坏时密码降级为空')
  eq(creds.userData.ph, '13400006666', '密文损坏时明文手机号仍正常读取')
}

console.log(`\nauth.test.js 完成：${pass} 通过，${fail} 失败`)
if (fail > 0) process.exit(1)
