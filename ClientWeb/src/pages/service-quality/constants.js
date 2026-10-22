// service-quality 页共享常量：状态四档色 / 多模型分类色 / 错误分类色 / 分档阈值 / 指标定义。
// 配色经 dataviz palette 校验器验证（方案 §5.2）：
//   - 分类色固定 8 槽（超 8 折叠「其他」），颜色跟随模型实体（按名称稳定分配）
//   - 状态色仅承载状态语义，始终伴随文字标签，不与系列色混用
export const STATUS_COLORS = {
  ok: '#0ca30c',
  light: '#fab219',
  moderate: '#ec835a',
  severe: '#d03b3b',
  idle: '#94a3b8',
}

// 多模型折线分类色（固定顺序，CVD 相邻对 ΔE ≥ 8 验证通过）
export const SERIES_COLORS = [
  '#2a78d6', '#eb6834', '#1baf7a', '#eda100',
  '#e87ba4', '#008300', '#6250d6', '#e34948',
]

// 错误堆叠柱三分类（429 / 5xx / 其他），三色 CVD 验证通过
export const ERROR_COLORS = {
  e429: '#eda100',
  e5xx: '#e34948',
  other: '#6250d6',
}

// 拥堵分档阈值（与后端 qosStatusOf 一致：1.2~2 轻度，2~5 中度，>5 严重）
export const CONGESTION_TIERS = [
  { key: 'ok', max: 1.2, color: STATUS_COLORS.ok },
  { key: 'light', max: 2.0, color: STATUS_COLORS.light },
  { key: 'moderate', max: 5.0, color: STATUS_COLORS.moderate },
  { key: 'severe', max: Infinity, color: STATUS_COLORS.severe },
]

// 低样本阈值：与后端 qosMinCoefficientSamples 一致
export const MIN_COEFFICIENT_SAMPLES = 5

// METRICS 趋势图指标集（同一时刻只画一个 Y 轴 —— 禁止双轴）。
//   kind: ms | ratio | pct，决定刻度与 tooltip 格式
//   thresholds: 参考线（虚线仅承载阈值语义），color 取状态色并始终配文字标签
//   coefficient: true 表示指标依赖拥堵系数样本，低样本时段不取点（断线而非画 0）
export const METRICS = [
  { key: 'avg', i18n: 'serviceQuality.metric.avg', field: 'ok_avg_elapsed_ms', kind: 'ms', thresholds: [] },
  { key: 'p95', i18n: 'serviceQuality.metric.p95', field: 'ok_p95_ms', kind: 'ms', thresholds: [] },
  {
    key: 'congestion', i18n: 'serviceQuality.metric.congestion', field: 'avg_congestion', kind: 'ratio', coefficient: true,
    thresholds: [
      { value: 1.2, color: STATUS_COLORS.light, i18n: 'serviceQuality.status.light' },
      { value: 2.0, color: STATUS_COLORS.moderate, i18n: 'serviceQuality.status.moderate' },
      { value: 5.0, color: STATUS_COLORS.severe, i18n: 'serviceQuality.status.severe' },
    ],
  },
  {
    key: 'stall', i18n: 'serviceQuality.metric.stall', field: 'stall_rate', kind: 'pct', coefficient: true,
    thresholds: [{ value: 20, color: STATUS_COLORS.moderate, i18n: 'serviceQuality.metric.stallThreshold' }],
  },
  {
    key: 'error', i18n: 'serviceQuality.metric.error', field: 'error_rate', kind: 'pct',
    thresholds: [{ value: 5, color: STATUS_COLORS.severe, i18n: 'serviceQuality.metric.errorThreshold' }],
  },
]

export function metricOf(key) {
  return METRICS.find((m) => m.key === key) || METRICS[0]
}

// tierOfCongestion 拥堵系数 → 分档 key（<0 视为无数据）
export function tierOfCongestion(v) {
  if (v === null || v === undefined || v < 0) return 'idle'
  for (const tier of CONGESTION_TIERS) {
    if (v < tier.max) return tier.key
  }
  return 'severe'
}

// modelColor 模型名 → 稳定分类色：按名称字典序排名取槽位，
// 刷新后调用数变化导致列表重排时颜色不漂移（颜色跟随实体，不跟随排名）。
export function modelColor(names, name) {
  const sorted = [...names].sort()
  const idx = sorted.indexOf(name)
  return SERIES_COLORS[idx >= 0 ? idx % SERIES_COLORS.length : 0]
}

// fmtMs 毫秒人性化（1234 → "1.2s"；800 → "800ms"）
export function fmtMs(ms) {
  const v = Number(ms) || 0
  if (v >= 10000) return (v / 1000).toFixed(0) + 's'
  if (v >= 1000) return (v / 1000).toFixed(1) + 's'
  return v + 'ms'
}

// fmtPct 百分比展示（0 → "-"）
export function fmtPct(v) {
  const n = Number(v) || 0
  if (!n) return '-'
  return n >= 10 ? n.toFixed(0) + '%' : n.toFixed(1) + '%'
}

// fmtRatio 拥堵系数（两位小数）
export function fmtRatio(v) {
  const n = Number(v)
  if (!isFinite(n) || n <= 0) return '-'
  return n.toFixed(2)
}

// fmtMetric 按指标类型格式化
export function fmtMetric(v, metric) {
  if (v === null || v === undefined || !isFinite(Number(v))) return '-'
  if (metric.kind === 'ms') return fmtMs(v)
  if (metric.kind === 'pct') return `${Number(v).toFixed(Number(v) >= 10 ? 0 : 1)}%`
  return Number(v).toFixed(2)
}

// fmtNum 千分位
export function fmtNum(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

// metricValue 取某指标在某时间桶的数值：
//   - 空桶 → null（断开折线，不画 0）
//   - 系数类指标样本 < MIN_COEFFICIENT_SAMPLES → null（低样本不取点）
export function metricValue(point, metric) {
  if (!point || !point.count) return null
  if (metric.coefficient && (Number(point.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES) return null
  const v = Number(point[metric.field])
  if (!isFinite(v) || v < 0) return null
  return v
}

// windowSeries 按窗口取桶序列
export function windowSeries(model, win) {
  if (!model) return []
  if (win === '1h') return model.minutes || []
  if (win === '7d') return model.days || []
  return model.hours || []
}

// bucketShortLabel 桶键短标签：分钟桶 "HH:MM"、小时桶 "MM-DD HH"、天桶 "MM-DD"
export function bucketShortLabel(key) {
  if (!key) return ''
  const parts = key.split(' ')
  if (parts.length === 2) {
    if (parts[1].endsWith(':00') && parts[1].length === 5) return parts[0].slice(5) + ' ' + parts[1].slice(0, 2)
    return parts[1]
  }
  return key.slice(5)
}

// fmtAgo 相对时间（最后请求）：<1min "刚刚"、<60min "N 分钟前"、<24h "N 小时前"、否则 "N 天前"
export function fmtAgo(ms, t) {
  const v = Number(ms) || 0
  if (!v) return t('serviceQuality.cards.never')
  const diff = Date.now() - v
  if (diff < 60_000) return t('serviceQuality.cards.justNow')
  const min = Math.floor(diff / 60_000)
  if (min < 60) return t('serviceQuality.cards.minutesAgo', { count: min })
  const hour = Math.floor(min / 60)
  if (hour < 24) return t('serviceQuality.cards.hoursAgo', { count: hour })
  return t('serviceQuality.cards.daysAgo', { count: Math.floor(hour / 24) })
}
