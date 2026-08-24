# LsmTokensServer 服务代码文件的迁移和重构 — 全面排查与优化方案（2026-08-24 第 03 轮）

> 排查方法：对 `/usr/local/LsmHttpAgent/`（旧，232 个 Go 文件）与 `ServerGo/`（新）做三路并行逐文件比对
> （① OpenClaw 爬取链路 ② 协议转换分析器 ③ 全量路由/工具 handler/测试/后台任务），
> 辅以 `go build` / `go test ./...` 基线验证（基线：全绿；`go vet` 5 处告警均为旧工程既有问题的平移，旧工程同类 7 处）。

## 0. 本轮结论总览

第 01/02 轮已完成算法/分表/协议/识别器/代理/WS/MCP/REST 主路由的迁移。本轮全面复查发现 5 类剩余缺口，
**全部在本轮完成修复**：

| # | 缺口 | 严重度 | 本轮状态 |
|---|---|---|---|
| 1 | `/SpiderDataSourceCrawl` SSE AI 爬取入口（管理+用户）缺失，`system.CallOpenClawStream` 为死代码 | 中 | ✅ 已迁移 |
| 2 | 协议转换分析器全部 JSON API（管理 7 条 + 用户 5 条）缺失 | 中 | ✅ 已迁移 |
| 3 | 4 条工具类接口缺失：`/UserInfoLogInterface`、`/WikiInterface`、`/CertDownloadInfoInterface`、`/CertDownloadInterface` | 中 | ✅ 已迁移 |
| 4 | 代理核心逻辑测试（extractAPIKey / buildProtocolAwareTargetURL 等）与 Bearer 头脱敏测试未迁移 | 低 | ✅ 已迁移 |
| 5 | ClientWeb SPA 前端（manager/user 应用）仍未实现 | 高（前端） | ⏳ 下轮（阶段 F） |

## 1. 逐文件比对总结论（延续 02 轮）

### 1.1 已确认 1:1 等价迁移（本轮复核无回归）
- 调度算法（指定/稳定/经济型）、分表、tokens 统计、缓存/清理、全部 manage CRUD、MySQL 连接、
  协议转换 6 文件、识别器、AI 代理转发 + 安全限流、REST 21 个 handler、WebSocket、MCP/爬虫 17 文件、
  openclaw_client.go（与旧版逐行一致）、server_mcp_spider_pipeline.go（逐行一致）。
- AI 代理端口（49000/49003）与 MCP 端口路由 1:1；新工程 Web 端口额外新增 `/healthz`（增强，非缺口）。
- 启动流程差异仅为：CGO 构建时间改为 -ldflags 注入；MCP Web 由阻塞改为 goroutine 启动（等价）。

### 1.2 本轮迁移明细（新增文件）

| 新文件 | 内容 | 来源 |
|---|---|---|
| `ServerGo/api/server_api_spider_crawl.go` | `/SpiderDataSourceCrawl` SSE handler：每用户并发锁、数据源存在性/权限/状态校验、`{{.DataSourceID}}` 提示词渲染、`system.CallOpenClawStream` 流式回写、15 分钟超时、SSE error+done 收尾协议（与旧前端 EventSource 兼容）。默认提示词中工作目录已改为 `/usr/local/LsmTokensServer/LsmTokensServer/` | server_web_spider_crawl.go 后端部分（前端模态框由 SPA 实现） |
| `ServerGo/api/server_api_protocol_converter.go` | 分析器 12 条 JSON 接口：Status/Toggle/Test/Records/RecordDetail/Users/Mapping（管理端）+ Status/Test/Records/RecordDetail/Mapping（用户端，记录强制按登录用户过滤）。`ProtocolConvertAnalyzerEnabled` 全局开关、`BuildProtocolConvertAnalyzerMapping` 9 组映射知识库 | server_web_manager/user_protocol_converter.go 的 API 部分 |
| `ServerGo/models/protocol_convert_analyzer.go` | 分析器数据层：跨分表查询（sort.Slice + 每表 LIMIT 取样 + 内存分页，深分页自动扩样）、按用户+模型哈希定位单表查询、单条大字段详情（base64 解码 + Bearer 头脱敏）。days 语义：≤0 全量、>0 最近 N 天（上限 90） | 同上文件 266-606 行 |
| `ServerGo/api/server_api_common_extra.go` | 证书信息/下载（路径解析基于可执行目录、10MB 上限、原始文件名下载头）；Wiki 列表/内容（跳过隐藏目录与 go-web-debug-tool，仅 .md，路径越界防护，内容返回原文由前端渲染 Markdown）；用户操作日志（UserLogReader 行索引 + 时间倒序 + 分页 + 关键词搜索 + 文件变化检测缓存池） | server_web_common_dialog_cert_handlers.go / server_web_common_wiki.go / server_web_common_dialog_handlers.go 的 API 部分 |
| `ServerGo/proxy/server_http_ai_proxy_logic_test.go` | extractAPIKey / parseModelFromBody / replaceModelInBody / getRelativePath / buildProtocolAwareTargetURL（含 v2.0.20 路径去重 12 用例）/ isValidProtocol / validateRequestModelName | test_proxy_logic_test.go |
| `ServerGo/models/security_redact_test.go` | RedactAuthorizationBearerHeaderText 8 用例（大小写/CRLF/多行/幂等/Basic 不变/Proxy-Authorization 不变） | test_header_redaction_test.go |

路由注册：`api/routes.go` 管理端 +16 条、用户端 +13 条（新增协议分析器、爬取、工具接口，路径与旧版 1:1）。
配套改动：`config.GetDefaultConfig()` 导出（getRelativePath 依赖全局配置，测试初始化用）。

## 2. 有意废弃项（架构决策，勿回迁）
- 旧 `server_web_*` Go 内嵌 HTML/CSS/JS 页面（约 20 个页面 + 协议分析器 1070 行模板）→ ClientWeb SPA 替代。
  其中爬取模态框 JS、分析器页面 JS、用户日志弹窗 JS 属前端资产，待 SPA 实现对应页面时移植。
- 旧服务端 `markdownToHTML` / `inlineMarkdown`（约 250 行）→ 新架构 README/Wiki 返回原文，前端渲染。
- 旧 `BuildProtocolConvertAnalyzerRecordConversion`（服务端四段转换拼装）→ 前端分四次调 Test 接口实现。

## 3. 剩余待办（下轮）

| 阶段 | 内容 |
|---|---|
| F | ClientWeb SPA 实现（登录、管理端各页面、用户端各页面、协议分析器页、爬取模态框、用户日志/Wiki/证书弹窗） |
| G+ | 继续迁移存量回归测试（v2.0xx 系列）：优先 websocket 包（已迁代码零测试，v2055/v2060）、chat_total 系列、stats gobucket 系列 |
| — | 统一 `config.DEFAULT_OPENCLAW_SYSTEM_PROMPT`（短版）与 `system.DefaultOpenClawSystemPrompt`（旧版长文案）两处默认值 |
| — | 核对 v2.0.56 task 特征异步回填 goroutine 在新 api 包中的接线 |

## 4. 验证方式与结果
1. `go build ./...` 通过；`go test ./...` 全绿（api/models/protocol/proxy/recognizer/spider）。
2. 新增测试覆盖：proxy 逻辑 7 组 + Bearer 脱敏 8 用例全部通过。
3. `./rebuild_restart_app.sh` 完整重启后：
   - 端口 49000/49003（AI 代理）、49101（管理 Web）、42901（用户 Web）、42902（MCP）全部监听；
   - 新增接口冒烟：`/ProtocolConvertAnalyzerStatus`、`/ProtocolConvertAnalyzerMapping`、`/WikiInterface`、
     `/CertDownloadInfoInterface`、`/UserInfoLogInterface` 返回 JSON 正常；
     `/SpiderDataSourceCrawl?data_source_id=1` 返回 SSE 流（缺参时报错事件符合预期）；
   - 旧服务 29000/29003 未受影响。

## 5. 提交
中文 commit：`阶段6：补齐协议转换分析器/爬虫SSE/证书下载/Wiki/用户日志接口 + 代理逻辑与脱敏测试迁移（迁移排查03轮）`
