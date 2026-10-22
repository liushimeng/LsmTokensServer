// 服务质量监控（ServiceQuality）页面主组件（v2.0.86 阶段DA）。
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
//       docs/项目迁移解决方案/源站模型服务质量监控时间维度优化方案_20261022_124609.md
//       docs/项目迁移解决方案/源站模型服务质量监控统计与预测方案_20261022_132957.md
//       docs/项目迁移解决方案/源站模型服务质量监控统计化与峰谷预测方案_20261022_143602.md
//
// v2.0.86 变更要点：
//   1. **删除自动刷新**（30s 轮询 + 勾选框）—— 页面重心由「实时窗口」切到
//      「统计（近1天/近1周逐时段）+ 未来结果预测」；手动刷新按钮走
//      {refresh:true} 绕过服务端缓存，确保拿到新快照。
//   2. **筛选行置顶**（dataviz 规范：过滤器在所有图表之上的**一行**内，
//      统辖下方全部图表）：时间窗口（1h/24h/7d）+ 趋势指标（5 项）。
//   3. **运行经验预测 / 逐时段明细删除模型下拉**，改为所有模型一起展示
//      （预测用小倍数网格 + 全模型对比条形 + 全模型表；明细用「时段×模型」矩阵
//      + 全模型长表）。
//   4. 新增「时段画像与高峰低谷」「时段带 × 模型 矩阵」两个模块，
//      回答「早 9–12、下午 2–6 各时段使用情况不同，高峰/低谷在哪」。
//   5. 模型状态总览改为**统计口径**（stats_24h / stats_7d），状态分档用
//      24h 窗口口径，替代会抖动的实时窗口状态。
//
// 时间维度口径（沿用 v2.0.84）：
//   - 时延一律取成功请求（ok_*），错误单独看图/表；
//   - 进行中时段（partial）在趋势图用空心端点、在明细表用标签标注；
//   - 系数类指标样本 < 5 时不取点 / 显示 —，避免小样本伪造结论。
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
import QoSPeriodMatrix from './QoSPeriodMatrix'
import QoSCompareChart from './QoSCompareChart'
import QoSForecast from './QoSForecast'
import QoSHourProfile from './QoSHourProfile'
import QoSBandMatrix from './QoSBandMatrix'
import {
  STATUS_COLORS, METRICS, MIN_COEFFICIENT_SAMPLES,
  fmtMs, fmtNum, fmtPct, fmtAgo, fmtHour,
} from './constants'

export default function ServiceQuality() {
  const { t } = useI18n()
  const isAdmin = isAdminRole()

  const [report, setReport] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [win, setWin] = useState('24h')
  const [metricKey, setMetricKey] = useState('avg')
  const [selectedModel, setSelectedModel] = useState(null) // 状态卡/热力图点击选中（仅高亮联动，不做过滤）
  const [trendSel, setTrendSel] = useState(() => new Set()) // 折线图选中模型

  const seqRef = useRef(0)

  // refresh=true → 服务端绕过 60s 缓存强制重算（手动刷新必须拿到新快照）
  const load = useCallback((refresh) => {
    const seq = ++seqRef.current
    setLoading(true)
    setError('')
    post('QoSMonitorInterface', refresh ? { refresh: true } : {})
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

  useEffect(() => { load(false) }, [load])

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

  const alertCount = models.filter((m) => (m.stats_24h && m.stats_24h.alert) || m.alert).length
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

  // 全站 24 小时拥堵系数（样本加权）与峰谷
  const siteCongestion = useMemo(() => {
    let w = 0
    let n = 0
    for (const m of models) {
      const s = m.stats_24h || {}
      w += (Number(s.avg_congestion) || 0) * (Number(s.coefficient_samples) || 0)
      n += Number(s.coefficient_samples) || 0
    }
    return n > 0 ? w / n : 0
  }, [models])
  const sitePv = useMemo(() => (report && report.peak_valley) || {}, [report])

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
          <span key="range">{t('serviceQuality.statRange', { days: 7 })}</span>,
          updatedAtText ? <span key="at">{t('serviceQuality.updatedAt', { time: updatedAtText })}</span> : null,
        ]}
        actions={(
          <button className="btn btn-primary" disabled={loading} onClick={() => load(true)}>
            {t('common.refresh')}
          </button>
        )}
      />

      {error ? <div className="alert alert-error">{t('serviceQuality.loadFailed', { error })}</div> : null}
      {loading && !models.length ? <div className="table-loading">{t('serviceQuality.loading')}</div> : null}
      {!loading && !error && !models.length ? <div className="table-empty">{t('serviceQuality.noData')}</div> : null}

      {models.length ? (
        <>
          {/* 统一筛选行（置顶，统辖下方所有图表） */}
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
              <span style={{ fontSize: 11, color: '#94a3b8', marginLeft: 'auto' }}>
                {t('serviceQuality.filter.scopeHint')}
              </span>
            </div>
          </div>

          {/* KPI 概览 */}
          <div className="card-grid kpi-grid">
            <div className="card"><h3>{t('serviceQuality.kpi.models')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(models.length)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.table.model')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.calls7d')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(report.total_calls_7d)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.window7d')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.alerts')}</h3><div style={{ fontSize: 24, fontWeight: 800, color: alertCount ? STATUS_COLORS.severe : undefined }}>{fmtNum(alertCount)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.alertsHint')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.weekOkP95')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{weekOkP95Avg ? fmtMs(weekOkP95Avg) : '-'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.table.weekP95')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.errorRate24h')}</h3><div style={{ fontSize: 24, fontWeight: 800, color: err24 > 5 ? STATUS_COLORS.severe : undefined }}>{err24 ? err24.toFixed(1) + '%' : '0%'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.window24h')}</div></div>
            <div className="card">
              <h3>{t('serviceQuality.kpi.siteCongestion24h')}</h3>
              <div style={{ fontSize: 24, fontWeight: 800, color: siteCongestion > 2 ? STATUS_COLORS.moderate : undefined }}>
                {siteCongestion ? siteCongestion.toFixed(2) : '-'}
              </div>
              <div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.siteCongestionHint')}</div>
            </div>
            <div className="card">
              <h3>{t('serviceQuality.kpi.sitePeak')}</h3>
              <div style={{ fontSize: 24, fontWeight: 800 }}>{fmtHour(sitePv.peak_hour)}</div>
              <div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.sitePeakHint', { valley: fmtHour(sitePv.valley_hour) })}</div>
            </div>
            <div className="card">
              <h3>{t('serviceQuality.kpi.siteValley')}</h3>
              <div style={{ fontSize: 24, fontWeight: 800, color: '#64748b' }}>{fmtHour(sitePv.valley_hour)}</div>
              <div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.siteValleyHint')}</div>
            </div>
          </div>

          {/* ① 模型状态总览（统计卡） */}
          <div className="card">
            <h3>{t('serviceQuality.cards.title')}</h3>
            <QoSCards models={models} selected={selectedModel} onSelect={(name) => setSelectedModel(name)} />
          </div>

          {/* ② 时段画像与高峰低谷 */}
          <div className="card">
            <h3>{t('serviceQuality.profile.title')}</h3>
            <QoSHourProfile report={report} models={models} t={t} />
          </div>

          {/* ③ 时段带 × 模型 矩阵（横向比较） */}
          <div className="card">
            <h3>{t('serviceQuality.bandMatrix.title')}</h3>
            <QoSBandMatrix models={models} allNames={allNames} t={t} />
          </div>

          {/* ④ 运行经验预测（全模型，无下拉） */}
          <div className="card">
            <h3>{t('serviceQuality.forecast.title')}</h3>
            <QoSForecast models={models} allNames={allNames} t={t} />
          </div>

          {/* ⑤ 模型质量对比（全模型固定窗口统计横向条形图） */}
          <div className="card">
            <h3>{t('serviceQuality.compare.title')}</h3>
            <QoSCompareChart models={models} allNames={allNames} t={t} />
          </div>

          {/* ⑥ 服务质量趋势（窗口 + 指标联动） */}
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

          {/* ⑦ 逐时段明细（时段 × 模型 矩阵 + 全模型长表，无下拉） */}
          <div className="card">
            <h3>{t('serviceQuality.period.titleAll')}</h3>
            <QoSPeriodMatrix models={models} allNames={allNames} win={win} t={t} />
            <div style={{ marginTop: 14, fontSize: 12, fontWeight: 600, color: '#334155' }}>
              {t('serviceQuality.period.tableTitleAll')}
            </div>
            <QoSPeriodTable models={models} win={win} t={t} />
          </div>

          {/* ⑧ 拥堵热力图（全模型小倍数网格对比，近 7 天 × 24 小时） */}
          <div className="card">
            <h3>{t('serviceQuality.heatmap.title')}</h3>
            <QoSHeatmap models={models} selected={selectedModel} onSelect={(name) => setSelectedModel(name)} t={t} />
          </div>

          {/* ⑨ 错误堆叠柱状图（窗口联动） */}
          <div className="card">
            <div style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: 8 }}>
              <h3 style={{ margin: 0 }}>{t('serviceQuality.errors.title')}</h3>
              <span style={{ marginLeft: 'auto', fontSize: 12, color: '#94a3b8' }}>
                {windows.find((w) => w.key === win) ? windows.find((w) => w.key === win).label : ''}
              </span>
            </div>
            <QoSErrorBars models={models} win={win} t={t} />
          </div>

          {/* ⑩ 全模型明细表（低对比色系的表格视图出口） */}
          <div className="card">
            <h3>{t('serviceQuality.table.title')}</h3>
            <DataTable
              rowKey="dst_model_name"
              rows={models}
              columns={[
                { key: 'model', title: t('serviceQuality.table.model'), render: (_, m) => <b>{m.dst_model_name}</b> },
                { key: 'status', title: t('serviceQuality.table.status'), render: (_, m) => {
                  const st = (m.stats_24h && m.stats_24h.status) || m.status || 'idle'
                  const al = (m.stats_24h && m.stats_24h.alert) || m.alert
                  return (
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5 }}>
                      <span style={{ width: 8, height: 8, borderRadius: 999, background: STATUS_COLORS[st] || STATUS_COLORS.idle }} />
                      {t(`serviceQuality.status.${st}`)}{al ? ' ⚠' : ''}
                    </span>
                  )
                } },
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
                  key: 'live_coeff', title: t('serviceQuality.table.coeff24h'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.stats_24h && m.stats_24h.avg_congestion) || 0,
                  render: (_, m) => {
                    const s = m.stats_24h || {}
                    if ((Number(s.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES && Number(s.calls) > 0) {
                      return <span title={t('serviceQuality.period.lowSample', { count: (Number(s.coefficient_samples) || 0) })}>—</span>
                    }
                    return (Number(s.avg_congestion) ? <b>{s.avg_congestion.toFixed(2)}</b> : '-')
                  },
                },
                {
                  key: 'avg', title: t('serviceQuality.table.okAvgMs'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.stats_24h && m.stats_24h.ok_avg_elapsed_ms) || 0,
                  render: (_, m) => fmtMs(m.stats_24h && m.stats_24h.ok_avg_elapsed_ms),
                },
                { key: 'p50', title: t('serviceQuality.table.weekP50'), nowrap: true, render: (_, m) => fmtMs(m.week_ok_p50_ms) },
                { key: 'p95', title: t('serviceQuality.table.p95ms'), nowrap: true, sortValue: (m) => Number(m.week_ok_p95_ms) || 0, render: (_, m) => fmtMs(m.week_ok_p95_ms) },
                {
                  key: 'peak', title: t('serviceQuality.table.peakHour'), nowrap: true,
                  render: (_, m) => fmtHour(m.peak_valley && m.peak_valley.peak_hour),
                },
                {
                  key: 'valley', title: t('serviceQuality.table.valleyHour'), nowrap: true,
                  render: (_, m) => fmtHour(m.peak_valley && m.peak_valley.valley_hour),
                },
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
