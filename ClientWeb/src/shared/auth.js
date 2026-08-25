// 登录态与本地偏好存储
// v2 安全加固（20260825）：不再持久化 API Key（旧版 XOR+Base64 伪加密可被离线还原），
// "记住我"仅保存模型名称；加载时发现旧版记录（含 ak 字段）立即清除。
import { baseUrl } from './api'

const STORAGE_KEY = 'lsm_agent_creds'

export function saveCredentials(modelName) {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify({ v: 2, mn: modelName, ts: Date.now() }))
  } catch { /* 忽略 */ }
}

export function loadCredentials() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return null
    const data = JSON.parse(stored)
    // 旧版记录（v:1，含加密 ak）→ 直接清除，仅返回可用的模型名
    if (data.v !== 2 || typeof data.ak !== 'undefined') {
      localStorage.removeItem(STORAGE_KEY)
      return null
    }
    return { modelName: data.mn || '' }
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
