import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'
import TimeRangeSelector from '../components/TimeRangeSelector'
import { useTimeSpanLevels } from '../shared/useTimeSpanLevels'
import { nearestSpan } from '../shared/timeSpan'
import { useI18n } from '../i18n'

// 过期数据清理报告：CleanupReportInterface（POST JSON）
// action: list {page,page_size,days} / state / tables [table=精确计数]
// 20260826：时间跨度为动态档位（1 小时 ~ transactionRetentionDays+1 天，统一 span 编码）

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
    const lowDisk = String(errMsg || '').indexOf(t('cleanup.diskSpaceLow')) >= 0
    if (s === 'success') return <span style={{ background: '#d1fae5', color: '#065f46', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>{t('common.success')}</span>
    if (s === 'partial') return lowDisk
      ? <span style={{ background: '#e0f2fe', color: '#075985', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>{t('cleanup.deletedNotRebuilt')}</span>
      : <span style={{ background: '#fef3c7', color: '#92400e', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>{t('cleanup.partial')}</span>
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

  const columns = [
    { key: 'cleanup_date', title: t('cleanup.cleanupDate'), render: (v) => <b>{v}</b> },
    { key: 'sub_table_index', title: t('cleanup.subTableIndex'), render: (v) => <span style={{ background: '#e0f2fe', color: '#0369a1', padding: '2px 8px', borderRadius: 6, fontSize: 11 }}>#{v}</span> },
    { key: 'sub_table_name', title: t('cleanup.subTableName'), render: (v) => <code style={{ fontSize: 11, color: '#475569' }}>{v}</code> },
    { key: 'deleted_rows', title: t('cleanup.deletedRows'), render: (v) => <b>{fmt(v)}</b> },
    { key: 'deleted_tokens_in', title: t('cleanup.inputTokens'), render: fmt },
    { key: 'deleted_tokens_out', title: t('cleanup.outputTokens'), render: fmt },
    { key: 'deleted_tokens_all', title: t('cleanup.totalTokens'), render: (v) => <b>{fmt(v)}</b> },
    { key: 'freed_bytes', title: t('cleanup.freedSpace'), render: (v) => <b style={{ color: '#0ea5e9' }}>{fmtBytes(v)}</b> },
    { key: 'duration_ms', title: t('cleanup.duration'), render: (v) => (v || 0) + t('cleanup.ms') },
    { key: 'retention_days', title: t('cleanup.retentionDaysCol'), render: (v) => (v || 0) + ' ' + t('cleanup.daysUnit') },
    { key: 'cutoff_time', title: t('cleanup.cutoffTime'), render: fmtTime },
    { key: 'status', title: t('cleanup.status'), render: (v, r) => (
      <span>
        {statusTag(v, r.error_msg)}
        {r.error_msg ? <div style={{ fontSize: 11, marginTop: 4, color: String(r.error_msg).indexOf(t('cleanup.diskSpaceLow')) >= 0 ? '#0369a1' : '#b91c1c' }}>{r.error_msg}</div> : null}
      </span>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('cleanup.title')}</h2>
      <div className="toolbar">
        <span>{t('cleanup.timeRange')}</span>
        <TimeRangeSelector span={days ?? 30} onChange={(v) => { setDays(v); setPage(1) }} levels={levels} loading={levelsLoading} />
        <button className="btn btn-primary" disabled={loading} onClick={() => { setPage(1); loadData(1, days); loadState(); loadTables() }}>
          {loading ? t('cleanup.refreshing') : t('common.refresh')}
        </button>
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
            <div className="card"><h3>{t('cleanup.totalFreedSpace')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtBytes(summary.total_freed_bytes)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('cleanup.fromDataFree')}</div></div>
            <div className="card"><h3>{t('cleanup.totalRecoveredTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_tokens_all)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('cleanup.inputOutputCumulative')}</div></div>
            <div className="card"><h3>{t('cleanup.currentRetentionConfig')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{retention}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('cleanup.recordsAutoDeleted')}</div></div>
          </div>

          {daily.length ? (
            <div className="card">
              <h3>{t('cleanup.dailyCleanupTrend')}</h3>
              <div className="trend-chart" style={{ display: 'flex', alignItems: 'flex-end', gap: 6, height: 160 }}>
                {daily.map((s) => (
                  <div key={s.date} className="trend-bar"
                       onClick={() => setSelDay(selDay === s.date ? null : s.date)}
                       title={t('cleanup.dailyBarDetail', { date: s.date, rows: fmt(s.deleted_rows), freed: fmtBytes(s.freed_bytes), tokens: fmt(s.deleted_tokens_all) })} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, height: '100%', justifyContent: 'flex-end', cursor: 'pointer' }}>
                    <div style={{ width: '70%', height: Math.max(2, ((s.deleted_rows || 0) / dailyMax) * 130), background: selDay === s.date ? 'linear-gradient(180deg,#059669,#047857)' : 'linear-gradient(180deg,#34d399,#10b981)', borderRadius: 4 }} />
                    <span style={{ fontSize: 10, color: selDay === s.date ? '#047857' : '#888', whiteSpace: 'nowrap' }}>{(s.date || '').substring(5)}</span>
                  </div>
                ))}
              </div>
              {selDay ? (
                <div style={{ marginTop: 8, fontSize: 12, color: 'var(--muted)' }}>
                  {(() => { const s = daily.find((x) => x.date === selDay); return s ? t('cleanup.dailyBarDetail', { date: s.date, rows: fmt(s.deleted_rows), freed: fmtBytes(s.freed_bytes), tokens: fmt(s.deleted_tokens_all) }) : '' })()}
                </div>
              ) : null}
            </div>
          ) : null}

          <div className="card">
            <h3>{t('cleanup.subTableStats')}</h3>
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
