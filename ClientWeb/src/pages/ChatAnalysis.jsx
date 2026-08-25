import { useEffect, useState } from 'react'
import { post, get } from '../shared/api'
import { isAdminRole, fetchMyModels } from '../shared/auth'
import { useUserModelOptions, modelNamesOf, allModelNames } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { fmtTime, fmtNum, fmtBytes, fmtMs, pickRouteQuery } from '../shared/format'

// 对话明细查询页（管理端 /ChatAnalysisInterface）
// 支持多条件筛选 + 分页 + 单条详情（按需拉取大字段）+ 批量删除
const DAYS_OPTIONS = [
  { v: 0, t: '全部时间' }, { v: -1, t: '最近 1 小时' }, { v: -2, t: '最近 2 小时' },
  { v: -4, t: '最近 4 小时' }, { v: -6, t: '最近 6 小时' }, { v: -12, t: '最近 12 小时' },
  { v: 1, t: '最近 1 天' }, { v: 3, t: '最近 3 天' }, { v: 5, t: '最近 5 天' },
  { v: 7, t: '最近 7 天' }, { v: 14, t: '最近 14 天' }, { v: 30, t: '最近 30 天' },
  { v: 60, t: '最近 60 天' }, { v: 90, t: '最近 90 天' },
]
const PAGE_SIZES = [3, 5, 10, 15, 20, 50, 100]
// 详情字段白名单（服务端 chatAnalysisDetailFieldColumns 一致）
const DETAIL_FIELDS = [
  { key: 'request_body', title: '请求体（转发）' },
  { key: 'response_body', title: '响应体（转发）' },
  { key: 'request_src_protocol_body', title: '请求体（原始协议）' },
  { key: 'response_src_protocol_body', title: '响应体（原始协议）' },
  { key: 'request_headers', title: '请求头（转发）' },
  { key: 'response_headers', title: '响应头（转发）' },
]
// SSE 事件解析（对齐旧版 lsmParseSSEEvent 契约）：逐行拆 event:/data:，data 尝试 JSON 解析
function parseSSEEvents(text) {
  if (!text) return []
  const events = []
  let cur = null
  for (const line of text.split(/\r?\n/)) {
    if (line.startsWith('event:')) {
      if (cur) events.push(cur)
      cur = { event: line.slice(6).trim(), data: [] }
    } else if (line.startsWith('data:')) {
      if (!cur) cur = { event: '', data: [] }
      cur.data.push(line.slice(5).trim())
    } else if (line === '' && cur) {
      events.push(cur); cur = null
    }
  }
  if (cur) events.push(cur)
  return events.map((e) => {
    const raw = e.data.join('\n')
    let parsed = null
    try { parsed = JSON.parse(raw) } catch { /* 非 JSON data */ }
    return { event: e.event, raw, parsed }
  })
}
// SSE 聚合解析（对齐旧版 lsmAggregateSSE 契约）：合并文本增量 + 累加 usage
function aggregateSSE(text) {
  const events = parseSSEEvents(text)
  const out = { textParts: [], usage: null, toolCalls: [], eventTypes: {} }
  events.forEach((e) => {
    if (e.event) out.eventTypes[e.event] = (out.eventTypes[e.event] || 0) + 1
    const p = e.parsed
    if (!p) return
    if (p.type === 'content_block_delta' && p.delta) {
      if (p.delta.text) out.textParts.push(p.delta.text)
      if (p.delta.partial_json) out.textParts.push(p.delta.partial_json)
    } else if (p.choices && p.choices[0] && p.choices[0].delta) {
      const t = p.choices[0].delta.content || p.choices[0].delta.reasoning_content
      if (t) out.textParts.push(t)
    } else if (p.type === 'content_block_start' && p.content_block && p.content_block.type === 'tool_use') {
      out.toolCalls.push(p.content_block.name || '')
    }
    const u = p.usage || (p.message && p.message.usage)
    if (u) {
      out.usage = out.usage || {}
      out.usage.input_tokens = (out.usage.input_tokens || 0) + (u.input_tokens || 0)
      out.usage.output_tokens = (out.usage.output_tokens || 0) + (u.output_tokens || 0)
      if (u.input_tokens !== undefined) out.usage.input_tokens_final = u.input_tokens
      if (u.output_tokens !== undefined) out.usage.output_tokens_final = u.output_tokens
    }
  })
  return out
}
function prettyJSON(s) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

export default function ChatAnalysis({ route }) {
  const init = pickRouteQuery(route && route.query)
  const isAdmin = isAdminRole() // 用户端：服务端强制 claims.UserName，隐藏用户名输入与批量删除
  // 筛选条件
  const [userName, setUserName] = useState(isAdmin ? init.userName : '')
  const [modelName, setModelName] = useState(init.modelName)
  const [days, setDays] = useState(3)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(10)
  const [filterUrl, setFilterUrl] = useState('')
  const [filterMethod, setFilterMethod] = useState('')
  const [filterStatus, setFilterStatus] = useState('')
  const [filterStatusNot, setFilterStatusNot] = useState(false)
  const [filterProtocolType, setFilterProtocolType] = useState(0)
  const [filterDstModel, setFilterDstModel] = useState('')
  const [filterTools, setFilterTools] = useState('')
  const [filterAgentTool, setFilterAgentTool] = useState('')
  const [filterInTok, setFilterInTok] = useState(0)
  const [filterOutTok, setFilterOutTok] = useState(0)
  // 下拉选项（接口动态拉取）
  const [dstModels, setDstModels] = useState([])
  const [agentTools, setAgentTools] = useState([])
  // 数据状态
  const [rows, setRows] = useState([])
  const [total, setTotal] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [okMsg, setOkMsg] = useState('')
  const [selected, setSelected] = useState([]) // 勾选的记录 ID
  // 详情弹窗
  const [detailRow, setDetailRow] = useState(null)
  const [detailTab, setDetailTab] = useState('request_body')
  const [detailValue, setDetailValue] = useState('')
  const [detailLoading, setDetailLoading] = useState(false)
  const [detailCache, setDetailCache] = useState({})
  const [detailView, setDetailView] = useState('raw') // raw / json / sse / agg
  const [copyOk, setCopyOk] = useState(false)
  const [deleting, setDeleting] = useState(false)
  // 用户名/模型名级联下拉：管理端用 UserModelOptionsInterface（页面生命周期内缓存一次），用户端用本人模型列表
  const { users: userOptions } = useUserModelOptions()
  const [myModels, setMyModels] = useState([])
  const modelOptions = isAdmin
    ? (userName.trim() ? modelNamesOf(userOptions, userName.trim()) : allModelNames(userOptions))
    : myModels.map((m) => m.model_name).filter(Boolean)

  const hasKey = (isAdmin ? userName.trim() !== '' : true) && modelName.trim() !== ''

  // 拉取下拉选项：目标模型（依赖 user+model）+ Agent 工具（全站）
  const loadOptions = async (modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (!mn) return
    try {
      const d = await post('ChatAnalysisDstModelsInterface', { user_name: isAdmin ? userName.trim() : '', model_name: mn })
      setDstModels(d.data || [])
    } catch { /* 静默失败，仅影响下拉选项 */ }
  }
  useEffect(() => {
    get('ChatAnalysisAgentToolsInterface').then((d) => setAgentTools(d.data || [])).catch(() => {})
    if (hasKey) { loadOptions(); setPage(1); doQuery(1); return }
    // 用户端进入页面未带模型：自动取本人第一个模型并查询（对齐旧版重定向逻辑）
    if (!isAdmin) {
      fetchMyModels()
        .then((ms) => {
          setMyModels(ms || [])
          const first = ms && ms[0]
          if (!first) return
          setModelName(first.model_name || '')
          setPage(1)
          loadOptions(first.model_name || '')
          doQuery(1, first.model_name || '')
        })
        .catch(() => {})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 查询（modelOverride 用于自动查询时规避 setState 异步）
  const doQuery = async (p = page, modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (!isAdmin && !mn) { setError('请先填写模型名'); return }
    if (isAdmin && (userName.trim() === '' || mn === '')) { setError('请先填写用户名和模型名'); return }
    setLoading(true); setError(''); setOkMsg(''); setSelected([])
    try {
      const d = await post('ChatAnalysisInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn,
        page: p, page_size: pageSize, days,
        filter_url: filterUrl.trim(), filter_method: filterMethod.trim(),
        filter_status: filterStatus.trim(), filter_status_not: filterStatusNot,
        filter_protocol_type: filterProtocolType, filter_dst_model_name: filterDstModel,
        filter_tools: filterTools.trim(), filter_agent_tool_name: filterAgentTool,
        filter_input_tokens_nonzero: filterInTok, filter_output_tokens_nonzero: filterOutTok,
      })
      const data = d.data || {}
      setRows(data.records || [])
      setTotal(data.totalCount || 0)
      setTotalPages(data.totalPages || 0)
      setPage(data.currentPage || p)
    } catch (e) {
      setError(e.message || '查询失败')
    } finally { setLoading(false) }
  }

  // 打开详情弹窗：按需拉取当前 Tab 字段（服务端白名单字段）
  const openDetail = async (row, field = 'request_body') => {
    setDetailRow(row); setDetailTab(field); setDetailView('raw'); setCopyOk(false)
    setDetailValue(''); setDetailLoading(false)
    if (detailCache[`${row.id}:${field}`] !== undefined) {
      setDetailValue(detailCache[`${row.id}:${field}`]); return
    }
    setDetailLoading(true)
    try {
      const d = await post('ChatAnalysisDetailInterface', {
        id: row.id, user_name: isAdmin ? userName.trim() : '', model_name: modelName.trim(), field,
      })
      const v = d.value || ''
      setDetailValue(v)
      setDetailCache((c) => ({ ...c, [`${row.id}:${field}`]: v }))
    } catch (e) {
      setDetailValue('加载失败：' + (e.message || ''))
    } finally { setDetailLoading(false) }
  }

  // 批量删除（最多 500 条/次，服务端限制）
  const batchDelete = async () => {
    if (!selected.length) return
    if (!window.confirm(`确认删除选中的 ${selected.length} 条对话记录？删除后不可恢复。`)) return
    setDeleting(true); setError(''); setOkMsg('')
    try {
      const d = await post('ChatAnalysisBatchDeleteInterface', {
        user_name: userName.trim(), model_name: modelName.trim(), ids: selected,
      })
      setOkMsg(d.message || '删除完成')
      setSelected([])
      doQuery(page)
    } catch (e) {
      setError(e.message || '删除失败')
    } finally { setDeleting(false) }
  }

  const toggleAll = (checked) => {
    setSelected(checked ? rows.map((r) => r.id) : [])
  }
  const toggleOne = (id, checked) => {
    setSelected((s) => (checked ? [...s, id] : s.filter((x) => x !== id)))
  }

  const columns = [
    ...(isAdmin ? [{ key: 'check', title: <input type="checkbox" checked={rows.length > 0 && selected.length === rows.length} onChange={(e) => toggleAll(e.target.checked)} />, render: (_, r) => (
      <input type="checkbox" checked={selected.includes(r.id)} onChange={(e) => toggleOne(r.id, e.target.checked)} />
    ) }] : []),
    { key: 'id', title: 'ID', width: 90 },
    { key: 'created_at', title: '时间', render: (v) => fmtTime(v) },
    { key: 'request_method', title: '方法', width: 70 },
    { key: 'request_url', title: 'URL', render: (v) => <span title={v}>{String(v || '').length > 60 ? String(v).slice(0, 60) + '…' : v}</span> },
    { key: 'response_status', title: '状态', render: (v) => {
      const ok = String(v).startsWith('2')
      return <span style={{ color: ok ? 'var(--ok)' : 'var(--danger)' }}>{v || '-'}</span>
    } },
    { key: 'dst_model_name', title: '目标模型' },
    { key: 'tokens_input_size', title: '输入Tok', render: (v) => fmtNum(v) },
    { key: 'tokens_output_size', title: '输出Tok', render: (v) => fmtNum(v) },
    { key: 'elapsed_ms', title: '耗时', render: (v) => fmtMs(v) },
    { key: 'agent_tool_name', title: 'Agent工具', render: (v) => v || '-' },
    { key: 'actions', title: '操作', render: (_, r) => (
      <button className="btn btn-sm" onClick={() => openDetail(r)}>详情</button>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">对话明细分析</h2>

      <div className="toolbar">
        {isAdmin ? <label>用户名
          <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 140 }}>
            <option value="">请选择用户</option>
            {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
          </select>
        </label> : null}
        <label>模型名
          <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
            <option value="">请选择模型</option>
            {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>时间跨度
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {DAYS_OPTIONS.map((o) => <option key={o.v} value={o.v}>{o.t}</option>)}
          </select>
        </label>
        <label>每页
          <select value={pageSize} onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1) }}>
            {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </label>
        <label>协议
          <select value={filterProtocolType} onChange={(e) => setFilterProtocolType(Number(e.target.value))}>
            <option value={0}>全部</option>
            <option value={1}>Anthropic</option>
            <option value={2}>OpenAI</option>
          </select>
        </label>
        <label>目标模型
          <select value={filterDstModel} onChange={(e) => setFilterDstModel(e.target.value)}>
            <option value="">全部</option>
            {dstModels.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>Agent工具
          <select value={filterAgentTool} onChange={(e) => setFilterAgentTool(e.target.value)}>
            <option value="">全部</option>
            {agentTools.map((t) => <option key={t} value={t}>{t}</option>)}
          </select>
        </label>
        <label>方法 <input value={filterMethod} onChange={(e) => setFilterMethod(e.target.value)} placeholder="POST" style={{ width: 80 }} /></label>
        <label>状态 <input value={filterStatus} onChange={(e) => setFilterStatus(e.target.value)} placeholder="200" style={{ width: 80 }} /></label>
        <label className="field-check" style={{ margin: 0 }}>
          <input type="checkbox" checked={filterStatusNot} onChange={(e) => setFilterStatusNot(e.target.checked)} />取反
        </label>
        <label>输入Tok
          <select value={filterInTok} onChange={(e) => setFilterInTok(Number(e.target.value))}>
            <option value={0}>全部</option><option value={1}>非零</option><option value={2}>为零</option>
          </select>
        </label>
        <label>输出Tok
          <select value={filterOutTok} onChange={(e) => setFilterOutTok(Number(e.target.value))}>
            <option value={0}>全部</option><option value={1}>非零</option><option value={2}>为零</option>
          </select>
        </label>
        <label>URL <input value={filterUrl} onChange={(e) => setFilterUrl(e.target.value)} placeholder="URL 包含" style={{ width: 150 }} /></label>
        <label>工具串 <input value={filterTools} onChange={(e) => setFilterTools(e.target.value)} placeholder="tools 包含" style={{ width: 120 }} /></label>
        <button className="btn btn-primary" onClick={() => { setPage(1); doQuery(1) }} disabled={loading}>查询</button>
        {isAdmin ? <button className="btn btn-danger" onClick={batchDelete} disabled={!selected.length || deleting}>
          {deleting ? '删除中…' : `批量删除(${selected.length})`}
        </button> : null}
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}
      {okMsg ? <div className="alert alert-ok">{okMsg}</div> : null}

      <DataTable columns={columns} rows={rows} loading={loading} rowKey="id" empty="暂无对话记录（需填写用户名 + 模型名后查询）" />

      {totalPages > 0 ? (
        <div className="pager">
          <span>共 {fmtNum(total)} 条 / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page <= 1 || loading} onClick={() => doQuery(page - 1)}>上一页</button>
          <span>第 {page} / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page >= totalPages || loading} onClick={() => doQuery(page + 1)}>下一页</button>
        </div>
      ) : null}

      {detailRow ? (
        <Modal title={`对话详情 #${detailRow.id}`} width={860} onClose={() => setDetailRow(null)}>
          <div className="toolbar detail-tabs" style={{ padding: '6px 10px' }}>
            {DETAIL_FIELDS.map((f) => (
              <button key={f.key} className={`btn btn-sm${detailTab === f.key ? ' btn-primary' : ''}`}
                      onClick={() => openDetail(detailRow, f.key)}>{f.title}</button>
            ))}
          </div>
          <dl className="kv" style={{ marginBottom: 12 }}>
            <dt>时间</dt><dd>{fmtTime(detailRow.created_at)}</dd>
            <dt>请求</dt><dd>{detailRow.request_method} {detailRow.request_url}</dd>
            <dt>状态</dt><dd>{detailRow.response_status}（耗时 {fmtMs(detailRow.elapsed_ms)}）</dd>
            <dt>Tokens</dt><dd>输入 {fmtNum(detailRow.tokens_input_size)} / 输出 {fmtNum(detailRow.tokens_output_size)}</dd>
            <dt>大小</dt><dd>请求 {fmtBytes(detailRow.request_content_length)} / 响应 {fmtBytes(detailRow.response_content_length)}</dd>
          </dl>
          {detailLoading ? <div className="table-loading">字段内容加载中…</div>
            : (() => {
              // 视图切换：raw 原文 / json 美化 / sse 事件解析 / agg 聚合解析（仅 body 类字段有意义）
              const isBody = detailTab.includes('body')
              let shown = detailValue || '（空）'
              if (isBody && detailView === 'json') shown = prettyJSON(detailValue) || '（空）'
              if (isBody && detailView === 'sse') {
                const evs = parseSSEEvents(detailValue)
                shown = evs.length
                  ? evs.map((e, i) => `# ${i + 1} event: ${e.event || '(default)'}\n${e.parsed ? JSON.stringify(e.parsed, null, 2) : e.raw}`).join('\n\n')
                  : '（未解析出 SSE 事件）'
              }
              if (isBody && detailView === 'agg') {
                const agg = aggregateSSE(detailValue)
                const usage = agg.usage || {}
                shown = [
                  `事件类型分布: ${Object.entries(agg.eventTypes).map(([k, v]) => `${k || '(default)'}×${v}`).join('、') || '无'}`,
                  agg.toolCalls.length ? `工具调用: ${agg.toolCalls.join('、')}` : '',
                  `usage: input=${usage.input_tokens_final ?? usage.input_tokens ?? 0} output=${usage.output_tokens_final ?? usage.output_tokens ?? 0}`,
                  '---- 聚合文本 ----',
                  agg.textParts.join('') || '（无文本增量）',
                ].filter(Boolean).join('\n')
              }
              const copy = () => {
                navigator.clipboard.writeText(detailValue || '').then(() => {
                  setCopyOk(true); setTimeout(() => setCopyOk(false), 1500)
                }).catch(() => {})
              }
              return (
                <div>
                  <div className="toolbar" style={{ padding: '4px 0' }}>
                    {isBody ? (
                      <span>
                        {['raw', 'json', 'sse', 'agg'].map((v) => (
                          <button key={v} className={`btn btn-sm${detailView === v ? ' btn-primary' : ''}`} onClick={() => setDetailView(v)}>
                            {{ raw: '原文', json: 'JSON 美化', sse: 'SSE 解析', agg: '聚合解析' }[v]}
                          </button>
                        ))}
                      </span>
                    ) : null}
                    <button className="btn btn-sm" onClick={copy}>{copyOk ? '已复制 ✓' : '复制'}</button>
                  </div>
                  <pre className="log-box">{shown}</pre>
                </div>
              )
            })()}
        </Modal>
      ) : null}
    </div>
  )
}
