# AGENT.md - LsmTokensServer 通用 AI Agent 规范

> 面向 Claude Code、Kilo Code、OpenCode、pi、OpenClaw 等 AI Agent 的高密度入口。工具特定说明见 [`CLAUDE.md`](CLAUDE.md) / [`KILO.md`](KILO.md)，爬虫任务流程见 [`Mission_Spider_MCP_Proc.md`](Mission_Spider_MCP_Proc.md)。
> **版本历史**: 完整版本历史（v2.0.0-v2.0.66）见 [`CHANGELOG.md`](CHANGELOG.md)，强制规则归档见 [`CLAUDE.md`](CLAUDE.md)。

**当前版本**: v2.0.66（详见 [`CLAUDE.md`](CLAUDE.md)）

---

## 🧰 本地私有 Python 工具集（Agent 加载用）

> 这些工具以本地目录形式存在于工程根目录，**仅在本机使用，不入库**（`.gitignore` 已排除）。
> AI Agent（Claude Code / Codex / OpenCode / pi / Hermes / OpenClaw 等）启动时扫描项目根目录，结合本节说明加载。

| 工具目录 | 模型 / 接口 | 用途 |
|----------|------|------|
| `python-generate-image-tool/` | `doubao-seedream-5-0-pro-260628`（固定），API `https://ark.cn-beijing.volces.com/api/v3/images/generations` | 火山引擎方舟大模型图片生成 SDK，含单元测试与端到端测试 |

调用示例：

```python
from src import ArkImageGenerator

gen = ArkImageGenerator()
path = gen.generate_and_save(
    prompt="赛博朋克风格城市夜景",
    size="2560x1440",        # 最小像素 3,686,400
    watermark=False,
    filename_prefix="cover",
)
```

API Key 加载优先级：环境变量 `ARK_API_KEY` > `.env` > 代码内置默认值。详细约束（异常层级、超时、小图标 workaround）见 [`CLAUDE.md`](../../CLAUDE.md) §6。

---

## 🕷️ MCP 爬虫服务快速参考（v2.0.0 / 接口 v2.0.0）

**服务地址**: http://localhost:29002

| 文档 | 用途 |
|------|------|
| [`Mission_Spider_MCP_Proc.md`](Mission_Spider_MCP_Proc.md) | **首先阅读**：Agent 任务流程（v2.0.0 chromedp 模式） |
| [`MCP_SpiderWebData_def.md`](MCP_SpiderWebData_def.md) | `/SpiderWebData` 详细定义 |
| [`MCP_GetSpiderDataSource_def.md`](MCP_GetSpiderDataSource_def.md) | `/GetSpiderDataSource` 详细定义 |
| [`MCP_InputSpiderDailyInfo_def.md`](MCP_InputSpiderDailyInfo_def.md) | `/InputSpiderDailyInfo` 详细定义 |

**三接口**：
- `/SpiderWebData` - 爬取（支持 8 个交互 action：navigate/click/scroll/scroll_to/fill_form/extract/screenshot/get_state）
- `/GetSpiderDataSource` - 数据源（返回 `id` / `url_address` / `description` / `status`）
- `/InputSpiderDailyInfo` - 保存（必须由 Agent 显式调用）

**实现文件**（v2.0.0 重构后）：
- `mcp_interface_common.go` - 共享类型 + 会话管理 + 内容提取
- `mcp_interface_spiderwebdata.go` - `/SpiderWebData` 接口
- `mcp_interface_getspiderdatasource.go` - `/GetSpiderDataSource` 接口
- `mcp_interface_inputspiderdailyinfo.go` - `/InputSpiderDailyInfo` 接口
- `spider_cdp_browser.go` - Chrome 进程生命周期（startChrome / killChrome / healthCheckLoop）
- `spider_cdp_engine.go` - `SpiderEngine` 单例 + `crawlWebDataCDP` 实现
- `spider_cdp_actions.go` - 8 个交互 action 派发
- `spider_cdp_session.go` - per-session chromedp.Context 绑定
- `spider_cdp_selectors.go` - 选择器解析（.class / #id / text: / xpath: / tag）
- `mysql_spider_model.go` - `TSpiderDataSource` / `TSpiderDailyInfo` + 分表
- `openclaw_client.go` - OpenClaw 本地 SSE 客户端（v2.0.4）
- `server_web_spider_crawl.go` - `/SpiderDataSourceCrawl` SSE 端点 + 爬取模态框（v2.0.4）

---

## 1. 项目概述

LsmTokensServer = AI 代理服务 + AI 信息爬虫：

| 服务           | 端口      | 协议       | 说明                                                |
| -------------- | --------- | ---------- | --------------------------------------------------- |
| Agent Proxy    | 29000     | HTTP       | AI API 代理入口                                     |
| Manager Web    | 9101      | HTTP       | 管理员后台：用户/模型/源站/路由/浏览记录/协议分析器 |
| User Web       | 29001     | HTTP/HTTPS | 用户门户（v1.3.0+ 支持 HTTPS）                      |
| **Spider MCP** | **29002** | HTTP       | 爬虫 MCP 服务（Agent 入口）                         |

**HTTPS（v1.3.0+）**：仅 User Web 支持；通过 `userWebUseHTTPS` / `userWebCertFile` / `userWebKeyFile` 配置；自签证书放项目根目录 `server.crt` / `server.key`。

核心特性：内存缓存热路径零 DB、动态配置零重启、智能路由、源站级协议处理算法、v1.3.0 协议转换器、SSE 透传、MySQL 哈希分表、Request Tools 解析、AI Agent 识别统计、协议转换分析器、**v2.0.0 爬虫服务 chromedp 模式**。

## 2. 强制工作流

### 编译 / 测试 / 重启

- **禁止**直接 `go build` / `nohup ./LsmTokensServer` / `./LsmTokensServer -d`
- 必须通过：

```bash
go test ./...
gofmt -w <修改的 .go>
./rebuild_restart_app.sh --build-only   # 仅编译
./rebuild_restart_app.sh                # 滚动重启
```

- 修改 Go 文件后必须 `gofmt -w`
- 测试失败必须先修复再编译重启
- 重启会中断流式响应，确认没有进行中的长连接后再执行

### 运行保护

用户/模型/源站/路由等配置通过 Web 管理页动态生效，**不要为配置变更重启**。仅代码变更或必须重载二进制时才重启。

## 3. 编码规范

- Go 1.22+，标准库 `net/http`，GORM + MySQL
- 严格错误处理，禁止忽略关键错误
- 导出函数必须有注释
- 测试文件命名 `*_test.go`，函数 `Test...`，与功能代码同包
- 单个 `.go` 文件不超过 **1500 行**；页面模板接近上限时按 `*_styles.go` / `*_body.go` / `*_scripts.go` 拆分
- Web 页面 `href` / `src` / `fetch()` / `action` 全部使用相对路径
- 新增/调整数据库索引写在 GORM model tag / AutoMigrate 中；分表模型用 `index:,composite:<id>,priority:n` 让 GORM 按表生成索引名

## 4. 前端 SubAgent 规则（强制）

修改以下内容时**必须**先调用前端 SubAgent：
1. `server_web_*.go` 中的 HTML 模板字符串
2. 内联 CSS / `<style>`
3. 内联 JavaScript / `<script>`
4. Web 页面路由或模板
5. `server_web_common_*.go` 共享前端组件

检查项：模板拼接顺序、DOM 闭合、CSS 作用域、相对路径、`.header-bar`/`.nav-bar` sticky 定位、相同功能 JS 的 MOE 一致性、响应式断点（主断点 `768px`）。

**Web UI 约定（高频）**：
- 页面链接一律相对路径，`encodeURIComponent` 编码查询参数
- `/AIRouteManage` 操作按钮：管理员端传 `user_name`+`model_name`，用户端只传 `model_name`
- `/ChatAnalysis` 首屏默认 page=1（不恢复 localStorage 深分页）；筛选状态 + 分页用 localStorage 同步 URL
- `/ProtocolConvertAnalyzer` 管理/用户端布局一致：列表展示 ID 列；`转换方向` 只读；`执行转换` 才填 Output；4 项筛选用 localStorage 持久化（`lsm_protocol_converter_filters` / `lsm_protocol_converter_user_filters`）；URL 参数优先于 localStorage；`结构转换成功率`/`字段转换率` 由后端 `CalculateConversionMetricsForSection` 实际转换后计算
- 启用态核心按钮避免黑/灰背景；优先蓝/紫/青/绿/红等语义色 + hover 态
- `/ChatAnalysis` 浏览记录页已拆分为 `server_web_manager_chat_page_{html,styles,body,scripts}.go`；`agentPageTemplate` 是组合入口；修改时禁止改变 `template.New(...).Parse(...)` 拼接方式

## 5. 代理核心流程与约束

Claude Code / Kilo Code / OpenCode / pi / OpenClaw 等 AI IDE 通过本代理服务访问底层 AI 模型：

1. **API Key 映射**：API Key → 用户模型
2. **模型名替换**：用户配置 → 源站实际模型
3. **智能路由**：按模型/协议/算法选目标源站；v1.3.0 起每个目标源站带 `DstEndPointAlgorithmTypeList`（`1=协议直连` / `2=协议转换器`）
4. **请求日志**：完整记录到 MySQL 哈希分表（`/ChatAnalysis` 浏览）；`DstEndPointAlgorithmType` 记录本次实际算法
5. **请求头脱敏边界**：`RequestHeaders` / `RequestSrcProtocolHeaders` 写库保留原始值；从数据库读出后**后端**正则脱敏 `Authorization: Bearer ...` → `************************`；禁止把完整 Bearer Token 传到前端再脱敏
6. **AI Agent 识别**：从 User-Agent 识别（claude-cli / opencode / Kilo-Code / OpenClaw / pi）

**热路径必须零 DB**：API Key / 用户 / 路由 / 源站 / 协议算法都走 `AgentCache`；`AlgorithmTypeForEndPointID(endpointID)` 走 `CachedAIRoute`，禁止转发时为算法查询 MySQL。

**v1.3.0 协议算法**：`NormalizeDstEndPointAlgorithmTypeList` 自动补齐；`1=协议直连` 源站协议=路由协议；`2=协议转换器` 源站协议=路由协议相反（OpenAI↔Anthropic 互转 body+header+path）。历史数据若 `dst_endpoint_algorithm_type=0/NULL`，展示按 `1=协议直连` 兼容。

**v2.0.5 经济型算法（`AlgorithmStrategyType=3`）**：
- Session 级别负载均衡：通过 `RecognizeSessionID(body, protocolType)` 从请求 body 解析 `session_id`，按 session 轮询分配到各源站
- 源站连续 3 次接口错误（`EconomicEndpointMaxConsecutiveFailures=3`）自动从 `DstEndPointIDList` 移除，同步更新 DB + 内存缓存
- 支持 Anthropic 和 OpenAI 协议（通过 `SessionRecognizer` 接口层统一识别）
- 无 `session_id` 时降级为 round-robin 行为
- 实现文件：`agent_algorithm_economic.go` / `agent_algorithm_economic_test.go` / `recognizer_session_id.go`

**v2.0.17 经济型算法 KB（知识问答）分支**：
- 触发条件（全部满足）：`RecognizeSessionID == ""` 且 `ExtractRequestToolNamesForAlgorithm == ""` 且 `RecognizeAgentTool(UA).AgentToolName` 不在高阶 Agent 白名单
- 高阶 Agent 白名单：`claude-cli` / `OpenAI/JS` / `OpenAI/Python` / `opencode` / `Kilo-Code`（大小写不敏感；UA 中 `/version` 后缀兼容）
- 行为：调用 `EconomicAlgorithmSelector.SelectForKBRequest(route)` 从 `route.DstEndPointIDs` 中随机挑选一个**可用源站**
- **不消费**经济型 livePool，**不写** sessionIndex / sessionQueue

**v2.0.16 Session 识别层**：
- 协议无关抽象：`recognizer_session_id.go` 定义 `SessionRecognizer` 接口 + 注册表 + `RecognizeSessionID(body, protocolType)` 通用入口
- 按协议分别实现：`openAISessionRecognizer` / `anthropicSessionRecognizer`
- Agent 工具级识别：`AgentToolSessionRecognizer` 接口 + OpenClaw 实现

**v2.0.16 SessionID 入库**：
- `TAgentHttpTransactionDataItem` 新增 `SessionID` 字段（size:128, indexed）；未识别则填 `"unknown_session_id"`
- 所有代理请求自动识别并保存 `session_id`

**v2.0.16 识别功能模块化**：
- Agent Name 识别 → `recognizer_agent_name.go`
- Request Tools 解析 → `recognizer_openai_function_call.go`（OpenAI 协议）+ `recognizer_anthropic_tool_call.go`（Anthropic 协议）

## 6. 核心文件速查

```
# 代理核心
server_http_ai_proxy.go        # 代理转发主路径
server_http_agent_proxy.go     # 代理基础设施（启停、端口）
protocol_converter.go          # OpenAI↔Anthropic 协议转换 + 分析器
protocol_openai_to_anthropic.go / protocol_anthropic_to_openai.go
agent_algorithm.go / agent_algorithm_stable.go / agent_algorithm_economic.go  # 路由算法策略

# 识别器（v2.0.16 重构）
recognizer_agent_name.go                # Agent Name 识别
recognizer_session_id.go                # Session ID 识别（合并 4 个 session 识别文件）
recognizer_openai_function_call.go      # OpenAI 协议 tools / tool_calls 解析
recognizer_anthropic_tool_call.go       # Anthropic 协议 tools 解析
temp_backfill_session_id.go             # SessionID 后台补全（临时文件）

# 数据库与缓存
mysql_http_agent_cache.go      # AgentCache（用户/模型/源站/路由/Agent统计/爬虫数据源）
mysql_http_agent_sub_table.go  # TAgentHttpTransactionDataItem_XX 分表
mysql_http_agent_analysis.go   # 协议/会话/任务统计 + parseRequestToolsFromBody 入口
mysql_http_agent_model.go      # 核心数据模型
mysql_http_*_manage.go         # 各资源 CRUD + 缓存同步

# Web 公共组件
server_web_common*.go          # 共享 HTML 骨架/导航/弹窗/工具栏
server_web_security.go         # 安全中间件

# Web 页面
server_web_manager*.go         # Manager Web（9101）
server_web_user*.go            # User Web（29001）

# 爬虫 MCP（v2.0.0 重构后）
mcp_interface_common.go         # 共享类型 + 会话管理 + 内容提取
mcp_interface_spiderwebdata.go  # /SpiderWebData
mcp_interface_getspiderdatasource.go
mcp_interface_inputspiderdailyinfo.go
spider_cdp_browser.go          # Chrome 进程生命周期
spider_cdp_engine.go           # SpiderEngine 单例 + crawlWebDataCDP
spider_cdp_actions.go          # 8 个交互 action 派发
spider_cdp_session.go          # per-session chromedp.Context
spider_cdp_selectors.go        # 选择器解析
spider_scheduler.go            # 调度器（仍 opt-in）
mysql_spider_model.go          # TSpiderDataSource / TSpiderDailyInfo + 分表
server_api_spider.go           # 数据源管理 API（非 MCP 接口）
server_web_spider_*.go         # 双端 Web UI
```

## 7. 数据库与缓存规则（v1.5.0）

**内存缓存优先**：`AgentCache` 已有的（用户/模型/源站/路由/Agent 统计/爬虫数据源）**必须**从内存读；`TAgentHttpTransactionDataItem_XX` 分表数据量过大不做全量缓存。

**数据变更流程**：MySQL 事务成功 → 缓存失效 → 下次查询从 MySQL 重新加载。**禁止**先写缓存再写 MySQL。

**大字段查询规则（强制，v2.0.39 强化）**：
- ⛔ **禁止**在 `TAgentHttpTransactionDataItem` 任何查询中使用 `Find(&records)` / `First(&record)` 不带 `Select(...)` 限定列。详情页按需加载允许。
- ✅ 列表查询**禁止** SELECT 大字段：`RequestBody` / `RequestSrcProtocolBody` / `ResponseBody` / `ResponseSrcProtocolBody` / `RequestHeaders` / `RequestSrcProtocolHeaders` / `ResponseHeaders` / `ResponseSrcProtocolHeaders`（共 4×longtext + 4×text，单行可达 4MB+）。
- ✅ 列表查询统一调用 **`selectTransactionColumns()`**（`mysql_http_agent_sub_table.go`）拿白名单常量，新增列**只能** append，禁止加入 longtext。
- `QueryAgentHttpTransactions()` 只返回元数据字段
- 按需加载：详情页点击时调用 `GetAgentHttpTransactionBodyByID()` / `GetAgentHttpTransactionDetailByID()`

**N+1 查询规则（v2.0.39 新增强制项）**：
- ⛔ 列表类接口禁止对每条路由/用户/模型循环调用 DB-heavy 函数
- ✅ 必须走批量聚合接口：`BatchGetRouteStatsByRouteIDs` / 未来聚合路径
- 前端 N+1 同样禁止：循环中对每条记录并发发 XHR 必须聚合到 `batch_*` 路径

## 8. AI Agent Tools 识别与统计（永久机制）

| 表                                 | 用途                                                            |
| ------------------------------------ | --------------------------------------------------------------- |
| `TAgentHttpTransactionDataItem_XX` | 8 张哈希分表，每条记录带 `agent_tool_name` / `agent_tool_info`  |
| `TAgentHttpAgentInfo`              | 唯一索引 `agent_tool_name`，含首/末次时间 + usage_count         |

识别 User-Agent 格式：
- `claude-cli/1.0` → `claude-cli`
- `Kilo-Code/3.0` → `Kilo-Code`
- `OpenClaw/1.1` → `OpenClaw`
- `pi/1.0` → `pi`
- `^([a-zA-Z0-9\-]+)/(.*)$` → 第一段
- 失败 → `unknown`

写入点：`server_http_ai_proxy.go` → `logAIProxyTransaction()` → `SaveAgentHttpTransaction`；统计更新：`UpdateAgentInfoUsage()` 异步原子写。

## 9. Request Tools 解析（永久机制）

`request_tools` 已完成历史回填。新数据由永久代码路径自动解析：
- 写入：`mysql_http_agent_sub_table.go` 的 `SaveAgentHttpTransaction`
- 解析：`mysql_http_agent_analysis.go` 的 `parseRequestToolsFromBody`
- 字段：`mysql_http_agent_model.go` 的 `RequestTools`

规则：从**实际转发给目标源站**的 base64 `request_body` 解析（不是 `request_src_protocol_body`）。支持 Anthropic `tools[].name`、OpenAI `tools[].function.name`、字符串 tools、自定义字段、嵌套包装、多层 base64、SSE 首个 JSON、`messages[].tool_calls[].function.name` 兜底。多工具名英文逗号拼接；无工具返回空字符串。

## 10. 协议转换分析器

`/ProtocolConvertAnalyzer` 提供 OpenAI↔Anthropic 协议互转分析：
- 结构转换成功率（`structure_success_rate`）
- 字段转换率（`field_conversion_rate`）
- 基础指标：转换率、字段覆盖率、语义映射率

**关键约束**：结构/字段转换率必须由后端 `CalculateConversionMetricsForSection` 实际执行转换后计算（通过 `convertRequestBodyOnly` / `convertResponseBodyOnly` 拆分避免递归）。**禁止**用 `calculateBasicMetrics`（仅返回基础三项）兜底——会导致这两个指标始终为 0%。

## 11. 常用回归测试

```bash
# 代理核心
go test -run TestEndToEndProxyForwarding -v

# request_tools
go test -run 'TestParseRequestToolsFromBody|TestExtractToolNamesFromMap|TestTruncateRequestTools' -v

# AI Agent 识别
go test -run TestAgentToolRecognition -v

# 协议转换（结构/字段转换率 + 互转 + 学习器）
go test -run 'TestConvertProtocol.*Metrics|TestConvertOpenAIToAnthropic|TestConvertAnthropicToOpenAI|TestProtocolConverterLearning' -v

# 经济型算法
go test -run 'TestEconomicSelector|TestEconomicEndpointFailureTracking' -v

# 经济型算法 v2.0.17 KB 分支
go test -run 'TestEconomicSelectForKBRequest|TestIsAdvancedAgentToolName|TestIsKnowledgeBaseRequest|TestExtractRequestToolNamesForAlgorithm' -v

# Session 识别层（v2.0.8）
go test -run 'TestRecognizeSessionID|TestAnthropicSessionRecognizer|TestOpenAISessionRecognizer|TestRegisterSessionRecognizer' -v

# 协议转换错误响应
go test -run 'TestConvertProtocolError|TestConvertProxyResponse' -v

# 爬虫 CDP 解析层
go test -run 'TestParseSelector|TestBuildNextPageURL|TestResolveURL|TestTextSearchJS' -v

# 脱敏
go test -run 'TestRedactAuthorization' -v

# 全量
go test ./...
```

## 12. HTTPS 服务配置（v1.3.0+）

仅 `userWebListenPort` 对应的 User Web 服务支持 HTTPS，其他服务保持 HTTP。

| 字段              | 类型   | 默认值       | 说明                             |
| ----------------- | ------ | ------------ | -------------------------------- |
| `userWebUseHTTPS` | bool   | false        | 启用 HTTPS                       |
| `userWebCertFile` | string | "server.crt" | 证书（相对可执行目录或绝对路径） |
| `userWebKeyFile`  | string | "server.key" | 私钥                             |

启用流程：修改配置 → `./rebuild_restart_app.sh` 重启 → 验证：

```bash
curl -k -s -o /dev/null -w "%{http_code}" https://localhost:29001/
grep "User Web server" LsmTokensServer.log
```

## 13. 常见问题速查

| 问题                               | 优先检查                                                                                                              |
| ---------------------------------- | --------------------------------------------------------------------------------------------------------------------- |
| 浏览记录 `工具列表: 无`            | `parseRequestToolsFromBody` / `SaveAgentHttpTransaction` / `request_tools` 列                                         |
| 浏览记录 `AI Agent: 无`            | `RecognizeAgentTool` 调用 / `SaveAgentHttpTransaction` 参数 / `agent_tool_name` 列                                    |
| 浏览记录 `协议算法: -/未知`        | `QueryAgentHttpTransactions` 是否选 `dst_endpoint_algorithm_type`；历史记录 `0/NULL` 按 `1=协议直连` 兼容             |
| 协议转换未触发                     | 路由源站 `dst_endpoint_algorithm_type_list=2` + 源站协议与路由协议相反 + 缓存已同步                                   |
| **经济型算法源站自动移除**         | 检查 `LsmTokensServer.log` 中 `[ECONOMIC]` 日志；连续 3 次失败自动移除                                                   |
| **经济型算法 session 分配不均** | 检查请求 body 是否含 `metadata.user_id`（内含 `session_id`）；无 session_id 时降级为 round-robin                      |
| Agent 筛选下拉无数据               | `TAgentHttpAgentInfo` 表 / `GetDistinctAgentToolNames` / 内存缓存加载                                                 |
| 代理认证失败                       | `mysql_http_agent_cache.go` 中 API Key → Model 缓存                                                                   |
| 禁用源站仍转发                     | `TAgentDstEndPoint.Status` / `forwardWithRetry` / 缓存同步                                                            |
| Web 修改后用户端不刷新             | 增删改事务后的内存缓存同步 + `setNoCacheHeaders(w)`                                                                   |
| 服务启动失败                       | `LsmTokensServer.log` / MySQL 配置 / 端口占用                                                                            |
| HTTPS 无法访问                     | 证书路径 / 权限 / `userWebUseHTTPS` 配置值                                                                            |
| **爬虫多轮 click 无效**            | `session_id` 是否传入；session TTL=10 分钟；选择器语法                                                               |
| **爬虫 Chrome 启动失败**           | `spiderChromePath` 是否找到 google-chrome / chromium-browser；`spiderCDPPort=9222` 端口占用                           |
| **爬虫并发过高导致 Chrome 卡死**   | `spiderMaxConcurrency` 默认 8，超过 64 会被自动限制                                                                   |

## 14. Go Web 调试工具链（go-web-debug-tool）

`go-web-debug-tool/` 是基于 Go + chromedp / CDP 的 **HTTP MCP 调试服务**（git 子模块），让 Orchestrator / SubAgent 通过 5 个 JSON 接口远程驱动真实 Chrome 浏览器做页面浏览、调试与自动化采集。
默认监听 `http://localhost:28999`，统一信封 `{code, message, data}`。

### 14.1 启动与停止

```bash
cd go-web-debug-tool
go build -o GoWebDebugTool .      # 编译
./GoWebDebugTool -d               # 守护进程模式启动
./GoWebDebugTool -u               # 优雅停止
```

### 14.2 五个接口 (POST + application/json)

| 接口 | URL | 用途 |
|------|-----|------|
| 新建页面 | `POST /NewChromePage` | 打开一个新的 Chrome 页面 / 标签 |
| 控制页面 | `POST /ControlChromePage` | 在指定 page 上执行交互动作 |
| 查询页面 | `POST /LookChromePageInfo` | 读取 Console / Network / DOM / screenshot 等 |
| 关闭页面 | `POST /CloseChromePage` | 关闭页面并释放 CDP 上下文 |
| 列出页面 | `POST /ListChromePages` | 枚举当前所有受管页面 |

> 所有 `page_id` 形如 `p_xxxxxxxx`，由服务端生成，**Agent 必须视为不透明字符串**保存。

### 14.3 Orchestrator / SubAgent 状态管理

主 Agent **必须** 持有一张全局的 `page_id ↔ 业务语义` 索引表。

- 启动或长时间空闲后，先调用 `/ListChromePages` 同步真实状态
- 任何 `2000 page_id not found` 都视为「本地索引漂移」，**立即** 重新拉取 `/ListChromePages` 修正
- SubAgent 派生触发条件：≥ 2 个页面联动 / 单流程超过 10 步 / 并行采集多页面信息

### 14.4 异常处理与重连

| 错误码 | 含义 | SubAgent 推荐策略 |
|--------|------|---------------------|
| `2000` | `page_id` 不存在 | 上报主 Agent；不要重试。 |
| `2001` | CDP 断开 | 等待 1-3 秒后重试一次；连续 3 次失败则向主 Agent 报「需要重建页面」。 |
| `2002` | Action execution failed | 视 message 决定：selector 没找到 → 重新定位再试；其他通常不重试。 |
| `2003` | 页面崩溃 | 立刻调用 `/CloseChromePage` 清理；本任务失败，由主 Agent 决定是否重开页面重做。 |

### 14.5 资源清理保障

- 每个 `page_id` 的生命周期由 **创建它的 Agent** 负责。
- SubAgent 结束时 **必须** 对自己创建的所有 `page_id` 调用 `/CloseChromePage`；返回 `2000` 视为已清理。

完整规范：[`go-web-debug-tool/MCP_Proc_Def.md`](go-web-debug-tool/MCP_Proc_Def.md)。

## 15. AI 信息爬虫功能（v1.4.0+ / 接口 v2.0.0）

**数据源管理**：管理员可配置公共爬虫数据源，用户可配置个人数据源。

**抓取模式（v2.0.0）**：Chrome DevTools Protocol 单模式。服务进程内嵌启动 headless Chrome（端口 9222），所有抓取走 chromedp；HTTP 模式已彻底移除。Chrome 不可用时直接返 `success=false`。

**按月分表**：`TSpiderDailyInfo_YYYYMM`，根据 `crawl_time` 动态选择；不存在时 `createSpiderDailyInfoTable` 自动建表（含 `(data_source_id, crawl_time DESC)` 复合索引）。

**核心模块**：
- `mysql_spider_model.go` - 数据模型 + AutoMigrate
- `mcp_interface_*.go` - 三个 MCP 接口
- `spider_cdp_browser.go` - Chrome 进程生命周期
- `spider_cdp_engine.go` - `SpiderEngine` 单例 + `crawlWebDataCDP`
- `spider_cdp_actions.go` - 8 个交互 action 派发
- `spider_cdp_session.go` - per-session chromedp.Context 绑定
- `spider_cdp_selectors.go` - 选择器解析
- `spider_scheduler.go` - `SpiderScheduler` 单例，5 分钟检查，最大 3 并发（仍 opt-in）
- `server_api_spider.go` - 数据源管理 API + 权限控制
- `server_web_spider_data_source.go` / `server_web_spider_daily_info.go` - 双端 Web UI

**约束**：
- 权限隔离：用户看自己 + 公共（`user_id=0`），管理员看全部
- 内存缓存：`AgentCache` 包含 `spiderDataSources` / `spiderDataSourcesList`
- Chrome 必须可用（端口 9222 未占用；google-chrome-stable / google-chrome / chromium-browser 任一可执行）
- 并发控制：调度器 semaphore 限制 max 3；MCP handler 层 `spiderMaxConcurrency` 默认 8，上限 64
- **SpiderDataSource 折叠交互**：列表记录支持折叠/展开（localStorage 记忆状态）；折叠后隐藏 `描述` 列、保留 `备注` 列

**导航菜单**：管理员端和用户端导航栏均含 `MCP爬虫数据源` → `/SpiderDataSource` 和 `每日MCP信息` → `/SpiderDailyInfo`。
