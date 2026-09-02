// 登录态与本地偏好存储
// 阶段BO（20260902）：v5 存储结构——用户名登录条目新增 ph（手机号）字段，
// 登录成功后自动保存手机号，登录页自动读取回填；兼容 v4（无 ph）与 v3 迁移。
// 阶段AS（20260831）：双登录方式独立记忆——模型名登录与用户名登录各自保存，
// 切换 Tab 不覆盖另一方的已保存凭据。
// v3（20260831）：支持双登录方式，存储 loginType（model/user）和名称（模型名或用户名）
// v2 安全加固（20260825）：不再持久化 API Key（旧版 XOR+Base64 伪加密可被离线还原）
// 阶段BO：显式 .js 后缀，兼容 node 直跑自检脚本（vite 构建同样支持）
import { baseUrl } from './api.js'

const STORAGE_KEY = 'lsm_agent_creds'

// saveCredentials 保存登录凭据到 localStorage
// @param {string} loginType - 登录类型：'model' 或 'user'
// @param {string} name - 模型名或用户名
// @param {string} [phone] - 手机号（仅 user 登录有效；阶段BO）
export function saveCredentials(loginType, name, phone) {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    let data = stored ? JSON.parse(stored) : null
    // 阶段AS：同时保留两个 Tab 的凭据，各存各的，互不覆盖。
    if (!data || !data.v4) {
      // 迁移旧数据或初始化：旧 v3 格式只存一份 → 迁移到 v5 双份
      const legacy = data && data.v === 3 ? data : null
      data = {
        v: 5,
        model: legacy && legacy.loginType !== 'user' ? { mn: legacy.mn || '', ts: legacy.ts || Date.now() } : null,
        user:  legacy && legacy.loginType === 'user' ? { mn: legacy.mn || '', ph: '', ts: legacy.ts || Date.now() } : null,
        active: loginType, // 最近一次使用的登录类型
      }
    }
    if (!data.v4) data.v4 = true
    // 去掉旧 v3 字段，避免歧义
    delete data.v3
    // 阶段BO：版本号升 v5（user 条目含 ph 手机号字段），读取侧兼容 v4
    data.v = 5

    if (loginType === 'model') {
      data.model = { mn: name || '', ts: Date.now() }
      data.active = 'model'
    } else {
      // 阶段BO：空手机号保留策略——本次未填 phone 时保留上次已存的 ph，避免误清
      const prevPh = (data.user && data.user.ph) || ''
      data.user = { mn: name || '', ph: phone || prevPh, ts: Date.now() }
      data.active = 'user'
    }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(data))
  } catch { /* 忽略 */ }
}

// loadCredentials 从 localStorage 加载登录凭据
// @returns {null | { loginType: string, modelName: string, modelSaved: boolean, userSaved: boolean }}
export function loadCredentials() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return null
    const data = JSON.parse(stored)

    // 阶段AS v4 / 阶段BO v5：双份存储，返回最近 active 的 Tab 对应的凭据 + 两侧是否有保存
    // （v5 的 user 条目额外含 ph 手机号字段，随 userData 原样返回）
    if ((data.v === 4 || data.v === 5) && data.active) {
      const activeData = data.active === 'user' ? data.user : data.model
      const mn = activeData && activeData.mn ? activeData.mn : ''
      return {
        loginType: data.active === 'user' ? 'user' : 'model',
        modelName: mn,
        modelSaved: !!(data.model && data.model.mn),
        userSaved:  !!(data.user  && data.user.mn),
        modelData: data.model || null,
        userData:  data.user  || null,
      }
    }

    // 旧版记录（v:1 含加密 ak / v:2 无 loginType / v:3 单份）→ 尝试迁移为 v5
    if (data.v === 3) {
      const loginType = data.loginType === 'user' ? 'user' : 'model'
      const mn = data.mn || ''
      // 直接迁移为 v5 格式
      try {
        const migrated = {
          v: 5,
          model: loginType === 'model' && mn ? { mn, ts: data.ts || Date.now() } : null,
          user:  loginType === 'user'  && mn ? { mn, ph: '', ts: data.ts || Date.now() } : null,
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
        modelData: loginType === 'model' ? { mn, ts: data.ts || Date.now() } : null,
        userData:  loginType === 'user'  ? { mn, ph: '', ts: data.ts || Date.now() } : null,
      }
    }

    // v1/v2：无有效凭据，清除
    localStorage.removeItem(STORAGE_KEY)
    return null
  } catch { return null }
}

export function clearCredentials() {
  try { localStorage.removeItem(STORAGE_KEY) } catch { /* 忽略 */ }
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
