// 阶段BV：ModelInfo 页面主组件
// 模块化拆分：toolbar（用户名/模型名/时间档位）+ 主组件（KPI/趋势/明细表）。
// 后端 ModelInfoInterface action=stats/trend 支持可选 user_name+model_name 参数，
// 同时指定时按单用户单模型视角聚合；否则走全站聚合（admin）或本人全模型聚合（user）。
import { useCallback, useEffect, useRef, useState } from 'react'
import { post } from '../../shared/api'
import { isAdminRole } from '../../shared/auth'
import { useUserModelOptions, useMyModelNames } from '../../shared/userModelOptions'
import DataTable from '../../components/DataTable'
import HourlyTrendPanel from '../../components/HourlyTrendPanel'
import useStatsPageFilters from '../../shared/useStatsPageFilters'
import ModelInfoToolbar from './ModelInfoToolbar'
import { useI18n } from '../../i18n'

function fmt(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}
function pct(v) {
  v = Number(v) || 0
  return Math.min(Math.max(v, 0), 100).toFixed(2) + '%'
}

export default function ModelInfo(props) {
  const { t } = useI18n()
  const route = props && props.route
  const isAdmin = isAdminRole()
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()

  // 筛选 + 记忆
  const filters = useStatsPageFilters('model_info', route, isAdmin, 3)
  const { userName, setUserName, modelName, setModelName, days, setDays, levels, levelsLoading } = filters

  // 数据
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [myModels, setMyModels] = useState(null) // 用户端「我的模型信息列表」

  // modelName ref 跟踪最新值（避免 useEffect 闭包陷阱，与 chat-analysis 一致）
  const modelNameRef = useRef(modelName)
  useEffect(() => { modelNameRef.current = modelName }, [modelName])
  const userNameRef = useRef(userName)
  useEffect(() => { userNameRef.current = userName }, [userName])

  // 是否处于「单用户单模型」视角（admin 端：user+model 同时指定；user 端：model 指定）
  const scopedAdmin = isAdmin && userName.trim() !== '' && modelName.trim() !== ''
  const scopedUser = !isAdmin && modelName.trim() !== ''
  const scoped = scopedAdmin || scopedUser

  const loadStats = useCallback((d, u, m) => {
    const un = (u !== undefined ? u : userNameRef.current).trim()
    const mn = (m !== undefined ? m : modelNameRef.current).trim()
    setLoading(true)
    setError('')
    // 管理端：未指定 user+model → 全站；指定 → 单用户单模型
    // 用户端：未指定 model → 本人全模型；指定 → 本人单模型
    const reqUserName = isAdmin ? un : ''
    const reqModelName = mn
    post('ModelInfoInterface', {
      action: 'stats',
      days: d,
      user_name: reqUserName,
      model_name: reqModelName,
    })
      .then((res) => setData((res && res.data) || {}))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [isAdmin])

  // 档位就绪后首查（不强制 user/model 必须有，因为允许全站/全模型视角）
  useEffect(() => {
    if (days === null) return
    loadStats(days)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days])

  // user/model 变化后自动重新查询（与对话分析一致；scoped 时尤其需要）
  useEffect(() => {
    if (days === null) return
    loadStats(days)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userName, modelName])

  // 用户端「我的模型信息列表」：仅在「未限定单模型」且首次进入页面时拉一次
  useEffect(() => {
    if (days === null) return
    if (isAdmin || scopedUser) return // 单模型视角下 dst_summary 与 models 重复，跳过
    if (myModels !== null) return
    post('ModelInfoInterface', { action: 'list' })
      .then((d) => setMyModels((d && d.data) || []))
      .catch(() => setMyModels([]))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days, scopedUser, isAdmin])

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
      const value = mode === 'token'
        ? t('modelInfo.tokensUnit', { count: fmt(it.tokens_all_size) })
        : t('modelInfo.callsUnit', { count: fmt(it.call_count) })
      return (
        <div key={it.model_name} style={{ padding: '12px 0', borderTop: '1px solid #f1f5f9' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <b>{it.model_name || t('modelInfo.unknownModel')}</b>
            <span style={{ color: '#475569', fontSize: 12 }}>{value} · {pct(share)}</span>
          </div>
          <div style={{ height: 10, background: '#e2e8f0', borderRadius: 999, overflow: 'hidden' }}>
            <div style={{
              height: '100%', width: share + '%', minWidth: 2, borderRadius: 999,
              background: mode === 'call' ? 'linear-gradient(90deg,#34d399,#059669)' : 'linear-gradient(90deg,#38bdf8,#2563eb)',
            }} />
          </div>
        </div>
      )
    })
  }

  // 是否展示 dst_xxx 块：用户端且非单模型视角（与原有行为一致）
  const showDst = !isAdmin && !scopedUser && dstModels.length

  return (
    <div className="page">
      <h2 className="page-title">{t('modelInfo.title2')}</h2>
      <ModelInfoToolbar
        isAdmin={isAdmin}
        userName={userName} setUserName={setUserName}
        modelName={modelName} setModelName={setModelName}
        days={days} setDays={setDays}
        levels={levels} levelsLoading={levelsLoading}
        onQuery={() => loadStats(days)} loading={loading}
        userOptions={userOptions} myModelNames={myModelNames}
      />
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

          {models.length ? (
            <div className="card">
              <h3>{t('modelInfo.hourlyTrend')}</h3>
              <HourlyTrendPanel
                api="ModelInfoInterface"
                span={days}
                labels={{
                  loading: t('modelInfo.trendLoading'),
                  empty: t('modelInfo.trendEmpty'),
                  call: t('modelInfo.trendCallSeries'),
                  token: t('modelInfo.trendTokenSeries'),
                  tooltip: t('modelInfo.trendTooltip'),
                  zoomHint: t('modelInfo.trendZoomHint'),
                  reset: t('modelInfo.trendReset'),
                  truncated: t('modelInfo.trendTruncated'),
                }}
                extraParams={(() => {
                  const un = isAdmin ? userName.trim() : ''
                  const mn = modelName.trim()
                  if (un && mn) return { user_name: un, model_name: mn }
                  if (!isAdmin && mn) return { model_name: mn }
                  return {}
                })()}
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
            <h3>{t('modelInfo.modelDetail', { view: scoped ? t('modelInfo.userModelScope') : (isAdmin ? t('modelInfo.adminView') : t('modelInfo.userView')) })}</h3>
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
                ...(isAdmin && !scoped ? [{ key: 'user_count', title: t('modelInfo.activeUsers'), render: fmt }] : []),
              ]}
            />
          </div>

          {showDst ? (
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

          {!isAdmin && !scopedUser && myModels && myModels.length ? (
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
