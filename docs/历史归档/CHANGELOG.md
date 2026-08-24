# CHANGELOG.md - LsmTokensServer 完整版本历史

> 本文件收录 v2.0.0-v2.0.68 的完整版本历史（根因分析、修复步骤、测试详情）。
> 日常开发以 [`CLAUDE.md`](CLAUDE.md) 入口（当前版本详情 + 强制规则归档）为准；需要追溯实现背景时再查本文件。

## v2.0.72 — OpenAI ↔ Anthropic 协议转换优化（2026-08-13）

**背景**：调研开源工程 Switchyard（NVIDIA NeMo，Rust LLM 流量代理）的翻译层（中性 IR + 双向 codec 架构），产出知识库文档 [`docs/Switchyard_OpenAI_Anthropic_Exchange.md`](docs/Switchyard_OpenAI_Anthropic_Exchange.md)；对照审计本工程协议转换模块发现 7 项 P0 功能性缺陷 + 10 项 P1 有损转换，产出优化方案 [`docs/LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md`](docs/LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md) 并实施。

**根因（最重要的 P0-1）**：`AnthropicContentBlock` 把 `tool_use`/`tool_result` 建模为嵌套字段（`{"type":"tool_use","tool_use":{...}}`），而真实 Anthropic 线格式是平铺的（`{"type":"tool_use","id":...,"name":...,"input":{...}}`）。后果链：a2o 非流式响应的 tool_calls 整体丢失但 `finish_reason=tool_calls` 照发 → 客户端 Agent 工具循环必断；o2a 响应产出非法嵌套结构；SSE 聚合路径因手工 map 解析恰好正确，两条路径行为不一致。既有测试全部用 Go struct 构造输入，恰好绕过 unmarshal 路径，缺陷完全隐形。

**修复点**（7 P0 + 11 P1，详见优化方案文档）：
1. `AnthropicContentBlock` 改平铺线格式建模（ID/Name/Input/ToolUseID/Content/IsError/Source），删除 `AnthropicToolUse`/`AnthropicToolResult`/`AnthropicSSEEvent` 死代码。
2. a2o 请求 tool_result 块拆分为独立 `role=tool` 消息（保留 tool_call_id、is_error 前缀 `[ERROR] `、数组 content 拍平）。
3. 多模态双向：o2a `image_url` 三形态 → Anthropic image source（url/base64 data URI 拆解）；a2o image 块 → `image_url` part。
4. `parseSSEEvents` 兼容 CRLF + 保留流尾残留事件。
5. `convertProxyResponse` SSE 分支聚合转换后按客户端协议重包装成合法 SSE（修复"JSON blob 配 event-stream 头"的协议自相矛盾）；整包缓冲保留，真流式状态机转换列入二期。
6. `wrapAnthropicResponseAsSSE`：tool_use 的 `content_block_start` 携带 id/name/`input:{}` + 补发 `input_json_delta`；空 content 补空 text 块。
7. P1 批次：`o2aDefaultMaxTokens=8192` 兜底；`Temperature`/`TopP` 指针化（显式 0 值透传）；连续 tool 消息合并；Arguments 解析失败包 `{"raw":...}`；确定性 tool_use id + 非法字符清洗；a2o 响应 `Created=time.Now().Unix()`；id 前缀 `msg_`↔`chatcmpl_` 改写；stop/finish 映射补齐 + 未识别透传；cache token 双向映射；system 数组形态拼接；`metadata.user_id`→`user`；未知 role 归并 user；OpenAI SSE 聚合按协议 `index` 分桶；`input_json_delta` 容错建块；`signature_delta` 保留。

**测试**：新增 `v2072_protocol_converter_fix_test.go`（23 个子测试全过，全部真实线格式 JSON 输入）；适配 `test_protocol_converter_request_test.go` / `test_protocol_converter_response_test.go`（Temperature 指针 + 平铺字段 + id 前缀改写断言）。全量 `go test ./...` 唯一失败 `TestChatStatsAggregator_AddRowAndSnapshots` 为既有时间窗口缺陷（干净树上同样失败，与本版无关）。

**强制规则追加**：见 [`CLAUDE.md`](CLAUDE.md) v2.0.72 区块（平铺建模 / 真实线格式测试输入 / tool_result 拆分与合并 / 协议自洽输出 / max_tokens 兜底 / 未识别值透传 / CRLF 兼容）。

---

## v2.0.68 — `/ChatAnalysisTotal` 模型分布维度增强（2026-07-30）

**目标**：让 `/ChatAnalysisTotal` stage 4 `model_distribution` 真正服务于「本平台用户配置的模型名称」维度分析；不引入 N+1、不查询 longtext、不破坏 v2.0.55/58/60 WS 契约。

**修复点**：
1. **数据形状扩展**（`mysql_http_agent_model_name_stats.go`）：`ModelNameUsageStat` 新增 `DstEndpointCount` / `TopDstEndpoints`（Top 3 源站，含 `dst_endpoint_id` + `call_count`）；`modelNameUsageAccumulator` 新增 `dstEndpoints` / `dstEndpointCalls` 两个 map。
2. **WS 单遍扫描扩展**（`server_ws_chat_total_stream.go`）：`streamScanRow` 增加 `user_name` + `dst_endpoint_id` 两列（仍为小字段，**未引入 longtext**）；`streamScanColumns` 同步扩展；`modelAgg` 新增 `userSet` / `dstEndpointSet` / `dstEndpointCalls`；`addRow` 在累加阶段去重用户与源站；`snapshotModelDist` 计算 `UserCount` + `DstEndpointCount` + Top 3 排序。
3. **HTTP fallback 路径增强**（`GetModelNameUsageStatsByRange`）：新增第三次 `GROUP BY model_name, dst_endpoint_id` 累加（命中 `idx_dst_endpoint_id` 索引）；非致命错误继续主聚合。
4. **新 HTTP 接口**（`lsmHandleModelDistributionFull`）：`action: "model_distribution_full"` 返回完整模型分布（无 50 行截断），复用 `GetModelNameUsageStatsByRange` + statsDB() 25s ctx 保护。
5. **range 报告字段对齐**（`TokensModelStat`）：新增 `CallCount` 字段作为 `Count` 的语义别名，与 stage 4 `ModelNameUsageStat.CallCount` 对齐；前端同一份渲染代码可同时复用两端数据。
6. **前端 stage 4 升级**（`server_web_manager_chat_total.go`）：图表维度切换（调用次数 / 总 Tokens / 用户数 / 源站数）；搜索框实时过滤；"加载全部"按钮触发新接口；表格列扩展（用户数 / 源站数 / Top 3 源站 ID）。

**强制规则追加**：
> ✅ `/ChatAnalysisTotal` stage 4 `model_distribution` 必须输出本平台模型名（`Transaction.model_name`）的完整维度：`CallCount/TokensInput/TokensOutput/TokensTotal/CallShare/TokenShare/UserCount/DstEndpointCount/TopDstEndpoints`。WS 路径与 HTTP fallback 字段必须一致。
>
> ✅ `streamScanColumns` 增加 `user_name` 与 `dst_endpoint_id` 是允许的（小字段白名单扩展）；严禁在 `streamScanColumns` 增加 4 个 longtext/text 字段（`request_body` / `request_src_protocol_body` / `response_body` / `response_src_protocol_body`）。
>
> ✅ `model_distribution_full` 全量拉取必须复用 `GetModelNameUsageStatsByRange`，ctx 25s 上限。严禁另写一条不带 ctx 的 `GROUP BY` 慢查询。
>
> ✅ `TopDstEndpoints` 长度 ≤ 3。严禁把全量源站列表传到前端（避免敏感数据泄露 + IO 膨胀）。

---

## v2.0.67 — 清理服务 invalid connection 自愈（2026-07-30）

**根因（两层叠加）**：
1. 清理扫描 SQL 在 133GB 大表上无覆盖索引：`scanAndDeleteExpired` 每批 `SELECT id, created_at, tokens_* FROM t WHERE created_at<? ORDER BY id ASC LIMIT 1000`。生产分表只有 `created_at` 单列二级索引与 `(user_name, model_name, created_at)` 统计复合索引，二者都无法支撑「纯 created_at 范围过滤 + 按 id 排序 + 取 tokens 三列」：MySQL 走 created_at 索引范围扫描后要对全部过期行 filesort，且 tokens 三列必须逐行回主键聚簇索引取数（单行 4 个 longtext 均值 459KB），千行批次即 GB 级随机 IO。实测首批扫描就超过 30s `readTimeout`，驱动砍断 socket → `invalid connection` → 清理服务在 shard_00 上**从未成功扫过一批**。
2. 首次错误即放弃 + 24 小时才重试：旧实现 scan 出错直接 `return err`，整表当次清理作废，下次尝试要等下一天 03:30。

**修复**：
- 修复 ①：清理专用覆盖索引 `idx_cleanup_created_id (created_at, id)`，启动时幂等创建（MySQL 走 `information_schema.STATISTICS` 存在性检查；SQLite 走 `sqlite_master`）。
- 修复 ②：scan/delete 失败退避重试（`cleanupMaxConsecutiveFailures=3` × `cleanupRetryBackoff=20s`），重试等待可取消。
- 修复 ③：调度层自动补偿重跑（`runDailyCleanup` 返回 failed 分表数，60s 后自动重跑，上限 5 次）。
- 修复 ④：前端「自动重试中」提示。

**强制规则**：清理类后台任务的扫描 SQL 必须有与「过滤列 + 排序列」匹配的覆盖索引；后台批处理任务遇连接级错误必须有界重试；定时任务的「失败后补偿重跑」应内建在调度循环里。

## 版本演进概览

| 版本 | 主题 | 关键教训 |
|------|------|----------|
| v2.0.68 | `/ChatAnalysisTotal` 模型分布维度增强 | 本平台模型名 + 用户数 + 源站维度；禁 longtext |
| v2.0.67 | 清理服务 invalid connection 自愈 | 大表扫描必须有覆盖索引；后台任务有界重试 |
| v2.0.66 | `/AIRouteManage`「最后使用」列修复 | 禁止把数据库故障静默降级成正常状态 |
| v2.0.64 | `/ChatAnalysisTotal` WS 1006 修复 | 独立 goroutine 必须 recover 兜底 |
| v2.0.62 | 清理服务从未真实删除修复 | GORM 列名隐式映射会静默返回零值 |
| v2.0.60 | 单遍分页流式聚合 | 7 次全表扫描 → 单遍扫描 + 增量聚合 |
| v2.0.58 | 统计查询 keyset 分页 | 深 OFFSET 慢 + 并发插入错行 |
| v2.0.55 | WS 流式分块推送 | 7 维度串行分块 + request_id 防重复 |
| v2.0.54 | 统计查询可取消超时 | 裸 goroutine + time.After 泄漏连接 |
| v2.0.52 | 统计 SQL Go 端聚合 | DATE_FORMAT + GROUP BY → temp table |
| v2.0.50 | 源站删除级联清理 | 删除前必须清理关联引用 |
| v2.0.47 | 浏览记录清理服务 | 流水数据必须硬删除 + 分批 |
| v2.0.42 | 大字段按字段懒加载 | 列表禁查 longtext |
| v2.0.39 | N+1 闭环 + 大字段白名单 | 禁止不带 Select 访问交易表 |
| v2.0.34 | Spider panic 与 session 泄漏 | recover + session 注册表清理 |
| v2.0.28 | Web 调试工具链迁移 Python→Go | go-web-debug-tool 子模块 |
| v2.0.18 | 源站状态列表 + 爬虫登录墙 | DstEndPointIDStatusList |
| v2.0.16 | 识别功能模块化 + SessionID 入库 | recognizer_*.go 拆分 |
| v2.0.13 | 页面水合探测 | wait_for_hydration |
| v2.0.11 | SPA 兼容性增强 | per-action watchdog |
| v2.0.8 | Session 识别重构 | SessionRecognizer 接口层 |
| v2.0.5 | 经济型算法 | session 级别负载均衡 |
| v2.0.0 | MCP 切换 chromedp | 8 个 action 全部 chromedp 实现 |

---

## 完整版本历史（v2.0.0-v2.0.65）

<details>
<summary>v2.0.65（当前生产版本 v2.0.66 前的过渡）</summary>

v2.0.65 未在 CLAUDE.md 中记录（直接由 v2.0.64 跳到 v2.0.66）。

</details>

<details>
<summary>v2.0.64 - 修复管理员端 `/ChatAnalysisTotal`「连接断开 (1006)」</summary>

基于用户反馈"管理员 Web 服务的统计分析页面没有正常显示，提示连接断开 (1006)，用户端正常"：

**根因**：管理员首页（`homeHandle`）和用户管理页（`userManageHandle`）都是列表级页面，模板数据里没有 `UserName` / `ModelName`，而它们嵌入的 `adminSubNavHTML` 二级导航渲染的「统计」链接需要这两个模板变量：`./ChatAnalysisTotal?user_name={{.UserName}}&model_name={{.ModelName}}`。模板变量为空 → 链接展开为 `./ChatAnalysisTotal?user_name=&model_name=` → 管理员端 `chatAnalysisTotalHandle` 原实现看到空参数直接 `http.Error(400)`。

**修复 ①**（`server_web_manager_chat_total.go` `chatAnalysisTotalHandle`）：参数缺失时对齐用户端语义 —— 调 `GetAllUsers(1,1)` 取第一个用户 + `GetUserModelsByUserID` 取该用户第一个模型 → 拼齐参数 302 重定向，不再 400。

**修复 ②**（`server_ws_chat_total.go` `readPump` query goroutine）：`runChatStatsQuery` 跑在独立 goroutine 却无 `recover()`，一旦 panic 会把**整个进程拉死**。加 `defer recover()` 兜底。

**强制规则**：
- 管理员端页面处理器**禁止**对空 `user_name`/`model_name` 直接 `http.Error(400)`
- 跑在独立 goroutine 且可能触碰 DB / 复杂聚合的逻辑**必须**加 `recover()` 兜底

</details>

<details>
<summary>v2.0.62 - 浏览记录保留期 60→45 天 + 修复清理服务「从未真实删除」</summary>

**缺陷 ①（严重）：GORM 列名错位导致删除被整体跳过**（`mysql_http_agent_cleanup.go` `cleanupOneSubTable` Step 1）：
- 统计用的匿名结构体字段名为 `Rows`（无 gorm tag），GORM `NamingStrategy` 把它映射到列 `rows`，而 SQL 别名是 `` `row_count` ``
- `stats.Rows` 恒为 0 → `if stats.Rows == 0 { return }` 永远命中 → Step 2 删除、Step 3 释放空间被完全跳过
- **修复**：四个字段一律加显式 `gorm:"column:..."` tag

**缺陷 ②（严重）：磁盘空间不足以支撑表重建**：
- `ALTER TABLE ... ENGINE=InnoDB` 是全表复制重建，需要约等于表大小的额外空间
- **修复**：新增 `checkDiskSpaceForRebuild(tableName)` 预检守卫

**强制规则**：
- 用 `Scan(&struct{...})` 接收带 SQL 别名的聚合结果时，结构体字段**必须**写显式 `gorm:"column:<别名>"` tag
- `ALTER TABLE ... ENGINE=InnoDB` 前**必须**走 `checkDiskSpaceForRebuild` 预检
- 磁盘不足时**只跳过重建，不跳过删除**
- 删除批次要按**行的实际体积**定（本表单行 body 均值 459KB，批次必须留在 [100, 2000] 区间）

</details>

<details>
<summary>v2.0.60 - /ChatAnalysisTotalWS 单遍分页扫描 + 全维度增量流式聚合</summary>

基于"页面一直显示「加载中」，数据量大时首屏卡死"的反馈 — 根因：v2.0.55–59 的 `runChatStatsQuery` 把 7 个维度拆成 **7 次独立全表扫描**。

**单遍 keyset 分页扫描**（`server_ws_chat_total_stream.go` 新增）：
- `streamScanRow` 结构体一次性含 17 个非 longtext 列
- `chatStatsAggregator` 7 个维度共享单遍增量累加器
- `streamChatStats(ctx, sdb, subTableNum, agg, onBatch)` 跨 8 张分表 keyset 分页（每批 `statsShardScanBatch`=5000 行）

**强制规则**：
- `/ChatAnalysisTotalWS` 必须走 v2.0.60 单遍分页流式聚合，禁止还原到 v2.0.55–59 模式
- `streamScanRow` / `streamScanColumns` 严禁含 longtext 字段
- 累加器**快照**必须是 map 克隆

</details>

<details>
<summary>v2.0.59 - 7 stage 全面 ECharts 图表化</summary>

**7 个 stage 全部图表化**（`server_web_manager_chat_total.go` `__lsmRenderStageHTML`）：
- `kpi`：4 张数字卡栅格 + 中心圆环图
- `time_stats`：柱状图（按小时/天桶自适应）
- `tokens_summary`：4 张数字卡栅格 + 输入/输出 Tokens 占比环图
- `model_distribution`：横向柱状图（Top 15）
- `protocol_stats`：3 个小型环图 + 多维分组柱状图
- `agent_stats`：横向柱状图（Top 15）

**强制规则**：
- 7 个 stage 渲染必须以 ECharts 图表为主，禁止把 stage 数据直接 `<pre>JSON.stringify` 输出
- `daysSelect` 解析禁止 `parseInt(...) || 7`

</details>

<details>
<summary>v2.0.58 - 统计重构为「分布式分页数据库查询」</summary>

**新增 keyset 分页扫描 helper `scanShardPaged`**（`mysql_http_agent_all_stats.go`）：
- `const statsShardScanBatch = 5000`
- 对单张分表按主键 `id` **keyset 分页**（`WHERE id > lastID [AND created_at >= cutoff] ORDER BY id ASC LIMIT 5000`）
- **为什么 keyset 而非 LIMIT/OFFSET**：并发插入不重不漏、O(log N) 主键 seek

**强制规则**：
- 全站统计的「逐行明细扫描」必须走 `scanShardPaged` keyset 分页
- 每批新建 GORM 链（禁止跨批复用 `*gorm.DB`）

</details>

<details>
<summary>v2.0.55 - WebSocket 流式分块推送</summary>

**7 个数据维度串行分块推送**（`server_ws_chat_total.go` `runChatStatsQuery`）：
1. `kpi` — 总体 KPI
2. `time_stats` — 时间分布
3. `tokens_summary` — Tokens 概览
4. `model_distribution` — 本平台模型分布
5. `trend_chart` — 时序折线
6. `protocol_stats` — 协议分析
7. `agent_stats` — Agent 工具统计

**强制规则**：
- `/ChatAnalysisTotal` 必须走 `/ChatAnalysisTotalWS`；禁止恢复「📊 读取」按钮
- `request_id` 必须 12 个小写 hex 字符
- 7 个 stage 推送顺序固定（`wsChatStatsStageOrder` 常量）

</details>

<details>
<summary>v2.0.54 - 统计查询 DB 层可取消超时</summary>

**根因①：`lsmRunInsightsSummary` 超时 goroutine 泄漏 MySQL 连接**
**根因②：GORM Logger 处于 `Info` 级**（逐条打印全部 SQL）

**修复**：
- DSN 追加 `&timeout=10s&readTimeout=30s&writeTimeout=30s`
- 新增 `statsDB()`：返回绑定 25s context 的会话，超时时驱动向 MySQL 发 KILL 中断查询并归还连接

**强制规则**：
- 统计查询必须走 `DB.WithContext(ctx)`（`statsDB()`）+ 有界超时（默认 25s）
- 禁止用「裸 goroutine + time.After 放弃等待」做 DB 超时

</details>

<details>
<summary>v2.0.52 - 统计查询 Go 端聚合</summary>

- `GetTokensRangeStats` 重写：只 `SELECT 8 个小字段`，Go 端按天/周桶聚合
- `GetTimeRangeStats` 重写：只 `SELECT created_at` 单列

**强制规则**：
- 统计查询必须 Go 端聚合，禁止用 `DATE_FORMAT(...)+GROUP BY` 让 MySQL 走 temp table
- 统计 SQL 的 SELECT 列表禁止包含 longtext 字段

</details>

<details>
<summary>v2.0.51 - 统计页面白屏卡死修复</summary>

- `TAgentHttpTransactionDataItem` 新增 `index:idx_user_model_created,priority:1|2|3` 复合索引
- 前端 fetch 加 AbortController + 30s 超时

**强制规则**：
- 统计查询必须走 `(user_name, model_name, created_at)` 复合索引，禁止全表扫描

</details>

<details>
<summary>v2.0.50 - 源站删除级联清理</summary>

- `DeleteDstEndPoint` 删除源站本体之前先调 `cleanupRoutesForEndpointDeletion(id)`
- 多源站路由：剔除源站引用；单源站路由：级联硬删除

**强制规则**：
- 删除源站必须**先**清理 `TAgentHttpAIRoute` 中的引用，**再**硬删 `TAgentDstEndPoint` 记录
- 剔除源站时三个列表（ID / 状态 / 算法）必须按同一位置一并剔除

</details>

<details>
<summary>v2.0.49 - /CleanupReport 时间可观测性增强</summary>

- 新增 `GetTransactionTimeBoundaries(subTableNum)`：返回全局最早/最新交易保存时间
- 新增 `saveCleanupReport`：按 `(cleanup_date, sub_table_index)` 冲突键 upsert

**强制规则**：
- `/CleanupReport` 必须展示最早/最新交易保存时间和最新清理截止时间
- 清理报告同日重复写入必须走 upsert

</details>

<details>
<summary>v2.0.47 - 浏览记录过期数据自动清理服务</summary>

- 新增 `TAgentHttpTransactionCleanupReport` 清理报告表
- `StartTransactionCleanupService(cfg)` 启动 goroutine，每天凌晨 03:30 触发
- `cleanupOneSubTable`：①SELECT COUNT+SUM → ②分批 DELETE → ③ALTER TABLE 重建 → ④写报告

**强制规则**：
- **禁止**跳过 `Unscoped()` 硬删除
- **必须**通过 `ALTER TABLE table_name ENGINE=InnoDB` 重建释放磁盘空间
- **必须**用 `LIMIT 5000` 分批删除 + 500ms sleep

</details>

<details>
<summary>v2.0.46 - Tokens 统计报告 SSE 实时进度推送</summary>

- 同一 `POST /ChatAnalysisTotalRangeInterface` 接口根据 `?stream=1` 查询参数切换 JSON/SSE
- 模态框改为两态：进度态 ↔ 结果态

**强制规则**：
- 报告生成必须走 SSE 流式推送（`?stream=1`），禁止恢复"1 次同步 fetch + 前端假 spinner"模式
- 颗粒度决策必须**基于 brush 选区实际跨度**

</details>

<details>
<summary>v2.0.44 - 「时间跨度统计」列新增协议区分显示</summary>

- `RouteBatchStatResult` 新增 `AnthropicCount` / `OpenAICount` / `OtherCount` / `CountByProtocol`
- SQL 升级为 `GROUP BY user_name, model_name, COALESCE(protocol_type, 0)`

**强制规则**：
- **禁止**把「时间跨度统计」列还原成单数字显示
- `/AIRouteManage`「时间跨度统计」列必须区分协议（Anthropic / OpenAI）

</details>

<details>
<summary>v2.0.42 - 大字段按字段单列懒加载</summary>

- `selectTransactionColumns()` 明确禁止 6 个 longtext/text 字段
- 新增 `GetAgentHttpTransactionFieldByID`，按 `id + user_name + model_name` 且每次只 `Select` 一个字段

**强制规则**：
- `/ChatAnalysis` 列表 SQL 禁止访问上述六字段；展开区块必须按 ID、按字段单列查询

</details>

<details>
<summary>v2.0.40 - 「时间跨度统计」列新增小时级筛选</summary>

- 时间跨度统一编码为单个 `int`（span）：`span == 0` 无限制；`span > 0` 最近 N 天；`span < 0` 最近 (-span) 小时
- 新增 helper `resolveStatsSpanCutoff(span) (time.Time, bool)`

</details>

<details>
<summary>v2.0.39 - N+1 闭环 + 交易表大字段强制白名单</summary>

**前端缺失函数补齐**：新增 `loadRecordCountsBatch()` 走 `action=batch_stats`
**后端消除 enrichRoute N+1**：`list` action 一次性 `BatchGetRouteStatsByRouteIDs`
**交易表大字段强制白名单**：新增 `selectTransactionColumns()` 统一白名单常量

**强制规则**：
- **禁止**在 `TAgentHttpTransactionDataItem` 任何查询中使用 `Find(&records)` / `First(&record)` 不带 `Select(...)` 限定列
- 列表/统计路径**必须**显式 `Select(...)` 或聚合

</details>

<details>
<summary>v2.0.34 - Spider goroutine panic 与 session 泄漏修复</summary>

- 新增 `safeSubmatchSlice(s, m, pair)` 辅助函数
- 新增 `releaseSpiderSession(s)`：在 `spiderSessionsMu.Lock()` 下 `detachCDPContext` + `delete(map, id)`
- `/healthz` 新增 `panic_count` + `last_panic_at`

</details>

<details>
<summary>v2.0.33 - MCP 爬虫服务并发/资源泄漏防护补强</summary>

- handler 超时 cleanup 同步化
- session 数量上限 + LRU 淘汰（`maxSpiderSessions` 默认 64）

</details>

<details>
<summary>v2.0.32 - 智能路由管理批量操作</summary>

- 前端批量 UI：全选 checkbox + 行级 checkbox + 批量操作栏
- 后端 `batch_update` / `batch_delete` 两个 action
- DB 层按协议分流：`BatchUpdateAIRoute` 按路由 `protocol_type` 与每个 endpoint 的 `protocol_type` 自动校正 `algorithm_type`

</details>

<details>
<summary>v2.0.31 - AI 代理 HTTPS 监听服务</summary>

- 新增 `agentHttpsListenPort` 配置字段，默认 29003
- 提取 `buildAIProxyMux(cfg)` 复用同一 mux/handler

</details>

<details>
<summary>v2.0.30 - MCP /SpiderWebData 级联污染自愈</summary>

- `dispatchCDPAction` 放行 `ActionTypeRestartBrowser`
- 新增 `restartChromeCommon(forced bool)` 单一真值源
- MCP handler 入口并发限流（`mcpHandlerSem`）
- SPA 路由替代路径 hints + infoq.cn 已知站点专项

</details>

<details>
<summary>v2.0.29 - 管理员端浏览记录批量删除</summary>

- 新增 `DeleteAgentHttpTransactions(userName, modelName, subTableNum, ids)`：单 SQL `WHERE id IN ?` + `Unscoped()` 批量硬删除
- 新增 `chatAnalysisBatchDeleteInterfaceHandle`

</details>

<details>
<summary>v2.0.28 - Web 调试工具链迁移 Python→Go</summary>

- `git submodule` 从 `python-web-debug-tool` 切换到 `go-web-debug-tool`
- 新增 `DEBUG_TOOL.md` 文档

</details>

<details>
<summary>v2.0.27 - MCP handler PANIC 自循环修复</summary>

- handler panic recover 服务端自愈（`detachAllSpiderSessions() + engine.RestartChrome()`）
- 新增 `trackingResponseWriter` 追踪 Write / WriteHeader / Flush 三种写入路径
- watchdog 并发安全加固（`sync.Once` 包住 detach 逻辑）

</details>

<details>
<summary>v2.0.25 - MCP 爬虫反爬 / 登录墙 hint 可观测性增强</summary>

- 新增 `buildFallbackStrategyHint(errType, rawURL)` 对八类 errType 注入 `fallback_strategy_hint`
- `cookies` action 新增 `op: "import"` 批量导入

</details>

<details>
<summary>v2.0.24 - MCP /InputSpiderDailyInfo 空记录防护</summary>

- handler 层 + DB 层双层防护（`IsEmptySpiderDailyInfo`）
- 删除宽容化：管理员跳过权限预检

</details>

<details>
<summary>v2.0.22 - MCP 爬虫浏览器沙箱 DNS 不可达兜底</summary>

- 新增 `errType=dns_unresolved`
- `errType=dns_unresolved` 加入 `shouldTryRSSFallback` 白名单
- goroutine panic 归到 `errType=session_invalid`

</details>

<details>
<summary>v2.0.20 - MCP 爬虫 RSS / Atom Feed 自动 fallback</summary>

- 新增 `spider_rss_fallback.go`：`LookupRSSFallbackSources(url)` + `FetchRSSTries`
- 新增 `fallback_strategy` 请求字段（auto / rss_first / none）

</details>

<details>
<summary>v2.0.18 - 路由表新增 DstEndPointIDStatusList</summary>

- `TAgentHttpAIRoute` 新增 `DstEndPointIDStatusList` 字段（1=启用, 0=禁用）
- 源站禁用/启用重构：`UpdateDstEndPointStatus` 不再物理删除路由中的源站 ID
- patch: MCP 爬虫稳定性补强
- patch2: MCP 爬虫登录墙识别 + 移动端降级 + session_id 主动轮换

</details>

<details>
<summary>v2.0.17 - 经济型算法 KB 分支</summary>

- 当请求未识别出 session_id、未识别出 Tool Call / Function Call、且 UA 不在高阶 Agent 白名单时，从 `DstEndPointIDList` 随机挑选可用源站
- KB 分支不消费 livePool、不写 session 粘性映射
- patch: MCP 爬虫 restart_browser session 残留修复 + hydration 探测 ES Module 加载可见性

</details>

<details>
<summary>v2.0.16 - 识别功能模块化重构 + SessionID 入库</summary>

- `agent_tool_recognizer.go` → `recognizer_agent_name.go`
- 4 个 session 识别文件 → 合并为 `recognizer_session_id.go`
- `TAgentHttpTransactionDataItem` 新增 `SessionID` 字段（size:128, indexed）
- 新建 `temp_backfill_session_id.go`：后台补全旧数据 session_id

</details>

<details>
<summary>v2.0.13 - 页面水合（hydration）状态探测</summary>

- 新增 `wait_for_hydration`（默认 false）+ `wait_for_hydration_ms`（默认 2000，上限 5000）
- 新增 `data.hydration_state`（`HydrationDiagnostics`）

</details>

<details>
<summary>v2.0.11 - SPA 框架兼容性增强</summary>

- `click` action 加 per-action watchdog 硬超时（1500ms）
- `click` / `click_at` 加动作效果校验（`data.click_effect_verification`）
- `fill_form` 增加 `ControlledInputDiagnostics` 诊断

</details>

<details>
<summary>v2.0.8 - Session 识别重构</summary>

- 提取 `SessionRecognizer` 接口层
- 按 OpenAI/Anthropic 协议分别实现
- `RecognizeSessionID(body, protocolType)` 供所有算法策略复用

</details>

<details>
<summary>v2.0.5 - 经济型算法 v2.0.5 实现</summary>

- Session 级别负载均衡（`session_id` 轮询分配）
- 源站连续 3 次失败自动移除
- OpenAI/Anthropic 双协议支持

</details>

<details>
<summary>v2.0.0 - MCP 爬虫服务切换为 Chrome DevTools Protocol (chromedp)</summary>

- 服务进程内嵌启动 headless Chrome + 30s 健康检查 + 异常自动重启
- 8 个交互 action 全部用 chromedp 真正实现
- HTTP 回退路径彻底移除
- Agent 必须显式调用 `/InputSpiderDailyInfo` 保存数据

</details>

---

> 完整测试用例列表、性能实测数据、生产验证结论等细节已在上方各版本中保留。
> 如需查看某个版本的完整原文（含测试名称列表），请查阅 git 历史中对应版本的 CLAUDE.md。
