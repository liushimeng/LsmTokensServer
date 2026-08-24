import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'

// 过期数据清理报告：CleanupReportInterface（POST JSON）
// action: list {page,page_size,days} / state / tables [table=精确计数]

const PAGE_SIZE = 20
const DAYS_OPTIONS = [7, 30, 90, 0]
const daysLabel = (d) => (d === 0 ? '无限制' : '最近' + d + '天')
function normalizeDays(v) {
  const n = parseInt(v, 10)
  return DAYS_OPTIONS.includes(n) ? n : 30
}

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

function statusTag(status, errMsg) {
  const s = String(status || 'unknown')
  const lowDisk = String(errMsg || '').indexOf('磁盘空间不足') >= 0
  if (s === 'success') return <span style={{ background: '#d1fae5', color: '#065f46', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>成功</span>
  if (s === 'partial') return lowDisk
    ? <span style={{ background: '#e0f2fe', color: '#075985', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>已删除·未重建</span>
    : <span style={{ background: '#fef3c7', color: '#92400e', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>部分</span>
  if (s === 'failed') return (
    <span>
      <span style={{ background: '#fee2e2', color: '#991b1b', padding: '3px 9px', borderRadius: 6, fontSize: 11, fontWeight: 700 }}>失败</span>
      {String(errMsg || '').indexOf('下次运行自动继续') >= 0 ? <span style={{ marginLeft: 4, fontSize: 12, color: '#6b7280' }}>⏳ 自动重试中（服务将自动重试，无需人工干预）</span> : null}
    </span>
  )
  return <span>{s}</span>
}

export default function CleanupReport() {
  const [days, setDays] = useState(() => normalizeDays(localStorage.getItem('lsm:cleanupReport:days:v1')))
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
      else alert('精确计数失败：' + (d.message || '表过大超时，仍显示近似值'))
    } catch (e) { alert('精确计数失败：' + e.message) }
  }

  const totalPages = Math.ceil(total / PAGE_SIZE) || 1
  const retention = state && typeof state.retention_days === 'number'
    ? (state.retention_days <= 0 ? '已禁用' : state.retention_days + ' 天')
    : '-'
  const dailyMax = Math.max(1, ...daily.map((s) => s.deleted_rows || 0))

  const columns = [
    { key: 'cleanup_date', title: '清理日期', render: (v) => <b>{v}</b> },
    { key: 'sub_table_index', title: '分表索引', render: (v) => <span style={{ background: '#e0f2fe', color: '#0369a1', padding: '2px 8px', borderRadius: 6, fontSize: 11 }}>#{v}</span> },
    { key: 'sub_table_name', title: '分表名', render: (v) => <code style={{ fontSize: 11, color: '#475569' }}>{v}</code> },
    { key: 'deleted_rows', title: '删除条数', render: (v) => <b>{fmt(v)}</b> },
    { key: 'deleted_tokens_in', title: '输入 Tokens', render: fmt },
    { key: 'deleted_tokens_out', title: '输出 Tokens', render: fmt },
    { key: 'deleted_tokens_all', title: '总 Tokens', render: (v) => <b>{fmt(v)}</b> },
    { key: 'freed_bytes', title: '释放空间', render: (v) => <b style={{ color: '#0ea5e9' }}>{fmtBytes(v)}</b> },
    { key: 'duration_ms', title: '耗时', render: (v) => (v || 0) + ' ms' },
    { key: 'retention_days', title: '保留天数', render: (v) => (v || 0) + ' 天' },
    { key: 'cutoff_time', title: '过期判定时间', render: fmtTime },
    { key: 'status', title: '状态', render: (v, r) => (
      <span>
        {statusTag(v, r.error_msg)}
        {r.error_msg ? <div style={{ fontSize: 11, marginTop: 4, color: String(r.error_msg).indexOf('磁盘空间不足') >= 0 ? '#0369a1' : '#b91c1c' }}>{r.error_msg}</div> : null}
      </span>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">过期数据清理报告</h2>
      <div className="toolbar">
        <span>时间跨度</span>
        <select value={days} onChange={(e) => { setDays(normalizeDays(e.target.value)); setPage(1) }}>
          {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{daysLabel(d)}</option>)}
        </select>
        <button className="btn btn-primary" disabled={loading} onClick={() => { setPage(1); loadData(1, days); loadState(); loadTables() }}>
          {loading ? '加载中…' : '刷新报告'}
        </button>
        {state ? (
          <span>
            <span className={`status-dot ${state.running ? 'status-on' : state.enabled === false ? 'status-off' : ''}`} />
            {!state.enabled ? '已禁用（配置 TransactionRetentionDays=0）'
              : state.running ? '正在执行…'
              : '上次执行：' + (state.last_run_at ? fmtTime(state.last_run_at) : '从未')}
            {state.last_cutoff_time || state.cutoff_time ? '（截止：' + fmtTime(state.last_cutoff_time || state.cutoff_time) + '）' : ''}
          </span>
        ) : null}
        <span>保留天数：{retention}</span>
      </div>
      {error ? <div className="alert alert-error">加载失败：{error}</div> : null}
      {loading ? <div className="table-loading">正在加载清理报告…</div> : null}

      {!loading && !error ? (
        <>
          <div className="card-grid kpi-grid">
            <div className="card"><h3>累计删除条数</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_deleted_rows)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>所有清理任务累计</div></div>
            <div className="card"><h3>累计释放磁盘空间</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtBytes(summary.total_freed_bytes)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>来自 information_schema.DATA_FREE</div></div>
            <div className="card"><h3>累计回收 Tokens</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_tokens_all)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>输入 + 输出 累计</div></div>
            <div className="card"><h3>当前保留天数配置</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{retention}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>超过该天数的浏览记录将被自动删除</div></div>
          </div>

          {daily.length ? (
            <div className="card">
              <h3>每日清理趋势</h3>
              <div className="trend-chart" style={{ display: 'flex', alignItems: 'flex-end', gap: 6, height: 160 }}>
                {daily.map((s) => (
                  <div key={s.date} className="trend-bar"
                       onClick={() => setSelDay(selDay === s.date ? null : s.date)}
                       title={`${s.date}：删除 ${fmt(s.deleted_rows)} 条 / 释放 ${fmtBytes(s.freed_bytes)} / Tokens ${fmt(s.deleted_tokens_all)}`} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, height: '100%', justifyContent: 'flex-end', cursor: 'pointer' }}>
                    <div style={{ width: '70%', height: Math.max(2, ((s.deleted_rows || 0) / dailyMax) * 130), background: selDay === s.date ? 'linear-gradient(180deg,#059669,#047857)' : 'linear-gradient(180deg,#34d399,#10b981)', borderRadius: 4 }} />
                    <span style={{ fontSize: 10, color: selDay === s.date ? '#047857' : '#888', whiteSpace: 'nowrap' }}>{(s.date || '').substring(5)}</span>
                  </div>
                ))}
              </div>
              {selDay ? (
                <div style={{ marginTop: 8, fontSize: 12, color: 'var(--muted)' }}>
                  {(() => { const s = daily.find((x) => x.date === selDay); return s ? `${s.date}：删除 ${fmt(s.deleted_rows)} 条 / 释放 ${fmtBytes(s.freed_bytes)} / Tokens ${fmt(s.deleted_tokens_all)}` : '' })()}
                </div>
              ) : null}
            </div>
          ) : null}

          <div className="card">
            <h3>分表统计（Schema Inspector）</h3>
            <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 12 }}>
              行数默认来自 information_schema.TABLES（估算值）。点击「精确计数」执行 COUNT(*)（25s 超时保护，超时回退近似值）。
            </div>
            {tables == null ? <div className="table-loading">加载中…</div> : !tables.length ? <div className="table-empty">暂无分表元数据。</div> : (
              <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill,minmax(min(280px,100%),1fr))', gap: 12 }}>
                {tables.map((e) => (
                  <div key={e.table_name} style={{ border: '1px solid #e5e7eb', borderRadius: 14, padding: 14 }}>
                    <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
                      <b style={{ fontSize: 12, wordBreak: 'break-all' }}>{e.table_name}</b>
                      {!e.exists ? <span style={{ background: '#f1f5f9', color: '#64748b', fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 999 }}>表缺失</span>
                        : e.approximate ? <span style={{ background: '#fef9c3', color: '#854d0e', fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 999 }}>≈ 估算</span>
                        : <span style={{ background: '#d1fae5', color: '#065f46', fontSize: 10, fontWeight: 700, padding: '2px 7px', borderRadius: 999 }}>精确</span>}
                    </div>
                    <div style={{ fontSize: 22, fontWeight: 800 }}>{e.exists ? fmt(e.row_count) : '-'}<small style={{ fontSize: 11, color: '#94a3b8', marginLeft: 4 }}>行</small></div>
                    {e.exists ? (
                      <div style={{ marginTop: 8, fontSize: 11, color: '#64748b', lineHeight: 1.7 }}>
                        数据 <b>{fmtBytes(e.data_bytes)}</b> · 索引 <b>{fmtBytes(e.index_bytes)}</b><br />
                        时间范围：<b>{e.earliest_at || '-'}</b> — <b>{e.latest_at || '-'}</b>
                        {e.approximate ? <div style={{ marginTop: 8 }}><button className="btn btn-sm" onClick={() => exactCount(e.table_name)}>精确计数</button></div> : null}
                        {e.error ? <div style={{ marginTop: 8, color: '#b91c1c' }}>{e.error}</div> : null}
                      </div>
                    ) : <div style={{ marginTop: 8, fontSize: 11, color: '#64748b' }}>该分表尚未创建</div>}
                  </div>
                ))}
              </div>
            )}
          </div>

          <div className="card">
            <h3>清理明细</h3>
            <DataTable columns={columns} rows={reports} empty="暂无清理记录" />
            <div className="pager">
              <span>第 {(page - 1) * PAGE_SIZE + (reports.length ? 1 : 0)}-{Math.min(page * PAGE_SIZE, total)} 条 / 共 {total} 条</span>
              <button className="btn btn-sm" disabled={page <= 1} onClick={() => { const p = page - 1; setPage(p); loadData(p, days) }}>上一页</button>
              <span>第 {page} / {totalPages} 页</span>
              <button className="btn btn-sm" disabled={page * PAGE_SIZE >= total} onClick={() => { const p = page + 1; setPage(p); loadData(p, days) }}>下一页</button>
            </div>
          </div>
        </>
      ) : null}
    </div>
  )
}
