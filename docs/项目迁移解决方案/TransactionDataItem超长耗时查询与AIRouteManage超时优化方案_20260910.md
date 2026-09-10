# TransactionDataItem 超长耗时查询兼容与 AIRouteManage 超时优化方案

**日期**：2026-09-10
**适用版本**：v2.0.78 起
**问题来源**：阶段BY/BX（`ResponsiveSvgChart` 趋势图改造）后端大盘生产实测发现：8 张分表 `TAgentHttpTransactionDataItem_00~_07` 累计 280GB+、单行 4MB、单分表最大 133GB（shard_00），导致：

1. 列表 / 详情 / 统计接口在 MySQL 25s ctx 与连接池 100 上限下频繁超时；
2. 管理员 Web 与用户 Web `/AIRouteManage` 页面"反复刷新都显示'请求超时，服务可能正在重启，请刷新页面重试'"；
3. 所有 `TAgentHttpTransactionDataItem` 相关页面（`ChatAnalysis`、`ChatAnalysisTotal`、`ChatAnalysisSession`、`ChatAnalysisTask`、`ModelInfo`、`AgentInfo`、`ProtocolConvertAnalyzer`、`CleanupReport`、`AIRouteManage`、`UserAIRoute`、`ModelInfoManage`、`AgentInfoManage` 等）需要一套面向"超长时间 MySQL 查询"的页面兼容方案。

---

## 1. 现状与根因分析（基于代码盘点）

### 1.1 后端热点（`ServerGo/`）

| 类别 | 文件:行 | 问题 |
|---|---|---|
| 大字段直读 | `models/subtable.go:956-978` `GetAgentHttpTransactionByID` | `First(&record)` 整行 4 个 longtext，单条响应 4MB；详情页频繁打开会反复拉 |
| 大字段直读 | `models/protocol_convert_analyzer.go:60-92` `GetProtocolConvertAnalyzerRecordDetailByID` | 同上，4 个 longtext 全 SELECT |
| OFFSET 深分页 | `models/subtable.go:789-852` `QueryAgentHttpTransactions` | `offset := (page-1)*pageSize` + `Offset().Find()`；深分页 O(N) 索引扫描 + 回表；pageSize 上限 100、深度无上限 |
| OFFSET 深分页 | `models/protocol_convert_analyzer.go:292` `QueryProtocolConvertAnalyzerRecordsByModel` | 同 OFFSET 模式 |
| OFFSET 深分页 | `models/mysql_http_agent_cleanup.go:907` `QueryCleanupReports` | 清理报告 OFFSET（影响小但模式一致） |
| 跨分表 GROUP BY 热点 | `models/mysql_http_agent_model_name_stats.go:59-240` `GetModelNameUsageStatsByRange` | 每张分表 3 条独立 GROUP BY（含 `COUNT(DISTINCT user_name)`），8 张 = 24 条 SQL |
| 跨分表 GROUP BY | `models/subtable.go:1677-1734` `GetModelInfoUsageStatsAll` | 8 表 × `GROUP BY(dst_model_name, user_name)`，基数爆炸 |
| 跨分表 GROUP BY | `models/mysql_http_agent_info_stats.go:90-143` `GetAgentInfoUsageStatsAll` | 8 表 × `GROUP BY(agent_tool_name, user_name)` |
| 写入 | `proxy/server_http_ai_proxy.go:414` | 唯一主动写入点（每请求 1 条 INSERT）；`go logAIProxyTransaction` 异步化但无批量、无重试、无限流 |
| 统计 ctx | `database/connect.go` | 默认 `StatsQueryTimeout = 25s`、连接池 100/10、慢查询阈值 2s；shard_00 133GB 下首屏大概率超时 |
| 详情批量加载 | `models/subtable.go:900-938` `GetAgentHttpTransactionFieldByID` | 已支持按需单列拉取，但前端 `chat-analysis` 默认展开时一次性拉 8 个长字段（4 个 longtext + 4 个 text） |

### 1.2 前端热点（`ClientWeb/src/`）

| 类别 | 文件:行 | 问题 |
|---|---|---|
| 全局默认超时 | `shared/api.js:11` | `DEFAULT_TIMEOUT_MS = 5000`（5s）——MySQL 25s 上下文下注定超时 |
| 超时硬编码文案 | `shared/api.js:31/33` | `'请求超时，服务可能正在重启，请刷新页面重试'` 直接 `throw new Error(...)` 硬编码中文，未走 i18n |
| 自动 reload 误触发 | `main.jsx:14` | `isChunkLoadError` 正则包含 `/Failed to fetch/i`，把任何网络层失败都当 chunk 错误 → `window.location.reload()` |
| App.jsx 强制刷新 | `App.jsx:67-82` | `UserInfoInterface` 非 401 失败时 `window.location.hash='#/Login'; window.location.reload()` |
| AIRouteManage 首屏 | `pages/AIRouteManage.jsx:65-117` | 挂载即 `loadRoutes` + `UserManageInterface(list)` 两条 5s 串行；任意超时 → 列表空 + 控件 disabled |
| AIRouteManage 控件 disabled | `pages/AIRouteManage.jsx` TimeRangeSelector | `levels=[]` 时整个 select 被 disabled，用户看不出还能点击 |
| 无骨架屏 | 全项目 | 仅 `table-loading` / `table-empty` 两态文字，无 skeleton |
| 无重试 UI | 全项目 | 所有 `setError(e.message)` 终端状态；用户必须手动刷新 |
| 长连接无超时 | `pages/ChatAnalysisTotal.jsx:135` `fetch(?stream=1)` / `pages/ChatAnalysisTotal.jsx:188` `openWs` | 原生 fetch 无超时上限；WS 仅 fallback HTTP；超长扫描无法取消 |
| 列表全量拉 | `pages/AIRouteManage.jsx`、`pages/DstEndPointManage.jsx`、`pages/UserManage.jsx`、`pages/ModelInfo/index.jsx`、`pages/AgentInfo/index.jsx`、`pages/ChatAnalysisSession.jsx`、`pages/ChatAnalysisTask.jsx` | 列表接口不走分页参数，前端切片；分表增长后单次响应可达 10MB+ |
| AIRouteManage batch_stats | `pages/AIRouteManage.jsx:148` | 管理端 1 条聚合；用户端循环 `model_name` 逐条 `count_record_by_protocol` → N 个 5s 并行请求 |
| 详情全量 | `pages/chat-analysis/useChatAnalysisData.js:95` `ChatAnalysisDetailInterface` | 首屏 `request_body` 自动加载 → 单条 4MB IO |

### 1.3 根因总结

1. **超时配置错位**：后端 25s ctx、前端 5s 兜底，请求必超时；超时后前端没有按接口分级、没有可恢复重试；
2. **接口大字段 IO 不可拆分**：详情整行 4MB、列表虽已白名单但 `batch_stats` 等聚合未走 keyset 分页；
3. **OFFSET 深分页 + 全量 GROUP BY**：在 100GB+ 分表上慢到必然 25s 超时；
4. **前端"超时即不可恢复"**：文案 + `window.location.reload()` 把一次短暂网络抖动放大为"整页反复刷新"。

---

## 2. 设计目标

1. **零丢失**：现有功能（列表、详情、统计、批量、导出、清理报告）100% 保留行为不变；
2. **可恢复**：超时 / 网络错误不能直接 reload，必须给用户"重试 / 后台加载 / 切换档位"三种出口；
3. **可观测**：长查询必须有进度反馈（行数 / 百分比 / 阶段文字）；
4. **可中断**：长查询必须可取消（`AbortController` 一直传到后端 gorm ctx）；
5. **零管理代码泄露**：`dist-user` 仍不含管理端代码（CLAUDE.md §2.5）；
6. **三语同步**：所有新增 / 修改文案在 zh-CN / en / ja 三语同步；
7. **不改架构**：不引入新中间件、不上 ES、不改分表策略（保留 8 表），只做接口分级 / IO 拆分 / 前后端协同。

---

## 3. 方案总体架构

```
┌──────────────────────────────────────────────────────────────────────────────┐
│ 浏览器（用户 Web 29001 / 管理端 9101）                                         │
│                                                                              │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│ │  前端共享层                                                              │  │
│ │   shared/api.js   ── 默认超时 → 30s，按接口覆写（[60s, 300s, never]）    │  │
│ │   shared/retry.js ── 指数退避重试（GET 最多3次，写操作禁止）              │  │
│ │   shared/longFetch.js ── 长查询封装（progress + AbortController）         │  │
│ │   main.jsx       ── 移除 /Failed to fetch/i 兜底；chunk 错误才 reload    │  │
│ │   components/Skeleton.jsx ── 骨架屏（表格 / 卡片 / 趋势图三态）           │  │
│ │   components/EmptyState.jsx ── 空态（含「重试」「切换档位」按钮）         │  │
│ │   components/LongQueryProgress.jsx ── 进度条 + 「取消」按钮              │  │
│ └────────────────────────────────────────────────────────────────────────┘  │
│                              ▲ fetch（带超时/retry/progress）                │
└──────────────────────────────┼───────────────────────────────────────────────┘
                               │
┌──────────────────────────────┼───────────────────────────────────────────────┐
│ Go API（9101 / 29001）        ▼                                               │
│  ┌────────────────────────────────────────────────────────────────────────┐  │
│ │  新增中间件                                                               │  │
│ │   api/middleware/query_classifier.go ── 按 URL 分类（list/detail/stats）│  │
│ │   api/middleware/long_query_ctx.go    ── 长查询 ctx 5min，AbortController│  │
│ │                                                                            │  │
│ │  改造点（按接口分级 ctx）                                                  │  │
│ │   - list/分页查询      → 30s ctx（GORM WithContext）                      │  │
│ │   - 单条详情（除大字段）→ 10s ctx                                          │  │
│ │   - 大字段按需加载      → 60s ctx，单列 SELECT                             │  │
│ │   - 统计聚合（All）     → 300s ctx，limit 收敛 + keyset 分页               │  │
│ │   - 长流式（SSE/WS）    → 客户端断连即终止                                  │  │
│ │                                                                            │  │
│ │  新增 / 改造 DAO                                                           │  │
│ │   - QueryAgentHttpTransactionsKeyset（替代 OFFSET）                        │  │
│ │   - GetAgentHttpTransactionMetaByID（不含 longtext 的元数据单条）            │  │
│ │   - GetAgentHttpTransactionLargeField（单列按需）                          │  │
│ │   - QueryAgentHttpTransactionsCountFast（Covering Index 命中，不读数据）   │  │
│ │   - 跨分表全站聚合：scanShardPaged（已有） + 并发上限 4                     │  │
│ └────────────────────────────────────────────────────────────────────────┘  │
│                              ▲ GORM WithContext(ctx)                          │
└──────────────────────────────┼───────────────────────────────────────────────┘
                               │
                MySQL 8.x（280GB，8 分表）
```

---

## 4. 后端详细设计

### 4.1 接口超时分级（按 URL 路径分类）

新增文件：`ServerGo/api/middleware/query_classifier.go`

```go
package middleware

// QueryClass 决定 ctx 超时上限
type QueryClass int

const (
    QShort QueryClass = iota // 10s  : 用户信息、配置、小字典
    QNormal                  // 30s  : 列表分页、单条元数据、统计短查询
    QDetail                  // 60s  : 大字段按需加载、单行详情的 longtext
    QLong                    // 300s : 跨分表全站聚合、All 变体、SSE
    QStream                  // 不超时：SSE / WS；客户端断连由 ctx.Done() 取消
)

func ClassifyQuery(path string) QueryClass {
    switch {
    case strings.HasSuffix(path, "/ChatAnalysisTotalRangeInterface"),
         strings.HasSuffix(path, "/ChatAnalysisTotalWS"),
         strings.HasSuffix(path, "/CleanupReportInterface"),
         strings.Contains(path, "/AIRouteManageInterface"),
         strings.Contains(path, "/UserAIRouteInterface"),
         strings.Contains(path, "/ModelInfoInterface"),
         strings.Contains(path, "/AgentInfoInterface"):
        return QLong
    case strings.HasSuffix(path, "/ChatAnalysisDetailInterface"),
         strings.HasSuffix(path, "/ProtocolConvertAnalyzerRecordDetail"):
        return QDetail
    case strings.HasSuffix(path, "/TimeSpanConfigInterface"),
         strings.HasSuffix(path, "/UserInfoInterface"),
         strings.HasSuffix(path, "/UserModelListInterface"):
        return QShort
    default:
        return QNormal
    }
}
```

中间件文件：`ServerGo/api/middleware/long_query_ctx.go`

```go
func QueryContextMiddleware(timeout time.Duration) gin.HandlerFunc {
    return func(c *gin.Context) {
        klass := ClassifyQuery(c.Request.URL.Path)
        var deadline time.Duration
        switch klass {
        case QShort:  deadline = 10 * time.Second
        case QNormal: deadline = 30 * time.Second
        case QDetail: deadline = 60 * time.Second
        case QLong:   deadline = 300 * time.Second
        case QStream: deadline = 0 // 不超时，依赖 ctx.Done()
        }
        if deadline > 0 {
            ctx, cancel := context.WithTimeout(c.Request.Context(), deadline)
            c.Request = c.Request.WithContext(ctx)
            defer cancel()
        }
        c.Set("query_class", klass)
        c.Next()
    }
}
```

在 `ServerGo/api/routes.go` 注册 `engine.Use(QueryContextMiddleware(...))`，对管理端与用户端统一生效。

### 4.2 DAO 层改造

#### 4.2.1 keyset 分页（替代 OFFSET）

文件：`ServerGo/models/subtable.go`

```go
// QueryAgentHttpTransactionsKeyset 替代 QueryAgentHttpTransactions 的 OFFSET 版本
//
// 入参：cursor *TransactionCursor（nil=首页），pageSize（白名单 10/20/50/100）
// 返回：rows + nextCursor（nil=已到末页）
//
// 索引命中：idx_user_model_created(user_name, model_name, created_at)
// 排序：ORDER BY id DESC（主键倒序，避免 created_at 同值 tie）
//
// 与原 QueryAgentHttpTransactions 并行保留一阶段，二阶段灰度替换。
```

接口请求体新增字段 `cursor_id`（int64），`page` 字段降级为兼容项。

#### 4.2.2 大字段按需加载

文件：`ServerGo/models/subtable.go`

```go
// GetAgentHttpTransactionMetaByID 不含 longtext 的元数据单条
// SELECT 白名单 28 列（排除 4 个 longtext + 4 个 text）
func GetAgentHttpTransactionMetaByID(userName, modelName string, id uint64) (*TAgentHttpTransactionDataItem, error)

// GetAgentHttpTransactionLargeField 单列按需加载（已有 GetAgentHttpTransactionFieldByID）
// 改造：ctx 60s，错误返回 `field_not_loaded`，前端按需 retry
```

#### 4.2.3 COUNT 收敛（Covering Index）

```go
// QueryAgentHttpTransactionsCountFast
//   命中覆盖索引 idx_user_model_created，避免回表
//   SQL: SELECT COUNT(*) FROM ... WHERE user_name=? AND model_name=? [AND ...]
//   原 CountAgentHttpTransactions 改为内部调用此函数
```

#### 4.2.4 跨分表聚合并发收敛

文件：`ServerGo/models/mysql_http_agent_all_stats.go`

```go
// runAllShardAggregators 并发上限 4，跑 8 张表的全站聚合
// 用 errgroup + semaphore(4)，单表失败不阻塞其他表
func runAllShardAggregators[T any](
    fn func(subTableIdx int) (T, error),
) ([]T, []error) { ... }
```

替换 `GetTimeRangeStatsAll`、`GetTokensRangeStatsAll`、`GetProtocolAnalysisStatsAll`、`GetAgentToolStatsByRangeAll`、`CountAgentHttpTransactionsAll`、`GetAllStatsKPISummary`、`GetHourlyTrendAll`、`GetModelNameUsageStatsByRange`、`GetDstModelUsageStatsByUserModel`、`GetModelInfoUsageStatsAll`、`GetAgentInfoUsageStatsAll` 等"全表循环"实现。

#### 4.2.5 写入并发收敛

文件：`ServerGo/proxy/server_http_ai_proxy.go` 与 `ServerGo/proxy/server_http_ai_proxy_utils.go`

```go
// 新增 transactionWriteSem = make(chan struct{}, 32)
// 替代 agentHttpSubTableMutex：分段锁 + 信号量，让不同 (user, model) 并发落库
// 减少 shard_00 串行化造成的写入瓶颈
```

#### 4.2.6 详情接口 ctx 注入

文件：`ServerGo/api/server_api_manager_chat_analysis.go`、`server_api_user_chat_analysis.go`、`server_api_protocol_converter.go`

```go
// ChatAnalysisDetailInterfaceHandler 接收 c.Request.Context()
// gorm.WithContext(ctx).Table(...).Select("request_body").Take(...)
// 大字段按需 ctx=60s（中间件已设置）
```

### 4.3 API 路由改造

| 接口 | 改造前 | 改造后 |
|---|---|---|
| `ChatAnalysisInterface` | `page/page_size` | `cursor_id/page_size`（keyset），保留 `page` 兼容 |
| `ChatAnalysisDetailInterface` | 单次拉全部字段 | `field` 单列 + `meta` 元数据分离（前端先 meta 渲染表格，再按需 lazy load 大字段） |
| `ProtocolConvertAnalyzerRecords` | OFFSET | keyset + `cursor_id` |
| `ProtocolConvertAnalyzerRecordDetail` | 整行 longtext | `field` 单列按需 |
| `CleanupReportInterface` | OFFSET | keyset |
| `AIRouteManageInterface` / `UserAIRouteInterface` | action=list 全量 | action=list 仍全量（路由表小），但新增 `action=batch_stats_v2` 用 `?stream=1` SSE 分块推送，避免 5s 超时 |
| `ModelInfoInterface` / `AgentInfoInterface` action=stats | 全表 8 GROUP BY | ctx=300s + 并发 4；返回前如果 ctx 即将到期则返回 partial + warning |
| `ChatAnalysisTotalInterface` action=full_http | 单次 25s 超时 | ctx=300s + `partial=true` 字段支持部分返回 |

### 4.4 后端新增字段

| 字段 | 位置 | 类型 | 含义 |
|---|---|---|---|
| `partial` | `AgentHttpQueryResult` | bool | true 表示 ctx 超时部分返回（前端展示"已加载 N/M 行"） |
| `warning` | 顶层 | string | 超时/部分返回时的可读说明 |
| `cursor_id` | `AgentHttpQueryResult` | int64 | 下一页 keyset 起点（nil=末页） |
| `totalCountApprox` | `AgentHttpQueryResult` | int64 | 近似总数（来自 Covering Index COUNT，快） |
| `query_time_ms` | 顶层 | int64 | 服务端实际查询耗时（用于前端 debug） |

### 4.5 后端测试

文件：`ServerGo/api/middleware/query_classifier_test.go`、`ServerGo/models/subtable_keyset_test.go`

- 单元测试：keyset 分页正确性（顺序、无重复、首末页）
- 中间件测试：URL → QueryClass 映射、ctx 超时触发取消
- 性能测试：`BenchmarkQueryAgentHttpTransactionsKeyset` vs `BenchmarkQueryAgentHttpTransactionsOffset`（用生产 mock 表 100 万行）

---

## 5. 前端详细设计

### 5.1 `shared/api.js` 重构

```js
// 旧：DEFAULT_TIMEOUT_MS = 5000
// 新：默认 30s；按接口 URL 后缀覆写
function timeoutFor(path) {
  if (/\/ChatAnalysisTotalRangeInterface|\/ChatAnalysisTotalWS|\/CleanupReportInterface|\/AIRouteManageInterface|\/UserAIRouteInterface|\/ModelInfoInterface|\/AgentInfoInterface/.test(path)) return 300_000
  if (/\/ChatAnalysisDetailInterface|\/ProtocolConvertAnalyzerRecordDetail/.test(path)) return 60_000
  if (/\/TimeSpanConfigInterface|\/UserInfoInterface|\/UserModelListInterface/.test(path)) return 10_000
  return 30_000 // 默认
}
```

错误文案改为 i18n：

```js
if (err.name === 'AbortError') throw new ApiError('timeout', t('errors.timeoutDetailed', { phase: options.phase || '' }))
if (err.name === 'TypeError' && /Failed to fetch/i.test(err.message)) throw new ApiError('network', t('errors.networkDetailed'))
```

新增 `ApiError` 类：携带 `code: 'timeout' | 'network' | 'http_5xx' | 'http_4xx' | 'aborted' | 'unknown'`，供组件按 code 决定重试 / 降级。

### 5.2 `shared/retry.js` 新增

```js
// 仅 GET 重试，写操作（add/update/delete）禁止自动重试
export async function retryGet(path, options = {}, policy = { max: 3, baseMs: 800, factor: 2 }) {
  let attempt = 0, lastErr
  while (attempt <= policy.max) {
    try { return await get(path, options) }
    catch (e) {
      if (e.code === 'http_4xx') throw e // 4xx 不重试
      if (e.code === 'aborted') throw e
      lastErr = e
      const delay = policy.baseMs * Math.pow(policy.factor, attempt) + Math.random() * 200
      await new Promise(r => setTimeout(r, delay))
      attempt++
    }
  }
  throw lastErr
}
```

### 5.3 `shared/longFetch.js` 新增（长查询 + 进度 + 取消）

```js
// 长查询 SSE 风格分块进度：服务端定期 send "progress" 事件，前端累计
// 也支持 AbortController.onCancel → fetch.abort()
export function longFetch(path, { body, onProgress, signal, timeoutMs = 300_000 }) { ... }
```

### 5.4 `main.jsx` 修复

```js
// 旧：isChunkLoadError 包含 /Failed to fetch/i
// 新：仅匹配真正 chunk 错误
const isChunkLoadError = (message) =>
  /Loading chunk|error loading dynamically imported module|Importing a module script failed|Loading CSS chunk|chunk.*404/i.test(message)
```

`UserInfoInterface` 在 `App.jsx:67` 非 401 失败时改为：不 reload，只显示 `Layout` 顶部横幅 + `重试` 按钮（最多 3 次指数退避）；只有最后一次仍失败才 fallback 到 Login 路由。

### 5.5 通用组件

#### `components/Skeleton.jsx`

```jsx
// 三种 preset：table(10 行)、card(4 卡片)、chart(40% 高度)
export function Skeleton({ preset = 'table', rows = 6 }) { ... }
```

样式：在 `index.css` 加 `.skeleton-pulse { animation: skeletonPulse 1.4s ease-in-out infinite; }` 灰白渐变。

#### `components/EmptyState.jsx`

```jsx
export function EmptyState({ icon, title, hint, onRetry, onChangeFilter, retryLabel }) { ... }
```

用于 `AIRouteManage`、`DstEndPointManage`、`UserManage`、`ModelInfo`、`AgentInfo`、`ChatAnalysisSession/Task` 等所有列表空态；点击重试 → 重新调 load*；点击切换档位 → 弹出 TimeRangeSelector。

#### `components/LongQueryProgress.jsx`

```jsx
export function LongQueryProgress({ percent, rows, phase, onCancel, message }) { ... }
```

样式：顶部固定条 + 「取消」按钮（`AbortController.abort()`）。

### 5.6 `pages/AIRouteManage.jsx` 修复

具体修改清单（行号以当前文件为准）：

1. **`loadRoutes`**：用 `retryGet` 替代裸 `post`；失败 → `setError(err.message)` + 「重试」按钮。
2. **`levelsLoading`**：`useTimeSpanLevels` 改为内部用 `retryGet` + skeleton；`TimeRangeSelector` 在 levels 为空时显示骨架而非 disabled。
3. **`onUserChange`**：`Promise.all` 改为 `Promise.allSettled`，单条失败不影响其他；任一失败 → 局部提示 + 「重试」按钮。
4. **加载态**：将 `<DataTable loading={loading}>` 替换为 `loading ? <Skeleton preset="table" rows={6}/> : <DataTable ...>`。
5. **错误态**：`setError(e.message)` 后面包一层 `<EmptyState onRetry={loadRoutes}>`，让用户主动重试。
6. **batch_stats**：管理端 1 条已聚合，OK；用户端改为新接口 `UserAIRouteInterface action=batch_stats_v2`，单次聚合 + 分块 SSE 推送（避免 N 个 5s 请求）。
7. **首屏骨架**：`return` 最外层 JSX 顶层用 `<Skeleton preset="table"/>` 替换整页直到 `routesLoaded && levelsLoaded` 双 true。

### 5.7 `pages/chat-analysis/useChatAnalysisData.js` 修复

1. **`fetchList`**：cursor 分页；首屏 `loadRows(cursor=null, pageSize=20)`，滚动到底自动 loadMore；提供 `loadingMore` state。
2. **`fetchDetail`**：`meta` 必拉（28 列），大字段 4 个 longtext + 4 个 text 改为按 tab 切换时按需拉（`request_body` / `response_body` / `request_headers` / `response_headers`）；每个字段单独 loading；缓存命中不重拉。
3. **`error`**：使用 `<EmptyState onRetry={fetchList}>`；提供「重新筛选」「切换档位」按钮。

### 5.8 `pages/ChatAnalysisTotal.jsx` 修复

1. **`runRangeReport`**：套 `longFetch`；onProgress 显示「扫描中… 已处理 N 行 / 约 M 行」；onCancel 触发 AbortController。
2. **HTTP fallback**：用 `retryGet`；ctx 超时时返回 `partial=true` + warning，前端展示「已加载部分数据，点击重新加载」按钮。
3. **WS fallback**：保留，但 `onError` 时不再直接 fallback HTTP，而是先展示「WS 失败，正在重试…（第 N/3 次）」按钮。

### 5.9 `pages/ModelInfo/index.jsx`、`pages/AgentInfo/index.jsx` 修复

1. 列表/统计加载用 skeleton + `retryGet`。
2. `HourlyTrendPanel` 调用 `action=trend` 的 ctx 已是 300s，无需改；但前端补 timeout 兜底（300s 匹配）。
3. 空态用 `<EmptyState onRetry={loadAll} hint={t('xxx.tryChangeDays')}/>`。

### 5.10 三语 i18n 补齐

文件：`ClientWeb/src/i18n/locales/{zh-CN,en,ja}.json`

新增键：

```jsonc
{
  "errors.timeoutDetailed":      { "zh-CN": "请求超时：{phase}（服务端正在处理，可能需要几十秒到几分钟，请稍候或点击重试）",
                                   "en": "Request timeout: {phase}. The backend may take seconds to minutes. Please wait or retry.",
                                   "ja": "リクエストタイムアウト：{phase}。サーバーは数秒～数分かかる場合があります。再試行してください。" },
  "errors.networkDetailed":      { "zh-CN": "网络错误，无法连接到 Web 服务（可能正在重启，请稍后重试）",
                                   "en": "Network error. The web service may be restarting, please retry later.",
                                   "ja": "ネットワークエラー。Web サービスは再起動中の可能性があります。" },
  "errors.partialResult":        { "zh-CN": "查询超时，已加载部分数据（{rows} 行）",
                                   "en": "Query timed out, partial data loaded ({rows} rows)",
                                   "ja": "クエリタイムアウト。部分データを読み込みました（{rows} 件）" },
  "common.retry":                { "zh-CN": "重试", "en": "Retry", "ja": "再試行" },
  "common.cancel":               { "zh-CN": "取消", "en": "Cancel", "ja": "キャンセル" },
  "common.longQueryTitle":       { "zh-CN": "长时间查询中…", "en": "Long-running query…", "ja": "長時間クエリ実行中…" },
  "common.longQueryHint":        { "zh-CN": "数据量较大，已处理 {rows} 行 / 约 {approx} 行（约 {pct}%）",
                                   "en": "Large dataset. Processed {rows} / ~{approx} rows (~{pct}%)",
                                   "ja": "大量データ処理中：{rows} / 約 {approx} 行（約 {pct}%）" },
  "aiRouteManage.reloadList":    { "zh-CN": "重新加载路由列表", "en": "Reload route list", "ja": "ルート一覧を再読み込み" },
  "aiRouteManage.changeDays":    { "zh-CN": "切换统计档位", "en": "Change time range", "ja": "統計期間を変更" },
  "modelInfo.emptyRetry":        { "zh-CN": "暂无模型数据，点击重试或切换统计档位",
                                   "en": "No model data. Click retry or change the time range.",
                                   "ja": "モデルデータがありません。再試行または期間を変更してください" },
  "agentInfo.emptyRetry":        { "zh-CN": "暂无 Agent 数据，点击重试或切换统计档位",
                                   "en": "No agent data. Click retry or change the time range.",
                                   "ja": "Agent データがありません。再試行または期間を変更してください" }
}
```

### 5.11 前端测试

文件：`ClientWeb/src/shared/api.test.js`、`shared/retry.test.js`、`components/Skeleton.test.jsx`、`components/EmptyState.test.jsx`、`pages/AIRouteManage.test.jsx`

- 单测：`retryGet` 指数退避正确；`timeoutFor` 路径映射正确；`Skeleton` 三种 preset 渲染。
- 集成测试：用 vitest + jsdom + `fetch-mock` 模拟 25s 慢响应 → 验证组件显示 skeleton + 「重试」按钮而非直接 reload。

---

## 6. 验证清单（实施完成后逐项打勾）

### 6.1 后端

- [ ] `go build ./...` 通过；
- [ ] `go test ./...` 全绿（含新增 `query_classifier_test.go` / `subtable_keyset_test.go`）；
- [ ] `mysql -e "EXPLAIN SELECT ... FROM TAgentHttpTransactionDataItem_00 WHERE user_name=? AND model_name=? ORDER BY id DESC LIMIT 20"` 命中索引；
- [ ] 模拟 1 万行分表：keyset vs OFFSET 性能对比（keyset < 100ms，OFFSET 大值 > 5s）；
- [ ] curl 触发 `/ChatAnalysisInterface` 超时场景，验证 `partial=true` 返回。

### 6.2 前端

- [ ] `npm run build` 双构建通过（`dist-manager` + `dist-user`）；
- [ ] `dist-user` 不含管理代码（grep `AIRouteManageInterface.*action='add'` 应为空）；
- [ ] 三语 i18n 完整：所有新增键三种语言均存在；
- [ ] AIRouteManage 页面：手动 `slowDown=2s` mock 后能看到 skeleton + 「重试」按钮，而不是 "请求超时" 死循环；
- [ ] 用无头浏览器（harness）截图 9101 AIRouteManage 与 29001 AIRouteManage，确认 controls 可点、表头可换；
- [ ] 清理 `main.jsx` 自动 reload 误触发：mock 一个 `TypeError: Failed to fetch` → 不应触发 reload，仅显示横幅。

### 6.3 集成

- [ ] `./rebuild_restart_app.sh` 通过；
- [ ] curl 探测 9101 / 29001 均 200；
- [ ] 模拟生产数据（mock 100GB+ 分表）下，所有 `TAgentHttpTransactionDataItem` 相关页面首屏 < 8s 出表格，超时不刷新；
- [ ] 中文 commit 提交：阶段BZ：TransactionDataItem 超长耗时查询与 AIRouteManage 超时优化。

---

## 7. 风险与回退

| 风险 | 触发条件 | 回退方案 |
|---|---|---|
| keyset 分页导致前端无 `totalCount` | 用户期望总页数 | 保留 `totalCountApprox` 字段（来自 Covering Index COUNT） |
| 60s 详情 ctx 仍不够（单行 longtext 4MB） | 极端大 body（> 4MB） | 单列 SELECT + 流式读取（`Rows()` 而非 `Take()`），前端边读边渲染 |
| 客户端 retry 期间用户重复点 | 网络抖动 | AbortController + seqRef，重复请求复用同一个 controller |
| 移除 `/Failed to fetch/i` 兜底后真正 chunk 漏掉 | 罕见 chunk 404 | 保留 `Loading chunk|chunk.*404` 三条核心正则 |
| `dist-user` 引入新管理代码 | 误用 `__APP_ROLE__ !== 'user'` | 在 `chat-analysis/InlineDetailRow` / `BatchEditModal` 等处增加 `if (__APP_ROLE__ !== 'manager') return null` 守卫 |

---

## 8. 实施切片（每切片独立可测、可提交）

| 切片 | 内容 | 文件 | 测试 |
|---|---|---|---|
| BZ-1 | 后端 ctx 分级中间件 + URL 分类 | `api/middleware/query_classifier.go`、`long_query_ctx.go`、`routes.go` | `query_classifier_test.go` |
| BZ-2 | keyset 分页 + Covering Index COUNT | `models/subtable.go` 新函数 + 灰度替换 | `subtable_keyset_test.go` |
| BZ-3 | 大字段按需元数据/单列分离 | `subtable.go`、`chat_analysis.go`、`protocol_convert_analyzer.go` | 单测 |
| BZ-4 | 跨分表聚合并发收敛 + 300s ctx | `mysql_http_agent_all_stats.go`、`info_stats.go`、`model_name_stats.go` | 性能对比 |
| BZ-5 | 前端 `api.js` 重构 + `retryGet` + `longFetch` + `main.jsx` 误触发修复 | `shared/api.js`、`shared/retry.js`、`shared/longFetch.js`、`main.jsx` | `api.test.js`、`retry.test.js` |
| BZ-6 | 通用组件 `Skeleton` / `EmptyState` / `LongQueryProgress` | `components/{Skeleton,EmptyState,LongQueryProgress}.jsx`、`index.css` | `.test.jsx` |
| BZ-7 | AIRouteManage 重写首屏 + 控件修复 | `pages/AIRouteManage.jsx` | 集成测试 |
| BZ-8 | chat-analysis 详情懒加载大字段 + cursor 分页 | `pages/chat-analysis/useChatAnalysisData.js`、`InlineDetailRow.jsx`、`DetailTabs.jsx` | 集成测试 |
| BZ-9 | ModelInfo / AgentInfo / ChatAnalysisTotal / Session / Task / CleanupReport 接入 | `pages/...` | 集成测试 |
| BZ-10 | 三语 i18n 补齐 + API 部分返回字段 | `locales/*.json`、后端 DTO 字段 | grep 校验 |
| BZ-11 | rebuild + curl + 无头浏览器截图 + 中文 commit | — | 验证清单 |

---

## 9. 与既有方案的衔接

- **CLAUDE.md §2.5 双构建隔离**：本方案新增的 `Skeleton` / `EmptyState` / `LongQueryProgress` / `retryGet` 均放在 `shared/` 与 `components/` 下，非角色依赖；`AIRouteManage` 主组件保留原有 manager/user 路由分流。`BatchEditModal` 已是 manager-only 动态 import，本次不动。
- **CLAUDE.md §2.6 安全**：本方案不引入新凭据、新接口；`api_key` 字段后端继续 `redactAPIKey`；新增 ctx 仅控制超时，不绕过 `ManagerAuthMiddleware`。
- **CLAUDE.md §2.1 编译/启动**：所有改动通过 `./rebuild_restart_app.sh` 验证；不直接 `go build`。
- **阶段BY/BX 趋势图**：本方案不修改 `KLineTrendChart` / `ResponsiveSvgChart`；趋势图依赖的 `action=trend` 接口新增 ctx=300s 覆盖即可。

---

## 10. 预期收益

1. **首屏成功率**：从当前 ~30%（shard_00 133GB 必超时）提升至 ≥ 99%（ctx 5min + retry + skeleton 三层兜底）；
2. **AIRouteManage 控件可用率**：从当前 "反复刷新仍超时" 提升至 100% 可用（重试按钮 + 档位切换）；
3. **详情页打开**：单条从 4MB 整行降至首屏 ~5KB 元数据 + 按需懒加载 4 个 longtext（任一时刻 < 200KB）；
4. **统计页**：跨分表全站聚合从 25s 必超时降至 60-180s 完成（300s ctx + 并发 4）；
5. **可中断**：用户可主动取消长查询（`AbortController` 透传到 gorm ctx）。