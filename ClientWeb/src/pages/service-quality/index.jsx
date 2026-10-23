// 服务质量监控（ServiceQuality）页面主组件（v2.0.87 阶段DB）。
//
// 方案：docs/项目迁移解决方案/源站模型服务质量监控页面方案_20261022_113519.md
//       docs/项目迁移解决方案/源站模型服务质量监控时间维度优化方案_20261022_124609.md
//       docs/项目迁移解决方案/源站模型服务质量监控统计与预测方案_20261022_132957.md
//       docs/项目迁移解决方案/源站模型服务质量监控统计化与峰谷预测方案_20261022_143602.md
//       docs/项目迁移解决方案/源站模型服务质量监控多窗口统计与源站横向对比方案_20261022_150702.md
//
// v2.0.88 变更要点（阶段DC）：
//   1. 逐时段明细模块重构：「时段 × 模型」矩阵改为**四维分组列**（每个模型下同时
//      显示 请求数 / 拥堵系数 / 错误率 / 成功平均时延），删除指标切换按钮；
//      外层去掉竖向滚动（表格随页面撑满），首列（时段）横向滚动时粘性；新增合计行。
//   2. 「全模型逐时段明细表」支持折叠/展开，**默认折叠隐藏**，状态 localStorage 记忆。
//   3. 统计窗口 / 趋势窗口 / 趋势指标三组筛选按钮状态 **localStorage 记忆**
//      （key 前缀 lsm:serviceQuality:，按构建角色 manager/user 隔离，见 prefs.js）。
//   4. 修复：D1 趋势图桶数取各系列最大值；D2 错误分布图首模型无桶时崩溃；
//      D3 ⑩ 全模型明细表状态/系数/时延列跟随统计窗口；D4 告警卡提示跟随统计窗口。
//
// v2.0.87 变更要点：
//   1. 统计窗口（近 1 天 / 1 周 / 1 月）与趋势窗口（1h/24h/7d/30d）分离，
//      统计窗口统辖状态总览 / 时段画像 / 时段带矩阵 / 质量对比；
//   2. 「模型状态总览」升级为统计图表：全站统计条 + 源站横向比较（拥堵条 +
//      状态 + 较全站中位偏差 + 错误率/时延/P95/调用量 + 峰谷）+ 最好/最差结论；
//   3. 新增近 30 天逐日趋势、30 天画像/时段带/峰谷，逐时段明细支持 30d；
//   4. 每模型统计卡增加「近 30 天」一行。
//
// v2.0.86 变更要点（保留）：
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
import { isAdminRole, currentRole } from '../../shared/auth'
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
import QoSWindowStats from './QoSWindowStats'
import { qosPrefKey, loadQosPref, saveQosPref } from './prefs'
import {
  STATUS_COLORS, METRICS, MIN_COEFFICIENT_SAMPLES, WINDOWS, STAT_WINDOWS,
  fmtMs, fmtNum, fmtPct, fmtAgo, fmtHour, windowStats, windowSeries, windowPeakValley,
  statWindowLabelKey, trendWindowLabelKey,
} from './constants'

// ---- 状态记忆（v2.0.88 阶段DC）----
// key 按构建角色隔离：管理员端 manager / 用户端 user（同一份源码、两套构建产物）。
const ROLE = currentRole()
const PREF_KEYS = {
  statWin: qosPrefKey(ROLE, 'statWin'),
  trendWin: qosPrefKey(ROLE, 'trendWin'),
  trendMetric: qosPrefKey(ROLE, 'trendMetric'),
  periodTableOpen: qosPrefKey(ROLE, 'periodTableOpen'),
}
const STAT_WIN_KEYS = STAT_WINDOWS.map((w) => w.key)
const TREND_WIN_KEYS = WINDOWS.map((w) => w.key)
const METRIC_KEYS = METRICS.map((m) => m.key)
// 两张 DataTable 的排序记忆键必须分开：DataTable 默认按「角色 + 路径」生成 key，
// 同页多表会共享（如同名列 model 互相串味），此处显式区分。
const SORT_KEY_PERIOD_TABLE = `lsm:serviceQuality:sort:periodTable:${ROLE}`
const SORT_KEY_MODEL_TABLE = `lsm:serviceQuality:sort:modelTable:${ROLE}`

export default function ServiceQuality() {
  const { t } = useI18n()
  const isAdmin = isAdminRole()

  const [report, setReport] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  // 三组筛选按钮 + 明细表折叠状态：初始化读 localStorage，变更即写盘（v2.0.88）
  const [win, setWin] = useState(() => loadQosPref(PREF_KEYS.trendWin, TREND_WIN_KEYS, '24h')) // 趋势窗口：1h/24h/7d/30d
  const [statWin, setStatWin] = useState(() => loadQosPref(PREF_KEYS.statWin, STAT_WIN_KEYS, '7d')) // 统计窗口：24h/7d/30d
  const [metricKey, setMetricKey] = useState(() => loadQosPref(PREF_KEYS.trendMetric, METRIC_KEYS, 'avg'))
  const [periodTableOpen, setPeriodTableOpen] = useState(() => loadQosPref(PREF_KEYS.periodTableOpen, null, false) === true)
  const [selectedModel, setSelectedModel] = useState(null) // 状态卡/热力图点击选中（仅高亮联动，不做过滤）
  const [trendSel, setTrendSel] = useState(() => new Set()) // 折线图选中模型

  // 筛选状态写盘（低频操作，直接写，无需 debounce）
  useEffect(() => { saveQosPref(PREF_KEYS.trendWin, win) }, [win])
  useEffect(() => { saveQosPref(PREF_KEYS.statWin, statWin) }, [statWin])
  useEffect(() => { saveQosPref(PREF_KEYS.trendMetric, metricKey) }, [metricKey])
  useEffect(() => { saveQosPref(PREF_KEYS.periodTableOpen, periodTableOpen) }, [periodTableOpen])

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

  // 当前统计窗口的全站汇总（后端 windows[statWin]，跨模型样本加权）
  const statView = useMemo(
    () => (report && report.windows ? report.windows[statWin] : null),
    [report, statWin],
  )
  const statStats = (statView && statView.stats) || {}
  const sitePv = (statView && statView.peak_valley) || {}
  const siteCongestion = Number(statStats.avg_congestion) || 0
  const siteErrorRate = Number(statStats.error_rate) || 0
  const siteOkP95 = Number(statStats.ok_p95_ms) || 0
  const alertCount = models.filter((m) => {
    const s = windowStats(m, statWin)
    return !!(s && s.alert)
  }).length

  // 逐时段明细表的行数（折叠态提示用；口径与 QoSPeriodTable 一致：仅统计有请求的时段）
  const periodRowCount = useMemo(() => {
    let n = 0
    for (const m of models) {
      for (const p of windowSeries(m, win)) {
        if (Number(p.count) > 0) n++
      }
    }
    return n
  }, [models, win])

  const updatedAt = report && report.generated_at_ms ? new Date(report.generated_at_ms) : null
  const updatedAtText = updatedAt
    ? `${String(updatedAt.getHours()).padStart(2, '0')}:${String(updatedAt.getMinutes()).padStart(2, '0')}:${String(updatedAt.getSeconds()).padStart(2, '0')}`
    : ''

  return (
    <div className="page">
      <PageHeader
        icon="🧠"
        title={t('serviceQuality.title')}
        breadcrumb={[t('nav.modelProxy'), t('nav.serviceQuality')]}
        info={[
          <span key="sub">{t('serviceQuality.subtitle')}</span>,
          <span key="scope">{isAdmin ? t('serviceQuality.scopeAdmin') : t('serviceQuality.scopeUser')}</span>,
          <span key="range">{t('serviceQuality.statRangeMulti')}</span>,
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
              <span style={{ fontSize: 12, color: '#64748b' }}>{t('serviceQuality.filter.statWindow')}</span>
              <span style={{ display: 'inline-flex' }}>
                {STAT_WINDOWS.map((w, i) => (
                  <button
                    key={w.key}
                    className={'btn ' + (statWin === w.key ? 'btn-primary' : '')}
                    style={{
                      borderRadius: i === 0 ? '6px 0 0 6px' : i === STAT_WINDOWS.length - 1 ? '0 6px 6px 0' : 0,
                      padding: '2px 12px', fontSize: 12,
                    }}
                    onClick={() => setStatWin(w.key)}
                  >{t(w.i18n)}</button>
                ))}
              </span>
              <span style={{ fontSize: 12, color: '#64748b', marginLeft: 8 }}>{t('serviceQuality.filter.trendWindow')}</span>
              <span style={{ display: 'inline-flex' }}>
                {WINDOWS.map((w, i) => (
                  <button
                    key={w.key}
                    className={'btn ' + (win === w.key ? 'btn-primary' : '')}
                    style={{
                      borderRadius: i === 0 ? '6px 0 0 6px' : i === WINDOWS.length - 1 ? '0 6px 6px 0' : 0,
                      padding: '2px 12px', fontSize: 12,
                    }}
                    onClick={() => setWin(w.key)}
                  >{t(w.i18n)}</button>
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
              <span style={{ fontSize: 11, color: '#94a3b8', marginLeft: 'auto', maxWidth: 420 }}>
                {t('serviceQuality.filter.scopeHintMulti')}
              </span>
            </div>
          </div>

          {/* KPI 概览（跟随统计窗口） */}
          <div className="card-grid kpi-grid">
            <div className="card"><h3>{t('serviceQuality.kpi.models')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(models.length)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.table.model')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.windowCalls')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(statStats.calls)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t(statWindowLabelKey(statWin))}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.calls30d')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmtNum(report.total_calls_30d)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.window30d')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.alerts')}</h3><div style={{ fontSize: 24, fontWeight: 800, color: alertCount ? STATUS_COLORS.severe : undefined }}>{fmtNum(alertCount)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.alertsHintWindow')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.windowP95')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{siteOkP95 ? fmtMs(siteOkP95) : '-'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.windowP95Hint')}</div></div>
            <div className="card"><h3>{t('serviceQuality.kpi.windowError')}</h3><div style={{ fontSize: 24, fontWeight: 800, color: siteErrorRate > 5 ? STATUS_COLORS.severe : undefined }}>{siteErrorRate ? siteErrorRate.toFixed(1) + '%' : '0%'}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('serviceQuality.kpi.windowErrorHint')}</div></div>
            <div className="card">
              <h3>{t('serviceQuality.kpi.windowCongestion')}</h3>
              <div style={{ fontSize: 24, fontWeight: 800, color: siteCongestion >= 2 ? STATUS_COLORS.moderate : undefined }}>
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

          {/* ① 模型状态总览（统计图表 + 每模型统计卡） */}
          <div className="card">
            <h3>{t('serviceQuality.overview.title')}</h3>
            <QoSWindowStats report={report} models={models} allNames={allNames} win={statWin} t={t} />
            <div style={{ marginTop: 14, fontSize: 12, fontWeight: 600, color: '#334155' }}>
              {t('serviceQuality.cards.title')}
            </div>
            <QoSCards models={models} selected={selectedModel} onSelect={(name) => setSelectedModel(name)} />
          </div>

          {/* ② 时段画像与高峰低谷（跟随统计窗口） */}
          <div className="card">
            <h3>{t('serviceQuality.profile.titleWindow', { window: t(statWindowLabelKey(statWin)) })}</h3>
            <QoSHourProfile report={report} models={models} win={statWin} t={t} />
          </div>

          {/* ③ 时段带 × 模型 矩阵（横向比较，跟随统计窗口） */}
          <div className="card">
            <h3>{t('serviceQuality.bandMatrix.titleWindow', { window: t(statWindowLabelKey(statWin)) })}</h3>
            <QoSBandMatrix models={models} allNames={allNames} win={statWin} t={t} />
          </div>

          {/* ④ 运行经验预测（全模型，无下拉） */}
          <div className="card">
            <h3>{t('serviceQuality.forecast.title')}</h3>
            <QoSForecast models={models} allNames={allNames} t={t} />
          </div>

          {/* ⑤ 模型质量对比（全模型固定窗口统计横向条形图，跟随统计窗口） */}
          <div className="card">
            <h3>{t('serviceQuality.compare.titleWindow', { window: t(statWindowLabelKey(statWin)) })}</h3>
            <QoSCompareChart models={models} allNames={allNames} win={statWin} t={t} />
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

          {/* ⑦ 逐时段明细（四维分组列矩阵 + 可折叠的全模型长表，无下拉） */}
          <div className="card">
            <h3>{t('serviceQuality.period.titleAll')}</h3>
            <div style={{ marginTop: 2, marginBottom: 8, fontSize: 12, fontWeight: 600, color: '#334155' }}>
              {t('serviceQuality.period.matrixTitle', { window: t(trendWindowLabelKey(win)) })}
            </div>
            <QoSPeriodMatrix models={models} allNames={allNames} win={win} t={t} />

            {/* 全模型逐时段明细表：默认折叠隐藏（状态 localStorage 记忆） */}
            <div style={{ marginTop: 16, display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap' }}>
              <span style={{ fontSize: 12, fontWeight: 600, color: '#334155' }}>
                {t('serviceQuality.period.tableTitleAll')}
              </span>
              <button
                className={'btn ' + (periodTableOpen ? '' : 'btn-primary')}
                style={{ padding: '2px 12px', fontSize: 12 }}
                onClick={() => setPeriodTableOpen((v) => !v)}
                aria-expanded={periodTableOpen}
              >
                {periodTableOpen
                  ? t('serviceQuality.period.tableCollapse')
                  : t('serviceQuality.period.tableExpand')}
              </button>
              <span style={{ fontSize: 11, color: '#94a3b8' }}>
                {t('serviceQuality.period.tableRowCount', { count: fmtNum(periodRowCount) })}
              </span>
              {!periodTableOpen ? (
                <span style={{ fontSize: 11, color: '#94a3b8' }}>
                  {t('serviceQuality.period.tableCollapsedHint')}
                </span>
              ) : null}
            </div>
            {periodTableOpen ? (
              <div style={{ marginTop: 8 }}>
                <QoSPeriodTable models={models} win={win} t={t} sortStorageKey={SORT_KEY_PERIOD_TABLE} />
              </div>
            ) : null}
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
                {t((WINDOWS.find((w) => w.key === win) || WINDOWS[1]).i18n)}
              </span>
            </div>
            <QoSErrorBars models={models} win={win} t={t} />
          </div>

          {/* ⑩ 全模型明细表（低对比色系的表格视图出口） */}
          <div className="card">
            <h3>{t('serviceQuality.table.titleWindow', { window: t(statWindowLabelKey(statWin)) })}</h3>
            <DataTable
              rowKey="dst_model_name"
              rows={models}
              sortStorageKey={SORT_KEY_MODEL_TABLE}
              columns={[
                { key: 'model', title: t('serviceQuality.table.model'), render: (_, m) => <b>{m.dst_model_name}</b> },
                { key: 'status', title: t('serviceQuality.table.status'), render: (_, m) => {
                  const s = windowStats(m, statWin) || {}
                  const st = s.status || m.status || 'idle'
                  const al = s.alert || m.alert
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
                {
                  key: 'calls30d', title: t('serviceQuality.table.calls30d'), sortable: true, nowrap: true,
                  sortValue: (m) => Number(m.calls_30d) || 0,
                  render: (_, m) => fmtNum(m.calls_30d),
                },
                { key: 'baseline', title: t('serviceQuality.table.baseline'), nowrap: true, render: (_, m) => m.baseline_ms_per_token > 0 ? m.baseline_ms_per_token.toFixed(2) : '-' },
                { key: 'bands', title: t('serviceQuality.table.bands'), nowrap: true, render: (_, m) => `${m.baseline_bands || 0}/10` },
                {
                  key: 'live_coeff', title: t('serviceQuality.table.coeffWindow', { window: t(statWindowLabelKey(statWin)) }), sortable: true, nowrap: true,
                  sortValue: (m) => Number((windowStats(m, statWin) || {}).avg_congestion) || 0,
                  render: (_, m) => {
                    const s = windowStats(m, statWin) || {}
                    if ((Number(s.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES && Number(s.calls) > 0) {
                      return <span title={t('serviceQuality.period.lowSample', { count: (Number(s.coefficient_samples) || 0) })}>—</span>
                    }
                    return (Number(s.avg_congestion) ? <b>{s.avg_congestion.toFixed(2)}</b> : '-')
                  },
                },
                {
                  key: 'avg', title: t('serviceQuality.table.avgWindow', { window: t(statWindowLabelKey(statWin)) }), sortable: true, nowrap: true,
                  sortValue: (m) => Number((windowStats(m, statWin) || {}).ok_avg_elapsed_ms) || 0,
                  render: (_, m) => fmtMs((windowStats(m, statWin) || {}).ok_avg_elapsed_ms),
                },
                { key: 'p50', title: t('serviceQuality.table.weekP50'), nowrap: true, render: (_, m) => fmtMs(m.week_ok_p50_ms) },
                { key: 'p95', title: t('serviceQuality.table.p95Window', { window: t(statWindowLabelKey(statWin)) }), nowrap: true, sortValue: (m) => Number((windowStats(m, statWin) || {}).ok_p95_ms) || 0, render: (_, m) => fmtMs((windowStats(m, statWin) || {}).ok_p95_ms) },
                {
                  key: 'peak', title: t('serviceQuality.table.peakHour'), nowrap: true,
                  render: (_, m) => fmtHour((windowPeakValley(m, statWin) || {}).peak_hour),
                },
                {
                  key: 'valley', title: t('serviceQuality.table.valleyHour'), nowrap: true,
                  render: (_, m) => fmtHour((windowPeakValley(m, statWin) || {}).valley_hour),
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
