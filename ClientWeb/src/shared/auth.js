// 登录态与本地偏好存储
// v3（20260831）：支持双登录方式，存储 loginType（model/user）和名称（模型名或用户名）
// v2 安全加固（20260825）：不再持久化 API Key（旧版 XOR+Base64 伪加密可被离线还原）
import { baseUrl } from './api'

const STORAGE_KEY = 'lsm_agent_creds'

// saveCredentials 保存登录凭据到 localStorage
// @param {string} loginType - 登录类型：'model' 或 'user'
// @param {string} name - 模型名或用户名
export function saveCredentials(loginType, name) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ v: 3, loginType, mn: name, ts: Date.now() }))
  } catch { /* 忽略 */ }
}

// loadCredentials 从 localStorage 加载登录凭据
// @returns {null | { loginType: string, modelName: string }}
export function loadCredentials() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return null
    const data = JSON.parse(stored)
    // v3 版本：包含 loginType 和 mn
    if (data.v === 3) {
      return { loginType: data.loginType === 'user' ? 'user' : 'model', modelName: data.mn || '' }
    }
    // 旧版记录（v:1 含加密 ak / v:2 无 loginType）→ 直接清除
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
