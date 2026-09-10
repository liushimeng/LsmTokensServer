// shared/retry.js —— 指数退避重试工具（仅 GET 自动重试）
//
// 用途：
//   - 长查询接口（AIRouteManageInterface、ModelInfoInterface、AgentInfoInterface、
//     ChatAnalysisTotalInterface、CleanupReportInterface）首次 5s 超时后，
//     自动 retry 1~2 次（指数退避 800ms / 1600ms），避免「请求超时，请刷新页面重试」
//     落到 main.jsx 全局监听再次触发 reload。
//   - 写操作（add / update / delete / batch_*）一律禁止自动重试，避免重复插入。
//
// 注意：
//   - 内部仍走 request(path, opts)，opts 内置 AbortController；retry 复用同一个
//     外部 signal（如有），调用方传入的 controller 可一键终止整轮重试。
//   - 抛 ApiError（含 .code 字段），由上层组件按 code 渲染 EmptyState / Retry 按钮。

import { request } from './api'

const sleep = (ms) => new Promise((r) => setTimeout(r, ms))

// 默认重试策略：最多 3 次（首请求 + 2 次重试），间隔 800ms/1600ms。
const DEFAULT_POLICY = { maxRetries: 2, baseMs: 800, factor: 2, jitterMs: 200 }

/**
 * GET 重试封装。
 * @param {string} path 接口路径
 * @param {object} options request options（含 timeout / signal）
 * @param {object} [policy] { maxRetries, baseMs, factor, jitterMs }
 * @returns {Promise<any>} 解码后的 data
 */
export async function retryGet(path, options = {}, policy = {}) {
  const p = { ...DEFAULT_POLICY, ...policy }
  let attempt = 0
  let lastErr
  // 外层 signal 若已 abort，立即抛错（不重试）
  if (options.signal && options.signal.aborted) {
    throw new ApiError('aborted', '请求已取消', null)
  }
  while (attempt <= p.maxRetries) {
    try {
      // 每次重试都重新计算 timeout（避免长 timeout 累加到首请求就被截断）
      return await request(path, { ...options, __retryAttempt: attempt })
    } catch (e) {
      // 4xx / 已取消 / 业务错误：直接抛出，不重试
      if (e instanceof ApiError && (e.code === 'http_4xx' || e.code === 'aborted' || e.code === 'business')) {
        throw e
      }
      lastErr = e
      if (attempt >= p.maxRetries) break
      const delay = p.baseMs * Math.pow(p.factor, attempt) + Math.random() * p.jitterMs
      // sleep 期间也要尊重外层 signal
      await sleepWithSignal(delay, options.signal)
      attempt++
    }
  }
  throw lastErr
}

function sleepWithSignal(ms, signal) {
  return new Promise((resolve, reject) => {
    const t = setTimeout(resolve, ms)
    if (signal) {
      const onAbort = () => {
        clearTimeout(t)
        reject(new ApiError('aborted', '请求已取消', null))
      }
      if (signal.aborted) {
        clearTimeout(t)
        reject(new ApiError('aborted', '请求已取消', null))
        return
      }
      signal.addEventListener('abort', onAbort, { once: true })
    }
  })
}

// ApiError 透出 code 字段供上层组件按 code 渲染不同 UI
// 注：上方 class 已 export；不再重复 export，避免 Vite/rolldown ESM
// 「Duplicated export 'ApiError'」错误。
export class ApiError extends Error {
  constructor(code, message, data) {
    super(message)
    this.name = 'ApiError'
    this.code = code // 'timeout' | 'network' | 'http_4xx' | 'http_5xx' | 'aborted' | 'business' | 'unknown'
    this.data = data || null
  }
}