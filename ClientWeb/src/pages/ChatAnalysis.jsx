import { useEffect, useState } from 'react'
import { post, get } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import { useUserModelOptions, useMyModelNames, modelNamesOf, allModelNames } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import SseEventList from '../components/SseEventList'
import AggregateView from '../components/AggregateView'
import JsonTree from '../components/JsonTree'
import { fmtTime, fmtNum, fmtBytes, fmtMs, pickRouteQuery } from '../shared/format'
import { parseSSEEvents, aggregateSSE, aggregateToText, sseEventsToText } from '../shared/sse'
import { prettyJSON } from '../shared/json'

// 对话明细查询页（管理端 /ChatAnalysisInterface）
// 支持多条件筛选 + 分页 + 单条详情（按需拉取大字段）+ 批量删除
// v2.0.7x 阶段AM：新增「转发类型」徽标与筛选；详情 Modal 重构支持 JSON/SSE/聚合多视图。
const DAYS_OPTIONS = [
  { v: 0, t: '全部时间' }, { v: -1, t: '最近1小时' }, { v: -2, t: '最近2小时' },
  { v: -4, t: '最近4小时' }, { v: -6, t: '最近6小时' }, { v: -12, t: '最近12小时' },
  { v: 1, t: '最近1天' }, { v: 3, t: '最近3天' }, { v: 5, t: '最近5天' },
  { v: 7, t: '最近7天' }, { v: 14, t: '最近14天' }, { v: 30, t: '最近30天' },
  { v: 60, t: '最近60天' }, { v: 90, t: '最近90天' },
]
const PAGE_SIZES = [3, 5, 10, 15, 20, 50, 100]
// 详情字段白名单（与服务端 chatAnalysisDetailFieldColumns 对齐）
const DETAIL_FIELDS = [
  { key: 'request_body', title: '请求体（转发）' },
  { key: 'response_body', title: '响应体（转发）' },
  { key: 'request_src_protocol_body', title: '请求体（原始协议）' },
  { key: 'response_src_protocol_body', title: '响应体（原始协议）' },
  { key: 'request_headers', title: '请求头（转发）' },
  { key: 'response_headers', title: '响应头（转发）' },
]

// v2.0.7x 阶段AM：转发类型徽标工具。
// 与后端常量 DstEndPointAlgorithmType_Direct / DstEndPointAlgorithmType_ProtocolConverter 对齐。
const ALGO_TYPE_DIRECT = 1
const ALGO_TYPE_CONVERTER = 2
function protocolBadgeClass(v) {
  if (v === ALGO_TYPE_DIRECT) return 'protocol-badge direct'
  if (v === ALGO_TYPE_CONVERTER) return 'protocol-badge converter'
  return 'protocol-badge unknown'
}
function protocolBadgeText(v) {
  if (v === ALGO_TYPE_DIRECT) return '🔗 直连'
  if (v === ALGO_TYPE_CONVERTER) return '🔄 转换'
  return '未知'
}
function protocolBadgeTitle(v) {
  if (v === ALGO_TYPE_DIRECT) return '协议直连：转发协议 = 客户端协议（Anthropic↔Anthropic 或 OpenAI↔OpenAI）'
  if (v === ALGO_TYPE_CONVERTER) return '协议转换：转发协议 ≠ 客户端协议（Anthropic↔OpenAI 互转）'
  return '未知转发类型（数据异常或旧版本）'
}
function protocolBadge(v) {
  return (
    <span className={protocolBadgeClass(v)} title={protocolBadgeTitle(v)}>
      {protocolBadgeText(v)}
    </span>
  )
}

// 详情视图类型
const VIEW_RAW = 'raw'
const VIEW_JSON = 'json'
const VIEW_SSE = 'sse'
const VIEW_AGG = 'agg'
const VIEW_LABELS = { [VIEW_RAW]: '原文', [VIEW_JSON]: 'JSON 美化', [VIEW_SSE]: 'SSE 解析', [VIEW_AGG]: '聚合解析' }

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
  const [filterAlgorithmType, setFilterAlgorithmType] = useState(0) // 0=全部, 1=直连, 2=转换
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
  const [detailView, setDetailView] = useState(VIEW_RAW) // raw / json / sse / agg
  const [copyOk, setCopyOk] = useState(false)
  const [deleting, setDeleting] = useState(false)
  // 用户名/模型名级联下拉：管理端用 UserModelOptionsInterface（页面生命周期内缓存一次），用户端用本人模型列表
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()
  const modelOptions = isAdmin
    ? (userName.trim() ? modelNamesOf(userOptions, userName.trim()) : allModelNames(userOptions))
    : myModelNames

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
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 用户端进入页面未带模型：本人模型列表（缓存一次）到达后自动选第一个并查询
  useEffect(() => {
    if (isAdmin || !myModelNames.length || modelName) return
    const first = myModelNames[0]
    setModelName(first); setPage(1)
    loadOptions(first)
    doQuery(1, first)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [myModelNames])

  // 查询（modelOverride 用于自动查询时规避 setState 异步）
  const doQuery = async (p = page, modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (!isAdmin && !mn) { setError('请先选择模型'); return }
    if (isAdmin && (userName.trim() === '' || mn === '')) { setError('请先选择用户名和模型名'); return }
    setLoading(true); setError(''); setOkMsg(''); setSelected([])
    try {
      const d = await post('ChatAnalysisInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn,
        page: p, page_size: pageSize, days,
        filter_url: filterUrl.trim(), filter_method: filterMethod.trim(),
        filter_status: filterStatus.trim(), filter_status_not: filterStatusNot,
        filter_protocol_type: filterProtocolType, filter_algorithm_type: filterAlgorithmType,
        filter_dst_model_name: filterDstModel,
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
    setDetailRow(row); setDetailTab(field); setDetailView(VIEW_RAW); setCopyOk(false)
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
    if (!window.confirm(`确认删除选中的 ${selected.length} 条对话记录？此操作不可恢复！`)) return
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

  // 详情当前视图的"实际显示文本"（用于复制按钮 = "复制当前视图内容"，不再是原始 detailValue）
  const getShownContent = () => {
    const v = detailValue || ''
    if (!detailTab.includes('body')) return v
    if (detailView === VIEW_RAW) return v
    if (detailView === VIEW_JSON) return prettyJSON(v)
    if (detailView === VIEW_SSE) return sseEventsToText(parseSSEEvents(v))
    if (detailView === VIEW_AGG) return aggregateToText(aggregateSSE(v))
    return v
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
    // v2.0.7x 阶段AM：转发类型徽标列
    {
      key: 'dst_endpoint_algorithm_type', title: '转发类型', width: 110,
      render: (v) => protocolBadge(v),
    },
    { key: 'tokens_input_size', title: '输入 Tokens', render: (v) => fmtNum(v) },
    { key: 'tokens_output_size', title: '输出 Tokens', render: (v) => fmtNum(v) },
    { key: 'elapsed_ms', title: '耗时', render: (v) => fmtMs(v) },
    { key: 'agent_tool_name', title: 'Agent工具', render: (v) => v || '-' },
    { key: 'actions', title: '操作', render: (_, r) => (
      <button className="btn btn-sm" onClick={() => openDetail(r)}>详情</button>
    ) },
  ]

  // 协议名称辅助
  const protocolName = (v) => v === 1 ? 'Anthropic' : v === 2 ? 'OpenAI' : '-'

  // 详情头部元信息（阶段AP：卡片式网格 + 协议流向）
  const renderDetailHead = () => {
    if (!detailRow) return null
    const algoType = detailRow.dst_endpoint_algorithm_type
    const isConvert = algoType === ALGO_TYPE_CONVERTER
    const statusOk = String(detailRow.response_status).startsWith('2')
    return (
      <header className="detail-head">
        {/* 协议流向 */}
        <div className="detail-protocol-flow">
          <span className={detailRow.protocol_type === 1 ? 'protocol-badge protocol-anthropic' : detailRow.protocol_type === 2 ? 'protocol-badge protocol-openai' : 'protocol-badge unknown'}>
            {protocolName(detailRow.protocol_type)}
          </span>
          <span className="pf-arrow">→</span>
          <span className={protocolBadgeClass(algoType)} title={protocolBadgeTitle(algoType)}>
            {protocolBadgeText(algoType)}
          </span>
          {isConvert ? (
            <>
              <span className="pf-arrow">→</span>
              <span className="protocol-badge unknown">目标端</span>
            </>
          ) : null}
          <span className="pf-label">（ID #{detailRow.id}）</span>
        </div>

        {/* 指标网格 */}
        <div className="detail-head-grid">
          <div className="detail-head-card">
            <span className="dhc-label">⏱ 耗时</span>
            <span className="dhc-value">{fmtMs(detailRow.elapsed_ms)}</span>
          </div>
          <div className="detail-head-card">
            <span className="dhc-label">📥 输入 Tokens</span>
            <span className="dhc-value">{fmtNum(detailRow.tokens_input_size)}</span>
          </div>
          <div className="detail-head-card">
            <span className="dhc-label">📤 输出 Tokens</span>
            <span className="dhc-value">{fmtNum(detailRow.tokens_output_size)}</span>
          </div>
          <div className="detail-head-card">
            <span className="dhc-label">📦 请求 / 响应大小</span>
            <span className="dhc-value">{fmtBytes(detailRow.request_content_length)} / {fmtBytes(detailRow.response_content_length)}</span>
          </div>
        </div>

        {/* 请求信息行 */}
        <div className="detail-head-request">
          <span className="dhreq-method">{detailRow.request_method}</span>
          <span className="dhreq-url" title={detailRow.request_url}>{detailRow.request_url}</span>
          <span className={`dhreq-status ${statusOk ? 'ok' : 'err'}`}>{detailRow.response_status}</span>
          <span>{fmtTime(detailRow.created_at)}</span>
        </div>
      </header>
    )
  }

  // 详情主体渲染
  const renderDetailBody = () => {
    const isBody = detailTab.includes('body')
    if (detailLoading) return <div className="table-loading">字段内容加载中…</div>
    if (!isBody) return <pre className="log-box detail-content">{detailValue || '（空）'}</pre>
    if (detailView === VIEW_RAW) return <pre className="log-box detail-content">{detailValue || '（空）'}</pre>
    if (detailView === VIEW_JSON) return <div className="detail-content"><JsonTree value={detailValue} /></div>
    if (detailView === VIEW_SSE) return <div className="detail-content"><SseEventList events={parseSSEEvents(detailValue)} /></div>
    if (detailView === VIEW_AGG) return <div className="detail-content"><AggregateView result={aggregateSSE(detailValue)} /></div>
    return <pre className="log-box detail-content">{detailValue || '（空）'}</pre>
  }

  // 详情底部状态栏
  const renderDetailFoot = () => {
    if (!detailRow) return null
    const isBody = detailTab.includes('body')
    const byteSize = fmtBytes((detailValue || '').length)
    const lineCount = (detailValue || '').split('\n').length
    const copy = () => {
      navigator.clipboard.writeText(getShownContent() || '').then(() => {
        setCopyOk(true); setTimeout(() => setCopyOk(false), 1500)
      }).catch(() => {})
    }
    return (
      <footer className="detail-foot">
        <div className="detail-foot-meta">
          <span className="muted">字段</span>
          <span>{DETAIL_FIELDS.find((f) => f.key === detailTab)?.title}</span>
          {isBody ? <span className="muted">视图</span> : null}
          {isBody ? <span>{VIEW_LABELS[detailView]}</span> : null}
          <span className="muted">大小</span><span>{byteSize}</span>
          <span className="muted">行数</span><span>{lineCount}</span>
        </div>
        <div className="detail-foot-actions">
          <button className="btn btn-sm" onClick={copy}>{copyOk ? '已复制 ✓' : '复制当前视图'}</button>
        </div>
      </footer>
    )
  }

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
        {/* v2.0.7x 阶段AM：转发类型筛选 */}
        <label>转发类型
          <select value={filterAlgorithmType} onChange={(e) => setFilterAlgorithmType(Number(e.target.value))}>
            <option value={0}>全部</option>
            <option value={1}>🔗 协议直连</option>
            <option value={2}>🔄 协议转换</option>
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
        <label>输入 Tokens
          <select value={filterInTok} onChange={(e) => setFilterInTok(Number(e.target.value))}>
            <option value={0}>全部</option><option value={1}>非零</option><option value={2}>为零</option>
          </select>
        </label>
        <label>输出 Tokens
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

      <DataTable columns={columns} rows={rows} loading={loading} rowKey="id" empty="暂无数据（需选择用户名 + 模型名后查询）" />

      {totalPages > 0 ? (
        <div className="pager">
          <span>共 {fmtNum(total)} 条 · 第 {page} / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page <= 1 || loading} onClick={() => doQuery(page - 1)}>上一页</button>
          <span>第 {page} / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page >= totalPages || loading} onClick={() => doQuery(page + 1)}>下一页</button>
        </div>
      ) : null}

      {detailRow ? (
        <Modal title={`对话详情 #${detailRow.id} · ${protocolBadgeText(detailRow.dst_endpoint_algorithm_type)}`} width={960} onClose={() => setDetailRow(null)}>
          {renderDetailHead()}

          {/* 主 Tab：分组式 6 个字段 */}
          <nav className="detail-tabs">
            <div className="detail-tab-group">
              <span className="detail-tab-group-label">转发体</span>
              {DETAIL_FIELDS.filter(f => f.key === 'request_body' || f.key === 'response_body').map((f) => (
                <button key={f.key} className={`detail-tab${detailTab === f.key ? ' active' : ''}`}
                        onClick={() => openDetail(detailRow, f.key)}>{f.title}</button>
              ))}
            </div>
            <div className="detail-tab-group">
              <span className="detail-tab-group-label">原始体</span>
              {DETAIL_FIELDS.filter(f => f.key.includes('src_protocol')).map((f) => (
                <button key={f.key} className={`detail-tab${detailTab === f.key ? ' active' : ''}`}
                        onClick={() => openDetail(detailRow, f.key)}>{f.title}</button>
              ))}
            </div>
            <div className="detail-tab-group">
              <span className="detail-tab-group-label">头部</span>
              {DETAIL_FIELDS.filter(f => f.key.includes('headers')).map((f) => (
                <button key={f.key} className={`detail-tab${detailTab === f.key ? ' active' : ''}`}
                        onClick={() => openDetail(detailRow, f.key)}>{f.title}</button>
              ))}
            </div>
          </nav>

          {/* 仅 body 类字段显示视图子Tab（pill胶囊样式） */}
          {detailTab.includes('body') ? (
            <nav className="detail-views">
              {Object.entries(VIEW_LABELS).map(([v, label]) => (
                <button key={v} className={`detail-view${detailView === v ? ' active' : ''}`}
                        onClick={() => setDetailView(v)}>{label}</button>
              ))}
            </nav>
          ) : null}

          <main className="detail-body">
            {renderDetailBody()}
          </main>

          {renderDetailFoot()}
        </Modal>
      ) : null}
    </div>
  )
}