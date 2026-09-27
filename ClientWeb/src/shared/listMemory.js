// 阶段CS：全站列表「筛选条件 + 时间跨度」localStorage 记忆通用纯逻辑工具
// 背景：F5/右键刷新后各列表页筛选状态丢失（如 AIRouteManage 选「最近6小时」刷新后回落默认档）。
// 本文件仅含纯函数（无 React 依赖、显式 .js 后缀约定不适用——本文件零导入），node 可直跑自检；
// React Hook（usePersistedSpan/useDebouncedMemorySave）见同目录 useListMemory.js。
// key 规范：lsm:{pageKey}:span:{role}（{span}）；lsm:{pageKey}:filters:{role}（筛选字段对象）

export function roleSuffix(isAdmin) { return isAdmin ? 'manager' : 'user' }

// 读取记忆对象；缺失/非法 JSON/非对象均返回 null
export function loadListMemory(key) {
  try {
    const raw = window.localStorage.getItem(key)
    if (!raw) return null
    const saved = JSON.parse(raw)
    return saved && typeof saved === 'object' ? saved : null
  } catch { return null }
}

export function saveListMemory(key, payload) {
  try { window.localStorage.setItem(key, JSON.stringify(payload)) } catch { /* 忽略 */ }
}

// resolveSpanTarget：span 初始化目标值选取（纯函数，可单测）
// 优先级：localStorage 记忆值 > legacyKey 兼容值 > 默认档
export function resolveSpanTarget(savedSpan, legacySpan, defaultSpan) {
  const pick = (v) => (typeof v === 'number' && !Number.isNaN(v) ? v : null)
  return pick(savedSpan) ?? pick(legacySpan) ?? defaultSpan
}

// 从 legacyKey（裸数字串，如 CleanupReport 旧 key）读取兼容值；无则 null
export function readLegacySpan(legacyKey) {
  if (!legacyKey) return null
  try {
    const raw = window.localStorage.getItem(legacyKey)
    if (raw == null || raw === '') return null
    const n = Number(raw)
    return Number.isNaN(n) ? null : n
  } catch { return null }
}
