// shared/timeoutFor.js —— 按 URL 路径决定 fetch 超时上限（毫秒）
//
// 与 ServerGo/api/middleware/query_classifier.go 的 QueryClass 严格对齐：
//   QShort  10s   shortURLPatterns
//   QNormal 30s   默认（覆盖绝大多数 CRUD）
//   QDetail 60s   detailURLPatterns
//   QLong   300s  longURLPatterns（跨分表全站聚合、AIRouteManage batch_stats、CleanupReport）
//   QStream 不超时（SSE/WS 由调用方自行决定）
//
// 设计原则：
//  - 后端 ctx 已经按 URL 分级（10/30/60/300s），前端 timeout 设为同档 + 5s 网络缓冲，
//    保证后端 ctx 先到 → 服务端主动 KILL 查询 → 释放连接，再让前端收到 5xx 错误；
//  - 短超时一律不阻塞重试；长超时场景（如 AIRouteManage batch_stats）给 305s，
//    配合 shared/retry.js 的指数退避，确保网络/服务抖动不会再次触发
//    「请求超时，服务可能正在重启，请刷新页面重试」无限重试风暴。
//
// 返回 -1 表示「不超时」（调用方应自行用 AbortController 控制 cancel）。
export function timeoutFor(path) {
  if (!path) return 30_000
  // 流式接口：SSE / WS（用 ?stream=1 标识 SSE 走流）
  if (/ChatAnalysisTotalRangeInterface\?[^#]*stream=1/.test(path)) return -1
  if (/ChatAnalysisTotalRangeInterface\b/.test(path) && !/stream=1/.test(path)) return 305_000 // Range 报告非流式走 5 分钟
  if (/ChatAnalysisTotalWS\b/.test(path)) return -1
  // 详情大字段
  if (/ChatAnalysisDetailInterface\b/.test(path)) return 65_000
  if (/ProtocolConvertAnalyzerRecordDetail\b/.test(path)) return 65_000
  if (/ProtocolConvertAnalyzerTest\b/.test(path)) return 65_000
  // 长查询：跨分表 / 路由管理 / 清理报告
  if (/ChatAnalysisTotalInterface\b/.test(path)) return 305_000
  if (/CleanupReportInterface\b/.test(path)) return 305_000
  if (/AIRouteManageInterface\b/.test(path)) return 305_000
  if (/UserAIRouteInterface\b/.test(path)) return 305_000
  if (/ModelInfoInterface\b/.test(path)) return 305_000
  if (/AgentInfoInterface\b/.test(path)) return 305_000
  if (/ProtocolConvertAnalyzerRecords\b/.test(path)) return 305_000
  if (/ProtocolConvertAnalyzerUsers\b/.test(path)) return 305_000
  // 短查询
  if (/TimeSpanConfigInterface\b/.test(path)) return 12_000
  if (/UserInfoInterface\b/.test(path)) return 12_000
  if (/UserModelListInterface\b/.test(path)) return 12_000
  if (/UserModelOptionsInterface\b/.test(path)) return 12_000
  if (/AppVersionInterface\b/.test(path)) return 12_000
  if (/GitInfoInterface\b/.test(path)) return 12_000
  if (/SystemInfoInterface\b/.test(path)) return 12_000
  // 默认 30s
  return 30_000
}

export const DEFAULT_TIMEOUT_MS = 30_000