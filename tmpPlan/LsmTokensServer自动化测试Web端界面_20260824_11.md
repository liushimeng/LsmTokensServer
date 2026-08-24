# LsmTokensServer Web 页面自动化测试与迁移排查优化方案（第 11 轮）

> 版本：v1.0（2026-08-24）
> 生成方式：旧工程 LsmHttpAgent 源码全量比对 + 接口 curl 冒烟 + GoWebDebugTool 驱动 Headless Chrome 遍历断言
> 对应上一轮：`LsmTokensServerWeb页面自动化测试和debug解决方案_20260824_08.md`（阶段K 已修复 P0×2 / P1×1 / P2×1，阶段L/N 已补源码弹窗与移动端适配）

## 1. 测试环境

| 项 | 值 |
|----|----|
| 旧服务（参照基准） | `/usr/local/LsmHttpAgent`，管理端 `http://127.0.0.1:19101/`（并行期间端口偏移，原 9101） |
| 被测服务 | `/usr/local/LsmTokensServer/LsmTokensServer/LsmTokensServer`（管理端 9101 / 用户端 29001 HTTPS / AI 代理 29000、29003 / MCP 29002） |
| 驱动工具 | `go-web-debug-tool`（HTTP `127.0.0.1:28999`，Headless Chrome + anti_detect，自动忽略自签名证书） |
| 遍历方式 | `NewChromePage` → `ControlChromePage(eval_js)` 切 hash 路由 → `LookChromePageInfo(console, level=error)` 断言 |

## 2. 旧工程 Web 层全量清单（比对基准）

### 2.1 管理端页面（旧 server_web_manager.go 注册，共 17 页 + 12 接口组）

| 旧页面 | 新工程对应 | 状态 |
|---|---|---|
| `/`（首页） | `Home.jsx` | ✅ |
| `/UserManage` + `UserManageInterface` + `UserModelManageInterface` | `UserManage.jsx` | ✅ |
| `/DstEndPointManage` | `DstEndPointManage.jsx` | ✅ |
| `/AIRouteManage` | `AIRouteManage.jsx` | ✅ |
| `/ModelInfo` | `ModelInfo.jsx` | ✅ |
| `/ModelInfoManage` | `ModelInfo.jsx`（已合并） | ✅ |
| `/AgentInfo` | `AgentInfo.jsx` | ✅ |
| `/ProtocolConvertAnalyzer` + 8 个接口 | `ProtocolConvertAnalyzer.jsx` | ✅ |
| `/ChatAnalysis`（明细） | `ChatAnalysis.jsx` | ✅ |
| `/ChatAnalysisTotal`（汇总 + WS 流式） | `ChatAnalysisTotal.jsx` | ✅（`ChatAnalysisTotalWS` 已挂载） |
| `/ChatAnalysisSession` | `ChatAnalysisSession.jsx` | ✅ |
| `/ChatAnalysisTask` | `ChatAnalysisTask.jsx` | ✅ |
| `/ChatDialog`（**交互式对话页**） | `ChatDialog.jsx`（仅配置查看） | ⚠️ **P1 功能差距** |
| `/SpiderDataSource` + `SpiderDataSourceCrawl`（SSE AI 爬取） | `SpiderDataSource.jsx`（爬取内嵌行级弹窗） | ✅ |
| `/SpiderDailyInfo` | `SpiderDailyInfo.jsx` | ✅ |
| `/CleanupReport` | `CleanupReport.jsx` | ✅ |
| 工具栏弹窗 9 接口（BuildTime/Git/SystemInfo/SourceCode/Readme/UserInfoLog/Wiki/CertInfo/CertDownload） | `ToolbarDialogs.jsx` | ✅ |

### 2.2 用户端页面（旧 server_web_user.go 注册）

`/UserLogin`（验证码）、`/`（用户首页）、ChatAnalysis 系列 4 页、AIRoute/DstEndPoint/ModelInfo/AgentInfo 只读版、
ProtocolConverter 只读版、Spider 2 页（数据源权限过滤）、CleanupReport、ChatDialog —— 新工程均为**同一套 React 页面按角色裁剪**，路由路径与接口名完全一致（含 `CaptchaGenerate`、`UserLogoutInterface`、用户端 `ChatAnalysisTotalWS` 复用）。

### 2.3 同源 AI 代理挂载（关键机制）

旧版在管理端/用户端 mux 上挂载 `/{AgentAnthropicListenURL}/` 与 `/{AgentOpenAIListenURL}/`，
让 ChatDialog 页面 JS 用**相对路径**同源调用 AI 代理（规避 CORS / Mixed Content）。
**验证结果：新工程 `api/routes.go` 两端均已调用 `proxy.MountAIProxyHandlers(mux)`，
用户端 `UserAuthMiddleware` 亦已放行代理前缀**（实测 `9101/Anthropic/v1/messages` 与 `29001/Anthropic/v1/messages` 均返回 401 `missing Authorization header`，即已到达代理自身鉴权层）——后端无缺口。

## 3. 本轮自动化测试结果

### 3.1 接口冒烟（curl，管理端 9101，全部通过）

- 工具栏 9 接口（UserInfo/SystemInfo/Git/Readme/Wiki/SourceCode/BuildTimeLog/UserInfoLog/CertInfo）：全部 200 且数据结构正确（README 内容、Git 分支、源码文件清单、构建时间行均非空）。
- 业务 15 接口（ModelInfoManage/AIRoute/DstEndPoint/UserManage/CleanupReport/Spider×2 等）：列表动作 200 返回真实数据；ChatAnalysis 系列 4 接口正确返回参数校验错误（`缺少 user_name 或 model_name 参数`，符合预期）。
- 用户端 `CaptchaGenerate`：200，返回 `captcha_id` + base64 PNG。

### 3.2 浏览器遍历（GoWebDebugTool，管理端 9101）

16 个路由（首页 + 15 页面 + UserLogin 回落）全部渲染出正确 `page-title`，**console error = 0**。

### 3.3 发现的问题

| 级别 | 问题 | 影响 | 修复方案 |
|------|------|------|------|
| **P1** | `ChatDialog` 缺失交互式对话能力：旧版支持发消息、SSE 流式输出、系统提示词、协议切换（Anthropic/OpenAI）、消息编辑/删除、会话历史 localStorage 持久化、停止生成；新版仅展示配置 | 管理员/用户无法在 Web 内直接试跑模型链路 | 重写 `ChatDialog.jsx`：保留两步配置加载，追加完整对话区；同源相对路径 fetch，后端零改动 |

其余（路由对等性、同源代理、证书、README、构建日志、角色判定、移动端适配）复测均通过，无回归。

## 4. ChatDialog 交互式对话实现规格（对齐旧版 server_web_chat_dialog.go）

1. **两步配置**：`ChatDialogInterface action=models` → `action=config`（返回完整 `api_key`、`proxy_path`、`anthropic_proxy_path`/`openai_proxy_path`、`protocol_type`、`agent_base_url`）。
2. **发送**：
   - Anthropic（protocol_type=1）：`POST {proxyPath}/v1/messages`，`Authorization: Bearer <api_key>`、`anthropic-version: 2023-06-01`，`system` 为顶级字段；
   - OpenAI（protocol_type=2）：`POST {proxyPath}/chat/completions`，`Authorization: Bearer <api_key>`，`system` 放入 messages；
   - 一律**相对路径**同源请求（mux 已挂载代理），非流式解析 `content[0].text` / `choices[0].message.content`。
3. **流式**：`stream:true` + ReadableStream 手工解析 SSE `data:` 行（Anthropic `content_block_delta.delta.text`；OpenAI `choices[0].delta.content`），气泡实时追加；`AbortController` 支持「停止生成」。
4. **历史**：localStorage 键 `lsm_chat_history_{userName}_{modelName}`（用户端 `lsm_chat_history_user_{modelName}`），含 `systemPrompt` + 消息数组；编辑（textarea 就地替换）、删除（user 消息连同其后 assistant 回复成对删除）、清空（confirm）。
5. **偏好**：流式开关 `lsm_chat_stream_pref`；协议偏好 `lsm_chat_protocol_{user}_{model}`（仅覆盖协议类型并同步 proxyPath）。
6. **快捷键**：Enter 发送、Shift+Enter 换行。

## 5. 验证流程

1. `npm run build`（ClientWeb）→ `go vet ./...` → `go test ./...` 全绿；
2. `./rebuild_restart_app.sh` 完整重启；
3. curl 冒烟复测 §3.1 接口集；
4. GoWebDebugTool 打开 `#/ChatDialog`：加载模型列表 → 选模型加载配置 → 断言对话区（输入框/发送按钮/流式开关/系统提示词）渲染、console 0 error；
5. 中文 commit：`阶段O：ChatDialog 交互式对话迁移 ……`。

### 5.1 实际验证结果（2026-08-24 已完成）

- `npm run build`、`go vet ./...`、`go test ./...` 全部通过；`./rebuild_restart_app.sh` 完整重启成功（9101/29001/29000/29002/29003 全部监听）。
- 重启后 15 页面遍历复测：全部渲染正常、console error = 0。
- ChatDialog 端到端实测（GoWebDebugTool 驱动）：
  - 路由带参 `#/ChatDialog?user_name=…&model_name=…` 自动加载模型列表 + 对话配置 ✅
  - 对话区（系统提示词 / 输入框 / 发送 / 停止 / 流式开关 / 清空历史 / 消息编辑删除）渲染完整 ✅
  - 实发一条消息：请求经同源代理 `POST /Anthropic/v1/messages` 正确携带 Authorization 与消息体，
    上游正常受理（返回的 403 为该源站自身 billing 配额限制，非本工程缺陷），错误信息正确透出到助手气泡 ✅
  - 期间发现并修复 2 个实现缺陷：① 路由带参时未触发自动 loadModels；② React setState 异步导致
    buildRequestBody 读取旧消息数组（上游报 `messages must not be empty`）——改为显式传入发送时最新数组。

## 6. 长期建议（遗留项）

1. 对话内容渲染目前为纯文本（对齐旧版 escapeHtml），后续可引入 Markdown 渲染 + 代码高亮。
2. 旧版 API Key 本地简单加密存储（`simpleEncrypt`）本轮未迁移——新版**不再本地缓存明文 Key**（每次从 config 接口取），更安全，维持现状。
3. E2E 固化：将 §3 的遍历/冒烟脚本抽成 `ServerGo/api/e2e_web_test.go`（无 Chrome 时 skip）。
