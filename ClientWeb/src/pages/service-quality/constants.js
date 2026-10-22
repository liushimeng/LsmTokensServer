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
  const diff = Date.now() - ms
  if (diff < 60_000) return t('serviceQuality.cards.justNow')
  const min = Math.floor(diff / 60_000)
  if (min < 60) return t('serviceQuality.cards.minutesAgo', { count: min })
  const hour = Math.floor(min / 60)
  if (hour < 24) return t('serviceQuality.cards.hoursAgo', { count: hour })
  return t('serviceQuality.cards.daysAgo', { count: Math.floor(hour / 24) })
}

// ---- 阶段CZ v2.0.85：模型质量对比 + 运行经验预测 ----

// COMPARE_METRICS 模型质量对比指标集（窗口 24h/7d × 指标，同一时刻一个轴）。
//   color: tier（四档状态色）| tier20（≤20% 正常色，以上中度色）| err5（≤5% 正常，
//   5~20% 轻度，以上严重）| neutral（中性蓝）
export const COMPARE_METRICS = [
  { key: 'avg_congestion', i18n: 'serviceQuality.metric.congestion', kind: 'ratio', coefficient: true, color: 'tier' },
  { key: 'stall_rate', i18n: 'serviceQuality.metric.stall', kind: 'pct', coefficient: true, color: 'tier20' },
  { key: 'error_rate', i18n: 'serviceQuality.metric.error', kind: 'pct', color: 'err5' },
  { key: 'ok_ms_per_token', i18n: 'serviceQuality.compare.metricMsPerToken', kind: 'num', color: 'neutral' },
  { key: 'ok_avg_elapsed_ms', i18n: 'serviceQuality.metric.avg', kind: 'ms', color: 'neutral' },
  { key: 'ok_p95_ms', i18n: 'serviceQuality.metric.p95', kind: 'ms', color: 'neutral' },
]

export function compareMetricOf(key) {
  return COMPARE_METRICS.find((m) => m.key === key) || COMPARE_METRICS[0]
}

// compareMetricColor 按指标语义着色（阈值色仅承载分档语义，数值标签始终伴随）。
export function compareMetricColor(metric, value) {
  const v = Number(value)
  if (!isFinite(v) || v <= 0) return STATUS_COLORS.idle
  switch (metric.color) {
    case 'tier':
      return (CONGESTION_TIERS.find((tr) => v < tr.max) || CONGESTION_TIERS[CONGESTION_TIERS.length - 1]).color
    case 'tier20':
      return v <= 20 ? STATUS_COLORS.ok : STATUS_COLORS.moderate
    case 'err5':
      if (v <= 5) return STATUS_COLORS.ok
      return v <= 20 ? STATUS_COLORS.light : STATUS_COLORS.severe
    default:
      return '#2a78d6'
  }
}

// fmtCompareValue 对比条形图数值格式化
export function fmtCompareValue(metric, value) {
  const v = Number(value)
  if (!isFinite(v) || v <= 0) return '-'
  if (metric.kind === 'ms') return fmtMs(v)
  if (metric.kind === 'pct') return v.toFixed(v >= 10 ? 0 : 1) + '%'
  if (metric.kind === 'num') return v.toFixed(2)
  return v.toFixed(2)
}

// fmtSignedPct 带符号百分比（+12% / -8%），用于今日 vs 周均值偏差
export function fmtSignedPct(v) {
  const n = Number(v) || 0
  return (n > 0 ? '+' : '') + n.toFixed(1) + '%'
}

// WEEKDAY_KEYS 预测面板星期标签（i18n 键，0=周日；显式数组以便 check:i18n 识别字面量键）
export const WEEKDAY_KEYS = [
  'serviceQuality.forecast.weekday.sun',
  'serviceQuality.forecast.weekday.mon',
  'serviceQuality.forecast.weekday.tue',
  'serviceQuality.forecast.weekday.wed',
  'serviceQuality.forecast.weekday.thu',
  'serviceQuality.forecast.weekday.fri',
  'serviceQuality.forecast.weekday.sat',
]

// ===================== v2.0.86 阶段DA：时段画像 / 时段带 / 峰谷 =====================

// TIME_BANDS 业务时段带（与后端 qosTimeBands 严格一致：左闭右开 [start, end)）。
//
// 关键口径：各带时长不同（凌晨 6h、午间 2h…），跨带比较必须用
//   - avg_calls_per_hour（每小时强度，跨带可比）
//   - share（调用量占比）
// 直接比 calls 会被"带越长数越大"误导。
export const TIME_BANDS = [
  { key: 'dawn', start: 0, end: 6 },
  { key: 'morning', start: 6, end: 9 },
  { key: 'forenoon', start: 9, end: 12 },
  { key: 'noon', start: 12, end: 14 },
  { key: 'afternoon', start: 14, end: 18 },
  { key: 'evening', start: 18, end: 24 },
]

// bandOfHour 小时 → 所属时段带 key；无归属返回 ''。
export function bandOfHour(hour) {
  for (const b of TIME_BANDS) {
    if (hour >= b.start && hour < b.end) return b.key
  }
  return ''
}

// HOUR_PROFILE_METRICS 24 小时时段画像指标集（同一时刻一个 Y 轴，禁止双轴）。
//   field：取值字段；kind：刻度与 tooltip 格式；tier：是否按拥堵四档着色
export const HOUR_PROFILE_METRICS = [
  { key: 'calls', i18n: 'serviceQuality.profile.metricCalls', field: 'avg_calls_per_day', kind: 'num' },
  { key: 'congestion', i18n: 'serviceQuality.metric.congestion', field: 'avg_congestion', kind: 'ratio', tier: true },
  { key: 'error', i18n: 'serviceQuality.metric.error', field: 'error_rate', kind: 'pct', tier: true },
]

export function hourProfileMetricOf(key) {
  return HOUR_PROFILE_METRICS.find((m) => m.key === key) || HOUR_PROFILE_METRICS[0]
}

// PERIOD_MATRIX_METRICS 「时段 × 模型」矩阵指标集。
//   tier=true → 按拥堵四档着色（语义色 + 数值标签）；
//   tier=false → 单色（调用量/时延是量级不是序数等级，不做 value-ramp）。
export const PERIOD_MATRIX_METRICS = [
  { key: 'congestion', i18n: 'serviceQuality.metric.congestion', field: 'avg_congestion', kind: 'ratio', tier: true },
  { key: 'calls', i18n: 'serviceQuality.period.count', field: 'count', kind: 'num', tier: false },
  { key: 'error', i18n: 'serviceQuality.metric.error', field: 'error_rate', kind: 'pct', tier: true },
  { key: 'avg', i18n: 'serviceQuality.metric.avg', field: 'ok_avg_elapsed_ms', kind: 'ms', tier: false },
]

export function periodMatrixMetricOf(key) {
  return PERIOD_MATRIX_METRICS.find((m) => m.key === key) || PERIOD_MATRIX_METRICS[0]
}

// fmtHour 时段标签 "14:00"；-1（无峰谷）返回 '-'。
export function fmtHour(h) {
  const v = Number(h)
  if (!isFinite(v) || v < 0) return '-'
  return String(v).padStart(2, '0') + ':00'
}

// fmtHourRange 时段带区间 "09:00–12:00"。
export function fmtHourRange(start, end) {
  const s = Number(start)
  const e = Number(end)
  if (!isFinite(s) || !isFinite(e)) return '-'
  return String(s).padStart(2, '0') + ':00–' + String(e).padStart(2, '0') + ':00'
}

// fmtInt0 整数（不带小数位），用于调用量这类大数值。
export function fmtInt0(v) {
  const n = Number(v)
  if (!isFinite(n)) return '-'
  return fmtNum(Math.round(n))
}

// fmtNum1 一位小数（用于日均调用这类可能带小数的量）。
export function fmtNum1(v) {
  const n = Number(v)
  if (!isFinite(n)) return '-'
  return n.toFixed(1)
}

// bandOf 找时段带统计对象（按 key）。
export function findBand(bands, key) {
  if (!Array.isArray(bands)) return null
  for (const b of bands) {
    if (b.key === key) return b
  }
  return null
}

// peakValleyOf 取峰谷对象（模型级或报告级，缺省返回空对象）。
export function peakValleyOf(obj) {
  return (obj && obj.peak_valley) || {}
}
