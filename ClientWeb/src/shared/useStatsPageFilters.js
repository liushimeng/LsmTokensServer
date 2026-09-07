// 阶段BV：通用「用户名+模型名+days」筛选+记忆 Hook
// 用于 /ModelInfo、/AgentInfo 等统计页；与 chat-analysis 共享一致的 localStorage key 命名风格。
// 仅记忆 userName/modelName/days（不带其他过滤字段），按角色隔离 key。
import { useEffect, useRef, useState } from 'react'
import { pickRouteQuery } from './format'
import { useTimeSpanLevels } from './useTimeSpanLevels'
import { nearestSpan } from './timeSpan'

function safeGet(k) { try { return window.localStorage.getItem(k) } catch { return null } }
function safeSet(k, v) { try { window.localStorage.setItem(k, v) } catch { /* 忽略 */ } }

function loadFromStorage(pageKey, isAdmin) {
  const key = `lsm:${pageKey}:filters:${isAdmin ? 'manager' : 'user'}`
  try {
    const raw = safeGet(key)
    if (!raw) return null
    const saved = JSON.parse(raw)
    return saved && typeof saved === 'object' ? saved : null
  } catch { return null }
}

/**
 * @param {string} pageKey 页面 key（"model_info" / "agent_info"）
 * @param {object} route 路由对象（带 query）
 * @param {boolean} isAdmin 是否管理端（决定是否显示 userName 字段 + 角色隔离 key）
 * @param {number} defaultSpan 动态档位未就绪时的默认天数（一般 3）
 */
export default function useStatsPageFilters(pageKey, route, isAdmin, defaultSpan = 3) {
  const init = pickRouteQuery(route && route.query)
  const saved = loadFromStorage(pageKey, isAdmin)
  const { levels, loading: levelsLoading } = useTimeSpanLevels()

  const [userName, setUserName] = useState((isAdmin && init.userName) || (isAdmin && saved && saved.userName) || '')
  const [modelName, setModelName] = useState(init.modelName || (saved && saved.modelName) || '')
  const [days, setDays] = useState(null)

  // 档位加载后初始化 days
  useEffect(() => {
    if (!levels.length || days !== null) return
    const fromInit = init.days != null ? Number(init.days) : null
    const fromSaved = saved && saved.days != null ? Number(saved.days) : null
    const target = fromInit != null ? fromInit : (fromSaved != null ? fromSaved : defaultSpan)
    setDays(nearestSpan(levels, target))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [levels])

  // debounce 300ms 写入 localStorage
  const saveTimerRef = useRef(null)
  useEffect(() => {
    if (saveTimerRef.current) clearTimeout(saveTimerRef.current)
    saveTimerRef.current = setTimeout(() => {
      const payload = { userName, modelName, days }
      safeSet(`lsm:${pageKey}:filters:${isAdmin ? 'manager' : 'user'}`, JSON.stringify(payload))
    }, 300)
    return () => { if (saveTimerRef.current) clearTimeout(saveTimerRef.current) }
  }, [pageKey, isAdmin, userName, modelName, days])

  return {
    userName, setUserName,
    modelName, setModelName,
    days, setDays,
    levels, levelsLoading,
  }
}
