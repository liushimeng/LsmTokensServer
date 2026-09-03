# CLAUDE.md - LsmTokensServer 工程约束与代码规范（Claude Code 上下文）

> 供 Claude Code / Claude Agent 工具加载使用（当前版本 v2.0.77）。
> 通用 AI Agent 入口与 SubAgent 规则见 [`AGENTS.md`](AGENTS.md)；完整源码索引见 [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)。

## 1. 工程定位

LsmTokensServer 是开源 AI Tokens 代理与管理服务，前后端分离架构。

- 后端：`ServerGo/`（Go，按业务域分包）
- 前端：`ClientWeb/`（React + Vite，管理端/用户端双构建）
- 文档：`docs/`
- 脚本：`rebuild_restart_app.sh`

## 2. 必须遵守的规则

### 2.1 编译 / 启动必须走脚本
所有涉及编译、启动、重启的操作，必须且只能使用 `./rebuild_restart_app.sh`（不带任何参数）：
```bash
./rebuild_restart_app.sh                         # 完整重启（编译 + 运行）
```
禁止直接 `go build` 或 `nohup ./LsmTokensServer`。禁止带 `--build-only`、`--skip-web` 等参数（完整重启即可，脚本内部已处理前后端编译与服务启停）。

**⚠️ 服务中断规则（rebuild 会 kill 旧进程 + 重启新进程）**：
- rebuild 会**终止所有在线服务**（9101/29001/29000+29003/29002），中断窗口通常 **5–15 秒**。
- 前端 chunk hash 变化时，正在访问页面的浏览器会触发 404 → 主页面"全局错误监听"自动 `location.reload()`。
- **禁止在自动化测试循环、Agent 调用 API、用户登录使用页面期间调用 rebuild**。
- **同一阶段只允许一个 Agent 调用 rebuild**（并发 rebuild 会导致端口冲突与雪崩）。
- 完整规则、禁止场景、多 Agent 协调规则见 [`AGENTS.md`](AGENTS.md) §「rebuild_restart_app.sh 服务中断规则」。

### 2.2 端口规范
管理端 `9101`、AI 代理 `29000`（HTTP）/`29003`（HTTPS）、用户端 `29001`、MCP `29002`、爬虫 CDP `9222`。

### 2.3 敏感信息严禁提交
以下文件/目录绝不能提交到 git（已在 `.gitignore` 中）：
- `LsmTokensServer.conf`（MySQL 密码、openClaw API Key 等）
- `server.crt` / `server.key`（TLS 证书私钥）
- `*.log`、二进制、pid 文件
- `go-web-debug-tool/`、`python-generate-image-tool/`（本地私有子模块）
- `node_modules/`、`ClientWeb/dist*/`

### 2.4 提交规范
- 中文 commit message，分阶段提交，格式：`阶段X：简要说明`
- 每阶段完成后必须保证 `go build ./...` 通过、`go test ./...` 全绿（新增测试用例）。

### 2.5 管理员/用户 Web 服务双构建隔离（阶段T/v2.0.57 起强制）
- **前端必须双构建**：`npm run build` 一条命令产出 `ClientWeb/dist-manager`（管理员 Web，`managerWebListenPort`）与 `ClientWeb/dist-user`（用户 Web，`userWebListenPort`）两套产物；`webserver` 按角色绑定目录，**禁止共享目录或跨目录回落**。
- **角色由构建期常量决定**：前端代码用 `__APP_ROLE__`（vite `define` 静态替换为 `'manager'`/`'user'`）判断角色，**禁止运行时嗅探端口/localStorage**。
- **用户端产物零管理代码**：管理员专属页面（`UserManage`、`ManagerLogin`）必须动态 `import()` 懒加载且经 `__APP_ROLE__ === 'manager'` 常量门控注册；管理接口调用（`UserManageInterface`、`ManagerLoginInterface` 等）同样门控，确保经 Rollup 死代码消除后 `dist-user` 不含任何管理端字样（构建后需 grep 复验）。
- 管理员 Web 为超级管理员权限（无需用户登录，走 `ManagerLoginInterface` + 管理端 JWT）；用户 Web 支持用户名+密码 / 模型名+API Key 登录，数据按用户/模型维度（JWT claims）过滤，无创建用户/模型能力，可配置本人路由。
- 方案详见 [`docs/项目迁移解决方案/管理员与用户Web服务双构建隔离升级方案_20260825.md`](docs/项目迁移解决方案/管理员与用户Web服务双构建隔离升级方案_20260825.md)。

### 2.6 安全红线（v2.0.56 全面安全加固起强制）
- **禁止硬编码密钥/密码/生产 IP**：JWT 密钥与管理端登录凭证只放 `LsmTokensServer.conf` 的 `security` 段（`jwtSecret`/`managerUserName`/`managerPassword`/`trustProxyHeaders`）；Python 分析脚本 DB 凭证走环境变量 `LSM_MYSQL_*`。
- **管理端（9101）所有业务接口必须经过 `api.ManagerAuthMiddleware`**：新接口在 `api.RegisterManagerAPIRoutes` 内注册即自动受保护；登录走 `/ManagerLoginInterface`。
- **用户密码只存 bcrypt 哈希**：写库前 `api.HashPassword`，校验 `api.VerifyPassword`（自动兼容旧明文并升级）。
- **接口响应禁止明文密码/完整手机号**：密码字段置空，手机号用 `api.MaskPhone`。
- **前端禁止持久化 API Key**："记住我"仅存模型名；对话历史 localStorage 上限 200 条 + 30 天过期。
- **API Key / JWT 密钥生成禁止时间戳降级**：`crypto/rand` 失败必须返回错误。
- `tmpPlan/`、`.env` 已加入 `.gitignore`，方案文档与本地密钥文件严禁入库。
- 完整规范见 [`docs/开发指南/SECURITY.md`](docs/开发指南/SECURITY.md)。

## 3. 代码结构速查

**后端 `ServerGo/` 业务域分包**（迁移已完成，旧单体文件全部按域拆分）：

| 包 | 职责 |
|---|---|
| `config/` | 配置加载（`LsmTokensServer.conf`） |
| `logger/` | 日志轮转 |
| `database/` + `models/` | DB 基础 + 业务模型 + 路由选择算法（与 cache 同包避免循环依赖） |
| `recognizer/` | agent / session / tool 识别 |
| `protocol/` | Anthropic↔OpenAI 协议转换 + SSE |
| `proxy/` | AI 代理转发 + 安全限流 |
| `api/` | REST 接口（用户端 + 管理端） |
| `spider/` | 爬虫 CDP + MCP 接口（同包避免循环依赖） |
| `websocket/` | WS 推送（ChatTotal 流式） |
| `system/` | 系统辅助（连通性 / git 信息 / 系统信息） |
| `webserver/` | SPA 静态托管 + API 路由挂载（双构建目录绑定，不再有 HTML 生成） |

**前端 `ClientWeb/`**：React + Vite 双构建；模块化页面范例 `src/pages/chat-analysis/`——ChatAnalysis 由单文件拆分为 `index.jsx` + `ChatAnalysisToolbar.jsx` + `InlineDetailRow.jsx` + `Detail{Header,Tabs,Body,Footer}.jsx` + `useChatAnalysis{Filters,Data}.js` + `constants.js` + 自检脚本 `agentToolFields.test.js`；详情展示为 DataTable 内联展开行（替代 Modal）。

**ChatAnalysis「Agent工具定义」**：列表列与详情块数据源均为交易表 `RequestTools` 字段（`request_tools`，请求体解析出的工具列表，逗号分隔）；详情块中该项独占一行、多行换行完整展示。

## 4. 工作流

1. 先读 `docs/项目迁移解决方案/` 对应阶段文档，确认设计。
2. 在对应包内实现/修改，保持包内自洽，减少跨包循环依赖。
3. 单元测试 + `go vet` 通过。
4. `./rebuild_restart_app.sh` 完整重启验证。
5. 中文 commit 提交。

## 5. 敏感配置获取

- 实际配置文件路径：工程根目录 `LsmTokensServer.conf`（勿提交）
- 脱敏模板：`LsmTokensServer.conf.example`

## 6. 本地私有 Python 工具（AI Agent 加载使用）

> 这些工具以本地私有子模块形式存在（gitlink 提交到本仓库，实体库位于本地 `/usr/local/git-local-repos/`，不开源）。
> AI Agent（Claude Code / Codex / OpenCode / pi / Hermes / OpenClaw）启动时会自动扫描项目根目录，结合本节说明即可加载。

### 6.1 python-generate-image-tool 快速用法

火山引擎方舟大模型图片生成 SDK：`ArkImageGenerator().generate_and_save(prompt, size, watermark=False)`；默认输出 `ImageOutput/{prefix}_{timestamp}_{seq:03d}.png`；最小像素 3,686,400；小图标先 `2048x2048` 生成再 `resize_image()` 二次缩放。

```bash
cd python-generate-image-tool
pip install -r requirements.txt
python -m pytest tests/ -v        # 单元测试（mock，不消耗 API 配额）
python test_generate_e2e.py       # 端到端测试（调用真实 API）
```

```python
from src import ArkImageGenerator

gen = ArkImageGenerator()
path = gen.generate_and_save(
    prompt="赛博朋克风格城市夜景，霓虹灯光",
    size="2560x1440",          # 默认；最小像素 3,686,400
    response_format="url",
    watermark=False,
    filename_prefix="cover",
)
```

- **API Key 加载优先级**（高→低）：环境变量 `ARK_API_KEY` > `.env` 中 `ARK_API_KEY` > 代码内置 `DEFAULT_API_KEY`（仓库已埋默认值，本机可直接调用）。
- **异常层级**：`ArkBaseError` → `ConfigError` / `ValidationError` / `ArkAPIError` / `NetworkError`，捕获基类即可统一处理。
- **Agent 调用约束**：模型固定 `doubao-seedream-5-0-pro-260628`（禁止修改）；HTTP 超时 120 秒；图片/音频/视频处理优先使用本机 `ffmpeg`。

## 7. SubAgent 角色定义与使用规则

统一维护在 [`AGENTS.md`](AGENTS.md)「SubAgent 角色定义与使用规则」：五类强制场景（Web 端游戏设计 / 服务器端游戏设计 / 产品设计 / 界面设计与图片生成 / 复杂功能测试与独立游戏产品），含触发条件、角色、系统词要点与四条使用原则（角色隔离、系统词定制、结果整合、并行优先）。Claude Code 同样适用。
