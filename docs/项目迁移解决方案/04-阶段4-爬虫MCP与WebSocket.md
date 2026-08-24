# 阶段4：爬虫 / MCP / WebSocket 模块迁移

## 迁移映射

| 旧文件（前缀） | 新包 |
|---|---|
| `spider_core.go`、`spider_scheduler.go`、`spider_cdp_*.go` | `ServerGo/spider/cdp/` |
| `spider_anti_bot*.go` | `ServerGo/spider/antibot/` |
| `spider_rss_fallback.go` | `ServerGo/spider/rss/` |
| `mcp_interface_*.go`、`server_mcp_spider_pipeline.go` | `ServerGo/mcp/` |
| `server_ws_hub.go`、`server_ws_chat_total*.go` | `ServerGo/websocket/` |

## 注意
- 调度器开关默认关闭（`enableSpiderScheduler=false`），避免与旧服务抢 Chrome CDP 资源。
- MCP 端口 29002、Web 端口 9101/29001、Agent 端口 29000/29003 保持不变，本阶段仍只编译不启动。

## 验收
- 编译通过；spider/mcp/ws 相关单测通过。
