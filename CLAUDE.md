# CLAUDE.md - LsmTokensServer 工程约束与代码规范（Claude Code 上下文）

> 供 Claude Code / Claude Agent 工具加载使用。通用 AI Agent 规范见 [`docs/开发指南/AGENT.md`](docs/开发指南/AGENT.md)。
> 完整源码索引见 [`docs/开发指南/AGENT_INDEX.md`](docs/开发指南/AGENT_INDEX.md)。

## 1. 工程定位

LsmTokensServer 是开源 AI Tokens 代理与管理服务，由私有项目 LsmHttpAgent 迁移重构而来，前后端分离架构。

- 后端：`ServerGo/`（Go，按业务域分包）
- 前端：`ClientWeb/`（React + Vite）
- 文档：`docs/`
- 脚本：`rebuild_restart_app.sh`

## 2. 必须遵守的规则

### 2.1 编译 / 启动必须走脚本
所有涉及编译、启动、重启的操作，必须通过 `./rebuild_restart_app.sh`：
```bash
./rebuild_restart_app.sh --build-only           # 仅编译（不启动、不占端口）
./rebuild_restart_app.sh --build-only --skip-web # 仅编译后端
./rebuild_restart_app.sh                        # 完整重启（编译 + 运行）
```
禁止直接 `go build` 或 `nohup ./LsmTokensServer`。

### 2.2 旧服务不得停止
- **旧服务 `/usr/local/LsmHttpAgent` 必须持续运行**，在 AI 代理端口（29000/29003）全量验证通过并人工确认之前，禁止停止。
- 端口规范：管理端 `9101`、AI 代理 `29000`（HTTP）/`29003`（HTTPS）、用户端 `29001`、MCP `29002`、爬虫 CDP `9222`。
- 新旧服务并行期间需保持端口不冲突（旧服务已偏移至 19000 段，新服务占用规范端口）。

### 2.3 敏感信息严禁提交
以下文件/目录绝不能提交到 git（已在 `.gitignore` 中）：
- `LsmTokensServer.conf`（MySQL 密码、openClaw API Key 等）
- `server.crt` / `server.key`（TLS 证书私钥）
- `*.log`、二进制、pid 文件
- `go-web-debug-tool/`（本地私有子模块）
- `python-generate-image-tool/`（本地私有子模块，图片生成 Python SDK）
- `node_modules/`、`ClientWeb/dist/`

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
4. `./rebuild_restart_app.sh --build-only` 编译验证。
5. 中文 commit 提交。

## 5. 敏感配置获取

- 实际配置文件路径：工程根目录 `LsmTokensServer.conf`（勿提交）
- 脱敏模板：`LsmTokensServer.conf.example`

## 6. 本地私有 Python 工具（AI Agent 加载使用）

> 这些工具以本地私有子模块形式存在（gitlink 提交到本仓库，实体库位于本地 `/usr/local/git-local-repos/`，不开源）。
> AI Agent（Claude Code / Codex / OpenCode / pi / Hermes / OpenClaw）启动时会自动扫描项目根目录，结合本节说明即可加载。

| 工具目录 | 用途 | 关键能力 |
|----------|------|----------|
| `python-generate-image-tool/` | 火山引擎方舟大模型 `doubao-seedream-5-0-pro-260628` 图片生成 SDK | `ArkImageGenerator().generate_and_save(prompt, size, watermark=False)`；默认输出 `ImageOutput/{prefix}_{timestamp}_{seq:03d}.png`；最小像素 3,686,400；可调用 `resize_image()` 二次缩放得到小图标 |

### 6.1 python-generate-image-tool 快速用法

```bash
# 安装依赖
cd python-generate-image-tool
pip install -r requirements.txt

# 单元测试（mock，不消耗 API 配额）
python -m pytest tests/ -v

# 端到端测试（调用真实 API）
python test_generate_e2e.py
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
print("图片已保存:", path)
```

### 6.2 API Key 加载优先级（高 → 低）

1. 环境变量 `ARK_API_KEY`
2. `.env` 文件中的 `ARK_API_KEY`
3. 代码内置 `DEFAULT_API_KEY`（仓库内已埋默认值，本机可直接调用）

### 6.3 异常层级

`ArkBaseError` → `ConfigError` / `ValidationError` / `ArkAPIError` / `NetworkError`，捕获基类即可统一处理。

### 6.4 Agent 调用约束

- **模型固定**：禁止修改 `model` 字段，必须为 `doubao-seedream-5-0-pro-260628`。
- **超时**：HTTP 请求超时 120 秒（`ArkImageGenerator.REQUEST_TIMEOUT_SECONDS`）。
- **小图标 workaround**：API 拒绝低于 3,686,400 像素，先用 `2048x2048` 生成，再用 `resize_image()` 缩到目标尺寸。
- **多媒体处理**：图片/音频/视频优先使用本机 `ffmpeg`。
