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

## ⚠️ rebuild_restart_app.sh 服务中断规则（v2.0.78+ 强制）

`./rebuild_restart_app.sh` 是**会中断所有在线服务的重启脚本**，不仅是"编译命令"。

### 中断影响清单

| 行为 | 影响 |
|---|---|
| `kill` 旧 LsmTokensServer 进程 | **管理端 9101 / 用户端 29001 / AI 代理 29000+29003 / MCP 29002 全部停止** |
| 等待端口释放 → 启动新进程 | 服务中断窗口通常 **5–15 秒**（取决于编译耗时，旧进程被杀到新进程就绪的间隔） |
| **重建期间用户端 HTML 入口的 hash 文件变化**（如 `Login-CWBegAxZ.js`） | 用户浏览器加载旧 hash 触发 404，**前端全局错误监听会自动 `location.reload()`**——对登录页用户是体验跳变；对正在调用 API 的 Agent 是直接失败 |

### 禁止场景

1. **禁止在自动化测试循环中调用 rebuild**：单元测试/集成测试运行期间触发 rebuild，会让测试中的 HTTP 请求 `connection refused`。
3. **禁止在另一个 Agent 正在调用 API 时调用 rebuild**：抓数据、跑 E2E、CDP 自动化、上传爬虫结果等长任务期间，重启会让正在 in-flight 的请求全部失败，Agent 需要从头重试并可能误判为业务错误。
4. **禁止在用户正在登录或使用页面时调用 rebuild**：用户体验断裂（前端会触发 chunk 404 全局重载）。

### 何时调用 rebuild（强制场景）

- ✅ 本阶段代码修改完毕，自检与单元测试全绿后 → 调用 rebuild 验证集成效果
- ✅ 跨阶段（涉及后端 Go 二进制变化）必须 rebuild 才能生效时
- ❌ 仅前端改动但本阶段不需要端到端验证时——**可不调用**，下次阶段一起 rebuild

### 多 Agent 并行场景的协调规则

- **同一阶段只允许一个 Agent 调用 rebuild**：并发 rebuild 会导致端口冲突、旧进程互相 kill、新进程端口未就绪等雪崩。
- **跨阶段并行**（如 SubAgent A 写后端 + SubAgent B 写前端）：A 先 rebuild 验证后端 → B 提交代码后**单独再 rebuild**一次（B 的提交未在 A 的 rebuild 中生效）。
- rebuild 前应 `git status` 与 `git diff` 确认本次提交已落地，避免本地未提交改动被旧进程覆盖。

### 临时调试需要"不停服务热重启"？

- 后端代码：直接 `go run` 或 IDE 调试器 attach（绕过 rebuild）；端口已被占用 → 进程内 `kill -SIGUSR1` 或类似自重启钩子（项目未提供，不在支持范围）。
- 前端代码：仅修改 `ClientWeb/src/` 时可绕过 rebuild，由 `webserver` 直接服务源文件（具体配置见 `webserver/` 包）；但**生产双构建隔离的产物仍来自 rebuild**。

### 错误处理建议

调用 rebuild 后若脚本被中断或新进程未就绪：
1. `ps aux | grep LsmTokensServer` 检查是否有进程
2. `ss -tlnp | grep -E "9101|29001"` 检查端口监听
3. 都没有则手动启动：`cd ServerGo && nohup ./LsmTokensServer -c ../LsmTokensServer.conf >/tmp/lsm.log 2>&1 &`（**仅限恢复**，不要作为常规启动方式）

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
