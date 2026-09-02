# KILO.md - LsmTokensServer Kilo Code 规范

> **加载顺序**: Kilo Code 先读本文件，再按需读 [`AGENT.md`](AGENT.md)（通用规则）、[`Developer_SOP.md`](Developer_SOP.md)（详细 SOP）、[`AGENT_INDEX.md`](AGENT_INDEX.md)（源码索引）。本文件只保留 Kilo Code / VS Code 特定约束。
> **版本历史**: 完整版本历史见 [`CHANGELOG.md`](../历史归档/CHANGELOG.md)，强制规则归档见 [`CLAUDE.md`](../../CLAUDE.md)。

**当前版本**: v2.0.77（详见 [`CLAUDE.md`](../../CLAUDE.md)）

---

## 🕷️ MCP 爬虫服务（v2.0.0）

服务地址 `http://localhost:29002`。Agent 仍必须显式调用 `/InputSpiderDailyInfo` 保存数据。

| 文档 | 用途 |
|------|------|
| [`Mission_Spider_MCP_Proc.md`](../mcp/Mission_Spider_MCP_Proc.md) | **首先阅读**：Agent 任务流程 |
| [`MCP_SpiderWebData_def.md`](../mcp/MCP_SpiderWebData_def.md) | `/SpiderWebData` 详细定义 |
| [`MCP_GetSpiderDataSource_def.md`](../mcp/MCP_GetSpiderDataSource_def.md) | `/GetSpiderDataSource` |
| [`MCP_InputSpiderDailyInfo_def.md`](../mcp/MCP_InputSpiderDailyInfo_def.md) | `/InputSpiderDailyInfo` |

实现文件（v2.0.0 重构后）：
- `mcp_interface_common.go` - 共享类型 + 会话管理 + 内容提取
- `mcp_interface_spiderwebdata.go` / `mcp_interface_getspiderdatasource.go` / `mcp_interface_inputspiderdailyinfo.go` - 三接口
- `spider_cdp_*.go` - Chrome CDP 引擎（v2.0.0 新增）
- `openclaw_client.go` - OpenClaw 本地 SSE 客户端（v2.0.4）
- `server_web_spider_crawl.go` - `/SpiderDataSourceCrawl` SSE 端点 + 爬取模态框（v2.0.4）

详细规范、HTTPS 配置、Python 工具、协议转换分析器等内容统一在 [`AGENT.md`](AGENT.md)。

---

## 1. Kilo Code 必须遵守的强制规则

### 编译 / 测试 / 重启

- **禁止**直接 `go build` / `nohup ./LsmTokensServer` / `./LsmTokensServer -d`
- 必须通过：

```bash
go test ./...
gofmt -w <修改的 .go>
./rebuild_restart_app.sh                # 完整重启（编译 + 运行）
```

- **禁止**给 `rebuild_restart_app.sh` 带 `--build-only`、`--skip-web` 等参数（完整重启即可）。
- 修改 Go 文件后必须 `gofmt -w`
- 测试失败必须先修复再编译重启
- 配置变更（用户/模型/源站/路由）通过 Web 管理页实时生效，通常无需重启

### 运行保护

LsmTokensServer 是 Kilo Code / Claude Code / OpenCode / pi / OpenClaw 等 AI IDE 的网络代理依赖，重启会中断长对话或流式响应。

- 仅代码变更或必须重载二进制时重启
- 必须使用 `./rebuild_restart_app.sh` 的滚动重启和验证流程
- 不要为了修改代理配置而重启服务

### 前端修改必须调用前端 SubAgent

修改 HTML 模板、CSS、JavaScript、Web 路由、`server_web_common_*.go` 共享前端组件时**必须**先调用前端 SubAgent。

检查重点：模板拼接顺序、DOM 完整性、CSS 作用域、相对路径、sticky 定位、MOE 一致性、响应式断点。详细检查表见 `AGENT.md` §4 / `Developer_SOP.md` §10。

**Web UI 关键约定（高频）**：
- 管理员 `/AIRouteManage` 路由操作按钮：相对路径 `./ChatDialog?user_name=...&model_name=...` 等
- 用户 `/AIRouteManage` 只传 `model_name`（用户由 JWT 确定）
- `/AIRouteManage` 路由编辑：`dst_endpoint_algorithm_type_list` 必须与 `dst_endpoint_id_list` 一一对应（`1=协议直连` / `2=协议转换器`）
- `/ChatAnalysis` 首屏默认 page=1（不恢复 localStorage 深分页）；筛选 + 分页用 localStorage 同步 URL；时间跨度默认 3 天，支持 `全部` + 1/3/5/7/14/30/60/90 天
- `/ProtocolConvertAnalyzer` 管理/用户端布局一致：列表含 ID 列；选中只填 Input、清空 Output；`转换方向` 由记录 `protocol_type` 自动确定只读；4 项筛选用 localStorage 持久化（`lsm_protocol_converter_filters` / `lsm_protocol_converter_user_filters`），URL 参数优先；`结构转换成功率`/`字段转换率` 由后端 `CalculateConversionMetricsForSection` 实际转换后计算
- 启用按钮避免黑/灰背景；灰色只用于 disabled/只读/状态标签
- `/ChatAnalysis` 浏览记录页拆分为 `server_web_manager_chat_page_{html,styles,body,scripts}.go`；`agentPageTemplate` 是组合入口
- 新增/调整数据库索引写在 GORM model tag / AutoMigrate 中；分表用 `index:,composite:<id>,priority:n`；性能排查先用 MySQL `SHOW INDEX` / `EXPLAIN`

## 2. Kilo Code / VS Code 协作上下文

Kilo Code 作为 VS Code AI 扩展运行，其网络请求通过 LsmTokensServer 转发：

1. **API Key 映射**：Kilo Code API Key → 用户模型
2. **模型替换**：用户模型名 → 源站模型名
3. **智能路由**：按模型/协议/算法选源站；v1.3.0 起每个目标源站维护 `dst_endpoint_algorithm_type_list`
4. **请求日志**：记录到 MySQL 哈希分表，`/ChatAnalysis` 展示
5. **请求头脱敏**：写库保留原始值，从 DB 读出后**后端**正则脱敏 `Authorization: Bearer ...` → `************************`；禁止前端再脱敏
6. **AI Agent 识别**：从 User-Agent 识别 Kilo Code 写入 `TAgentHttpAgentInfo`

VS Code 工作流建议：
- 修改前先看 `AGENT_INDEX.md` 定位模块
- 后端改动优先跑相关 Go 测试
- 前端改动用浏览器/DevTools/CDP 工具验证页面交互和 Console 错误
- 涉及 Web 页面链接、fetch、form action 时只用相对路径

## 3. 常用入口

| 目标 | 文件 / 命令 |
|------|-------------|
| 通用规则 | `AGENT.md` |
| 详细 SOP | `Developer_SOP.md` |
| 完整源码索引 | `AGENT_INDEX.md` |
| 完整版本历史 | `CHANGELOG.md` |
| 管理端 | `server_web_manager.go`，默认端口 `9101` |
| 用户端 | `server_web_user.go`，默认端口 `29001`（支持 HTTPS） |
| 代理核心 | `server_http_ai_proxy.go`，默认端口 `29000` |
| 爬虫 MCP | `mcp_interface_*.go` + `spider_cdp_*.go`，默认端口 `29002` |
| Session 识别 | `recognizer_session_id.go`（v2.0.16 合并） |
| 数据分表 | `mysql_http_agent_sub_table.go` |
| 内存缓存 | `mysql_http_agent_cache.go` |
| 协议转换 | `protocol_converter.go` |
| Agent 识别 | `recognizer_agent_name.go` |

## 4. AI 信息爬虫（v1.4.0+ / 接口 v2.0.0 / chromedp 单模式）

Kilo Code 开发环境下的爬虫功能模块：管理员/用户双端 UI（`/SpiderDataSource`、`/SpiderDailyInfo`）、Chrome CDP 单模式（v2.0.0 移除 HTTP 回退）、按月分表存储。

### 4.1 核心文件速查

| 任务 | 文件 |
|------|------|
| 数据模型 | `mysql_spider_model.go` |
| 爬虫引擎 | `spider_cdp_browser.go` / `spider_cdp_engine.go` / `spider_cdp_actions.go` / `spider_cdp_session.go` / `spider_cdp_selectors.go` |
| 调度器 | `spider_scheduler.go` |
| API 接口 | `server_api_spider.go` |
| 数据源页面 | `server_web_spider_data_source.go` |
| 每日信息页面 | `server_web_spider_daily_info.go` |
| 管理员导航 | `server_web_common_nav_admin.go` |
| 用户导航 | `server_web_common_nav_user.go` |
| **MCP 服务（v2.0.0）** | `mcp_interface_*.go` + `spider_cdp_*.go` |

### 4.2 关键约束

- 修改 `server_web_spider_*.go` 页面模板时必须调用前端 SubAgent
- 模板拼接顺序：`sharedPageHead + headerToolbarHTML + navHTML + pageHTML + toolbarStyles + toolbarScripts`
- 模板条件语句**不能**嵌入在 HTML 属性中间，应整个属性条件渲染
- 页面链接全部使用相对路径
- `data_source.description` 是 Agent 处理爬取内容的依据（去广告/列表/时间窗口/字数截断）
- **SpiderDataSource 折叠交互**：列表记录支持折叠/展开（localStorage 记忆状态）；折叠后隐藏 `描述` 列、保留 `备注` 列，行高变小更紧凑

### 4.3 VS Code 调试建议

调试爬虫功能时优先观察：
1. `LsmTokensServer.log` 中的 `[SPIDER]` 前缀日志
2. Chrome CDP 连接状态和自动恢复日志（`spiderCDPHealthCheckSec=30`）
3. MCP 服务日志中的 `[MCP]` 前缀（`/SpiderWebData` 调用、session 状态、截图）
4. 调度器触发和并发控制日志

## 5. Go Web 调试工具链（go-web-debug-tool）

`go-web-debug-tool` 是基于 Go + chromedp / CDP 的 **HTTP MCP 调试服务**（git 子模块），让 Kilo / Claude Code / OpenCode / pi / OpenClaw 等 AI Agent 通过 5 个 JSON 接口远程驱动真实 Chrome 浏览器做页面浏览、调试、数据采集与自动化操作。

- 默认监听 `http://localhost:28999`，统一信封 `{code, message, data}`
- 5 个 RESTful 接口：`POST /NewChromePage` / `/ControlChromePage` / `/LookChromePageInfo` / `/CloseChromePage` / `/ListChromePages`
- 自动追踪新开标签页 (`target.Created`)，通过 `spawned_page_id` 字段回传
- Console / Network 环形缓冲（各 500 条），Agent 按需摘要
- 反自动化检测（`anti_detect=true`）：注入 `--enable-automation=false` + `navigator.webdriver` shim
- PID 文件 + 守护模式 (`-d`)、优雅停止 (`-u`)、热重启

### 5.1 编译 / 运行

```bash
cd go-web-debug-tool && go build -o GoWebDebugTool .      # 编译
./GoWebDebugTool -d                                        # 守护进程模式启动
./GoWebDebugTool -u                                        # 优雅停止
./rebuild_restart_app.sh                                   # 一键重建并守护重启
```

### 5.2 五个接口 (POST + application/json)

| 接口 | URL | 用途 |
|------|-----|------|
| 新建页面 | `POST /NewChromePage` | 打开一个新的 Chrome 页面 / 标签 |
| 控制页面 | `POST /ControlChromePage` | 在指定 page 上执行 click / input_text / eval_js / scroll / 等 action |
| 查询页面 | `POST /LookChromePageInfo` | 读取 Console / Network / DOM / screenshot / 等 |
| 关闭页面 | `POST /CloseChromePage` | 关闭页面并释放 CDP 上下文 |
| 列出页面 | `POST /ListChromePages` | 枚举当前所有受管页面 |

### 5.3 单页面基础流程

1. `POST /ListChromePages` — Agent 启动 / 失联恢复时先列出现有页面，避免重复打开。
2. `POST /NewChromePage` — `{ "url": "https://example.com" }` → 拿到 `page_id`。
3. `POST /ControlChromePage` 多次循环：click / input_text / eval_js / scroll / screenshot…
4. `POST /LookChromePageInfo` — `info=screenshot / console / network` 取页面状态作为下一步决策依据。
5. 任务结束 `POST /CloseChromePage` 释放资源。

### 5.4 错误码（与 spec 完全一致）

| Code | 含义 | Kilo 推荐动作 |
|------|------|--------------|
| 0    | Success | 正常处理 `data` |
| 1000 | Invalid JSON body | 检查请求体语法，不要重试 |
| 1001 | Missing/invalid parameter | 校对参数表，不要重试 |
| 1002 | Unauthorized | 检查 `auth_token` 与请求头 |
| 2000 | page_id not found | 先调用 `/ListChromePages` 恢复页面索引 |
| 2001 | CDP disconnected / browser unreachable | 等待 1-3 秒后重试；连续 3 次失败后重新 `/NewChromePage` |
| 2002 | Action execution failed（含 per-action 超时） | 检查 selector / 参数，可有限重试；React 受控组件可切 `input_text use_js=true` |
| 2003 | Page crashed | 调用 `/CloseChromePage` 清理，重新 `/NewChromePage` |
| 3000 | Internal server error | 查看服务日志，必要时上报 |

> 不应对 `2001/2003` 无限重试；建议 3 次重连失败后将错误上抛给用户。

### 5.5 反自动化检测 (anti_detect)

为绕过常见 WAF/反爬对 `navigator.webdriver === true` 的判定，默认 `anti_detect=true` 启用三层防护：

1. 启动 flag `--enable-automation=false`：覆盖 chromedp 默认值。
2. 启动 flag `--disable-blink-features=AutomationControlled`：关闭 Blink 的 AutomationControlled 特性。
3. 页面级 `Page.addScriptToEvaluateOnNewDocument` shim：定义 `navigator.webdriver` getter 返回 `false`。

三层互为冗余。需要原始 chromedp 行为时改 `anti_detect=false`。

完整规范：[`go-web-debug-tool/MCP_Proc_Def.md`](../../go-web-debug-tool/MCP_Proc_Def.md)。

---

更详细的服务端口、HTTPS 配置、协议转换器规则、AI Agent 识别、Request Tools 解析、内存数据库规则等内容见 [`AGENT.md`](AGENT.md)。
