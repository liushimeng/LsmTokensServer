# 阶段4：爬虫 / MCP / WebSocket 模块迁移

## 迁移映射

| 旧文件（前缀） | 新包 |
|---|---|
| `spider_core.go`、`spider_scheduler.go`、`spider_cdp_*.go` | `ServerGo/spider/cdp/` |
| `spider_anti_bot*.go` | `ServerGo/spider/antibot/` |
| `spider_rss_fallback.go` | `ServerGo/spider/rss/` |
| `mcp_interface_*.go`、`server_mcp_spider_pipeline.go` | `ServerGo/spider/`（与爬虫合并，见下） |
| `server_ws_hub.go`、`server_ws_chat_total*.go` | `ServerGo/websocket/` |

## 实际调整（迁移过程中发现）
- 旧工程中 `SpiderSession` 等核心类型定义于 mcp_interface_common.go，而 spider 引擎大量引用，
  二者实为同一业务域，强拆会产生循环依赖。故将 spider 与 mcp 合并为单一 `spider` 包
  （MCP 接口文件保留原文件名 mcp_interface_*.go，置于 spider 包内）。
- websocket 独立成包，引用 models 导出常量（TimeStatsMaxDays/StatsShardScanBatch）。
- 测试：spider 全部单测（反爬/CDP/RSS/MCP 接口）迁移并通过。

## 注意
- 调度器开关默认关闭（`enableSpiderScheduler=false`），避免与旧服务抢 Chrome CDP 资源。
- MCP 端口 29002、Web 端口 9101/29001、Agent 端口 29000/29003 保持不变，本阶段仍只编译不启动。

## 验收
- 编译通过；spider/mcp/ws 相关单测通过。
