// 服务质量监控（ServiceQuality）页面主组件（v2.0.83）。
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
//
// 一次拉取 QoSMonitorInterface（后端单次 7 天扫描出全部视图：实时卡 + 60 分钟 +
// 24 小时 + 7 天 + 7×24 热力图 + 周 P50/P95），前端窗口切换纯客户端切片；
// 30s 自动刷新（可关）配合服务端 60s 缓存达到分钟级准实时。
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../../shared/api'
import { isAdminRole } from '../../shared/auth'
import PageHeader from '../../components/PageHeader'
import DataTable from '../../components/DataTable'
import { useI18n } from '../../i18n'
import QoSCards from './QoSCards'
import QoSTrendChart from './QoSTrendChart'
import QoSHeatmap from './QoSHeatmap'
import QoSErrorBars from './QoSErrorBars'
import { STATUS_COLORS, fmtMs, fmtNum, fmtPct } from './constants'

const REFRESH_MS = 30_000

export default function ServiceQuality() {
  const { t } = useI18n()
  const isAdmin = isAdminRole()

  const [report, setReport] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [auto, setAuto] = useState(true)
  const [win, setWin] = useState('24h')
  const [selectedModel, setSelectedModel] = useState(null) // 热力图模型
  const [trendSel, setTrendSel] = useState(() => new Set()) // 折线图选中模型

  const seqRef = useRef(0)

  const load = useCallback(() => {
    const seq = ++seqRef.current
    setLoading(true)
    setError('')
    post('QoSMonitorInterface', {})
      .then((res) => {
        if (seqRef.current !== seq) return
        setReport((res && res.data) || null)
      })
      .catch((e) => {
        if (seqRef.current !== seq) return
        setError(e && e.message ? e.message : String(e))
      })
      .finally(() => {
        if (seqRef.current === seq) setLoading(false)
      })
  }, [])

  useEffect(() => { load() }, [load])

  // 30s 自动刷新
  useEffect(() => {
    if (!auto) return
    const tid = setInterval(load, REFRESH_MS)
    return () => clearInterval(tid)
  }, [auto, load])

  const models = useMemo(() => (report && Array.isArray(report.models) ? report.models : []), [report])
  const allNames = useMemo(() => models.map((m) => m.dst_model_name), [models])

  // 首份数据：默认选中调用量 Top3 模型 + 热力图定位第一个模型
  useEffect(() => {
    if (!models.length) return
    setSelectedModel((prev) => (prev && models.some((m) => m.dst_model_name === prev)
      ? prev
      : models[0].dst_model_name))
    setTrendSel((prev) => {
      if (prev.size > 0 && models.some((m) => prev.has(m.dst_model_name))) return prev
      return new Set(models.slice(0, 3).map((m) => m.dst_model_name))
    })
  }, [models])

  const toggleTrend = (name) => {
    setTrendSel((prev) => {
      const next = new Set(prev)
      if (next.has(name)) next.delete(name)
      else next.add(name)
      return next
    })
  }

  const heatmapModel = useMemo(
    () => models.find((m) => m.dst_model_name === selectedModel) || null,
    [models, selectedModel],
  )

  const alertCount = models.filter((m) => m.alert).length
  const weekP95Avg = useMemo(() => {
    const vals = models.filter((m) => m.week_p95_ms > 0).map((m) => m.week_p95_ms)
    if (!vals.length) return 0
    return Math.round(vals.reduce((a, b) => a + b, 0) / vals.length)
  }, [models])

  const updatedAt = report && report.generated_at_ms ? new Date(report.generated_at_ms) : null
  const updatedAtText = updatedAt
    ? `${String(updatedAt.getHours()).padStart(2, '0')}:${String(updatedAt.getMinutes()).padStart(2, '0')}:${String(updatedAt.getSeconds()).padStart(2, '0')}`
    : ''

  const windows = [
    { key: '1h', label: t('serviceQuality.window1h') },
    { key: '24h', label: t('serviceQuality.window24h') },
    { key: '7d', label: t('serviceQuality.window7d') },
  ]

  return (
    <div className="page">
      <PageHeader
        icon="🧠"
        title={t('serviceQuality.title')}
        breadcrumb={[t('nav.modelProxy'), t('nav.serviceQuality')]}
        info={[
          <span key="sub">{t('serviceQuality.subtitle')}</span>,
          <span key="scope">{isAdmin ? t('serviceQuality.scopeAdmin') : t('serviceQuality.scopeUser')}</span>,
          updatedAtText ? <span key="at">{t('serviceQuality.updatedAt', { time: updatedAtText })}</span> : null,
        ]}
        actions={(
          <span style={{ display: 'inline-flex', gap: 8, alignItems: 'center' }}>
            <label style={{ display: 'inline-flex', gap: 4, alignItems: 'center', fontSize: 12, cursor: 'pointer', color: '#475569' }}>
              <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
              {t('serviceQuality.autoRefresh')}
            </label>
            <button className="btn btn-primary" disabled={loading} onClick={load}>{t('common.refresh')}</button>
          </span>
        )}
      />

      {error ? <div className="alert alert-error">{t('serviceQuality.loadFailed', { error })}</div> : null}
      {loading && !models.length ? <div className="table-loading">{t('serviceQuality.loading')}</div> : null}
      {!loading && !error && !models.length ? <div className="table-empty">{t('serviceQuality.noData')}</div> : null}

      {models.length ? (
        <>
          {/* KPI 概览 */}
          <div className="card-grid kpi-grid">
            <div className="card"><h3>{t('serviceQuality.kpi.models')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(models.length)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.table.model')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.calls7d')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(report.total_calls_7d)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.window7d')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.alerts')}</h3><div style={{ fontSize: 24, fontWeight: 800, color: alertCount ? STATUS_COLORS.severe : undefined }}>{fmtNum(alertCount)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.cards.coefficient')} &gt; 2.0 / {t('serviceQuality.cards.errorRate')} &gt; 5%</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.weekP95')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{weekP95Avg ? fmtMs(weekP95Avg) : '-'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.table.weekP95')}</div></div>
          </div>

          {/* 模型状态总览卡 */}
          <div className="card">
            <h3>{t('serviceQuality.cards.title')}</h3>
            <QoSCards models={models} selected={selectedModel} onSelect={(name) => setSelectedModel(name)} />
          </div>

          {/* 时延趋势折线（窗口切换） */}
          <div className="card">
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <h3 style={{ margin: 0 }}>{t('serviceQuality.trend.title')}</h3>
              <div style={{ marginLeft: 'auto', display: 'inline-flex' }}>
                {windows.map((w) => (
                  <button
                    key={w.key}
                    className={'btn ' + (win === w.key ? 'btn-primary' : '')}
                    style={{ borderRadius: w.key === '1h' ? '6px 0 0 6px' : w.key === '7d' ? '0 6px 6px 0' : 0, padding: '2px 10px', fontSize: 12 }}
                    onClick={() => setWin(w.key)}
                  >{w.label}</button>
                ))}
              </div>
            </div>
            <QoSTrendChart models={models} allNames={allNames} selected={trendSel} onToggle={toggleTrend} win={win} t={t} />
          </div>

          {/* 拥堵热力图（模型选择） */}
          <div className="card">
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
              <h3 style={{ margin: 0 }}>{t('serviceQuality.heatmap.title')}</h3>
              <select
                style={{ marginLeft: 'auto', maxWidth: 260, fontSize: 12 }}
                value={selectedModel || ''}
                onChange={(e) => setSelectedModel(e.target.value)}
                aria-label={t('serviceQuality.heatmap.selectModel')}
              >
                {models.map((m) => (
                  <option key={m.dst_model_name} value={m.dst_model_name}>{m.dst_model_name}</option>
                ))}
              </select>
            </div>
            <QoSHeatmap model={heatmapModel} t={t} />
          </div>

          {/* 错误堆叠柱状图 */}
          <div className="card">
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <h3 style={{ margin: 0 }}>{t('serviceQuality.errors.title')}</h3>
              <span style={{ marginLeft: 'auto', fontSize: 12, color: '#94a3b8' }}>
                {windows.find((w) => w.key === win) ? windows.find((w) => w.key === win).label : ''}
              </span>
            </div>
            <QoSErrorBars models={models} win={win} t={t} />
          </div>

          {/* 全模型明细表（低对比色系的表格视图出口） */}
          <div className="card">
            <h3>{t('serviceQuality.table.title')}</h3>
            <DataTable
              rowKey="dst_model_name"
              rows={models}
              columns={[
                { key: 'model', title: t('serviceQuality.table.model'), render: (_, m) => <b>{m.dst_model_name}</b> },
                { key: 'status', title: t('serviceQuality.table.status'), render: (_, m) => (
                  <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                    <span style={{ width: 8, height: 8, borderRadius: 999, background: STATUS_COLORS[m.status] || STATUS_COLORS.idle }} />
                    {t(`serviceQuality.status.${m.status}`)}{m.alert ? ' ⚠' : ''}
                  </span>
                ) },
                { key: 'calls7d', title: t('serviceQuality.table.calls7d'), render: (_, m) => fmtNum(m.calls_7d) },
                { key: 'calls24h', title: t('serviceQuality.table.calls24h'), render: (_, m) => fmtNum(m.calls_24h) },
                { key: 'baseline', title: t('serviceQuality.table.baseline'), render: (_, m) => m.baseline_ms_per_token > 0 ? m.baseline_ms_per_token.toFixed(2) : '-' },
                { key: 'live_coeff', title: t('serviceQuality.table.coeff'), render: (_, m) => m.live.coefficient ? <b>{m.live.coefficient.toFixed(2)}</b> : '-' },
                { key: 'avg', title: t('serviceQuality.table.avgMs'), render: (_, m) => fmtMs(m.live.avg_elapsed_ms) },
                { key: 'p95', title: t('serviceQuality.table.p95ms'), render: (_, m) => fmtMs(m.week_p95_ms) },
                { key: 'weekp50', title: t('serviceQuality.table.weekP50'), render: (_, m) => fmtMs(m.week_p50_ms) },
                { key: 'stall', title: t('serviceQuality.table.stall'), render: (_, m) => stallOfDay(m) },
                { key: 'jitter', title: t('serviceQuality.table.jitter'), render: (_, m) => jitterOfDay(m) },
                { key: 'e429', title: t('serviceQuality.table.e429'), render: (_, m) => errRateOfDay(m, 'error_429') },
                { key: 'e5xx', title: t('serviceQuality.table.e5xx'), render: (_, m) => errRateOfDay(m, 'error_5xx') },
              ]}
            />
          </div>
        </>
      ) : null}
    </div>
  )
}

// stallOfDay 卡顿率（最近一个有数据的小时桶）
function stallOfDay(m) {
  const hours = m.hours || []
  for (let i = hours.length - 1; i >= 0; i--) {
    if (hours[i].count > 0) return fmtPct(hours[i].stall_rate)
  }
  return '-'
}
function jitterOfDay(m) {
  const hours = m.hours || []
  for (let i = hours.length - 1; i >= 0; i--) {
    if (hours[i].count > 0) return fmtPct(hours[i].jitter)
  }
  return '-'
}
function errRateOfDay(m, field) {
  const hours = m.hours || []
  for (let i = hours.length - 1; i >= 0; i--) {
    if (hours[i].count > 0) {
      const errs = Number(hours[i][field]) || 0
      return fmtPct(errs / hours[i].count * 100)
    }
  }
  return '-'
}
