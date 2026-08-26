// 时间跨度动态档位（20260826）：与后端 /TimeSpanConfigInterface 配套的纯逻辑工具。
//
// 统一 span 编码（与后端 v2.0.40/2.0.41 约定一致）：
//   span == 0   无限制（旧「全部」，新 UI 不再提供）
//   span  > 0   最近 span 天
//   span  < 0   最近 (-span) 小时
//
// 档位生成：最小 1 小时，最大 transactionRetentionDays + 1 天（≤365 天），
// 共 10 档，从「漂亮值」阶梯等距抽取，首尾固定。确定性算法，可单测。
//
// 运行：node src/shared/timeSpan.test.js（无第三方测试框架）

// 小时级「漂亮值」阶梯（1h ~ 18h）
const HOUR_STEPS = [1, 2, 3, 4, 6, 8, 12, 18]
// 天级「漂亮值」阶梯（1 ~ 365 天，换算为小时）
const DAY_STEPS_HOURS = [1, 2, 3, 4, 5, 7, 10, 14, 21, 30, 45, 60, 75, 90, 120, 150, 180, 270, 365].map((d) => d * 24)

// 默认档位数（产品约定 10 档）
export const DEFAULT_LEVELS = 10
// 配置接口拉取失败时的兜底上限（后端统计天上限 365 天）
export const FALLBACK_MAX_SPAN_HOURS = 365 * 24

// spanToHours 统一 span 编码换算为小时数
export function spanToHours(span) {
  if (span > 0) return span * 24
  if (span < 0) return -span
  return 0
}

// hoursToSpan 把「小时数」编码回统一 span：<24h 用负值（小时），≥24h 优先整天数
export function hoursToSpan(hours) {
  if (hours % 24 === 0 && hours >= 24) return hours / 24
  return -hours
}

// buildSpanLevels 生成动态档位列表。
// maxSpanHours：档位上限（小时）；levels：档数（默认 10，最小 2）。
// 返回 [{ span, hours }]，严格递增；首档 1 小时，末档 maxSpanHours。
//
// 算法：把 [1h, ...候选漂亮值, maxH] 视作一条序列，在序列上等距取 n 个下标
// （round(i*(len-1)/(n-1))），保证档位在整个区间上均匀分布且都是「漂亮值」。
// 候选不足 n-2 个时按几何插值补齐，仍不足时等差兜底。
export function buildSpanLevels(maxSpanHours, levels = DEFAULT_LEVELS) {
  const n = Math.max(2, Math.min(20, Math.floor(levels)))
  const maxH = Math.max(2, Math.floor(maxSpanHours))
  const midCount = n - 2 // 首尾之外的中间档数

  // 候选阶梯：严格位于 (1h, maxH) 开区间内的漂亮值
  const candidates = [...HOUR_STEPS, ...DAY_STEPS_HOURS].filter((v) => v > 1 && v < maxH)

  let picks = []
  if (candidates.length >= midCount) {
    // 首尾纳入序列整体等距抽取
    const seqLen = candidates.length + 2
    for (let i = 1; i <= midCount; i++) {
      const idx = Math.round((i * (seqLen - 1)) / (n - 1)) - 1 // -1：跳过序列首位的 1h
      picks.push(candidates[Math.min(Math.max(idx, 0), candidates.length - 1)])
    }
  } else {
    picks.push(...candidates)
    // 候选不足：按几何插值补齐
    const ratio = Math.pow(maxH, 1 / (n - 1))
    for (let i = 1; i <= midCount && picks.length < midCount; i++) {
      const v = Math.round(Math.pow(ratio, i))
      if (v > 1 && v < maxH && !picks.includes(v)) picks.push(v)
    }
  }

  // 首尾 + 去重 + 排序；若不足 n 档则用等差补齐兜底
  let all = [...new Set([1, ...picks, maxH])].sort((a, b) => a - b)
  if (all.length < n) {
    for (let i = 1; i <= n - 2 && all.length < n; i++) {
      const v = Math.max(2, Math.round((i * maxH) / (n - 1)))
      if (!all.includes(v)) all.push(v)
    }
    all = [...new Set([1, ...all.filter((v) => v > 1 && v < maxH), maxH])].sort((a, b) => a - b)
  }

  return all.map((h) => ({ span: hoursToSpan(h), hours: h }))
}

// nearestSpan 旧值就近迁移：localStorage / URL 里存的历史档位不在新档位列表时，
// 找时间跨度最接近的档（0/全部 → 最大档）。
export function nearestSpan(levels, raw) {
  const n = parseInt(raw, 10)
  if (!levels || !levels.length) return n || 3
  if (levels.some((l) => l.span === n)) return n
  if (!Number.isFinite(n) || n === 0) return levels[levels.length - 1].span
  let best = levels[0]
  let bestDist = Infinity
  for (const l of levels) {
    const dist = Math.abs(l.hours - spanToHours(n))
    if (dist < bestDist) {
      bestDist = dist
      best = l
    }
  }
  return best.span
}

// spanLabelKey 档位文案：返回 i18n key + 插值（timeRange.* 命名空间，三语统一）
export function spanLabel(span, maxSpanHours) {
  const h = spanToHours(span)
  const isMax = maxSpanHours > 0 && h >= maxSpanHours
  if (h >= 24 && h % 24 === 0) {
    return { key: isMax ? 'timeRange.maxDays' : 'timeRange.lastNDays', vars: { n: h / 24 } }
  }
  return { key: 'timeRange.lastNHours', vars: { n: h } }
}

// ---- /TimeSpanConfigInterface 配置拉取（模块级缓存 60s，双端同源） ----

let cachedConfig = null
let inflight = null
const CACHE_TTL_MS = 60 * 1000

export function resetTimeSpanConfigCache() {
  cachedConfig = null
  inflight = null
}

// fetchTimeSpanConfig 拉取档位上限配置；失败时返回兜底值（365 天），不抛异常。
export async function fetchTimeSpanConfig(fetchFn) {
  if (cachedConfig && Date.now() - cachedConfig.fetchedAt < CACHE_TTL_MS) {
    return cachedConfig.data
  }
  if (inflight) return inflight
  const doFetch = fetchFn || defaultFetch
  inflight = doFetch()
    .then((data) => {
      const d = normalizeConfig(data)
      cachedConfig = { fetchedAt: Date.now(), data: d }
      return d
    })
    .catch(() => {
      // 兜底：接口不可用（服务重启等）时按 365 天生成档位，不阻塞页面
      const d = normalizeConfig(null)
      cachedConfig = { fetchedAt: Date.now(), data: d }
      return d
    })
    .finally(() => {
      inflight = null
    })
  return inflight
}

async function defaultFetch() {
  const res = await fetch(baseUrl() + 'TimeSpanConfigInterface', { credentials: 'include' })
  if (!res.ok) throw new Error('HTTP ' + res.status)
  return res.json()
}

function baseUrl() {
  const p = window.location.pathname
  return p.substring(0, p.lastIndexOf('/') + 1)
}

// normalizeConfig 校验响应并推导 maxSpanHours（与后端 timeSpanMaxDays 同规则）
export function normalizeConfig(data) {
  let maxDays = 365
  let retention = 0
  if (data && Number.isFinite(data.max_span_days) && data.max_span_days > 0) {
    maxDays = Math.min(365, Math.floor(data.max_span_days))
  }
  if (data && Number.isFinite(data.transaction_retention_days) && data.transaction_retention_days > 0) {
    retention = Math.floor(data.transaction_retention_days)
  }
  return { retentionDays: retention, maxSpanDays: maxDays, maxSpanHours: maxDays * 24, minSpanHours: 1, levels: DEFAULT_LEVELS }
}
