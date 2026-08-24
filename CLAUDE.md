# CLAUDE.md - LsmTokensServer 工程约束与代码规范（Claude Code 上下文）

> 供 Claude Code / Claude Agent 工具加载使用。通用 AI Agent 规范见 [`docs/开发指南/AGENT.md`](docs/开发指南/AGENT.md)。
> 完整源码索引见 [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)。

## 1. 工程定位

LsmTokensServer 是开源 AI Tokens 代理与管理服务，由私有项目 LsmHttpAgent 迁移重构而来，前后端分离架构。

- 后端：`ServerGo/`（Go，按业务域分包）
- 前端：`ClientWeb/`（React + Vite）
- 文档：`docs/`
- 脚本：`scripts/rebuild_restart_app.sh`

## 2. 必须遵守的规则

### 2.1 编译 / 启动必须走脚本
所有涉及编译、启动、重启的操作，必须通过 `./scripts/rebuild_restart_app.sh`：
```bash
./scripts/rebuild_restart_app.sh --build-only           # 仅编译（迁移期默认推荐）
./scripts/rebuild_restart_app.sh --build-only --skip-web # 仅编译后端
./scripts/rebuild_restart_app.sh                        # 完整重启（切换时才用）
```
禁止直接 `go build` 或 `nohup ./LsmTokensServer`。

### 2.2 旧服务不得停止
- **旧服务 `/usr/local/LsmHttpAgent` 必须持续运行**，在 AI 代理端口（29000/29003）全量验证通过并人工确认之前，禁止停止。
- 新程序迁移期默认 `--build-only` 模式，不占用生产端口。
- 端口与旧版一致（9101/29000/29001/29002/29003），切换需在同一时间窗口完成。

### 2.3 敏感信息严禁提交
以下文件/目录绝不能提交到 git（已在 `.gitignore` 中）：
- `LsmTokensServer.conf`（MySQL 密码、openClaw API Key 等）
- `server.crt` / `server.key`（TLS 证书私钥）
- `*.log`、二进制、pid 文件
- `tools/go-web-debug-tool/`（本地私有子模块）
- `node_modules/`、`ClientWeb/dist/`

### 2.4 提交规范
- 中文 commit message，分阶段提交，格式：`阶段X：简要说明`
- 每阶段完成后必须保证 `go build ./...` 通过、`go test ./...` 全绿（新增测试用例）。

## 3. 代码结构速查

| 旧文件前缀 | 新位置（`ServerGo/` 下） | 说明 |
|---|---|---|
| `server_conf.go` | `config/` | 配置加载 |
| `server_logger.go` | `logger/` | 日志轮转 |
| `mysql_connect.go` / `mysql_*_sub_table.go` | `database/` + `models/` | DB 基础 + 业务模型 |
| `agent_algorithm*.go` | `models/` | 路由选择算法（与 cache 同包避免循环依赖） |
| `recognizer_*.go` | `recognizer/` | agent/session/tool 识别 |
| `protocol_*.go` | `protocol/` | Anthropic↔OpenAI 协议转换 + SSE |
| `server_http_ai_proxy*.go` / `server_http_agent_proxy.go` | `proxy/` | AI 代理转发 + 安全限流 |
| `server_api_*.go` | `api/` | REST 接口（用户端 + 管理端） |
| `spider_*.go` + `mcp_interface_*.go` | `spider/` | 爬虫 CDP + MCP 接口（同包避免循环依赖） |
| `server_ws_*.go` | `websocket/` | WS 推送（ChatTotal 流式） |
| `ai_api_connectivity.go` / `git_info.go` / `system_info_linux.go` | `system/` | 系统辅助 |
| server_web_*.go（旧 HTML 生成） | 废弃，由前端实现 | `webserver/` 仅做 SPA 静态托管 + API 路由挂载 |

## 4. 工作流

1. 先读 `docs/项目迁移解决方案/` 对应阶段文档，确认设计。
2. 在对应包内实现/修改，保持包内自洽，减少跨包循环依赖。
3. 单元测试 + `go vet` 通过。
4. `./scripts/rebuild_restart_app.sh --build-only` 编译验证。
5. 中文 commit 提交。

## 5. 敏感配置获取

- 实际配置文件路径：工程根目录 `LsmTokensServer.conf`（勿提交）
- 脱敏模板：`LsmTokensServer.conf.example`
