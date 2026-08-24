import { useCallback, useEffect, useMemo, useState } from 'react'
import { get, post } from '../shared/api'
import { fmtTime } from '../shared/format'
import DataTable from '../components/DataTable'

// 协议转换分析器（实验性页面，迁移自旧 server_web_manager_protocol_converter*.go）
// 功能：全局开关 / 转换测试（四段）/ 记录列表+筛选分页 / 记录详情 / 用户统计 / 映射知识库

// 四个数据段定义（与后端 ProtocolAnalyzerSection* 一致）
const SECTIONS = [
  ['request_headers', 'Request Header'],
  ['request_body', 'Request Body'],
  ['response_headers', 'Response Header'],
  ['response_body', 'Response Body'],
]

const KB_GROUPS = [
  ['request_fields', '请求体字段映射'],
  ['response_fields', '响应体字段映射'],
  ['role_mapping', '消息角色映射'],
  ['content_block_mapping', '内容块映射'],
  ['finish_reason_mapping', '停止原因映射'],
  ['sse_event_mapping', 'SSE 事件映射'],
  ['tool_use_mapping', '工具调用映射'],
  ['request_header_fields', '请求头映射'],
  ['response_header_fields', '响应头映射'],
]

const DAYS_OPTIONS = [0, 1, 3, 7, 15, 30, 60, 90]

// 单段转换：复刻旧页面 convertOneSection 逻辑（headers/sse 走 text_input，其余走 input）
async function convertOneSection(input, direction, section, isStream) {
  const pair = { input: input || '', output: '', warnings: [], error: '' }
  if (!String(input || '').trim()) return pair
  const format = isStream && section === 'response_body' ? 'sse' : 'json'
  const payload = { direction, section, format, is_stream: !!isStream }
  if (section.indexOf('headers') >= 0 || format === 'sse') {
    payload.text_input = input
    payload.input = {}
  } else {
    try {
      payload.input = JSON.parse(input)
    } catch (e) {
      pair.error = '输入不是有效的 JSON: ' + e.message
      return pair
    }
  }
  try {
    const d = await post('ProtocolConvertAnalyzerTest', payload)
    pair.warnings = d.warnings || []
    pair.format = d.format
    if (d.format === 'headers') {
      pair.output = d.text || ''
    } else if (d.output != null) {
      pair.output = JSON.stringify(d.output, null, 2)
      pair.metrics = d.metrics || null
    }
  } catch (e) {
    pair.error = e.message || '转换失败'
  }
  return pair
}

// 指标进度条（复刻旧版 renderMetrics 的核心部分）
function MetricsPanel({ metrics }) {
  if (!metrics) return null
  const bar = (label, value, extra) => {
    const pct = ((value || 0) * 100).toFixed(1)
    const cls = (value || 0) >= 0.8 ? '#a6e3a1' : (value || 0) >= 0.5 ? '#f9e2af' : '#f38ba8'
    return (
      <div style={{ marginBottom: 8 }}>
        <div style={{ fontSize: 12, color: '#89b4fa', marginBottom: 2 }}>{label}</div>
        <div style={{ height: 20, background: '#313244', borderRadius: 10, overflow: 'hidden' }}>
          <div style={{ width: pct + '%', minWidth: '40px', height: '100%', background: cls, borderRadius: 10,
            display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontWeight: 600, color: '#1e1e2e' }}>
            {pct}%{extra || ''}
          </div>
        </div>
      </div>
    )
  }
  return (
    <div style={{ background: '#1e1e2e', borderRadius: 8, padding: 14, color: '#cdd6f4', marginTop: 12 }}>
      <h4 style={{ margin: '0 0 10px', color: '#89b4fa' }}>协议转换率分析</h4>
      {bar('结构转换成功率 — JSON解析+转换+序列化的整体成功率', metrics.structure_success_rate)}
      {bar('字段转换率 — 输出中实际包含的转换后字段占比', metrics.field_conversion_rate)}
      {bar('综合转换率 (字段覆盖率×60% + 语义映射率×40%)', metrics.conversion_rate)}
      {bar('字段覆盖率 — 输入顶级字段中被目标协议支持的比例', metrics.field_coverage_rate,
        ` (${metrics.mapped_top_level_count || 0}/${metrics.input_top_level_count || 0})`)}
      {bar('语义映射率 — 所有字段路径的映射成功率', metrics.semantic_mapping_rate,
        ` (${metrics.converted_fields || 0}/${metrics.total_input_fields || 0})`)}
      <div style={{ display: 'flex', gap: 14, flexWrap: 'wrap', fontSize: 12, marginTop: 10 }}>
        <span>JSON解析: {metrics.parsed_ok ? '✓ 成功' : '✗ 失败'}</span>
        <span>转换执行: {metrics.converted_ok ? '✓ 成功' : '✗ 失败'}</span>
        <span>输出有效: {metrics.output_valid ? '✓ 有效' : '✗ 无效'}</span>
      </div>
      {(metrics.unmapped_fields || []).length > 0 && (
        <div style={{ marginTop: 8, fontSize: 12 }}>
          <div style={{ color: '#f38ba8' }}>未被目标协议采纳的字段:</div>
          {metrics.unmapped_fields.map((f) => (
            <span key={f} style={{ background: '#f38ba833', color: '#f38ba8', padding: '2px 8px', borderRadius: 4, margin: 2, display: 'inline-block' }}>{f}</span>
          ))}
        </div>
      )}
      {(metrics.target_extra_fields || []).length > 0 && (
        <div style={{ marginTop: 8, fontSize: 12 }}>
          <div style={{ color: '#a6e3a1' }}>目标协议支持但输入未提供的字段:</div>
          {metrics.target_extra_fields.map((f) => (
            <span key={f} style={{ background: '#a6e3a133', color: '#a6e3a1', padding: '2px 8px', borderRadius: 4, margin: 2, display: 'inline-block' }}>{f}</span>
          ))}
        </div>
      )}
    </div>
  )
}

export default function ProtocolConvertAnalyzer() {
  // ===== 全局开关状态 =====
  const [enabled, setEnabled] = useState(false)
  const [isAdmin, setIsAdmin] = useState(false)
  const [tab, setTab] = useState('test')

  // ===== 筛选与记录列表 =====
  const [userName, setUserName] = useState('')
  const [modelName, setModelName] = useState('')
  const [protocolType, setProtocolType] = useState('0')
  const [days, setDays] = useState('3')
  const [page, setPage] = useState(1)
  const [total, setTotal] = useState(0)
  const [records, setRecords] = useState(null) // null=加载中
  const [users, setUsers] = useState([]) // 用户统计（仅管理端）

  // ===== 转换测试状态（四段） =====
  const [inputs, setInputs] = useState({ request_headers: '', request_body: '', response_headers: '', response_body: '' })
  const [outputs, setOutputs] = useState({})
  const [direction, setDirection] = useState('o2a')
  const [isStream, setIsStream] = useState(false)
  const [converting, setConverting] = useState(false)
  const [metrics, setMetrics] = useState(null)
  const [testMsg, setTestMsg] = useState('')
  const [testErr, setTestErr] = useState('')
  const [selectedId, setSelectedId] = useState(null)
  const [detailErr, setDetailErr] = useState('')

  // ===== 映射知识库 =====
  const [mapping, setMapping] = useState(null)

  // 初始加载：角色探测 + 状态 + 记录 + 映射
  useEffect(() => {
    // 管理端 mux 独有 Toggle 接口；GET 在管理端返回 405（POST-only），
    // 用户端无该接口会回落到 SPA 首页返回 200，据此区分管理/用户角色。
    fetch('ProtocolConvertAnalyzerToggle', { credentials: 'include' })
      .then((r) => setIsAdmin(r.status === 405))
      .catch(() => setIsAdmin(false))
    get('ProtocolConvertAnalyzerStatus').then((d) => setEnabled(!!d.enabled)).catch(() => {})
    get('ProtocolConvertAnalyzerMapping').then(setMapping).catch(() => {})
  }, [])

  // 用户统计（仅管理端，用于筛选下拉）
  useEffect(() => {
    if (!isAdmin) return
    get('ProtocolConvertAnalyzerUsers').then((d) => setUsers(d.users || [])).catch(() => {})
  }, [isAdmin])

  // 模型下拉选项：选中用户时仅显示该用户的模型，否则显示全部模型并集
  const modelOptions = useMemo(() => {
    const set = new Set()
    users.forEach((u) => {
      if (!userName || u.user_name === userName) (u.model_names || []).forEach((m) => set.add(m))
    })
    return Array.from(set).sort()
  }, [users, userName])

  // 加载记录列表
  const loadRecords = useCallback((p) => {
    setPage(p)
    setRecords(null)
    const params = new URLSearchParams({ page: p, page_size: 10, protocol_type: protocolType, days })
    if (userName) params.set('user_name', userName)
    if (modelName) params.set('model_name', modelName)
    get('ProtocolConvertAnalyzerRecords?' + params.toString())
      .then((d) => { setRecords(d.records || []); setTotal(d.total || 0) })
      .catch(() => setRecords([]))
  }, [protocolType, userName, modelName, days])

  useEffect(() => { loadRecords(1) }, [loadRecords])

  // 切换全局开关
  const toggle = async () => {
    try {
      const d = await post('ProtocolConvertAnalyzerToggle', { enabled: !enabled })
      setEnabled(!!d.enabled)
    } catch (e) { alert('切换失败: ' + e.message) }
  }

  // 选择记录 → 按需加载详情（后端已 base64 解码并脱敏）
  const selectRecord = async (rec) => {
    setSelectedId(rec.id)
    setOutputs({}); setMetrics(null); setTestMsg(''); setTestErr(''); setDetailErr('')
    setDirection(rec.protocol_type === 1 ? 'a2o' : 'o2a')
    setIsStream(!!rec.is_stream)
    setInputs({ request_headers: '详情加载中...', request_body: '详情加载中...',
      response_headers: '详情加载中...', response_body: '详情加载中...' })
    try {
      const params = new URLSearchParams({ id: rec.id })
      if (userName !== undefined && rec.user_name) params.set('user_name', rec.user_name)
      if (rec.model_name) params.set('model_name', rec.model_name)
      const d = await get('ProtocolConvertAnalyzerRecordDetail?' + params.toString())
      const det = d.detail || {}
      setInputs({
        request_headers: det.request_headers || '',
        request_body: det.request_src_protocol_body || det.request_body || '',
        response_headers: det.response_headers || '',
        response_body: det.response_src_protocol_body || det.response_body || '',
      })
    } catch (e) {
      setInputs({ request_headers: '', request_body: '', response_headers: '', response_body: '' })
      setDetailErr('详情加载失败: ' + e.message)
    }
  }

  // 执行转换：前端并发四次调用 Test 接口（四段独立转换）
  const doConvert = async () => {
    setConverting(true); setTestMsg(''); setTestErr(''); setMetrics(null)
    const jobs = await Promise.all(SECTIONS.map(([sec]) =>
      convertOneSection(inputs[sec], direction, sec, isStream)))
    const out = {}
    let allWarnings = []
    let m = null
    jobs.forEach((pair, i) => {
      out[SECTIONS[i][0]] = pair
      allWarnings = allWarnings.concat(pair.warnings || [])
      if (!m && pair.metrics) m = pair.metrics
    })
    setOutputs(out)
    setMetrics(m)
    if (allWarnings.length) setTestErr('转换提示: ' + allWarnings.join('；'))
    const failed = jobs.some((p) => p.error)
    setTestMsg(failed ? '部分段落转换失败' : '转换完成')
    setConverting(false)
  }

  const totalPages = Math.max(1, Math.ceil(total / 10))

  const recordColumns = [
    { key: 'id', title: 'ID', width: 70, render: (v) => <code>{v}</code> },
    { key: 'created_at', title: '时间', render: (v) => fmtTime(v) },
    { key: 'user_name', title: '用户' },
    { key: 'model_name', title: '模型' },
    { key: 'protocol_type', title: '协议', render: (v) => v === 1 ? 'Anthropic' : 'OpenAI' },
    { key: 'request_url', title: 'URL', render: (v) => (
      <span className="wrap" title={v}>{v && v.length > 50 ? v.slice(0, 50) + '…' : (v || '-')}</span>
    ) },
    { key: 'is_stream', title: '流式', render: (v) => (v ? '是' : '否') },
    { key: 'op', title: '操作', render: (_, rec) => (
      <button className={'btn btn-sm' + (selectedId === rec.id ? ' btn-primary' : '')}
              onClick={() => selectRecord(rec)}>选择</button>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">协议转换分析器（实验性）</h2>

      {/* 全局开关 + 状态徽标 */}
      <div className="card">
        <div className="toolbar" style={{ marginBottom: 0 }}>
          <span>当前状态：</span>
          <span className="tag" style={{ background: enabled ? '#f0fdf4' : '#fef2f2',
            color: enabled ? '#15803d' : '#b91c1c' }}>{enabled ? '已启用' : '已禁用'}</span>
          {isAdmin ? (
            <button className="btn btn-primary" onClick={toggle}>{enabled ? '禁用' : '启用'}</button>
          ) : <span style={{ color: 'var(--muted)', fontSize: 12 }}>仅管理端可切换</span>}
          <span style={{ marginLeft: 'auto', color: 'var(--muted)', fontSize: 12 }}>
            实验性验证页面：真实的协议转换器功能尚未正式上线，所有请求仍按原始协议直接转发。
          </span>
        </div>
        {isAdmin && users.length > 0 && (
          <p style={{ margin: '10px 0 0', fontSize: 12, color: 'var(--muted)' }}>
            用户统计：共 {users.length} 个用户、{users.reduce((n, u) => n + (u.model_names || []).length, 0)} 个模型。
          </p>
        )}
      </div>

      {/* Tab 切换 */}
      <div className="card">
        <div style={{ display: 'flex', gap: 6, borderBottom: '1px solid var(--border)', marginBottom: 14, paddingBottom: 10 }}>
          <button className={'btn btn-sm' + (tab === 'test' ? ' btn-primary' : '')} onClick={() => setTab('test')}>转换测试</button>
          <button className={'btn btn-sm' + (tab === 'records' ? ' btn-primary' : '')} onClick={() => setTab('records')}>转换记录</button>
          <button className={'btn btn-sm' + (tab === 'mapping' ? ' btn-primary' : '')} onClick={() => setTab('mapping')}>字段映射表</button>
        </div>

        {/* ===== 转换测试 / 记录共用转换面板 ===== */}
        {tab !== 'mapping' && (
          <>
            {/* 记录筛选（测试与记录两个 Tab 共用筛选加载逻辑，记录表格仅在 records Tab 展开） */}
            {tab === 'records' && (
              <div className="toolbar">
                {isAdmin && (
                  <>
                    <label>用户筛选</label>
                    <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }}>
                      <option value="">全部用户</option>
                      {users.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
                    </select>
                  </>
                )}
                <label>模型筛选</label>
                <select value={modelName} onChange={(e) => setModelName(e.target.value)}>
                  <option value="">全部模型</option>
                  {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
                </select>
                <label>数据来源</label>
                <select value={protocolType} onChange={(e) => setProtocolType(e.target.value)}>
                  <option value="0">全部协议</option>
                  <option value="1">Anthropic</option>
                  <option value="2">OpenAI</option>
                </select>
                <label>时间范围</label>
                <select value={days} onChange={(e) => setDays(e.target.value)}>
                  {DAYS_OPTIONS.map((d) => (
                    <option key={d} value={String(d)}>{d === 0 ? '无限制时间' : `最近${d}天`}</option>
                  ))}
                </select>
                <span style={{ color: 'var(--muted)', fontSize: 12 }}>共 {total} 条记录</span>
              </div>
            )}
            {tab === 'records' && (
              <>
                <DataTable columns={recordColumns} rows={records || []} loading={!records} empty="暂无记录" rowKey="id" />
                <div className="pager">
                  <button className="btn btn-sm" disabled={page <= 1} onClick={() => loadRecords(page - 1)}>上一页</button>
                  <span>第 {page} / {totalPages} 页</span>
                  <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => loadRecords(page + 1)}>下一页</button>
                </div>
              </>
            )}

            {/* 四段转换面板 */}
            {detailErr ? <div className="alert alert-error">{detailErr}</div> : null}
            <div className="toolbar">
              <label>转换方向</label>
              <select value={direction} onChange={(e) => setDirection(e.target.value)}>
                <option value="o2a">OpenAI → Anthropic</option>
                <option value="a2o">Anthropic → OpenAI</option>
              </select>
              <label className="field-check" style={{ margin: 0 }}>
                <input type="checkbox" checked={isStream} onChange={(e) => setIsStream(e.target.checked)} /> 流式（SSE）
              </label>
              <button className="btn btn-primary" onClick={doConvert} disabled={converting}>
                {converting ? '转换中…' : '执行转换'}
              </button>
              {selectedId ? <span style={{ color: 'var(--muted)', fontSize: 12 }}>已选记录 #{selectedId}</span> : null}
            </div>
            {testMsg ? <div className="alert alert-ok">{testMsg}</div> : null}
            {testErr ? <div className="alert alert-error">{testErr}</div> : null}
            <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fit, minmax(420px, 1fr))', gap: 16 }}>
              {/* 输入面板 */}
              <div className="card" style={{ margin: 0 }}>
                <h3>输入（可手动编辑或从记录载入）</h3>
                {SECTIONS.map(([sec, label]) => (
                  <div key={sec} style={{ marginBottom: 10 }}>
                    <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--muted)', marginBottom: 4 }}>{label}</div>
                    <textarea className="log-box" style={{ minHeight: 100, maxHeight: 260 }} value={inputs[sec]}
                      onChange={(e) => setInputs({ ...inputs, [sec]: e.target.value })} />
                  </div>
                ))}
              </div>
              {/* 输出面板 */}
              <div className="card" style={{ margin: 0 }}>
                <h3>输出</h3>
                {SECTIONS.map(([sec, label]) => {
                  const pair = outputs[sec]
                  return (
                    <div key={sec} style={{ marginBottom: 10 }}>
                      <div style={{ fontSize: 12, fontWeight: 600, color: 'var(--muted)', marginBottom: 4 }}>{label}</div>
                      <div className="log-box" style={{
                        background: pair && pair.error ? '#fff5f5' : undefined,
                        color: pair && pair.error ? '#991b1b' : undefined,
                        minHeight: 60,
                      }}>{pair ? (pair.error || pair.output || '') : ''}</div>
                      {pair && pair.warnings && pair.warnings.length ? (
                        <div style={{ fontSize: 12, color: '#946200', marginTop: 3 }}>{pair.warnings.join('；')}</div>
                      ) : null}
                    </div>
                  )
                })}
              </div>
            </div>
            <MetricsPanel metrics={metrics} />
          </>
        )}

        {/* ===== 映射知识库 ===== */}
        {tab === 'mapping' && (
          !mapping ? <div className="table-loading">加载中…</div> : KB_GROUPS.map(([key, title]) => (
            <div key={key} style={{ marginBottom: 20 }}>
              <h3 style={{ fontSize: 14, borderBottom: '2px solid var(--border)', paddingBottom: 6 }}>{title}</h3>
              {(mapping[key] || []).map((r, i) => (
                <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '8px 0', borderBottom: '1px solid #f0f0f0' }}>
                  <span className="tag" style={{ background: '#10a37f', color: '#fff', fontFamily: 'monospace' }}>{r.openai || '不支持'}</span>
                  <span style={{ color: 'var(--muted)', fontWeight: 700 }}>⟷</span>
                  <span className="tag" style={{ background: '#d97757', color: '#fff', fontFamily: 'monospace' }}>{r.anthropic || '不支持'}</span>
                  <span style={{ flex: 1, fontSize: 12, color: 'var(--muted)' }}>{r.note}{r.type ? `（${r.type}）` : ''}</span>
                </div>
              ))}
            </div>
          ))
        )}
      </div>
    </div>
  )
}
