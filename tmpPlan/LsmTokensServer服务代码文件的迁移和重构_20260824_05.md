# LsmTokensServer 服务代码文件的迁移和重构 — 全面排查与优化方案（2026-08-24 第 05 轮）

> 排查方法：以 04 轮基线（后端 handler 1:1 迁移、ClientWeb SPA 全量实现、`go build`/`go test ./...` 全绿）
> 为起点，本轮对旧工程 `/usr/local/LsmHttpAgent/` **全部 256 个 Go 文件**逐文件比对到新工程
> `ServerGo/`（153 个 Go 文件），识别出 04 轮遗留的功能性缺陷与未迁移的存量回归测试。
> 结论：**后端业务功能已 1:1 迁移完毕，但存在 2 处由旧代码平移带入的真实 SQL 缺陷，
> 以及 57 个存量回归测试尚未迁移（阶段 G）。**

## 0. 本轮结论总览

| # | 缺口 | 严重度 | 本轮状态 |
|---|---|---|---|
| 1 | `estimateTransactionTaskFeatures` 统计 SQL 用 MariaDB 保留字 `rows` 作别名 → 语法错误 | 高 | ✅ 本轮修复 |
| 2 | `MigrateAgentToolColumns` 用 `SHOW COLUMNS` 6 列 Scan 进单个 bool → 列检查恒失败、重复 ALTER | 中 | ✅ 本轮修复 |
| 3 | `StartAIProxyService` 误用全局 `config.G` 而非入参 `cfg`（迁移引入） | 中 | ✅ 本轮修复 |
| 4 | 协议转换分析器「学习记录」转换视图（`BuildProtocolConvertAnalyzerRecordConversion`）缺失 | 中 | ✅ 本轮补齐 |
| 5 | 存量回归测试 57 个未迁移（阶段 G） | 中 | ✅ 本轮迁移 |
| 6 | `server_web_common_display_responses_test.go`（旧服务端 HTML 生成测试） | 低 | ⏳ 有意废弃（SPA 替代） |

## 1. 逐文件比对结果（256 → 153）

### 1.1 已迁移（结构性 1:1，`go build`/`go test` 全绿）

| 旧文件前缀 | 新位置 | 状态 |
|---|---|---|
| `agent_algorithm*.go` | `models/` | ✅ |
| `ai_api_connectivity.go` / `git_info.go` / `system_info_linux.go` / `source_code_stats.go` / `openclaw_client.go` | `system/` | ✅ |
| `mcp_interface_*.go` / `spider_*.go` / `server_mcp_spider_pipeline.go` | `spider/` | ✅ |
| `mysql_*.go` | `database/` + `models/` | ✅ |
| `protocol_*.go` | `protocol/`（`protocol_analyzer.go` 拆至 `protocol/` + `models/protocol_convert_analyzer.go`） | ✅ |
| `recognizer_*.go` | `recognizer/` | ✅ |
| `server_api_*.go` | `api/` | ✅ |
| `server_conf.go` / `server_logger.go` | `config/` / `logger/` | ✅ |
| `server_http_*.go` | `proxy/` | ✅ |
| `server_ws_*.go` | `websocket/` | ✅ |
| `server_web_*.go`（服务端内嵌 HTML） | `webserver/`（SPA 托管）+ `ClientWeb/`（React） | ✅ 有意废弃服务端 HTML |
| `main.go` | `ServerGo/main.go` | ✅ |
| `go-web-debug-tool/` | 子模块 `go-web-debug-tool/` | ✅ |

### 1.2 已迁移的测试（28 个）

`agent_algorithm_economic`、`agent_info_stats_days`、`agent_info_stats`、`model_info_stats_days`、
`model_info_stats`、`mysql_http_agent_model_route_remap`、`recognizer_agent_name`、`recognizer_session_id`、
`recognizer_session_id_tool`、`recognizer_tools`（→models）、`mcp_interface_common`、`mcp_interface_login_wall`、
`mcp_interface_spiderwebdata`、`spider_anti_bot_*`（5）、`spider_cdp_actions_*`（2）、`spider_cdp_hydration`、
`spider_cdp_integration`、`spider_cdp_safety`、`spider_rss_fallback`、`test_protocol_converter_error/request/response`、
`test_proxy_logic`（→`proxy/server_http_ai_proxy_logic_test.go`）、`test_header_redaction`（→`models/security_redact_test.go`）。

### 1.3 未迁移的测试（57 个，本轮迁移）

按目标包分组：

- **`system/`（1）**：`ai_api_connectivity_test.go`
- **`models/`（10）**：`route_last_used_time_test.go`、`v2066_route_last_used_test.go`、
  `v2071_route_last_success_failure_test.go`、`v2052_tokens_stats_gobucket_test.go`、
  `v2053_chat_total_gobucket_full_test.go`、`v2056_all_stats_data_test.go`、`v2058_stats_paged_scan_test.go`、
  `v2062_cleanup_real_delete_test.go`、`v2063_subtable_inspector_test.go`、`v2067_cleanup_resilience_test.go`
- **`protocol/`（6）**：`test_protocol_converter_learning_test.go`、`v2072_protocol_converter_fix_test.go`、
  `v2073_protocol_converter_ccswitch_enhancements_test.go`、`v2073_cc_switch_optimization_test.go`、
  `v2073_agent_detection_enhance_test.go`（部分属 recognizer/proxy，按函数归属定包）
- **`proxy/`（3）**：`test_api_proxy_test.go`、`v2022_dns_panic_test.go`、`v2031_agent_https_proxy_test.go`
- **`spider/`（15）**：`v2024_spider_daily_info_validation`、`v2025_fallback_strategy_hint`、
  `v2026_chrome_lifecycle`、`v2026_panic_recovery_hint`、`v2027_handler_panic_selfheal`、`v2027_healthz_metrics`、
  `v2030_force_restart`、`v2033_spider_session_leak`、`v2034_spider_panic_session`、`v2035_spider_target_close`、
  `v2043_spider_rss_timeout`、`v2047_mcp_logging`、`v2073_agent_detection_enhance`、`v2073_cc_switch_optimization`
- **`api/`（21）**：`test_api_crud`、`test_api_transactions`、`v2029_chat_analysis_batch_delete`、
  `v2032_airoute_batch`、`v2032_cert_download`、`v2038_dstendpoint_batch`、`v2039_airoute_list_batch`、
  `v2040_chat_analysis_tokens_filter`、`v2040_stats_span_hours`、`v2041_chat_analysis_hours_filter`、
  `v2042_chat_analysis_lazy_field`、`v2044_airoute_protocol_count`、`v2045_chat_analysis_total_insights`、
  `v2045_tokens_range_report`、`v2046_tokens_report_progress`、`v2047_transaction_cleanup`、
  `v2048_chat_analysis_total_optimizations`、`v2048_cleanup_report_visibility`、`v2050_endpoint_cascade_delete`、
  `v2051_chat_total_whitescreen_fix`、`v2054_stats_query_timeout`、`v2064_chat_total_admin_1006_fix`、
  `v2068_chat_total_model_distribution`
- **`websocket/`（3）**：`v2055_chat_total_ws_stream`、`v2059_chat_total_charts`、`v2060_chat_total_stream`

> 注意：上述分组为初判，迁移时按每个测试实际引用的函数归属动态落到对应包；
> 若测试引用的函数在新代码中不存在（被重命名/重构），则该测试揭示真实功能缺口，需一并补齐。

## 2. 功能性缺陷修复（本轮核心优化）

### 2.1 缺陷一：`AS rows` 保留字导致统计 SQL 语法错误

- **位置**：`ServerGo/models/subtable.go:353`（旧 `mysql_http_agent_sub_table.go:348` 平移带入）
- **现象**：`estimateTransactionTaskFeatures` 执行
  `SELECT COALESCE(MIN(id),0) AS min_id, ..., COUNT(*) AS rows, ...` 时 MariaDB 报
  `You have an error in your SQL syntax near 'rows, COALESCE(...'`，导致 Task 特征回填的
  统计/预估永远失败（`[TASK_BACKFILL][ESTIMATE] table=... failed`）。
- **根因**：`rows` 是 MySQL/MariaDB 保留字，不能裸用作列别名。
- **修复**：别名 `rows` → `row_count`，同步 `taskFeatureBackfillEstimate.Rows` 的
  gorm tag `column:rows` → `column:row_count`（字段名保留 `Rows`，仅改列映射）。

### 2.2 缺陷二：`SHOW COLUMNS` 6 列 Scan 进单 bool 导致列检查恒失败

- **位置**：`ServerGo/models/mysql_agent_info_manage.go:176/186`（旧 `mysql_agent_info_manage.go:172/187` 平移带入）
- **现象**：`MigrateAgentToolColumns` 用 `Raw("SHOW COLUMNS FROM ... LIKE 'agent_tool_name'").Scan(&columnExist)`
  判断列是否存在，但 `SHOW COLUMNS` 返回 6 列（Field/Type/Null/Key/Default/Extra），Scan 进单个
  `bool` 报 `expected 6 destination arguments in Scan, not 1` → `columnExist` 恒为 false →
  每次启动都重复 `ALTER TABLE ... ADD COLUMN`（`Duplicate column name` 噪音），且
  `agent_tool_name` 索引分支（`CREATE INDEX`）永不执行。
- **根因**：误用 Scan 目标类型；应改用 `information_schema.COLUMNS` 精确判断列存在。
- **修复**：改为
  `SELECT COUNT(*) FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = ? AND COLUMN_NAME = ?`，
  扫描进 `int64`，`>0` 视为存在。

### 2.3 缺陷三：`StartAIProxyService` 误用全局 `config.G` 而非入参 `cfg`（迁移引入）

- **位置**：`ServerGo/proxy/server_http_ai_proxy.go` 的 `StartAIProxyService`（旧 `server_http_ai_proxy.go:39`
  平移带入）。旧代码全程使用入参 `cfg.*`；新代码 11 处改为 `config.G.*`。
- **现象**：`StartAIProxyService(cfg)` 签名带 `cfg` 却全程读全局 `config.G`。生产启动时
  `config.G == cfg` 恰好掩盖问题；但单测/复用场景下 `config.G` 未初始化即触发 nil 指针，
  且函数语义与签名不符（cfg 参数被忽略）。
- **修复**：函数体内 11 处 `config.G` 改回入参 `cfg`（`buildAIProxyMux(cfg)`、`cfg.AgentListenPort`、
  `cfg.AgentHttpsListenPort`、`cfg.UserWebCertFile`/`UserWebKeyFile` 等）。

### 2.4 缺陷四：协议转换分析器「学习记录」转换视图缺失

- **位置**：旧 `server_web_manager_protocol_converter.go:317` 的
  `BuildProtocolConvertAnalyzerRecordConversion` 及其类型/helper
  （`ProtocolConvertAnalyzerSectionPair` / `ProtocolConvertAnalyzerRecordConversion` /
  `convertAnalyzerSectionPair` / `formatAnalyzerDisplayValue` / `firstNonEmpty`）未迁移。
- **现象**：协议转换分析器记录详情页的「转换前后对照」能力缺失，`test_protocol_converter_learning_test.go`
  无法编译。
- **修复**：新文件 `ServerGo/models/protocol_convert_analyzer_conversion.go` 原样迁移该函数与类型
  （仅适配包路径：`protocol.ConvertProtocolAnalyzerInput`、`RedactAuthorizationBearerHeaderText`）。

## 3. 实施步骤

1. 编写本方案文档（本文件）。
2. 修复 2.1 / 2.2 两个 SQL 缺陷。
3. 按 §1.3 分组迁移 57 个存量回归测试，每批迁移后 `go test <pkg>` 验证：
   1. `system/` → `protocol/` → `models/` → `proxy/`（基础包）
   2. `spider/`
   3. `api/` → `websocket/`
   - 迁移规则：`package main` → 目标包；顶层函数按归属加包前缀；
     测试内引用的全局状态（`cfg`、`DB`、`spiderSessions` 等）映射到新包对应符号；
     `initTestEnv` 复用各包既有 `testutil_test.go`。
4. `go build ./...`、`go vet ./...`、`go test ./...` 全绿。
5. `./rebuild_restart_app.sh` 完整重启，接口/页面冒烟（登录、管理端、用户端、分析器、爬虫 SSE、Chat WS）。
6. 中文 commit 提交。

## 4. 有意废弃项（延续前几轮）
- 旧服务端内嵌 HTML 生成代码及其测试（`server_web_common_display_responses_test.go`）—— SPA 替代。
- 旧服务端 `markdownToHTML` —— 前端渲染 Markdown。

## 5. 验证方式与结果

1. `go build ./...` 通过（exit 0）。
2. `go test -count=1 ./...` 全绿：`api` / `config` / `models` / `protocol` / `proxy` / `recognizer` /
   `spider` / `system` / `websocket` 共 9 个包全部 `ok`，0 FAIL。
3. `go vet ./...` 剩余 5 处告警均为旧工程既有生产代码问题平移（`self-assignment of ConsoleLines`、
   `repeats json tag "y"`×4），与 04 轮结论一致，未新增；本轮迁移引入的 v2026 测试
   `copies lock value` 告警已改为正确的加锁写法消除。
4. 存量回归测试迁移 57 个全部落地（部分按新包结构拆分跨包：`v2024` 拆 api/spider/models 三侧、
   `v2031` 拆 config/proxy、`v2072` 拆 protocol/proxy、`v2073_agent_detection` 拆 recognizer/models、
   `v2068` 归位 websocket），废弃 `server_web_common_display_responses_test.go`（服务端 HTML 生成测试）。
5. 功能性缺陷修复 4 处：`AS rows` 保留字、`SHOW COLUMNS` bool Scan、`config.G`→`cfg`、
   补齐 `BuildProtocolConvertAnalyzerRecordConversion`（`models/protocol_convert_analyzer_conversion.go`）。
6. `./rebuild_restart_app.sh` 完整重启后验证：
   - 新服务（PID 2984202）监听 9101/29000/29001/29002/29003 全部正常；
   - `/healthz` 返回 `{"status":"ok"}`；管理端 9101 返回 SPA 200（zh-CN）；
     用户端 29001 HTTPS 未登录 302 → /UserLogin；
   - 重启后 err log 中 `near 'rows'` / `Duplicate column name` /
     `expected 6 destination arguments` 三类旧 SQL 错误 0 出现（本轮两处 SQL 缺陷修复生效）；
   - 旧服务 LsmHttpAgent（PID 2912429，端口 19101/19000 等）持续运行，未受影响。

### commit 记录
- `阶段G：存量回归测试全量迁移 + SQL/config 缺陷修复（迁移排查05轮）`
