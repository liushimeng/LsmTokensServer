// 菜单配置与记忆（docs/菜单栏设计以及优化方案/01、02）
// 一级 = 功能分组（可折叠），二级 = 页面项；key 与 App.jsx PAGES 注册表一致。
// label 字段为 i18n 翻译键，由 SideNav 组件通过 t() 翻译。

export const NAV_TREES = {
  // 阶段T：admin 树经构建期常量裁剪，用户端产物不携带管理菜单字样
  admin: __APP_ROLE__ === 'manager' ? [
    { id: 'overview', label: 'nav.overview', icon: '🏠', items: [
      { key: 'Home', label: 'nav.home' },
    ]},
    { id: 'user-route', label: 'nav.userRoute', icon: '👥', items: [
      { key: 'UserManage', label: 'nav.userManage' },
      { key: 'DstEndPointManage', label: 'nav.dstEndPointManage' },
      { key: 'AIRouteManage', label: 'nav.aiRouteManage' },
    ]},
    { id: 'model-proxy', label: 'nav.modelProxy', icon: '🧠', items: [
      { key: 'ModelInfo', label: 'nav.modelInfo' },
      { key: 'AgentInfo', label: 'nav.agentInfo' },
      { key: 'ProtocolConvertAnalyzer', label: 'nav.protocolConvertAnalyzer' },
    ]},
    { id: 'analysis', label: 'nav.analysis', icon: '💬', items: [
      { key: 'ChatAnalysis', label: 'nav.chatAnalysis' },
      { key: 'ChatAnalysisTotal', label: 'nav.chatAnalysisTotal' },
      { key: 'ChatAnalysisSession', label: 'nav.chatAnalysisSession' },
      { key: 'ChatAnalysisTask', label: 'nav.chatAnalysisTask' },
      { key: 'ChatDialog', label: 'nav.chatDialog' },
    ]},
    { id: 'spider', label: 'nav.spider', icon: '🕷', items: [
      { key: 'SpiderDataSource', label: 'nav.spiderDataSource' },
      { key: 'SpiderDailyInfo', label: 'nav.spiderDailyInfo' },
      { key: 'CleanupReport', label: 'nav.cleanupReport' },
    ]},
  ] : [],
  user: [
    { id: 'overview', label: 'nav.overview', icon: '🏠', items: [
      { key: 'Home', label: 'nav.home' },
    ]},
    { id: 'analysis', label: 'nav.analysis', icon: '💬', items: [
      { key: 'ChatAnalysis', label: 'nav.chatAnalysis' },
      { key: 'ChatAnalysisTotal', label: 'nav.chatAnalysisTotal' },
      { key: 'ChatAnalysisSession', label: 'nav.chatAnalysisSession' },
      { key: 'ChatAnalysisTask', label: 'nav.chatAnalysisTask' },
      { key: 'ChatDialog', label: 'nav.chatDialog' },
    ]},
    { id: 'route-model', label: 'nav.routeModel', icon: '🧭', items: [
      { key: 'AIRouteManage', label: 'nav.aiRouteManage' },
      { key: 'ModelInfo', label: 'nav.modelInfo' },
      { key: 'AgentInfo', label: 'nav.agentInfo' },
      { key: 'ProtocolConvertAnalyzer', label: 'nav.protocolConvertAnalyzer' },
      { key: 'DstEndPointManage', label: 'nav.dstEndPointManage' },
    ]},
    { id: 'spider', label: 'nav.spider', icon: '🕷', items: [
      { key: 'SpiderDataSource', label: 'nav.spiderDataSource' },
      { key: 'SpiderDailyInfo', label: 'nav.spiderDailyInfo' },
      { key: 'CleanupReport', label: 'nav.cleanupReport' },
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
