// 统一请求封装：JSON API / 表单 POST / SSE / WebSocket
// 后端契约：成功返回 {success:true,...}，失败 {success:false,message:"..."}（HTTP 可能仍为 200）

export function baseUrl() {
  const p = window.location.pathname
  return p.substring(0, p.lastIndexOf('/') + 1)
}

export async function request(path, options = {}) {
  const opts = { credentials: 'include', ...options }
  if (opts.body && typeof opts.body !== 'string' && !(opts.body instanceof FormData)) {
    opts.headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) }
    opts.body = JSON.stringify(opts.body)
  } else {
    opts.headers = { ...(opts.headers || {}) }
  }
  const res = await fetch(baseUrl() + path, opts)
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
  if (data && data.success === false) throw new Error(data.message || '请求失败')
  return data
}

export const get = (path) => request(path, { method: 'GET' })
export const post = (path, body) => request(path, { method: 'POST', body })
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
