# LsmTokensServer 知识库索引

> 开源版 AI Tokens 代理与管理服务（前后端分离架构）
> 旧私有项目 LsmHttpAgent 迁移重构而来，设计文档见 [项目迁移解决方案/](项目迁移解决方案/)

## 目录导航

### 架构与迁移
- [项目迁移解决方案/总览](项目迁移解决方案/00-总体迁移方案.md)
- [阶段1：工程骨架搭建](项目迁移解决方案/01-阶段1-工程骨架搭建.md)
- [阶段2：后端基础模块](项目迁移解决方案/02-阶段2-后端基础模块.md)
- [阶段3：代理/协议/API](项目迁移解决方案/03-阶段3-代理协议与API.md)
- [阶段4：爬虫/MCP/WebSocket](项目迁移解决方案/04-阶段4-爬虫MCP与WebSocket.md)
- [阶段5：前端页面实现](项目迁移解决方案/05-阶段5-前端页面实现.md)
- [阶段6：测试与切换](项目迁移解决方案/06-阶段6-测试与切换.md)

### 开发指南
- [AGENT.md — 通用 AI Agent 规范](开发指南/AGENT.md)
- [AGENT_INDEX.md — 源码文件索引](开发指南/AGENT_INDEX.md)
- [Developer_SOP.md — 开发标准操作规程](开发指南/Developer_SOP.md)
- [SKILL.md — 技能说明](开发指南/SKILL.md)
- [KILO.md — Kilo Code 工具说明](开发指南/KILO.md)
- [DEBUG_TOOL.md — 调试工具](开发指南/DEBUG_TOOL.md)

### 协议分析
- [cc-switch 系列与 Switchyard 借鉴](协议分析/)
- [协议转换优化方案（20260813）](协议分析/LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md)
- [Agent/Tools 检测优化方案](协议分析/LsmTokensServer_Detect_Agent_Tools优化方案_20260813_01.md)

### MCP 接口定义
- [MCP 接口清单](mcp/)
- [SpiderWebData 接口定义](mcp/MCP_SpiderWebData_def.md)
- [爬虫任务流程](mcp/Mission_Spider_MCP_Proc.md)

### 历史归档
- [CHANGELOG](历史归档/CHANGELOG.md)
- [MEMORY](历史归档/MEMORY.md)
- [CLAUDE.md（旧版，仅供参考）](历史归档/CLAUDE.md)

## 工程结构速览

```
LsmTokensServer/
├── ServerGo/       后端 Go（按模块分包：config/logger/database/models/proxy/protocol/api/spider/websocket/system/webserver）
├── ClientWeb/      前端 React + Vite
├── docs/           本文档目录
├── rebuild_restart_app.sh    一键编译前后端+部署+运行
├── tools/          本地私有工具（go-web-debug-tool，git 忽略）
├── LsmTokensServer.conf.example   配置模板（开源可提交）
├── LsmTokensServer.conf           实际配置（含敏感信息，git 忽略）
└── server.crt / server.key        TLS 证书（git 忽略）
```

## 快速开始

```bash
# 编译前后端（迁移期默认 build-only，不启动不占端口）
./rebuild_restart_app.sh --build-only

# 仅编译后端
./rebuild_restart_app.sh --build-only --skip-web

# 运行测试
cd ServerGo && go test ./...
```
