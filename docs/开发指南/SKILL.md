---
name: lsm-http-agent
description: LsmTokensServer project workflow, frontend, proxy, database, and validation rules for AI agent tools
---

# SKILL.md - LsmTokensServer AI Agent Skill

> 面向 Claude Code、Kilo Code、OpenCode、pi、OpenClaw 等 AI Agent Tools 的紧凑加载指南。进入本工程后优先加载本文件，再按需跳转 `AGENT.md`、`CLAUDE.md`、`KILO.md`、`AGENT_INDEX.md`、`Developer_SOP.md`。

**当前版本**: v2.0.3
- v2.0.3: MCP 爬虫服务稳定性与反爬能力增强；SpiderDataSource 折叠交互（localStorage 记忆状态）
- v2.0.1: `/SpiderWebData` 接口新增 `elements` 元素清单（links/headings/paragraphs）
- v2.0.0: MCP 爬虫服务切换为 Chrome DevTools Protocol (chromedp)；HTTP 回退路径彻底移除；Agent 必须显式调用 `/InputSpiderDailyInfo` 保存数据

## 1. 项目定位

LsmTokensServer 是 Go 1.22+ 编写的 AI Relay / HTTP Agent：通过模型级 API Key 接收 Anthropic / OpenAI 请求，基于内存缓存识别用户模型与智能路由，按源站级协议算法（v1.3.0：`1=协议直连`、`2=协议转换器`）转发到目标源站，并把完整请求响应写入 MySQL 哈希分表供 Web 分析页面展示。

| 服务 | 端口 | 说明 |
|------|------|------|
| Agent Proxy | `29000` | AI API 代理入口 |
| Manager Web | `9101` | 管理员后台 |
| User Web | `29001` | 用户门户（HTTPS 可选） |
| **Spider MCP** | **`29002`** | 爬虫 MCP 服务（v2.0.0 chromedp 模式） |

## 2. 强制工作流

### 编译 / 测试 / 重启

禁止直接运行：

```bash
go build
nohup ./LsmTokensServer
./LsmTokensServer -d
```

必须使用：

```bash
go test ./...
./rebuild_restart_app.sh                # 完整重启（编译 + 运行）
```

规则：

- 修改 Go 文件后必须 `gofmt -w` 修改过的 `.go` 文件
- 测试失败必须先修复；不要带失败测试继续编译或重启
- 配置变更（用户、模型、源站、路由）正常通过 Web 页面实时生效，不需要重启
- LsmTokensServer 是 AI IDE 代理依赖，重启会中断流式响应；仅代码变更或必须重载二进制时重启。
- **禁止**给 `rebuild_restart_app.sh` 带 `--build-only`、`--skip-web` 等参数（完整重启即可）。

## 3. 前端修改 Skill

修改以下内容前必须调用前端 SubAgent 检查：

- `server_web_*.go` 内 HTML 模板字符串
- 内联 CSS / `<style>`
- 内联 JS / `<script>`
- Web 页面路由或模板
- `server_web_common_*.go` 共享前端组件

检查重点：

- 模板拼接顺序：常见为 `sharedPageHead + headerToolbarHTML + navHTML + pageHTML + toolbarStyles + toolbarScripts`
- DOM 闭合：避免重复或缺失 `</body></html>`
- CSS 作用域：页面级 `.btn`、`.card`、`table` 等可能影响当前模板内所有元素
- 路径：所有 `href`、`fetch()`、`action` 使用相对路径，禁止 `/` 开头绝对路径
- sticky：不要破坏 `.header-bar` / `.nav-bar` 的 sticky top / z-index
- 响应式：主断点 `768px`；表格操作按钮优先 flex + wrap
- MOE 一致性：管理员端和用户端同类功能的按钮、参数、视觉保持一致

### Web UI 约定

- `/AIRouteManage` 管理员路由操作：
  - `对话` → `./ChatDialog?user_name=...&model_name=...`
  - `浏览记录` → `./ChatAnalysis?user_name=...&model_name=...`
  - `统计` → `./ChatAnalysisTotal?user_name=...&model_name=...`
- `/AIRouteManage` 用户路由操作：
  - 只传 `model_name`，用户身份由 JWT 确定
- Query 参数必须 `encodeURIComponent()`
- 启用态按钮不要用黑色/灰色背景；灰色只用于 disabled、只读输入或状态标签
- 新增操作按钮时优先放入已有操作单元格，不新增列；只有新增/删除列才调整详情行 `colspan`
- `/ProtocolConvertAnalyzer` 管理端/用户端记录表显示 `ID` 列；选择记录只填充 Input（四区块）并清空 Output；方向按记录 `protocol_type` 只读展示；仅点击 `执行转换` 才调用转换接口填充 Output
- `/ChatAnalysis` 页面模板入口是 `server_web_manager_chat_page_html.go` 的 `agentPageTemplate`，CSS/HTML/JS 分别在 `server_web_manager_chat_page_styles.go`、`server_web_manager_chat_page_body.go`、`server_web_manager_chat_page_scripts.go`；管理员端和用户端共用同一模板
- `/ChatAnalysis` 筛选（含时间跨度 1/3/5/7/14/30/60/90，默认 3 天）使用 localStorage 恢复，URL 参数优先；分页越界以后端 `currentPage` 回退
- `/ChatAnalysis` 首屏不得恢复 localStorage 中的历史深分页页码；URL 未显式携带 `page` 时默认回到第 1 页，避免旧页码触发大 OFFSET 慢查询

## 4. 代理热路径 Skill

代理核心热路径必须零 DB 访问：

```text
Bearer API Key
→ AgentCache 查 UserModel
→ AgentCache 查 User
→ 解析 body.model
→ AgentCache 查 route(modelID + protocol)
→ 算法选择 endpoint
→ CachedAIRoute 取 endpoint 对应 DstEndPointAlgorithmType
→ AgentCache 查 endpoint
→ 算法=2 时协议转换；算法=1 时直连
→ 替换 Authorization + model
→ 转发 / SSE 透传
→ 异步写 MySQL 分表
```

禁止在 `handleAIProxyRequest` 等热路径新增 MySQL 查询；源站协议算法也必须从 `CachedAIRoute.DstEndPointAlgorithmTypes` 读取。

协议转换器必须同步转换 Body、Header 和 API 路径：OpenAI `/chat/completions`/`/v1/chat/completions` → Anthropic `/v1/messages`，Anthropic `/messages`/`/v1/messages` → OpenAI `/v1/chat/completions`；非 2xx 源站错误要转换为客户端协议的错误 envelope，保留 upstream message/type，不能转换成空成功响应。

请求头安全边界：`RequestHeaders` / `RequestSrcProtocolHeaders` 写库保持原始值；数据库读取后返回给 Web/API/协议转换分析器前，必须在后端脱敏 `Authorization: Bearer ...` 为 `Authorization: Bearer ************************`，不得向前端暴露完整 API Key。

关键文件：

| 领域 | 文件 |
|------|------|
| 代理核心 | `server_http_ai_proxy.go`, `server_http_agent_proxy.go`, `server_http_ai_proxy_security.go` |
| 缓存 | `mysql_http_agent_cache.go`, `mysql_http_stats_cache.go` |
| 路由算法 | `agent_algorithm.go`, `agent_algorithm_stable.go` |
| 分表日志 | `mysql_http_agent_sub_table.go` |
| 数据模型 | `mysql_http_agent_model.go` |

## 5. 数据库与缓存 Skill

配置表 CRUD 必须事务成功后同步缓存：

| 模块 | 典型文件 | 缓存动作 |
|------|----------|----------|
| 用户 | `mysql_http_user_manage.go` | `addUserToCache`, `invalidateUserCache` |
| 用户模型 | `mysql_http_model_manage.go` | `addModelToCache`, `invalidateModelCache` |
| 智能路由 | `mysql_http_ai_route_manage.go` | `addRouteToCache`, `updateRouteInCache`, `removeRouteFromCache`；同步 `DstEndPointAlgorithmTypeList` |
| 源站 | `mysql_http_dst_endpoint_manage.go` | `addDstEndPointToCache`, `invalidateDstEndPointCache` |
| 模型信息 | `mysql_http_model_info_manage.go` | `addModelInfoToCache`, `updateModelInfoInCache`, `removeModelInfoFromCache` |
| AI Agent 工具 | `mysql_agent_info_manage.go` | `UpdateAgentInfoUsage` |

查询性能规则：

- 新增或调整数据库索引必须优先写在 GORM model tag / AutoMigrate 中，禁止在初始化流程中手写 `CREATE INDEX` / `DROP INDEX`；分表模型使用 `index:,composite:<id>,priority:n` 让 GORM 按表生成索引名，避免 SQLite 全局索引名冲突
- 列表查询必须分页、筛选或限制时间范围
- 列表查询不要 SELECT 大字段：`request_body`、`response_body`、`request_src_protocol_body`、`response_src_protocol_body`
- 单条查询用 `First()`，不要用 `Find()` 查单条
- JSON API 响应使用 `setNoCacheHeaders(w)`

## 6. Request Tools Skill

`request_tools` 新记录由永久路径自动解析：

- 写入：`mysql_http_agent_sub_table.go` → `SaveAgentHttpTransaction`
- 解析：`mysql_http_agent_analysis.go` → `parseRequestToolsFromBody`
- 字段：`mysql_http_agent_model.go` → `RequestTools`
- 测试：`test_request_tools_parsing_test.go`

规则：

- 解析来源是实际转发给目标源站的 base64 `request_body`，不是 `request_src_protocol_body`
- 支持 Anthropic `tools[].name`、OpenAI `tools[].function.name`、字符串 tools、自定义字段、`metadata.tools`、`parameters.tools`、包装字段、明文/base64、多层 base64、SSE 首个 JSON、`messages[].tool_calls`
- 多工具名用英文逗号拼接；无工具返回空字符串
- 截断不追加 `...` 伪工具名

常用命令：

```bash
# 解析回归
go test -run 'TestParseRequestToolsFromBody|TestExtractToolNamesFromMap|TestTruncateRequestTools' -count=1 -v
```

## 7. 爬虫 MCP Skill（v2.0.3）

服务端口 `29002`，chromedp 单模式（HTTP 抓取路径已彻底移除）。Agent 6 步流程详见 `Mission_Spider_MCP_Proc.md`：

1. `GET /` 健康检查
2. `POST /GetSpiderDataSource` 获取数据源（v2.0.3 新增 `remark` 字段；折叠交互 localStorage 记忆）
3. 解析 `description` 确定处理策略
4. `POST /SpiderWebData` 爬取（8 个 action + v2.0.1 `elements` 元素清单）
5. Agent 自己清洗/翻译/截断
6. `POST /InputSpiderDailyInfo` 保存（**必调**）

实现文件：

| 区域 | 文件 |
|------|------|
| MCP 接口（v2.0.0 拆分） | `mcp_interface_common.go`, `mcp_interface_spiderwebdata.go`, `mcp_interface_getspiderdatasource.go`, `mcp_interface_inputspiderdailyinfo.go` |
| Chrome CDP 引擎（v2.0.0） | `spider_cdp_browser.go`, `spider_cdp_engine.go`, `spider_cdp_actions.go`, `spider_cdp_session.go`, `spider_cdp_selectors.go` |
| 数据模型 + 分表 | `mysql_spider_model.go` |
| Web 管理（非 MCP） | `server_api_spider.go`, `server_web_spider_*.go` |

约束：

- Chrome 必须可用：端口 `9222` 未占用；`spiderChromePath` 找 `google-chrome-stable` / `google-chrome` / `chromium-browser` 任一可执行
- session_id TTL=10 分钟；选择器语法 `.class` / `#id` / `text:xxx` / `xpath:expr` / `tag`
- 30s 健康检查 + 异常自动重启
- v2.0.3 反爬能力：请求头随机化、指纹模拟、请求间隔抖动
- v2.0.3 超时策略：全局/操作级/页面级多层超时
- Agent **必须**显式调用 `/InputSpiderDailyInfo`（v2.0.0 已移除自动保存）

## 8. 重要文件地图

| 区域 | 文件 |
|------|------|
| 工具总入口 | `SKILL.md`, `AGENT.md`, `CLAUDE.md`, `KILO.md`, `AGENT_INDEX.md`, `MEMORY.md` |
| 详细 SOP | `Developer_SOP.md` |
| 启动/配置/日志 | `main.go`, `server_conf.go`, `server_logger.go` |
| 管理端页面 | `server_web_manager*.go` |
| 管理端 API | `server_api_manager_*.go` |
| 用户端页面 | `server_web_user*.go` |
| 用户端 API | `server_api_user_*.go` |
| Web 公共组件 | `server_web_common*.go`, `server_web_security.go` |
| 协议转换 | `protocol_converter.go`, `protocol_openai_to_anthropic.go`, `protocol_anthropic_to_openai.go`, `server_web_*protocol_converter*.go` |
| 爬虫 MCP | `mcp_interface_*.go`, `spider_cdp_*.go`, `mysql_spider_model.go` |
| 测试 | `test_*_test.go`, `spider_cdp_*_test.go` |

## 9. 常用回归命令

```bash
# 代理核心
go test -run TestEndToEndProxyForwarding -count=1 -v

# request_tools
go test -run 'TestParseRequestToolsFromBody|TestExtractToolNamesFromMap|TestTruncateRequestTools' -count=1 -v

# 协议转换
go test -run TestConvert -count=1 -v

# 爬虫 CDP 解析层
go test -run 'TestParseSelector|TestBuildNextPageURL|TestResolveURL|TestTextSearchJS' -count=1 -v

# 脱敏
go test -run 'TestRedactAuthorization' -count=1 -v

# AI Agent 识别
go test -run TestAgentToolRecognition -count=1 -v

# 全量
go test ./...

# 完整重启（编译 + 滚动重启 + 验证端口）
./rebuild_restart_app.sh
```

## 10. 快速排查

| 问题 | 优先检查 |
|------|----------|
| 代理认证失败 | `mysql_http_agent_cache.go` 的 API Key → Model 缓存 |
| 禁用源站仍转发 | `TAgentDstEndPoint.Status`, `forwardWithRetry`, 缓存同步 |
| 浏览记录工具列表为空 | `parseRequestToolsFromBody`, `SaveAgentHttpTransaction`, `request_tools` 列 |
| Web 修改后页面旧数据 | CRUD 后缓存同步 + `setNoCacheHeaders(w)` |
| 按钮看起来不可用 | 检查是否误用灰/黑背景；启用按钮使用语义色 |
| 服务启动失败 | `LsmTokensServer.log`, MySQL 配置, 端口占用 |
| 爬虫 `success=false` 无详细错误 | Chrome 不可用；`[MCP]` / `[SPIDER]` 启动日志；端口 9222 是否被占用；v2.0.3 反爬触发目标站限流 |
| 爬虫多轮 click 无效 | `session_id` 是否传入；上一次响应 `CurrentRawHTML` 是否被缓存（TTL=10 分钟）；v2.0.3 检查选择器语法 |
| 爬虫并发卡死 | `spiderMaxConcurrency` 默认 8，上限 64；v2.0.3 超时策略防止卡死 |
