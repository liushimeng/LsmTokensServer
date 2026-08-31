import { useEffect, useRef, useState } from 'react'
import { openWs, post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import { useUserModelOptions, useMyModelNames, modelNamesOf, allModelNames } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import TimeRangeSelector from '../components/TimeRangeSelector'
import { useTimeSpanLevels } from '../shared/useTimeSpanLevels'
import { nearestSpan } from '../shared/timeSpan'
import { fmtNum, fmtMs, pickRouteQuery } from '../shared/format'
import { useI18n } from '../i18n'

// 汇总统计页（/ChatAnalysisTotalWS WebSocket 流式分块推送）
// 协议：连接后首条消息 {type:'query',days,model_name,request_id(12hex)}；
//       服务端按 7 个 stage 串行推 chunk 快照，结束推 done；错误推 error 帧。
// WS 不可用（网关不支持 Upgrade）时 fallback 到 action=full_http 的 HTTP 接口。
// 20260826：时间跨度为动态档位（1 小时 ~ transactionRetentionDays+1 天，统一 span 编码）。

// 生成 12 位小写十六进制 request_id（服务端 sanitizeRequestID 强校验）
function genRequestId() {
  let s = ''
  for (let i = 0; i < 12; i++) s += Math.floor(Math.random() * 16).toString(16)
  return s
}

export default function ChatAnalysisTotal({ route }) {
  const { t } = useI18n()

  // stage 名称 → 中文名（渲染顺序与后端 wsChatStatsStageOrder 一致）
  const STAGE_NAMES = {
    kpi: t('chatAnalysisTotal.kpiOverview'), time_stats: t('chatAnalysisTotal.timeStats'),
    tokens_summary: t('chatAnalysisTotal.tokensSummary'), model_distribution: t('chatAnalysisTotal.modelDistOverview'),
    trend_chart: t('chatAnalysisTotal.usageTrendChart'), protocol_stats: t('chatAnalysisTotal.protocolStats'),
    agent_stats: t('chatAnalysisTotal.agentStats'),
  }

  // 区间报告 stage 名称
  const RANGE_STAGES = [
    ['validate', t('chatAnalysisTotal.validateRange')], ['series', t('chatAnalysisTotal.fetchSeries')],
    ['model_dist', t('chatAnalysisTotal.modelDistributionStage')], ['latency_dist', t('chatAnalysisTotal.latencyDistribution')],
    ['agent_dist', t('chatAnalysisTotal.agentToolDistribution')],
  ]

  const init = pickRouteQuery(route && route.query)
  const isAdmin = isAdminRole() // 用户端：服务端强制本人数据，隐藏用户名输入
  const [userName, setUserName] = useState(isAdmin ? init.userName : '')
  const [modelName, setModelName] = useState(init.modelName)
  const { levels, loading: levelsLoading } = useTimeSpanLevels()
  const [days, setDays] = useState(null) // 档位加载后初始化（默认就近 7 天档）
  // 各 stage 累计快照
  const [stages, setStages] = useState({})
  const [running, setRunning] = useState(false)
  const [progress, setProgress] = useState('') // 进度提示（已扫描行数）
  const [error, setError] = useState('')
  const [doneInfo, setDoneInfo] = useState(null) // done 帧信息
  const wsRef = useRef(null)
  const reqIdRef = useRef('')

  // ===== 区间报告（v2.0.46 对齐：POST ChatAnalysisTotalRangeInterface?stream=1 SSE）=====
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
    if (!rangeStart || !rangeEnd) { setRangeError(t('chatAnalysisTotal.fillStartDate')); return }
    const startMs = new Date(rangeStart + 'T00:00:00').getTime()
    const endMs = new Date(rangeEnd + 'T23:59:59.999').getTime()
    if (!(startMs > 0 && endMs > startMs)) { setRangeError(t('chatAnalysisTotal.invalidRange')); return }
    const spanMs = endMs - startMs
    if (spanMs > 365 * 24 * 3600 * 1000) { setRangeError(t('chatAnalysisTotal.rangeTooLong')); return }
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
          text = t('chatAnalysisTotal.aggregating', { done: payload.done, total: payload.total })
        }
        setRangePct(typeof payload.percent === 'number' ? payload.percent : 0)
        setRangePctText(text)
      } else if (ev === 'done') {
        rangeDoneRef.current = true
        setRangeSteps(Object.fromEntries(RANGE_STAGES.map(([s]) => [s, { state: 'done' }])))
        setRangePct(100); setRangePctText(t('chatAnalysisTotal.complete'))
        setRangeReport(payload)
        setRangeRunning(false)
      } else if (ev === 'error') {
        setRangeError((payload && (payload.message || payload.error)) || t('chatAnalysisTotal.unknownError'))
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
        applyEvent('error', { message: t('chatAnalysisTotal.httpError', { status: resp.status }) }); return
      }
      setRangePct(5); setRangePctText(t('chatAnalysisTotal.validationPassed'))
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
      if (!rangeDoneRef.current) applyEvent('error', { message: t('chatAnalysisTotal.connectionInterrupted') })
    } catch (e) {
      applyEvent('error', { message: t('chatAnalysisTotal.requestFailed', { message: e.message }) })
    } finally {
      // 阶段AY：rangeRunning 必须在所有退出路径（正常 done / 异常 / 早退 return）
      // 都重置，避免 UI 永远停留在"生成中"。
      setRangeRunning(false)
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
  const runQuery = (d = days ?? 7, u = userName, m = modelName) => {
    stopQuery()
    setStages({}); setError(''); setDoneInfo(null); setRunning(true); setProgress(t('chatAnalysisTotal.connecting'))
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
          if (kpi && kpi.scan_final) setProgress(t('chatAnalysisTotal.scanComplete', { count: fmtNum(kpi.scanned_rows) }))
          else if (kpi) setProgress(t('chatAnalysisTotal.scanning', { count: fmtNum(kpi.scanned_rows) }))
        } else if (obj.type === 'done') {
          setDoneInfo(obj); setRunning(false); setProgress('')
        } else if (obj.type === 'error') {
          setError(t('chatAnalysisTotal.queryError', { stage: STAGE_NAMES[obj.stage] || obj.stage || t('chatAnalysisTotal.stageError'), message: obj.message }))
          setRunning(false); setProgress('')
        } else if (obj.type === 'busy') {
          setError(obj.message || t('chatAnalysisTotal.busyError')); setRunning(false)
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
        setError(t('chatAnalysisTotal.sendQueryFailed', { message: e.message })); setRunning(false)
      }
    }
  }

  // HTTP fallback：POST ChatAnalysisTotalInterface action=full_http
  const httpFallback = async (d, u, m) => {
    setProgress(t('chatAnalysisTotal.wsUnavailable'))
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
      setError(e.message || t('chatAnalysisTotal.queryFailed'))
    } finally { setRunning(false); setProgress('') }
  }

  // 首次进入：若路由带了 user/model 则自动查询
  useEffect(() => {
    return () => stopQuery() // 离开页面时关闭 WS
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])
  // 动态档位到达后初始化 span（默认 7 天就近档）
  useEffect(() => {
    if (!levels.length || days !== null) return
    setDays(nearestSpan(levels, 7))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [levels])

  // 首次进入：若路由带了 user/model 则自动查询（等待档位就绪）
  useEffect(() => {
    if (days === null) return
    if (init.userName && init.modelName) runQuery(days, init.userName, init.modelName)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days])

  // 用户端未带模型进入：本人模型列表（缓存一次）到达后自动选第一个并查询
  useEffect(() => {
    if (days === null) return
    if (isAdmin || init.modelName || !myModelNames.length || modelName) return
    setModelName(myModelNames[0])
    runQuery(days, '', myModelNames[0])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [myModelNames, days])

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
      <h2 className="page-title">{t('chatAnalysisTotal.title')}</h2>

      <div className="toolbar">
        {isAdmin ? <label>{t('chatAnalysisTotal.userNameLabel')}
          <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 160 }}>
            <option value="">{t('chatAnalysisTotal.allUsers')}</option>
            {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
          </select>
        </label> : null}
        <label>{t('chatAnalysisTotal.modelNameLabel')}
          <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
            <option value="">{t('chatAnalysisTotal.allModels')}</option>
            {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysisTotal.timeRange')}
          <TimeRangeSelector span={days ?? 7} onChange={(d) => { setDays(d); setStages({}); setDoneInfo(null); if (!running) runQuery(d) }} levels={levels} loading={levelsLoading} />
        </label>
        <button className="btn btn-primary" onClick={() => runQuery()} disabled={running}>{t('chatAnalysisTotal.query')}</button>
        {running ? <button className="btn" onClick={() => { stopQuery(); setRunning(false); setProgress('') }}>{t('chatAnalysisTotal.cancel')}</button> : null}
        {running || progress ? <span style={{ color: 'var(--muted)', fontSize: 13 }}>{running ? (progress || t('chatAnalysisTotal.statizing')) : progress}</span> : null}
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}
      {doneInfo && doneInfo.timed_out ? (
        <div className="alert alert-error">{t('chatAnalysisTotal.queryTimeout', { warnings: String(doneInfo.warnings || []).join('; ') })}</div>
      ) : null}
      {doneInfo && !doneInfo.timed_out && doneInfo.elapsed_ms != null ? (
        <div className="alert alert-ok">{t('chatAnalysisTotal.statComplete', { ms: fmtMs(doneInfo.elapsed_ms) })}</div>
      ) : null}
      {doneInfo && doneInfo.http_fallback ? (
        <div className="alert alert-ok">{t('chatAnalysisTotal.statCompleteHttp')}</div>
      ) : null}

      {/* 维度 1：KPI 卡片 */}
      <div className="card-grid kpi-grid">
        <div className="card">
          <h3>{t('chatAnalysisTotal.totalRequests')}</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.total_calls) : '-'}</div>
        </div>
        <div className="card">
          <h3>{t('chatAnalysisTotal.totalTokensCard')}</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.total_tokens) : '-'}</div>
        </div>
        <div className="card">
          <h3>{t('chatAnalysisTotal.activeModels')}</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.active_models) : '-'}</div>
        </div>
        <div className="card">
          <h3>{t('chatAnalysisTotal.activeDays')}</h3>
          <div style={{ fontSize: 26, fontWeight: 700 }}>{kpi ? fmtNum(kpi.active_days) : '-'}</div>
        </div>
      </div>

      {/* 维度 3：Tokens 概览 */}
      <div className="card">
        <h3>{t('chatAnalysisTotal.tokensOverview')}</h3>
        {ts ? (
          <>
            <dl className="kv">
              <dt>{t('chatAnalysisTotal.callCount')}</dt><dd>{fmtNum(ts.total_count)}</dd>
              <dt>{t('chatAnalysisTotal.inputTokens')}</dt><dd>{fmtNum(ts.total_input)}</dd>
              <dt>{t('chatAnalysisTotal.outputTokens')}</dt><dd>{fmtNum(ts.total_output)}</dd>
              <dt>{t('chatAnalysisTotal.totalTokens')}</dt><dd>{fmtNum(ts.total_tokens)}</dd>
              <dt>{t('chatAnalysisTotal.inputOutputRatio')}</dt><dd>{ts.total_output ? (ts.total_input / ts.total_output).toFixed(2) : '-'}</dd>
            </dl>
            <DataTable
              columns={[
                { key: 'date', title: t('chatAnalysisTotal.date') },
                { key: 'count', title: t('chatAnalysisTotal.count'), render: fmtNum },
                { key: 'tokens_input', title: t('chatAnalysisTotal.inputTokens'), render: fmtNum },
                { key: 'tokens_output', title: t('chatAnalysisTotal.outputTokens'), render: fmtNum },
                { key: 'tokens_total', title: t('chatAnalysisTotal.totalTokens'), render: fmtNum },
                { key: 'avg_elapsed_ms', title: t('chatAnalysisTotal.avgDuration'), render: fmtMs },
              ]}
              rows={ts.buckets || []} empty={t('chatAnalysisTotal.noData')} />
          </>
        ) : <div className="table-empty">{t('chatAnalysisTotal.noData')}</div>}
      </div>

      {/* 维度 4：源站模型分布 */}
      <div className="card">
        <h3>{t('chatAnalysisTotal.modelDistribution')}</h3>
        <DataTable
          columns={[
            { key: 'model_name', title: t('chatAnalysisTotal.sourceModel') },
            { key: 'call_count', title: t('chatAnalysisTotal.callCount2'), render: fmtNum },
            { key: 'call_share', title: t('chatAnalysisTotal.callShare'), render: (v) => (v != null ? (v * 100).toFixed(2) + '%' : '-') },
            { key: 'tokens_input', title: t('chatAnalysisTotal.inputTokens'), render: fmtNum },
            { key: 'tokens_output', title: t('chatAnalysisTotal.outputTokens'), render: fmtNum },
            { key: 'tokens_total', title: t('chatAnalysisTotal.totalTokens'), render: fmtNum },
            { key: 'token_share', title: t('chatAnalysisTotal.tokenShare'), render: (v) => (v != null ? (v * 100).toFixed(2) + '%' : '-') },
            { key: 'dst_endpoint_count', title: t('chatAnalysisTotal.sourceCount'), render: (v) => fmtNum(v) },
            { key: 'top_dst_endpoints', title: t('chatAnalysisTotal.topSources'), render: (v) => (v || []).map((e) => `#${e.dst_endpoint_id}(${fmtNum(e.call_count)})`).join('、') || '-' },
          ]}
          rows={modelDist} empty={t('chatAnalysisTotal.noData')} />
      </div>

      {/* 维度 2：时间分布 */}
      <div className="card">
        <h3>{t('chatAnalysisTotal.timeDistribution')}</h3>
        <DataTable
          columns={[
            { key: 'date', title: t('chatAnalysisTotal.date') },
            { key: 'count', title: t('chatAnalysisTotal.callCount2'), render: (v, r) => (
              <span title={fmtNum(v)}>{v > 0 ? '█'.repeat(Math.min(30, Math.max(1, Math.round(v / Math.max(1, Math.max(...timeStats.map((x) => x.count))) * 30)))) + ' ' + fmtNum(v) : fmtNum(v)}</span>
            ) },
          ]}
          rows={timeStats} empty={t('chatAnalysisTotal.noData')} />
      </div>

      {/* 维度 5：用量趋势 */}
      <div className="card">
        <h3>{t('chatAnalysisTotal.usageTrend')}</h3>
        <DataTable
          columns={[
            { key: 'date', title: t('chatAnalysisTotal.date') },
            { key: 'count', title: t('chatAnalysisTotal.count'), render: fmtNum },
            { key: 'tokens_input', title: t('chatAnalysisTotal.inputTokens'), render: fmtNum },
            { key: 'tokens_output', title: t('chatAnalysisTotal.outputTokens'), render: fmtNum },
            { key: 'tokens_total', title: t('chatAnalysisTotal.totalTokens'), render: fmtNum },
          ]}
          rows={trend} empty={t('chatAnalysisTotal.noData')} />
      </div>

      {/* 区间报告（v2.0.46：ChatAnalysisTotalRangeInterface?stream=1 SSE 流式） */}
      <div className="card">
        <h3>{t('chatAnalysisTotal.rangeReport')}</h3>
        <div className="toolbar">
          <label>{t('chatAnalysisTotal.startDate')} <input type="date" value={rangeStart} onChange={(e) => setRangeStart(e.target.value)} /></label>
          <label>{t('chatAnalysisTotal.endDate')} <input type="date" value={rangeEnd} onChange={(e) => setRangeEnd(e.target.value)} /></label>
          <label>{t('chatAnalysisTotal.granularity')}
            <select value={rangeGran} onChange={(e) => setRangeGran(e.target.value)}>
              <option value="">{t('chatAnalysisTotal.autoGranularity')}</option>
              <option value="minute">{t('chatAnalysisTotal.minute')}</option>
              <option value="hour">{t('chatAnalysisTotal.hour')}</option>
              <option value="day">{t('chatAnalysisTotal.day')}</option>
            </select>
          </label>
          <button className="btn btn-primary" onClick={() => runRangeReport()} disabled={rangeRunning}>
            {rangeRunning ? t('chatAnalysisTotal.generating') : t('chatAnalysisTotal.generateReport')}
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
                  <h4 style={{ margin: '10px 0 6px' }}>{t('chatAnalysisTotal.seriesBuckets', { count: fmtNum(r.series ? r.series.length : 0) })}</h4>
                  <DataTable
                    columns={[
                      { key: 'date', title: t('chatAnalysisTotal.date') },
                      { key: 'count', title: t('chatAnalysisTotal.count'), render: fmtNum },
                      { key: 'tokens_input', title: t('chatAnalysisTotal.inputTokens'), render: fmtNum },
                      { key: 'tokens_output', title: t('chatAnalysisTotal.outputTokens'), render: fmtNum },
                      { key: 'tokens_total', title: t('chatAnalysisTotal.totalTokens'), render: fmtNum },
                    ]}
                    rows={r.series || []} empty={t('chatAnalysisTotal.noData')} />
                  <h4 style={{ margin: '10px 0 6px' }}>{t('chatAnalysisTotal.modelDist')}</h4>
                  <DataTable
                    columns={[
                      { key: 'model_name', title: t('chatAnalysisTotal.sourceModel') },
                      { key: 'call_count', title: t('chatAnalysisTotal.callCount2'), render: fmtNum },
                      { key: 'tokens_total', title: t('chatAnalysisTotal.totalTokens'), render: fmtNum },
                    ]}
                    rows={r.model_dist || []} empty={t('chatAnalysisTotal.noData')} />
                  <h4 style={{ margin: '10px 0 6px' }}>{t('chatAnalysisTotal.latencyDist')}</h4>
                  <DataTable
                    columns={[
                      { key: 'range', title: t('chatAnalysisTotal.latencyDist') },
                      { key: 'count', title: t('chatAnalysisTotal.count'), render: fmtNum },
                      { key: 'percentage', title: t('chatAnalysisTotal.percentage'), render: (v) => (v != null ? (v * 100).toFixed(1) + '%' : '-') },
                    ]}
                    rows={r.latency_dist || []} empty={t('chatAnalysisTotal.noData')} />
                  {a ? (
                    <>
                      <h4 style={{ margin: '10px 0 6px' }}>{t('chatAnalysisTotal.agentToolDist', { unique: fmtNum(a.unique_tools || 0), total: fmtNum(a.total_agent_count || 0) })}</h4>
                      <DataTable
                        columns={[
                          { key: 'agent_tool_name', title: t('chatAnalysisTotal.agentToolName') },
                          { key: 'count', title: t('chatAnalysisTotal.callCount2'), render: fmtNum },
                          { key: 'percentage', title: t('chatAnalysisTotal.percentage'), render: (v) => (v != null ? (v * 100).toFixed(1) + '%' : '-') },
                        ]}
                        rows={a.tool_stats || []} empty={t('chatAnalysisTotal.noData')} />
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
        <h3>{t('chatAnalysisTotal.protocolAnalysis')}</h3>
        {proto ? (
          <>
            {kvTable(proto, [
              ['avg_elapsed_ms', t('chatAnalysisTotal.avgElapsed'), fmtMs], ['min_elapsed_ms', t('chatAnalysisTotal.minElapsed'), fmtMs],
              ['max_elapsed_ms', t('chatAnalysisTotal.maxElapsed'), fmtMs], ['avg_req_size', t('chatAnalysisTotal.avgReqSize'), fmtNum],
              ['avg_resp_size', t('chatAnalysisTotal.avgRespSize'), fmtNum], ['stream_count', t('chatAnalysisTotal.streamCount'), fmtNum],
              ['non_stream_count', t('chatAnalysisTotal.nonStreamCount'), fmtNum], ['has_system_prompt', t('chatAnalysisTotal.hasSystemPrompt'), fmtNum],
              ['has_tool_call', t('chatAnalysisTotal.hasToolCall'), fmtNum], ['multi_turn_count', t('chatAnalysisTotal.multiTurn'), fmtNum],
              ['single_turn_count', t('chatAnalysisTotal.singleTurn'), fmtNum], ['sample_count', t('chatAnalysisTotal.sampleCount'), fmtNum],
            ])}
            <h3 style={{ marginTop: 12 }}>{t('chatAnalysisTotal.methodDist')}</h3>
            <DataTable columns={[{ key: 'k', title: t('chatAnalysisTotal.method') }, { key: 'v', title: t('chatAnalysisTotal.count'), render: fmtNum }]}
                       rows={Object.entries(proto.method_stats || {}).map(([k, v]) => ({ k, v }))} empty={t('chatAnalysisTotal.noData')} />
            <h3 style={{ marginTop: 12 }}>{t('chatAnalysisTotal.urlPatternDist')}</h3>
            <DataTable columns={[{ key: 'k', title: t('chatAnalysisTotal.urlPattern') }, { key: 'v', title: t('chatAnalysisTotal.count'), render: fmtNum }]}
                       rows={Object.entries(proto.url_pattern_stats || {}).map(([k, v]) => ({ k, v }))} empty={t('chatAnalysisTotal.noData')} />
            <h3 style={{ marginTop: 12 }}>{t('chatAnalysisTotal.statusCodeDist')}</h3>
            <DataTable columns={[{ key: 'k', title: t('chatAnalysisTotal.statusCode') }, { key: 'v', title: t('chatAnalysisTotal.count'), render: fmtNum }]}
                       rows={Object.entries(proto.status_stats || {}).map(([k, v]) => ({ k, v }))} empty={t('chatAnalysisTotal.noData')} />
          </>
        ) : <div className="table-empty">{t('chatAnalysisTotal.noData')}</div>}
      </div>

      {/* 维度 7：Agent 工具统计 */}
      <div className="card">
        <h3>{t('chatAnalysisTotal.agentToolStats')}</h3>
        {agent ? (
          <>
            <dl className="kv">
              <dt>{t('chatAnalysisTotal.agentTotalCalls')}</dt><dd>{fmtNum(agent.total_agent_count)}</dd>
              <dt>{t('chatAnalysisTotal.toolCount')}</dt><dd>{fmtNum(agent.unique_tools)}</dd>
            </dl>
            <DataTable
              columns={[
                { key: 'agent_tool_name', title: t('chatAnalysisTotal.agentToolName') },
                { key: 'count', title: t('chatAnalysisTotal.callCount2'), render: fmtNum },
                { key: 'percentage', title: t('chatAnalysisTotal.percentage'), render: (v) => (v * 100).toFixed(2) + '%' },
                { key: 'first_seen_at', title: t('chatAnalysisTotal.firstSeen') },
                { key: 'last_seen_at', title: t('chatAnalysisTotal.lastSeen') },
              ]}
              rows={agent.tool_stats || []} empty={t('chatAnalysisTotal.noData')} />
          </>
        ) : <div className="table-empty">{t('chatAnalysisTotal.noData')}</div>}
      </div>
    </div>
  )
}
