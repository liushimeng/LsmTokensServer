# CC Switch — Agent 工具检测与 Session 识别机制详解

> **文档版本**: v1.0 (2026-08-13)
> **源码版本**: cc-switch 主分支 (2026-08-13)
> **技术栈**: Rust (axum 后端) + TypeScript/React (前端) + SQLite (数据库) + Tauri (桌面框架)

---

## 1. 概述

CC Switch 是一个桌面应用，用于管理和切换多个 AI 编程工具的 API 提供商。其核心代理服务器承担两个关键职责：

1. **Agent 工具检测** — 识别请求来自哪个 AI 工具（Claude Code、Codex CLI、Gemini 等）
2. **Session 识别** — 从请求中提取会话标识，用于关联同一对话的多个请求

### 核心设计理念差异（vs LsmTokensServer）

| 维度 | CC Switch | LsmTokensServer |
|------|-----------|--------------|
| **检测策略** | **URL 路径路由** — 不同工具打不同 API 端点 | **User-Agent 解析** — 从 UA 字符串提取工具名 |
| **Session 来源** | 按工具类型分离提取逻辑 | 协议无关抽象层 + 工具级识别器 |
| **架构定位** | 本地桌面代理（单用户） | 企业级网络代理（多用户） |
| **协议支持** | Anthropic / OpenAI / Responses / Gemini | Anthropic / OpenAI |
| **未识别处理** | 生成 UUID 兜底 | 返回空字符串 |

---

## 2. Agent 工具检测机制

### 2.1 URL 路径路由（核心检测方式）

**CC Switch 不解析 User-Agent 来识别工具**。每个 AI 工具使用特定的 API 端点路径，代理服务器通过 URL 路由将请求分发到对应的处理器。

**关键文件**: `src-tauri/src/proxy/server.rs` (`build_router` 方法)

```rust
// 路由表：URL 路径 → 处理函数 → 工具类型
.route("/v1/messages", post(handlers::handle_messages))           // Claude Code
.route("/claude/v1/messages", post(handlers::handle_messages))    // Claude Code (带前缀)
.route("/chat/completions", post(handlers::handle_chat_completions))   // Codex CLI
.route("/v1/chat/completions", post(handlers::handle_chat_completions)) // Codex CLI
.route("/responses", post(handlers::handle_responses))            // Codex CLI (Responses API)
.route("/v1/responses", post(handlers::handle_responses))         // Codex CLI
.route("/v1beta/*path", any(handlers::handle_gemini))             // Gemini
.route("/gemini/v1beta/*path", any(handlers::handle_gemini))      // Gemini (带前缀)
.route("/gemini/v1/*path", any(handlers::handle_gemini))          // Gemini (带前缀)
```

### 2.2 AppType 枚举 — 工具注册中心

**关键文件**: `src-tauri/src/app_config.rs`

CC Switch 用 Rust enum 管理所有支持的工具：

```rust
pub enum AppType {
    Claude,        // Claude Code CLI
    ClaudeDesktop, // Claude Desktop App
    Codex,         // OpenAI Codex CLI
    Gemini,        // Google Gemini CLI
    GrokBuild,     // Grok Build (xAI)
    OpenCode,      // OpenCode IDE
    OpenClaw,      // OpenClaw WebChat
    Hermes,        // Hermes CLI
}
```

### 2.3 两种代理模式

CC Switch 对不同工具采用不同的代理模式：

#### 代理模式（Proxy Mode）
请求流经 CC Switch 的代理服务器，URL 路径匹配后由处理器处理：
- **Claude** — `/v1/messages` → `handle_messages()`
- **Claude Desktop** — `/claude-desktop/v1/messages` → `handle_claude_desktop_messages()`
- **Codex CLI** — `/chat/completions`, `/responses` → `handle_chat_completions()` / `handle_responses()`
- **Grok Build** — `/grokbuild/v1/responses` → `handle_grokbuild_responses()`
- **Gemini** — `/v1beta/*path` → `handle_gemini()`

#### 附加模式（Additive Mode）
CC Switch **不代理请求**，而是直接修改工具的配置文件：
- **OpenCode** — 写入 JSON 配置到工具目录
- **OpenClaw** — 写入 JSON 配置到工具目录
- **Hermes** — 写入 YAML 配置到工具目录

```rust
impl AppType {
    pub fn is_additive_mode(&self) -> bool {
        matches!(self, AppType::OpenCode | AppType::OpenClaw | AppType::Hermes)
    }
}
```

### 2.4 User-Agent 预设（非检测用途）

**关键文件**: `src/config/userAgentPresets.ts`

CC Switch 的 User-Agent 预设**不用于检测工具类型**，而是用于上游 API 白名单绕过。当非白名单工具需要访问有 UA 白名单的上游提供商（如 Kimi Coding Plan）时，CC Switch 替换 UA 为预设值：

```typescript
const USER_AGENT_PRESETS = [
  "claude-cli/2.1.161 (external, cli)",
  "claude-code/1.0.0",
  "anthropic-cli/2.1.164",
  "Kilo-Code/1.0",
  // ...更多预设
];
```

### 2.5 协议格式转换

处理器根据工具类型进行协议转换：

| 工具 | 输入协议 | 输出协议 | 处理器 |
|------|---------|---------|--------|
| Claude | Anthropic Messages | Anthropic Messages | `handle_messages()` |
| Codex CLI | OpenAI Chat/Responses | OpenAI Chat/Responses | `handle_chat_completions()` / `handle_responses()` |
| Grok Build | OpenAI Responses | OpenAI Responses | `handle_grokbuild_responses()` |
| Gemini | Google Generative AI | Google Generative AI | `handle_gemini()` |

---

## 3. Session 识别机制

### 3.1 架构概览

**关键文件**: `src-tauri/src/proxy/session.rs`

Session 识别采用**按工具类型分离**的策略。`extract_session_id` 函数接收 URL 路由确定的 `client_format` 参数，分派到对应的提取逻辑：

```rust
pub fn extract_session_id(
    headers: &HeaderMap,
    body: &serde_json::Value,
    client_format: &str,  // 由 URL 路由确定："claude" | "codex" | "grokbuild"
) -> SessionIdResult
```

### 3.2 Session ID 来源枚举

```rust
pub enum SessionIdSource {
    MetadataUserId,     // 从 metadata.user_id 提取 (Claude 格式: user_xxx_session_yyy)
    MetadataSessionId,  // 从 metadata.session_id 提取
    Header,             // 从 HTTP 头提取
    Generated,          // 生成新 UUID 兜底
}
```

### 3.3 Claude 工具 Session 识别

**优先级**（命中即返回）：

1. **HTTP 头** `x-claude-code-session-id` 或 `claude-code-session-id`
2. **Body** `metadata.user_id`（格式: `user_xxx_session_yyy` → 提取 `yyy` 部分）
3. **Body** `metadata.session_id`
4. **兜底**: 生成新 UUID

```rust
fn extract_claude_session(headers, body) -> Option<SessionIdResult> {
    // 1. Header: x-claude-code-session-id
    for header_name in &["x-claude-code-session-id", "claude-code-session-id"] {
        if let Some(value) = headers.get(*header_name) {
            // ...
        }
    }
    // 2. Body: metadata.user_id → parse_session_from_user_id()
    // 3. Body: metadata.session_id
    extract_from_metadata(body)
}
```

**`metadata.user_id` 解析**（Claude 特有格式）：
```rust
fn parse_session_from_user_id(user_id: &str) -> Option<String> {
    // 格式: "user_john_doe_session_abc123def456"
    if let Some(pos) = user_id.find("_session_") {
        return Some(user_id[pos + 9..].to_string()); // "_session_" 长度为 9
    }
    None
}
```

### 3.4 Codex CLI / OpenAI 协议 Session 识别

**优先级**（命中即返回）：

1. **HTTP 头** `session_id` 或 `x-session-id`（长度 > 20 字符才认可）
2. **Body** `metadata.session_id`（长度 > 10 字符才认可）
3. **兜底**: 生成新 UUID，前缀 `"codex_"`

```rust
fn extract_responses_session(headers, body, prefix: &str) -> Option<SessionIdResult> {
    // 1. Headers: session_id / x-session-id
    for header_name in &["session_id", "x-session-id"] {
        if let Some(value) = headers.get(*header_name) {
            if session_id.len() > 20 {  // 长度校验
                return Some(format!("{prefix}_{session_id}"));
            }
        }
    }
    // 2. Body: metadata.session_id
    if session_id.len() > 10 {
        return Some(format!("{prefix}_{session_id}"));
    }
    None
}
```

### 3.5 Grok Build Session 识别

**优先级**（命中即返回）：

1. **HTTP 头** `x-grok-conv-id`（对话 ID，跨多轮稳定）
2. **HTTP 头** `x-grok-session-id`（Session ID，作为 conv-id 缺失时的回退）
3. **Body** `metadata.session_id`
4. **兜底**: 生成新 UUID，前缀 `"grokbuild_"`

```rust
let header_names: &[&str] = if prefix == "grokbuild" {
    &["x-grok-conv-id", "x-grok-session-id"]  // 对话 ID 优先
} else {
    &["session_id", "x-session-id"]
};
```

**注意**: `x-grok-req-id`（逐请求 ID）**不被用于 session 识别**，因为它是每请求变化的，不适合聚合。

### 3.6 未识别处理

当所有提取路径都未命中时，CC Switch **生成新 UUID** 作为 session_id：

```rust
fn generate_new_session_id() -> SessionIdResult {
    SessionIdResult {
        session_id: Uuid::new_v4().to_string(),
        source: SessionIdSource::Generated,
        client_provided: false,
    }
}
```

这与 LsmTokensServer 的行为不同 — LsmTokensServer 返回空字符串，由经济型算法决定是否生成。

### 3.7 `metadata.user_id` 的两种解析方式

| 维度 | CC Switch | LsmTokensServer |
|------|-----------|--------------|
| 格式 | `user_xxx_session_yyy`（下划线分隔） | 内嵌 JSON 字符串 `{"session_id":"xxx"}` |
| 解析方式 | `find("_session_")` + 字符串切片 | 两层 `json.Unmarshal` + 启发式扫描 |
| 适用工具 | Claude Code | 所有 OpenAI/Anthropic 客户端 |

---

## 4. 关键实现细节

### 4.1 Provider Router 与熔断器

**关键文件**: `src-tauri/src/proxy/provider_router.rs`

```rust
pub fn select_provider(&self, app_type: &str, providers: &[ProviderConfig]) -> Option<usize> {
    // 每个 (app_type, provider_index) 组合有独立的熔断器
    // 故障自动切换到下一个可用 provider
}
```

### 4.2 受保护的请求头

**关键文件**: `src/lib/requestOverrides.ts`

CC Switch 保护以下请求头不被用户覆盖（用于内部路由和识别）：
- `session_id`
- `x-client-request-id`
- `x-codex-window-id`

### 4.3 Session Manager — 文件系统扫描

**关键文件**: `src-tauri/src/session_manager/mod.rs`

CC Switch 通过并行文件系统扫描管理会话历史：

```rust
pub async fn scan_all_sessions() -> Vec<SessionInfo> {
    // 并行扫描 7 个 provider 的会话目录
    // codex, claude, opencode, openclaw, gemini, hermes, grokbuild
}
```

每个 provider 有独立的扫描器模块（`session_manager/providers/`），扫描对应工具的本地会话存储目录。

### 4.4 Proxy Takeover Status

**关键文件**: `src/types/proxy.ts`

```typescript
interface ProxyTakeoverStatus {
  claude: boolean;
  "claude-desktop": boolean;
  codex: boolean;
  gemini: boolean;
  grokbuild: boolean;
  opencode: boolean;
  openclaw: boolean;
  hermes: boolean;
}
```

每个工具独立跟踪代理接管状态，用于 UI 显示和故障转移。

---

## 5. 支持的工具对比

| 工具 | CC Switch 支持 | LsmTokensServer 支持 | CC Switch 检测方式 | LsmTokensServer 检测方式 |
|------|:---:|:---:|------|------|
| Claude Code | ✅ | ✅ | URL `/v1/messages` | UA `claude-code` |
| Claude Desktop | ✅ | ❌ | URL `/claude-desktop/v1/messages` | — |
| Codex CLI | ✅ | ✅ | URL `/responses`, `/chat/completions` | UA `codex-cli` / `codex` |
| Gemini | ✅ | ❌ | URL `/v1beta/*path` | — |
| Grok Build | ✅ | ❌ | URL `/grokbuild/v1/responses` | — |
| OpenCode | ✅ (附加模式) | ✅ | 配置文件写入 | UA 检测（待实现） |
| OpenClaw | ✅ (附加模式) | ✅ | 配置文件写入 | UA `OpenAI/JS` |
| Hermes | ✅ (附加模式) | ✅ | 配置文件写入 | UA `hermes` |
| Pi | ❌ | ✅ | — | UA `pi` |
| aider | ❌ | ✅ | — | UA `aider` |
| continue | ❌ | ✅ | — | UA `continue` |
| cline | ❌ | ✅ | — | UA `cline` |
| windsurf | ❌ | ✅ | — | UA `windsurf` |
| cursor | ❌ | ✅ | — | UA `cursor` |
| copilot | ❌ | ✅ | — | UA `copilot` |

---

## 6. Session 识别策略对比

### 6.1 LsmTokensServer 识别路径（OpenAI 协议，6 步优先级）

1. Agent 工具级（UA 触发，如 OpenClaw → 从 system content 提取 `sessionId=`）
2. HTTP 头（`x-codex-turn-metadata` JSON 头 / `X-Session-Id` / `Session-Id`）
3. `metadata.user_id` 内嵌 JSON
4. `client_metadata.session_id` / `client_metadata.thread_id`
5. `prompt_cache_key`
6. 顶层 `session_id`

### 6.2 CC Switch 识别路径（按工具类型分派）

- **Claude**: header → `metadata.user_id`(`_session_` 分隔) → `metadata.session_id` → UUID
- **Codex**: header(长度>20) → `metadata.session_id`(长度>10) → UUID
- **Grok Build**: `x-grok-conv-id` → `x-grok-session-id` → `metadata.session_id` → UUID

### 6.3 关键差异

1. **CC Switch 生成 UUID 兜底，LsmTokensServer 返回空字符串** — CC Switch 作为本地单用户代理，每个请求都需要 session 标识；LsmTokensServer 作为企业代理，空 session 表示"无需按 session 路由"。

2. **CC Switch 不做启发式扫描** — 没有 `extractSessionIDHeuristic` 等容错机制，依赖严格的 JSON 解析。

3. **CC Switch 按工具类型分派，LsmTokensServer 按协议类型分派** — CC Switch 知道确切的工具类型（由 URL 路由决定），可以针对性地查找该工具的 session 字段；LsmTokensServer 只知道协议类型（OpenAI/Anthropic），需要通用的多路径扫描。

4. **CC Switch 的 `metadata.user_id` 解析是字符串切片**（`_session_` 分隔符），LsmTokensServer 是两层 JSON 反序列化。两者处理的是不同客户端发送的不同格式。

---

## 7. 可借鉴的设计

### 7.1 URL 路径检测（高价值）

CC Switch 的 URL 路径路由比 LsmTokensServer 的 UA 解析更可靠：
- URL 路径是协议规范的一部分，不会被客户端随意修改
- UA 字符串是客户端自定义的，格式不统一

**对 LsmTokensServer 的启示**：可以结合 URL 路径 + UA 双重检测。当请求打到 `/v1/messages` 时，即使 UA 不含 `claude-code`，也可以推断为 Anthropic 协议客户端。

### 7.2 Session ID 来源追踪（中等价值）

CC Switch 的 `SessionIdSource` 枚举记录了 session ID 的来源（Header / Metadata / Generated），便于调试和日志。LsmTokensServer 当前不记录来源。

### 7.3 长度校验（低价值）

CC Switch 对 Codex 的 session ID 做长度校验（header > 20, metadata > 10），过滤短值误匹配。LsmTokensServer 当前没有这种校验。

### 7.4 受保护请求头（中等价值）

CC Switch 保护 `session_id` 等请求头不被用户覆盖。LsmTokensServer 的 request override 机制可以借鉴。

---

## 8. 源码文件索引

| 文件路径 | 功能 |
|---------|------|
| `src-tauri/src/proxy/server.rs` | URL 路由定义（核心检测） |
| `src-tauri/src/proxy/session.rs` | Session ID 提取逻辑 |
| `src-tauri/src/proxy/handlers.rs` | 各工具请求处理器 |
| `src-tauri/src/proxy/handler_context.rs` | RequestContext 创建 |
| `src-tauri/src/proxy/provider_router.rs` | Provider 选择与熔断器 |
| `src-tauri/src/app_config.rs` | AppType 枚举定义 |
| `src/config/userAgentPresets.ts` | UA 预设（非检测，白名单绕过） |
| `src/config/codexProviderPresets.ts` | Codex 配置模板 |
| `src/config/opencodeProviderPresets.ts` | OpenCode 配置模板 |
| `src/config/openclawProviderPresets.ts` | OpenClaw 配置模板 |
| `src/config/hermesProviderPresets.ts` | Hermes 配置模板 |
| `src/lib/requestOverrides.ts` | 请求覆盖保护 |
| `src/lib/userAgent.ts` | UA 验证（非检测） |
| `src/types/proxy.ts` | ProxyTakeoverStatus 类型 |
| `src-tauri/src/session_manager/mod.rs` | 文件系统会话扫描 |
