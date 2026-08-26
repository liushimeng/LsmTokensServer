// 对话分析筛选条件 localStorage 记忆 Hook
// 支持按角色（manager/user）隔离存储，debounce 300ms 保存
import { useEffect, useRef, useState } from 'react'
import { pickRouteQuery } from '../../shared/format'
import { useTimeSpanLevels } from '../../shared/useTimeSpanLevels'
import { nearestSpan } from '../../shared/timeSpan'
import { safeGet, safeSet } from './constants'

// 从 localStorage 恢复筛选参数（按角色隔离 key）
function loadFiltersFromStorage(isAdmin) {
  const key = `lsm:chat_analysis:filters:${isAdmin ? 'manager' : 'user'}`
  try {
    const raw = safeGet(key)
    if (!raw) return null
    const saved = JSON.parse(raw)
    return saved && typeof saved === 'object' ? saved : null
  } catch { return null }
}

export default function useChatAnalysisFilters(route, isAdmin) {
  const init = pickRouteQuery(route && route.query)
  const saved = loadFiltersFromStorage(isAdmin)

  // 动态时间档位
  const { levels, loading: levelsLoading } = useTimeSpanLevels()

  // 筛选条件（路由 > localStorage > 默认值）
  const [userName, setUserName] = useState(init.userName || (isAdmin && saved && saved.userName) || '')
  const [modelName, setModelName] = useState(init.modelName || (saved && saved.modelName) || '')
  const [days, setDays] = useState(null)
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState((saved && saved.pageSize) || 10)
  const [filterUrl, setFilterUrl] = useState((saved && saved.filterUrl) || '')
  const [filterMethod, setFilterMethod] = useState((saved && saved.filterMethod) || '')
  const [filterStatus, setFilterStatus] = useState((saved && saved.filterStatus) || '')
  const [filterStatusNot, setFilterStatusNot] = useState((saved && saved.filterStatusNot) || false)
  const [filterProtocolType, setFilterProtocolType] = useState((saved && saved.filterProtocolType) || 0)
  const [filterAlgorithmType, setFilterAlgorithmType] = useState((saved && saved.filterAlgorithmType) || 0)
  const [filterDstModel, setFilterDstModel] = useState((saved && saved.filterDstModel) || '')
  const [filterTools, setFilterTools] = useState((saved && saved.filterTools) || '')
  const [filterAgentTool, setFilterAgentTool] = useState((saved && saved.filterAgentTool) || '')
  const [filterInTok, setFilterInTok] = useState((saved && saved.filterInTok) || 0)
  const [filterOutTok, setFilterOutTok] = useState((saved && saved.filterOutTok) || 0)

  // 动态档位加载后初始化 days
  useEffect(() => {
    if (!levels.length || days !== null) return
    setDays(nearestSpan(levels, (saved && saved.days != null) ? saved.days : 3))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [levels])

  // debounce 保存筛选参数到 localStorage
  const saveTimerRef = useRef(null)
  useEffect(() => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      const filters = {
        userName, modelName, days, pageSize,
        filterUrl, filterMethod, filterStatus, filterStatusNot,
        filterProtocolType, filterAlgorithmType, filterDstModel,
        filterTools, filterAgentTool, filterInTok, filterOutTok,
      }
      const key = `lsm:chat_analysis:filters:${isAdmin ? 'manager' : 'user'}`
      safeSet(key, JSON.stringify(filters))
    }, 300)
    return () => { if (saveTimerRef.current) clearTimeout(saveTimerRef.current) }
  }, [userName, modelName, days, pageSize, filterUrl, filterMethod, filterStatus, filterStatusNot,
      filterProtocolType, filterAlgorithmType, filterDstModel, filterTools, filterAgentTool,
      filterInTok, filterOutTok, isAdmin])

  return {
    // 筛选状态
    userName, setUserName,
    modelName, setModelName,
    days, setDays,
    page, setPage,
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
    // 路由初始值
    init,
  }
}
