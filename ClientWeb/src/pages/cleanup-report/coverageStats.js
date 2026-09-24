// ClientWeb/src/pages/cleanup-report/coverageStats.js
//
// 阶段CR：/CleanupReport 页面「数据保留配置 / 数据清理覆盖 / 清理任务统计」
// 纯函数模块（无 React 依赖，node 直跑可自检 —— coverageStats.test.js）。
//
// 覆盖三类计算：
//   1. aggregateTables：分表元数据（SubTableInspectorInfo）→ 现存数据汇总
//   2. spanDaysBetween / retentionJudgment：现存跨度 vs 保留配置的健康判定
//   3. buildSubTableShares：按分表聚合的清理统计 → 占比条数据（参考 ModelInfo 模型显示）

// aggregateTables 聚合分表元数据为「现存数据」汇总。
// 入参 entries：GetSubTableInspector 的 JSON（table_name/exists/row_count/
// data_bytes/index_bytes/approximate/earliest_at/latest_at）。
// earliest_at / latest_at 为 "YYYY-MM-DD HH:MM:SS" 字符串（可字典序比较）。
// 只统计 exists=true 的表；均缺失时 exists=0、rows=0、时间串为空。
export function aggregateTables(entries) {
  const list = Array.isArray(entries) ? entries : []
  let exists = 0
  let rows = 0
  let dataBytes = 0
  let indexBytes = 0
  let approximate = false
  let earliest = ''
  let latest = ''
  for (const e of list) {
    if (!e || !e.exists) continue
    exists++
    rows += Number(e.row_count) || 0
    dataBytes += Number(e.data_bytes) || 0
    indexBytes += Number(e.index_bytes) || 0
    if (e.approximate) approximate = true
    const e1 = String(e.earliest_at || '')
    const l1 = String(e.latest_at || '')
    if (e1 && (!earliest || e1 < earliest)) earliest = e1
    if (l1 && (!latest || l1 > latest)) latest = l1
  }
  return { exists, rows, dataBytes, indexBytes, approximate, earliest, latest }
}

// spanDaysBetween 按时间串计算跨度天数（含首尾，最小 1；不可解析返回 0）。
export function spanDaysBetween(earliest, latest) {
  if (!earliest || !latest) return 0
  const a = new Date(String(earliest).replace(' ', 'T'))
  const b = new Date(String(latest).replace(' ', 'T'))
  if (isNaN(a.getTime()) || isNaN(b.getTime())) return 0
  return Math.max(1, Math.round((b.getTime() - a.getTime()) / 86400000))
}

// retentionJudgment 现存数据跨度是否符合保留配置。
// 入参 spanDays：现存跨度天数；retDays：配置保留天数（<=0 视为禁用清理）。
// 返回 { key, tone }：key 为 i18n 文案键，tone: on=正常 / warn=轻度积压 /
// off=清理异常 / muted=禁用或无数据（中性灰）。
export function retentionJudgment(spanDays, retDays) {
  if (!(retDays > 0)) return { key: 'cleanup.judgmentDisabled', tone: 'muted' }
  if (!(spanDays > 0)) return { key: 'cleanup.judgmentNoData', tone: 'muted' }
  if (spanDays > retDays + 7) return { key: 'cleanup.coverageAbnormal', tone: 'off' }
  if (spanDays > retDays + 1) return { key: 'cleanup.coverageBacklog', tone: 'warn' }
  return { key: 'cleanup.coverageNormal', tone: 'on' }
}

// buildSubTableShares 按分表聚合统计生成占比条数据（降序，附百分比）。
// 入参 subStats：/CleanupReportInterface 的 sub_table_stats；
// field：'deleted_rows' | 'deleted_tokens_all' 等数值字段名。
// 总量为 0 时全部 share=0（避免除零）。
export function buildSubTableShares(subStats, field) {
  const list = Array.isArray(subStats) ? subStats : []
  const items = list.map((s) => ({
    name: (s && s.sub_table_name) || '#' + ((s && s.sub_table_index) || 0),
    value: Number(s && s[field]) || 0,
  }))
  const total = items.reduce((acc, it) => acc + it.value, 0)
  return items
    .map((it) => ({ ...it, share: total > 0 ? (it.value / total) * 100 : 0 }))
    .sort((a, b) => b.value - a.value)
}
