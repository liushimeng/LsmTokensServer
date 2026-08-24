# LsmTokensServer 服务代码文件的迁移和重构 — 全面排查与优化方案（2026-08-24 第 06 轮）

> 排查方法：以 05 轮基线（后端 handler 1:1 迁移 + ClientWeb SPA 全量实现 + 57 个存量回归测试迁移 +
> 4 处 SQL/config 缺陷修复 + `go build`/`go test ./...` 全绿）为起点，本轮对旧工程
> `/usr/local/LsmHttpAgent/` **全部 256 个 Go 文件**逐文件回读比对到新工程 `ServerGo/`（220 个 Go 文件），
> 并对**配置文件端口、全部 Web 页面/路由、go vet 告警**做了最终一轮收敛排查。
> 结论：**后端业务功能、前端页面、REST/MCP 路由均已 1:1 迁移完毕，`go build`/`go test`/`go vet`
> 三项全绿（0 FAIL / 0 vet 告警）。本轮仅修复旧代码平移带入的 2 处真实缺陷（1 处功能缺陷 + 1 处无效语句）。**

## 0. 本轮结论总览

| # | 缺口 | 严重度 | 本轮状态 |
|---|---|---|---|
| 1 | `spider_cdp_actions_basic.go` 两处滚动探测结构体 `json:"y,max,vh"` 标签错误 → `hasMore` 恒 false | 高（功能缺陷） | ✅ 本轮修复 |
| 2 | `spider_cdp_hydration.go` `last.ConsoleLines = last.ConsoleLines` 自赋值无效语句 | 低 | ✅ 本轮修复 |
| 3 | 旧服务端内嵌 HTML 生成代码及其测试（`server_web_common_display_responses_test.go`） | 低 | ⏳ 有意废弃（SPA 替代，延续前几轮） |
| 4 | `go vet` 全部告警清零 | — | ✅ 本轮达成（0 告警） |

## 1. 全量逐文件比对结果（旧 256 → 新 220）

### 1.1 生产代码（非 `_test.go`，旧 130 → 新 128）

| 旧文件前缀 | 新位置 | 状态 |
|---|---|---|
| `agent_algorithm*.go` | `models/agent_algorithm*.go` | ✅ |
| `ai_api_connectivity.go` / `git_info.go` / `system_info_linux.go` / `source_code_stats.go` / `openclaw_client.go` | `system/` | ✅ |
| `mcp_interface_*.go` / `spider_*.go` / `server_mcp_spider_pipeline.go` | `spider/` | ✅ |
| `mysql_*.go` | `database/`（connect）+ `models/`（业务模型 + `subtable.go`） | ✅ |
| `protocol_*.go` | `protocol/`（`protocol_analyzer.go` 拆至 `protocol/` + `models/protocol_convert_analyzer.go`） | ✅ |
| `recognizer_*.go` | `recognizer/` | ✅ |
| `server_api_*.go` | `api/` | ✅ |
| `server_conf.go` / `server_logger.go` | `config/config.go` / `logger/logger.go` | ✅ |
| `server_http_*.go` | `proxy/` | ✅ |
| `server_ws_*.go` | `websocket/` | ✅ |
| `server_web_*.go`（服务端内嵌 HTML，约 60 个文件） | `webserver/`（SPA 静态托管）+ `ClientWeb/`（React SPA） | ✅ 有意废弃服务端 HTML |
| `server_mcp_spider_pipeline.go` | `spider/server_mcp_spider_pipeline.go` | ✅ |
| `main.go` | `ServerGo/main.go` | ✅ |

> 说明：旧 130 个生产文件中，约 60 个为 `server_web_*.go`（Go 拼接 HTML/JS/CSS 的服务端渲染），
> 在新前后端分离架构下由 `ClientWeb/`（React + Vite）+ `webserver/`（SPA 托管）替代，属**有意废弃**
> 而非遗漏。其余全部结构性 1:1 迁移。

### 1.2 测试代码（旧 126 → 新 92，跨包拆分适配）

- **已迁移**：`agent_*`、`recognizer_*`、`mcp_*`、`spider_*`、`test_api_*`、`test_protocol_*`、
  `test_proxy_*`、`test_header_redaction`、`v2022~v2073` 全量存量回归测试（含 05 轮迁移的 57 个）。
- **有意废弃**：`server_web_common_display_responses_test.go`（旧服务端 HTML 生成测试，SPA 替代）。
- 跨包拆分适配（延续 05 轮）：`v2024` 拆 api/spider/models 三侧、`v2031` 拆 config/proxy、
  `v2072` 拆 protocol/proxy、`v2073_agent_detection` 拆 recognizer/models、`v2068` 归位 websocket。

## 2. 配置文件与端口分析（需求第 4 条）

### 2.1 两工程配置比对

| 配置项 | 旧 LsmHttpAgent.conf（当前运行） | 新 LsmTokensServer.conf | 结论 |
|---|---|---|---|
| `managerWebListenPort` | 19101 | 9101 | 新工程回用原规范端口 |
| `userWebListenPort` | 19001 | 29001 | 同上 |
| `mcpWebListenPort` | 19002 | 29002 | 同上 |
| `agentListenPort` | 19000 | 29000 | 同上 |
| `agentHttpsListenPort` | 19003 | 29003 | 同上 |
| `spiderCDPPort` | 19222 | 9222 | 同上 |
| `agentProductListenAddr` | 8.130.85.252（公网） | 0.0.0.0 | 新工程放开监听地址 |
| `userWebUseHTTPS` | true | true | 一致 |
| `DBMysql.*` | lsmDB / superuser | lsmDB / superuser | 一致（共享同一 MySQL） |
| `openClaw*` / `transactionRetentionDays` | 一致 | 一致 | 一致 |
| `enableSpiderScheduler` | false | false | 一致（迁移期关闭） |

> 关键发现：旧服务当前运行端口为 **19000/19001/19002/19003/19101/19222**（在原规范端口上整体偏移，
> 以便与**新服务并行运行**）；新服务按 CLAUDE.md 声明回用规范端口 **9101/29000/29001/29002/29003/9222**。
> 新服务已启动（PID 2987227），监听 9101/29000/29001/29002/29003/9222 全部正常；旧服务（PID 2912429）
> 持续运行于偏移端口，互不冲突。

### 2.2 服务监听与数据库功能

- 启动顺序（`ServerGo/main.go`）：配置/日志 → MySQL（可选，失败不影响 AI 代理）→ AI 代理（29000/29003）→
  爬虫引擎 CDP + MCP（29002）→ 管理端 Web（9101）/ 用户端 Web（29001）。
- 数据库功能（`database/` + `models/`）：表初始化（用户/模型/源站/路由/模型信息/Agent 信息/子表/清理报告/爬虫）、
  分表管理（`subtable.go`）、缓存加载（`LoadAgentCacheFromDB`）、统计缓存（`InitStatsCache`）、
  事务清理服务（`StartTransactionCleanupService`，迁移期默认关闭）——均已迁移。

## 3. Web 页面遍历结果（需求第 5 条）

### 3.1 页面级路由（旧服务端 HTML → 新 SPA）

| 旧页面（server_web_*.go） | 新 React 页面（ClientWeb/src/pages/） | 路由挂载 |
|---|---|---|
| manager/user `home` | `Home.jsx` | SPA `#/Home` |
| manager/user `login` | `Login.jsx` | `#/Login` |
| `server_web_manager_user.go` | `UserManage.jsx` | `#/UserManage` |
| `server_web_manager_dst_endpoint.go` | `DstEndPointManage.jsx` | `#/DstEndPointManage` |
| `server_web_manager_ai_route.go` | `AIRouteManage.jsx` | `#/AIRouteManage` |
| `server_web_manager_model_info.go` | `ModelInfo.jsx` | `#/ModelInfo` |
| `server_web_manager_agent_info.go` | `AgentInfo.jsx` | `#/AgentInfo` |
| `server_web_manager_protocol_converter.go` | `ProtocolConvertAnalyzer.jsx` | `#/ProtocolConvertAnalyzer` |
| `server_web_spider_data_source.go` | `SpiderDataSource.jsx` | `#/SpiderDataSource` |
| `server_web_spider_daily_info.go` | `SpiderDailyInfo.jsx` | `#/SpiderDailyInfo` |
| `server_web_manager_cleanup_report.go` | `CleanupReport.jsx` | `#/CleanupReport` |
| `server_web_*_chat_analysis.go` | `ChatAnalysis.jsx` | `#/ChatAnalysis` |
| `server_web_*_chat_total.go` | `ChatAnalysisTotal.jsx` | `#/ChatAnalysisTotal` |
| `server_web_*_chat_session.go` | `ChatAnalysisSession.jsx` | `#/ChatAnalysisSession` |
| `server_web_*_chat_task.go` | `ChatAnalysisTask.jsx` | `#/ChatAnalysisTask` |
| `server_web_chat_dialog.go` | `ChatDialog.jsx` | `#/ChatDialog` |

### 3.2 公共弹窗/工具栏（旧 server_web_common_dialog_*.go → 新 ToolbarDialogs.jsx）

| 旧弹窗 | 新组件 | API 路由 |
|---|---|---|
| `server_web_common_dialog_userlog.go` | `UserLogDialog` | `/UserInfoLogInterface` |
| `server_web_common_wiki.go` | `WikiDialog` | `/WikiInterface` |
| `server_web_common_dialog_cert*.go` | `CertDialog` | `/CertDownloadInfoInterface` `/CertDownloadInterface` |
| `server_web_common_dialog_git.go` | `GitDialog` | `/GitInfoInterface` |
| `server_web_common_dialog_sysinfo.go` | `SysDialog` | `/SystemInfoInterface` |
| `server_web_common_dialog_readme.go` | `ReadmeDialog` | `/ReadmeInterface` |
| `server_web_common_dialog_handlers.go`（构建日志） | `BuildLogDialog` | `/BuildTimeLogInterface` |
| `server_web_common_dialog_sourcecode.go` | —（并入 README/源码视图） | `/SourceCodeInterface` |

> 结论：**全部 Web 页面与公共弹窗均有对应 SPA 实现，无遗漏。** 管理端导航 10 项、用户端导航 13 项
> （见 `ClientWeb/src/components/Layout.jsx`），与旧 `server_web_common_nav_admin.go` / `nav_user.go` 对齐。

### 3.3 REST/MCP 路由比对

- 旧工程全部 `*Interface` REST 路由 + `/SpiderWebData`、`/GetSpiderDataSource`、`/GetSpiderDailyInfo`、
  `/InputSpiderDailyInfo`、`/ChatAnalysisTotalWS` 等 MCP/WS 路由，均在新工程
  `api/routes.go`（管理端 7 组 + 用户端 5 组）+ `proxy/mount.go`（Agent 代理路径同源转发）+
  `spider/`（MCP 29002）+ `websocket/`（ChatTotal 流式）1:1 挂载完毕。

## 4. 本轮功能缺陷修复（需求第 9 条优化）

### 4.1 缺陷一：滚动探测结构体 JSON tag 错误 → `hasMore` 恒 false（真实功能缺陷）

- **位置**：`ServerGo/spider/spider_cdp_actions_basic.go:364/414`（旧 `spider_cdp_actions_basic.go` 平移带入，
  `actionScrollPage` 与 `actionScrollTo` 两处）。
- **现象**：
  ```go
  var st struct {
      Y, Max, VH int `json:"y,max,vh"`
  }
  ```
  Go 结构体 tag 对同一声明行的每个字段整体生效，`Y`/`Max`/`VH` 三个字段的 JSON 名称**均为 `y`**
  （`max`/`vh` 被当作无效 option 忽略）。因此 `json.Unmarshal` 时 `Max`/`VH` 恒为 0，
  `hasMore = st.Y+st.VH < st.Max` 恒为 `false`——**无限滚动/滚动探测的「是否有更多内容」判断自始失效**，
  MCP `/SpiderWebData` 滚动后永远上报 `hasMore=false`。
- **根因**：误将「逗号分隔多个 JSON 名」当作「按字段拆分 tag」，实为 Go tag 解析语义（首个逗号前为名，其余为 option）。
- **修复**：拆为逐字段显式 tag：
  ```go
  var st struct {
      Y   int `json:"y"`
      Max int `json:"max"`
      VH  int `json:"vh"`
  }
  ```
  （两处同修，`gofmt` 已格式化。）

### 4.2 缺陷二：hydration 探测自赋值无效语句

- **位置**：`ServerGo/spider/spider_cdp_hydration.go:261`（旧 `spider_cdp_hydration.go` 平移带入）。
- **现象**：单次探测失败分支里 `last.ConsoleLines = last.ConsoleLines // 保留上次` 为无操作自赋值，
  语义即「保留上次快照」——`last` 未被重赋值时本就保留旧值，该语句多余。
- **修复**：删除自赋值语句，保留注释语义（`continue` 跳过本轮，`last` 自然保留旧值）。

## 5. 实施步骤

1. 全量回读旧工程 256 个 Go 文件 + 两工程配置 + 新旧路由逐条比对（本轮已完成）。
2. 修复 4.1 / 4.2 两处缺陷（本轮已完成）。
3. `go build ./...`、`go vet ./...`、`go test -count=1 ./...` 全绿（本轮已达成，见 §7）。
4. 精简更新 `CLAUDE.md` / `AGENTS.md` / `README.md` / `docs/INDEX.md` 等，删除陈旧迁移期说明，保留核心规则。
5. `./rebuild_restart_app.sh` 重新编译运行，端口/页面冒烟。
6. 中文 commit 提交。

## 6. 有意废弃项（延续前几轮）

- 旧服务端内嵌 HTML 生成代码及其测试（`server_web_common_display_responses_test.go`）—— SPA 替代。
- 旧服务端 `markdownToHTML` —— 前端 `renderMarkdown`（`ToolbarDialogs.jsx`）渲染。

## 7. 验证结果

1. `go build ./...` 通过（exit 0）。
2. `go vet ./...` **0 告警**（exit 0）——本轮将 05 轮遗留的 5 处告警（2 处真实缺陷 + 4 处 json tag 重复）全部清零。
3. `go test -count=1 ./...` 全绿：`api` / `config` / `models` / `protocol` / `proxy` / `recognizer` /
   `spider` / `system` / `websocket` 共 9 个包全部 `ok`，0 FAIL。
4. 服务运行状态：新服务（PID 2987227）监听 9101/29000/29001/29002/29003/9222；旧服务（PID 2912429）
   监听偏移端口持续运行，互不干扰。
