// 统一请求封装：JSON API / 表单 POST / SSE / WebSocket
// 后端契约：成功返回 {success:true,...}，失败 {success:false,message:"..."}（HTTP 可能仍为 200）
//
// 阶段BZ / v2.0.79：
//   - 默认超时由硬编码 5s 改为按 URL 路径动态分级（详见 shared/timeoutFor.js）；
//   - 长查询接口（AIRouteManageInterface / ModelInfoInterface / AgentInfoInterface /
//     ChatAnalysisTotalInterface / CleanupReportInterface）默认 305s，
//     配合后端 QueryContextMiddleware(300s) 的 ctx 透传，
//     解决生产 shard_00 133GB 下首屏必超时导致页面反复刷新的问题。
//   - 错误改为 ApiError + code 字段，调用方按 code 渲染 EmptyState / 重试按钮。
//   - 写操作保留原 post 接口；调用方如需自动重试请改用 shared/retry.js 的 retryGet（仅 GET）。

import { timeoutFor, DEFAULT_TIMEOUT_MS } from './timeoutFor'

export class ApiError extends Error {
  constructor(code, message, data) {
    super(message)
    this.name = 'ApiError'
    this.code = code
    this.data = data || null
  }
}

export function baseUrl() {
  const p = window.location.pathname
  return p.substring(0, p.lastIndexOf('/') + 1)
}

// 内部状态：最后一次 AbortError 的文案（避免循环依赖 i18n，由调用方决定如何展示）
const TIMEOUT_HINT_DEFAULT = '请求超时：服务端正在处理大量数据，请稍候或点击重试'
const NETWORK_HINT_DEFAULT = '网络错误：无法连接到 Web 服务（服务可能正在重启）'

export async function request(path, options = {}) {
  const opts = { credentials: 'include', ...options }
  if (opts.body && typeof opts.body !== 'string' && !(opts.body instanceof FormData)) {
    opts.headers = { 'Content-Type': 'application/json', ...(opts.headers || {}) }
    opts.body = JSON.stringify(opts.body)
  } else {
    opts.headers = { ...(opts.headers || {}) }
  }

  // 超时计算：调用方显式 timeout > URL 分类 > 全局默认
  let timeoutMs = opts.timeout
  if (timeoutMs === undefined) {
    timeoutMs = timeoutFor(path)
    if (timeoutMs < 0) timeoutMs = 0 // 0 = 不超时
  }
  if (timeoutMs === 0) {
    // 不超时模式：不挂 AbortController（除非外部已传 signal）
  } else {
    const controller = new AbortController()
    const tid = setTimeout(() => controller.abort(), timeoutMs)
    opts._lsmTimeoutTid = tid
    // 合并外层 signal（任一取消即取消）
    if (opts.signal) {
      const onOuterAbort = () => controller.abort()
      if (opts.signal.aborted) {
        controller.abort()
      } else {
        opts.signal.addEventListener('abort', onOuterAbort, { once: true })
      }
    }
    opts.signal = controller.signal
  }

  let res
  try {
    res = await fetch(baseUrl() + path, opts)
  } catch (err) {
    if (opts._lsmTimeoutTid) clearTimeout(opts._lsmTimeoutTid)
    if (err && err.name === 'AbortError') {
      throw new ApiError('timeout', TIMEOUT_HINT_DEFAULT, null)
    }
    if (err && /Failed to fetch/i.test(err.message || '')) {
      throw new ApiError('network', NETWORK_HINT_DEFAULT, null)
    }
    throw new ApiError('unknown', '网络错误，请检查网络连接或刷新页面重试', null)
  }
  if (opts._lsmTimeoutTid) clearTimeout(opts._lsmTimeoutTid)

  let data = null
  try { data = await res.json() } catch { /* 非 JSON（如文件下载） */ }
  if (!res.ok) {
    // 登录态失效 → 按构建角色跳对应登录页（阶段T：角色由 __APP_ROLE__ 构建期决定）
    if (res.status === 401) {
      if (__APP_ROLE__ === 'manager') { window.location.href = baseUrl() + 'ManagerLogin'; }
      else { window.location.hash = '#/Login'; window.location.reload(); }
    }
    const code = res.status >= 500 ? 'http_5xx' : 'http_4xx'
    throw new ApiError(code, (data && data.message) || `HTTP ${res.status}`, data)
  }
  if (data && data.success === false) {
    throw new ApiError('business', data.message || '请求失败', data.data)
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
      onError && onError(new ApiError('sse', obj.message || '服务端错误', obj))
      onDone && onDone(obj)
      return
    }
    onEvent && onEvent(obj, ev.data)
  }
  es.onerror = (e) => { es.close(); onError && onError(new ApiError('sse', 'SSE 连接异常', null)) }
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

export { DEFAULT_TIMEOUT_MS, timeoutFor }