// 对话分析页面主组件
// 模块化重构：筛选工具栏 + 数据表格 + 内联详情展开（替代 Modal 弹窗）
import { useEffect, useRef, useState } from 'react'
import { post, get } from '../../shared/api'
import { isAdminRole } from '../../shared/auth'
import { useUserModelOptions, useMyModelNames } from '../../shared/userModelOptions'
import DataTable from '../../components/DataTable'
import { fmtTime, fmtNum, fmtMs } from '../../shared/format'
import { useI18n } from '../../i18n'
import { protocolBadgeClass, protocolBadgeText, protocolBadgeTitle } from './constants'
import useChatAnalysisFilters from './useChatAnalysisFilters'
import useChatAnalysisData from './useChatAnalysisData'
import ChatAnalysisToolbar from './ChatAnalysisToolbar'
import InlineDetailRow from './InlineDetailRow'

export default function ChatAnalysis({ route }) {
  const { t } = useI18n()
  const isAdmin = isAdminRole()

  // 用户名/模型名下拉选项
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()

  // 筛选条件 Hook
  const filters = useChatAnalysisFilters(route, isAdmin)
  const {
    userName, setUserName, modelName, setModelName,
    days, setDays, page, setPage, pageSize, setPageSize,
    filterUrl, setFilterUrl, filterMethod, setFilterMethod,
    filterStatus, setFilterStatus, filterStatusNot, setFilterStatusNot,
    filterProtocolType, setFilterProtocolType,
    filterAlgorithmType, setFilterAlgorithmType,
    filterDstModel, setFilterDstModel,
    filterTools, setFilterTools, filterAgentTool, setFilterAgentTool,
    filterInTok, setFilterInTok, filterOutTok, setFilterOutTok,
    levels, levelsLoading, init,
  } = filters

  // 数据 Hook
  const data = useChatAnalysisData(isAdmin, userName, modelName, days, pageSize, {
    filterUrl, filterMethod, filterStatus, filterStatusNot,
    filterProtocolType, filterAlgorithmType, filterDstModel,
    filterTools, filterAgentTool, filterInTok, filterOutTok,
  })
  const {
    rows, total, totalPages, loading, error, okMsg,
    doQuery, setError, setOkMsg,
    selected, toggleAll, toggleOne,
    expandedIds, toggleExpand,
    detailStates, loadDetail, setDetailView, setCopyOk, copyOk,
    batchDelete, deleting,
  } = data

  // 下拉选项（接口动态拉取）
  const [dstModels, setDstModels] = useState([])
  const [agentTools, setAgentTools] = useState([])

  // 拉取下拉选项
  const loadOptions = async (modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (!mn) return
    try {
      const d = await post('ChatAnalysisDstModelsInterface', { user_name: isAdmin ? userName.trim() : '', model_name: mn })
      setDstModels(d.data || [])
    } catch { /* 静默失败 */ }
  }

  // 阶段AY：modelNameRef 跟踪最新 modelName，避免 useEffect 的闭包陷阱
  // （loadOptions() 不传参时读取 ref，保证请求参数与 UI 一致）
  const modelNameRef = useRef(modelName)
  useEffect(() => { modelNameRef.current = modelName }, [modelName])

  // Agent 工具下拉（全站，加载一次）
  useEffect(() => {
    get('ChatAnalysisAgentToolsInterface').then((d) => setAgentTools(d.data || [])).catch(() => {})
  }, [])

  // 动态档位就绪后首查（管理端需 userName+modelName，用户端需 modelName）
  useEffect(() => {
    if (days === null) return
    const hasKey = (isAdmin ? userName.trim() !== '' : true) && modelNameRef.current.trim() !== ''
    if (hasKey) { loadOptions(modelNameRef.current); setPage(1); doQuery(1) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days])

  // 用户端进入页面未带模型：本人模型列表（缓存一次）到达后自动选第一个并查询
  useEffect(() => {
    if (days === null) return
    if (isAdmin || !myModelNames.length || modelName) return
    const first = myModelNames[0]
    setModelName(first); setPage(1)
    loadOptions(first)
    doQuery(1, first)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [myModelNames, days])

  // 手动查询按钮
  const handleQuery = () => {
    setPage(1)
    doQuery(1)
  }

  // 切换展开详情
  const handleToggleExpand = (rowId) => {
    toggleExpand(rowId)
  }

  // 加载详情字段
  const handleTabChange = (row, field) => {
    loadDetail(row, field)
  }

  // 切换视图模式
  const handleViewChange = (rowId, view) => {
    setDetailView(rowId, view)
  }

  // 复制
  const handleCopy = () => {
    setCopyOk(true)
    setTimeout(() => setCopyOk(false), 1500)
  }

  // 表格列定义
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
    { key: 'dst_endpoint_algorithm_type', title: t('chatAnalysis.forwardType'), width: 110,
      render: (v) => <span className={protocolBadgeClass(v)} title={protocolBadgeTitle(v, t)}>{protocolBadgeText(v, t)}</span>,
    },
    { key: 'tokens_input_size', title: t('chatAnalysis.inputTokens'), render: (v) => fmtNum(v) },
    { key: 'tokens_output_size', title: t('chatAnalysis.outputTokens'), render: (v) => fmtNum(v) },
    { key: 'elapsed_ms', title: t('chatAnalysis.duration'), render: (v) => fmtMs(v) },
    { key: 'agent_tool_name', title: t('chatAnalysis.agentTool'), render: (v) => v || '-' },
    // v2.0.7x 阶段BG：Agent工具信息 列——展示调用工具标识（tool_identifier 字段），
    // 不再使用 agent_tool_info 字段；空值降级 '-'；title 展示完整内容，便于悬停查看；超长字符串截断。
    { key: 'tool_identifier', title: t('chatAnalysis.agentToolInfo'), render: (v) =>
      v ? <span title={v}>{String(v).length > 60 ? String(v).slice(0, 60) + '…' : v}</span> : '-'
    },
    // v2.0.76 阶段BD：AgentSessionID 列——优先展示 Agent 工具原生识别值（agent_tool_session_id），
    // 为空时降级展示生效 session_id；合成 ID（self_generate_ 前缀）灰色斜体区分，超 24 字符截断。
    { key: 'agent_tool_session_id', title: t('chatAnalysis.agentSessionId'), render: (v, row) => {
      let sid = v
      let isSynth = false
      if (!sid) {
        sid = row.session_id
        isSynth = typeof sid === 'string' && sid.startsWith('self_generate_')
      }
      if (!sid || sid === 'unknown_session_id') return '-'
      return <span style={isSynth ? { color: 'var(--muted)', fontStyle: 'italic' } : undefined} title={sid}>
        {sid.length > 24 ? sid.slice(0, 24) + '…' : sid}
      </span>
    } },
    { key: 'actions', title: t('chatAnalysis.action'), render: (_, r) => {
      const isExpanded = expandedIds.has(r.id)
      return (
        <button className={`btn btn-sm ${isExpanded ? 'btn-active' : ''}`} onClick={() => handleToggleExpand(r.id)}>
          {isExpanded ? '▾ ' + t('common.collapse') : '▸ ' + t('chatAnalysis.detail')}
        </button>
      )
    } },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('chatAnalysis.title')}</h2>

      <ChatAnalysisToolbar
        isAdmin={isAdmin}
        userName={userName} setUserName={setUserName}
        modelName={modelName} setModelName={setModelName}
        days={days} setDays={setDays}
        pageSize={pageSize} setPageSize={setPageSize}
        filterUrl={filterUrl} setFilterUrl={setFilterUrl}
        filterMethod={filterMethod} setFilterMethod={setFilterMethod}
        filterStatus={filterStatus} setFilterStatus={setFilterStatus}
        filterStatusNot={filterStatusNot} setFilterStatusNot={setFilterStatusNot}
        filterProtocolType={filterProtocolType} setFilterProtocolType={setFilterProtocolType}
        filterAlgorithmType={filterAlgorithmType} setFilterAlgorithmType={setFilterAlgorithmType}
        filterDstModel={filterDstModel} setFilterDstModel={setFilterDstModel}
        filterTools={filterTools} setFilterTools={setFilterTools}
        filterAgentTool={filterAgentTool} setFilterAgentTool={setFilterAgentTool}
        filterInTok={filterInTok} setFilterInTok={setFilterInTok}
        filterOutTok={filterOutTok} setFilterOutTok={setFilterOutTok}
        levels={levels} levelsLoading={levelsLoading}
        onQuery={handleQuery} loading={loading}
        selectedCount={selected.length} onDeleteSelected={batchDelete} deleting={deleting}
        dstModels={dstModels} agentTools={agentTools}
        userOptions={userOptions} myModelNames={myModelNames}
      />

      {error ? <div className="alert alert-error">{error}</div> : null}
      {okMsg ? <div className="alert alert-ok">{okMsg}</div> : null}

      <DataTable
        columns={columns}
        rows={rows}
        loading={loading}
        rowKey="id"
        empty={t('chatAnalysis.empty')}
        collapsible
        collapsedIds={expandedIds}
        onToggleCollapse={handleToggleExpand}
        renderCollapsedRow={(row) => {
          const state = detailStates[row.id]
          if (!state) return null
          return (
            <InlineDetailRow
              row={row}
              detailState={state}
              onTabChange={(field) => handleTabChange(row, field)}
              onViewChange={(view) => handleViewChange(row.id, view)}
              onCopy={handleCopy}
              copyOk={copyOk}
              onClose={() => handleToggleExpand(row.id)}
            />
          )
        }}
      />

      {totalPages > 0 ? (
        <div className="pager">
          <span>{t('chatAnalysis.totalRecords', { count: fmtNum(total), page, totalPages })}</span>
          <button className="btn btn-sm" disabled={page <= 1 || loading} onClick={() => { setPage(page - 1); doQuery(page - 1) }}>{t('chatAnalysis.prevPage')}</button>
          <span>{t('chatAnalysis.pageInfo', { page, totalPages })}</span>
          <button className="btn btn-sm" disabled={page >= totalPages || loading} onClick={() => { setPage(page + 1); doQuery(page + 1) }}>{t('chatAnalysis.nextPage')}</button>
        </div>
      ) : null}
    </div>
  )
}
