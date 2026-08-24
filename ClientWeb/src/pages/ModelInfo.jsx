import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'

// 模型信息（统计页）：ModelInfoInterface（POST JSON {action:'stats', days}）
// 无第三方图表库：用纯 div 进度条 / 表格替代 echarts

const DAYS_OPTIONS = [1, 3, 5, 7, 14, 30, 60, 90, 0]
const daysLabel = (d) => (d === 0 ? '无限制' : d + '天')

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
  const [days, setDays] = useState(() => normalizeDays(q?.get('days') || localStorage.getItem('lsm:modelInfo:days:v1:admin:__all__')))
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')

  const loadStats = useCallback((d) => {
    setLoading(true)
    setError('')
    post('ModelInfoInterface', { action: 'stats', days: d })
      .then((res) => setData((res && res.data) || {}))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => {
    localStorage.setItem('lsm:modelInfo:days:v1:admin:__all__', String(days))
    loadStats(days)
  }, [days, loadStats])

  const summary = (data && data.summary) || {}
  const models = (data && data.models) || []
  const trend = (data && data.trend) || []
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
        <button className="btn btn-primary" disabled={loading} onClick={() => loadStats(days)}>{loading ? '刷新中…' : '刷新统计'}</button>
        <span style={{ color: '#888', fontSize: 13 }}>按全站真实模型维度统计 Token 与调用量</span>
      </div>
      {error ? <div className="alert alert-error">加载失败：{error}</div> : null}
      {loading ? <div className="table-loading">正在加载模型统计…</div> : !models.length && !error ? <div className="table-empty">暂无模型调用数据。系统产生请求后将自动展示模型统计。</div> : null}

      {models.length ? (
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
            <h3>模型统计明细（管理员全站视角）</h3>
            <DataTable
              rowKey="model_name"
              rows={models}
              columns={[
                { key: 'rank', title: '排名', width: 60, render: (_, m) => models.indexOf(m) + 1 },
                { key: 'model_name', title: '模型名称', render: (v) => <b>{v || '未知模型'}</b> },
                { key: 'call_count', title: '调用次数', render: (v, m) => <span title={'占比 ' + pct(m.call_share)}>{fmt(v)}</span> },
                { key: 'call_share', title: '调用占比', render: (v) => <b style={{ color: '#2563eb' }}>{pct(v)}</b> },
                { key: 'tokens_input_size', title: '输入 Tokens', render: fmt },
                { key: 'tokens_output_size', title: '输出 Tokens', render: fmt },
                { key: 'tokens_all_size', title: '总 Tokens', render: (v, m) => <b title={'占比 ' + pct(m.token_share)}>{fmt(v)}</b> },
                { key: 'token_share', title: 'Token 占比', render: (v) => <b style={{ color: '#2563eb' }}>{pct(v)}</b> },
                { key: 'user_count', title: '活跃用户', render: fmt },
              ]}
            />
          </div>
          <div className="pager" style={{ justifyContent: 'flex-start', fontSize: 12, color: '#94a3b8' }}>Token 最大值标尺：{fmt(tokenMax)} · 调用最大值标尺：{fmt(callMax)}</div>
        </>
      ) : null}
    </div>
  )
}
