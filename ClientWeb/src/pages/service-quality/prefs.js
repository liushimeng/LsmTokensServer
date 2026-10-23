// 服务质量监控页面「状态记忆」纯逻辑（v2.0.88 阶段DC）。
//
// 背景：ServiceQuality 页顶部的三组筛选按钮（统计窗口 / 趋势窗口 / 趋势指标）
// 以及「全模型逐时段明细表」的折叠状态此前完全不持久化，F5 即回落默认值，
// 用户每次都要重新点一遍。本文件把这三类状态的读写抽成**零 React 依赖**的纯函数，
// 便于 node 直跑自检（见 prefs.test.js）。
//
// key 规范：lsm:serviceQuality:{name}:{role}
//   {role} 取 'manager' / 'user' —— 管理员端（9101）与用户端（29001）同一份源码、
//   两套构建产物，记忆必须按角色隔离，避免互相污染。
//
// 本文件不使用 __APP_ROLE__（node 直跑时不存在该全局），角色由调用方传入。

// qosPrefKey 生成记忆 key。
// @param {string} role 'manager' | 'user'
// @param {string} name 记忆项名称（如 'statWin' / 'trendWin' / 'trendMetric' / 'periodTableOpen'）
export function qosPrefKey(role, name) {
  const r = role === 'manager' ? 'manager' : 'user'
  return `lsm:serviceQuality:${name}:${r}`
}

// loadQosPref 读取记忆值并做白名单校验。
//   - 记忆缺失 / 非法 JSON / 不在白名单内 → 返回 fallback；
//   - allowed 为 null 时不做白名单校验（布尔等任意值场景）。
// @param {string} key 完整 localStorage key
// @param {Array<string>|null} allowed 允许值白名单
// @param {*} fallback 兜底默认值
export function loadQosPref(key, allowed, fallback) {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return fallback
    const raw = window.localStorage.getItem(key)
    if (raw === null || raw === undefined || raw === '') return fallback
    let v
    try {
      v = JSON.parse(raw)
    } catch {
      // 兼容「裸字符串」写入（如 '7d' 而非 '"7d"'）
      v = raw
    }
    if (allowed && Array.isArray(allowed) && !allowed.includes(v)) return fallback
    return v
  } catch {
    return fallback
  }
}

// saveQosPref 写入记忆值（失败静默忽略，绝不阻断页面渲染）。
// @param {string} key 完整 localStorage key
// @param {*} value 待记忆值
export function saveQosPref(key, value) {
  try {
    if (typeof window === 'undefined' || !window.localStorage) return
    window.localStorage.setItem(key, JSON.stringify(value))
  } catch { /* 忽略 */ }
}
