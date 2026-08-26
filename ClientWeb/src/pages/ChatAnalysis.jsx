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
import { useI18n } from '../i18n'

// 对话明细查询页（管理端 /ChatAnalysisInterface）
// 支持多条件筛选 + 分页 + 单条详情（按需拉取大字段）+ 批量删除
// v2.0.7x 阶段AM：新增「转发类型」徽标与筛选；详情 Modal 重构支持 JSON/SSE/聚合多视图。
const DAYS_OPTIONS = [
  { v: 0, t: 'allTime' }, { v: -1, t: 'lastNHours', vars: { n: 1 } }, { v: -2, t: 'lastNHours', vars: { n: 2 } },
  { v: -4, t: 'lastNHours', vars: { n: 4 } }, { v: -6, t: 'lastNHours', vars: { n: 6 } }, { v: -12, t: 'lastNHours', vars: { n: 12 } },
  { v: 1, t: 'lastNDays', vars: { n: 1 } }, { v: 3, t: 'lastNDays', vars: { n: 3 } }, { v: 5, t: 'lastNDays', vars: { n: 5 } },
  { v: 7, t: 'lastNDays', vars: { n: 7 } }, { v: 14, t: 'lastNDays', vars: { n: 14 } }, { v: 30, t: 'lastNDays', vars: { n: 30 } },
  { v: 60, t: 'lastNDays', vars: { n: 60 } }, { v: 90, t: 'lastNDays', vars: { n: 90 } },
]
const PAGE_SIZES = [3, 5, 10, 15, 20, 50, 100]
// 详情字段白名单（与服务端 chatAnalysisDetailFieldColumns 对齐）
const DETAIL_FIELDS = [
  { key: 'request_body', titleKey: 'chatAnalysis.requestBody' },
  { key: 'response_body', titleKey: 'chatAnalysis.responseBody' },
  { key: 'request_src_protocol_body', titleKey: 'chatAnalysis.requestSrcProtocolBody' },
  { key: 'response_src_protocol_body', titleKey: 'chatAnalysis.responseSrcProtocolBody' },
  { key: 'request_headers', titleKey: 'chatAnalysis.requestHeaders' },
  { key: 'response_headers', titleKey: 'chatAnalysis.responseHeaders' },
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
function protocolBadgeText(v, t) {
  if (v === ALGO_TYPE_DIRECT) return t('chatAnalysis.direct')
  if (v === ALGO_TYPE_CONVERTER) return t('chatAnalysis.converter')
  return t('chatAnalysis.unknown')
}
function protocolBadgeTitle(v, t) {
  if (v === ALGO_TYPE_DIRECT) return t('chatAnalysis.directTooltip')
  if (v === ALGO_TYPE_CONVERTER) return t('chatAnalysis.converterTooltip')
  return t('chatAnalysis.unknownTooltip')
}
function protocolBadge(v, t) {
  return (
    <span className={protocolBadgeClass(v)} title={protocolBadgeTitle(v, t)}>
      {protocolBadgeText(v, t)}
    </span>
  )
}

// 详情视图类型
const VIEW_RAW = 'raw'
const VIEW_JSON = 'json'
const VIEW_SSE = 'sse'
const VIEW_AGG = 'agg'

export default function ChatAnalysis({ route }) {
  const { t } = useI18n()
  const VIEW_LABELS = { [VIEW_RAW]: t('chatAnalysis.raw'), [VIEW_JSON]: t('chatAnalysis.jsonBeautify'), [VIEW_SSE]: t('chatAnalysis.sseParse'), [VIEW_AGG]: t('chatAnalysis.aggParse') }

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
    if (!isAdmin && !mn) { setError(t('chatAnalysis.pleaseSelectModel')); return }
    if (isAdmin && (userName.trim() === '' || mn === '')) { setError(t('chatAnalysis.pleaseSelectUserAndModel')); return }
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
      setError(e.message || t('chatAnalysis.queryFailed'))
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
      setDetailValue(t('chatAnalysis.loadFailed') + (e.message || ''))
    } finally { setDetailLoading(false) }
  }

  // 批量删除（最多 500 条/次，服务端限制）
  const batchDelete = async () => {
    if (!selected.length) return
    if (!window.confirm(t('chatAnalysis.deleteConfirm', { count: selected.length }))) return
    setDeleting(true); setError(''); setOkMsg('')
    try {
      const d = await post('ChatAnalysisBatchDeleteInterface', {
        user_name: userName.trim(), model_name: modelName.trim(), ids: selected,
      })
      setOkMsg(d.message || t('chatAnalysis.deleteCompleted'))
      setSelected([])
      doQuery(page)
    } catch (e) {
      setError(e.message || t('chatAnalysis.deleteFailed'))
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
    { key: 'id', title: t('chatAnalysis.id'), width: 90 },
    { key: 'created_at', title: t('chatAnalysis.time'), render: (v) => fmtTime(v) },
    { key: 'request_method', title: t('chatAnalysis.method'), width: 70 },
    { key: 'request_url', title: t('chatAnalysis.url'), render: (v) => <span title={v}>{String(v || '').length > 60 ? String(v).slice(0, 60) + '…' : v}</span> },
    { key: 'response_status', title: t('chatAnalysis.status'), render: (v) => {
      const ok = String(v).startsWith('2')
      return <span style={{ color: ok ? 'var(--ok)' : 'var(--danger)' }}>{v || '-'}</span>
    } },
    { key: 'dst_model_name', title: t('chatAnalysis.dstModel') },
    // v2.0.7x 阶段AM：转发类型徽标列
    {
      key: 'dst_endpoint_algorithm_type', title: t('chatAnalysis.forwardType'), width: 110,
      render: (v) => protocolBadge(v, t),
    },
    { key: 'tokens_input_size', title: t('chatAnalysis.inputTokens'), render: (v) => fmtNum(v) },
    { key: 'tokens_output_size', title: t('chatAnalysis.outputTokens'), render: (v) => fmtNum(v) },
    { key: 'elapsed_ms', title: t('chatAnalysis.duration'), render: (v) => fmtMs(v) },
    { key: 'agent_tool_name', title: t('chatAnalysis.agentTool'), render: (v) => v || '-' },
    { key: 'actions', title: t('chatAnalysis.action'), render: (_, r) => (
      <button className="btn btn-sm" onClick={() => openDetail(r)}>{t('chatAnalysis.detail')}</button>
    ) },
  ]

  // 协议名称辅助
  const protocolName = (v) => v === 1 ? t('chatAnalysis.anthropic') : v === 2 ? t('chatAnalysis.openai') : '-'

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
          <span className={protocolBadgeClass(algoType)} title={protocolBadgeTitle(algoType, t)}>
            {protocolBadgeText(algoType, t)}
          </span>
          {isConvert ? (
            <>
              <span className="pf-arrow">→</span>
              <span className="protocol-badge unknown">{t('chatAnalysis.target')}</span>
            </>
          ) : null}
          <span className="pf-label">{t('chatAnalysis.idWithHash', { id: detailRow.id })}</span>
        </div>

        {/* 指标网格 */}
        <div className="detail-head-grid">
          <div className="detail-head-card">
            <span className="dhc-label">⏱ {t('chatAnalysis.elapsed')}</span>
            <span className="dhc-value">{fmtMs(detailRow.elapsed_ms)}</span>
          </div>
          <div className="detail-head-card">
            <span className="dhc-label">📥 {t('chatAnalysis.inputTokensCard')}</span>
            <span className="dhc-value">{fmtNum(detailRow.tokens_input_size)}</span>
          </div>
          <div className="detail-head-card">
            <span className="dhc-label">📤 {t('chatAnalysis.outputTokensCard')}</span>
            <span className="dhc-value">{fmtNum(detailRow.tokens_output_size)}</span>
          </div>
          <div className="detail-head-card">
            <span className="dhc-label">📦 {t('chatAnalysis.reqRespSize')}</span>
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
    if (detailLoading) return <div className="table-loading">{t('chatAnalysis.fieldLoading')}</div>
    if (!isBody) return <pre className="log-box detail-content">{detailValue || t('chatAnalysis.emptyContent')}</pre>
    if (detailView === VIEW_RAW) return <pre className="log-box detail-content">{detailValue || t('chatAnalysis.emptyContent')}</pre>
    if (detailView === VIEW_JSON) return <div className="detail-content"><JsonTree value={detailValue} /></div>
    if (detailView === VIEW_SSE) return <div className="detail-content"><SseEventList events={parseSSEEvents(detailValue)} /></div>
    if (detailView === VIEW_AGG) return <div className="detail-content"><AggregateView result={aggregateSSE(detailValue)} /></div>
    return <pre className="log-box detail-content">{detailValue || t('chatAnalysis.emptyContent')}</pre>
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
          <span className="muted">{t('chatAnalysis.field')}</span>
          <span>{DETAIL_FIELDS.find((f) => f.key === detailTab)?.titleKey ? t(DETAIL_FIELDS.find((f) => f.key === detailTab).titleKey) : detailTab}</span>
          {isBody ? <span className="muted">{t('chatAnalysis.view')}</span> : null}
          {isBody ? <span>{VIEW_LABELS[detailView]}</span> : null}
          <span className="muted">{t('chatAnalysis.size')}</span><span>{byteSize}</span>
          <span className="muted">{t('chatAnalysis.lines')}</span><span>{lineCount}</span>
        </div>
        <div className="detail-foot-actions">
          <button className="btn btn-sm" onClick={copy}>{copyOk ? t('common.copied') + ' ✓' : t('chatAnalysis.copyView')}</button>
        </div>
      </footer>
    )
  }

  return (
    <div className="page">
      <h2 className="page-title">{t('chatAnalysis.title')}</h2>

      <div className="toolbar">
        {isAdmin ? <label>{t('chatAnalysis.userNameLabel')}
          <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 140 }}>
            <option value="">{t('chatAnalysis.selectUser')}</option>
            {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
          </select>
        </label> : null}
        <label>{t('chatAnalysis.modelNameLabel')}
          <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
            <option value="">{t('chatAnalysis.selectModel')}</option>
            {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysis.timeRange')}
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {DAYS_OPTIONS.map((o) => <option key={o.v} value={o.v}>{t(`chatAnalysis.${o.t}`, o.vars)}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysis.perPage')}
          <select value={pageSize} onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1) }}>
            {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysis.protocol')}
          <select value={filterProtocolType} onChange={(e) => setFilterProtocolType(Number(e.target.value))}>
            <option value={0}>{t('chatAnalysis.all')}</option>
            <option value={1}>{t('chatAnalysis.anthropic')}</option>
            <option value={2}>{t('chatAnalysis.openai')}</option>
          </select>
        </label>
        {/* v2.0.7x 阶段AM：转发类型筛选 */}
        <label>{t('chatAnalysis.forwardType')}
          <select value={filterAlgorithmType} onChange={(e) => setFilterAlgorithmType(Number(e.target.value))}>
            <option value={0}>{t('chatAnalysis.all')}</option>
            <option value={1}>{t('chatAnalysis.protocolDirect')}</option>
            <option value={2}>{t('chatAnalysis.protocolConverter')}</option>
          </select>
        </label>
        <label>{t('chatAnalysis.dstModel')}
          <select value={filterDstModel} onChange={(e) => setFilterDstModel(e.target.value)}>
            <option value="">{t('chatAnalysis.all')}</option>
            {dstModels.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysis.agentTool')}
          <select value={filterAgentTool} onChange={(e) => setFilterAgentTool(e.target.value)}>
            <option value="">{t('chatAnalysis.all')}</option>
            {agentTools.map((tt) => <option key={tt} value={tt}>{tt}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysis.method')} <input value={filterMethod} onChange={(e) => setFilterMethod(e.target.value)} placeholder={t('chatAnalysis.methodPlaceholder')} style={{ width: 80 }} /></label>
        <label>{t('chatAnalysis.status')} <input value={filterStatus} onChange={(e) => setFilterStatus(e.target.value)} placeholder={t('chatAnalysis.statusPlaceholder')} style={{ width: 80 }} /></label>
        <label className="field-check" style={{ margin: 0 }}>
          <input type="checkbox" checked={filterStatusNot} onChange={(e) => setFilterStatusNot(e.target.checked)} />{t('chatAnalysis.invert')}
        </label>
        <label>{t('chatAnalysis.inputTokens')}
          <select value={filterInTok} onChange={(e) => setFilterInTok(Number(e.target.value))}>
            <option value={0}>{t('chatAnalysis.all')}</option><option value={1}>{t('chatAnalysis.nonZero')}</option><option value={2}>{t('chatAnalysis.zero')}</option>
          </select>
        </label>
        <label>{t('chatAnalysis.outputTokens')}
          <select value={filterOutTok} onChange={(e) => setFilterOutTok(Number(e.target.value))}>
            <option value={0}>{t('chatAnalysis.all')}</option><option value={1}>{t('chatAnalysis.nonZero')}</option><option value={2}>{t('chatAnalysis.zero')}</option>
          </select>
        </label>
        <label>{t('chatAnalysis.url')} <input value={filterUrl} onChange={(e) => setFilterUrl(e.target.value)} placeholder={t('chatAnalysis.urlContains')} style={{ width: 150 }} /></label>
        <label>{t('chatAnalysis.toolsContain')} <input value={filterTools} onChange={(e) => setFilterTools(e.target.value)} placeholder={t('chatAnalysis.toolsContain')} style={{ width: 120 }} /></label>
        <button className="btn btn-primary" onClick={() => { setPage(1); doQuery(1) }} disabled={loading}>{t('chatAnalysis.query')}</button>
        {isAdmin ? <button className="btn btn-danger" onClick={batchDelete} disabled={!selected.length || deleting}>
          {deleting ? t('chatAnalysis.deleting') : `${t('chatAnalysis.batchDelete')}(${selected.length})`}
        </button> : null}
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}
      {okMsg ? <div className="alert alert-ok">{okMsg}</div> : null}

      <DataTable columns={columns} rows={rows} loading={loading} rowKey="id" empty={t('chatAnalysis.empty')} />

      {totalPages > 0 ? (
        <div className="pager">
          <span>{t('chatAnalysis.totalRecords', { count: fmtNum(total), page, totalPages })}</span>
          <button className="btn btn-sm" disabled={page <= 1 || loading} onClick={() => doQuery(page - 1)}>{t('chatAnalysis.prevPage')}</button>
          <span>{t('chatAnalysis.pageInfo', { page, totalPages })}</span>
          <button className="btn btn-sm" disabled={page >= totalPages || loading} onClick={() => doQuery(page + 1)}>{t('chatAnalysis.nextPage')}</button>
        </div>
      ) : null}

      {detailRow ? (
        <Modal title={`${t('chatAnalysis.conversationDetail')} #${detailRow.id} · ${protocolBadgeText(detailRow.dst_endpoint_algorithm_type, t)}`} width={960} onClose={() => setDetailRow(null)}>
          {renderDetailHead()}

          {/* 主 Tab：分组式 6 个字段 */}
          <nav className="detail-tabs">
            <div className="detail-tab-group">
              <span className="detail-tab-group-label">{t('chatAnalysis.forwardBody')}</span>
              {DETAIL_FIELDS.filter(f => f.key === 'request_body' || f.key === 'response_body').map((f) => (
                <button key={f.key} className={`detail-tab${detailTab === f.key ? ' active' : ''}`}
                        onClick={() => openDetail(detailRow, f.key)}>{t(f.titleKey)}</button>
              ))}
            </div>
            <div className="detail-tab-group">
              <span className="detail-tab-group-label">{t('chatAnalysis.rawBody')}</span>
              {DETAIL_FIELDS.filter(f => f.key.includes('src_protocol')).map((f) => (
                <button key={f.key} className={`detail-tab${detailTab === f.key ? ' active' : ''}`}
                        onClick={() => openDetail(detailRow, f.key)}>{t(f.titleKey)}</button>
              ))}
            </div>
            <div className="detail-tab-group">
              <span className="detail-tab-group-label">{t('chatAnalysis.headers')}</span>
              {DETAIL_FIELDS.filter(f => f.key.includes('headers')).map((f) => (
                <button key={f.key} className={`detail-tab${detailTab === f.key ? ' active' : ''}`}
                        onClick={() => openDetail(detailRow, f.key)}>{t(f.titleKey)}</button>
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
