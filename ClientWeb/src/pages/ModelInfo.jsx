import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import HourlyTrendPanel from '../components/HourlyTrendPanel'
import { useI18n } from '../i18n'

// 模型信息（统计页）：ModelInfoInterface
//   - action='stats' 返回 summary/models/dst_summary/dst_models（仅用户端）
//   - action='list' 用户端「我的模型信息列表」（成本/能力/动态性能标签/源站数）
//   - action='trend'（新增）小时级 K 线图：调用次数 + Tokens 数（小时桶/天桶自适应）
// 无第三方图表库：K 线图用自研 SVG HourlyTrendPanel + KLineTrendChart 组件。

const DAYS_OPTIONS = [1, 3, 5, 7, 14, 30, 60, 90, 0]

function fmt(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}
function pct(v) {
  v = Number(v) || 0
  return Math.min(Math.max(v, 0), 100).toFixed(2) + '%'
}
function normalizeDays(v) {
  const n = parseInt(v, 10)
  return DAYS_OPTIONS.includes(n) ? n : 3
}

export default function ModelInfo(props) {
  const { t } = useI18n()
  const q = props?.route?.query
  const isAdmin = isAdminRole()
  // 记忆 key 按角色隔离（用户端与管理端不复用同一天数偏好）
  const storageKey = `lsm:modelInfo:days:v1:${isAdmin ? 'admin:__all__' : 'user'}`
  const [days, setDays] = useState(() => normalizeDays(q?.get('days') || localStorage.getItem(storageKey)))
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [myModels, setMyModels] = useState(null) // 用户端"我的模型信息列表"（action=list）

  const daysLabel = (d) => (d === 0 ? t('modelInfo.daysAll', { days: d }) : t('modelInfo.daysDay', { days: d }))

  const loadStats = useCallback((d) => {
    setLoading(true)
    setError('')
    post('ModelInfoInterface', { action: 'stats', days: d })
      .then((res) => setData((res && res.data) || {}))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    localStorage.setItem(storageKey, String(days))
    loadStats(days)
    if (!isAdmin && myModels === null) {
      // 用户端独有：我的模型信息列表（成本/能力/动态性能标签/源站数）
      post('ModelInfoInterface', { action: 'list' })
        .then((d) => setMyModels((d && d.data) || []))
        .catch(() => setMyModels([]))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days, loadStats])

  const summary = (data && data.summary) || {}
  const models = (data && data.models) || []
  const dstSummary = (data && data.dst_summary) || {}
  const dstModels = (data && data.dst_models) || []
  const tokenMax = Math.max(1, ...models.map((m) => m.tokens_all_size || 0))
  const callMax = Math.max(1, ...models.map((m) => m.call_count || 0))

  const shareBars = (list, mode) => {
    if (!list.length) return <div className="table-empty">{t('modelInfo.noStatsData')}</div>
    return list.slice(0, 8).map((it) => {
      const share = Math.min(Math.max(Number(mode === 'token' ? it.token_share : it.call_share) || 0, 0), 100)
      const value = mode === 'token' ? t('modelInfo.tokensUnit', { count: fmt(it.tokens_all_size) }) : t('modelInfo.callsUnit', { count: fmt(it.call_count) })
      return (
        <div key={it.model_name} style={{ padding: '12px 0', borderTop: '1px solid #f1f5f9' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <b>{it.model_name || t('modelInfo.unknownModel')}</b>
            <span style={{ color: '#475569', fontSize: 12 }}>{value} · {pct(share)}</span>
          </div>
          <div style={{ height: 10, background: '#e2e8f0', borderRadius: 999, overflow: 'hidden' }}>
            <div style={{ height: '100%', width: share + '%', minWidth: 2, borderRadius: 999, background: mode === 'call' ? 'linear-gradient(90deg,#34d399,#059669)' : 'linear-gradient(90deg,#38bdf8,#2563eb)' }} />
          </div>
        </div>
      )
    })
  }

  return (
    <div className="page">
      <h2 className="page-title">{t('modelInfo.title2')}</h2>
      <div className="toolbar">
        <span>{t('modelInfo.timeSpan')}</span>
        <select value={days} onChange={(e) => setDays(normalizeDays(e.target.value))}>
          {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{daysLabel(d)}</option>)}
        </select>
        <button className="btn btn-primary" disabled={loading} onClick={() => loadStats(days)}>{loading ? t('modelInfo.loading') : t('modelInfo.refresh')}</button>
        <span style={{ color: '#888', fontSize: 13 }}>{isAdmin ? t('modelInfo.adminStats') : t('modelInfo.userStats')}</span>
      </div>
      {error ? <div className="alert alert-error">{t('modelInfo.loadFailed', { error })}</div> : null}
      {loading ? <div className="table-loading">{t('modelInfo.loading')}</div> : !models.length && !error ? <div className="table-empty">{t('modelInfo.noModelData')}</div> : null}

      {models.length || dstModels.length || (myModels && myModels.length) ? (
        <>
          <div className="card-grid kpi-grid">
            <div className="card"><h3>{t('modelInfo.statModelCount')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.model_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.byTargetModel')}</div></div>
            <div className="card"><h3>{t('modelInfo.totalCallCount')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_call_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.requestTotal')}</div></div>
            <div className="card"><h3>{t('modelInfo.totalTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.tokens_all_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.inputOutput')}</div></div>
            <div className="card"><h3>{t('modelInfo.inputOutputTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.tokens_input_size)} / {fmt(summary.tokens_output_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.tokenStructure')}</div></div>
          </div>

          {models.length || dstModels.length ? (
            <div className="card">
              <h3>{t('modelInfo.hourlyTrend')}</h3>
              <HourlyTrendPanel
                api="ModelInfoInterface"
                storageKey={`lsm:modelInfo:trend:v1:${isAdmin ? 'admin' : 'user'}`}
                labels={{
                  '1d': t('modelInfo.trendWindow1d'),
                  '3d': t('modelInfo.trendWindow3d'),
                  '7d': t('modelInfo.trendWindow7d'),
                  '30d': t('modelInfo.trendWindow30d'),
                  loading: t('modelInfo.trendLoading'),
                  empty: t('modelInfo.trendEmpty'),
                  call: t('modelInfo.trendCallSeries'),
                  token: t('modelInfo.trendTokenSeries'),
                  tooltip: t('modelInfo.trendTooltip'),
                }}
              />
            </div>
          ) : null}

          <div className="card-grid">
            <div className="card">
              <h3>{t('modelInfo.modelTokenUsage')}</h3>
              <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>{t('modelInfo.rankByTotalTokens')}</div>
              {shareBars(models.slice().sort((a, b) => (b.tokens_all_size || 0) - (a.tokens_all_size || 0)), 'token')}
            </div>
            <div className="card">
              <h3>{t('modelInfo.modelCallCount')}</h3>
              <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>{t('modelInfo.rankByCallCount')}</div>
              {shareBars(models.slice().sort((a, b) => (b.call_count || 0) - (a.call_count || 0)), 'call')}
            </div>
          </div>

          <div className="card">
            <h3>{t('modelInfo.modelDetail', { view: isAdmin ? t('modelInfo.adminView') : t('modelInfo.userView') })}</h3>
            <DataTable
              rowKey="model_name"
              rows={models}
              columns={[
                { key: 'rank', title: t('modelInfo.rank'), width: 60, render: (_, m) => models.indexOf(m) + 1 },
                { key: 'model_name', title: t('modelInfo.modelName2'), render: (v) => <b>{v || t('modelInfo.unknownModel')}</b> },
                { key: 'call_count', title: t('modelInfo.callCount2'), render: (v, m) => <span title={t('modelInfo.callShare') + ' ' + pct(m.call_share)}>{fmt(v)}</span> },
                { key: 'call_share', title: t('modelInfo.callShare'), render: (v) => <b style={{ color: '#2563eb' }}>{pct(v)}</b> },
                { key: 'tokens_input_size', title: t('modelInfo.inputTokens'), render: fmt },
                { key: 'tokens_output_size', title: t('modelInfo.outputTokens'), render: fmt },
                { key: 'tokens_all_size', title: t('modelInfo.totalTokens2'), render: (v, m) => <b title={t('modelInfo.tokenShare') + ' ' + pct(m.token_share)}>{fmt(v)}</b> },
                { key: 'token_share', title: t('modelInfo.tokenShare'), render: (v) => <b style={{ color: '#2563eb' }}>{pct(v)}</b> },
                ...(isAdmin ? [{ key: 'user_count', title: t('modelInfo.activeUsers'), render: fmt }] : []),
              ]}
            />
          </div>

          {!isAdmin && dstModels.length ? (
            <>
              <div className="card-grid kpi-grid">
                <div className="card"><h3>{t('modelInfo.dstModelCount')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.model_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.byTargetModelAgg')}</div></div>
                <div className="card"><h3>{t('modelInfo.dstCallCount')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.total_call_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.requestTotal')}</div></div>
                <div className="card"><h3>{t('modelInfo.dstTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.tokens_all_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.inputOutput')}</div></div>
                <div className="card"><h3>{t('modelInfo.dstInputOutput')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.tokens_input_size)} / {fmt(dstSummary.tokens_output_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.tokenStructure')}</div></div>
              </div>
              <div className="card">
                <h3>{t('modelInfo.dstModelStats')}</h3>
                <DataTable
                  rowKey="model_name"
                  rows={dstModels}
                  empty={t('modelInfo.noDstData')}
                  columns={[
                    { key: 'rank', title: t('modelInfo.rank'), width: 60, render: (_, m) => dstModels.indexOf(m) + 1 },
                    { key: 'model_name', title: t('modelInfo.modelName2'), render: (v) => <b>{v || t('modelInfo.unknownModel')}</b> },
                    { key: 'call_count', title: t('modelInfo.callCount2'), render: (v, m) => <span title={t('modelInfo.callShare') + ' ' + pct(m.call_share)}>{fmt(v)}</span> },
                    { key: 'call_share', title: t('modelInfo.callShare'), render: (v) => <b style={{ color: '#059669' }}>{pct(v)}</b> },
                    { key: 'tokens_all_size', title: t('modelInfo.totalTokens2'), render: (v, m) => <b title={t('modelInfo.tokenShare') + ' ' + pct(m.token_share)}>{fmt(v)}</b> },
                    { key: 'token_share', title: t('modelInfo.tokenShare'), render: (v) => <b style={{ color: '#059669' }}>{pct(v)}</b> },
                  ]}
                />
              </div>
            </>
          ) : null}

          {!isAdmin && myModels && myModels.length ? (
            <div className="card">
              <h3>{t('modelInfo.myModelList')}</h3>
              <DataTable
                rowKey="model_name"
                rows={myModels}
                empty={t('modelInfo.noModelInfo')}
                columns={[
                  { key: 'model_name', title: t('modelInfo.modelName2'), render: (v) => <b>{v || '-'}</b> },
                  { key: 'description', title: t('modelInfo.description2'), render: (v) => v || '-' },
                  { key: 'cost', title: t('modelInfo.cost'), render: (_, m) => `${Number(m.cost_per_100w_input || 0).toFixed(2)} / ${Number(m.cost_per_100w_output || 0).toFixed(2)}` },
                  { key: 'max_context_length', title: t('modelInfo.capability'), render: (v) => (v ? fmt(v) + ' Tokens' : '-') },
                  { key: 'perf', title: t('modelInfo.perfLabel'), render: (_, m) => (
                    <span style={{ fontSize: 12 }}>
                      <span style={{ color: m.success_rate >= 99 ? '#059669' : '#d97706' }}>{t('modelInfo.successRate2')} {pct(m.success_rate)}</span>
                      {' · '}{t('modelInfo.ttfb')} {fmt(m.avg_ttfb_ms)}ms · {t('modelInfo.speed')} {fmt(m.tokens_per_second)} {t('modelInfo.perSecond')}
                      {Number(m.error_429_rate) > 0 ? ` · 429 ${pct(m.error_429_rate)}` : ''}
                      {Number(m.error_5xx_rate) > 0 ? ` · 5xx ${pct(m.error_5xx_rate)}` : ''}
                    </span>
                  ) },
                  { key: 'endpoint_count', title: t('modelInfo.sourceCount'), render: fmt },
                  { key: 'call_count', title: t('modelInfo.callCount2'), render: fmt },
                  { key: 'tokens_all_size', title: t('modelInfo.totalTokens2'), render: fmt },
                ]}
              />
            </div>
          ) : null}
          <div className="pager" style={{ justifyContent: 'flex-start', fontSize: 12, color: '#94a3b8' }}>{t('modelInfo.tokenMaxScale', { max: fmt(tokenMax), callMax: fmt(callMax) })}</div>
        </>
      ) : null}
    </div>
  )
}
