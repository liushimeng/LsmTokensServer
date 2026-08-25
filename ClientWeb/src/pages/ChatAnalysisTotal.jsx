import { useEffect, useRef, useState } from 'react'
import { openWs, post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import { useUserModelOptions, useMyModelNames, modelNamesOf, allModelNames } from '../shared/userModelOptions'
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
  const isAdmin = isAdminRole() // 用户端：服务端强制本人数据，隐藏用户名输入
  const [userName, setUserName] = useState(isAdmin ? init.userName : '')
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

  // ===== 区间报告（v2.0.46 对齐：POST ChatAnalysisTotalRangeInterface?stream=1 SSE）=====
  const RANGE_STAGES = [
    ['validate', '校验时间区间'], ['series', '拉取时序数据'], ['model_dist', '模型分布'],
    ['latency_dist', '延迟分布'], ['agent_dist', 'Agent 工具分布'],
  ]
  const [rangeStart, setRangeStart] = useState('') // yyyy-mm-dd
  const [rangeEnd, setRangeEnd] = useState('')
  const [rangeGran, setRangeGran] = useState('') // '' = 自动推断
  const [rangeSteps, setRangeSteps] = useState({}) // stage → {state,message}
  const [rangePct, setRangePct] = useState(0)
  const [rangePctText, setRangePctText] = useState('')
  const [rangeRunning, setRangeRunning] = useState(false)
  const [rangeError, setRangeError] = useState('')
  const [rangeReport, setRangeReport] = useState(null) // {range_report, agent_dist}
  const rangeDoneRef = useRef(false)
  // 用户名/模型名级联下拉：管理端用 UserModelOptionsInterface（页面生命周期内缓存一次），用户端用本人模型列表
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()
  const modelOptions = isAdmin
    ? (userName.trim() ? modelNamesOf(userOptions, userName.trim()) : allModelNames(userOptions))
    : myModelNames

  // 趋势数据到位后，默认区间取趋势首尾日期
  useEffect(() => {
    const t = stages.trend_chart || []
    if (t.length && !rangeStart && !rangeEnd) {
      setRangeStart((t[0].date || '').substring(0, 10))
      setRangeEnd((t[t.length - 1].date || '').substring(0, 10))
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [(stages.trend_chart || []).length])

  const inferGranularity = (spanMs) => (spanMs <= 48 * 3600 * 1000 ? 'hour' : 'day')

  const runRangeReport = async () => {
    if (!rangeStart || !rangeEnd) { setRangeError('请填写起止日期'); return }
    const startMs = new Date(rangeStart + 'T00:00:00').getTime()
    const endMs = new Date(rangeEnd + 'T23:59:59.999').getTime()
    if (!(startMs > 0 && endMs > startMs)) { setRangeError('无效的时间区间：结束须晚于开始'); return }
    const spanMs = endMs - startMs
    if (spanMs > 365 * 24 * 3600 * 1000) { setRangeError('时间区间过长：最大支持 365 天'); return }
    const gran = rangeGran || inferGranularity(spanMs)
    setRangeSteps({}); setRangePct(0); setRangePctText(''); setRangeError(''); setRangeReport(null)
    rangeDoneRef.current = false
    setRangeRunning(true)
    const body = { model_name: modelName.trim(), start_ms: startMs, end_ms: endMs, granularity: gran }
    if (userName.trim()) body.user_name = userName.trim()
    const applyEvent = (ev, payload) => {
      if (ev === 'progress' && payload) {
        const stage = payload.stage || ''
        setRangeSteps((s) => ({ ...s, [stage]: { state: 'running', message: payload.message || '' } }))
        let text = payload.message || payload.title || stage
        if (stage === 'series' && typeof payload.done === 'number' && typeof payload.total === 'number' && payload.total > 0) {
          text = `已聚合 ${payload.done}/${payload.total} 桶`
        }
        setRangePct(typeof payload.percent === 'number' ? payload.percent : 0)
        setRangePctText(text)
      } else if (ev === 'done') {
        rangeDoneRef.current = true
        setRangeSteps(Object.fromEntries(RANGE_STAGES.map(([s]) => [s, { state: 'done' }])))
        setRangePct(100); setRangePctText('完成')
        setRangeReport(payload)
        setRangeRunning(false)
      } else if (ev === 'error') {
        setRangeError((payload && (payload.message || payload.error)) || '未知错误')
        setRangeSteps((s) => Object.fromEntries(Object.entries(s).map(([k, v]) => [k, v.state === 'running' ? { state: 'failed' } : v])))
        setRangeRunning(false)
      }
    }
    // 解析单个 SSE 事件块（event:/data: 行），对齐旧版 lsmParseSSEEvent
    const parseEvent = (raw) => {
      let ev = 'message', data = ''
      raw.split('\n').forEach((line) => {
        if (!line) return
        if (line.indexOf('event:') === 0) ev = line.substring(6).trim()
        else if (line.indexOf('data:') === 0) data += (data ? '\n' : '') + line.substring(5).trim()
      })
      if (!data) return
      try { applyEvent(ev, JSON.parse(data)) } catch { /* 忽略非 JSON 帧 */ }
    }
    try {
      const resp = await fetch('ChatAnalysisTotalRangeInterface?stream=1', {
        method: 'POST', credentials: 'include',
        headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
      })
      if (!resp.ok || !resp.body) {
        applyEvent('error', { message: `HTTP ${resp.status}（无响应流）` }); return
      }
      setRangePct(5); setRangePctText('校验通过…')
      const reader = resp.body.getReader()
      const decoder = new TextDecoder('utf-8')
      let buffer = ''
      for (;;) {
        const result = await reader.read()
        if (result.done) break
        buffer += decoder.decode(result.value, { stream: true })
        let idx
        while ((idx = buffer.indexOf('\n\n')) >= 0) {
          const raw = buffer.substring(0, idx)
          buffer = buffer.substring(idx + 2)
          parseEvent(raw)
        }
      }
      if (buffer.length) parseEvent(buffer)
      if (!rangeDoneRef.current) applyEvent('error', { message: '连接中断，未收到完成事件' })
    } catch (e) {
      applyEvent('error', { message: '请求失败: ' + e.message })
    }
  }
  // ===== 区间报告结束 =====

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

  // 用户端未带模型进入：本人模型列表（缓存一次）到达后自动选第一个并查询
  useEffect(() => {
    if (isAdmin || init.modelName || !myModelNames.length || modelName) return
    setModelName(myModelNames[0])
    runQuery(days, '', myModelNames[0])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [myModelNames])

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
        {isAdmin ? <label>用户名
          <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 160 }}>
            <option value="">全部用户</option>
            {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
          </select>
        </label> : null}
        <label>模型名
          <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
            <option value="">全部模型</option>
            {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
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

      {/* 区间报告（v2.0.46：ChatAnalysisTotalRangeInterface?stream=1 SSE 流式） */}
      <div className="card">
        <h3>区间报告（tokens 时序 / 模型分布 / 延迟分布 / Agent 工具）</h3>
        <div className="toolbar">
          <label>开始日期 <input type="date" value={rangeStart} onChange={(e) => setRangeStart(e.target.value)} /></label>
          <label>结束日期 <input type="date" value={rangeEnd} onChange={(e) => setRangeEnd(e.target.value)} /></label>
          <label>颗粒度
            <select value={rangeGran} onChange={(e) => setRangeGran(e.target.value)}>
              <option value="">自动（≤48h→小时，否则→天）</option>
              <option value="minute">分钟</option>
              <option value="hour">小时</option>
              <option value="day">天</option>
            </select>
          </label>
          <button className="btn btn-primary" onClick={() => runRangeReport()} disabled={rangeRunning}>
            {rangeRunning ? '生成中…' : '生成报告'}
          </button>
        </div>
        {rangeError ? <div className="alert alert-error" style={{ marginTop: 8 }}>{rangeError}</div> : null}

        {rangeRunning || Object.keys(rangeSteps).length ? (
          <div style={{ marginTop: 10 }}>
            <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', marginBottom: 8 }}>
              {RANGE_STAGES.map(([s, label]) => {
                const st = rangeSteps[s]
                const icon = !st || st.state === 'running' ? '⏳' : st.state === 'done' ? '✅' : '✗'
                return (
                  <span key={s} title={st && st.message} style={{ fontSize: 12, color: st && st.state === 'failed' ? 'var(--danger,#c0392b)' : 'var(--muted)' }}>
                    {icon} {label}
                  </span>
                )
              })}
            </div>
            <div style={{ background: 'var(--border,#eee)', borderRadius: 4, height: 14, overflow: 'hidden' }}>
              <div style={{ width: `${Math.max(0, Math.min(100, rangePct))}%`, height: '100%', background: '#007aff', transition: 'width .3s' }} />
            </div>
            <div style={{ fontSize: 12, color: 'var(--muted)', marginTop: 4 }}>
              {Math.round(rangePct)}%{rangePctText ? ` · ${rangePctText}` : ''}
            </div>
          </div>
        ) : null}

        {rangeReport ? (
          <div style={{ marginTop: 12 }}>
            {(() => {
              const r = rangeReport.range_report || {}
              const a = rangeReport.agent_dist
              return (
                <>
                  <h4 style={{ margin: '10px 0 6px' }}>时序桶（{fmtNum(r.series ? r.series.length : 0)} 桶）</h4>
                  <DataTable
                    columns={[
                      { key: 'date', title: '时间' },
                      { key: 'count', title: '次数', render: fmtNum },
                      { key: 'tokens_input', title: '输入Tok', render: fmtNum },
                      { key: 'tokens_output', title: '输出Tok', render: fmtNum },
                      { key: 'tokens_total', title: '总Tok', render: fmtNum },
                    ]}
                    rows={r.series || []} empty="暂无数据" />
                  <h4 style={{ margin: '10px 0 6px' }}>模型分布</h4>
                  <DataTable
                    columns={[
                      { key: 'model_name', title: '模型' },
                      { key: 'call_count', title: '调用次数', render: fmtNum },
                      { key: 'tokens_total', title: '总Tok', render: fmtNum },
                    ]}
                    rows={r.model_dist || []} empty="暂无数据" />
                  <h4 style={{ margin: '10px 0 6px' }}>延迟分布</h4>
                  <DataTable
                    columns={[
                      { key: 'range', title: '延迟区间' },
                      { key: 'count', title: '次数', render: fmtNum },
                      { key: 'percentage', title: '占比', render: (v) => (v != null ? (v * 100).toFixed(1) + '%' : '-') },
                    ]}
                    rows={r.latency_dist || []} empty="暂无数据" />
                  {a ? (
                    <>
                      <h4 style={{ margin: '10px 0 6px' }}>Agent 工具（{fmtNum(a.unique_tools || 0)} 种 / {fmtNum(a.total_agent_count || 0)} 次）</h4>
                      <DataTable
                        columns={[
                          { key: 'agent_tool_name', title: '工具名称' },
                          { key: 'count', title: '调用次数', render: fmtNum },
                          { key: 'percentage', title: '占比', render: (v) => (v != null ? (v * 100).toFixed(1) + '%' : '-') },
                        ]}
                        rows={a.tool_stats || []} empty="暂无数据" />
                    </>
                  ) : null}
                </>
              )
            })()}
          </div>
        ) : null}
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
