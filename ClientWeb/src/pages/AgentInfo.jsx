import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import HourlyTrendPanel from '../components/HourlyTrendPanel'
import { useI18n } from '../i18n'

// Agent 信息（统计页）：AgentInfoInterface
//   - action='stats' 返回 summary/agents/trend（管理员端）
//   - action='trend'（新增）小时级 K 线图：调用次数 + Tokens 数
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

export default function AgentInfo(props) {
  const { t } = useI18n()
  const q = props?.route?.query
  const isAdmin = isAdminRole()
  const storageKey = `lsm:agentInfo:days:v1:${isAdmin ? 'admin:__all__' : 'user'}`
  const [days, setDays] = useState(() => normalizeDays(q?.get('days') || localStorage.getItem(storageKey)))
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const daysLabel = (d) => (d === 0 ? t('agentInfo.daysAll', { days: d }) : t('agentInfo.daysDay', { days: d }))

  const loadStats = useCallback((d) => {
    setLoading(true)
    setError('')
    post('AgentInfoInterface', { action: 'stats', days: d })
      .then((res) => setData((res && res.data) || {}))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    localStorage.setItem(storageKey, String(days))
    loadStats(days)
  }, [days, loadStats])

  const summary = (data && data.summary) || {}
  const agents = (data && data.agents) || []

  const shareBars = (list, mode) => {
    if (!list.length) return <div className="table-empty">{t('agentInfo.noStatsData')}</div>
    return list.slice(0, 8).map((it) => {
      const share = Math.min(Math.max(Number(mode === 'token' ? it.token_share : it.call_share) || 0, 0), 100)
      const value = mode === 'token' ? t('agentInfo.tokensUnit', { count: fmt(it.tokens_all_size) }) : t('agentInfo.callsUnit', { count: fmt(it.call_count) })
      return (
        <div key={it.agent_tool_name} style={{ padding: '12px 0', borderTop: '1px solid #f1f5f9' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <b>{it.agent_tool_name || t('agentInfo.unknown')}</b>
            <span style={{ color: '#475569', fontSize: 12 }}>{value} · {pct(share)}</span>
          </div>
          <div style={{ height: 10, background: '#e2e8f0', borderRadius: 999, overflow: 'hidden' }}>
            <div style={{ height: '100%', width: share + '%', minWidth: 2, borderRadius: 999, background: mode === 'call' ? 'linear-gradient(90deg,#34d399,#059669)' : 'linear-gradient(90deg,#c084fc,#7c3aed)' }} />
          </div>
        </div>
      )
    })
  }

  return (
    <div className="page">
      <h2 className="page-title">{t('agentInfo.title2')}</h2>
      <div className="toolbar">
        <span>{t('agentInfo.timeSpan')}</span>
        <select value={days} onChange={(e) => setDays(normalizeDays(e.target.value))}>
          {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{daysLabel(d)}</option>)}
        </select>
        <button className="btn btn-primary" disabled={loading} onClick={() => loadStats(days)}>{loading ? t('agentInfo.loading') : t('agentInfo.refresh')}</button>
        <span style={{ color: '#888', fontSize: 13 }}>{t('agentInfo.agentStats')}</span>
      </div>
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
                storageKey={`lsm:agentInfo:trend:v1:${isAdmin ? 'admin' : 'user'}`}
                labels={{
                  '1d': t('agentInfo.trendWindow1d'),
                  '3d': t('agentInfo.trendWindow3d'),
                  '7d': t('agentInfo.trendWindow7d'),
                  '30d': t('agentInfo.trendWindow30d'),
                  loading: t('agentInfo.trendLoading'),
                  empty: t('agentInfo.trendEmpty'),
                  call: t('agentInfo.trendCallSeries'),
                  token: t('agentInfo.trendTokenSeries'),
                  tooltip: t('agentInfo.trendTooltip'),
                }}
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
            <h3>{t('agentInfo.agentDetail', { view: isAdmin ? t('agentInfo.adminView') : t('agentInfo.userView') })}</h3>
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
                ...(isAdmin ? [{ key: 'user_count', title: t('agentInfo.activeUsers'), render: fmt }] : []),
              ]}
            />
          </div>
        </>
      ) : null}
    </div>
  )
}
