// 统一请求封装：JSON API / 表单 POST / SSE / WebSocket
// 后端契约：成功返回 {success:true,...}，失败 {success:false,message:"..."}（HTTP 可能仍为 200）

export function baseUrl() {
  const p = window.location.pathname
  return p.substring(0, p.lastIndexOf('/') + 1)
}

// 默认请求超时上限（毫秒）：服务重启期间避免请求永久挂起（5 秒平衡用户体验与网络波动容忍）
// 调用方可通过 options.timeout 覆盖（如源站保存需要等待后端连通性测试，最长 60 秒）
const DEFAULT_TIMEOUT_MS = 5000

export async function request(path, options = {}) {
  const opts = { credentials: 'include', ...options }
  if (opts.body && typeof opts.body !== 'string' && !(opts.body instanceof FormData)) {
    opts.headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) }
    opts.body = JSON.stringify(opts.body)
  } else {
    opts.headers = { ...(opts.headers || {}) }
  }
  // 超时控制：服务端重启/网络异常时避免请求永久挂起（可由调用方 options.timeout 覆盖）
  const controller = new AbortController()
  const tid = setTimeout(() => controller.abort(), options.timeout || DEFAULT_TIMEOUT_MS)
  opts.signal = controller.signal
  let res
  try {
    res = await fetch(baseUrl() + path, opts)
  } catch (err) {
    clearTimeout(tid)
    if (err.name === 'AbortError') {
      throw new Error('请求超时，服务可能正在重启，请刷新页面重试')
    }
    throw new Error('网络错误，请检查网络连接或刷新页面重试')
  }
  clearTimeout(tid)
  let data = null
  try { data = await res.json() } catch { /* 非 JSON（如文件下载） */ }
  if (!res.ok) {
    // 登录态失效 → 按构建角色跳对应登录页（阶段T：角色由 __APP_ROLE__ 构建期决定）
    if (res.status === 401) {
      if (__APP_ROLE__ === 'manager') { window.location.href = baseUrl() + 'ManagerLogin'; }
      else { window.location.hash = '#/Login'; window.location.reload(); }
    }
    throw new Error((data && data.message) || `HTTP ${res.status}`)
  }
  if (data && data.success === false) {
    const err = new Error(data.message || '请求失败')
    err.data = data.data
    throw err
  }
  return data
}

export const get = (path, opts) => request(path, { method: 'GET', ...opts })
export const post = (path, body, opts) => request(path, { method: 'POST', body, ...opts })
export const postForm = (path, formData) =>
  request(path, { method: 'POST', body: formData })

// SSE 流式请求（如 /SpiderDataSourceCrawl），onEvent(dataObj)、onError(err)、onDone()
export function openSse(path, params, { onEvent, onError, onDone }) {
  const qs = new URLSearchParams(params || {}).toString()
  const es = new EventSource(baseUrl() + path + (qs ? '?' + qs : ''))
  es.onmessage = (ev) => {
    let obj = null
    try { obj = JSON.parse(ev.data) } catch { obj = { raw: ev.data } }
    if (obj && obj.type === 'done') { es.close(); onDone && onDone(obj); return }
    if (obj && obj.type === 'error') {
      es.close()
      onError && onError(new Error(obj.message || '服务端错误'))
      onDone && onDone(obj)
      return
    }
    onEvent && onEvent(obj, ev.data)
  }
  es.onerror = (e) => { es.close(); onError && onError(e) }
  return es
}

// WebSocket 流式请求（如 /ChatAnalysisTotalWS）
export function openWs(path, params, { onMessage, onError, onClose }) {
  const proto = window.location.protocol === 'https:' ? 'wss' : 'ws'
  const qs = new URLSearchParams(params || {}).toString()
  const ws = new WebSocket(`${proto}://${window.location.host}${baseUrl()}${path}${qs ? '?' + qs : ''}`)
  ws.onmessage = (ev) => {
    let obj = null
    try { obj = JSON.parse(ev.data) } catch { obj = { raw: ev.data } }
    onMessage && onMessage(obj, ev.data)
  }
  ws.onerror = (e) => onError && onError(e)
  ws.onclose = (e) => onClose && onClose(e)
  return ws
}

// 文件下载（如证书下载）
export function download(path, params) {
  const qs = new URLSearchParams(params || {}).toString()
  const a = document.createElement('a')
  a.href = baseUrl() + path + (qs ? '?' + qs : '')
  document.body.appendChild(a)
  a.click()
  a.remove()
}
