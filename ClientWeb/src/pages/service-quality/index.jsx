// 服务质量监控（ServiceQuality）页面主组件（v2.0.84）。
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
//       docs/项目迁移解决方案/源站模型服务质量监控时间维度优化方案_20261022_124609.md
//
// 一次拉取 QoSMonitorInterface（后端单次 7 天扫描出全部视图：实时卡 + 60 分钟 +
// 24 小时 + 7 天 + 7×24 热力图 + 周分位），前端窗口切换纯客户端切片；
// 30s 自动刷新（可关）配合服务端 30s 缓存达到分钟级准实时。
//
// 时间维度口径（v2.0.84）：
//   - 时延一律取成功请求（ok_*），错误单独看图/表；
//   - 进行中时段（partial）在趋势图用空心端点、在明细表用标签标注；
//   - 系数类指标样本 < 5 时不取点 / 显示 —，避免小样本伪造结论；
//   - 统一筛选行（窗口 + 指标）位于所有图表之上，一次切换全部联动。
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
import QoSPeriodTable from './QoSPeriodTable'
import {
  STATUS_COLORS, METRICS, MIN_COEFFICIENT_SAMPLES,
  fmtMs, fmtNum, fmtPct, fmtRatio, fmtAgo,
} from './constants'

const REFRESH_MS = 30_000

export default function ServiceQuality() {
  const { t } = useI18n()
  const isAdmin = isAdminRole()

  const [report, setReport] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [auto, setAuto] = useState(true)
  const [win, setWin] = useState('24h')
  const [metricKey, setMetricKey] = useState('avg')
  const [selectedModel, setSelectedModel] = useState(null) // 热力图 / 时段明细 / 卡联动
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

  // 30s 自动刷新（保留旧数据渲染，避免骨架闪烁）
  useEffect(() => {
    if (!auto) return
    const tid = setInterval(load, REFRESH_MS)
    return () => clearInterval(tid)
  }, [auto, load])

  const models = useMemo(() => (report && Array.isArray(report.models) ? report.models : []), [report])
  const allNames = useMemo(() => models.map((m) => m.dst_model_name), [models])

  // 首份数据：默认选中调用量 Top3 模型 + 联动模型取第一个
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

  const selected = useMemo(
    () => models.find((m) => m.dst_model_name === selectedModel) || null,
    [models, selectedModel],
  )

  const alertCount = models.filter((m) => m.alert).length
  const weekOkP95Avg = useMemo(() => {
    const vals = models.filter((m) => m.week_ok_p95_ms > 0).map((m) => m.week_ok_p95_ms)
    if (!vals.length) return 0
    return Math.round(vals.reduce((a, b) => a + b, 0) / vals.length)
  }, [models])

  // 近 24 小时全站错误率（小时桶合计）
  const err24 = useMemo(() => {
    let errs = 0
    let calls = 0
    for (const m of models) {
      for (const p of (m.hours || [])) {
        errs += (Number(p.error_429) || 0) + (Number(p.error_5xx) || 0) + (Number(p.error_other) || 0)
        calls += Number(p.count) || 0
      }
    }
    return calls > 0 ? errs / calls * 100 : 0
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
            <div className="card"><h3>{t('serviceQuality.kpi.weekOkP95')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{weekOkP95Avg ? fmtMs(weekOkP95Avg) : '-'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.table.weekP95')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.errorRate24h')}</h3><div style={{ fontSize: 24, fontWeight: 800, color: err24 > 5 ? STATUS_COLORS.severe : undefined }}>{err24 ? err24.toFixed(1) + '%' : '0%'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.window24h')}</div></div>
          </div>

          {/* 模型状态总览卡 */}
          <div className="card">
            <h3>{t('serviceQuality.cards.title')}</h3>
            <QoSCards models={models} selected={selectedModel} onSelect={(name) => setSelectedModel(name)} />
          </div>

          {/* 统一筛选行：统辖下方所有图表（窗口 + 趋势指标） */}
          <div className="card" style={{ padding: '10px 14px' }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 12, color: '#64748b' }}>{t('serviceQuality.filter.window')}</span>
              <span style={{ display: 'inline-flex' }}>
                {windows.map((w) => (
                  <button
                    key={w.key}
                    className={'btn ' + (win === w.key ? 'btn-primary' : '')}
                    style={{
                      borderRadius: w.key === '1h' ? '6px 0 0 6px' : w.key === '7d' ? '0 6px 6px 0' : 0,
                      padding: '2px 12px', fontSize: 12,
                    }}
                    onClick={() => setWin(w.key)}
                  >{w.label}</button>
                ))}
              </span>
              <span style={{ fontSize: 12, color: '#64748b', marginLeft: 8 }}>{t('serviceQuality.filter.metric')}</span>
              <span style={{ display: 'inline-flex' }}>
                {METRICS.map((m, i) => (
                  <button
                    key={m.key}
                    className={'btn ' + (metricKey === m.key ? 'btn-primary' : '')}
                    style={{
                      borderRadius: i === 0 ? '6px 0 0 6px' : i === METRICS.length - 1 ? '0 6px 6px 0' : 0,
                      padding: '2px 10px', fontSize: 12,
                    }}
                    onClick={() => setMetricKey(m.key)}
                  >{t(m.i18n)}</button>
                ))}
              </span>
            </div>
          </div>

          {/* 服务质量趋势（窗口 + 指标联动） */}
          <div className="card">
            <h3>{t('serviceQuality.trend.title')}</h3>
            <QoSTrendChart
              models={models} allNames={allNames} selected={trendSel}
              onToggle={toggleTrend}
              onAll={() => setTrendSel(new Set(allNames))}
              onNone={() => setTrendSel(new Set())}
              win={win} metricKey={metricKey} t={t}
            />
          </div>

          {/* 逐时段明细（近 1 天逐小时 / 近 1 周逐日） */}
          <div className="card">
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8, flexWrap: 'wrap' }}>
              <h3 style={{ margin: 0 }}>{t('serviceQuality.period.title')}</h3>
              <select
                style={{ marginLeft: 'auto', maxWidth: 260, fontSize: 12 }}
                value={selectedModel || ''}
                onChange={(e) => setSelectedModel(e.target.value)}
                aria-label={t('serviceQuality.period.selectModel')}
              >
                {models.map((m) => (
                  <option key={m.dst_model_name} value={m.dst_model_name}>{m.dst_model_name}</option>
                ))}
              </select>
            </div>
            <QoSPeriodTable model={selected} win={win} t={t} />
          </div>

          {/* 拥堵热力图（近 7 天 × 24 小时） */}
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
            <QoSHeatmap model={selected} t={t} />
          </div>

          {/* 错误堆叠柱状图（窗口联动） */}
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
                {
                  key: 'calls7d', title: t('serviceQuality.table.calls7d'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.calls_7d) || 0,
                  render: (_, m) => (
                    <span title={t('serviceQuality.table.okErrSplit', { ok: fmtNum(m.ok_calls_7d), err: fmtNum(m.err_calls_7d) })}>
                      {fmtNum(m.calls_7d)}
                    </span>
                  ),
                },
                {
                  key: 'calls24h', title: t('serviceQuality.table.calls24h'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.calls_24h) || 0,
                  render: (_, m) => fmtNum(m.calls_24h),
                },
                { key: 'baseline', title: t('serviceQuality.table.baseline'), nowrap: true, render: (_, m) => m.baseline_ms_per_token > 0 ? m.baseline_ms_per_token.toFixed(2) : '-' },
                { key: 'bands', title: t('serviceQuality.table.bands'), nowrap: true, render: (_, m) => `${m.baseline_bands || 0}/10` },
                {
                  key: 'live_coeff', title: t('serviceQuality.table.coeff'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.live && m.live.coefficient) || 0,
                  render: (_, m) => ((Number(m.live && m.live.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES && m.live && m.live.req_count > 0)
                    ? <span title={t('serviceQuality.period.lowSample', { count: (m.live && m.live.coefficient_samples) || 0 })}>—</span>
                    : (m.live && m.live.coefficient ? <b>{m.live.coefficient.toFixed(2)}</b> : '-'),
                },
                {
                  key: 'avg', title: t('serviceQuality.table.okAvgMs'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.live && (m.live.ok_avg_elapsed_ms || m.live.avg_elapsed_ms)) || 0,
                  render: (_, m) => fmtMs(m.live && (m.live.ok_avg_elapsed_ms || m.live.avg_elapsed_ms)),
                },
                { key: 'p50', title: t('serviceQuality.table.weekP50'), nowrap: true, render: (_, m) => fmtMs(m.week_ok_p50_ms) },
                { key: 'p95', title: t('serviceQuality.table.p95ms'), sortable: true, nowrap: true, sortValue: (m) => Number(m.week_ok_p95_ms) || 0, render: (_, m) => fmtMs(m.week_ok_p95_ms) },
                { key: 'stall', title: t('serviceQuality.table.stall'), nowrap: true, render: (_, m) => lastHourMetric(m, 'stall_rate', fmtPct) },
                { key: 'jitter', title: t('serviceQuality.table.jitter'), nowrap: true, render: (_, m) => lastHourMetric(m, 'jitter', fmtPct) },
                { key: 'coeff_last', title: t('serviceQuality.table.lastHourCoeff'), nowrap: true, render: (_, m) => lastHourMetric(m, 'avg_congestion', fmtRatio) },
                { key: 'e429', title: t('serviceQuality.table.e429'), nowrap: true, render: (_, m) => errRate24h(m, ['error_429']) },
                { key: 'e5xx', title: t('serviceQuality.table.e5xx'), nowrap: true, render: (_, m) => errRate24h(m, ['error_5xx']) },
                { key: 'last', title: t('serviceQuality.table.lastRequest'), nowrap: true, render: (_, m) => fmtAgo(m.last_request_at_ms, t) },
              ]}
            />
          </div>
        </>
      ) : null}
    </div>
  )
}

// lastHourMetric 最近一个有数据的小时桶指标（低样本显示 —）
function lastHourMetric(m, field, fmt) {
  const hours = m.hours || []
  for (let i = hours.length - 1; i >= 0; i--) {
    if (hours[i].count > 0) {
      if (['stall_rate', 'jitter', 'avg_congestion'].includes(field)
        && (Number(hours[i].coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES) return '—'
      return fmt(hours[i][field])
    }
  }
  return '-'
}

// errRate24h 近 24 小时指定错误分类的占比
function errRate24h(m, fields) {
  const hours = m.hours || []
  let errs = 0
  let calls = 0
  for (const p of hours) {
    calls += Number(p.count) || 0
    for (const f of fields) errs += Number(p[f]) || 0
  }
  return calls > 0 ? fmtPct(errs / calls * 100) : '-'
}
