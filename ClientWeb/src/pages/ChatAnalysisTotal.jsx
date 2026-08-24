import { useEffect, useRef, useState } from 'react'
import { openWs, post } from '../shared/api'
import DataTable from '../components/DataTable'
import { fmtNum, fmtMs, pickRouteQuery } from '../shared/format'

// 汇总统计页（/ChatAnalysisTotalWS WebSocket 流式分块推送）
// 协议：连接后首条消息 {type:'query',days,model_name,request_id(12hex)}；
//       服务端按 7 个 stage 串行推 chunk 快照，结束推 done；错误推 error 帧。
// WS 不可用（网关不支持 Upgrade）时 fallback 到 action=full_http 的 HTTP 接口。
const DAYS_OPTIONS = [0, 1, 3, 5, 7, 14, 30, 60, 90]

// 生成 12 位小写十六进制 request_id（服务端 sanitizeRequestID 强校验）
function genRequestId() {
  let s = ''
  for (let i = 0; i < 12; i++) s += Math.floor(Math.random() * 16).toString(16)
  return s
}

// stage 名称 → 中文名（渲染顺序与后端 wsChatStatsStageOrder 一致）
const STAGE_NAMES = {
  kpi: 'KPI 概览', time_stats: '时间分布', tokens_summary: 'Tokens 概览',
  model_distribution: '源站模型分布', trend_chart: '用量趋势', protocol_stats: '协议分析', agent_stats: 'Agent 工具',
}

export default function ChatAnalysisTotal({ route }) {
  const init = pickRouteQuery(route && route.query)
  const [userName, setUserName] = useState(init.userName)
  const [modelName, setModelName] = useState(init.modelName)
  const [days, setDays] = useState(7)
  // 各 stage 累计快照
  const [stages, setStages] = useState({})
  const [running, setRunning] = useState(false)
  const [progress, setProgress] = useState('') // 进度提示（已扫描行数）
  const [error, setError] = useState('')
  const [doneInfo, setDoneInfo] = useState(null) // done 帧信息
  const wsRef = useRef(null)
  const reqIdRef = useRef('')

  const applyStage = (stage, data) => setStages((s) => ({ ...s, [stage]: data }))

  // 停止当前查询：发 cancel 并关闭连接
  const stopQuery = () => {
    const ws = wsRef.current
    if (ws) {
      try { ws.readyState === 1 && ws.send(JSON.stringify({ type: 'cancel' })) } catch { /* 忽略 */ }
      try { ws.close() } catch { /* 忽略 */ }
      wsRef.current = null
    }
  }

  // WS 查询主流程
  const runQuery = (d = days, u = userName, m = modelName) => {
    stopQuery()
    setStages({}); setError(''); setDoneInfo(null); setRunning(true); setProgress('连接中…')
    const rid = genRequestId()
    reqIdRef.current = rid
    let opened = false
    const ws = openWs('ChatAnalysisTotalWS', { user_name: u.trim(), model_name: m.trim() }, {
      onMessage: (obj) => {
        // 防重复契约：丢弃旧 request_id 的帧
        if (obj.request_id && obj.request_id !== reqIdRef.current) return
        if (obj.type === 'chunk') {
          applyStage(obj.stage, obj.data)
          const kpi = obj.stage === 'kpi' ? obj.data : null
          if (kpi && kpi.scan_final) setProgress(`扫描完成（${fmtNum(kpi.scanned_rows)} 行）`)
          else if (kpi) setProgress(`扫描中… 已处理 ${fmtNum(kpi.scanned_rows)} 行`)
        } else if (obj.type === 'done') {
          setDoneInfo(obj); setRunning(false); setProgress('')
        } else if (obj.type === 'error') {
          setError(`${STAGE_NAMES[obj.stage] || obj.stage || '查询'}出错：${obj.message}`)
          setRunning(false); setProgress('')
        } else if (obj.type === 'busy') {
          setError(obj.message || '上一个查询仍在进行'); setRunning(false)
        }
      },
      onError: () => {
        if (!opened && !wsRef.current) return
        // WS 失败 → HTTP full_http fallback（数据形状与各 stage 完全对齐）
        httpFallback(d, u, m)
      },
      onClose: () => { wsRef.current = null },
    })
    wsRef.current = ws
    ws.onopen = () => {
      opened = true
      try {
        ws.send(JSON.stringify({ type: 'query', days: d, model_name: m.trim(), request_id: rid }))
      } catch (e) {
        setError('发送查询失败：' + e.message); setRunning(false)
      }
    }
  }

  // HTTP fallback：POST ChatAnalysisTotalInterface action=full_http
  const httpFallback = async (d, u, m) => {
    setProgress('WebSocket 不可用，改用 HTTP 全量拉取…')
    try {
      const resp = await post('ChatAnalysisTotalInterface', {
        user_name: u.trim(), model_name: m.trim(), days: d, action: 'full_http',
      })
      setStages({
        kpi: resp.kpi, time_stats: resp.time_stats, tokens_summary: resp.tokens_summary,
        model_distribution: resp.model_distribution, trend_chart: resp.trend_chart,
        protocol_stats: resp.protocol_stats, agent_stats: resp.agent_stats,
      })
      setDoneInfo({ elapsed_ms: null, http_fallback: true })
    } catch (e) {
      setError(e.message || '统计查询失败')
    } finally { setRunning(false); setProgress('') }
  }

  // 首次进入：若路由带了 user/model 则自动查询
  useEffect(() => {
    return () => stopQuery() // 离开页面时关闭 WS
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  useEffect(() => {
    if (init.userName && init.modelName) runQuery(days, init.userName, init.modelName)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const kpi = stages.kpi
  const ts = stages.tokens_summary
  const timeStats = stages.time_stats || []
  const modelDist = stages.model_distribution || []
  const trend = stages.trend_chart || []
  const proto = stages.protocol_stats
  const agent = stages.agent_stats

  const kvTable = (obj, keys) => (
    <dl className="kv">
      {keys.map(([k, label, fmt]) => (
        <div key={k} style={{ display: 'contents' }}>
          <dt>{label}</dt><dd>{obj ? (fmt ? fmt(obj[k]) : fmtNum(obj[k])) : '-'}</dd>
        </div>
      ))}
    </dl>
  )

  return (
    <div className="page">
      <h2 className="page-title">对话汇总统计</h2>

      <div className="toolbar">
        <label>用户名 <input value={userName} onChange={(e) => setUserName(e.target.value)} placeholder="user_name（可留空=全站）" style={{ width: 160 }} /></label>
        <label>模型名 <input value={modelName} onChange={(e) => setModelName(e.target.value)} placeholder="model_name（可留空）" style={{ width: 160 }} /></label>
        <label>时间跨度
          <select value={days} onChange={(e) => { const d = Number(e.target.value); setDays(d); setStages({}); setDoneInfo(null); if (!running) runQuery(d) }}>
            {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{d === 0 ? '全部时间' : `最近 ${d} 天`}</option>)}
          </select>
        </label>
        <button className="btn btn-primary" onClick={() => runQuery()} disabled={running}>查询统计</button>
        {running ? <button className="btn" onClick={() => { stopQuery(); setRunning(false); setProgress('') }}>取消</button> : null}
        {running || progress ? <span style={{ color: 'var(--muted)', fontSize: 13 }}>{running ? (progress || '统计中…') : progress}</span> : null}
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}
      {doneInfo && doneInfo.timed_out ? (
        <div className="alert alert-error">查询超时：{String(doneInfo.warnings || []).join('; ')}</div>
      ) : null}
      {doneInfo && !doneInfo.timed_out && doneInfo.elapsed_ms != null ? (
        <div className="alert alert-ok">统计完成，耗时 {fmtMs(doneInfo.elapsed_ms)}</div>
      ) : null}
      {doneInfo && doneInfo.http_fallback ? (
        <div className="alert alert-ok">统计完成（HTTP 全量模式）</div>
      ) : null}

      {/* 维度 1：KPI 卡片 */}
      <div className="card-grid kpi-grid">
        <div className="card">
          <h3>总请求数</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.total_calls) : '-'}</div>
        </div>
        <div className="card">
          <h3>总 Tokens</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.total_tokens) : '-'}</div>
        </div>
        <div className="card">
          <h3>活跃源站模型</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.active_models) : '-'}</div>
        </div>
        <div className="card">
          <h3>活跃天数</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.active_days) : '-'}</div>
        </div>
      </div>

      {/* 维度 3：Tokens 概览 */}
      <div className="card">
        <h3>Tokens 概览</h3>
        {ts ? (
          <>
            <dl className="kv">
              <dt>调用次数</dt><dd>{fmtNum(ts.total_count)}</dd>
              <dt>输入 Tokens</dt><dd>{fmtNum(ts.total_input)}</dd>
              <dt>输出 Tokens</dt><dd>{fmtNum(ts.total_output)}</dd>
              <dt>总 Tokens</dt><dd>{fmtNum(ts.total_tokens)}</dd>
              <dt>输入/输出比</dt><dd>{ts.total_output ? (ts.total_input / ts.total_output).toFixed(2) : '-'}</dd>
            </dl>
            <DataTable
              columns={[
                { key: 'date', title: '日期' },
                { key: 'count', title: '次数', render: fmtNum },
                { key: 'tokens_input', title: '输入', render: fmtNum },
                { key: 'tokens_output', title: '输出', render: fmtNum },
                { key: 'tokens_total', title: '合计', render: fmtNum },
                { key: 'avg_elapsed_ms', title: '平均耗时', render: fmtMs },
              ]}
              rows={ts.buckets || []} empty="暂无数据" />
          </>
        ) : <div className="table-empty">等待数据…</div>}
      </div>

      {/* 维度 4：源站模型分布 */}
      <div className="card">
        <h3>源站模型分布（dst_model_name）</h3>
        <DataTable
          columns={[
            { key: 'model_name', title: '源站模型' },
            { key: 'call_count', title: '调用次数', render: fmtNum },
            { key: 'call_share', title: '调用占比', render: (v) => (v != null ? (v * 100).toFixed(2) + '%' : '-') },
            { key: 'tokens_input', title: '输入Tok', render: fmtNum },
            { key: 'tokens_output', title: '输出Tok', render: fmtNum },
            { key: 'tokens_total', title: '总Tok', render: fmtNum },
            { key: 'token_share', title: 'Tok占比', render: (v) => (v != null ? (v * 100).toFixed(2) + '%' : '-') },
            { key: 'dst_endpoint_count', title: '源站数', render: (v) => fmtNum(v) },
            { key: 'top_dst_endpoints', title: 'Top 源站', render: (v) => (v || []).map((e) => `#${e.dst_endpoint_id}(${fmtNum(e.call_count)})`).join('、') || '-' },
          ]}
          rows={modelDist} empty="等待数据…" />
      </div>

      {/* 维度 2：时间分布 */}
      <div className="card">
        <h3>时间分布</h3>
        <DataTable
          columns={[
            { key: 'date', title: '时间' },
            { key: 'count', title: '调用次数', render: (v, r) => (
              <span title={fmtNum(v)}>{v > 0 ? '█'.repeat(Math.min(30, Math.max(1, Math.round(v / Math.max(1, Math.max(...timeStats.map((x) => x.count))) * 30)))) + ' ' + fmtNum(v) : fmtNum(v)}</span>
            ) },
          ]}
          rows={timeStats} empty="等待数据…" />
      </div>

      {/* 维度 5：用量趋势 */}
      <div className="card">
        <h3>用量趋势（按天）</h3>
        <DataTable
          columns={[
            { key: 'date', title: '日期' },
            { key: 'count', title: '次数', render: fmtNum },
            { key: 'tokens_input', title: '输入Tok', render: fmtNum },
            { key: 'tokens_output', title: '输出Tok', render: fmtNum },
            { key: 'tokens_total', title: '总Tok', render: fmtNum },
          ]}
          rows={trend} empty="等待数据…" />
      </div>

      {/* 维度 6：协议分析 */}
      <div className="card">
        <h3>协议分析</h3>
        {proto ? (
          <>
            {kvTable(proto, [
              ['avg_elapsed_ms', '平均耗时', fmtMs], ['min_elapsed_ms', '最小耗时', fmtMs],
              ['max_elapsed_ms', '最大耗时', fmtMs], ['avg_req_size', '平均请求大小', fmtNum],
              ['avg_resp_size', '平均响应大小', fmtNum], ['stream_count', '流式次数', fmtNum],
              ['non_stream_count', '非流式次数', fmtNum], ['has_system_prompt', '含系统提示词', fmtNum],
              ['has_tool_call', '含工具调用', fmtNum], ['multi_turn_count', '多轮对话', fmtNum],
              ['single_turn_count', '单轮对话', fmtNum], ['sample_count', '取样条数', fmtNum],
            ])}
            <h3 style={{ marginTop: 12 }}>方法分布</h3>
            <DataTable columns={[{ key: 'k', title: '方法' }, { key: 'v', title: '次数', render: fmtNum }]}
                       rows={Object.entries(proto.method_stats || {}).map(([k, v]) => ({ k, v }))} empty="暂无数据" />
            <h3 style={{ marginTop: 12 }}>URL 模式分布</h3>
            <DataTable columns={[{ key: 'k', title: 'URL 模式' }, { key: 'v', title: '次数', render: fmtNum }]}
                       rows={Object.entries(proto.url_pattern_stats || {}).map(([k, v]) => ({ k, v }))} empty="暂无数据" />
            <h3 style={{ marginTop: 12 }}>状态码分布</h3>
            <DataTable columns={[{ key: 'k', title: '状态码' }, { key: 'v', title: '次数', render: fmtNum }]}
                       rows={Object.entries(proto.status_stats || {}).map(([k, v]) => ({ k, v }))} empty="暂无数据" />
          </>
        ) : <div className="table-empty">等待数据…</div>}
      </div>

      {/* 维度 7：Agent 工具统计 */}
      <div className="card">
        <h3>Agent 工具统计</h3>
        {agent ? (
          <>
            <dl className="kv">
              <dt>Agent 总调用</dt><dd>{fmtNum(agent.total_agent_count)}</dd>
              <dt>工具数</dt><dd>{fmtNum(agent.unique_tools)}</dd>
            </dl>
            <DataTable
              columns={[
                { key: 'agent_tool_name', title: '工具名称' },
                { key: 'count', title: '调用次数', render: fmtNum },
                { key: 'percentage', title: '占比', render: (v) => (v * 100).toFixed(2) + '%' },
                { key: 'first_seen_at', title: '首次出现' },
                { key: 'last_seen_at', title: '最近出现' },
              ]}
              rows={agent.tool_stats || []} empty="暂无数据" />
          </>
        ) : <div className="table-empty">等待数据…</div>}
      </div>
    </div>
  )
}
