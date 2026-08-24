# CLAUDE.md - LsmTokensServer Claude Code 规范

> **加载顺序**: Claude Code 先读本文件，再按需读 [`AGENT.md`](AGENT.md)（通用 AI Agent 规则）、[`Developer_SOP.md`](Developer_SOP.md)（详细 SOP）、[`AGENT_INDEX.md`](AGENT_INDEX.md)（完整源码目录）。
> **版本历史**: 完整版本历史（v2.0.0-v2.0.68）见 [`CHANGELOG.md`](CHANGELOG.md)。

**当前版本**: v2.0.72
- v2.0.72: **OpenAI ↔ Anthropic 协议转换优化**（基于 Switchyard 开源翻译层调研，见 [`docs/Switchyard_OpenAI_Anthropic_Exchange.md`](docs/Switchyard_OpenAI_Anthropic_Exchange.md) 与 [`docs/LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md`](docs/LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md)）：
  - **修复 ①：P0-1 `AnthropicContentBlock` 平铺线格式建模**（`protocol_types.go`）：真实 Anthropic 线格式是平铺的 `{"type":"tool_use","id":...,"name":...,"input":{...}}`，此前嵌套建模（`ToolUse *AnthropicToolUse` / `ToolResult *AnthropicToolResult`）导致真实 JSON 反序列化后 tool_use/tool_result **永远为 nil 整体丢失**——a2o 非流式响应 `finish_reason=tool_calls` 与空 tool_calls 同时在场，客户端 Agent 工具循环必断；o2a 响应序列化产出非法嵌套结构。改为平铺字段（ID/Name/Input/ToolUseID/Content/IsError/Source），删除 `AnthropicToolUse`/`AnthropicToolResult` 类型与 `AnthropicSSEEvent` 死代码。既有测试全部用 Go struct 构造输入恰好绕过此缺陷——新测试全部改用真实线格式 JSON 字符串输入。
  - **修复 ②：P0-4 a2o 请求 tool_result 拆分**：`convertAnthropicContentBlocksToOpenAI` 返回消息序列——每个 tool_result 块 → 独立 `role=tool` 消息（`tool_call_id` 保留，`is_error` 加 `[ERROR] ` 前缀，数组 content 拍平），此前降级为拼进 user 消息的纯文本。
  - **修复 ③：P0-5 多模态双向转换**：o2a `image_url` 三形态（对象 url / data URI 拆 base64+media_type / 裸字符串）→ 合法 Anthropic image source（此前产出无 source 的空 image 块，Anthropic 必 400）；a2o image 块 → `image_url` part（此前静默消失）。
  - **修复 ④：P0-6 `parseSSEEvents` 兼容 CRLF**：每行 `TrimRight("\r")`（此前 `\r\n` 流中空白行是 `"\r"`，事件永不 flush、data 粘连全流解析失败）；流尾残留事件保留。
  - **修复 ⑤：P0-3 上游 SSE → 客户端 stream 输出协议自洽**：`convertProxyResponse` SSE 分支聚合转换后经 `wrapConvertedResponseAsSSE` 按客户端协议重包装成合法 SSE 事件流（此前把单个 JSON blob 配上 `text/event-stream` 直发，客户端 SSE 解析器必炸）。**注意：整包缓冲（P0-2）本版保留**，真流式逐事件转换（StreamTranslationState 状态机方案）列入二期，见优化方案文档 §5。
  - **修复 ⑥：P0-7 `wrapAnthropicResponseAsSSE` tool_use 完整化**：`content_block_start` 携带 id/name/`input:{}`，随后发 `input_json_delta`；空 content 补空 text 块（合法 Anthropic 流）；thinking 块补 thinking_delta。
  - **修复 ⑦：P1 批次**：max_tokens 双字段缺失默认 8192（`o2aDefaultMaxTokens`，Anthropic 必填）；`Temperature`/`TopP` 改 `*float64`（显式 0 值透传）；连续 `role=tool` 消息合并为一条 user 消息多 tool_result 块（Anthropic user/assistant 交替约束）；tool_call Arguments 解析失败包 `{"raw":...}`、空串→`{}`；缺失 tool_use id 生成 `toolu_o2a_%08d` 确定性 id + 非法字符清洗 `_`；a2o 响应 `Created=time.Now().Unix()`（此前硬编码 0）；id 前缀改写 `msg_`↔`chatcmpl_`；stop/finish 映射补齐 refusal/pause_turn/model_context_window_exceeded/function_call/content_filter + **未识别值原样透传**；cache token 双向映射（`cache_read/cache_creation` ↔ `prompt_tokens_details.cached_tokens`，o2a input_tokens 减缓存防重复计数）；Anthropic system 数组形态拼接 text（不再 JSON dump）；`metadata.user_id`→`user`；未知 role 归并 user；OpenAI SSE 聚合按协议 `index` 字段分桶（`OpenAIToolCall.Index *int`，废除 `choice.Index*1000+idx` 串桶）；`input_json_delta` 先于 start 到达时按 tool_use 建块；`signature_delta` 保留。
  - **版本同步**：`APP_VERSION` 升级至 `v2.0.72`
  - **新增 `v2072_protocol_converter_fix_test.go`**（23 个子测试全过，全部真实线格式 JSON 输入）；适配 2 个既有测试文件（Temperature 指针 + 平铺字段）。唯一失败 `TestChatStatsAggregator_AddRowAndSnapshots` 为既有时间窗口缺陷（干净树上同样失败，与本次无关）。
  - **强制规则**：
    > ✅ `AnthropicContentBlock` 必须保持平铺线格式建模（id/name/input/tool_use_id/content/is_error 在块顶层），禁止恢复嵌套 `ToolUse`/`ToolResult` 子结构——嵌套建模会让真实 Anthropic JSON 反序列化后工具调用整体丢失且测试难以发现。
    > ✅ 协议转换测试必须用真实线格式 JSON 字符串做输入，禁止只用 Go struct 构造输入——struct 构造会绕过 unmarshal 路径，让建模与线格式不符的缺陷完全隐形（本版 P0-1 潜伏至今的根因）。
    > ✅ a2o 消息转换中 tool_result 块必须拆分为独立 `role=tool` 消息并保留 `tool_call_id`，禁止降级为拼进 user 消息的纯文本（工具循环关联断裂）。
    > ✅ o2a 连续 `role=tool` 消息必须合并为一条 user 消息的多个 tool_result 块，禁止逐条独立成 user 消息（违反 Anthropic user/assistant 交替约束）。
    > ✅ `convertProxyResponse` 写出体的协议形态必须与 Content-Type 自洽：标 `text/event-stream` 就必须是合法 SSE 事件序列，禁止把聚合后的单个 JSON blob 配 SSE 头直发。
    > ✅ Anthropic 方向请求必须保证 `max_tokens > 0`（缺省走 `o2aDefaultMaxTokens`），禁止产出 `max_tokens:0` 的非法请求。
    > ✅ stop/finish_reason 映射未识别的值必须原样透传，禁止静默置空——新 stop reason 不应因代理升级滞后而消失。
    > ✅ SSE 解析必须兼容 CRLF 且保留流尾残留事件（上游漏发末尾空行不丢最后一个 chunk）。

---

## 历史版本（最近：v2.0.71）
- v2.0.71: **重构 `/AIRouteManage`「最后记录信息列」**：「最后使用」「最后响应状态」「最后目标模型」3 列在服务端按 response_status 是否 2xx 合并为「最后成功记录」「最后失败记录」2 列（均不可排序，删除 last_used 排序三件套）。
  - 数据层 `getRouteLastRecordByStatus(userName, modelName, protocolType, subTableNum, success bool)`：成功 `AND response_status LIKE ?`（参数 `"2%"`），失败 `AND NOT LIKE ?`（空串=传输层错误=失败）。**LIKE 模式必须参数化**——内联 `'2%'` 会被 `fmt.Sprintf` 把 `%` 当动词展开成 `%!(...)`。
  - 复用 `BatchGetRouteLastUsedTimes` 批量链路（每 key 两次查询），cacheKey 前缀 `"GetRouteLastRecordByStatus"` + success/failure 段隔离；`RouteBatchStatResult` 字段整体替换为 `LastSuccess*`/`LastFailure*` 两组同构字段（时间/状态/目标模型同源同行）。
  - `enrichRoute`/`enrichRouteForUser` 的 `else`（模型查找失败）分支必须显式写入两组 `*_failed=true`；「查询失败」（红粗）与「暂无成功/失败记录」（灰斜体）三态可区分。
  - 新增 `v2071_route_last_success_failure_test.go`（17 个子测试）；删除 v2069/v2070 测试文件；v2066 测试收缩为仍成立契约。

---

## 历史版本（最近：v2.0.69）
- v2.0.69: **新增 `/AIRouteManage`「最后响应状态」列**（v2.0.71 已并入「最后成功/失败记录」两列）：
  - **目标**：在「最后使用」列右侧新增「最后响应状态」列，展示 `TAgentHttpTransactionDataItem.ResponseStatus` 最后一次值并视觉标记正常/异常；不引入 N+1、不破坏 v2.0.66 的批量并发/fan-out/recover 兜底链路。
  - **现状评估**：`ResponseStatus` 是 `string`（`size:50;index`，`mysql_http_agent_model.go:116`），存的是 `http.Response.Status` 完整文本（如 `"200 OK"` / `"500 Internal Server Error"`），**不是裸 int 也不是 bool**。写库路径 `server_http_ai_proxy.go:246` → `logAIProxyTransaction` → `SaveAgentHttpTransaction(..., status string, ...)` → `mysql_http_agent_sub_table.go:548`。
  - **核心设计决策（合并查询，非独立批量函数）**：
    - 不另起一轮 `BatchGetRouteLastResponseStatuses` —— 那样会引入两轮 round-trip 浪费 DB 连接，且两轮之间若插入新记录会产生「last_used 来自记录 A、last_response_status 来自记录 B」的不一致。
    - 扩展 `GetRouteLastUsedTime` 的等价单条函数 `getRouteLastRecord`（`mysql_http_agent_sub_table.go`）：单 SQL `SELECT created_at, response_status FROM <table> WHERE user_name=? AND model_name=? [AND protocol_type=?] ORDER BY id DESC LIMIT 1`，保证 last_used 与 last_response_status 严格来自同一行。
    - `response_status` 是 VARCHAR(50)，多 50B IO 远小于另起一轮的 16ms 网络 RTT 与一致性风险。
  - **修复 ①：数据层扩展**（`mysql_http_agent_sub_table.go`）：
    - 新增 `parseResponseStatusCode(s string) int` —— 解析首段三位数字，兼容 `"200"` / `"200 OK"` / `"500 Internal Server Error"`；0 表示无记录/未使用/不可解析；不识别 4 位以上数字（避免 `"2000"` 误识别）。
    - 新增 `isResponseSuccess(code int) bool` —— `code >= 200 && code < 300`。
    - 新增内部结果类型 `lastRecordRow{CreatedAt time.Time, ResponseStatus string}`（仅批量链路内部使用）。
    - 新增 `getRouteLastRecord(userName, modelName string, protocolType int, subTableNum int) (lastRecordRow, error)` —— 复用 `GetRouteLastUsedTime` 所有基础设施（`statsDB()` 25s context / `statsCache` 5min TTL / `isValidTableName` / `recover` 兜底）；cacheKey 前缀独立 `"GetRouteLastRecord"` 防止污染 `GetRouteLastUsedTime` 的 `time.Time` 单字段缓存。
    - `RouteBatchStatResult`（行 2197）新增四字段：`LastResponseStatus string` / `LastResponseStatusCode int` / `LastResponseIsSuccess bool` / `LastResponseStatusFailed bool`，全部 `omitempty`。
    - `BatchGetRouteLastUsedTimes` 内层 goroutine 改调 `getRouteLastRecord`，panic 兜底 / 日志前缀 `[AIRouteManage]` 保持不变；fan-out 阶段同时填充四字段。
  - **修复 ②：API 层两端对称**：
    - 管理端 `enrichRoute`（`server_api_manager_ai_route.go:337`）+ 用户端 `enrichRouteForUser`（`server_api_user_ai_route.go:310`）在 `if err == nil` 分支末尾注入四字段；`else`（模型查找失败）分支显式置 `LastResponseStatusFailed=true` 与四字段零值，禁止把故障渲染成「异常响应」或「未使用」。
    - 单条兜底路径（statsMap 为 nil）改用 `getRouteLastRecord` 替代 `GetRouteLastUsedTime`，与批量路径同源同语义。
  - **修复 ③：Web 模板两端对称**（管理员端 `server_web_manager_ai_route.go` + 用户端 `server_web_user_ai_route.go`）：
    - 表头新增 `<th>最后响应状态</th>`（在「最后使用」与「操作」之间），**不可排序**（用户选择，字典序无运维意义）。
    - CSS 新增 `.route-last-response-status` 4 个类：`success`（绿）/ `failed`（红）/ `empty`（灰斜体）。
    - 新增 `renderLastResponseStatusCell(route)` 函数，四态视觉可分。
    - 表格 `<td>` 新增一列；`colspan` 同步：管理端 `10→11`、用户端 `9→10`。
  - **修复 ④：版本同步 + 测试**：
    - `APP_VERSION` 升级至 `v2.0.69`。
    - 新增 `v2069_route_last_response_status_test.go` 含 17 个子测试：
      - **纯函数契约**（3 个）：`TestParseResponseStatusCode_OK` / `_Failed` / `_Edge` + `TestIsResponseSuccess_Boundary`
      - **NilDB / 缺失输入守护**（2 个）：`_LastResponseStatusNilDB` / `_MissingUserModel_MarksResponseFailed`
      - **DB 层源码契约**（3 个）：`_UsesStatsDB` / `_CacheKeyIsolation` / `_LastResponseJSONTags` + `_SelectsBothColumns`
      - **前端契约**（4 个）：`_RenderLastResponseStatusCell` / `_LastResponseStatusHeader` / `_ModelLookupFailureMarksRespFailed` / `_EmitsLastResponseStatus` / `_NoSortableLastResponseStatus`
      - **SQLite 集成**（6 个）：`_Success` / `_Failed_500` / `_NumericOnly` / `_NeverUsed` / `_PerProtocolIsolation` / `_SharedKeyFansOut` / `_AndTimeFromSameRow`
  - **强制规则**：
    > ✅ `/AIRouteManage`「最后响应状态」必须复用 v2.0.66 的批量查询链路（`BatchGetRouteLastUsedTimes` 内层调用 `getRouteLastRecord`），禁止另起一轮独立的批量查询 —— last_used 与 last_response_status 必须来自同一条最新记录，防止两轮 round-trip 引入不一致。
    > ✅ `ResponseStatus` 是 `string`（`size:50;index`），存的是 `http.Response.Status` 完整文本（如 `"200 OK"` / `"500 Internal Server Error"`），不是裸 int 也不是 bool。判断正常与否必须解析前缀三位数字，兼容 `"200"` / `"200 OK"` / `"500 Internal Server Error"` 三种格式，200<=code<300 即正常。
    > ✅ 「最后响应状态」必须四态视觉可分：正常（绿）/ 异常（红）/ 未使用（灰斜体）/ 查询失败（红色加粗）。禁止把数据库故障静默降级成「异常」或「未使用」—— 这正是 v2.0.66 「last_used 把故障渲染成未使用」缺陷的同源陷阱。
    > ✅ `enrichRoute` / `enrichRouteForUser` 新增 `last_response_status_*` 字段时，必须在 `if err == nil` 分支和 `else`（模型查找失败）分支都显式写入四字段，缺一就回到 v2.0.66「把故障渲染成正常状态」的陷阱。
    > ✅ `getRouteLastRecord` 必须走 `statsDB()` 25s context，禁止裸 `DB.Raw`。cache key 必须独立前缀 `"GetRouteLastRecord"`，禁止污染 `GetRouteLastUsedTime` 的 `time.Time` 单字段缓存。
    > ✅ 「最后响应状态」列**不可排序**（决策由用户确认）：不写 `data-sort-key`，不进入 `validColumns` / `keys` / `getSortedRoutes` 三件套。如未来要升级为可排序，必须同时补齐三件套并新增 `lastResponseSortValue` 等排序辅助。

---

## 历史版本（最近：v2.0.68）
- v2.0.68: **优化 `/ChatAnalysisTotal` 模型名称维度统计**（基于用户需求"统计分析页面，分析一下现在的统计的代码实现，数据能不能用本平台配置的模型名称维度完成统计数据的显示"）：
  - **目标**：让 stage 4 `model_distribution` 真正服务于"本平台用户配置的模型名称"维度分析；不引入 N+1、不查询 longtext、不破坏 v2.0.55/58/60 WS 契约。
  - **现状评估**：`Transaction.model_name` 字段（`mysql_http_agent_model.go:98`，注释"平台模型名称"）在 `SaveAgentHttpTransaction` 写库时已从 `UserModelInfo.ModelName` 拷贝（`mysql_http_agent_sub_table.go:534`），与 `TAgentHttpUserModelInfo.ModelName` 一致。`idx_user_model_created(user_name, model_name, created_at)` prefix 2 直接命中该字段，**无需 JOIN**。当前 7 stage 中仅 stage 4 含"本平台模型名"维度，WS 路径走 `snapshotModelDist`（`server_ws_chat_total_stream.go:367-396`），HTTP fallback 走 `GetModelNameUsageStatsByRange`（`mysql_http_agent_model_name_stats.go:46-173`）。
  - **修复 ①：stage 4 数据形状扩展**：
    - `ModelNameUsageStat`（`mysql_http_agent_model_name_stats.go:28`）新增 `DstEndpointCount int` + `TopDstEndpoints []DstEndpointUsage`（Top 3，含 `dst_endpoint_id` + `call_count`）；新增 `DstEndpointUsage` 类型
    - `modelNameUsageAccumulator` 新增 `dstEndpoints map[uint64]struct{}` + `dstEndpointCalls map[uint64]int64`
    - `GetModelNameUsageStatsByRange` 新增第三次 `GROUP BY model_name, dst_endpoint_id`（命中 `idx_dst_endpoint_id`），错误非致命（不中断主聚合）
  - **修复 ②：WS 单遍扫描累加器扩展**（`server_ws_chat_total_stream.go`）：
    - `streamScanRow` 增加 `user_name` + `dst_endpoint_id` 两列（**小字段白名单扩展，未引入 longtext**）；`streamScanColumns` 同步更新
    - `modelAgg` 新增 `userSet` / `dstEndpointSet` / `dstEndpointCalls`；`addRow` 累加阶段去重用户与源站
    - `snapshotModelDist` 计算 `UserCount` + `DstEndpointCount` + Top 3 排序（按 call_count desc → endpoint_id asc，与 HTTP 路径排序规则一致）
    - **性能评估**：单批 +290KB IO（user_name 50B + dst_endpoint_id 8B），远小于 longtext 体量；keyset 仍命中主键
  - **修复 ③：新 HTTP 接口** `lsmHandleModelDistributionFull`（`server_api_manager_chat_total.go`）：
    - `POST /ChatAnalysisTotalInterface {action:"model_distribution_full", days:N}` → 完整模型分布（无 50 行截断）
    - 复用 `GetModelNameUsageStatsByRange` + `statsDB()` 25s ctx 保护
    - 严禁另写一条不带 ctx 的 `GROUP BY` 慢查询
  - **修复 ④：range 报告字段对齐**（`mysql_http_agent_tokens.go`）：
    - `TokensModelStat` 新增 `CallCount int64` 作为 `Count` 的语义别名（JSON `count` + `call_count` 同值）
    - 2 个构造点（`GetTokensModelStats` 两处）同步设置 `CallCount = count`
    - 前端同一份渲染代码可同时复用 stage 4 + range 报告两端数据
  - **修复 ⑤：前端 stage 4 升级**（`server_web_manager_chat_total.go:488-525` + 新辅助函数）：
    - 图表维度切换：调用次数 / 总 Tokens / 用户数 / 源站数（4 选 1）
    - 搜索框实时过滤 model_name
    - "加载全部"按钮触发新接口，解除 50 行截断
    - 表格列扩展：用户数 + 源站数 + Top 3 源站 ID
    - 3 个新全局函数：`__lsmSwitchModelDistMetric` / `__lsmFilterModelDist` / `__lsmExpandModelDist`
  - **版本同步**：`APP_VERSION` 升级至 `v2.0.68`
  - **新增 `v2068_chat_total_model_distribution_test.go`**：10 个子测试覆盖 stage 4 新字段 + 跨路径一致性 + longtext 白名单 + JSON 字段别名
  - **强制规则**：
    > ✅ `/ChatAnalysisTotal` stage 4 `model_distribution` 必须输出本平台模型名（`Transaction.model_name`）的完整维度：`CallCount/TokensInput/TokensOutput/TokensTotal/CallShare/TokenShare/UserCount/DstEndpointCount/TopDstEndpoints`。WS 路径与 HTTP fallback 字段必须一致。
    > ✅ `streamScanColumns` 增加 `user_name` 与 `dst_endpoint_id` 是允许的（小字段白名单扩展）；严禁在 `streamScanColumns` 增加 4 个 longtext/text 字段（`request_body` / `request_src_protocol_body` / `response_body` / `response_src_protocol_body`）。前者的体量是后者的 1/1000。
    > ✅ `model_distribution_full` 全量拉取必须复用 `GetModelNameUsageStatsByRange`，ctx 25s 上限。严禁另写一条不带 ctx 的 `GROUP BY` 慢查询。
    > ✅ `TopDstEndpoints` 长度 ≤ 3。严禁把全量源站列表传到前端（避免敏感数据泄露 + IO 膨胀）。

---

## 历史版本（最近：v2.0.67）
- v2.0.67: **修复 `/CleanupReport` shard_00 恒报「scan batch failed: invalid connection」**（基于用户反馈"清理报告页面 2026-07-30 分表 #0 显示 ❌ 失败：scan/delete failed (deleted=0 so far): scan batch failed: invalid connection"）：
  - **根因（两层叠加）**：
    1. **扫描 SQL 在 133GB 大表上无覆盖索引**：`scanAndDeleteExpired` 每批 `SELECT id, created_at, tokens_* FROM t WHERE created_at<? ORDER BY id ASC LIMIT 1000`。生产分表只有 `created_at` 单列二级索引（gorm tag `index`）与 `(user_name, model_name, created_at)` 统计复合索引，二者都无法支撑「纯 created_at 范围过滤 + 按 id 排序 + 取 tokens 三列」：MySQL 走 created_at 索引范围扫描后要对全部过期行 filesort，且 tokens 三列必须逐行回主键聚簇索引取数 —— 单行含 4 个 longtext（实测均值 459KB），千行批次即 GB 级随机 IO。实测首批扫描就超过 30s `readTimeout`，驱动砍断 socket → `invalid connection` → 清理服务在 shard_00 上**从未成功扫过一批**（deleted=0）。
    2. **首次错误即放弃 + 24 小时才重试**：旧实现 scan 出错直接 `return err`，整表当次清理作废，下次尝试要等下一天 03:30；且报告文案没有「会自动重试」语义，运维看到「❌ 失败」以为必须人工介入。
  - **修复 ①：清理专用覆盖索引 `idx_cleanup_created_id (created_at, id)`**（`EnsureCleanupCreatedAtIndex`，启动时调用）：执行计划变为纯索引范围扫描 + 索引内排序（`ORDER BY id` 由索引天然有序满足），只有批内 1000 个 id 需要回表取 tokens。MySQL 路径走 `information_schema.STATISTICS` 存在性检查；SQLite 走 `sqlite_master`（**SQLite 索引名是库级命名空间**，同名索引全库只能建一次，与 MySQL 表级命名空间不同 —— 测试 `TestEnsureCleanupCreatedAtIndex_SQLiteNamespaceLimitation` 显式锁死该差异）。
  - **修复 ②：scan/delete 失败退避重试**：`cleanupMaxConsecutiveFailures=3` × `cleanupRetryBackoff=20s`，重试间隔用 `cleanupSleepRetryable` 可取消等待（同时监听表级 10min ctx 与 appCtx）；一次成功的扫+删即清零失败计数；重试耗尽才上抛 failed，错误文案带「已按 20s 间隔退避重试，下次运行自动继续」与已删条数。
  - **修复 ③：调度层自动补偿重跑**：`runDailyCleanup` 返回 failed 分表数；`transactionCleanupLoop` 检测到失败后 60s 自动重跑（`cleanupAutoRerunDelay`，上限 `cleanupAutoRerunMaxAttempts=5` 次），重跑幂等（报告按 `cleanup_date+sub_table_index` upsert）。瞬时连接故障无需等 24 小时自愈。
  - **修复 ④：前端「自动重试中」提示**：失败标签旁追加 `.cleanup-retry-hint`（⏳ 灰色小字 + hover「服务将自动重试，无需人工干预」），仅当 error_msg 含「下次运行自动继续」时显示；管理员端/用户端共用模板一处修改双端生效。
  - **关键取舍**：不在 delete 路径加事务/去重 —— `DELETE WHERE id IN (...)` 幂等，重试安全；tokens 在删除失败重试时可能重复累计一个批次，对「释放空间」决策无实质影响，换来实现简单。
  - **版本同步**：`APP_VERSION` 升级至 `v2.0.67`
  - **新增 `v2067_cleanup_resilience_test.go`**（13 个子测试全过）：索引创建/幂等/SQLite 命名空间差异/NilDB；重试文案与常量契约（总退避 ≤ 单表预算 1/3）；`cleanupSleepRetryable` 三态（正常/ctx 取消/已取消）；正常路径无回归（40 行秒级完成、tokens 精确）；`runDailyCleanup` 返回值驱动补偿调度；索引名与 `idx_user_model_created` 不冲突。
  - **强制规则**：
    > ✅ 清理类后台任务的扫描 SQL 必须有与「过滤列 + 排序列」匹配的覆盖索引（本例 `(created_at, id)`）。**禁止假设「有单列索引就够」** —— 大表上 filesort + 逐行回表取 longtext 同表列，首批就可能超 readTimeout 断连。新增索引必须显式 `CREATE INDEX` + `information_schema.STATISTICS` 存在性检查，禁止依赖 GORM tag 在分表 AutoMigrate 下稳定生效（v2.0.51 同款教训）。
    > ✅ 后台批处理任务遇连接级错误（invalid connection 等）必须有界重试（次数 × 退避双上限），禁止首次错误即放弃等下一天。重试等待必须可取消（监听 ctx），禁止裸 `time.Sleep`。
    > ✅ 定时任务的「失败后补偿重跑」应内建在调度循环里（60s 级），且重跑必须幂等（upsert 报告），禁止把自愈完全推给 24 小时后的下次日程。
    > ⚠ SQLite 索引名是**库级**命名空间，MySQL 是**表级** —— 写跨库测试时「同名索引建在多张表」的断言在 SQLite 下必然失败，存在性探测必须用 `sqlite_master` + `tbl_name` 精确到表。
    > ✅ 错误文案若被前端子串匹配（如「下次运行自动继续」→ ⏳ 提示），两侧必须用测试锁死同一字符串，单侧改动会让提示静默失效。

---

## 历史版本（最近：v2.0.66）
- v2.0.66: **修复 `/AIRouteManage`「最后使用」列恒显示「未使用」**（基于用户反馈"管理员端和用户端的智能路由管理页面，`最后使用` 列都显示 `未使用`，但 `时间跨度统计` 列明明能显示出记录数"）：
  - **根因（三层叠加，缺一不成灾）**：
    1. **`days=0` 让 SQL 无法命中复合索引**：`list` action 传 `Days: 0`（无限制），`BatchGetRouteStatsByRouteIDs` 拼出的 `MAX(created_at) … GROUP BY` 没有 `created_at` 下界，无法利用 `idx_user_model_created`。`EXPLAIN` 实测 **73560 行 + Using temporary; Using filesort**。而「时间跨度统计」列走的是**另一条路径**（独立 `batch_stats` XHR、`days=3`），能吃到索引范围扫描 —— 这正是「统计有数、最后使用没数」的直接原因：**两列查的是不同 SQL、不同时间窗口**。
    2. **无 context 保护**：该查询用裸 `DB.Raw`，超时只能等驱动 `readTimeout`(30s) 砍断 socket。日志实证 `[30022.420ms]` + `invalid connection` 共 **19 次**，连接被标记 invalid 后污染连接池。
    3. **失败被静默降级成「未使用」**：`rows, err := DB.Raw(...)` 出错后 `continue` 丢弃整个分表 → 收尾循环给缺失路由补零值 `RouteBatchStatResult{}` → `enrichRoute` 只看 `lastUsed.IsZero()` → 渲染「未使用」。错误还被 `statsMap, _ :=` 二次吞掉，**数据库故障被完整伪装成正常状态**。
  - **修复 ①：last_used 拆出独立快路径**（`mysql_http_agent_sub_table.go` 新增 `BatchGetRouteLastUsedTimes`）：
    - 按 `(user_name, model_name, protocol_type)` 去重后并发查询，单条走 `ORDER BY id DESC LIMIT 1` 命中 `idx_user_model_id_desc`，`EXPLAIN rows=1`，**实测 16ms vs 聚合版 179ms**。
    - 并发上限 `batchLastUsedConcurrency = 8`，20 条路由整体 < 50ms，且不与代理热路径抢 MySQL 连接（池上限 100）。
    - 每个 goroutine 带 `recover()` 兜底，单 key 异常不拖垮整页加载。
    - **关键取舍**：这部分回退了 v2.0.39「合并查询消灭 N+1」的思路 —— 当初的 N+1 是 20 条**快**查询，合并后变成 8 条会**超时**的慢查询，得不偿失。现在保留批量入口（调用方仍是一次调用），内部用有界并发跑快查询，两者好处都拿到。
  - **修复 ②：区分「查询失败」与「从未使用」**：`RouteBatchStatResult` 新增 `LastUsedFailed bool`（json: `last_used_failed`）；两端 `enrichRoute` / `enrichRouteForUser` 据此渲染「查询失败」而非「未使用」；前端新增 `renderLastUsedCell()` 三态渲染 + `.route-last-used.failed` 红色加粗样式 + hover 提示「不代表该路由未被使用，请查看服务端日志」。
  - **修复 ③：补齐 context 保护**：`GetRouteLastUsedTime` 与 `BatchGetRouteStatsByRouteIDs` 都改走 `statsDB()` 绑定 25s context，超时时驱动向 MySQL 发 KILL 并归还连接，不再等 30s 后污染连接池。`BatchGetRouteStatsByRouteIDs` 的分表失败改为收集 `failedShards` 并在末尾**上抛错误**，不再 `continue` 静默丢弃。
  - **修复 ④：协议扇出 bug**（独立缺陷，同批修复）：旧聚合把每行结果扇出到该 `(user, model)` 下**所有** routeID，`RouteBatchStatKey.ProtocolType` 从未进入映射 —— 同一模型的 Anthropic 与 OpenAI 路由**永远显示相同时间戳**。新函数按协议精确查询，两条路由各自独立。同时从聚合 SQL 中移除已无用的 `MAX(created_at)`。
  - **修复 ⑤：用户端消除 N+1 + 零值缓存陷阱**：`handleUserAIRouteList` 改为批量收集后一次 `BatchGetRouteLastUsedTimes`；`enrichRouteForUser` 签名新增 `statsMap` 参数（nil 时回退单条查询兼容其它调用方）。此前用户端逐条查询且失败时会命中 `GetRouteLastUsedTime` 的**零值缓存（5 分钟 TTL）**，一次超时会让该列在整个 TTL 窗口内持续显示错误状态。
  - **修复 ⑥：前端 SubAgent 审查发现的三个既有缺陷**（都在本列上，同批修掉）：
    - **用户端「最后使用」表头点了没反应**：`server_web_user_ai_route.go` 表头一直渲染着 `data-sort-key="last_used"` 可排序标记，但 `getSortedRoutes` 从无对应分支，`onSortColumn` 改完状态后 `return 0`，行序不变。补齐比较器。
    - **两端排序箭头永远空白**：`renderSortIndicators` 的 `keys` 与 `applyStoredSort` 的 `validColumns` 都漏了 `last_used` —— 前者导致 `sortIndicator-last_used` 占位符从未被写入（连中性 `⇅` 都没有），后者导致持久化的 `last_used` 排序在刷新后被判为非法并重置。
    - **`enrichRoute` 模型查找失败分支静默留空**：`GetUserModelByID` 失败时三个 `last_used_*` 键全不写，前端读到 `undefined` 后 fallback 成灰色「-」，与「未使用」视觉等同 —— **又是一处把故障伪装成正常状态的路径**，与本次主 bug 同源。补 `else` 分支显式标记 `last_used_failed=true`。
    - 排序新增三层分档（`lastUsedSortValue` + `lastUsedFailRank`）：**有时间戳 > 查询失败 > 未使用**，避免新的失败态淹没在一堆「未使用」里。
  - **版本同步**：`APP_VERSION` 升级至 `v2.0.66`
  - **新增 `v2066_route_last_used_test.go`**（16 个子测试全过，且经过**反向验证**：临时把 `luKey` 的 protocol 字段置 0 还原扇出 bug 后，`TestBatchGetRouteLastUsedTimes_PerProtocolIsolation` 报「Anthropic 与 OpenAI 路由的 last_used 相同 —— 协议扇出 bug 回归」，证明测试确实锁得住缺陷）：
    - `TestBatchGetRouteLastUsedTimes_PerProtocolIsolation` —— **核心回归**：同模型双协议路由时间戳必须不同
    - `TestBatchGetRouteLastUsedTimes_NeverUsedIsNotFailure` —— 表存在但无记录 = 「未使用」，不得标记失败
    - `TestBatchGetRouteLastUsedTimes_NilDB` / `_MissingUserModel` —— 故障必须标记 `LastUsedFailed`，禁止伪装
    - `TestBatchGetRouteStats_NoMaxCreatedAt` / `_UsesStatsDB` / `TestGetRouteLastUsedTime_UsesStatsDB`
    - `TestAIRouteTemplates_RenderLastUsedCell` / `TestAIRouteAPI_EmitsLastUsedFailed` —— 两端前后端契约
    - `TestAIRouteTemplates_LastUsedSortable` —— 可排序表头必须三件套齐全
    - `TestEnrichRoute_ModelLookupFailureMarksFailed` —— 模型查找失败分支必须显式标记
  - **强制规则**：
    > ✅ `/AIRouteManage`「最后使用」必须走 `BatchGetRouteLastUsedTimes`（`ORDER BY id DESC LIMIT 1` 命中 `idx_user_model_id_desc`）。禁止用 `MAX(created_at) + GROUP BY` 聚合取 last_used —— `days=0` 时它无法命中 `idx_user_model_created`，必然全表扫描并超时。
    > ✅ 「查询失败」与「从未使用」必须在数据结构（`LastUsedFailed`）与页面（红色「查询失败」vs 灰色「未使用」）上都可区分。**禁止把数据库故障静默降级成正常业务状态** —— 这会让故障在页面上完全隐形，本次 bug 潜伏至今的根本原因。
    > ✅ 所有统计类查询必须走 `statsDB()` 绑定 25s context。禁止裸 `DB.Raw` —— 超时只能等驱动 `readTimeout`(30s) 砍断 socket，连接被标记 `invalid` 后污染连接池。
    > ✅ 批量查询遇分表失败必须收集并上抛错误，禁止 `continue` 后给缺失项补零值 —— 调用方无法区分「真的 0」与「查询挂了」。
    > ⚠ 「合并查询消灭 N+1」不是无条件正确：合并前先看**合并后的执行计划**。20 条命中索引的快查询，优于 8 条触发全表扫描的慢聚合。
    > ⚠ 按 `(user, model)` 聚合的结果扇出到多条路由时，**必须**把 `protocol_type` 纳入映射键，否则同模型的双协议路由会共享同一份数据。
    > ✅ 新增可排序表头必须三件套齐全：`getSortedRoutes` 比较器分支 + `applyStoredSort` 的 `validColumns` 条目 + `renderSortIndicators` 的 `keys` 条目。缺任一项都是**静默失效**（点击无反应 / 箭头空白 / 刷新后丢失），不会报错。
    > ✅ `enrichRoute` 每条 `if err == nil` 分支都要有对应的 `else` 显式写入失败态字段。留空让前端 fallback 成「-」等同于把故障渲染成正常状态。

---

## 强制规则归档（所有版本）

> 以下是 v2.0.0-v2.0.66 所有版本积累的强制规则，按主题分类。**这些规则都是用线上故障换来的约束，必须遵守。**
> 各版本完整根因分析见 [`CHANGELOG.md`](CHANGELOG.md)。

### 数据库与查询

- ✅ 用 `Scan(&struct{...})` 接收**带 SQL 别名**的聚合结果时，结构体字段**必须**写显式 `gorm:"column:<别名>"` tag（v2.0.62 缺陷 ①）。
- ✅ `ALTER TABLE ... ENGINE=InnoDB` 前**必须**走 `checkDiskSpaceForRebuild` 预检（v2.0.62）。
- ✅ 磁盘不足时**只跳过重建，不跳过删除**（v2.0.62）。
- ✅ 删除批次要按**行的实际体积**定，留在 [100, 2000] 区间（v2.0.62）。
- ✅ `/ChatAnalysisTotalWS` 必须走 v2.0.60 单遍分页流式聚合，禁止还原到 v2.0.55–59 模式（v2.0.60）。
- ✅ `streamScanRow` / `streamScanColumns` 严禁含 longtext 字段（v2.0.60）。
- ✅ 累加器**快照**必须是 map 克隆（v2.0.60）。
- ✅ 全站统计的「逐行明细扫描」必须走 `scanShardPaged` keyset 分页（v2.0.58）。
- ✅ 分页必须 keyset（`id > lastID ORDER BY id ASC`），禁止 LIMIT/OFFSET（v2.0.58）。
- ✅ 每批新建 GORM 链（禁止跨批复用 `*gorm.DB`）（v2.0.58）。
- ✅ 全站 KPI 必须走 `GetAllStatsKPISummary` 轻量聚合（v2.0.57）。
- ✅ 全站统计必须用 All 变体函数，禁止用带 user/model WHERE 的函数传 `("", "")`（v2.0.56）。
- ✅ 统计查询必须走 `DB.WithContext(ctx)`（`statsDB()`）+ 有界超时（默认 25s）（v2.0.54）。
- ✅ DSN 必须带 `readTimeout`/`writeTimeout`；GORM Logger 必须为 `Warn` + SlowThreshold(2s)（v2.0.54）。
- ✅ 统计查询必须 Go 端聚合，禁止用 `DATE_FORMAT(...)+GROUP BY` 让 MySQL 走 temp table（v2.0.52）。
- ✅ 统计查询必须走 `(user_name, model_name, created_at)` 复合索引（v2.0.51）。
- ✅ `/ChatAnalysis` 列表 SQL 禁止访问 6 个 longtext/text 字段（v2.0.42）。
- ✅ **禁止**在 `TAgentHttpTransactionDataItem` 任何查询中使用 `Find(&records)` / `First(&record)` 不带 `Select(...)` 限定列（v2.0.39）。
- ✅ 列表/统计路径**必须**显式 `Select(...)` 或聚合（v2.0.39）。
- ✅ 禁止跳过 `Unscoped()` 硬删除（v2.0.47）。
- ✅ **必须**用 `LIMIT 5000` 分批删除 + 500ms sleep（v2.0.47）。

### 连接与资源管理

- ✅ 禁止用「裸 goroutine + time.After 放弃等待」做 DB 超时（v2.0.54）。
- ✅ 跑在独立 goroutine 且可能触碰 DB / 复杂聚合的逻辑**必须**加 `recover()` 兜底（v2.0.64）。
- ✅ 管理员端页面处理器**禁止**对空 `user_name`/`model_name` 直接 `http.Error(400)`（v2.0.64）。

### 前端与页面渲染

- ✅ `/ChatAnalysisTotal` 7 个 stage 渲染必须以 ECharts 图表为主（v2.0.59）。
- ✅ `daysSelect` 解析禁止 `parseInt(...) || 7`（v2.0.59）。
- ✅ `lsmOpenReportModal` 在模板中只能声明一次（v2.0.59）。
- ✅ 「有记录天数」按 `date.substring(0,10)` 天粒度去重（v2.0.59）。
- ✅ `/ChatAnalysisTotal` 必须走 `/ChatAnalysisTotalWS`；禁止恢复「📊 读取」按钮（v2.0.55）。
- ✅ `request_id` 必须 12 个小写 hex 字符（v2.0.55）。
- ✅ 7 个 stage 推送顺序固定（`wsChatStatsStageOrder` 常量）（v2.0.55）。
- ✅ 报告生成必须走 SSE 流式推送（`?stream=1`）（v2.0.46）。
- ✅ 颗粒度决策必须**基于 brush 选区实际跨度**（v2.0.46）。
- ✅ `/AIRouteManage`「时间跨度统计」列必须区分协议（Anthropic / OpenAI）（v2.0.44）。
- ✅ `/CleanupReport` 必须展示最早/最新交易保存时间和最新清理截止时间（v2.0.49）。
- ✅ 清理报告同日重复写入必须走 upsert（v2.0.49）。
- ✅ 测试夹具构造 created_at 用「本地今天 00:00 + 12h 基准 ± i 秒/小时」（v2.0.59）。

### 业务逻辑

- ✅ 删除源站必须**先**清理 `TAgentHttpAIRoute` 中的引用，**再**硬删（v2.0.50）。
- ✅ 剔除源站时三个列表（ID / 状态 / 算法）必须按同一位置一并剔除（v2.0.50）。
- ✅ 浏览记录分表是历史流水，**不**随源站删除清理（v2.0.50）。
- ✅ 清理类「成功但降级」的场景必须能与「真失败」在报告与页面上区分（v2.0.62）。
- ✅ `TransactionRetentionDays=0` 表示**禁用**自动清理（v2.0.47）。

### 禁止事项（⛔）

- ⛔ 禁止在 `/ChatAnalysisTotalWS` 路径还原「每完成一个维度 sleep 1s 才推下一块」的旧模式（v2.0.60）。
- ⛔ 禁止让 7 个维度各自重做一次全表扫描（v2.0.60）。
- ⛔ 禁止改动 WS/Hub/前端三层的 7 stage 顺序契约与各 stage 数据形状（v2.0.58）。
- ⛔ 禁止把 GROUP BY 计算塞到 KPI 这种「首屏可见」的关键路径（v2.0.57）。
- ⛔ 禁止在 WS handler 用「裸 goroutine + time.After 放弃等待」（v2.0.55）。
- ⛔ 禁止把 `DATE_FORMAT/TIMESTAMPDIFF/CASE WHEN` 计算塞回 SQL 端（v2.0.53）。
- ⛔ 禁止删除 `idx_user_model_created` 复合索引（v2.0.53）。
- ⛔ 禁止恢复 `TransactionRetentionDays` 的 `omitempty`（v2.0.49）。
- ⛔ 禁止为验证清理功能缩短生产配置或向生产交易表注入/删除测试数据（v2.0.49）。

---

## 版本演进概览

| 版本 | 主题 | 关键教训 |
|------|------|----------|
| v2.0.72 | 协议转换优化（Switchyard 借鉴） | 建模必须匹配真实线格式；测试必须用真实 JSON 输入 |
| v2.0.67 | 清理服务 invalid connection 自愈 | 大表扫描必须有覆盖索引；后台任务有界重试 |
| v2.0.66 | `/AIRouteManage`「最后使用」列修复 | 禁止把数据库故障静默降级成正常状态 |
| v2.0.64 | `/ChatAnalysisTotal` WS 1006 修复 | 独立 goroutine 必须 recover 兜底 |
| v2.0.62 | 清理服务从未真实删除修复 | GORM 列名隐式映射会静默返回零值 |
| v2.0.60 | 单遍分页流式聚合 | 7 次全表扫描 → 单遍扫描 |
| v2.0.58 | 统计查询 keyset 分页 | 深 OFFSET 慢 + 并发插入错行 |
| v2.0.55 | WS 流式分块推送 | 7 维度串行分块 + request_id 防重复 |
| v2.0.54 | 统计查询可取消超时 | 裸 goroutine + time.After 泄漏连接 |
| v2.0.52 | 统计 SQL Go 端聚合 | DATE_FORMAT + GROUP BY → temp table |
| v2.0.50 | 源站删除级联清理 | 删除前必须清理关联引用 |
| v2.0.47 | 浏览记录清理服务 | 流水数据必须硬删除 + 分批 |
| v2.0.42 | 大字段按字段懒加载 | 列表禁查 longtext |
| v2.0.39 | N+1 闭环 + 大字段白名单 | 禁止不带 Select 访问交易表 |
| v2.0.34 | Spider panic 与 session 泄漏 | recover + session 注册表清理 |
| v2.0.18 | 源站状态列表 + 爬虫登录墙 | DstEndPointIDStatusList |
| v2.0.16 | 识别功能模块化 + SessionID 入库 | recognizer_*.go 拆分 |
| v2.0.13 | 页面水合探测 | wait_for_hydration |
| v2.0.11 | SPA 兼容性增强 | per-action watchdog |
| v2.0.8 | Session 识别重构 | SessionRecognizer 接口层 |
| v2.0.5 | 经济型算法 | session 级别负载均衡 |
| v2.0.0 | MCP 切换 chromedp | 8 个 action 全部 chromedp 实现 |

> 完整版本历史（含根因分析、修复步骤、测试详情）见 [`CHANGELOG.md`](CHANGELOG.md)。

---

## 🕷️ MCP 爬虫服务（v2.0.0）

服务地址 `http://localhost:29002`。三个接口：

| 文档 | 用途 |
|------|------|
| [`MCP_SpiderWebData_def.md`](MCP_SpiderWebData_def.md) | `/SpiderWebData` 详细定义（v2.0.1 elements） |
| [`MCP_GetSpiderDataSource_def.md`](MCP_GetSpiderDataSource_def.md) | `/GetSpiderDataSource` |
| [`MCP_InputSpiderDailyInfo_def.md`](MCP_InputSpiderDailyInfo_def.md) | `/InputSpiderDailyInfo` |
| [`Mission_Spider_MCP_Proc.md`](Mission_Spider_MCP_Proc.md) | Agent 任务流程（首先阅读） |

代码组织（v2.0.0 重构后）：
- `mcp_interface_common.go` - 共享类型 + 会话管理 + 内容提取
- `mcp_interface_spiderwebdata.go` - `/SpiderWebData` 接口
- `mcp_interface_getspiderdatasource.go` - `/GetSpiderDataSource` 接口
- `mcp_interface_inputspiderdailyinfo.go` - `/InputSpiderDailyInfo` 接口
- `spider_cdp_*.go` - Chrome CDP 引擎（browser/engine/actions/session/selectors）
- `openclaw_client.go` - OpenClaw 本地 SSE 客户端（v2.0.4）
- `server_web_spider_crawl.go` - `/SpiderDataSourceCrawl` SSE 端点 + 爬取模态框（v2.0.4）

详细规范、HTTPS、协议转换器、Python 工具链、内存数据库、爬虫细节等统一在 [`AGENT.md`](AGENT.md)。

---

## 1. Claude Code 必须遵守的强制规则

### 编译 / 测试 / 重启

- **禁止**直接 `go build` / `nohup ./LsmTokensServer` / `./LsmTokensServer -d`
- 必须通过：

```bash
go test ./...
gofmt -w <修改的 .go 文件>
./rebuild_restart_app.sh --build-only   # 仅编译
./rebuild_restart_app.sh                # 滚动重启
```

- 修改 Go 代码后必须先 `gofmt -w`
- 测试失败必须先修复再编译重启
- `./rebuild_restart_app.sh` 会滚动重启并验证端口；不要绕过验证逻辑

### 运行保护

LsmTokensServer 是 Claude Code / Kilo Code / OpenCode / pi / OpenClaw 等 AI IDE 的网络代理依赖，直接重启会中断长对话或流式响应。

- 配置变更（用户/模型/源站/路由）通过 Web 管理页完成，正常**无需重启**
- 只有代码变更或必须重载二进制时才重启
- 重启前确认没有正在进行的长流式响应

### 前端修改必须调用前端 SubAgent

修改以下内容时，必须先调用前端 SubAgent：
- `server_web_*.go` 中的 HTML 模板字符串
- 内联 CSS / `<style>`
- 内联 JavaScript / `<script>`
- Web 页面路由或模板
- `server_web_common_*.go` 共享前端组件

检查重点：模板拼接顺序、DOM 闭合、CSS 作用域、相对路径、sticky 定位、MOE 一致性、响应式断点。详见 `AGENT.md` §4 / `Developer_SOP.md` §10。

**Web UI 关键约定（高频）**：
- `/AIRouteManage` 操作按钮：管理员端传 `user_name`+`model_name`，用户端只传 `model_name`；链接全部相对路径（`./ChatDialog` / `./ChatAnalysis` / `./ChatAnalysisTotal`）
- `/ChatAnalysis` 首屏默认 page=1（不恢复 localStorage 深分页）；筛选 + 分页用 localStorage 同步 URL
- `/ProtocolConvertAnalyzer` 管理/用户端布局一致：列表展示 ID 列；`转换方向` 由记录 `protocol_type` 自动确定且只读；4 项筛选用 localStorage 持久化（`lsm_protocol_converter_filters` / `lsm_protocol_converter_user_filters`），URL 参数优先于 localStorage；`结构转换成功率`/`字段转换率` 由后端 `CalculateConversionMetricsForSection` 实际转换后计算，禁止用 `calculateBasicMetrics` 兜底
- 启用按钮避免黑/灰背景；优先蓝/紫/青/绿/红等语义色 + hover 态
- `/ChatAnalysis` 浏览记录页已按 `server_web_manager_chat_page_{html,styles,body,scripts}.go` 拆分；`agentPageTemplate` 是组合入口；修改时禁止改变 `template.New(...).Parse(...)` 拼接方式或 `{{template "sharedDisplayJS"}}` 调用顺序
- 新增/调整数据库索引写在 GORM model tag / AutoMigrate 中；分表模型用 `index:,composite:<id>,priority:n` 让 GORM 按表生成索引名；排查性能问题先 MySQL `SHOW INDEX` / `EXPLAIN` 看真实执行计划

## 2. Claude Code 代理集成上下文（v2.0.0）

Claude Code 通过本代理服务访问底层 AI 模型：

1. **API Key 映射**：Claude Code API Key → 用户模型（模型级 API Key）
2. **模型名替换**：用户配置模型名 → 源站实际模型名
3. **智能路由**：按模型/协议/算法策略选目标源站；v1.3.0 起每个目标源站带 `DstEndPointAlgorithmTypeList`（`1=协议直连` / `2=协议转换器`）。协议转换器必须同步转换 body+header+API 路径：OpenAI `/chat/completions`/`/v1/chat/completions` ↔ Anthropic `/v1/messages`
   - **v2.0.8 Session 识别层**：`recognizer_session_id.go`（协议无关抽象层 + `SessionRecognizer` 接口 + `RecognizeSessionID` 通用入口 + OpenAI/Anthropic 协议实现 + Agent 工具级识别 + OpenClaw 实现）
   - **v2.0.5 经济型算法**：Session 级别负载均衡，从请求 body `metadata.user_id` 中解析 `session_id`，按 session 轮询分配到各源站；源站连续 3 次失败自动从路由移除；支持 Anthropic 和 OpenAI 协议
   - **v1.3.0 协议算法**：`1=协议直连` 源站协议=路由协议；`2=协议转换器` 源站协议=路由协议相反（OpenAI↔Anthropic 互转 body+header+path）
4. **请求日志**：完整记录到 MySQL 哈希分表（`/ChatAnalysis` 浏览）；`DstEndPointAlgorithmType` 记录本次实际协议处理算法
5. **请求头脱敏边界**：`RequestHeaders` / `RequestSrcProtocolHeaders` 写库保留原始值；从数据库读出后**后端**正则脱敏 `Authorization: Bearer ...` → `************************`；禁止把完整 Bearer Token 传到前端再脱敏
6. **AI Agent 识别**：从 User-Agent 识别请求来源（claude-cli / opencode / Kilo-Code / OpenClaw / pi），统计使用情况

**热路径必须零 DB**：API Key / 用户 / 路由 / 源站 / 协议算法都走 `AgentCache`；`AlgorithmTypeForEndPointID(endpointID)` 走 `CachedAIRoute`，禁止转发时为算法查询 MySQL。

## 3. 常用验证命令

```bash
# 代理核心
go test -run TestEndToEndProxyForwarding -v

# request_tools 解析
go test -run 'TestParseRequestToolsFromBody|TestExtractToolNamesFromMap_Dedup|TestTruncateRequestTools' -v

# AI Agent 识别
go test -run 'TestRecognizeAgentTool' -v

# Session 识别层
go test -run 'TestRecognizeSessionID|TestAnthropicSessionRecognizer|TestOpenAISessionRecognizer|TestRegisterSessionRecognizer' -v

# OpenClaw Agent 工具 Session 识别
go test -run 'TestOpenClawSessionRecognizer|TestRecognizeSessionID_OpenAI_OpenClaw' -v

# SessionID 入库
go test -run 'TestSaveAndQueryTransaction' -v

# 经济型算法
go test -run 'TestEconomicSelector|TestEconomicOnEndpointFailure|TestHashSessionToEndpoint' -v

# v2.0.17 KB 分支
go test -run 'TestEconomicSelectForKBRequest|TestIsAdvancedAgentToolName|TestIsKnowledgeBaseRequest|TestExtractRequestToolNamesForAlgorithm' -v

# 协议转换
go test -run 'TestConvertOpenAIToAnthropic|TestConvertAnthropicToOpenAI|TestConvertProtocolError|TestProtocolConverterLearning' -v

# MCP 接口
go test -run 'TestExtractWebElements|TestBuildPartialResultForFailure' -v

# 爬虫 CDP
go test -run 'TestParseSelector|TestBuildNextPageURL|TestResolveURL|TestUAPool|TestHeaderBundle' -v
LsmSpiderCDPIntegration=1 go test -tags integration -run TestSpiderEngineCrawlCDP -v

# SPA 兼容性
go test -run 'TestBuildAncestorProbeJS_|TestBuildControlledInputJS_' -v

# 水合探测
go test -run 'TestClassifyHydration|TestHydrationDiagnostics|TestHydrationSnapshot' -v

# 编译 / 重启
./rebuild_restart_app.sh --build-only
./rebuild_restart_app.sh
```

## 4. 快速排查

| 问题 | 优先检查 |
|------|----------|
| 浏览记录 `工具列表: 无` | `parseRequestToolsFromBody` / `SaveAgentHttpTransaction` / `request_tools` 列 |
| 浏览记录 `AI Agent: 无` | `RecognizeAgentTool` 调用 / `SaveAgentHttpTransaction` 参数 / `agent_tool_name` 列 |
| 浏览记录 `协议算法: -/未知` | `QueryAgentHttpTransactions` 是否选 `dst_endpoint_algorithm_type`；历史 `0/NULL` 按 `1=协议直连` 兼容 |
| 协议转换未触发 | 路由源站 `dst_endpoint_algorithm_type_list=2` + 源站协议与路由协议相反 + 缓存已同步 |
| **经济型算法源站自动移除** | 检查 `LsmTokensServer.log` 中 `[ECONOMIC]` 日志；连续 3 次失败自动移除 |
| **经济型算法 session 分配不均** | 检查请求 body 是否含 `metadata.user_id`（内含 `session_id`） |
| **KB 知识问答请求走错源站** | v2.0.17：未识别 session 且无 tool call 且 UA 非高阶 Agent 时，从 `DstEndPointIDs` 随机挑源站 |
| Agent 筛选下拉无数据 | `TAgentHttpAgentInfo` 表 / `GetDistinctAgentToolNames` / 内存缓存加载 |
| 代理认证失败 | `mysql_http_agent_cache.go` 中 API Key → Model 缓存 |
| 禁用源站仍转发 | `TAgentDstEndPoint.Status` / `forwardWithRetry` / 缓存同步 |
| Web 修改后用户端不刷新 | 增删改事务后的内存缓存同步 + `setNoCacheHeaders(w)` |
| 服务启动失败 | `LsmTokensServer.log` / MySQL 配置 / 端口占用 |
| **爬取按钮无响应** | OpenClaw 服务是否运行（`http://127.0.0.1:18789/health`） |
| **爬虫多轮 click 无效** | `session_id` 是否传入；session TTL=10 分钟 |
| **爬虫 Chrome 不可用** | `LsmTokensServer.log` 中 `[MCP]` / `[SPIDER]` 启动日志；`spiderCDPPort=9222` 端口占用 |
| **爬虫并发卡死** | `spiderMaxConcurrency` 默认 8，上限 64 |
| **click 挂起 10s+** | v2.0.11 已加 per-action watchdog（1500ms） |
| **click 返回 success 但无业务效果** | v2.0.11 返回 `data.click_effect_verification.effect_verified=false` |
| **fill_form 在受控 SPA 不生效** | v2.0.11 返回 `data.fill_form_result.fields[].diagnostics.framework_consumed=false` |
| **fill_form / click 全部无业务侧回调** | v2.0.13：传 `wait_for_hydration=true` 后看 `data.hydration_state` |

---

更详细的服务端口、HTTPS 配置、协议转换器规则、Python 工具链、内存数据库、爬虫细节等统一在 [`AGENT.md`](AGENT.md)。
