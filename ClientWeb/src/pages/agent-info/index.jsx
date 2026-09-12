// 阶段BV：AgentInfo 页面主组件
// 模块化拆分：toolbar + 主组件。
// 后端 AgentInfoInterface action=stats/trend 支持可选 user_name+model_name 参数，
// 同时指定时按单用户单模型视角聚合；否则走全站聚合（admin）或本人全模型聚合（user）。
import { useCallback, useEffect, useRef, useState } from 'react'
import { post } from '../../shared/api'
import { isAdminRole } from '../../shared/auth'
import { useUserModelOptions, useMyModelNames } from '../../shared/userModelOptions'
import DataTable from '../../components/DataTable'
import HourlyTrendPanel from '../../components/HourlyTrendPanel'
import useStatsPageFilters from '../../shared/useStatsPageFilters'
import PageHeader from '../../components/PageHeader'
import AgentInfoToolbar from './AgentInfoToolbar'
import { useI18n } from '../../i18n'

function fmt(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}
function pct(v) {
  v = Number(v) || 0
  return Math.min(Math.max(v, 0), 100).toFixed(2) + '%'
}

export default function AgentInfo(props) {
  const { t } = useI18n()
  const route = props && props.route
  const isAdmin = isAdminRole()
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()

  const filters = useStatsPageFilters('agent_info', route, isAdmin, 3)
  const { userName, setUserName, modelName, setModelName, days, setDays, levels, levelsLoading } = filters

  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const modelNameRef = useRef(modelName)
  useEffect(() => { modelNameRef.current = modelName }, [modelName])
  const userNameRef = useRef(userName)
  useEffect(() => { userNameRef.current = userName }, [userName])

  // 是否处于「单用户单模型」视角
  const scopedAdmin = isAdmin && userName.trim() !== '' && modelName.trim() !== ''
  const scopedUser = !isAdmin && modelName.trim() !== ''
  const scoped = scopedAdmin || scopedUser

  const loadStats = useCallback((d, u, m) => {
    const un = (u !== undefined ? u : userNameRef.current).trim()
    const mn = (m !== undefined ? m : modelNameRef.current).trim()
    setLoading(true)
    setError('')
    post('AgentInfoInterface', {
      action: 'stats',
      days: d,
      user_name: isAdmin ? un : '',
      model_name: mn,
    })
      .then((res) => setData((res && res.data) || {}))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [isAdmin])

  useEffect(() => {
    if (days === null) return
    loadStats(days)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days])

  useEffect(() => {
    if (days === null) return
    loadStats(days)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [userName, modelName])

  const summary = (data && data.summary) || {}
  const agents = (data && data.agents) || []

  const shareBars = (list, mode) => {
    if (!list.length) return <div className="table-empty">{t('agentInfo.noStatsData')}</div>
    return list.slice(0, 8).map((it) => {
      const share = Math.min(Math.max(Number(mode === 'token' ? it.token_share : it.call_share) || 0, 0), 100)
      const value = mode === 'token'
        ? t('agentInfo.tokensUnit', { count: fmt(it.tokens_all_size) })
        : t('agentInfo.callsUnit', { count: fmt(it.call_count) })
      return (
        <div key={it.agent_tool_name} style={{ padding: '12px 0', borderTop: '1px solid #f1f5f9' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <b>{it.agent_tool_name || t('agentInfo.unknown')}</b>
            <span style={{ color: '#475569', fontSize: 12 }}>{value} · {pct(share)}</span>
          </div>
          <div style={{ height: 10, background: '#e2e8f0', borderRadius: 999, overflow: 'hidden' }}>
            <div style={{
              height: '100%', width: share + '%', minWidth: 2, borderRadius: 999,
              background: mode === 'call' ? 'linear-gradient(90deg,#34d399,#059669)' : 'linear-gradient(90deg,#c084fc,#7c3aed)',
            }} />
          </div>
        </div>
      )
    })
  }

  return (
    <div className="page">
      <PageHeader icon="🧠" title={t('agentInfo.title2')}
        breadcrumb={[t('nav.modelProxy'), t('nav.agentInfo')]}
        info={[
          <span key="range">{t('agentInfo.daysRange', { days }) || `近 ${days} 天`}</span>,
          <span key="scope">{(isAdmin ? t('agentInfo.adminView') : t('agentInfo.userView')) || ''}</span>,
        ]}
        actions={<button className="btn btn-primary" disabled={loading} onClick={() => loadStats(days)}>{t('common.refresh')}</button>}
      />
      <AgentInfoToolbar
        isAdmin={isAdmin}
        userName={userName} setUserName={setUserName}
        modelName={modelName} setModelName={setModelName}
        days={days} setDays={setDays}
        levels={levels} levelsLoading={levelsLoading}
        onQuery={() => loadStats(days)} loading={loading}
        userOptions={userOptions} myModelNames={myModelNames}
      />
      {error ? <div className="alert alert-error">{t('agentInfo.loadFailed', { error })}</div> : null}
      {loading ? <div className="table-loading">{t('agentInfo.loading')}</div> : !agents.length && !error ? <div className="table-empty">{t('agentInfo.noAgentData')}</div> : null}

      {agents.length ? (
        <>
          <div className="card-grid kpi-grid">
            <div className="card"><h3>{t('agentInfo.statAgentCount')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.agent_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('agentInfo.byAgentName')}</div></div>
            <div className="card"><h3>{t('agentInfo.totalCallCount')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_call_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('agentInfo.requestTotal')}</div></div>
            <div className="card"><h3>{t('agentInfo.totalTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.tokens_all_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('agentInfo.inputOutput')}</div></div>
            <div className="card"><h3>{t('agentInfo.inputOutputTokens')}</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.tokens_input_size)} / {fmt(summary.tokens_output_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>{t('agentInfo.tokenStructure')}</div></div>
          </div>

          {agents.length ? (
            <div className="card">
              <h3>{t('agentInfo.hourlyTrend')}</h3>
              <HourlyTrendPanel
                api="AgentInfoInterface"
                span={days}
                labels={{
                  loading: t('agentInfo.trendLoading'),
                  empty: t('agentInfo.trendEmpty'),
                  call: t('agentInfo.trendCallSeries'),
                  token: t('agentInfo.trendTokenSeries'),
                  tooltip: t('agentInfo.trendTooltip'),
                  zoomHint: t('agentInfo.trendZoomHint'),
                  reset: t('agentInfo.trendReset'),
                  truncated: t('agentInfo.trendTruncated'),
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
              <h3>{t('agentInfo.agentTokenUsage')}</h3>
              <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>{t('agentInfo.rankByTotalTokens')}</div>
              {shareBars(agents.slice().sort((a, b) => (b.tokens_all_size || 0) - (a.tokens_all_size || 0)), 'token')}
            </div>
            <div className="card">
              <h3>{t('agentInfo.agentCallCount')}</h3>
              <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>{t('agentInfo.rankByCallCount')}</div>
              {shareBars(agents.slice().sort((a, b) => (b.call_count || 0) - (a.call_count || 0)), 'call')}
            </div>
          </div>

          <div className="card">
            <h3>{t('agentInfo.agentDetail', { view: scoped ? t('agentInfo.userModelScope') : (isAdmin ? t('agentInfo.adminView') : t('agentInfo.userView')) })}</h3>
            <DataTable
              rowKey="agent_tool_name"
              rows={agents}
              columns={[
                { key: 'rank', title: t('agentInfo.rank'), width: 60, render: (_, a) => agents.indexOf(a) + 1 },
                { key: 'agent_tool_name', title: t('agentInfo.agentName2'), render: (v) => <b>{v || t('agentInfo.unknown')}</b> },
                { key: 'call_count', title: t('agentInfo.callCount2'), render: (v, a) => <span title={t('agentInfo.callShare') + ' ' + pct(a.call_share)}>{fmt(v)}</span> },
                { key: 'call_share', title: t('agentInfo.callShare'), render: (v) => <b style={{ color: '#7c3aed' }}>{pct(v)}</b> },
                { key: 'tokens_input_size', title: t('agentInfo.inputTokens'), render: fmt },
                { key: 'tokens_output_size', title: t('agentInfo.outputTokens'), render: fmt },
                { key: 'tokens_all_size', title: t('agentInfo.totalTokens2'), render: (v, a) => <b title={t('agentInfo.tokenShare') + ' ' + pct(a.token_share)}>{fmt(v)}</b> },
                { key: 'token_share', title: t('agentInfo.tokenShare'), render: (v) => <b style={{ color: '#7c3aed' }}>{pct(v)}</b> },
                ...(isAdmin && !scoped ? [{ key: 'user_count', title: t('agentInfo.activeUsers'), render: fmt }] : []),
              ]}
            />
          </div>
        </>
      ) : null}
    </div>
  )
}
