// 前端加密工具（Web Crypto API + AES-GCM）
// 阶段BP（20260903）：为手机号敏感字段提供加密存储能力
//
// 安全说明：
// - 使用 Web Crypto API 的 AES-GCM 算法（现代浏览器原生支持）
// - 密钥由固定 passphrase 经 PBKDF2 派生（混淆级别）
// - 前端加密本质是混淆，无法抵抗逆向工程；目标是防 casual inspection
//   （DevTools 直接读取、他人借用浏览器等场景）
// - 每次加密使用随机 IV，确保同一明文加密结果不同

const PBKDF2_ITERATIONS = 10000
const SALT = new TextEncoder().encode('lsm-phone-20260903')
const PASSPHRASE = 'lsm-agent-creds-v6'

// 缓存派生后的密钥（避免每次加解密都重复 PBKDF2 计算）
let cachedKey = null

// 从固定 passphrase 派生 AES-GCM 密钥（256-bit）
async function getKey() {
  if (cachedKey) return cachedKey

  const encoder = new TextEncoder()
  const keyMaterial = await crypto.subtle.importKey(
    'raw',
    encoder.encode(PASSPHRASE),
    { name: 'PBKDF2' },
    false,
    ['deriveKey']
  )

  cachedKey = await crypto.subtle.deriveKey(
    {
      name: 'PBKDF2',
      salt: SALT,
      iterations: PBKDF2_ITERATIONS,
      hash: 'SHA-256',
    },
    keyMaterial,
    { name: 'AES-GCM', length: 256 },
    false,
    ['encrypt', 'decrypt']
  )
  return cachedKey
}

// 将 ArrayBuffer 转为 Base64URL 字符串
function arrayBufferToBase64Url(buffer) {
  const bytes = new Uint8Array(buffer)
  let binary = ''
  for (let i = 0; i < bytes.byteLength; i++) {
    binary += String.fromCharCode(bytes[i])
  }
  return btoa(binary).replace(/\+/g, '-').replace(/\//g, '_').replace(/=+$/, '')
}

// 将 Base64URL 字符串转为 Uint8Array
function base64UrlToArray(base64url) {
  const base64 = base64url.replace(/-/g, '+').replace(/_/g, '/')
  const padded = base64 + '='.repeat((4 - base64.length % 4) % 4)
  const binary = atob(padded)
  const bytes = new Uint8Array(binary.length)
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i)
  }
  return bytes
}

// 加密明文 → 返回 JSON 字符串 { iv: "Base64URL", data: "Base64URL" }
// @param {string} plaintext - 明文
// @returns {Promise<string>} - JSON 格式的密文
export async function encrypt(plaintext) {
  if (!plaintext) return ''

  const key = await getKey()
  // AES-GCM 标准 IV 长度为 12 字节
  const iv = crypto.getRandomValues(new Uint8Array(12))
  const encoder = new TextEncoder()
  const encoded = encoder.encode(plaintext)

  const ciphertext = await crypto.subtle.encrypt(
    { name: 'AES-GCM', iv },
    key,
    encoded
  )

  return JSON.stringify({
    iv: arrayBufferToBase64Url(iv),
    data: arrayBufferToBase64Url(ciphertext),
  })
}

// 解密密文 JSON → 返回明文
// @param {string} ciphertextJson - JSON 字符串 { iv: "Base64URL", data: "Base64URL" }
// @returns {Promise<string>} - 明文；解密失败时抛出异常
export async function decrypt(ciphertextJson) {
  if (!ciphertextJson) return ''

  try {
    const { iv, data } = JSON.parse(ciphertextJson)
    if (!iv || !data) throw new Error('Invalid ciphertext format')

    const key = await getKey()
    const ivBytes = base64UrlToArray(iv)
    const dataBytes = base64UrlToArray(data)

    const plaintextBuffer = await crypto.subtle.decrypt(
      { name: 'AES-GCM', iv: ivBytes },
      key,
      dataBytes
    )

    return new TextDecoder().decode(plaintextBuffer)
  } catch (e) {
    throw new Error('Decrypt failed: ' + (e.message || 'unknown'))
  }
}

// 判断字符串是否为加密密文格式（含 iv 和 data 字段的 JSON）
// @param {string} value - 待检测的 ph 字段值
// @returns {boolean}
export function isEncrypted(value) {
  if (!value || typeof value !== 'string') return false
  try {
    const parsed = JSON.parse(value)
    return parsed && typeof parsed === 'object' && 'iv' in parsed && 'data' in parsed
  } catch {
    return false
  }
}
