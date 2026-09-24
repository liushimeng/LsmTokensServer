import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'
import TimeRangeSelector from '../components/TimeRangeSelector'
import PageHeader from '../components/PageHeader'
import { useTimeSpanLevels } from '../shared/useTimeSpanLevels'
import { nearestSpan } from '../shared/timeSpan'
import { useI18n } from '../i18n'
import { aggregateTables, spanDaysBetween, retentionJudgment, buildSubTableShares } from './cleanup-report/coverageStats'

// 过期数据清理报告：CleanupReportInterface（POST JSON）
// action: list {page,page_size,days} / state / tables [table=精确计数]
// 20260826：时间跨度为动态档位（1 小时 ~ transactionRetentionDays+1 天，统一 span 编码）
// 阶段CR：KPI 卡「数据保留配置 / 数据清理覆盖」重设计去重 + 新增「清理任务统计」模块
// （stats/sub_table_stats：TAgentHttpTransactionCleanupReport 项统计，参考模型显示占比条）

const PAGE_SIZE = 20

function fmt(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}
function fmtBytes(b) {
  b = Number(b) || 0
  if (b < 1024) return b + ' B'
  if (b < 1024 * 1024) return (b / 1024).toFixed(2) + ' KB'
  if (b < 1024 * 1024 * 1024) return (b / 1024 / 1024).toFixed(2) + ' MB'
  return (b / 1024 / 1024 / 1024).toFixed(2) + ' GB'
}
function fmtTime(v) {
  if (!v) return '-'
  const raw = String(v)
  let d = new Date(raw)
  if (isNaN(d.getTime()) && raw.indexOf(' ') > 0) d = new Date(raw.replace(' ', 'T'))
  if (isNaN(d.getTime())) return '-'
  const p = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export default function CleanupReport() {
  const { t } = useI18n()

  function statusTag(status, errMsg) {
    const s = String(status || 'unknown')
    if (s === 'success') return <span style={{ background: '#d1fae5', color: '#065f46', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>{t('common.success')}</span>
    if (s === 'partial') return (
      <span style={{ background: '#fef3c7', color: '#92400e', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>{t('cleanup.partial')}</span>
    )
    if (s === 'failed') return (
      <span>
        <span style={{ background: '#fee2e2', color: '#991b1b', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>{t('cleanup.failed')}</span>
        {String(errMsg || '').indexOf(t('cleanup.autoRetrying')) >= 0 ? <span style={{ marginLeft: 4, fontSize: 12, color: '#6b7280' }}>{t('cleanup.autoRetrying')}</span> : null}
      </span>
    )
    return <span>{s}</span>
  }

  const { levels, loading: levelsLoading } = useTimeSpanLevels()
  const [days, setDays] = useState(null) // 档位加载后按旧 localStorage 值就近迁移
  const [page, setPage] = useState(1)
  const [reports, setReports] = useState([])
  const [total, setTotal] = useState(0)
  const [summary, setSummary] = useState({})
  const [daily, setDaily] = useState([])
  const [stats, setStats] = useState(null) // v2.0.79 阶段CR: 清理报告项统计
  const [subStats, setSubStats] = useState([]) // v2.0.79 阶段CR: 按分表聚合统计
  const [selDay, setSelDay] = useState(null) // 趋势图选中日期（触屏点击替代 hover title）
  const [state, setState] = useState(null)
  const [tables, setTables] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const loadData = useCallback((p, d) => {
    setLoading(true)
    setError('')
    post('CleanupReportInterface', { action: 'list', page: p, page_size: PAGE_SIZE, days: d })
      .then((res) => {
        setReports(res.reports || [])
        setTotal(res.total || 0)
        setSummary(res.total_summary || {})
        setDaily(res.daily_summaries || [])
        setStats(res.stats || null)
        setSubStats(res.sub_table_stats || [])
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  const loadState = useCallback(() => {
    post('CleanupReportInterface', { action: 'state' })
      .then((d) => setState((d && d.state) || (d && d.data && d.data.state) || null))
      .catch(() => {})
  }, [])

  const loadTables = useCallback(() => {
    post('CleanupReportInterface', { action: 'tables' })
      .then((d) => setTables((d && (d.tables || d.data)) || []))
      .catch(() => setTables([]))
  }, [])

  useEffect(() => {
    if (!levels.length || days !== null) return
    setDays(nearestSpan(levels, localStorage.getItem('lsm:cleanupReport:days:v1') || 30))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [levels])

  useEffect(() => {
    if (days === null) return
    localStorage.setItem('lsm:cleanupReport:days:v1', String(days))
    loadData(1, days)
    loadState()
    loadTables()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days])

  // 精确计数（单表 COUNT(*)，超时回退近似值）
  const exactCount = async (table) => {
    try {
      const d = await post('CleanupReportInterface', { action: 'tables', table })
      if (d.exact_table === table) loadTables()
      else alert(t('cleanup.exactCountFailed') + (d.message || t('cleanup.tableTooLarge')))
    } catch (e) { alert(t('cleanup.exactCountFailed') + e.message) }
  }


  const totalPages = Math.ceil(total / PAGE_SIZE) || 1
  const retention = state && typeof state.retention_days === 'number'
    ? (state.retention_days <= 0 ? t('cleanup.retentionDaysDisabled') : t('cleanup.retentionDaysValue', { days: state.retention_days }))
    : '-'
  const dailyMax = Math.max(1, ...daily.map((s) => s.deleted_rows || 0))

  // v2.0.79 阶段CR: 现存数据（TAgentHttpTransactionDataItem 分表现存汇总）
  // 与保留配置健康判定。注意不再用 state.earliest_transaction_at（仅在每日清理
  // 执行时刷新，禁用清理时恒为空）；分表元数据的 MIN/MAX(created_at) 更可靠。
  const kept = aggregateTables(tables)
  const keptSpan = spanDaysBetween(kept.earliest, kept.latest)
  const retDays = state && typeof state.retention_days === 'number' ? state.retention_days : 0
  const judgment = retentionJudgment(keptSpan, retDays)
  const judgmentColors = { on: '#059669', warn: '#d97706', off: '#dc2626', muted: '#94a3b8' }
  const rowShares = buildSubTableShares(subStats, 'deleted_rows')
  const tokenShares = buildSubTableShares(subStats, 'deleted_tokens_all')

  // shareBars 占比条（参考 ModelInfo 页「模型 Token 用量」实现）
  const shareBars = (items) => {
    if (!items || !items.length) return <div className="table-empty">{t('cleanup.taskStatsNoData')}</div>
    return items.map((it) => {
      const share = Math.min(Math.max(Number(it.share) || 0, 0), 100)
      return (
        <div key={it.name} style={{ padding: '12px 0', borderTop: '1px solid #f1f5f9' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <b style={{ fontSize: 12, wordBreak: 'break-all' }}>{it.name}</b>
            <span style={{ color: '#475569', fontSize: 12 }}>{fmt(it.value)} · {share.toFixed(1)}%</span>
          </div>
          <div style={{ height: 10, background: '#e2e8f0', borderRadius: 999, overflow: 'hidden' }}>
            <div style={{ height: '100%', width: share + '%', minWidth: 2, borderRadius: 999, background: 'linear-gradient(90deg,#38bdf8,#2563eb)' }} />
          </div>
        </div>
      )
    })
  }

  const columns = [
    { key: 'cleanup_date', title: t('cleanup.cleanupDate'), render: (v) => <b>{v}</b> },
    { key: 'sub_table_index', title: t('cleanup.subTableIndex'), render: (v) => <span style={{ background: '#e0f2fe', color: '#0369a1', padding: '2px 8px', borderRadius: 6, fontSize: 11 }}>#{v}</span> },
    { key: 'sub_table_name', title: t('cleanup.subTableName'), render: (v) => <code style={{ fontSize: 11, color: '#475569' }}>{v}</code> },
    { key: 'deleted_rows', title: t('cleanup.deletedRows'), render: (v) => <b>{fmt(v)}</b> },
    { key: 'deleted_tokens_in', title: t('cleanup.inputTokens'), render: fmt },
    { key: 'deleted_tokens_out', title: t('cleanup.outputTokens'), render: fmt },
    { key: 'deleted_tokens_all', title: t('cleanup.totalTokens'), render: (v) => <b>{fmt(v)}</b> },
    { key: 'duration_ms', title: t('cleanup.duration'), render: (v) => (v || 0) + t('cleanup.ms') },
    { key: 'retention_days', title: t('cleanup.retentionDaysCol'), render: (v) => (v || 0) + ' ' + t('cleanup.daysUnit') },
    { key: 'cutoff_time', title: t('cleanup.cutoffTime'), render: fmtTime },
    { key: 'status', title: t('cleanup.status'), render: (v, r) => (
      <span>
        {statusTag(v, r.error_msg)}
        {r.error_msg ? <div style={{ fontSize: 11, marginTop: 4, color: '#b91c1c' }}>{r.error_msg}</div> : null}
      </span>
    ) },
  ]

  return (
    <div className="page">
      <PageHeader icon="🕷" title={t('cleanup.title')}
        breadcrumb={[t('nav.spider'), t('nav.cleanupReport')]}
        info={[
          <span key="total">{t('cleanup.totalCount', { count: total }) || `共 ${total} 条`}</span>,
          <span key="range">{t('cleanup.timeRange')}：{days ?? 30}{t('cleanup.days')}</span>,
        ]}
        actions={<button className="btn btn-primary" disabled={loading} onClick={() => { setPage(1); loadData(1, days); loadState(); loadTables() }}>
          {loading ? t('cleanup.refreshing') : t('common.refresh')}
        </button>}
      />
      <div className="toolbar">
        <span>{t('cleanup.timeRange')}</span>
        <TimeRangeSelector span={days ?? 30} onChange={(v) => { setDays(v); setPage(1) }} levels={levels} loading={levelsLoading} />
        <span style={{ color: 'var(--muted)', fontSize: 12 }}>{t('common.refresh')}: {t('cleanup.refreshHint')}</span>
        {state ? (
          <span>
            <span className={`status-dot ${state.running ? 'status-on' : state.enabled === false ? 'status-off' : ''}`} />
            {!state.enabled ? t('cleanup.disabledConfig')
              : state.running ? t('cleanup.running')
              : t('cleanup.lastRunLabel') + (state.last_run_at ? fmtTime(state.last_run_at) : t('cleanup.neverRun'))}
            {state.last_cutoff_time || state.cutoff_time ? t('cleanup.cutoffPrefix') + fmtTime(state.last_cutoff_time || state.cutoff_time) + t('cleanup.cutoffSuffix') : ''}
          </span>
        ) : null}
        <span>{t('cleanup.retentionDaysLabel')}{retention}</span>
      </div>
      {error ? <div className="alert alert-error">{t('cleanup.loadFailed')}{error}</div> : null}
      {loading ? <div className="table-loading">{t('cleanup.refreshing')}</div> : null}

      {!loading && !error ? (
        <>
          <div className="card-grid kpi-grid">
            <div className="card"><h3>{t('cleanup.totalDeletedRows')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_deleted_rows)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('cleanup.allTasksCumulative')}</div></div>
            <div className="card"><h3>{t('cleanup.totalRecoveredTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_tokens_all)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('cleanup.inputOutputCumulative')}</div></div>
            {/* v2.0.79 阶段CR: 卡3 重设计——保存的时间配置信息 + TAgentHttpTransactionDataItem 现有保存信息（原「当前保留天数配置」，去重） */}
            <div className="card">
              <h3>{t('cleanup.retentionConfigTitle')}</h3>
              <div style={{ fontSize: 24, fontWeight: 800 }}>{retention}</div>
              <div style={{ fontSize: 12, color: '#94a3b8' }}>
                {retDays > 0 ? t('cleanup.retentionConfigDesc', { days: retDays }) : t('cleanup.retentionConfigDescDisabled')}
              </div>
              <div style={{ marginTop: 8, fontSize: 12, lineHeight: 1.8, color: '#64748b' }}>
                <div>
                  {state && state.next_run_at ? t('cleanup.scheduleNext', { time: fmtTime(state.next_run_at) }) : t('cleanup.scheduleDisabled')}
                </div>
                <div>
                  {t('cleanup.currentKeptLabel')}<b>{fmt(kept.rows)}</b> {t('cleanup.rows')}
                </div>
                <div>{kept.earliest || '-'} ~ {kept.latest || '-'}{keptSpan > 0 ? `（${keptSpan} ${t('cleanup.daysUnit')}）` : ''}</div>
              </div>
              <div style={{ marginTop: 6, fontSize: 12, fontWeight: 700, color: judgmentColors[judgment.tone] }}>
                {t(judgment.key)}
              </div>
            </div>
            {/* v2.0.79 阶段CR: 卡4 重设计——TAgentHttpTransactionCleanupReport 删除统计 + 现存数据统计与时间（原「数据覆盖范围」，去重） */}
            <div className="card">
              <h3>{t('cleanup.coverageTitle')}</h3>
              <div style={{ fontSize: 20, fontWeight: 800 }}>
                {fmt(summary.total_deleted_rows)} <span style={{ fontSize: 12, color: '#94a3b8' }}>/</span> ≈{fmt(kept.rows)}
              </div>
              <div style={{ fontSize: 11, color: '#94a3b8' }}>{t('cleanup.coverageDeletedVsKept')}</div>
              <div style={{ marginTop: 8, fontSize: 12, lineHeight: 1.8, color: '#64748b' }}>
                <div>
                  {t('cleanup.coverageDeletedLabel')}：{t('cleanup.coverageTokens', { count: fmt(summary.total_tokens_all) })}
                </div>
                {stats && stats.first_cleanup_date ? (
                  <div>{t('cleanup.coveragePeriod', { start: stats.first_cleanup_date, end: stats.last_cleanup_date })}</div>
                ) : null}
                <div>
                  {t('cleanup.coverageKeptLabel')}：{kept.earliest || '-'} ~ {kept.latest || '-'}
                  {kept.dataBytes + kept.indexBytes > 0 ? ` · ${fmtBytes(kept.dataBytes + kept.indexBytes)}` : ''}
                </div>
                {state && (state.last_cutoff_time || state.cutoff_time) ? (
                  <div style={{ color: '#94a3b8' }}>{t('cleanup.coverageCutoff', { time: fmtTime(state.last_cutoff_time || state.cutoff_time) })}</div>
                ) : null}
              </div>
            </div>
          </div>

          {daily.length ? (
            <div className="card">
              <h3>{t('cleanup.dailyCleanupTrend')}</h3>
              <div className="trend-chart" style={{ display: 'flex', alignItems: 'flex-end', gap: 6, height: 160 }}>
                {daily.map((s) => (
                  <div key={s.date} className="trend-bar"
                       onClick={() => setSelDay(selDay === s.date ? null : s.date)}
                       title={t('cleanup.dailyBarDetail', { date: s.date, rows: fmt(s.deleted_rows), tokens: fmt(s.deleted_tokens_all) })} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, height: '100%', justifyContent: 'flex-end', cursor: 'pointer' }}>
                    <div style={{ width: '70%', height: Math.max(2, ((s.deleted_rows || 0) / dailyMax) * 130), background: selDay === s.date ? 'linear-gradient(180deg,#059669,#047857)' : 'linear-gradient(180deg,#34d399,#10b981)', borderRadius: 4 }} />
                    <span style={{ fontSize: 10, color: selDay === s.date ? '#047857' : '#888', whiteSpace: 'nowrap' }}>{(s.date || '').substring(5)}</span>
                  </div>
                ))}
              </div>
              {selDay ? (
                <div style={{ marginTop: 8, fontSize: 12, color: 'var(--muted)' }}>
                  {(() => { const s = daily.find((x) => x.date === selDay); return s ? t('cleanup.dailyBarDetail', { date: s.date, rows: fmt(s.deleted_rows), tokens: fmt(s.deleted_tokens_all) }) : '' })()}
                </div>
              ) : null}
            </div>
          ) : null}

          {/* v2.0.79 阶段CR: 新模块——TAgentHttpTransactionCleanupReport 项统计（参考模型显示：概览行 + 分表占比条 + 分表明细表） */}
          {stats && stats.total_tasks > 0 ? (
            <div className="card">
              <h3>{t('cleanup.taskStatsTitle')}</h3>
              <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 12 }}>{t('cleanup.taskStatsSubtitle')}</div>
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit,minmax(150px,1fr))', gap: 10, marginBottom: 16 }}>
                {[
                  { label: t('cleanup.taskStatsTotalTasks'), value: fmt(stats.total_tasks) },
                  { label: t('cleanup.taskStatsStatusDist'), value: `${fmt(stats.success_count)} / ${fmt(stats.partial_count)} / ${fmt(stats.failed_count)}` },
                  { label: t('cleanup.taskStatsAvgDuration'), value: fmt(stats.avg_duration_ms) + t('cleanup.ms') },
                  { label: t('cleanup.taskStatsMaxDuration'), value: fmt(stats.max_duration_ms) + t('cleanup.ms') },
                  { label: t('cleanup.taskStatsPeriod'), value: stats.first_cleanup_date && stats.last_cleanup_date ? `${stats.first_cleanup_date} ~ ${stats.last_cleanup_date}` : '-' },
                ].map((it, i) => (
                  <div key={i} style={{ border: '1px solid #f1f5f9', borderRadius: 10, padding: '10px 12px' }}>
                    <div style={{ fontSize: 11, color: '#94a3b8' }}>{it.label}</div>
                    <div style={{ fontSize: 15, fontWeight: 800, marginTop: 2, wordBreak: 'break-all' }}>{it.value}</div>
                  </div>
                ))}
              </div>
              <div className="card-grid">
                <div>
                  <h3 style={{ fontSize: 14 }}>{t('cleanup.taskStatsRowShare')}</h3>
                  {shareBars(rowShares)}
                </div>
                <div>
                  <h3 style={{ fontSize: 14 }}>{t('cleanup.taskStatsTokenShare')}</h3>
                  {shareBars(tokenShares)}
                </div>
              </div>
              <div style={{ marginTop: 16 }}>
                <h3 style={{ fontSize: 14 }}>{t('cleanup.taskStatsTable')}</h3>
                <DataTable
                  rowKey="sub_table_index"
                  rows={subStats}
                  empty={t('cleanup.taskStatsNoData')}
                  columns={[
                    { key: 'sub_table_index', title: t('cleanup.subTableIndex'), render: (v) => <span style={{ background: '#e0f2fe', color: '#0369a1', padding: '2px 8px', borderRadius: 6, fontSize: 11 }}>#{v}</span> },
                    { key: 'sub_table_name', title: t('cleanup.subTableName'), render: (v) => <code style={{ fontSize: 11, color: '#475569' }}>{v}</code> },
                    { key: 'task_count', title: t('cleanup.taskStatsTaskCount'), render: fmt },
                    { key: 'deleted_rows', title: t('cleanup.deletedRows'), render: (v) => <b>{fmt(v)}</b> },
                    { key: 'deleted_tokens_all', title: t('cleanup.totalTokens'), render: fmt },
                    { key: 'status_counts', title: t('cleanup.status'), render: (_, s) => (
                      <span style={{ fontSize: 12 }}>
                        <span style={{ color: '#059669' }}>{fmt(s.success_count)}</span>
                        {' / '}
                        <span style={{ color: s.partial_count > 0 ? '#d97706' : '#94a3b8' }}>{fmt(s.partial_count)}</span>
                        {' / '}
                        <span style={{ color: s.failed_count > 0 ? '#dc2626' : '#94a3b8' }}>{fmt(s.failed_count)}</span>
                      </span>
                    ) },
                    { key: 'avg_duration_ms', title: t('cleanup.taskStatsAvgDuration'), render: (v) => fmt(v) + t('cleanup.ms') },
                    { key: 'last_cleanup_date', title: t('cleanup.lastCleanupDateCol'), render: (v) => v || '-' },
                  ]}
                />
              </div>
            </div>
          ) : null}

          <div className="card">
            <h3>{t('cleanup.capacityMonitor')}</h3>
            <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 12 }}>
              {t('cleanup.rowCountNote')}
            </div>
            {tables == null ? <div className="table-loading">{t('cleanup.refreshing')}</div> : !tables.length ? <div className="table-empty">{t('cleanup.noSubTableMeta')}</div> : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(min(280px,100%),1fr))', gap: 12 }}>
                {tables.map((e) => (
                  <div key={e.table_name} style={{ border: '1px solid #e5e7eb', borderRadius: 14, padding: 14 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
                      <b style={{ fontSize: 12, wordBreak: 'break-all' }}>{e.table_name}</b>
                      {!e.exists ? <span style={{ background: '#f1f5f9', color: '#64748b', fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 999 }}>{t('cleanup.tableMissing')}</span>
                        : e.approximate ? <span style={{ background: '#fef9c3', color: '#854d0e', fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 999 }}>{t('cleanup.approximate')}</span>
                        : <span style={{ background: '#d1fae5', color: '#065f46', fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 999 }}>{t('cleanup.exact')}</span>}
                    </div>
                    <div style={{ fontSize: 22, fontWeight: 800 }}>{e.exists ? fmt(e.row_count) : '-'}<small style={{ fontSize: 11, color: '#94a3b8', marginLeft: 4 }}>{t('cleanup.rows')}</small></div>
                    {e.exists ? (
                      <div style={{ marginTop: 8, fontSize: 11, color: '#64748b', lineHeight: 1.7 }}>
                        {t('cleanup.dataLabel')} <b>{fmtBytes(e.data_bytes)}</b> · {t('cleanup.indexLabel')} <b>{fmtBytes(e.index_bytes)}</b><br />
                        {t('cleanup.timeRangeLabel')}<b>{e.earliest_at || '-'}</b> — <b>{e.latest_at || '-'}</b>
                        {e.approximate ? <div style={{ marginTop: 8 }}><button className="btn btn-sm" onClick={() => exactCount(e.table_name)}>{t('cleanup.exactCount')}</button></div> : null}
                        {e.error ? <div style={{ marginTop: 8, color: '#b91c1c' }}>{e.error}</div> : null}
                      </div>
                    ) : <div style={{ marginTop: 8, fontSize: 11, color: '#64748b' }}>{t('cleanup.notCreatedYet')}</div>}
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="card">
            <h3>{t('cleanup.cleanupDetails')}</h3>
            <DataTable columns={columns} rows={reports} empty={t('cleanup.noCleanupRecords')} />
            <div className="pager">
              <span>{t('cleanup.pageInfo', { start: (page - 1) * PAGE_SIZE + (reports.length ? 1 : 0), end: Math.min(page * PAGE_SIZE, total), total })}</span>
              <button className="btn btn-sm" disabled={page <= 1} onClick={() => { const p = page - 1; setPage(p); loadData(p, days) }}>{t('cleanup.prevPage')}</button>
              <span>{t('cleanup.pageNum', { page, totalPages })}</span>
              <button className="btn btn-sm" disabled={page * PAGE_SIZE >= total} onClick={() => { const p = page + 1; setPage(p); loadData(p, days) }}>{t('cleanup.nextPage')}</button>
            </div>
          </div>
        </>
      ) : null}
    </div>
  )
}
