# AGENTS.md - LsmTokensServer AI Agent 通用入口

> 面向 Claude Code、Codex、OpenCode、pi、Hermes、OpenClaw 等 AI Agent 工具的高密度上下文。
> 工具特定说明见 [`CLAUDE.md`](CLAUDE.md)；完整源码索引见 [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)。

## 项目概述

LsmTokensServer（开源版）是 AI Tokens 代理与管理服务，由私有项目 LsmHttpAgent 迁移重构，前后端分离：

- **后端 Go**（`ServerGo/`）：按业务域分包，核心能力：
  - AI 代理转发（29000 HTTP / 29003 HTTPS），支持 Anthropic / OpenAI 协议互转
  - 用户端 REST（29001）+ 管理端 REST（9101），JWT 鉴权
  - MCP 爬虫服务（29002，CDP + 反爬）
  - WebSocket 推送（ChatTotal 流式统计）
- **前端 React + Vite**（`ClientWeb/`）：管理端 + 用户端两个 SPA
- **文档**（`docs/`）：迁移方案 + 开发指南 + 协议分析 + MCP 定义

## Agent 必读

1. **不要停止旧服务**：`/usr/local/LsmHttpAgent` 必须持续运行，AI 代理端口全量验证前严禁停。
2. **编译/启动走脚本**：`./rebuild_restart_app.sh`，仅编译用 `--build-only`。
3. **敏感信息不提交**：`LsmTokensServer.conf`、证书、日志、`tmpPlan/`、`.env`、私有子模块。
4. **中文 commit**：分阶段提交，格式 `阶段X：说明`。

## 安全红线（v2.0.56 起强制）

- **禁止硬编码任何密钥/密码/IP**：JWT 密钥、管理员凭证只能放 `LsmTokensServer.conf` 的 `security` 段；Python 分析脚本数据库凭证走环境变量 `LSM_MYSQL_*`。
- **新增管理端接口必须在 `ManagerAuthMiddleware` 之后挂载**（`api.RegisterManagerAPIRoutes` 内注册即自动受保护）；用户端接口同理走 `UserAuthMiddleware`。
- **密码只存 bcrypt 哈希**（`api.HashPassword`），校验用 `api.VerifyPassword`（自动兼容并升级旧明文）。
- **API 响应禁止返回明文密码/完整手机号**：用 `api.MaskPhone`，密码字段置空。
- **前端禁止持久化 API Key**：记住我只存模型名；对话历史 localStorage 须限条数（200）并带过期清理。
- 详见 [`docs/开发指南/SECURITY.md`](docs/开发指南/SECURITY.md)。

## 快速定位

- 迁移设计：[`docs/项目迁移解决方案/00-总体迁移方案.md`](docs/项目迁移解决方案/00-总体迁移方案.md)
- 源码索引：[`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)
- 代码结构对照：见 [`CLAUDE.md`](CLAUDE.md) §3
- 开发 SOP：[`docs/开发指南/Developer_SOP.md`](docs/开发指南/Developer_SOP.md)
- 知识库首页：[`docs/INDEX.md`](docs/INDEX.md)

## 代理可用的工具集（本工程提供的能力）

- 代码读写：修改后端 Go 模块 / 前端 React 页面
- 测试运行：`cd ServerGo && go test ./...` 或 `cd ClientWeb && npm run test`
- 编译部署：`./rebuild_restart_app.sh`
- git 操作：提交、推送（双远端：gitcode + gitee）
- **本地私有 Python 工具（仓库根目录，不入库）**：
  - `python-generate-image-tool/` — 火山引擎方舟大模型图片生成 SDK，模型固定 `doubao-seedream-5-0-pro-260628`，接口 `https://ark.cn-beijing.volces.com/api/v3/images/generations`；调用方式见 [`CLAUDE.md`](CLAUDE.md) §6.1

