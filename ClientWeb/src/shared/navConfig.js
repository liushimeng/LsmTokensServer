// 菜单配置与记忆（docs/菜单栏设计以及优化方案/01、02）
// 一级 = 功能分组（可折叠），二级 = 页面项；key 与 App.jsx PAGES 注册表一致。

export const NAV_TREES = {
  admin: [
    { id: 'overview', label: '总览', icon: '🏠', items: [
      { key: 'Home', label: '首页' },
    ]},
    { id: 'user-route', label: '用户与路由', icon: '👥', items: [
      { key: 'UserManage', label: '用户管理' },
      { key: 'DstEndPointManage', label: '端点管理' },
      { key: 'AIRouteManage', label: '路由管理' },
    ]},
    { id: 'model-proxy', label: '模型与代理', icon: '🧠', items: [
      { key: 'ModelInfo', label: '模型信息' },
      { key: 'AgentInfo', label: 'Agent信息' },
      { key: 'ProtocolConvertAnalyzer', label: '协议分析器' },
    ]},
    // 管理端补齐对话分析组：从用户管理/路由管理跳入分析页后仍有菜单归属与高亮
    { id: 'analysis', label: '对话分析', icon: '💬', items: [
      { key: 'ChatAnalysis', label: '浏览记录' },
      { key: 'ChatAnalysisTotal', label: '汇总统计' },
      { key: 'ChatAnalysisSession', label: '会话分析' },
      { key: 'ChatAnalysisTask', label: '任务分析' },
      { key: 'ChatDialog', label: '对话查看' },
    ]},
    { id: 'spider', label: '爬虫与数据', icon: '🕷', items: [
      { key: 'SpiderDataSource', label: '爬虫数据源' },
      { key: 'SpiderDailyInfo', label: '爬虫日报' },
      { key: 'CleanupReport', label: '清理报告' },
    ]},
  ],
  user: [
    { id: 'overview', label: '总览', icon: '🏠', items: [
      { key: 'Home', label: '首页' },
    ]},
    { id: 'analysis', label: '对话分析', icon: '💬', items: [
      { key: 'ChatAnalysis', label: '对话分析' },
      { key: 'ChatAnalysisTotal', label: '汇总统计' },
      { key: 'ChatAnalysisSession', label: '会话分析' },
      { key: 'ChatAnalysisTask', label: '任务分析' },
      { key: 'ChatDialog', label: '对话查看' },
    ]},
    { id: 'route-model', label: '路由与模型', icon: '🧭', items: [
      { key: 'AIRouteManage', label: '路由管理' },
      { key: 'ModelInfo', label: '模型信息' },
      { key: 'AgentInfo', label: 'Agent信息' },
      { key: 'ProtocolConvertAnalyzer', label: '协议分析器' },
      { key: 'DstEndPointManage', label: '端点管理' },
    ]},
    { id: 'spider', label: '爬虫与数据', icon: '🕷', items: [
      { key: 'SpiderDataSource', label: '爬虫数据源' },
      { key: 'SpiderDailyInfo', label: '爬虫日报' },
      { key: 'CleanupReport', label: '清理报告' },
    ]},
  ],
}

// 路由 → { groupId, itemKey } 映射（按角色）；未知路由返回 null（不高亮任何项）
const ROUTE_MAPS = {}
for (const role of Object.keys(NAV_TREES)) {
  const m = {}
  for (const g of NAV_TREES[role]) {
    for (const it of g.items) m[it.key] = { groupId: g.id, itemKey: it.key }
  }
  ROUTE_MAPS[role] = m
}
export function findMenuEntry(role, route) {
  return (ROUTE_MAPS[role] || {})[route] || null
}

// ---- localStorage 记忆（容错：隐私模式/JSON 损坏时退化为默认值）----
const LS_GROUP_KEY = (role) => `lsm.nav.collapsedGroups.${role}`
const LS_SIDEBAR_KEY = 'lsm.nav.sidebarCollapsed'

function safeGet(key) {
  try { return window.localStorage.getItem(key) } catch { return null }
}
function safeSet(key, val) {
  try { window.localStorage.setItem(key, val) } catch { /* 忽略 */ }
}

export function loadCollapsedGroups(role) {
  const raw = safeGet(LS_GROUP_KEY(role))
  if (!raw) return new Set()
  try {
    const arr = JSON.parse(raw)
    return new Set(Array.isArray(arr) ? arr.filter((s) => typeof s === 'string') : [])
  } catch {
    safeSet(LS_GROUP_KEY(role), '[]') // 损坏时覆盖写回合法值
    return new Set()
  }
}

export function saveCollapsedGroups(role, set) {
  safeSet(LS_GROUP_KEY(role), JSON.stringify([...set]))
}

export function loadSidebarCollapsed() {
  return safeGet(LS_SIDEBAR_KEY) === '1'
}

export function saveSidebarCollapsed(v) {
  safeSet(LS_SIDEBAR_KEY, v ? '1' : '0')
}
