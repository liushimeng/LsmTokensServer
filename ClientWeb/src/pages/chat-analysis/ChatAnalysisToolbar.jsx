// 对话分析筛选条件工具栏
// 管理端显示用户名选择器 + 批量删除；用户端隐藏
import TimeRangeSelector from '../../components/TimeRangeSelector'
import { modelNamesOf, allModelNames } from '../../shared/userModelOptions'
import { useI18n } from '../../i18n'

export default function ChatAnalysisToolbar({
  isAdmin,
  // 筛选状态
  userName, setUserName,
  modelName, setModelName,
  days, setDays,
  pageSize, setPageSize,
  filterUrl, setFilterUrl,
  filterMethod, setFilterMethod,
  filterStatus, setFilterStatus,
  filterStatusNot, setFilterStatusNot,
  filterProtocolType, setFilterProtocolType,
  filterAlgorithmType, setFilterAlgorithmType,
  filterDstModel, setFilterDstModel,
  filterTools, setFilterTools,
  filterAgentTool, setFilterAgentTool,
  filterInTok, setFilterInTok,
  filterOutTok, setFilterOutTok,
  // 动态档位
  levels, levelsLoading,
  // 操作
  onQuery, loading,
  selectedCount, onDeleteSelected, deleting,
  // 下拉选项
  dstModels, agentTools,
}) {
  const { t } = useI18n()
  const PAGE_SIZES = [3, 5, 10, 15, 20, 50, 100]

  return (
    <div className="toolbar">
      {isAdmin ? <label>{t('chatAnalysis.userNameLabel')}
        <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 140 }}>
          <option value="">{t('chatAnalysis.selectUser')}</option>
        </select>
      </label> : null}
      <label>{t('chatAnalysis.modelNameLabel')}
        <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
          <option value="">{t('chatAnalysis.selectModel')}</option>
        </select>
      </label>
      <label>{t('chatAnalysis.timeRange')}
        <TimeRangeSelector span={days ?? 3} onChange={(v) => { setDays(v) }} levels={levels} loading={levelsLoading} />
      </label>
      <label>{t('chatAnalysis.perPage')}
        <select value={pageSize} onChange={(e) => setPageSize(Number(e.target.value))}>
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
      <button className="btn btn-primary" onClick={onQuery} disabled={loading}>{t('chatAnalysis.query')}</button>
      {isAdmin ? <button className="btn btn-danger" onClick={onDeleteSelected} disabled={!selectedCount || deleting}>
        {deleting ? t('chatAnalysis.deleting') : `${t('chatAnalysis.batchDelete')}(${selectedCount})`}
      </button> : null}
    </div>
  )
}
