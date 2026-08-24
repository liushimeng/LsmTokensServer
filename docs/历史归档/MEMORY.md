# LsmTokensServer 项目记忆

> 给所有 AI Agent 工具（Claude Code / Kilo Code / OpenCode / pi / OpenClaw 等）加载的项目记忆。
> 最新代码与本文件不一致时以代码为准；修改前先读 [`AGENT.md`](AGENT.md) / [`CLAUDE.md`](CLAUDE.md) / [`KILO.md`](KILO.md)。

## 1. 项目一句话

- **路径**: `/usr/local/LsmTokensServer/LsmTokensServer`
- **语言**: Go 1.22+（标准库 `net/http` + GORM + MySQL）
- **版本**: v2.0.0
- **定位**: AI 统一中转站服务 + AI 信息爬虫 MCP 服务
- **核心**: 模型级 API Key + 内存缓存 + 智能路由 + 源站级协议算法 + MySQL 哈希分表

## 2. 服务端口

| 服务 | 端口 | 协议 | 说明 |
|------|------|------|------|
| Agent Proxy | `29000` | HTTP | AI API 代理入口 |
| Manager Web | `9101` | HTTP | 管理员后台 |
| User Web | `29001` | HTTP/HTTPS | 用户门户（`userWebUseHTTPS=true` 启用 TLS） |
| **Spider MCP** | **`29002`** | HTTP | 爬虫 MCP 服务（v2.0.0 chromedp 模式） |

## 3. 强制规则（高频）

1. **禁止**直接 `go build` / `nohup ./LsmTokensServer` / `./LsmTokensServer -d`；必须用 `./rebuild_restart_app.sh`
2. 修改 Go 代码后**必须** `gofmt -w`，测试失败必须先修复
3. 单个 `.go` 文件不超过 **1500 行**；接近上限按 `*_styles.go` / `*_body.go` / `*_scripts.go` 拆分
4. Web 页面 `href` / `src` / `fetch()` / `action` 全部**相对路径**
5. 修改 `server_web_*.go` / HTML 模板 / CSS / JS **必须调用前端 SubAgent**
6. 数据库索引写在 GORM model tag / AutoMigrate，分表用 `index:,composite:<id>,priority:n`
7. 协议转换器必须同步转换 **Body + Header + API 路径**
8. v2.0.0 爬虫服务是 **chromedp 单模式**（HTTP 抓取已彻底移除）
9. Agent 拿到 `/SpiderWebData` 响应后**必须显式调用** `/InputSpiderDailyInfo` 保存

## 4. 关键约束

- **LsmInterServer 不可修改**: `/usr/local/LsmInterServer/` 是独立项目代码，所有 8080 代理适配必须在 LsmTokensServer 侧完成
- **API Key 归属模型级**: `TAgentHttpUserModelInfo.api_key`，添加模型时若 `api_key` 为空系统自动生成
- **代理热路径零 DB**: `handleAIProxyRequest` 中 API Key → UserModel → User → Route → Endpoint 全走 `AgentCache`，禁止 MySQL 查询
- **协议算法 v1.3.0**: 路由源站 `dst_endpoint_algorithm_type_list`（`1=协议直连` / `2=协议转换器`）；`NormalizeDstEndPointAlgorithmTypeList` 自动补齐
- **请求头脱敏边界**: `RequestHeaders` / `RequestSrcProtocolHeaders` 写库保留原始值；读取后在**后端**脱敏 `Authorization: Bearer ...` → `Authorization: Bearer ************************`
- **分表规则**: `fnv32a(userName + "_" + modelName) % subTableNumber` 路由到 `TAgentHttpTransactionDataItem_XX`
- **分表字段**: 必须存 `UserName` / `ModelName` / `APIKey` / `DstEndPointID` / `ProtocolType`；`protocol_type` 字段 `1=Anthropic, 2=OpenAI`
- **缓存一致性**: CRUD 事务成功后必须调用 `addXxxToCache` / `invalidateXxxCache` / `updateXxxInCache` / `removeXxxFromCache`；路由用**精确**增删改，**禁止** `invalidateRouteCache` 粗粒度清除
- **返回首页**: 所有页面用 `goHome()` 函数，确保反向代理路径前缀场景下正确导航
- **JS URL 构造**: `apiUrl(path)` 用 `pathname.substring(0, pathname.lastIndexOf('/') + 1) + path`，**禁止** `new URL('../' + path, location.href).href`
- **模型唯一性**: `TAgentHttpUserModelInfo` 联合唯一索引 `idx_user_model (user_id + model_name)`
- **用户管理模型列表分析按钮**: 浏览记录（紫 `#6f42c1`）/ 统计（绿 `#28a745`）/ Session（蓝 `#17a2b8`）/ Task（黄 `#ffc107`）
- **ChatAnalysis 无参数**: `/ChatAnalysis` 无 `user_name` / `model_name` 时重定向到 `./`
- **用户分析 API 安全规则**: 用户端分析 API 强制使用 JWT Claims 中的用户名；必须校验模型归属
- **enrichRoute protocol_type**: 必须是路由自身协议类型（`route.ProtocolType`），不得覆盖为源站协议
- **DeleteAIRoute 缓存安全**: 删除前必须查询校验，拿到准确 `UserModelID` 再调用 `removeRouteFromCache`
- **路由添加页面**: 模型下拉不过滤已配满模型；显示所有模型并标记 `[已配满]`
- **路由编辑功能**: `/AIRouteManage` 列表支持编辑；编辑时用户/模型/协议只读
- **首页服务端口显示**: 管理员首页服务信息区域显示三个端口
- **协议转换分析器**: `/ProtocolConvertAnalyzer` 管理端/用户端记录表必须展示 `ID` 列；选中只填 Input（四区块）并清空 Output；方向由 `protocol_type` 自动确定只读；只有点击 `执行转换` 才调用接口填 Output
- **ChatAnalysis 性能**: 首屏默认 page=1，不恢复 localStorage 深分页，避免大 OFFSET 慢查询
- **ChatAnalysis 模板拆分**: `server_web_manager_chat_page_html.go` 保留 `agentPageTemplate` 组合入口；CSS/HTML/JS 拆到 `server_web_manager_chat_page_{styles,body,scripts}.go`；筛选/分页 localStorage、时间跨度、用户端分页回退逻辑不可删除
- **测试日志**: `disableUserLog` 全局开关 + `isTestLogEntry` 自动 fixture 识别（防止测试代码污染 `LsmTokensServerUsersInfo.log`）

## 5. Session / Task 定义

- **Session**: 同一 `remote_addr` 的连续请求，时间间隔 < 5 分钟属于同一 Session
- **Task**: Session 内，POST 请求到 `/messages` (Anthropic) 或 `/chat/completions` (OpenAI) 端点

## 6. 缓存层说明

`mysql_http_agent_cache.go` 提供线程安全的内存缓存（`sync.RWMutex`）：

| 缓存 | 类型 | 用途 |
|------|------|------|
| `users` | `map[uint64]*TAgentHttpUserInfo` | 按 ID 查用户 |
| `usersByName` | `map[string]*TAgentHttpUserInfo` | 按用户名查用户 |
| `models` | `map[uint64]*TAgentHttpUserModelInfo` | 按 ID 查模型 |
| `modelsByKey` | `map[string]*TAgentHttpUserModelInfo` | **代理热路径**，按 API Key 查模型 |
| `modelsByUserModel` | `map[string]*TAgentHttpUserModelInfo` | **分析热路径**，按 `userName:modelName` 查模型 |
| `routes` | `map[uint64][]*TAgentHttpAIRoute` | 按模型 ID 查路由列表 |
| `endpoints` | `map[uint64]*TAgentDstEndPoint` | 按源站 ID 查源站（代理热路径） |

启动时 `main.go` 调用 `LoadAgentCacheFromDB()` 加载全量数据。后续增删改通过 `addUserToCache` / `invalidateUserCache` 等函数增量维护。路由增删改用 `addRouteToCache` / `updateRouteInCache` / `removeRouteFromCache` **精确**维护，**禁止**用 `invalidateRouteCache` 粗粒度清除。

## 7. v2.0.0 爬虫 MCP 关键约束

- **服务地址**: `http://localhost:29002`
- **三接口**: `/SpiderWebData`（爬取）/ `/GetSpiderDataSource`（数据源）/ `/InputSpiderDailyInfo`（保存）
- **8 个 action**: `navigate` / `click` / `scroll` / `scroll_to` / `fill_form` / `extract` / `screenshot` / `get_state` 全部用 chromedp 实现
- **Chrome 进程生命周期**: 服务进程内嵌启动 headless Chrome（端口 9222）+ 30s 健康检查 + 异常自动重启
- **session TTL**: 10 分钟（`spiderSessionTTL`）；过期由 `sessionCleanupLoop` 清理
- **per-session Chrome tab**: 每个 `SpiderSession` 独立 tab
- **选择器语法**: `.class` / `#id` / `text:keyword` / `xpath:expr` / `tag`
- **screenshot 响应**: `screenshot` action 返回 base64 PNG
- **必调 `/InputSpiderDailyInfo`**: v2.0.0 已移除自动保存，Agent 拿到 `/SpiderWebData` 响应后必须显式保存
- **HTTP 抓取已彻底移除**: Chrome 不可用时直接 `success=false`
- **代码组织**: `mcp_interface_*.go`（接口）+ `spider_cdp_*.go`（chromedp 引擎）+ `mysql_spider_model.go`（数据模型 + 分表）
- **v1.x 历史文件已删除**: `server_mcp_spider_description.go` / `server_mcp_spider_translate.go` / `server_mcp_spider_pipeline.go` 已合并到 `mcp_interface_common.go`（pipeline 内的内容提取部分）

## 8. 测试清单（v2.0.8 完整清单）

保留 19 个核心测试文件：

**核心代理**:
- `test_api_proxy_test.go`（代理主链路转发 + initTestEnv 初始化）
- `test_api_crud_test.go`（用户/模型/源站/路由 CRUD）
- `test_api_transactions_test.go`（事务存储/查询/UTF-8）
- `test_proxy_logic_test.go`（代理单元函数）
- `test_request_tools_parsing_test.go`（`parseRequestToolsFromBody` 核心）
- `test_header_redaction_test.go`（Bearer 脱敏）

**协议转换**:
- `test_protocol_converter_request_test.go`（请求转换 26 测试）
- `test_protocol_converter_response_test.go`（响应转换）
- `test_protocol_converter_error_test.go`（错误响应）
- `test_protocol_converter_learning_test.go`（学习器）

**Session/Agent 识别**:
- `agent_algorithm_session_recognition_test.go`（Session 识别层 v2.0.8）
- `agent_tool_session_recognition_test.go`（OpenClaw Agent 工具 Session 识别）
- `test_agent_tool_recognition_test.go`（AI Agent UA 识别）

**经济型算法**:
- `agent_algorithm_economic_test.go`（负载均衡 25 测试）

**MCP/Spider**:
- `mcp_interface_common_test.go`（元素提取）
- `mcp_interface_spiderwebdata_test.go`（反爬截断）
- `spider_anti_bot_test.go`（UA 池 / Header Bundle）
- `spider_cdp_actions_test.go`（CDP 解析层）
- `spider_cdp_integration_test.go`（CDP E2E，需 `LSMSpiderCDPIntegration=1`）

**已删除**（v2.0.0 清理）:
- ~~`test_find_openai_agent_unknown_tool_test.go`~~
- ~~`test_find_opencode_unknown_tool_test.go`~~
- ~~`test_request_tools_backfill_audit_test.go`~~
- 这 3 个是带 `t.Skip` 的生产库审计脚本（~870 行），已合并到运维工具集，未来如有需要可建 `cmd/audit/`
