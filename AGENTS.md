# AGENTS.md - LsmTokensServer AI Agent 通用入口

> 面向 Claude Code、Codex、OpenCode、pi、Hermes、OpenClaw 等 AI Agent 工具的高密度上下文（当前版本 v2.0.77）。
> 工具特定说明见 [`CLAUDE.md`](CLAUDE.md)；完整源码索引见 [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)。

## 项目概述

LsmTokensServer（开源版）是 AI Tokens 代理与管理服务，前后端分离：

- **后端 Go**（`ServerGo/`，按业务域分包）：
  - AI 代理转发（29000 HTTP / 29003 HTTPS），支持 Anthropic / OpenAI 协议互转
  - 用户端 REST（29001）+ 管理端 REST（9101），JWT 鉴权
  - MCP 爬虫服务（29002，CDP + 反爬）；WebSocket 推送（ChatTotal 流式统计）
- **前端 React + Vite**（`ClientWeb/`）：管理端 `dist-manager` + 用户端 `dist-user` 双构建 SPA
- **文档**（`docs/`）：迁移方案 + 开发指南 + 协议分析 + MCP 定义

## Agent 必读

1. **端口规范**：管理端 `9101`、AI 代理 `29000`（HTTP）/`29003`（HTTPS）、用户端 `29001`、MCP `29002`、爬虫 CDP `9222`。
2. **编译/启动走脚本**：必须且只能使用 `./rebuild_restart_app.sh`（不带任何参数，完整重启）。禁止带 `--build-only`、`--skip-web` 等参数；禁止直接 `go build` 或 `nohup ./LsmTokensServer`。
3. **敏感信息不提交**：`LsmTokensServer.conf`、证书、日志、`tmpPlan/`、`.env`、私有子模块（`go-web-debug-tool/`、`python-generate-image-tool/`）。
4. **中文 commit**：分阶段提交，格式 `阶段X：说明`；每阶段保证 `go build ./...` 通过。
5. **前端双构建隔离**（阶段T 起强制）：`npm run build` 一条命令产出 `dist-manager`/`dist-user` 两套产物，`webserver` 按角色绑定目录、禁止共享或跨目录回落；角色由构建期常量 `__APP_ROLE__`（vite `define`）决定，禁止运行时嗅探；管理员专属页面与接口调用必须 `__APP_ROLE__ === 'manager'` 常量门控 + 动态 `import()`，确保用户端产物零管理代码。

## 安全红线（v2.0.56 起强制）

- **禁止硬编码任何密钥/密码/IP**：JWT 密钥、管理员凭证只能放 `LsmTokensServer.conf` 的 `security` 段；Python 分析脚本数据库凭证走环境变量 `LSM_MYSQL_*`。
- **新增管理端接口必须在 `ManagerAuthMiddleware` 之后挂载**（`api.RegisterManagerAPIRoutes` 内注册即自动受保护）；用户端接口同理走 `UserAuthMiddleware`。
- **密码只存 bcrypt 哈希**（`api.HashPassword`），校验用 `api.VerifyPassword`（自动兼容并升级旧明文）。
- **API 响应禁止返回明文密码/完整手机号**：用 `api.MaskPhone`，密码字段置空。
- **前端禁止持久化 API Key**：记住我只存模型名；对话历史 localStorage 须限条数（200）并带过期清理。
- 详见 [`docs/开发指南/SECURITY.md`](docs/开发指南/SECURITY.md)。

## 快速定位

| 目标 | 入口 |
|---|---|
| 迁移设计总览 | [`docs/项目迁移解决方案/00-总体迁移方案.md`](docs/项目迁移解决方案/00-总体迁移方案.md) |
| 源码索引 | [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md) |
| 通用 Agent 规范 | [`docs/开发指南/AGENT.md`](docs/开发指南/AGENT.md) |
| 代码结构速查 | [`CLAUDE.md`](CLAUDE.md) §3 |
| 开发 SOP | [`docs/开发指南/Developer_SOP.md`](docs/开发指南/Developer_SOP.md) |
| 知识库首页 | [`docs/INDEX.md`](docs/INDEX.md) |

## 代理可用的工具集（本工程提供的能力）

- 代码读写：修改后端 Go 模块 / 前端 React 页面
- 测试运行：`cd ServerGo && go test ./...`；前端自检脚本 `node ClientWeb/src/pages/chat-analysis/agentToolFields.test.js`
- 编译部署：`./rebuild_restart_app.sh`
- git 操作：提交、推送（双远端：gitcode + gitee）
- **本地私有 Python 工具（仓库根目录，不入库）**：`python-generate-image-tool/` — 火山引擎方舟大模型图片生成 SDK，模型固定 `doubao-seedream-5-0-pro-260628`，接口 `https://ark.cn-beijing.volces.com/api/v3/images/generations`；调用方式见 [`CLAUDE.md`](CLAUDE.md) §6

## SubAgent 角色定义与使用规则

> 以下场景必须使用独立 SubAgent（`spawn_agent`），每次 `spawn_agent` 必须传入与场景匹配的角色定义与系统词（message），确保专业分工与上下文隔离。

| 场景 | 触发条件 | SubAgent 角色 | 系统词要点 |
|---|---|---|---|
| Web 端游戏设计 | `ClientWeb/` 游戏功能设计/页面开发/交互逻辑 | Web 游戏设计师 / 前端游戏开发工程师 | React+Vite；双构建 `__APP_ROLE__` 门控；游戏动画（CSS Animation/Canvas/WebGL）、状态管理、音效集成；前端安全红线（禁持久化 API Key、localStorage 限条数） |
| 服务器端游戏设计 | `ServerGo/` 游戏后端逻辑/数据接口/业务域建模 | 游戏后端架构师 / Go 游戏服务开发工程师 | Go 业务域分包（`models/`、`api/`、`websocket/` 等）；房间匹配、实时通信、状态同步、排行榜；后端安全红线（bcrypt、JWT、禁硬编码密钥）；输出须过 `go build ./...` 与 `go test ./...` |
| 产品设计 | 新功能产品设计、需求拆解、交互流程、PRD | 产品经理 / 产品设计师 | 需求分析、优先级排序、MVP 定义；熟悉 AI Tokens 代理与管理业务；输出：用户故事 + 功能清单 + 交互流程图描述 + 验收标准 |
| 界面设计与图片生成 | UI 原型/视觉设计、`python-generate-image-tool` 出图 | UI/UX 设计师 / AI 图像创作师 | 需求转高保真描述与 prompt；模型固定、最小像素 3,686,400；输出路径 `ImageOutput/{prefix}_{timestamp}_{seq:03d}.png`；小图标先 `2048x2048` 生成再 `resize_image()` 缩放 |
| 复杂功能测试与独立游戏产品 | 前后端配合复杂功能测试、独立游戏完整开发 | 全栈游戏开发工程师 / QA 测试工程师 | React+Go 全栈；REST/WebSocket 接口契约；集成与端到端测试用例；产品全生命周期（设计→开发→测试→部署）；部署验证走 `./rebuild_restart_app.sh --build-only` |

**使用原则**：① 角色隔离——不同场景用不同 SubAgent，避免单一 Agent 承担过多上下文；② 系统词定制——每次 `spawn_agent` 必传角色定义与系统词；③ 结果整合——SubAgent 完成后由主 Agent 审核、集成与最终提交；④ 并行优先——无依赖的多个 SubAgent 并行执行。
 | 复杂功能测试与独立游戏产品 | 前后端配合复杂功能测试、独立游戏完整开发 | 全栈游戏开发工程师 / QA 测试工程师 | React+Go 全栈；REST/WebSocket 接口契约；集成与端到端测试用例；产品全生命周期（设计→开发→测试→部署）；部署验证走 `./rebuild_restart_app.sh` |
