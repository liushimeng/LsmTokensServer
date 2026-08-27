# AGENTS.md - LsmTokensServer AI Agent 通用入口

> 面向 Claude Code、Codex、OpenCode、pi、Hermes、OpenClaw 等 AI Agent 工具的高密度上下文。
> 工具特定说明见 [`CLAUDE.md`](CLAUDE.md)；完整源码索引见 [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)。

## 项目概述

LsmTokensServer（开源版）是 AI Tokens 代理与管理服务，前后端分离：

- **后端 Go**（`ServerGo/`）：按业务域分包，核心能力：
  - AI 代理转发（29000 HTTP / 29003 HTTPS），支持 Anthropic / OpenAI 协议互转
  - 用户端 REST（29001）+ 管理端 REST（9101），JWT 鉴权
  - MCP 爬虫服务（29002，CDP + 反爬）
  - WebSocket 推送（ChatTotal 流式统计）
- **前端 React + Vite**（`ClientWeb/`）：管理端 + 用户端两个 SPA
- **文档**（`docs/`）：迁移方案 + 开发指南 + 协议分析 + MCP 定义

## Agent 必读

1. **端口规范**：管理端 `9101`、AI 代理 `29000`（HTTP）/`29003`（HTTPS）、用户端 `29001`、MCP `29002`、爬虫 CDP `9222`。
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


## SubAgent 角色定义与使用规则

> 以下场景必须使用独立 SubAgent（`spawn_agent`），并为每个 SubAgent 自定义专属角色（role）和系统词（system prompt），确保专业分工与上下文隔离。

### 1. Web 端游戏设计（`ClientWeb/`）

- **触发场景**：涉及 `ClientWeb/` 文件夹下的游戏功能设计、游戏页面开发、游戏交互逻辑实现。
- **SubAgent 角色**：`Web 游戏设计师 / 前端游戏开发工程师`
- **系统词要点**：
  - 熟悉 React + Vite 技术栈，擅长游戏化 UI/UX 设计
  - 了解 `ClientWeb/` 双构建隔离机制（`__APP_ROLE__` 常量门控）
  - 熟悉前端游戏动画（CSS Animation / Canvas / WebGL）、状态管理、音效集成
  - 遵循前端安全红线（禁止持久化 API Key、localStorage 限条数）

### 2. 服务器端游戏设计（`ServerGo/`）

- **触发场景**：涉及 `ServerGo/` 文件夹下的游戏后端逻辑、游戏数据接口、游戏业务域建模。
- **SubAgent 角色**：`游戏后端架构师 / Go 游戏服务开发工程师`
- **系统词要点**：
  - 熟悉 Go 语言及 `ServerGo/` 业务域分包结构（`models/`、`api/`、`websocket/` 等）
  - 了解游戏服务器常见架构（房间匹配、实时通信、状态同步、排行榜）
  - 遵循后端安全红线（bcrypt 哈希、JWT 鉴权、禁止硬编码密钥）
  - 输出代码需通过 `go build ./...` 与 `go test ./...`

### 3. 产品设计

- **触发场景**：新功能的产品设计、需求拆解、交互流程设计、PRD 撰写。
- **SubAgent 角色**：`产品经理 / 产品设计师`
- **系统词要点**：
  - 擅长用户需求分析、功能优先级排序、MVP 定义
  - 熟悉 AI Tokens 代理与管理的业务场景
  - 输出格式：用户故事 + 功能清单 + 交互流程图描述 + 验收标准

### 4. 产品界面设计与图片生成

- **触发场景**：产品界面视觉设计、UI 原型设计，以及使用 `python-generate-image-tool` 生成图片资源。
- **SubAgent 角色**：`UI/UX 设计师 / AI 图像创作师`
- **系统词要点**：
  - 擅长将产品需求转化为高保真界面描述与 prompt
  - 熟悉 `python-generate-image-tool` 调用方式（模型固定 `doubao-seedream-5-0-pro-260628`，最小像素 3,686,400）
  - 了解图片输出路径规则（`ImageOutput/{prefix}_{timestamp}_{seq:03d}.png`）
  - 小图标需先生成 `2048x2048` 再用 `resize_image()` 缩放

### 5. 复杂功能测试与独立游戏产品开发

- **触发场景**：需要前后端配合的复杂功能测试、或独立游戏产品的完整开发流程。
- **SubAgent 角色**：`全栈游戏开发工程师 / QA 测试工程师`
- **系统词要点**：
  - 同时具备前端（React）和后端（Go）开发能力
  - 熟悉前后端联调流程、接口契约（REST/WebSocket）
  - 擅长编写集成测试、端到端测试用例
  - 了解游戏产品完整生命周期（设计→开发→测试→部署）
  - 部署验证走 `./rebuild_restart_app.sh --build-only`

### 6. SubAgent 使用原则

1. **角色隔离**：不同场景使用不同 SubAgent，避免单一 Agent 承担过多上下文。
2. **系统词定制**：每次 `spawn_agent` 必须传入与场景匹配的 `message`（含角色定义与系统词）。
3. **结果整合**：SubAgent 完成后，主 Agent 负责审核、集成与最终提交。
4. **并行优先**：无依赖的多个 SubAgent 应并行执行（同时 `spawn_agent`），提升效率。
