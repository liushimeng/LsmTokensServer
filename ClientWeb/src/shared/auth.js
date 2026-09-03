// 登录态与本地偏好存储
// 阶段BQ（2026-09-03）：v7 存储结构——model 条目新增 ak（加密 API Key），
// user 条目新增 pw（加密密码）；ph（加密手机号）延续阶段BP。
// 敏感字段全部 AES-GCM 加密（crypto.js），远强于 v1 时代 XOR+Base64 伪加密。
// 阶段BP（20260903）：v6 存储结构——user 条目 ph 字段加密存储。
// 阶段BO（20260902）：v5 存储结构——user 条目新增 ph（手机号）字段。
// 阶段AS（20260831）：双登录方式独立记忆——模型名登录与用户名登录各自保存，
// 切换 Tab 不覆盖另一方的已保存凭据。
// v3（20260831）：支持双登录方式，存储 loginType（model/user）和名称（模型名或用户名）
// v2 安全加固（20260825）：不再持久化 API Key（旧版 XOR+Base64 伪加密可被离线还原）
// 阶段BQ：显式 .js 后缀，兼容 node 直跑自检脚本（vite 构建同样支持）
import { baseUrl } from './api.js'
import { encrypt, decrypt, isEncrypted } from './crypto.js'

const STORAGE_KEY = 'lsm_agent_creds'

// 加密辅助：对字段加密（空值返回空串，保持已存密文时返回原值）
async function encField(value, keepEncrypted) {
  if (!value) return keepEncrypted || ''
  if (isEncrypted(value)) return value
  return await encrypt(value)
}

// 解密辅助：对字段解密（兼容密文/明文/空值）
async function decField(value) {
  if (!value) return ''
  try {
    return isEncrypted(value) ? await decrypt(value) : value
  } catch {
    return '' // 解密失败降级为空
  }
}

// saveCredentials 保存登录凭据到 localStorage（async：敏感字段需加密）
// @param {string} loginType - 登录类型：'model' 或 'user'
// @param {string} name - 模型名或用户名
// @param {Object} [extra] - 额外字段：model 传 {apiKey}，user 传 {password, phone}
export async function saveCredentials(loginType, name, extra = {}) {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    let data = stored ? JSON.parse(stored) : null
    // 阶段AS：同时保留两个 Tab 的凭据，各存各的，互不覆盖。
    if (!data || !data.v4) {
      // 迁移旧数据或初始化
      const legacy = data && data.v === 3 ? data : null
      data = {
        v: 7,
        model: legacy && legacy.loginType !== 'user' ? { mn: legacy.mn || '', ak: '', ts: legacy.ts || Date.now() } : null,
        user:  legacy && legacy.loginType === 'user' ? { mn: legacy.mn || '', pw: '', ph: '', ts: legacy.ts || Date.now() } : null,
        active: loginType,
      }
    }
    if (!data.v4) data.v4 = true
    delete data.v3
    // 阶段BQ：版本号升 v7（model 含 ak、user 含 pw/ph 加密字段），读取侧兼容 v6/v5/v4
    data.v = 7

    if (loginType === 'model') {
      const prevAk = (data.model && data.model.ak) || ''
      data.model = {
        mn: name || '',
        ak: await encField(extra.apiKey, prevAk),
        ts: Date.now(),
      }
      data.active = 'model'
    } else {
      // user Tab：密码必填（登录校验保证），手机号选填
      const prevPw = (data.user && data.user.pw) || ''
      const prevPh = (data.user && data.user.ph) || ''
      data.user = {
        mn: name || '',
        pw: await encField(extra.password, prevPw),
        ph: await encField(extra.phone, prevPh),
        ts: Date.now(),
      }
      data.active = 'user'
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(data))
  } catch { /* 忽略 */ }
}

// loadCredentials 从 localStorage 加载登录凭据（async：敏感字段需解密）
// @returns {Promise<null | { loginType, modelName, modelSaved, userSaved, modelData, userData }>}
// modelData = { mn, ak(已解密), ts }；userData = { mn, pw(已解密), ph(已解密), ts }
export async function loadCredentials() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return null
    const data = JSON.parse(stored)

    // 阶段BQ v7 / v6 / v5 / v4：双份存储，返回最近 active 的 Tab 对应的凭据
    if ((data.v === 4 || data.v === 5 || data.v === 6 || data.v === 7) && data.active) {
      const activeData = data.active === 'user' ? data.user : data.model
      const mn = activeData && activeData.mn ? activeData.mn : ''

      // 解密各 Tab 的敏感字段
      const modelAk = data.model && data.model.ak ? await decField(data.model.ak) : ''
      const userPw = data.user && data.user.pw ? await decField(data.user.pw) : ''
      const userPh = data.user && data.user.ph ? await decField(data.user.ph) : ''

      return {
        loginType: data.active === 'user' ? 'user' : 'model',
        modelName: mn,
        modelSaved: !!(data.model && data.model.mn),
        userSaved:  !!(data.user  && data.user.mn),
        modelData: data.model ? { mn: data.model.mn || '', ak: modelAk, ts: data.model.ts || 0 } : null,
        userData:  data.user  ? { mn: data.user.mn  || '', pw: userPw, ph: userPh, ts: data.user.ts || 0 } : null,
      }
    }

    // 旧版记录（v:1 含加密 ak / v:2 无 loginType / v:3 单份）→ 尝试迁移为 v7
    if (data.v === 3) {
      const loginType = data.loginType === 'user' ? 'user' : 'model'
      const mn = data.mn || ''
      try {
        const migrated = {
          v: 7,
          model: loginType === 'model' && mn ? { mn, ak: '', ts: data.ts || Date.now() } : null,
          user:  loginType === 'user'  && mn ? { mn, pw: '', ph: '', ts: data.ts || Date.now() } : null,
          active: loginType,
        }
        localStorage.setItem(STORAGE_KEY, JSON.stringify(migrated))
      } catch { /* 忽略 */ }
      if (!mn) { localStorage.removeItem(STORAGE_KEY); return null }
      return {
        loginType,
        modelName: mn,
        modelSaved: loginType === 'model' && !!mn,
        userSaved:  loginType === 'user'  && !!mn,
        modelData: loginType === 'model' ? { mn, ak: '', ts: data.ts || Date.now() } : null,
        userData:  loginType === 'user'  ? { mn, pw: '', ph: '', ts: data.ts || Date.now() } : null,
      }
    }

    // v1/v2：无有效凭据，清除
    localStorage.removeItem(STORAGE_KEY)
    return null
  } catch { return null }
}

// clearCredentials 清除登录凭据
// @param {string} [type] - 不传参=全量清除（登出语义）；'model'=仅清模型 Tab；'user'=仅清用户 Tab
export function clearCredentials(type) {
  try {
    if (!type) {
      localStorage.removeItem(STORAGE_KEY)
      return
    }
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return
    const data = JSON.parse(stored)
    if (type === 'model') {
      data.model = null
    } else if (type === 'user') {
      data.user = null
    }
    // 清除后若两侧都为空，全量移除
    if (!data.model && !data.user) {
      localStorage.removeItem(STORAGE_KEY)
    } else {
      localStorage.setItem(STORAGE_KEY, JSON.stringify(data))
    }
  } catch { /* 忽略 */ }
}

export async function logout() {
  clearCredentials()
  try { await fetch('UserLogoutInterface', { credentials: 'include' }) } catch { /* 忽略 */ }
  window.location.hash = '#/Login'
  window.location.reload()
}

// 阶段T 双构建隔离：角色由构建期常量 __APP_ROLE__（vite define 静态替换）决定，
// 不再嗅探端口；localStorage 'lsm.role' 仅作历史遗留兜底。
export const BUILD_ROLE = __APP_ROLE__

// 当前构建角色：管理端构建恒为 'manager'，用户端构建恒为 'user'
export function currentRole() {
  if (BUILD_ROLE === 'manager' || BUILD_ROLE === 'user') return BUILD_ROLE
  try { return localStorage.getItem('lsm.role') === 'manager' ? 'manager' : 'user' } catch { return 'user' }
}
export const isAdminRole = () => currentRole() === 'manager'

// 管理端登出：清 manager 会话 Cookie 后回管理端登录页
// 用户端构建经 DCE 裁剪，不携带 ManagerLogoutInterface / /ManagerLogin 字样
export async function managerLogout() {
  if (__APP_ROLE__ !== 'manager') { return logout() }
  try { await fetch('ManagerLogoutInterface', { credentials: 'include' }) } catch { /* 忽略 */ }
  window.location.href = baseUrl() + 'ManagerLogin'
}
