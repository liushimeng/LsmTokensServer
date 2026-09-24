// ClientWeb/src/pages/cleanup-report/coverageStats.test.js
//
// 阶段CR：CleanupReport 页面重构纯函数自检脚本（无第三方测试框架，node 直跑）。
// 覆盖：
//   1. aggregateTables 分表现存数据聚合（exists 过滤 / 求和 / 时间 min-max / approximate 标记）
//   2. spanDaysBetween 跨度天数计算
//   3. retentionJudgment 现存跨度 vs 保留配置健康判定
//   4. buildSubTableShares 分表占比条数据（降序 / 百分比 / 除零安全）
//   5. 本次新增 cleanup.* i18n key 三语齐全性
//
// 运行：
//   cd ClientWeb && node src/pages/cleanup-report/coverageStats.test.js

import fs from 'node:fs'
import path from 'node:path'
import url from 'node:url'
import { aggregateTables, spanDaysBetween, retentionJudgment, buildSubTableShares } from './coverageStats.js'

const __dirname = path.dirname(url.fileURLToPath(import.meta.url))
const localesDir = path.resolve(__dirname, '../../i18n/locales')
function loadJson(name) {
  return JSON.parse(fs.readFileSync(path.join(localesDir, name), 'utf8'))
}
const zhCN = loadJson('zh-CN.json')
const en = loadJson('en.json')
const ja = loadJson('ja.json')

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) {
    pass++
    // eslint-disable-next-line no-console
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    // eslint-disable-next-line no-console
    console.error(`  ✗ ${name}`)
  }
}

// ===== 1. aggregateTables =====
{
  const entries = [
    { table_name: 'TAgentHttpTransactionDataItem_00', exists: true, row_count: 100, data_bytes: 2048, index_bytes: 512, approximate: true, earliest_at: '2026-08-25 03:00:00', latest_at: '2026-09-24 09:00:00' },
    { table_name: 'TAgentHttpTransactionDataItem_01', exists: true, row_count: 50, data_bytes: 1024, index_bytes: 256, approximate: false, earliest_at: '2026-08-20 10:00:00', latest_at: '2026-09-25 01:00:00' },
    { table_name: 'TAgentHttpTransactionDataItem_02', exists: false, row_count: 999, data_bytes: 9, index_bytes: 9, earliest_at: '', latest_at: '' },
  ]
  const agg = aggregateTables(entries)
  ok(agg.exists === 2, `aggregateTables exists 过滤 (${agg.exists})`)
  ok(agg.rows === 150, `aggregateTables 行数求和 (${agg.rows})`)
  ok(agg.dataBytes === 3072 && agg.indexBytes === 768, `aggregateTables 字节求和 (${agg.dataBytes}/${agg.indexBytes})`)
  ok(agg.approximate === true, 'aggregateTables approximate 任一为 true')
  ok(agg.earliest === '2026-08-20 10:00:00', `aggregateTables earliest 取最小 (${agg.earliest})`)
  ok(agg.latest === '2026-09-25 01:00:00', `aggregateTables latest 取最大 (${agg.latest})`)
  const empty = aggregateTables(null)
  ok(empty.rows === 0 && empty.earliest === '' && empty.exists === 0, 'aggregateTables 非数组入参安全')
}

// ===== 2. spanDaysBetween =====
{
  ok(spanDaysBetween('2026-09-24 10:00:00', '2026-09-24 23:00:00') === 1, 'spanDaysBetween 同日最小 1 天')
  ok(spanDaysBetween('2026-08-25 00:00:00', '2026-09-24 00:00:00') === 30, 'spanDaysBetween 30 天')
  ok(spanDaysBetween('', '2026-09-24 00:00:00') === 0, 'spanDaysBetween 空串返回 0')
  ok(spanDaysBetween('bad', '2026-09-24 00:00:00') === 0, 'spanDaysBetween 不可解析返回 0')
}

// ===== 3. retentionJudgment =====
{
  ok(retentionJudgment(30, 30).key === 'cleanup.coverageNormal', 'judgment 跨度=配置 → 正常')
  ok(retentionJudgment(35, 30).key === 'cleanup.coverageBacklog', 'judgment 跨度=配置+5 → 轻度积压')
  ok(retentionJudgment(40, 30).key === 'cleanup.coverageAbnormal', 'judgment 跨度=配置+10 → 清理异常')
  ok(retentionJudgment(30, 0).key === 'cleanup.judgmentDisabled', 'judgment 配置 0 → 清理已禁用')
  ok(retentionJudgment(0, 30).key === 'cleanup.judgmentNoData', 'judgment 跨度 0 → 暂无数据')
  ok(retentionJudgment(35, 30).tone === 'warn' && retentionJudgment(40, 30).tone === 'off', 'judgment tone 分级')
}

// ===== 4. buildSubTableShares =====
{
  const subStats = [
    { sub_table_index: 0, sub_table_name: 'TAgentHttpTransactionDataItem_00', deleted_rows: 300, deleted_tokens_all: 900 },
    { sub_table_index: 1, sub_table_name: 'TAgentHttpTransactionDataItem_01', deleted_rows: 100, deleted_tokens_all: 100 },
  ]
  const rows = buildSubTableShares(subStats, 'deleted_rows')
  ok(rows.length === 2 && rows[0].name === 'TAgentHttpTransactionDataItem_00', 'shares 降序排列')
  ok(Math.abs(rows[0].share - 75) < 1e-9 && Math.abs(rows[1].share - 25) < 1e-9, 'shares 百分比正确 (75/25)')
  const tokens = buildSubTableShares(subStats, 'deleted_tokens_all')
  ok(Math.abs(tokens[0].share - 90) < 1e-9, 'shares 换字段复算 (90/10)')
  const zero = buildSubTableShares([{ sub_table_index: 0, sub_table_name: 'x', deleted_rows: 0 }], 'deleted_rows')
  ok(zero[0].share === 0, 'shares 总量为 0 除零安全')
  const none = buildSubTableShares(null, 'deleted_rows')
  ok(none.length === 0, 'shares 非数组入参安全')
}

// ===== 5. 本次新增 cleanup.* i18n key 三语齐全 =====
{
  const required = [
    'cleanup.retentionConfigTitle',
    'cleanup.retentionConfigDesc',
    'cleanup.retentionConfigDescDisabled',
    'cleanup.scheduleNext',
    'cleanup.scheduleDisabled',
    'cleanup.currentKeptLabel',
    'cleanup.coverageTitle',
    'cleanup.coverageDeletedLabel',
    'cleanup.coverageKeptLabel',
    'cleanup.coverageTokens',
    'cleanup.coveragePeriod',
    'cleanup.coverageCutoff',
    'cleanup.taskStatsTitle',
    'cleanup.taskStatsSubtitle',
    'cleanup.taskStatsTotalTasks',
    'cleanup.taskStatsStatusDist',
    'cleanup.taskStatsAvgDuration',
    'cleanup.taskStatsMaxDuration',
    'cleanup.taskStatsPeriod',
    'cleanup.taskStatsRowShare',
    'cleanup.taskStatsTokenShare',
    'cleanup.taskStatsTaskCount',
    'cleanup.taskStatsNoData',
    'cleanup.lastCleanupDateCol',
    'cleanup.judgmentNoData',
    'cleanup.judgmentDisabled',
  ]
  for (const key of required) {
    ok(zhCN[key] !== undefined && en[key] !== undefined && ja[key] !== undefined, `i18n ${key} 三语齐全`)
  }
}

console.log(`\n${fail === 0 ? '✅' : '❌'} coverageStats 自检：${pass} 通过 / ${fail} 失败`)
process.exit(fail === 0 ? 0 : 1)
