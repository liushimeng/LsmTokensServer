// 登录态与本地偏好存储
// v2 安全加固（20260825）：不再持久化 API Key（旧版 XOR+Base64 伪加密可被离线还原），
// "记住我"仅保存模型名称；加载时发现旧版记录（含 ak 字段）立即清除。
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

// 管理端登出：清 manager 会话 Cookie 后回管理端登录页
export async function managerLogout() {
  try { await fetch('ManagerLogoutInterface', { credentials: 'include' }) } catch { /* 忽略 */ }
  window.location.href = '/ManagerLogin'
}
