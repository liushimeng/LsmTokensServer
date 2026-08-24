import { useEffect, useState } from 'react'
import { post, get } from '../shared/api'
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

export default function ChatAnalysis({ route }) {
  const init = pickRouteQuery(route && route.query)
  // 筛选条件
  const [userName, setUserName] = useState(init.userName)
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
  const [deleting, setDeleting] = useState(false)

  const hasKey = userName.trim() !== '' && modelName.trim() !== ''

  // 拉取下拉选项：目标模型（依赖 user+model）+ Agent 工具（全站）
  const loadOptions = async () => {
    if (!hasKey) return
    try {
      const d = await post('ChatAnalysisDstModelsInterface', { user_name: userName.trim(), model_name: modelName.trim() })
      setDstModels(d.data || [])
    } catch { /* 静默失败，仅影响下拉选项 */ }
  }
  useEffect(() => {
    get('ChatAnalysisAgentToolsInterface').then((d) => setAgentTools(d.data || [])).catch(() => {})
    if (hasKey) { loadOptions(); setPage(1); doQuery(1) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 查询
  const doQuery = async (p = page) => {
    if (!hasKey) { setError('请先填写用户名和模型名'); return }
    setLoading(true); setError(''); setOkMsg(''); setSelected([])
    try {
      const d = await post('ChatAnalysisInterface', {
        user_name: userName.trim(), model_name: modelName.trim(),
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
    setDetailRow(row); setDetailTab(field)
    setDetailValue(''); setDetailLoading(false)
    if (detailCache[`${row.id}:${field}`] !== undefined) {
      setDetailValue(detailCache[`${row.id}:${field}`]); return
    }
    setDetailLoading(true)
    try {
      const d = await post('ChatAnalysisDetailInterface', {
        id: row.id, user_name: userName.trim(), model_name: modelName.trim(), field,
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
    { key: 'check', title: <input type="checkbox" checked={rows.length > 0 && selected.length === rows.length} onChange={(e) => toggleAll(e.target.checked)} />, render: (_, r) => (
      <input type="checkbox" checked={selected.includes(r.id)} onChange={(e) => toggleOne(r.id, e.target.checked)} />
    ) },
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
        <label>用户名 <input value={userName} onChange={(e) => setUserName(e.target.value)} placeholder="user_name" style={{ width: 130 }} /></label>
        <label>模型名 <input value={modelName} onChange={(e) => setModelName(e.target.value)} placeholder="model_name" style={{ width: 150 }} /></label>
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
        <button className="btn btn-danger" onClick={batchDelete} disabled={!selected.length || deleting}>
          {deleting ? '删除中…' : `批量删除(${selected.length})`}
        </button>
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
            : <pre className="log-box">{detailValue || '（空）'}</pre>}
        </Modal>
      ) : null}
    </div>
  )
}
