// 阶段CS：列表记忆 React Hook（纯逻辑见 listMemory.js，本文件不供 node 直跑）
//   - usePersistedSpan：时间跨度档位记忆（动态档位 + nearestSpan 就近迁移 + 变更即写盘）
//   - useDebouncedMemorySave：文本筛选字段 debounce 300ms 写盘（对齐 chat-analysis 既有行为）
import { useCallback, useEffect, useRef, useState } from 'react'
import { useTimeSpanLevels } from './useTimeSpanLevels'
import { nearestSpan } from './timeSpan'
import { roleSuffix, loadListMemory, saveListMemory, resolveSpanTarget, readLegacySpan } from './listMemory'

/**
 * usePersistedSpan：时间跨度档位记忆 Hook
 * @param {string} pageKey 页面 key（如 "airoute"）
 * @param {boolean} isAdmin 是否管理端（角色隔离 key）
 * @param {number} defaultSpan 无记忆时的默认天数（经 nearestSpan 就近吸附）
 * @param {string} [legacyKey] 旧版裸值 key（兼容迁移，如 lsm:cleanupReport:days:v1）
 * @returns {{days:number|null, setDays:Function, levels:Array, levelsLoading:boolean}}
 */
export function usePersistedSpan(pageKey, isAdmin, defaultSpan = 3, legacyKey = null) {
  const { levels, loading: levelsLoading } = useTimeSpanLevels()
  const storageKey = `lsm:${pageKey}:span:${roleSuffix(isAdmin)}`
  const [days, setDaysState] = useState(null)

  // 动态档位到达后初始化：记忆值 > legacy 兼容值 > 默认档，统一 nearestSpan 就近迁移
  // （span 编码负值=小时，「最近6小时」= -6 可完整还原）
  useEffect(() => {
    if (!levels.length || days !== null) return
    const mem = loadListMemory(storageKey)
    const target = resolveSpanTarget(mem && mem.span, readLegacySpan(legacyKey), defaultSpan)
    setDaysState(nearestSpan(levels, target))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [levels])

  // 变更即写盘（下拉单选低频，无需 debounce）
  const setDays = useCallback((v) => {
    setDaysState(v)
    if (v !== null && v !== undefined) saveListMemory(storageKey, { span: Number(v) })
  }, [storageKey])

  return { days, setDays, levels, levelsLoading }
}

/**
 * useDebouncedMemorySave：筛选字段对象 debounce 300ms 写盘
 * @param {string} key 完整 localStorage key
 * @param {object} payload 需持久化的字段对象（每次 render 重新序列化比对）
 */
export function useDebouncedMemorySave(key, payload) {
  const timerRef = useRef(null)
  const serialized = JSON.stringify(payload)
  useEffect(() => {
    if (timerRef.current) clearTimeout(timerRef.current)
    timerRef.current = setTimeout(() => {
      try { saveListMemory(key, JSON.parse(serialized)) } catch { /* 忽略 */ }
    }, 300)
    return () => { if (timerRef.current) clearTimeout(timerRef.current) }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, serialized])
}
