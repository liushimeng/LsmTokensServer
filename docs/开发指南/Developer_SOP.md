# Developer_SOP.md - LsmTokensServer 开发标准操作规程

## 1. 项目定位

LsmTokensServer 是 Lsm AI Tokens 代理服务（AI Relay）+ AI 信息爬虫 MCP 服务。通过单端口接收 Anthropic / OpenAI 协议的 API 请求，基于**模型级 API Key** 识别用户和模型，按源站级协议算法（v1.3.0：`1=协议直连` / `2=协议转换器`）转发到目标源站，记录完整请求响应到 MySQL 哈希分表。v2.0.3 爬虫 MCP 服务（chromedp 单模式）：稳定性增强、反爬能力优化、SpiderDataSource 折叠交互。

## 2. 技术栈

| 类型     | 技术                                          |
| -------- | --------------------------------------------- |
| 语言     | Go 1.26+                                      |
| Web 框架 | 标准库 `net/http`                             |
| ORM      | GORM 1.31.1 + MySQL Driver                    |
| 浏览器   | chromedp + headless Chrome（v2.0.0 爬虫）     |
| 前端     | React 19 + Vite（`dist-manager`/`dist-user` 双构建，`__APP_ROLE__` 门控） |
| 架构     | 后端单一二进制 + 前端双构建产物，配置驱动，守护进程管理 |

## 3. 数据库设计规范

### 3.1 唯一性约束

| 维度      | 约束规则                                       | 实现方式                                                            |
| --------- | ---------------------------------------------- | ------------------------------------------------------------------- |
| UserName  | **全平台唯一**                                 | `TAgentHttpUserInfo.user_name` 字段 `unique` 索引                   |
| ModelName | **用户维度唯一**（同一用户下模型名称不可重复） | `TAgentHttpUserModelInfo` 联合唯一索引 `uniqueIndex:idx_user_model` |
| API Key   | **全平台唯一**                                 | `TAgentHttpUserModelInfo.api_key` 字段 `unique` 索引                |
| Phone     | **全平台唯一**（可选 7-20 位数字）             | `TAgentHttpUserInfo.phone` 字段 `unique` 索引                       |

### 3.2 分表存储规范

- **分表规则**: `fnv32a(userName + "_" + modelName) % subTableNumber`
- **表名格式**: `TAgentHttpTransactionDataItem_XX` (XX = 00 ~ subTableNumber-1)
- **路由键**: `UserName + ModelName` 组合可唯一定位到目标分表
- **关联字段**: 每条记录必须存储 `UserName` / `ModelName` / `APIKey` / `DstEndPointID` / `ProtocolType`
- **请求头审计与脱敏边界**: `RequestHeaders` / `RequestSrcProtocolHeaders` 写库保留原始值；所有数据库读取路径在返回 Web/API/协议转换分析器前，必须对 `Authorization: Bearer ...` 在**后端**脱敏为 `Authorization: Bearer ************************`。脱敏属于读取/展示层逻辑，不得修改数据库原始记录

### 3.3 索引设计原则

- 高频查询字段必须加索引（user_name, model_name, api_key, dst_endpoint_id, request_method, response_status, elapsed_ms）
- 联合索引优先于单列索引（如 user_name + model_name）
- 缓存热路径字段优先建立内存索引

### 3.4 索引管理规范

- 新增或调整数据库索引必须优先通过 GORM model tag + `AutoMigrate` 管理，禁止在初始化流程中手写 `CREATE INDEX` / `DROP INDEX`
- 分表模型需要复合索引时，使用 `index:,composite:<id>,priority:n`，让 GORM 按实际表名生成索引名，避免 SQLite 全局索引名冲突
- `/ChatAnalysis` 列表查询依赖 `TAgentHttpTransactionDataItem` 上的 `user_name + model_name + created_at + id` 复合索引；不要在 `InitAgentHttpSubTables` 中额外手写同类索引
- 性能排查先用 MySQL `SHOW INDEX` / `EXPLAIN` 看真实执行计划，再决定调整

## 4. 内存缓存设计规范

`mysql_http_agent_cache.go` 提供线程安全的内存缓存（`sync.RWMutex`）：

| 缓存                  | 类型                                  | 用途                                           |
| --------------------- | ------------------------------------- | ---------------------------------------------- |
| users                 | `map[uint64]*TAgentHttpUserInfo`      | 按 ID 查用户                                   |
| usersByName           | `map[string]*TAgentHttpUserInfo`      | 按用户名查用户                                 |
| models                | `map[uint64]*TAgentHttpUserModelInfo` | 按 ID 查模型                                   |
| modelsByKey           | `map[string]*TAgentHttpUserModelInfo` | **代理热路径**，按 API Key 查模型              |
| modelsByUserModel     | `map[string]*TAgentHttpUserModelInfo` | **分析热路径**，按 `userName:modelName` 查模型 |
| routes                | `map[uint64][]*TAgentHttpAIRoute`     | 按模型 ID 查路由列表                           |
| endpoints             | `map[uint64]*TAgentDstEndPoint`       | 按源站 ID 查源站（代理热路径）                 |
| agentTools            | `map[string]*TAgentHttpAgentInfo`     | 按 `agent_tool_name` 查 AI Agent 统计          |
| spiderDataSources     | `map[uint64]*TSpiderDataSource`       | 按 ID 查爬虫数据源                             |
| spiderDataSourcesList | `[]uint64`                            | 数据源 ID 列表（按更新时间）                   |

### 4.1 缓存一致性

- 所有 CRUD 操作在数据库写入后**必须**调用对应的缓存失效/更新函数
- 新增: `addUserToCache` / `addModelToCache` / `addRouteToCache` / `addDstEndPointToCache` / `addModelInfoToCache`
- 更新: `updateRouteInCache` / `updateModelInfoInCache`（路由/模型信息）
- 删除: `invalidateUserCache` / `invalidateModelCache` / `removeRouteFromCache` / `invalidateDstEndPointCache` / `removeModelInfoFromCache`
- **路由**增删改用**精确**增删改函数，**禁止**用 `invalidateRouteCache` 粗粒度清除（避免同一模型多路由短暂全部失效的窗口期）
- 启动时: `LoadAgentCacheFromDB()` 加载全量数据

## 5. 前端路径规范

### 5.1 强制规则

> **所有链接、表单 action、JS fetch 请求必须使用相对路径**，禁止以 `/` 开头的绝对路径。

**原因**: 代理服务可能监听与 Web 管理相同的域名或端口，绝对路径的请求可能被代理服务拦截转发，导致返回 HTML 而非预期 JSON。

### 5.2 路径示例

| 场景                      | 正确做法                                       | 错误做法                        |
| ------------------------- | ---------------------------------------------- | ------------------------------- |
| 从 `/UserManage` 返回首页 | `href="javascript:void(0)" onclick="goHome()"` | `href="/"`                      |
| 从 `/UserManage` 调用 API | `fetch('../UserManageInterface')`              | `fetch('/UserManageInterface')` |
| 同页面内跳转              | `href="./ChatAnalysis?..."`                    | `href="/ChatAnalysis?..."`      |

### 5.3 ProtocolConvertAnalyzer 页面规范

- 管理端和用户端 `/ProtocolConvertAnalyzer` 布局必须保持一致
- 记录列表必须展示 `ID` 列，新增/删除列时同步调整空态或详情行 `colspan`
- 选择记录时只填充 Input 四区块（Request Header、Request Body、Response Header、Response Body），Output 必须保持为空
- `转换方向` 由记录 `protocol_type` 自动确定并只读展示，不能让用户手动选择反方向
- 只有点击 `执行转换` 后才调用转换接口并填充 Output
- **筛选条件必须 localStorage 持久化**：`用户筛选` / `模型筛选` / `数据来源` / `时间范围` 四个控件在 Chrome 浏览器端通过 `window.localStorage` 自动保存。管理端 key `lsm_protocol_converter_filters`、用户端 key `lsm_protocol_converter_user_filters`，与 `lsm_*` 命名空间保持一致。URL 参数（`user_name` / `model_name` / `protocol_type` / `days`）优先级高于 localStorage，便于分享深链接覆盖缓存
- **协议转换率分析依赖后端完整指标**：「结构转换成功率 (`structure_success_rate`)」与「字段转换率 (`field_conversion_rate`)」由后端 `protocol_converter.go` 的 `CalculateConversionMetricsForSection` 实际执行转换后计算，前端只负责展示。`ConvertProtocolRequestBody` / `ConvertProtocolResponseBody` 必须返回 `CalculateConversionMetricsForSection` 的完整结果；如果改用 `calculateBasicMetrics`（仅返回基础三项），会导致结构/字段转换率始终为 0%
- 回归测试：`TestConvertProtocolRequestBody_MetricsIncludesStructureAndFieldRates` / `TestConvertProtocolResponseBody_MetricsIncludesStructureAndFieldRates` / `TestConvertProtocolRequestBody_MetricsAnthropicToOpenAI`

### 5.4 ChatAnalysis 页面模板拆分规范

`/ChatAnalysis` 管理端和用户端共用 `agentPageTemplate`，模板按功能拆分：

| 文件                                      | 职责                                                               |
| ----------------------------------------- | ------------------------------------------------------------------ |
| `server_web_manager_chat_page_html.go`    | 组合入口，保留 `agentPageTemplate`                                 |
| `server_web_manager_chat_page_styles.go`  | 页面 CSS (`agentPageStyles`)                                       |
| `server_web_manager_chat_page_body.go`    | 页面主体 HTML、筛选表单、结果容器 (`agentPageBodyHTML`)            |
| `server_web_manager_chat_page_scripts.go` | 页面 JS、localStorage、筛选、分页、详情懒加载 (`agentPageScripts`) |

拆分或修改时必须保持 `template.New(...).Parse(...)` 与 `tmpl.Execute` 行为不变，保留 `{{template "sharedDisplayJS"}}` 在页面专属脚本之前。筛选条件（含时间跨度）和分页状态必须继续使用 localStorage 恢复，URL 参数优先。

### 5.5 goHome 函数规范

所有页面必须提供统一的 `goHome()` 函数：

```javascript
function goHome() {
    var pathname = window.location.pathname;
    var idx = pathname.lastIndexOf('/');
    if (idx > 0) {
        window.location.href = pathname.substring(0, idx + 1);
    } else {
        window.location.href = './';
    }
}
```

## 6. 编码规范

### 6.1 Go 编码规范

1. **格式化**: 必须使用 `gofmt` 或 `goimports` 格式化代码
2. **错误处理**: 必须进行严密的错误处理，不允许使用 `_` 忽略关键错误
3. **变量命名**: 使用驼峰命名法，导出变量首字母大写，私有变量小写
4. **注释**: 每个导出函数必须有注释，复杂逻辑需要注释说明
5. **导入分组**: 标准库分组，第三方分组，本地模块分组
6. **测试文件**: `*_test.go` 命名，测试函数 `Test` 前缀，与功能代码同包

### 6.2 错误处理示例

```go
// 正确做法：检查并处理错误
if err != nil {
    logger.Printf("[ERROR] Failed to do something: %v", err)
    return err
}

// 错误做法：忽略关键错误
// result, _ := SomeFunction() // 禁止这样做
```

### 6.3 代码行数限制

> **任何 Go 源文件代码行数不得超过 1500 行。**

- 单元测试文件除外
- 接近限制时按功能模块拆分
- 前端大模板优先采用组合入口 + `*_styles.go` / `*_body.go` / `*_scripts.go`，参考 `server_web_manager_chat_page_html.go` 和 `server_web_common_toolbar.go`
- 推荐单文件控制在 500-800 行以内

## 7. 代理核心流程

```
客户端请求 (Anthropic/OpenAI)
    ↓
提取 Authorization: Bearer {API Key}
    ↓
通过 API Key 查询模型（内存缓存优先）→ 获取 UserModelInfo
    ↓
通过 UserID 查询用户（内存缓存优先）→ 校验协议权限
    ↓
解析请求体 JSON → 提取 model 字段
    ↓
查询智能路由（按模型 ID + 协议类型）→ 获取 CachedAIRoute
    ↓
算法选择器选择目标源站（指定型/稳定型/经济型/智能型）→ 获取 endpoint ID
    ↓
查询目标源站信息（内存缓存优先）→ 获取源站 URL + API Key
    ↓
检查源站是否被禁用（TAgentDstEndPoint.Status == 0）→ 禁用则返回 403 Forbidden
    ↓
（协议转换器）按 AlgorithmType 决定：1=协议直连 / 2=协议转换器
    ↓
替换 Authorization 为源站 API Key
替换请求体 model 为源站模型名称
    ↓
转发到目标源站 → 透传响应
    ↓
（稳定型）遇到 429/500/502/503/504/连接错误 → 自动切换下一个源站重试
    ↓
异步记录请求/响应到哈希分表
```

## 8. 测试规范

| 文件类型                                      | 覆盖范围                                                                          | 依赖                                    |
| --------------------------------------------- | --------------------------------------------------------------------------------- | --------------------------------------- |
| `test_api_proxy_test.go`                      | 代理主链路转发 + initTestEnv 初始化                                               | SQLite 内存库 + `initTestEnv`           |
| `test_api_crud_test.go`                       | 用户/模型/源站/路由 CRUD                                                          | SQLite 内存库 + `initTestEnv`           |
| `test_api_transactions_test.go`               | 事务存储/查询/UTF-8                                                               | SQLite 内存库 + `initTestEnv`           |
| `test_proxy_logic_test.go`                    | 纯函数：extractAPIKey / parseModelFromBody / replaceModelInBody / getRelativePath | 无                                      |
| `test_request_tools_parsing_test.go`          | `parseRequestToolsFromBody` 核心                                                  | 无                                      |
| `test_header_redaction_test.go`               | Bearer 脱敏                                                                       | 无                                      |
| `test_agent_tool_recognition_test.go`         | AI Agent UA 识别                                                                  | 无                                      |
| `test_protocol_converter_request_test.go`     | 协议转换请求（26 测试）                                                           | 无                                      |
| `test_protocol_converter_response_test.go`    | 协议转换响应                                                                      | 无                                      |
| `test_protocol_converter_error_test.go`       | 错误响应转换                                                                      | 无                                      |
| `test_protocol_converter_learning_test.go`    | 协议转换学习器                                                                    | 无                                      |
| `agent_algorithm_session_recognition_test.go` | Session 识别层（v2.0.8）                                                          | 无                                      |
| `agent_tool_session_recognition_test.go`      | OpenClaw Agent 工具 Session 识别                                                  | 无                                      |
| `agent_algorithm_economic_test.go`            | 经济型算法负载均衡                                                                | 无                                      |
| `mcp_interface_common_test.go`                | MCP 元素提取                                                                      | 无                                      |
| `mcp_interface_spiderwebdata_test.go`         | SpiderWebData 反爬截断                                                            | 无                                      |
| `spider_anti_bot_test.go`                     | UA 池 / Header Bundle                                                             | 无                                      |
| `spider_cdp_actions_test.go`                  | CDP 解析层（选择器 / URL / text JS）                                              | 无                                      |
| `spider_cdp_integration_test.go`              | CDP 引擎 E2E                                                                      | 需 `LSMSpiderCDPIntegration=1` + Chrome |

- 单元测试：不依赖外部服务，直接调用被测函数
- 集成测试：使用 SQLite 内存数据库模拟 MySQL，使用 `httptest` 模拟 HTTP 服务端点
- 所有测试必须能通过 `go test ./...` 执行
- 测试环境初始化函数统一命名为 `initTestEnv(t *testing.T)`，返回清理函数
- **不要**把一次性运维脚本（生产库审计/修复）放进 `*_test.go`，应单独建 `cmd/audit/` 或 `scripts/`

## 9. 运维规范

### 9.1 服务重启规则

> LsmTokensServer 是 Claude Code / Kilo Code / OpenCode / pi / OpenClaw 等 AI IDE 的网络代理依赖，直接重启会中断正在进行的 AI 对话。

#### 核心原则：动态配置零重启管理

- 新增/修改/删除用户、源站、路由：访问 Web 管理页面，**无需重启**，实时生效
- 禁止为了修改代理配置而重启整个服务

#### 代码升级/二进制替换重启流程

**必须**使用 `./rebuild_restart_app.sh` 滚动重启（脚本内部完成新实例启动 + 端口验证 + 旧实例停止）：

```bash
# 完整重启（编译 + 启动新实例 + 验证端口 + 停止旧实例）
./rebuild_restart_app.sh
```

**禁止的操作**：
- 手动 `go build` / `nohup ./LsmTokensServer &` / `./LsmTokensServer -d`
- **禁止**给 `rebuild_restart_app.sh` 带 `--build-only`、`--skip-web` 等参数（完整重启即可）。
- 在 AI IDE 正在长对话或流式响应过程中重启
- 不验证新实例就停止旧实例

## 10. 前端 SubAgent 规则（MOE 架构对抗）

### 10.1 背景

大模型 **MOE（Mixture of Experts）架构**下，不同专家模块对前端代码（HTML/CSS/JS）的理解深度和生成一致性存在差异。在 Go 语言项目中，前端代码以内联模板字符串形式存在于 `.go` 文件中，MOE 架构可能导致：

- **样式冲突**：不同专家生成的 CSS 选择器覆盖顺序不一致
- **DOM 结构不一致**：页面模板中 `<body>` / `<html>` 标签重复或缺失
- **JS 事件绑定丢失**：相同功能在不同页面实现不一致
- **模板拼接顺序错误**：`Parse()` 调用中模板片段顺序错误导致渲染异常
- **响应式断点混乱**：不同页面使用不同的 `@media` 断点值

### 10.2 触发条件

**当满足以下任一条件时，必须调用前端 SubAgent：**

1. 修改包含 HTML 模板字符串的 `.go` 文件（`server_web_*.go`）
2. 修改 CSS 样式（内联 `<style>` 或模板样式常量）
3. 修改 JavaScript 逻辑（内联 `<script>` 或模板脚本常量）
4. 新增/删除 Web 页面路由或页面模板
5. 修改共享前端组件（`server_web_common_*.go`）

### 10.3 SubAgent 检查清单

| 检查项        | 说明                                  | 通过标准                                                     |
| ------------- | ------------------------------------- | ------------------------------------------------------------ |
| 模板拼接顺序  | `Parse()` 中模板片段顺序              | `sharedPageHead + headerToolbarHTML + navHTML + pageContent` |
| DOM 完整性    | 页面模板包含完整的 `<body>` 内内容    | 标签正确闭合，无重复 `<body>`                                |
| CSS 冲突      | 全局选择器与共享样式冲突              | 页面 CSS 使用 scoped 类名                                    |
| 相对路径      | `href` / `src` / `fetch()` / `action` | 禁止以 `/` 开头的绝对路径                                    |
| Sticky 一致性 | `.header-bar` / `.nav-bar` 定位       | `top` 值与共享样式匹配                                       |
| MOE 一致性    | 相同功能在不同页面实现                | `goHome()` / `getApiUrl()` / `logout()` 代码一致             |
| 响应式断点    | `@media` 查询断点值                   | 统一使用 `768px`                                             |

### 10.4 交互流程

```
主 Agent 识别前端修改
  → 调用前端 SubAgent
  → SubAgent 执行检查
  → 返回检查报告
  → 主 Agent 确认/修复
  → 继续主流程（gofmt → go test ./... → ./rebuild_restart_app.sh）
```

## 11. 功能扩展原则

1. 评估当前文件行数，如果接近 1500 行，先拆分模块
2. 按照功能职责拆分到不同的包文件
3. 保持每个文件行数在限制以内
4. 测试文件与功能代码同包，使用 `*_test.go` 命名
5. **不要把一次性运维脚本（生产库审计/修复）放进 `*_test.go`**；应放 `cmd/audit/` 或 `scripts/`
6. 任何架构变更（v2.0.8 Session 识别重构、v2.0.0 chromedp 重构、v1.3.0 协议转换器、v1.5.0 内存数据库规则等）必须更新 `CLAUDE.md` / `KILO.md` / `AGENT.md` / `Developer_SOP.md` / `AGENT_INDEX.md`
