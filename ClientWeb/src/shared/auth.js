// 登录态与本地凭据存储（与旧版 server_web_user_login.go 算法保持一致）
const STORAGE_KEY = 'lsm_agent_creds'

function simpleEncrypt(text, salt) {
  if (!text) return ''
  let result = ''
  for (let i = 0; i < text.length; i++) {
    result += String.fromCharCode(text.charCodeAt(i) ^ salt.charCodeAt(i % salt.length))
  }
  return btoa(encodeURIComponent(result))
}

function simpleDecrypt(encoded, salt) {
  if (!encoded) return ''
  try {
    const text = decodeURIComponent(atob(encoded))
    let result = ''
    for (let i = 0; i < text.length; i++) {
      result += String.fromCharCode(text.charCodeAt(i) ^ salt.charCodeAt(i % salt.length))
    }
    return result
  } catch {
    return ''
  }
}

function generateSalt() {
  const hostname = window.location.hostname || 'lsm_agent'
  const userAgent = navigator.userAgent || ''
  return 'lsm_' + hostname.replace(/[^a-zA-Z0-9]/g, '_') + '_' + userAgent.length
}

export function saveCredentials(modelName, apiKey) {
  try {
    const salt = generateSalt()
    const data = { v: 1, mn: simpleEncrypt(modelName, salt + '_mn'), ak: simpleEncrypt(apiKey, salt + '_ak'), ts: Date.now() }
    localStorage.setItem(STORAGE_KEY, JSON.stringify(data))
  } catch { /* 忽略 */ }
}

export function loadCredentials() {
  try {
    const stored = localStorage.getItem(STORAGE_KEY)
    if (!stored) return null
    const data = JSON.parse(stored)
    if (data.v !== 1) { localStorage.removeItem(STORAGE_KEY); return null }
    const salt = generateSalt()
    return {
      modelName: simpleDecrypt(data.mn, salt + '_mn'),
      apiKey: simpleDecrypt(data.ak, salt + '_ak'),
    }
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
