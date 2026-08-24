# LsmTokensServer 服务代码文件的迁移和重构方案

> 日期：2026-08-24
> 范围：AI 代理端口（42900 / 42903，对应旧工程 29000 / 29003）
> 现象：客户端提示词 `Cannot connect to API: Unable to connect. Is the computer able to access the url?`
> 目标：基于旧工程 LsmHttpAgent 的成熟实现，补齐 / 优化新工程 LsmTokensServer 的 AI 代理链路

---

## 1. 现状调查

### 1.1 工程结构对比

| 关注点 | 旧工程 LsmHttpAgent | 新工程 LsmTokensServer | 评估 |
|---|---|---|---|
| AI 代理端口 | 29000 / 29003 | **42900 / 42903**（已迁至 40000 段） | ✅ 已完成（CLAUDE.md / INDEX.md 文档记录） |
| 包结构 | `package main` 单包 | `proxy / protocol / recognizer / models / config / logger / database` | ✅ 完成 |
| 监听路由 | `/Anthropic`, `/OpenAI` | `/Anthropic`, `/OpenAI` | ✅ 一致 |
| `Mux` 复用 HTTPS/HTTP | 已实现 | 已实现 | ✅ 一致 |
| `forwardWithRetry` 透明重试 | 已实现 | 已实现（迁移自 models 包，算法选择器同包） | ✅ 一致 |
| `recognizer` Agent / Session / Tool | 单包 | `recognizer` 子包 | ✅ 一致 |
| 协议转换（Anthropic↔OpenAI） | 单包 `protocol_*` | `protocol` 子包 | ✅ 一致 |
| SSE 解析 | `protocol_sse.go` | `protocol/sse.go`（已迁出） | ✅ 迁移完成 |
| 智能路由（稳定/经济/指定） | `agent_algorithm*.go` | `models/agent_algorithm*.go` | ✅ 一致 |
| API Key 提取（Bearer） | `extractAPIKey` | `proxy.extractAPIKey` | ✅ 一致 |
| URL 拼接（路径去重 / v1 折叠） | `buildTargetURL` | `proxy.buildTargetURL` | ✅ 一致 |
| 模型名替换（字节级保序） | `replaceModelInBody` | `proxy.replaceModelInBody` | ✅ 一致 |
| 协议转换 URL 改写 | `rewriteProtocolConvertedRelativePath` | `proxy.rewriteProtocolConvertedRelativePath` | ✅ 一致 |
| 故障转移 + Session 粘性 | 已实现 | 已实现 | ✅ 一致 |
| 合成 session（OpenAI/Python/opencode） | 已实现 | 已实现 | ✅ 一致 |
| 协议头清理（hop-by-hop + auth 转发链） | `shouldForwardProxyHeader` | `protocol.ShouldForwardProxyHeader` | ✅ 一致 |
| 鉴权失败限流（5/min/IP） | 已实现 | 已实现 | ✅ 一致 |
| `redactAuthorizationBearerHeaderText` | 本地 | 迁至 `models/security_redact.go` | ✅ 一致 |

### 1.2 实测验证（迁移完成度证据）

新工程实际启动后已通过以下场景验证：

```text
# Claude-model + Anthropic 路径 - 成功（流式 / 非流式均 OK）
$ curl -X POST http://127.0.0.1:42900/Anthropic/v1/messages \
    -H "Authorization: Bearer sk-92f3d3ccb306aaabe9f2460c07ba3839e71caced71dd61a7179d8fdc72980137" \
    -d '{"model":"Claude-model","max_tokens":30,"messages":[{"role":"user","content":"say hi"}]}'
→ 成功路由到 LongCat-2.0（dst endpoint id=51），返回完整 Anthropic 协议响应

# 流式 + claude-code UA - 成功
$ curl -H "User-Agent: claude-code/1.0.0" ... -d '{"stream":true,...}'
→ 返回正确的 SSE 事件流（message_start / content_block_* / message_delta / message_stop）

# OpenAI 路径访问只有 Anthropic 路由的 model - 按预期 400 拒绝
→ "No route configured for model 'Claude-model' protocol"（旧工程行为一致）

# 非法 API Key 格式 - 按预期 401
→ "Invalid API Key"（旧工程行为一致）
```

**结论**：核心代理链路（API Key 提取 → 缓存查找 → 路由选择 → 源站转发 → SSE 透传 → 协议转换）**功能完整、与旧工程一致**，迁移是成功的。

### 1.3 用户报障“Cannot connect to API”的可能成因分析

客户端报障 `Cannot connect to API: Unable to connect. Is the computer able to access the url?` 通常来自 Claude Code / Cursor / Cline 等 IDE 插件，常见成因：

| # | 成因 | 是否已在迁移中修复 | 备注 |
|---|---|---|---|
| A | 客户端访问的是 **旧端口 29000/29003**，但新工程已迁到 42900/42903 | ❌ **未在代码层面**（需要用户/前端配置切换） | 见 §2.1 |
| B | API Key 误输入或被路由清理失效 | ✅ 已处理（缓存查找失败 → 401） | — |
| C | 模型名未在 cache（用户新建的 model 还没刷新缓存） | ✅ 已处理（400 "Invalid API Key"） | — |
| D | 源站 endpoint `disabled`（状态=0） | ✅ 已处理（forwardWithRetry 跳过并报 403） | — |
| E | 源站 URL 拼接错误（basePath 含 /v1，relative 又带 /v1） | ✅ 已修复（旧工程注释：`避免产生 /openai/v1/v1/responses 这种重复 /v1 的 404 路径`） | 见 §2.2 |
| F | 协议转换时仅转换 body，但 URL 仍走源协议路径 | ✅ 已修复（`rewriteProtocolConvertedRelativePath` 同步改写 /v1/messages ↔ /v1/chat/completions） | 见 §2.3 |
| G | 上游 SSE 聚合转换后未包装回客户端 SSE 事件流 | ✅ 已修复（旧工程注释：`此前直接把 JSON blob 配上 text/event-stream 发给客户端，协议自相矛盾`） | 见 §2.4 |
| H | Anthropic 空 content 响应被原样转 SSE → 不是合法流 | ✅ 已修复（自动补一对空 text 块） | — |
| I | tool_use 块缺 id/name/input → 客户端拒绝 | ✅ 已修复（content_block_start 携带完整字段） | — |
| J | UA 不识别 → Session 合成路径未生效 | ✅ 已处理（`IsSyntheticSessionEligibleAgent` 已包含 opencode/OpenAI/Python 等） | — |
| K | HTTP/2 客户端期望 h2c，新工程用 HTTP/1.1 | ⚠️ 部分场景 | 见 §2.5 |
| L | 客户端走的 URL 是 HTTPS 42903，但本地 DNS / TLS 证书链不完整 | ⚠️ 部分场景 | 见 §2.6 |

### 1.4 已发现的代码冗余（迁移期遗留，需清理）

新工程 `proxy/server_http_ai_proxy_utils.go` 中存在 **重复定义**：

- 文件中既调用 `protocol.ParseSSEEvents`（§170），又重复定义 `func ParseSSEEvents(text string) []protocol.SSEEvent`（§192）和 `trimIncompleteTrailingUTF8`（§229）。
- 由于 Go 同包内不允许同名函数重复定义，**编译器以先定义的 `protocol.ParseSSEEvents` 为准**，proxy 包内的 `ParseSSEEvents` 实际成为死代码。

风险：未来跨包引用 `proxy.ParseSSEEvents`（例如测试）会导致导入冲突；且 `proxy.trimIncompleteTrailingUTF8` 与 `protocol.trimIncompleteTrailingUTF8` 重复维护。

---

## 2. 待修复 / 优化项（按优先级）

### 2.1 ⭐ P0：在日志与启动横幅中明确端口提示（避免用户混淆 29000 vs 42900）

**问题**：用户在客户端配置里填了旧端口 29000，自然连不上。

**修复**：
- 启动横幅增加一条 `[PROXY] !!! 如果客户端配置的是旧端口 29000，请改为新端口 42900` 的醒目提示。
- `/Anthropic/v1/messages` 收到请求时，若客户端仍以 `Host: ...29000...` 形式提供，则返回明确指引。

实施位置：`ServerGo/proxy/server_http_ai_proxy.go` `StartAIProxyService`。

### 2.2 ⭐ P1：清理 `proxy/server_http_ai_proxy_utils.go` 中的 SSE 重复定义

**问题**：见 §1.4。

**修复**：
- 删除 `proxy/server_http_ai_proxy_utils.go` 中的 `ParseSSEEvents`（§192-§224）和 `trimIncompleteTrailingUTF8`（§229-§265），统一使用 `protocol.ParseSSEEvents`。
- 删除迁移期遗留注释 `// protocol.SSEEvent 表示一个 SSE 事件 // parseTokensFromResponseBody...`（§105-§107）。

### 2.3 ⭐ P1：补强 `dst endpoint URL` 的拼接健壮性

旧工程已实现 `buildTargetURL` 处理以下三种折叠，**新工程已迁移**：

- `basePath` 末尾等于 `relativePath` → 取 basePath（避免 `/coding/v1/messages/chat/completions`）。
- `basePath` 以 `/v1` 结尾且 `relativePath` 以 `/v1/` 开头 → 剥掉 `relativePath` 的 `/v1` 前缀（避免 `/openai/v1/v1/responses`）。
- `basePath` 末尾 `/` 与 `relativePath` 起始 `/` 去重。

**额外加固建议**：当 basePath 完全等于 `/v1/<x>` 而 relativePath 也等于 `/v1/<x>` 时直接短路（防止某些 OpenAI 兼容网关把路径写死成 `/openai/v1/v1/messages`）。

### 2.4 ⭐ P1：SSE 转换后重新包装为客户端协议事件流

旧工程 § `convertProxyResponse` 已实现 `wrapConvertedResponseAsSSE`，**新工程已迁移**。

**额外加固**：
- `wrapAnthropicResponseAsSSE`：检测上游响应 `model` 字段被替换为实际路由模型名（如 `LongCat-2.0`）时，**保留**（与客户端期望保持一致；若客户端校验严格，可改回用户 model 名）。当前实现保留上游 model —— 与旧工程一致。
- 增加 Anthropic 流式首条 `message_start.message.model` 与 `message_delta.usage` 输出_tokens 兜底（防止某些 OpenAI 兼容厂商的 SSE 缺少 usage 块导致客户端卡住）。

### 2.5 ⭐ P2：HTTP 客户端连接池参数调优

旧 / 新工程共享 `sharedHTTPClient` 配置：

```go
Timeout: 300s
Transport: {
    MaxIdleConns:        100,
    MaxIdleConnsPerHost: 10,
    IdleConnTimeout:     90s,
}
```

**加固建议**：
- 新增 `DialContext` 超时（当前未配置，单次 TCP 握手可阻塞到 300s 总超时）。
- 新增 `TLSHandshakeTimeout: 10s`（防止 TLS 卡死）。
- 新增 `ResponseHeaderTimeout: 60s`（与 aiProxyServer.ReadTimeout 对齐）。
- 新增 `ExpectContinueTimeout: 1s`（防止慢客户端卡死上传）。

### 2.6 ⭐ P2：HTTPS 代理证书路径多级回退

旧工程仅 `resolvePath` 一次；新工程已实现两段回退（可执行文件目录 → 父目录）。**OK，无需额外改动**。

### 2.7 ⭐ P2：协议转换头清理统一收敛

旧 / 新工程都有 `ShouldForwardProxyHeader`。**额外加固**：
- 当客户端协议是 Anthropic 时，丢弃上游 OpenAI 的 `openai-organization`、`openai-project` 等头。
- 当客户端协议是 OpenAI 时，丢弃上游 Anthropic 的 `anthropic-version`/`anthropic-beta` 等头。

实施位置：`protocol/header_forward.go`。

### 2.8 ⭐ P3：增强诊断日志

- `forwardWithRetry` 在每次重试时打印：endpoint id、URL、目标 model、源 model、协议转换类型。
- `IsFailoverError` 触发时记录 `endpoint=http_code+body_snippet`。
- 增加一条启动横幅：`[PROXY] Cached routes: model=<m> protocol=<a|o> endpoints=[...]`（每个 model 一行）—— 排查“明明配了源站但仍报 No route configured”时尤为有用。

### 2.9 ⭐ P3：增加 connect 失败的明确错误响应

当上游返回 `connection refused / timeout / DNS failure` 时（而不是 HTTP 4xx/5xx），客户端收到的就是裸 `connection error`。改为返回 `502 Bad Gateway` + JSON：

```json
{"error":"Bad Gateway","message":"upstream <endpoint_id> connect failed: <err>"}
```

实施位置：`proxy/server_http_ai_proxy_utils.go` 的 `forwardWithRetry`。

---

## 3. 测试矩阵（迁移期回归）

| # | 客户端 / 场景 | 协议 | UA | 期望 |
|---|---|---|---|---|
| 1 | Claude Code 1.x | Anthropic | `claude-code/1.0.x` | 200 + 流式 SSE |
| 2 | Cursor | Anthropic | `cursor/x.y.z` | 200 |
| 3 | Cline | Anthropic | `cline/x.y.z` | 200 |
| 4 | OpenAI Python SDK | OpenAI | `OpenAI/Python x.y.z` | 200（需 route 配 OpenAI 协议） |
| 5 | aider | Anthropic | `aider/x.y.z` | 200 |
| 6 | opencode | Anthropic | `opencode/x.y.z` | 200（合成 session） |
| 7 | LongCat | OpenAI | `OpenAI/JS x.y.z` | 200 |
| 8 | Kimi / coding | Anthropic | `Kilo/x.y.z` | 200（tool_use 流） |
| 9 | basePath 含 /v1 的源站（如 `/openai/v1`） | 双向 | — | URL 正确折叠 |
| 10 | 协议转换 OpenAI→Anthropic | 双向 | — | URL + body + header 三者同步改写 |
| 11 | 上游 SSE 响应 → 客户端 Anthropic | 双向 | — | 重新包装为合法 Anthropic 流 |
| 12 | 上游非 SSE 响应 → 客户端要求 stream | 双向 | — | 仍以 JSON 返回 + Content-Type: text/event-stream 包装 |
| 13 | endpoint 全 disabled | 任意 | — | 403 Forbidden |
| 14 | 所有 endpoint 连接失败 | 任意 | — | 502 Bad Gateway + JSON |
| 15 | 客户端走旧端口 29000 | 任意 | — | 在新工程上连不上（用户需改配置）—— 在启动横幅明确提示 |

---

## 4. 实施步骤

### 阶段 A：清理重复代码（P1）

1. 删除 `proxy/server_http_ai_proxy_utils.go` 中重复的 `ParseSSEEvents`、`trimIncompleteTrailingUTF8` 与过期注释。
2. `go build ./... && go vet ./... && go test ./...` 全绿。
3. 提交：`清理：proxy 包内 SSE 解析重复定义，统一使用 protocol.ParseSSEEvents`。

### 阶段 B：HTTP 客户端连接池加固（P2）

1. 修改 `proxy/server_http_agent_proxy.go` 中 `sharedHTTPClient.Transport` 配置（增加 `DialContext`、`TLSHandshakeTimeout`、`ResponseHeaderTimeout`）。
2. 新增 `proxy/server_http_ai_proxy_utils.go` 测试覆盖 connect failure 路径。
3. 提交：`优化：sharedHTTPClient 增加握手/首包超时，避免上游阻塞拖垮代理`。

### 阶段 C：诊断日志与启动横幅（P0/P3）

1. `proxy/server_http_ai_proxy.go` 启动时打印旧端口提示。
2. `proxy/server_http_ai_proxy_utils.go` 的 `forwardWithRetry` 增强日志。
3. 提交：`优化：AI 代理启动横幅与重试日志可观测性增强`。

### 阶段 D：协议头双向清理（P2）

1. `protocol/header_forward.go` 增加协议感知头过滤。
2. 单元测试覆盖。
3. 提交：`优化：协议转换时双向清理上游私有头，避免泄露到客户端`。

### 阶段 E：Connect 失败 JSON 响应（P3）

1. `proxy/server_http_ai_proxy_utils.go` `forwardWithRetry` 捕获底层连接错误并返回 502。
2. 测试覆盖。
3. 提交：`优化：上游连接失败时返回 502 + JSON 而非裸错误，提升客户端可读性`。

### 阶段 F：编译 + 启动 + 端到端验证 + 提交

1. `./rebuild_restart_app.sh --build-only`。
2. 通过 curl 跑测试矩阵 §3 的关键场景（1, 2, 3, 4, 9, 10, 11, 12, 13, 14）。
3. `./rebuild_restart_app.sh`（按需切换；迁移期默认 `--build-only`）。
4. 中文 commit：`优化：AI 代理链路清理与可观测性增强（端口横幅/SSE去重/连接池加固/connect失败JSON）`。

---

## 5. 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| 协议头清理误删必要头（如 `anthropic-version`） | 低 | 客户端拒绝 | 单元测试 + 灰度 |
| HTTP 客户端超时调小导致正常流被截断 | 低 | 部分请求 502 | 与旧工程一致保守配置（300s 总超时不变） |
| 删除 proxy 重复定义破坏测试 | 低 | 构建失败 | `go vet` + `go test` 先于 commit |
| 启动横幅日志刷屏 | 低 | 启动慢 | 仅启动时打一次 |
| 切到真实端口 42900/42903 影响现有客户端 | 低 | 旧客户端无法访问 | 旧工程 LsmHttpAgent 仍在运行；过渡期并存 |

---

## 6. 结论

新工程 LsmTokensServer 的 AI 代理迁移 **已经基本完整**，核心代理链路与旧工程一致，实测可用（已验证 Claude-model + Anthropic 流式与非流式均成功）。

本方案聚焦于：
1. **P0 用户感知**：启动横幅明确提示端口变化（解决旧端口 29000→42900 带来的客户端配置混淆）。
2. **P1 代码质量**：清理迁移期遗留的重复 SSE 解析定义。
3. **P2 健壮性**：HTTP 客户端连接池加固、协议头双向清理。
4. **P3 可观测性**：增强 connect 失败的 JSON 响应与重试日志。

预计工作量：阶段 A ~ 30 min；阶段 B/C/D/E 各 ~ 30-60 min；阶段 F 验证 + 提交 ~ 30 min。
