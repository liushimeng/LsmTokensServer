# LsmTokensServer 服务代码文件的迁移和重构 — 全面排查与优化方案（2026-08-24 第 02 轮）

> 排查方法：对 `/usr/local/LsmHttpAgent/`（旧）与 `ServerGo/`（新）全部 Go 文件逐文件 diff（`diff -w`）+
> 路由挂载层核对 + 编译验证 + 49000 端口端到端实测。

## 0. 本轮修复的直接故障（已完成）

**现象**：Agent 调用 `http://<host>:49000` 失败，报 `Cannot connect to API: Unable to connect... [retrying in 4s attempt #3]`。

**根因**：不是代码缺陷，是配置端口错位——`LsmTokensServer.conf` 中 `agentListenPort=42900`、`agentHttpsListenPort=42903`，服务实际监听 42900/42903，而 Agent 客户端配置指向 49000/49003，无进程监听导致连接被拒。

**修复**：配置改为 `49000 / 49003`，`./rebuild_restart_app.sh` 重启。验证结果：
- 端口监听：49000 / 49003 / 49101(管理) / 42901(用户) / 42902(MCP) 全部就绪；
- 端到端：`POST http://127.0.0.1:49000/Anthropic/v1/messages`（Bearer 真实 key）→ HTTP 200，模型正常返回，usage 统计字段齐全；
- 与旧 29000 行为比对：无 Authorization 时同样返回 401 JSON，行为一致；
- 旧服务 29000/29003 仍在运行，未受影响。

## 1. 逐文件比对总结论

### 1.1 已完整迁移（逻辑 1:1 等价，diff 仅包名/导出改名）

| 域 | 旧文件 → 新位置 | 结论 |
|---|---|---|
| 调度算法 | agent_algorithm.go / _stable.go / _economic.go → models/ | 指定型（滚动）、稳定型（失败 3 次滚动并联动 RotateAIRouteEndpointList）、经济型（按 token 计费择优+坏源站剔除）逐行等价 |
| 分表 | mysql_http_agent_sub_table.go → models/subtable.go | 建表/AutoMigrate/复合索引/task feature backfill 一致（2682→2685 行） |
| tokens 统计 | mysql_http_agent_tokens.go → models/ | 颗粒度规范化、分表遍历、25s 超时一致 |
| 缓存/清理 | mysql_http_agent_cache.go / _cleanup.go → models/ | 全表预热与增删改同步、每日清理循环、CleanupReport 一致 |
| 全部 manage CRUD | ai_route / dst_endpoint / model_info / model / user / agent_info → models/ | 含批量启停、级联路由状态同步、endpoint→model_info 自动同步 |
| MySQL 连接 | mysql_connect.go → database/connect.go | 连接池/超时一致；唯一差异：gorm 慢 SQL 日志被 io.Discard 静默（见 2.3） |
| 协议转换 | protocol_*.go 6 个 → protocol/ | Anthropic↔OpenAI 请求/响应/SSE/错误体/头，逐行等价 |
| 识别器 | recognizer_*.go → recognizer/ | UA→agent、session_id 识别表、工具名提取一致 |
| AI 代理转发 | server_http_ai_proxy*.go、server_http_agent_proxy.go → proxy/ | extractAPIKey、forwardWithRetry、failover 判定、经济型降级、SSE 透传全部迁移；且新增增强（502 JSON connect 错误、10s 拨号超时、跨协议头过滤、证书路径回退） |
| 安全限流 | server_http_ai_proxy_security.go → proxy/ | 限流桶(burst 5/1rps/TTL 10min)、key 正则、脱敏一致 |
| openclaw | openclaw_client.go → system/ | 客户端 1:1，但调用链缺失（见 2.4） |
| REST handler | 21 个 server_api_*.go → api/ | 用户端+管理端+爬虫数据 API 处理逻辑等价 |
| WebSocket | server_ws_*.go → websocket/ | hub 限 128 连接、ChatTotal 流式聚合一致 |
| MCP/爬虫 | mcp_interface_*、spider_* 17 个 → spider/ | 全部迁移，限流信号量在 main.go 挂接 |
| 系统/配置/日志 | system_info_linux.go(949 行差 1 行包名)、config、logger | 一致，配置保留旧格式向后兼容 |

### 1.2 旧工程有意废弃项（架构决策，勿回迁）
- `server_web_*` Go 内嵌 HTML 页面（约 20 个页面 handler）→ 由 ClientWeb SPA 替代。
- 注：ClientWeb 目前仍是占位（apps/manager、apps/user 为空目录），SPA 未实现前 Web 管理页面不可用。

## 2. 发现的缺口与优化项

### 2.1 【关键】REST/WS 路由未挂载（阶段5 未完成）
- `webserver/webserver.go:74 RegisterAPIRoutes` 仅注册 `/healthz`。
- 旧工程 `server_web_manager.go` / `server_web_user.go` 约 70+ 条 `mux.HandleFunc`（API Interface 路由 + 鉴权中间件 + 同源代理挂载）在新工程无等价物。
- `websocket.chatAnalysisTotalWSHandle` 未导出，无法被 webserver 挂载。
- **影响**：49101/42901 端口上 REST API 全部 404（仅 SPA 静态可访问），Web 门户后端不可用。

### 2.2 【关键】同源 AI 代理转发未挂载到 Web 端口
- 旧工程在 manager/user Web mux 上挂 `/Anthropic/`、`/OpenAI/` 同进程转发（解决浏览器 CORS/Mixed Content）。新工程未实现，proxy 包 `anthropicProxyHandler/openAIProxyHandler` 未导出。

### 2.3 【低】gorm 慢 SQL/错误日志被静默
- `database/connect.go:48-53` 用 `log.New(io.Discard,...)` 丢弃 gorm Warn 级慢 SQL 日志；旧工程输出到全局 logger。建议恢复（可配阈值）。

### 2.4 【中】OpenClaw 爬取调用链缺失
- 旧 `server_web_spider_crawl.go`（580 行，`/SpiderDataSourceCrawl` SSE 触发爬取）未迁移；`system/openclaw_client.go` 的 `CallOpenClawStream` 在新工程无调用方，`config.OpenClaw*` 五项配置无消费者（死代码状态）。

### 2.5 【中】协议转换分析器 Web 接口缺失
- 旧管理端 8 条 + 用户端 6 条 `/ProtocolConvertAnalyzer*` 路由及 handler 未迁移（底层 `protocol/protocol_analyzer.go` 分析逻辑已迁移，只缺 handler+路由）。

### 2.6 【低】旧代理逻辑测试未迁移
- 旧 `test_proxy_logic_test.go`（extractAPIKey/parseModelFromBody/buildProtocolAwareTargetURL 等）在新 proxy 包无对应。

## 3. 实施计划

| 阶段 | 内容 | 状态 |
|---|---|---|
| A（本轮） | 修复配置端口 49000/49003 并重启验证 | ✅ 完成 |
| B（本轮） | `RegisterManagerAPIRoutes` / `RegisterUserAPIRoutes`（按旧版路径 1:1 挂载全部 Interface API + 登录/验证码 + WS + 爬虫数据接口）；导出 websocket WS handler；proxy 导出同源挂载函数；迁移 UserSecurityChain/ManagerSecurityChain 安全中间件；webserver 区分管理端/用户端 mux（用户端套鉴权+安全链） | ✅ 完成（49101/42901 API 已挂载并实测通过） |
| C（本轮） | database/connect.go 恢复 gorm 慢 SQL 日志输出（stderr，随服务日志采集） | ✅ 完成 |
| D（下轮） | 迁移 `/SpiderDataSourceCrawl` SSE 爬取触发接口并接线 OpenClaw 配置 | 待做 |
| E（下轮） | 迁移协议转换分析器 Web 接口（管理 8 条 + 用户 6 条） | 待做 |
| F（下轮） | ClientWeb SPA 前端实现（manager/user 应用） | 待做 |
| G（下轮） | 补齐 proxy 包旧逻辑测试迁移 | 待做 |

## 4. 验证方式
1. `go build ./...` + `go vet ./...` + `go test ./...` 全绿。
2. `./rebuild_restart_app.sh` 重启后：
   - `curl http://127.0.0.1:49000/Anthropic/v1/messages`（带 key）→ 200；
   - `curl http://127.0.0.1:49101/SystemInfoInterface`、`/ChatAnalysisTotalInterface` 等 → 正常 JSON（不再 404）；
   - `curl https://127.0.0.1:42901/UserLoginInterface` 未登录访问用户端受保护接口 → 302/401（鉴权生效）。
3. 旧服务 29000/29003 保持运行不受影响。
