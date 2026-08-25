import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'

// 模型信息（统计页）：ModelInfoInterface（POST JSON {action:'stats', days}）
// 用户端（29001）stats 额外返回 dst_summary/dst_models（目标源站模型统计）；
// action=list 返回"我的模型信息列表"（成本/能力/动态性能标签/源站数/调用统计）。
// 无第三方图表库：用纯 div 进度条 / 表格替代 echarts

const DAYS_OPTIONS = [1, 3, 5, 7, 14, 30, 60, 90, 0]
const daysLabel = (d) => (d === 0 ? '全部时间' : '最近' + d + '天')

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
  const q = props?.route?.query
  const isAdmin = isAdminRole()
  // 记忆 key 按角色隔离（用户端与管理端不复用同一天数偏好）
  const storageKey = `lsm:modelInfo:days:v1:${isAdmin ? 'admin:__all__' : 'user'}`
  const [days, setDays] = useState(() => normalizeDays(q?.get('days') || localStorage.getItem(storageKey)))
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [myModels, setMyModels] = useState(null) // 用户端"我的模型信息列表"（action=list）

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
  const trend = isAdmin ? ((data && data.trend) || []) : []
  const trendMax = Math.max(1, ...trend.map((s) => s.count || 0))
  const tokenMax = Math.max(1, ...models.map((m) => m.tokens_all_size || 0))
  const callMax = Math.max(1, ...models.map((m) => m.call_count || 0))

  const shareBars = (list, mode) => {
    if (!list.length) return <div className="table-empty">暂无统计数据</div>
    return list.slice(0, 8).map((it) => {
      const share = Math.min(Math.max(Number(mode === 'token' ? it.token_share : it.call_share) || 0, 0), 100)
      const value = mode === 'token' ? fmt(it.tokens_all_size) + ' Tokens' : fmt(it.call_count) + ' 次'
      return (
        <div key={it.model_name} style={{ padding: '12px 0', borderTop: '1px solid #f1f5f9' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <b>{it.model_name || '未知模型'}</b>
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
      <h2 className="page-title">模型信息</h2>
      <div className="toolbar">
        <span>时间跨度</span>
        <select value={days} onChange={(e) => setDays(normalizeDays(e.target.value))}>
          {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{daysLabel(d)}</option>)}
        </select>
        <button className="btn btn-primary" disabled={loading} onClick={() => loadStats(days)}>{loading ? '加载中…' : '刷新'}</button>
        <span style={{ color: '#888', fontSize: 13 }}>{isAdmin ? '按全站真实模型维度统计 Token 与调用量' : '按本人模型维度统计 Token 与调用量'}</span>
      </div>
      {error ? <div className="alert alert-error">加载失败：{error}</div> : null}
      {loading ? <div className="table-loading">加载中…</div> : !models.length && !error ? <div className="table-empty">暂无模型调用数据。系统产生请求后将自动展示模型统计。</div> : null}

      {models.length || dstModels.length || (myModels && myModels.length) ? (
        <>
          <div className="card-grid kpi-grid">
            <div className="card"><h3>统计模型数</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.model_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>按目标模型聚合</div></div>
            <div className="card"><h3>全站调用次数</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.total_call_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>请求记录总量</div></div>
            <div className="card"><h3>全站 Tokens</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.tokens_all_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>输入 + 输出</div></div>
            <div className="card"><h3>输入 / 输出 Tokens</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(summary.tokens_input_size)} / {fmt(summary.tokens_output_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>Token 结构</div></div>
          </div>

          {trend.length ? (
            <div className="card">
              <h3>调用次数趋势</h3>
              <div style={{ display: 'flex', alignItems: 'flex-end', gap: 6, height: 160 }}>
                {trend.map((s) => (
                  <div key={s.date} title={`${s.date}：${fmt(s.count)} 次 / ${fmt(s.tokens_total)} Tokens`} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 4, height: '100%', justifyContent: 'flex-end' }}>
                    <div style={{ width: '70%', height: Math.max(2, ((s.count || 0) / trendMax) * 130), background: 'linear-gradient(180deg,#38bdf8,#2563eb)', borderRadius: 4 }} />
                    <span style={{ fontSize: 10, color: '#888', whiteSpace: 'nowrap' }}>{(s.date || '').substring(5)}</span>
                  </div>
                ))}
              </div>
            </div>
          ) : null}

          <div className="card-grid">
            <div className="card">
              <h3>模型 Token 使用统计</h3>
              <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>按总 Tokens 排名</div>
              {shareBars(models.slice().sort((a, b) => (b.tokens_all_size || 0) - (a.tokens_all_size || 0)), 'token')}
            </div>
            <div className="card">
              <h3>模型调用次数统计</h3>
              <div style={{ fontSize: 12, color: '#64748b', marginBottom: 8 }}>按调用次数排名</div>
              {shareBars(models.slice().sort((a, b) => (b.call_count || 0) - (a.call_count || 0)), 'call')}
            </div>
          </div>

          <div className="card">
            <h3>模型统计明细（{isAdmin ? '管理员全站视角' : '我的模型视角'}）</h3>
            <DataTable
              rowKey="model_name"
              rows={models}
              columns={[
                { key: 'rank', title: '排名', width: 60, render: (_, m) => models.indexOf(m) + 1 },
                { key: 'model_name', title: '模型名', render: (v) => <b>{v || '未知模型'}</b> },
                { key: 'call_count', title: '调用次数', render: (v, m) => <span title={'占比 ' + pct(m.call_share)}>{fmt(v)}</span> },
                { key: 'call_share', title: '调用占比', render: (v) => <b style={{ color: '#2563eb' }}>{pct(v)}</b> },
                { key: 'tokens_input_size', title: '输入 Tokens', render: fmt },
                { key: 'tokens_output_size', title: '输出 Tokens', render: fmt },
                { key: 'tokens_all_size', title: '总 Tokens', render: (v, m) => <b title={'占比 ' + pct(m.token_share)}>{fmt(v)}</b> },
                { key: 'token_share', title: 'Token 占比', render: (v) => <b style={{ color: '#2563eb' }}>{pct(v)}</b> },
                ...(isAdmin ? [{ key: 'user_count', title: '活跃用户', render: fmt }] : []),
              ]}
            />
          </div>

          {!isAdmin && dstModels.length ? (
            <>
              <div className="card-grid kpi-grid">
                <div className="card"><h3>目标模型数</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.model_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>按目标源站模型聚合</div></div>
                <div className="card"><h3>目标调用次数</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.total_call_count)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>请求记录总量</div></div>
                <div className="card"><h3>目标 Tokens</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.tokens_all_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>输入 + 输出</div></div>
                <div className="card"><h3>目标输入 / 输出</h3><div style={{ fontSize: 24, fontWeight: 800 }}>{fmt(dstSummary.tokens_input_size)} / {fmt(dstSummary.tokens_output_size)}</div><div style={{ fontSize: 12, color: '#94a3b8' }}>Token 结构</div></div>
              </div>
              <div className="card">
                <h3>目标源站模型统计（我的平台模型 → 目标模型转发分布）</h3>
                <DataTable
                  rowKey="model_name"
                  rows={dstModels}
                  empty="暂无目标模型数据"
                  columns={[
                    { key: 'rank', title: '排名', width: 60, render: (_, m) => dstModels.indexOf(m) + 1 },
                    { key: 'model_name', title: '目标模型名', render: (v) => <b>{v || '未知模型'}</b> },
                    { key: 'call_count', title: '调用次数', render: (v, m) => <span title={'占比 ' + pct(m.call_share)}>{fmt(v)}</span> },
                    { key: 'call_share', title: '调用占比', render: (v) => <b style={{ color: '#059669' }}>{pct(v)}</b> },
                    { key: 'tokens_all_size', title: '总 Tokens', render: (v, m) => <b title={'占比 ' + pct(m.token_share)}>{fmt(v)}</b> },
                    { key: 'token_share', title: 'Token 占比', render: (v) => <b style={{ color: '#059669' }}>{pct(v)}</b> },
                  ]}
                />
              </div>
            </>
          ) : null}

          {!isAdmin && myModels && myModels.length ? (
            <div className="card">
              <h3>我的模型信息列表（成本 / 能力 / 动态性能标签）</h3>
              <DataTable
                rowKey="model_name"
                rows={myModels}
                empty="暂无模型信息"
                columns={[
                  { key: 'model_name', title: '模型名', render: (v) => <b>{v || '-'}</b> },
                  { key: 'description', title: '描述', render: (v) => v || '-' },
                  { key: 'cost', title: '成本（元/100万 Tokens 输入/输出）', render: (_, m) => `${Number(m.cost_per_100w_input || 0).toFixed(2)} / ${Number(m.cost_per_100w_output || 0).toFixed(2)}` },
                  { key: 'max_context_length', title: '能力（上下文）', render: (v) => (v ? fmt(v) + ' Tokens' : '-') },
                  { key: 'perf', title: '动态性能标签', render: (_, m) => (
                    <span style={{ fontSize: 12 }}>
                      <span style={{ color: m.success_rate >= 99 ? '#059669' : '#d97706' }}>成功率 {pct(m.success_rate)}</span>
                      {' · '}TTFB {fmt(m.avg_ttfb_ms)}ms · 速度 {fmt(m.tokens_per_second)} tok/s
                      {Number(m.error_429_rate) > 0 ? ` · 429 ${pct(m.error_429_rate)}` : ''}
                      {Number(m.error_5xx_rate) > 0 ? ` · 5xx ${pct(m.error_5xx_rate)}` : ''}
                    </span>
                  ) },
                  { key: 'endpoint_count', title: '源站数', render: fmt },
                  { key: 'call_count', title: '调用次数', render: fmt },
                  { key: 'tokens_all_size', title: '总 Tokens', render: fmt },
                ]}
              />
            </div>
          ) : null}
          <div className="pager" style={{ justifyContent: 'flex-start', fontSize: 12, color: '#94a3b8' }}>Token 最大值标尺：{fmt(tokenMax)} · 调用最大值标尺：{fmt(callMax)}</div>
        </>
      ) : null}
    </div>
  )
}
