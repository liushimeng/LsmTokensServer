# AGENT_INDEX.md - LsmTokensServer 源码索引

> 本文档为所有 AI Agent 工具（Claude Code、Kilo Code、OpenCode、pi、OpenClaw 等）提供源码文件快速定位。
> **结构说明**：
> 工程按模块分包组织（见 [docs/INDEX.md](../INDEX.md)）。
> 本文保留旧版平铺文件名说明，便于对照检索；新路径按 [迁移方案](项目迁移解决方案/00-总体迁移方案.md) 映射。
> 新版源码根目录：`ServerGo/`（`config/` `logger/` `database/` `models/` `proxy/` `protocol/` `api/` `spider/` `websocket/` `system/` `webserver/`）。
> 新版前端：`ClientWeb/`（React + Vite，阶段T 起双构建：`dist-manager` / `dist-user` 两套隔离产物，见 `../项目迁移解决方案/管理员与用户Web服务双构建隔离升级方案_20260825.md`）。

> 修改代码前，请先阅读与你所用工具对应的上下文文件（[`AGENT.md`](AGENT.md) / [`CLAUDE.md`](CLAUDE.md) / [`KILO.md`](KILO.md)）。

## 文档导航

| 文件 | 用途 |
|------|------|
| [`AGENT.md`](AGENT.md) | 通用核心规范 — 项目概述、强制工作流、编码规范、代理流程 |
| [`CLAUDE.md`](CLAUDE.md) | Claude Code 补充规范 |
| [`KILO.md`](KILO.md) | Kilo Code 补充规范 |
| [`Developer_SOP.md`](Developer_SOP.md) | 详细开发规范 — 数据库/缓存/前端/测试/MOE 规则 |
| [`SKILL.md`](SKILL.md) | 紧凑 AI Agent Skill（Claude Code / Kilo Code / OpenCode） |
| [`MEMORY.md`](MEMORY.md) | 项目记忆与上下文 |
| [`CHANGELOG.md`](CHANGELOG.md) | 完整版本历史（v2.0.0-v2.0.66 根因分析、修复步骤、测试详情） |

### 🧰 本地私有 Python 工具（AI Agent 加载）

> 工程根目录下的本地私有工具，**仅在本机使用，不入库**（`.gitignore` 已排除）。

| 工具目录 | 用途 |
|----------|------|
| `python-generate-image-tool/` | 火山引擎方舟大模型图片生成 SDK；模型 `doubao-seedream-5-0-pro-260628`（固定）；详细用法见 [`CLAUDE.md`](../../CLAUDE.md) §6 |

### 🕷️ MCP 爬虫服务接口文档（v2.0.0）

| 文件 | 用途 |
|------|------|
| [`MCP_SpiderWebData_def.md`](MCP_SpiderWebData_def.md) | `/SpiderWebData` 接口定义 — 网页爬取（chromedp 模式） |
| [`MCP_GetSpiderDataSource_def.md`](MCP_GetSpiderDataSource_def.md) | `/GetSpiderDataSource` 接口定义 |
| [`MCP_InputSpiderDailyInfo_def.md`](MCP_InputSpiderDailyInfo_def.md) | `/InputSpiderDailyInfo` 接口定义 |
| [`Mission_Spider_MCP_Proc.md`](Mission_Spider_MCP_Proc.md) | **Agent 爬虫任务指南** — 完整调用流程、示例代码、错误处理 |

## 服务端口速查

| 服务 | 端口 | 文档 |
|------|------|------|
| Agent Proxy | `29000` | — |
| Manager Web | `9101` | — |
| User Web | `29001` | HTTPS 可选（`userWebUseHTTPS=true`） |
| **Spider MCP** | **`29002`** | `Mission_Spider_MCP_Proc.md` |

## 关键规范速查

| 规范 | 要求 |
|------|------|
| 编译/重启 | **必须通过 `./rebuild_restart_app.sh`**，禁止直接 `go build` 或 `nohup` 启动 |
| 自动流程 | 修改代码后：格式化 → 测试 → 编译 → 重启 → 验证 |
| 测试 | `go test ./...` |
| 代码行数 | 任何 `.go` 文件不得超过 1500 行 |
| 前端路径 | 必须使用相对路径，禁止以 `/` 开头的绝对路径 |
| 前端修改 | **必须调用前端 SubAgent** |
| 代理热路径 | 零 MySQL DB 访问，全部走内存缓存 |
| 错误处理 | 不允许用 `_` 忽略关键错误 |
| 算法策略 | 指定型(1) / 稳定型(2) / 经济型(3) / 智能型(4) |
| 模型信息 | 新增源站自动同步 ModelName 到 TAgentModelInfo |
| 路由入口 | `/AIRouteManage` 操作列保持 `对话` / `浏览记录` / `统计` 入口，管理员传 `user_name+model_name`，用户只传 `model_name` |
| 按钮视觉 | 启用按钮避免黑/灰背景；灰色只用于 disabled、只读或状态标签 |
| `/ChatAnalysis` 模板 | `agentPageTemplate` 入口在 `server_web_manager_chat_page_html.go`，CSS/HTML/JS 拆到 `*_styles.go` / `*_body.go` / `*_scripts.go` |

---

## Go 源码文件目录（v2.0.0 重构后）

### 一、入口与基础设施

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`main.go`](main.go) | 程序入口，初始化配置、启动各服务、管理进程生命周期 | `main`, `APP_NAME` |
| [`server_conf.go`](server_conf.go) | 配置结构体与默认常量，JSON 解析及向后兼容 | `DBMysqlConfig`, `LsmTokensServerConfig` |
| [`server_logger.go`](server_logger.go) | 日志轮转管理，支持按大小自动备份 | `InitLogger` |

### 二、AI 代理核心（HTTP 转发引擎）

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`server_http_ai_proxy.go`](server_http_ai_proxy.go) | AI 统一代理引擎：API Key 认证、模型替换、SSE 透传、协议转换触发 | `StartAIProxyService`, `handleAIProxyRequest` |
| [`server_http_agent_proxy.go`](server_http_agent_proxy.go) | 代理基础设施：端口检查与服务启停 | `StopAIProxyService` |
| [`server_http_ai_proxy_security.go`](server_http_ai_proxy_security.go) | 代理层安全辅助（路径校验等） | — |
| [`protocol_converter.go`](protocol_converter.go) | 协议转换分析器：结构/字段转换率 + 统一入口 | `ConvertProtocolRequestBody`, `CalculateConversionMetricsForSection` |
| [`protocol_openai_to_anthropic.go`](protocol_openai_to_anthropic.go) | OpenAI → Anthropic 请求/响应转换 | `ConvertOpenAIToAnthropicRequest/Response` |
| [`protocol_anthropic_to_openai.go`](protocol_anthropic_to_openai.go) | Anthropic → OpenAI 请求/响应转换 | `ConvertAnthropicToOpenAIRequest/Response` |
| [`agent_algorithm.go`](agent_algorithm.go) | 算法策略类型定义与说明 | `GetAlgorithmName` |
| [`agent_algorithm_stable.go`](agent_algorithm_stable.go) | 稳定型算法：滚动切换 + 连续 3 次错误转移 | `StableAlgorithmSelector` |
| [`agent_algorithm_economic.go`](agent_algorithm_economic.go) | 经济型算法：Session 级别负载均衡 + livePool 消费语义 + 确定性哈希 | `EconomicAlgorithmSelector`, `SyncEconomicRouteEndpoints`, `hashSessionToEndpoint` |
| [`recognizer_session_id.go`](recognizer_session_id.go) | v2.0.16 合并：Session 识别协议无关抽象层 + OpenAI/Anthropic 协议实现 + Agent 工具级识别 + OpenClaw 实现 | `SessionRecognizer`, `RecognizeSessionID`, `openAISessionRecognizer`, `anthropicSessionRecognizer`, `AgentToolSessionRecognizer`, `openClawSessionRecognizer` |
| [`recognizer_agent_name.go`](recognizer_agent_name.go) | v2.0.16 迁移：Agent User-Agent 识别 | `RecognizeAgentTool` |
| [`recognizer_openai_function_call.go`](recognizer_openai_function_call.go) | v2.0.16 新增：OpenAI 协议 tools / tool_calls 解析 | `extractOpenAIToolNames`, `extractOpenAIToolCallsFromMessages` |
| [`recognizer_anthropic_tool_call.go`](recognizer_anthropic_tool_call.go) | v2.0.16 新增：Anthropic 协议 tools 解析 | `extractAnthropicToolNames` |
| [`temp_backfill_session_id.go`](temp_backfill_session_id.go) | v2.0.16 新增：SessionID 后台补全（临时文件） | `runSessionIDBackfill` |
| [`ai_api_connectivity.go`](ai_api_connectivity.go) | 源站 API 连通性测试 | `TestDstEndPointConnectivity` |

### 三、爬虫 MCP 服务（端口 29002，v2.0.0 重构）

#### 3.1 MCP 接口文件（v2.0.0 重构新增）

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`mcp_interface_common.go`](mcp_interface_common.go) | 共享类型（MCPAPIResponse / SpiderSession / InteractiveAction / PageState）+ 会话管理 + 内容提取 + HTTP 辅助 + `StartMCPWebServer` | `MCPAPIResponse`, `SpiderSession`, `getOrCreateSession`, `extractContentSimpleWithLimit`, `StartMCPWebServer` |
| [`mcp_interface_spiderwebdata.go`](mcp_interface_spiderwebdata.go) | `/SpiderWebData` 接口：请求/响应/Handler | `SpiderWebDataRequest`, `SpiderWebDataResponse`, `MCPSpiderWebDataHandler` |
| [`mcp_interface_getspiderdatasource.go`](mcp_interface_getspiderdatasource.go) | `/GetSpiderDataSource` 接口 | `SpiderDataSourceResponse`, `MCPGetSpiderDataSourceHandler` |
| [`mcp_interface_inputspiderdailyinfo.go`](mcp_interface_inputspiderdailyinfo.go) | `/InputSpiderDailyInfo` 接口 | `InputSpiderDailyInfoRequest`, `MCPInputSpiderDailyInfoHandler` |

#### 3.2 Chrome CDP 引擎（v2.0.0）

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`spider_cdp_browser.go`](spider_cdp_browser.go) | Chrome 进程生命周期：startChrome / killChrome / healthCheckLoop | `SpiderEngine.Start/Stop` |
| [`spider_cdp_engine.go`](spider_cdp_engine.go) | `SpiderEngine` 单例 + `crawlWebDataCDP` fetch 实现 | `GetSpiderEngine`, `crawlWebDataCDP` |
| [`spider_cdp_actions.go`](spider_cdp_actions.go) | 8 个交互 action 派发（chromedp 实现） | `dispatchCDPAction`, `actionNavigate/Click/Scroll/ScrollTo/FillForm/Extract/Screenshot/GetState` |
| [`spider_cdp_session.go`](spider_cdp_session.go) | per-session chromedp.Context 绑定 | `attachCDPContext`, `detachCDPContext` |
| [`spider_cdp_selectors.go`](spider_cdp_selectors.go) | 选择器解析（.class / #id / text: / xpath: / tag） | `parseSelector` |

#### 3.3 爬虫数据层 + 调度器 + Web UI

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`mysql_spider_model.go`](mysql_spider_model.go) | 数据模型 + AutoMigrate + 分表 | `TSpiderDataSource`, `TSpiderDailyInfo`, `InitSpiderTables`, `ListSpiderDataSources`, `SaveSpiderDailyInfo` |
| [`spider_scheduler.go`](spider_scheduler.go) | 调度器（5 分钟检查 / 最大 3 并发，仍 opt-in） | `GetSpiderScheduler`, `Start` |
| [`server_api_spider.go`](server_api_spider.go) | Web 管理 API（数据源 CRUD + 权限） | `SpiderDataSourceInterfaceHandle` |
| [`server_web_spider_data_source.go`](server_web_spider_data_source.go) | 数据源管理 Web UI（管理员 + 用户端） | `spiderDataSourceHandle` |
| [`server_web_spider_daily_info.go`](server_web_spider_daily_info.go) | 每日信息 Web UI | `spiderDailyInfoHandle` |

### 四、Web 服务 - 公共组件

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`server_web_common.go`](server_web_common.go) | 模板函数、编码、缓存控制 | `setNoCacheHeaders`, `toJSON` |
| [`server_web_common_header.go`](server_web_common_header.go) | 共享页面头部 + Header 工具栏 | `sharedPageHead`, `headerToolbarHTML` |
| [`server_web_common_nav_admin.go`](server_web_common_nav_admin.go) | 管理员导航栏 | `adminNavHTML` |
| [`server_web_common_nav_user.go`](server_web_common_nav_user.go) | 用户导航栏 | `userNavHTML` |
| [`server_web_common_display.go`](server_web_common_display.go) | 共享显示模块（JSON 树 / SSE 解析） | `sharedDisplayJSTemplate` |
| [`server_web_common_toolbar.go`](server_web_common_toolbar.go) | 共享弹窗入口 | `toolbarStyles`, `toolbarScripts` |
| [`server_web_common_toolbar_base.go`](server_web_common_toolbar_base.go) | 工具栏基础资源 | `toolbarBaseStyles`, `toolbarBaseScripts` |
| [`server_web_common_dialog_*.go`](server_web_common_dialog_*.go) | 弹窗实现（git/readme/sourcecode/sysinfo/userlog/wiki） | — |
| [`server_web_common_dialog_handlers.go`](server_web_common_dialog_handlers.go) | 通用弹窗接口处理 | `buildTimeLogInterfaceHandle` |
| [`server_web_common_wiki.go`](server_web_common_wiki.go) | Wiki 文件列表与内容服务 | `getWikiFiles` |
| [`server_web_security.go`](server_web_security.go) | 安全中间件：响应头、请求大小、速率限制 | `SecurityHeadersMiddleware` |

### 五、Web 服务 - 管理员端（端口 9101）

| 文件 | 核心功能 |
|------|----------|
| [`server_web_manager.go`](server_web_manager.go) | 管理员 Web 服务启动与路由注册 |
| [`server_web_manager_home.go`](server_web_manager_home.go) | 管理员首页 |
| [`server_web_manager_user.go`](server_web_manager_user.go) | 用户管理页面 |
| [`server_web_manager_ai_route.go`](server_web_manager_ai_route.go) | 智能路由管理页面 |
| [`server_web_manager_dst_endpoint.go`](server_web_manager_dst_endpoint.go) | 源站管理页面 |
| [`server_web_manager_model_info.go`](server_web_manager_model_info.go) | 模型信息管理页面 |
| [`server_web_manager_chat_analysis.go`](server_web_manager_chat_analysis.go) | 对话分析页面框架 |
| [`server_web_manager_chat_page.go`](server_web_manager_chat_page.go) | 对话分析页保留入口 |
| [`server_web_manager_chat_page_html.go`](server_web_manager_chat_page_html.go) | 对话分析页模板组合入口（`agentPageTemplate`） |
| [`server_web_manager_chat_page_styles.go`](server_web_manager_chat_page_styles.go) | 对话分析页 CSS（`agentPageStyles`） |
| [`server_web_manager_chat_page_body.go`](server_web_manager_chat_page_body.go) | 对话分析页 HTML 主体（`agentPageBodyHTML`） |
| [`server_web_manager_chat_page_scripts.go`](server_web_manager_chat_page_scripts.go) | 对话分析页 JS（`agentPageScripts`） |
| [`server_web_manager_chat_detail.go`](server_web_manager_chat_detail.go) | 对话详情页 |
| [`server_web_manager_chat_script.go`](server_web_manager_chat_script.go) | 共享 JS 工具（已废弃，保留兼容） |
| [`server_web_manager_chat_session.go`](server_web_manager_chat_session.go) | Session 分析页 |
| [`server_web_manager_chat_task.go`](server_web_manager_chat_task.go) | Task 分析页 |
| [`server_web_manager_chat_total.go`](server_web_manager_chat_total.go) | 统计分析页 |
| [`server_web_manager_protocol_converter.go`](server_web_manager_protocol_converter.go) | 协议转换分析器管理页 + 调试 API |
| [`server_web_manager_protocol_converter_html.go`](server_web_manager_protocol_converter_html.go) | 协议转换分析器页面 HTML |

### 六、Web 服务 - 用户端（端口 29001，支持 HTTPS）

| 文件 | 核心功能 |
|------|----------|
| [`server_web_user.go`](server_web_user.go) | 用户 Web 启动（HTTP/HTTPS） |
| [`server_web_user_login.go`](server_web_user_login.go) | 用户登录页面 |
| [`server_web_user_home.go`](server_web_user_home.go) | 用户首页 |
| [`server_web_user_ai_route.go`](server_web_user_ai_route.go) | 用户路由管理（支持编辑） |
| [`server_web_user_dst_endpoint.go`](server_web_user_dst_endpoint.go) | 用户源站管理（只读 + 测试） |
| [`server_web_user_model_info.go`](server_web_user_model_info.go) | 用户模型信息页 |
| [`server_web_user_chat_analysis.go`](server_web_user_chat_analysis.go) | 用户对话分析页 |
| [`server_web_user_chat_session.go`](server_web_user_chat_session.go) | 用户 Session 分析页 |
| [`server_web_user_chat_task.go`](server_web_user_chat_task.go) | 用户 Task 分析页 |
| [`server_web_user_chat_total.go`](server_web_user_chat_total.go) | 用户统计页 |
| [`server_web_user_protocol_converter.go`](server_web_user_protocol_converter.go) | 用户协议转换分析器 |

### 七、API 接口

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`server_api_manager_user.go`](server_api_manager_user.go) | 管理员用户/模型 CRUD | `userManageInterfaceHandle` |
| [`server_api_manager_ai_route.go`](server_api_manager_ai_route.go) | 管理员路由 CRUD | `aiRouteManageInterfaceHandle` |
| [`server_api_manager_chat_analysis.go`](server_api_manager_chat_analysis.go) | 管理员对话记录查询 | `chatAnalysisInterfaceHandle` |
| [`server_api_manager_chat_session.go`](server_api_manager_chat_session.go) | Session 分析 API | `chatAnalysisSessionInterfaceHandle` |
| [`server_api_manager_chat_task.go`](server_api_manager_chat_task.go) | Task 分析 API | `chatAnalysisTaskInterfaceHandle` |
| [`server_api_manager_chat_total.go`](server_api_manager_chat_total.go) | 统计 API | `chatAnalysisTotalInterfaceHandle` |
| [`server_api_manager_dst_endpoint.go`](server_api_manager_dst_endpoint.go) | 源站管理 API | `dstEndPointManageInterfaceHandle` |
| [`server_api_manager_model_info.go`](server_api_manager_model_info.go) | 模型信息 API | `modelInfoManageInterfaceHandle` |
| [`server_api_user_login.go`](server_api_user_login.go) | 用户登录 API（JWT + 验证码） | `userLoginInterfaceHandle` |
| [`server_api_user_home.go`](server_api_user_home.go) | 用户首页 API | `userInfoInterfaceHandle` |
| [`server_api_user_analysis.go`](server_api_user_analysis.go) | 用户统计 API | `userChatAnalysisTotalInterfaceHandle` |
| [`server_api_user_chat_analysis.go`](server_api_user_chat_analysis.go) | 用户浏览记录 API | `userChatAnalysisInterfaceHandle` |
| [`server_api_user_dst_endpoint.go`](server_api_user_dst_endpoint.go) | 用户源站 API | `userDstEndPointInterfaceHandle` |
| [`server_api_user_ai_route.go`](server_api_user_ai_route.go) | 用户路由 API | `userAIRouteInterfaceHandle` |
| [`server_api_user_model_info.go`](server_api_user_model_info.go) | 用户模型信息 API | `userModelInfoInterfaceHandle` |
| [`server_api_chat_dialog.go`](server_api_chat_dialog.go) | 对话配置 API | `chatDialogInterfaceHandle` |
| [`server_web_chat_dialog.go`](server_web_chat_dialog.go) | 对话页面（独立走代理） | `chatDialogHandle` |

### 八、数据库与数据模型

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`mysql_connect.go`](mysql_connect.go) | MySQL 连接初始化与连接池 | `InitMySQL`, `CloseMySQL` |
| [`mysql_http_agent_model.go`](mysql_http_agent_model.go) | 核心数据模型（用户/模型/路由/交易） | `TAgentHttpUserInfo`, `TAgentHttpTransactionDataItem` |
| [`mysql_http_agent_model_info.go`](mysql_http_agent_model_info.go) | 模型信息表 | `TAgentModelInfo` |
| [`mysql_http_agent_analysis.go`](mysql_http_agent_analysis.go) | Session/Task 分析 + `parseRequestToolsFromBody` | `parseRequestToolsFromBody` |
| [`mysql_http_agent_tokens.go`](mysql_http_agent_tokens.go) | Tokens 时间段统计 | `GetTokensRangeStats` |
| [`mysql_http_agent_sub_table.go`](mysql_http_agent_sub_table.go) | 哈希分表管理 | `QueryAgentHttpTransactions` |
| [`mysql_http_agent_cache.go`](mysql_http_agent_cache.go) | 内存缓存（零 DB 热路径） | `AgentCache`, `LoadAgentCacheFromDB` |
| [`mysql_http_stats_cache.go`](mysql_http_stats_cache.go) | 统计查询内存缓存 | — |
| [`mysql_http_user_manage.go`](mysql_http_user_manage.go) | 用户 CRUD + 缓存同步 | `AddUser`, `invalidateUserCache` |
| [`mysql_http_model_manage.go`](mysql_http_model_manage.go) | 模型 CRUD + API Key 生成 | `AddUserModel` |
| [`mysql_http_model_info_manage.go`](mysql_http_model_info_manage.go) | 模型信息 CRUD | `AddModelInfo` |
| [`mysql_http_dst_endpoint_manage.go`](mysql_http_dst_endpoint_manage.go) | 源站 CRUD | `AddDstEndPoint` |
| [`mysql_http_ai_route_manage.go`](mysql_http_ai_route_manage.go) | 智能路由 CRUD | `UpdateAIRoute` |
| [`mysql_agent_info_manage.go`](mysql_agent_info_manage.go) | AI Agent 工具统计表 | `TAgentHttpAgentInfo` |

### 九、系统信息与工具

| 文件 | 核心功能 | 主要导出 |
|------|----------|----------|
| [`git_info.go`](git_info.go) | Git 仓库信息 | `GitRepoInfo` |
| [`source_code_stats.go`](source_code_stats.go) | 源码文件统计 | `SourceCodeStats` |
| [`system_info_linux.go`](system_info_linux.go) | Linux 系统信息（CPU/内存/磁盘/网络） | `SystemInfo` |

### 十、测试文件（v2.0.8 完整清单）

| 文件 | 核心功能 | 关键测试 |
|------|----------|----------|
| [`test_api_proxy_test.go`](test_api_proxy_test.go) | 代理主链路转发 + initTestEnv 初始化 | `TestEndToEndProxyForwarding`, `TestProtocolConverter*`, `TestProxyAuthFailures` |
| [`test_api_crud_test.go`](test_api_crud_test.go) | 用户/模型/源站/路由 CRUD | `TestUserCRUD`, `TestDstEndPointCRUD`, `TestAIRouteCRUD` |
| [`test_api_transactions_test.go`](test_api_transactions_test.go) | 事务存储/查询/UTF-8 | `TestSaveAndQueryTransaction`, `TestSanitizeUTF8` |
| [`test_proxy_logic_test.go`](test_proxy_logic_test.go) | 代理层基础工具 | `TestExtractAPIKey`, `TestBuildProtocolAwareTargetURL` |
| [`test_request_tools_parsing_test.go`](test_request_tools_parsing_test.go) | `parseRequestToolsFromBody` 核心解析 | `TestParseRequestToolsFromBody`, `TestExtractToolNamesFromMap` |
| [`test_header_redaction_test.go`](test_header_redaction_test.go) | Bearer Token 脱敏 | `TestRedactAuthorizationBearerHeaderText` |
| [`test_agent_tool_recognition_test.go`](test_agent_tool_recognition_test.go) | AI Agent User-Agent 识别 | `TestRecognizeAgentTool`, `TestExtractUserAgentFromHeaders` |
| [`test_protocol_converter_request_test.go`](test_protocol_converter_request_test.go) | 协议转换请求全量单测（26 测试） | `TestConvertOpenAIToAnthropicRequest*`, `TestRoundTrip*` |
| [`test_protocol_converter_response_test.go`](test_protocol_converter_response_test.go) | 协议转换响应 | `TestConvertAnthropicToOpenAIResponse*`, `TestConvertOpenAIToAnthropicResponse*` |
| [`test_protocol_converter_error_test.go`](test_protocol_converter_error_test.go) | 协议转换错误响应处理 | `TestConvertProtocolErrorResponseBody*` |
| [`test_protocol_converter_learning_test.go`](test_protocol_converter_learning_test.go) | 协议转换学习器 | `TestProtocolConverterLearning*` |
| [`agent_algorithm_session_recognition_test.go`](agent_algorithm_session_recognition_test.go) | Session 识别层（v2.0.8） | `TestRecognizeSessionID*`, `TestAnthropicSessionRecognizer`, `TestOpenAISessionRecognizer`, `TestRegisterSessionRecognizer` |
| [`agent_tool_session_recognition_test.go`](agent_tool_session_recognition_test.go) | OpenClaw Agent 工具 Session 识别 | `TestOpenClawSessionRecognizer*`, `TestRecognizeSessionID_OpenAI_OpenClaw*` |
| [`agent_algorithm_economic_test.go`](agent_algorithm_economic_test.go) | 经济型算法负载均衡 | `TestEconomicSelectorSessionAffinity`, `TestEconomicSelectorLivePoolConsume` |
| [`mcp_interface_common_test.go`](mcp_interface_common_test.go) | MCP 元素提取（links/headings/paragraphs） | `TestExtractWebElements*` |
| [`mcp_interface_spiderwebdata_test.go`](mcp_interface_spiderwebdata_test.go) | SpiderWebData 反爬截断 | `TestBuildPartialResultForFailure*` |
| [`spider_anti_bot_test.go`](spider_anti_bot_test.go) | UA 池 / Header Bundle | `TestUAPoolNext*`, `TestHeaderBundle*` |
| [`spider_cdp_actions_test.go`](spider_cdp_actions_test.go) | CDP 解析层（选择器 / URL / text JS） | `TestParseSelector`, `TestBuildNextPageURL` |
| [`spider_cdp_integration_test.go`](spider_cdp_integration_test.go) | CDP 引擎 E2E（需 `LSMSpiderCDPIntegration=1`） | `TestSpiderEngineCrawlCDP` |

### 十一、前端源码 - ChatAnalysis 模块化页面（React + Vite）

> `ClientWeb/src/pages/ChatAnalysis.jsx` 为重导出入口；实际代码位于 `ClientWeb/src/pages/chat-analysis/`。

| 文件 | 核心功能 |
|------|----------|
| [`chat-analysis/index.jsx`](../../ClientWeb/src/pages/chat-analysis/index.jsx) | 主页面组件：组合工具栏 + DataTable + 内联展开行 |
| [`chat-analysis/ChatAnalysisToolbar.jsx`](../../ClientWeb/src/pages/chat-analysis/ChatAnalysisToolbar.jsx) | 筛选工具栏（用户/模型/Agent/时间跨度等） |
| [`chat-analysis/InlineDetailRow.jsx`](../../ClientWeb/src/pages/chat-analysis/InlineDetailRow.jsx) | 内联展开详情面板（替代旧 Modal 弹窗） |
| [`chat-analysis/DetailHeader.jsx`](../../ClientWeb/src/pages/chat-analysis/DetailHeader.jsx) | 详情头部（请求概要信息） |
| [`chat-analysis/DetailTabs.jsx`](../../ClientWeb/src/pages/chat-analysis/DetailTabs.jsx) | 字段 Tab 导航（请求体/响应头/转换分析等） |
| [`chat-analysis/DetailBody.jsx`](../../ClientWeb/src/pages/chat-analysis/DetailBody.jsx) | 详情主体渲染（JSON 树 / 原文） |
| [`chat-analysis/DetailFooter.jsx`](../../ClientWeb/src/pages/chat-analysis/DetailFooter.jsx) | 详情底部状态栏（耗时/Token/协议算法） |
| [`chat-analysis/useChatAnalysisFilters.js`](../../ClientWeb/src/pages/chat-analysis/useChatAnalysisFilters.js) | 筛选条件 localStorage 持久化 Hook |
| [`chat-analysis/useChatAnalysisData.js`](../../ClientWeb/src/pages/chat-analysis/useChatAnalysisData.js) | 数据查询 Hook（分页/筛选/加载状态） |
| [`chat-analysis/constants.js`](../../ClientWeb/src/pages/chat-analysis/constants.js) | 常量定义（localStorage key / 默认值等） |

---

## 代理核心流程

```
客户端请求 (Anthropic/OpenAI)
    ↓
提取 Authorization: Bearer {API Key}
    ↓
通过 API Key 查询模型（内存缓存，零 DB）→ 获取 UserModelInfo
    ↓
通过 UserID 查询用户（内存缓存，零 DB）→ 校验协议权限
    ↓
解析请求体 JSON → 提取 model 字段
    ↓
查询智能路由（内存缓存，按模型 ID + 协议类型）→ 获取 CachedAIRoute
    ↓
算法选择器选择目标源站（指定型/稳定型/经济型/智能型）→ 获取 endpoint ID
    ↓
（经济型）通过 Session 识别层 `RecognizeSessionID(body, protocolType)` 解析 session_id → 按 session 分配源站
    ↓
查询目标源站信息（内存缓存，零 DB）→ 获取源站 URL + API Key
    ↓
检查源站是否被禁用（TAgentDstEndPoint.Status == 0）→ 禁用则返回 403 Forbidden
    ↓
拼接目标 URL + 替换 Authorization + 替换请求体 model
    ↓
（协议转换器）按 AlgorithmType 决定：1=协议直连 / 2=协议转换器
    ↓
转发到目标源站 → 透传响应
    ↓
（稳定型）遇到 429/500/502/503/504/连接错误 → 自动切换下一个源站重试
    ↓
异步记录请求/响应到哈希分表
```

## 数据库分表字段规范（协议转换审计）

| 字段 | 说明 |
|------|------|
| `request_headers` | 实际转发给目标源站的请求头（数据库原文保存；读取展示前脱敏 Authorization Bearer） |
| `request_src_protocol_headers` | 客户端原始请求头（数据库原文保存；读取展示前脱敏 Authorization Bearer） |
| `request_body` | 实际转发给目标源站的请求体（base64 编码） |
| `request_src_protocol_body` | 客户端原始请求体（base64 编码） |
| `response_body` | 实际返回给客户端的响应体（base64 编码） |
| `response_src_protocol_body` | 目标源站原始响应体（base64 编码） |

## Web UI 当前约定（2026-06-18）

| 场景 | 约定 |
|------|------|
| `/AIRouteManage` 管理员操作列 | `对话` → `./ChatDialog?user_name=...&model_name=...`；`浏览记录` → `./ChatAnalysis?user_name=...&model_name=...`；`统计` → `./ChatAnalysisTotal?user_name=...&model_name=...` |
| `/AIRouteManage` 用户操作列 | `对话` / `浏览记录` / `统计` 只传 `model_name`，用户身份由 JWT 决定 |
| Query 参数 | JS 侧必须使用 `encodeURIComponent()`，避免模型名/用户名特殊字符破坏链接 |
| 操作列布局 | 新增按钮时使用 flex + wrap（如 `.route-actions`），避免移动端表格被撑宽 |
| 按钮颜色 | 启用态使用蓝/紫/青/绿/红等语义色；灰色只用于 disabled、只读输入、禁用状态标签 |
| 详情行 colspan | 仅新增/删除表格列时调整；仅在已有"操作"单元格内加按钮时无需调整 |
| `/ProtocolConvertAnalyzer` | 管理端/用户端记录表显示 `ID`；选择记录只填充 Input 并清空 Output；方向按 `protocol_type` 只读展示；仅点击 `执行转换` 才调用转换接口填充 Output |
| `/ChatAnalysis` 时间跨度 | 1/3/5/7/14/30/60/90 天 + 全部，默认 3 天；localStorage 持久化；URL 参数优先 |
| `/ChatAnalysis` 首屏分页 | 默认 page=1，不恢复 localStorage 深分页，避免大 OFFSET 慢查询 |
| `/SpiderDataSource` 折叠 | 记录支持折叠/展开（localStorage 记忆）；折叠后隐藏 `描述` 列、保留 `备注` 列，行高变小更紧凑 |
