// 对话分析数据查询 + 详情加载 Hook
// 封装列表查询、详情按需加载、缓存、批量删除
import { useState } from 'react'
import { post } from '../../shared/api'
import { useI18n } from '../../i18n'

export default function useChatAnalysisData(isAdmin, userName, modelName, days, pageSize, filters) {
  const { t } = useI18n()

  // 列表数据
  const [rows, setRows] = useState([])
  const [total, setTotal] = useState(0)
  const [totalPages, setTotalPages] = useState(0)
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [okMsg, setOkMsg] = useState('')
  const [selected, setSelected] = useState([])

  // 详情状态（支持多条同时展开）
  const [expandedIds, setExpandedIds] = useState(new Set())
  const [detailStates, setDetailStates] = useState({}) // { [rowId]: { tab, view, value, loading, cache } }
  const [copyOk, setCopyOk] = useState(false)
  const [deleting, setDeleting] = useState(false)

  // 查询列表
  const doQuery = async (p, modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (!isAdmin && !mn) { setError(t('chatAnalysis.pleaseSelectModel')); return }
    if (isAdmin && (userName.trim() === '' || mn === '')) { setError(t('chatAnalysis.pleaseSelectUserAndModel')); return }
    setLoading(true); setError(''); setOkMsg(''); setSelected([])
    try {
      const d = await post('ChatAnalysisInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn,
        page: p, page_size: pageSize, days: days ?? 3,
        filter_url: filters.filterUrl.trim(), filter_method: filters.filterMethod.trim(),
        filter_status: filters.filterStatus.trim(), filter_status_not: filters.filterStatusNot,
        filter_protocol_type: filters.filterProtocolType, filter_algorithm_type: filters.filterAlgorithmType,
        filter_dst_model_name: filters.filterDstModel,
        filter_tools: filters.filterTools.trim(), filter_agent_tool_name: filters.filterAgentTool,
        filter_input_tokens_nonzero: filters.filterInTok, filter_output_tokens_nonzero: filters.filterOutTok,
      })
      const data = d.data || {}
      setRows(data.records || [])
      setTotal(data.totalCount || 0)
      setTotalPages(data.totalPages || 0)
    } catch (e) {
      setError(e.message || t('chatAnalysis.queryFailed'))
    } finally { setLoading(false) }
  }

  // 切换展开/收起
  const toggleExpand = (rowId) => {
    setExpandedIds((prev) => {
      const next = new Set(prev)
      if (next.has(rowId)) {
        next.delete(rowId)
      } else {
        next.add(rowId)
        // 首次展开时初始化 detail state
        setDetailStates((states) => {
          if (states[rowId]) return states
          return { ...states, [rowId]: { tab: 'request_body', view: 'raw', value: '', loading: false, cache: {} } }
        })
      }
      return next
    })
  }

  // 加载详情字段
  const loadDetail = async (row, field) => {
    const rowId = row.id
    const state = detailStates[rowId]
    if (!state) return

    // 缓存命中
    if (state.cache[field] !== undefined) {
      setDetailStates((s) => ({
        ...s,
        [rowId]: { ...s[rowId], tab: field, value: s[rowId].cache[field], loading: false },
      }))
      return
    }

    // 开始加载
    setDetailStates((s) => ({
      ...s,
      [rowId]: { ...s[rowId], tab: field, value: '', loading: true },
    }))

    try {
      const d = await post('ChatAnalysisDetailInterface', {
        id: rowId, user_name: isAdmin ? userName.trim() : '', model_name: modelName.trim(), field,
      })
      const v = d.value || ''
      setDetailStates((s) => ({
        ...s,
        [rowId]: {
          ...s[rowId],
          value: v,
          loading: false,
          cache: { ...s[rowId].cache, [field]: v },
        },
      }))
    } catch (e) {
      setDetailStates((s) => ({
        ...s,
        [rowId]: {
          ...s[rowId],
          value: t('chatAnalysis.loadFailed') + (e.message || ''),
          loading: false,
        },
      }))
    }
  }

  // 切换详情视图模式
  const setDetailView = (rowId, view) => {
    setDetailStates((s) => ({
      ...s,
      [rowId]: { ...s[rowId], view },
    }))
  }

  // 批量删除（管理端独占；用户端构建时 __APP_ROLE__ !== 'manager' 守卫阻断，Rollup 整函数 tree-shake 剔除）
  const batchDelete = async () => {
    if (__APP_ROLE__ !== 'manager') return
    if (!selected.length) return
    if (!window.confirm(t('chatAnalysis.deleteConfirm', { count: selected.length }))) return
    setDeleting(true); setError(''); setOkMsg('')
    try {
      const d = await post('ChatAnalysisBatchDeleteInterface', {
        user_name: userName.trim(), model_name: modelName.trim(), ids: selected,
      })
      setOkMsg(d.message || t('chatAnalysis.deleteCompleted'))
      setSelected([])
      doQuery() // 刷新当前页
    } catch (e) {
      setError(e.message || t('chatAnalysis.deleteFailed'))
    } finally { setDeleting(false) }
  }

  // 勾选操作
  const toggleAll = (checked) => {
    setSelected(checked ? rows.map((r) => r.id) : [])
  }
  const toggleOne = (id, checked) => {
    setSelected((s) => (checked ? [...s, id] : s.filter((x) => x !== id)))
  }

  return {
    // 列表
    rows, total, totalPages, loading, error, okMsg,
    doQuery, setError, setOkMsg,
    // 选择
    selected, toggleAll, toggleOne,
    // 展开详情
    expandedIds, toggleExpand,
    detailStates, loadDetail, setDetailView, setCopyOk, copyOk,
    // 批量删除
    batchDelete, deleting,
  }
}
