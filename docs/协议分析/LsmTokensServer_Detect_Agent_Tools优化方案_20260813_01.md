# LsmTokensServer Agent 工具检测与 Session 识别优化方案

> **版本**: v1.0
> **日期**: 2026-08-13
> **参考**: [`Switchyard_Detect_Agent_Tools_Sessions.md`](Switchyard_Detect_Agent_Tools_Sessions.md)
> **目标版本**: v2.0.73

---

## 1. 现状评估

### 1.1 当前实现

| 模块 | 文件 | 能力 |
|------|------|------|
| Agent 名称识别 | `recognizer_agent_name.go` | 从 UA 提取 name/info，OpenAI 细化 |
| Session 识别 | `recognizer_session_id.go` | 协议分发 + 5 级 OpenAI / 3 级 Anthropic |
| 工具名提取 | `recognizer_anthropic_tool_call.go` / `recognizer_openai_function_call.go` | 从 body 提取工具名列表 |
| 高阶 Agent 白名单 | `agent_algorithm_economic.go` | 5 个 Agent（claude-cli, openai/js, openai/python, opencode, kilo-code） |
| 合成 Session 白名单 | `agent_algorithm_economic.go` | 2 个 Agent（opencode, openai/python） |

### 1.2 识别的 Agent（当前 6 个）

| Agent | UA 模式 | 高阶白名单 | 合成 Session |
|-------|---------|-----------|-------------|
| claude-cli | `claude-cli` / `claude-cli/VERSION` | ✅ | ❌ |
| OpenAI/JS (OpenClaw) | `OpenAI/JS` | ✅ | ❌ |
| OpenAI-Python | `OpenAI-Python/VERSION` | ✅ | ❌ |
| opencode | `opencode` / `opencode/VERSION` | ✅ | ✅ |
| Kilo-Code | `Kilo-Code` / `kilo-code/VERSION` | ✅ | ❌ |
| Codex CLI | 多种 UA | ❌ | ❌ |

### 1.3 与 Switchyard 的差距

| 维度 | Switchyard | LsmTokensServer | 差距 |
|------|-----------|-------------|------|
| Session 识别来源 | Header-first（30+ 候选头） | Body-first + 3 个 Anthropic 头兜底 | **缺少 Claude Code / Codex CLI 专属头识别** |
| Sub-Agent 检测 | 三重信号 | 无 | **完全缺失** |
| 工具行为分类 | 5 类 + Bash 子命令模式 | 仅提取工具名列表 | **无分类** |
| 测试结果检测 | 通过/失败短语匹配 | 无 | **完全缺失** |
| 覆盖 Agent 数 | 7+ 生态 | 6 个 | **缺少 Pi / Hermes / Aider / Cline 等** |

---

## 2. 优化方案

### 2.1 优化 ①：Header-Based Session 识别增强（P0）

**目标**：借鉴 Switchyard 的 header-first 策略，在现有 body-first 基础上增加 HTTP 头识别路径，提升 Session 识别覆盖率。

**修改文件**: `recognizer_session_id.go`

#### 2.1.1 Anthropic 协议新增头识别

在 `anthropicSessionRecognizer.Recognize` 中，`parseSessionIDFromAnthropicHeaders` 之前新增：

```go
// 新增：Claude Code 专属头
// x-claude-code-session-id 是 Claude Code CLI 发送的会话标识头
for _, h := range []string{
    "X-Claude-Code-Session-Id",  // Claude Code CLI 原生头
} {
    if v := strings.TrimSpace(headers.Get(h)); v != "" {
        return v
    }
}
```

**当前已有的 Anthropic 头**（保留）：
- `anthropic-beta: ...; session-id=xxx; ...`
- `X-Session-Id`
- `X-Anthropic-Session-Id`

**新增的 Anthropic 头**：
- `X-Claude-Code-Session-Id`：Claude Code CLI 原生发送（与 Switchyard 的 `x-claude-code-session-id` 对应）

#### 2.1.2 OpenAI 协议新增头识别

在 `openAISessionRecognizer.Recognize` 中，agent tool recognizer 之后、`metadata.user_id` 之前新增头识别路径：

```go
// 新增：OpenAI 协议下的头识别路径
// 1. x-codex-turn-metadata JSON 头（Codex CLI 原生）
if sid := parseSessionIDFromCodexTurnMetadata(headers); sid != "" {
    return sid
}
// 2. x-session-id（OpenCode 原生）
if v := strings.TrimSpace(headers.Get("X-Session-Id")); v != "" {
    return v
}
// 3. session-id（通用 Codex 兼容）
if v := strings.TrimSpace(headers.Get("Session-Id")); v != "" {
    return v
}
```

#### 2.1.3 新增 Codex Turn Metadata 解析函数

```go
// parseSessionIDFromCodexTurnMetadata 从 x-codex-turn-metadata JSON 头提取 session_id。
// Codex CLI 将多个元数据字段打包在一个 JSON 对象头中。
// 解析 x-codex-turn-metadata.session_id（优先）和 x-codex-turn-metadata.thread_id（备选）。
func parseSessionIDFromCodexTurnMetadata(headers http.Header) string {
    if headers == nil {
        return ""
    }
    raw := headers.Get("X-Codex-Turn-Metadata")
    if raw == "" {
        return ""
    }
    var meta map[string]interface{}
    if err := json.Unmarshal([]byte(raw), &meta); err != nil {
        return ""
    }
    // 优先 session_id
    if v, ok := meta["session_id"]; ok {
        if s, ok := v.(string); ok {
            if sid := strings.TrimSpace(s); sid != "" {
                return sid
            }
        }
    }
    // 备选 thread_id
    if v, ok := meta["thread_id"]; ok {
        if s, ok := v.(string); ok {
            if tid := strings.TrimSpace(s); tid != "" {
                return tid
            }
        }
    }
    return ""
}
```

#### 2.1.4 头识别优先级（最终序）

**OpenAI 协议**（命中即返回）：
1. Agent 工具级（UA 触发，如 OpenClaw）
2. **`x-codex-turn-metadata` JSON 头**（新增）
3. **`x-session-id` / `session-id` 头**（新增）
4. `metadata.user_id` 内嵌 JSON
5. `client_metadata.session_id` / `client_metadata.thread_id`
6. `prompt_cache_key`
7. 顶层 `session_id` 字段

**Anthropic 协议**（命中即返回）：
1. `metadata.user_id` 内嵌 JSON
2. **`x-claude-code-session-id` 头**（新增）
3. `anthropic-beta` 头中 `session-id=xxx`
4. `X-Session-Id` / `X-Anthropic-Session-Id` 头
5. 顶层 `session_id` 字段

---

### 2.2 优化 ②：扩展 Agent 检测模式（P1）

**目标**：增加更多 AI Agent 工具的 UA 识别支持。

**修改文件**: `recognizer_agent_name.go`

当前 `RecognizeAgentTool` 只对 "OpenAI" 前缀做特殊处理。需要对更多 Agent 增加已知模式的精细化识别。

#### 2.2.1 新增已知 Agent 前缀映射表

```go
// knownAgentPrefixes 已知 Agent 工具的 UA 前缀到标准名称映射。
// 用于当 UA 解析出的 name 与实际 Agent 名称不一致时做规范化。
// key 为小写前缀，value 为标准名称。
var knownAgentPrefixes = map[string]string{
    "claude-code":    "claude-code",     // Claude Code CLI 新版 UA
    "anthropic-cli":  "claude-code",     // Anthropic CLI 变体
    "codex":          "codex-cli",       // OpenAI Codex CLI
    "pi":             "pi",              // Pi AI（Anthropic 的 Pi 助手）
    "hermes":         "hermes",          // Hermes CLI
    "aider":          "aider",           // Aider AI pair programming
    "continue":       "continue",        // Continue IDE extension
    "cline":          "cline",           // Cline VS Code extension
    "windsurf":       "windsurf",        // Windsurf IDE (Codeium)
    "cursor":         "cursor",          // Cursor IDE
    "copilot":        "copilot",         // GitHub Copilot
}
```

#### 2.2.2 修改 `RecognizeAgentTool` 逻辑

在现有 OpenAI 细化逻辑之后，增加对已知前缀的规范化处理：

```go
// 现有 OpenAI 细化逻辑
if strings.EqualFold(name, "OpenAI") {
    return refineOpenAIAgentName(userAgent, name)
}

// 新增：已知 Agent 前缀规范化
if canonical, ok := lookupKnownAgentPrefix(name); ok {
    return AgentToolRecognitionResult{
        AgentToolName: canonical,
        AgentToolInfo: info,
    }
}
```

**新增辅助函数**：
```go
func lookupKnownAgentPrefix(name string) (string, bool) {
    lower := strings.ToLower(name)
    if canonical, ok := knownAgentPrefixes[lower]; ok {
        return canonical, true
    }
    // 前缀匹配（如 "claude-code/1.0" → "claude-code"）
    for prefix, canonical := range knownAgentPrefixes {
        if strings.HasPrefix(lower, prefix+"/") {
            return canonical, true
        }
    }
    return "", false
}
```

---

### 2.3 优化 ③：扩展高阶 Agent 白名单和合成 Session 白名单（P1）

**目标**：让更多 Agent 享受高阶路由和合成 Session 粘性。

**修改文件**: `agent_algorithm_economic.go`

#### 2.3.1 扩展高阶 Agent 白名单

```go
var EconomicAdvancedAgentWhiteList = map[string]bool{
    "claude-cli":    true,
    "claude-code":   true,  // 新增：Claude Code 新版 UA
    "openai/js":     true,
    "openai/python": true,
    "opencode":      true,
    "kilo-code":     true,
    "codex-cli":     true,  // 新增：OpenAI Codex CLI
    "pi":            true,  // 新增：Pi AI
    "hermes":        true,  // 新增：Hermes CLI
    "aider":         true,  // 新增：Aider
    "continue":      true,  // 新增：Continue IDE
    "cline":         true,  // 新增：Cline
    "windsurf":      true,  // 新增：Windsurf
    "cursor":        true,  // 新增：Cursor IDE
    "copilot":       true,  // 新增：GitHub Copilot
}
```

#### 2.3.2 扩展合成 Session 白名单

```go
var EconomicSyntheticSessionEligibleAgents = map[string]bool{
    "opencode":      true,
    "openai/python": true,
    "codex-cli":     true,  // 新增：Codex CLI
    "hermes":        true,  // 新增：Hermes
    "aider":         true,  // 新增：Aider
    "continue":      true,  // 新增：Continue
    "cline":         true,  // 新增：Cline
}
```

---

### 2.4 优化 ④：新增测试用例（P1）

**新增文件**: `v2073_agent_detection_enhance_test.go`

测试用例覆盖：

1. **新 Agent UA 解析**：
   - `TestRecognizeAgentTool_NewAgents` — codex/pi/hermes/aider/continue/cline/windsurf/cursor/copilot
   - `TestRecognizeAgentTool_ClaudeCodeUA` — "claude-code/1.0" 和 "Claude-Code/2.0"

2. **高阶白名单扩展**：
   - `TestIsAdvancedAgentToolName_Expanded` — 所有新增 Agent 都应返回 true
   - `TestIsAdvancedAgentToolName_StillRejectsUnknown` — 未知 Agent 仍返回 false

3. **合成 Session 白名单扩展**：
   - `TestIsSyntheticSessionEligibleAgent_Expanded` — 新增 Agent 应返回 true

4. **Header-based Session 识别**：
   - `TestParseSessionIDFromCodexTurnMetadata` — 从 JSON 头提取 session_id
   - `TestParseSessionIDFromCodexTurnMetadata_Empty` — 无头/空 JSON 返回 ""
   - `TestRecognizeSessionID_HeaderFallback` — 头识别在 body 识别之前触发

5. **已知前缀映射**：
   - `TestLookupKnownAgentPrefix` — 已知前缀返回标准名称
   - `TestLookupKnownAgentPrefix_Unknown` — 未知前缀返回 false

---

## 3. 实施步骤

| 步骤 | 内容 | 文件 |
|------|------|------|
| 1 | 新增 `knownAgentPrefixes` 映射表 + `lookupKnownAgentPrefix` | `recognizer_agent_name.go` |
| 2 | 修改 `RecognizeAgentTool` 增加已知前缀规范化 | `recognizer_agent_name.go` |
| 3 | 新增 `parseSessionIDFromCodexTurnMetadata` 函数 | `recognizer_session_id.go` |
| 4 | 修改 `anthropicSessionRecognizer.Recognize` 增加 `x-claude-code-session-id` 头 | `recognizer_session_id.go` |
| 5 | 修改 `openAISessionRecognizer.Recognize` 增加 Codex 头和通用头 | `recognizer_session_id.go` |
| 6 | 扩展 `EconomicAdvancedAgentWhiteList` | `agent_algorithm_economic.go` |
| 7 | 扩展 `EconomicSyntheticSessionEligibleAgents` | `agent_algorithm_economic.go` |
| 8 | 更新 `IsAdvancedAgentToolName` 和 `IsSyntheticSessionEligibleAgent` 的注释 | `agent_algorithm_economic.go` |
| 9 | 新增测试文件 `v2073_agent_detection_enhance_test.go` | 新文件 |
| 10 | 运行测试、编译、重启 | - |

---

## 4. 兼容性评估

| 变更 | 向后兼容 | 风险 |
|------|---------|------|
| 新增头识别路径 | ✅ 原有路径不变，新路径仅增加命中机会 | 低 |
| 新增已知前缀映射 | ✅ 未命中映射走原有逻辑 | 低 |
| 扩展白名单 | ✅ 新增条目不影响已有条目 | 低 |
| 头识别优先级调整 | ⚠️ 头识别优先于 body 识别可能改变现有行为 | 中 — 需确认头识别的准确性 |

**缓解措施**：头识别放在 Agent 工具级识别之后、`metadata.user_id` 之前，避免改变 OpenClaw 等已验证路径的行为。

---

## 5. 不做的事（本次不做）

以下功能虽在 Switchyard 中存在，但本次优化**不做**：

| 功能 | 原因 |
|------|------|
| Sub-Agent 检测 | LsmTokensServer 是代理层，不做路由分支，优先级低 |
| 工具行为分类（Edit/Write/Read/Plan/Bash） | 属于路由策略层，需配合 StageRouter 使用 |
| 测试结果检测 | 属于工具行为分析的子集，需整体设计 |
| `x-switchyard-*` 原生覆盖头 | LsmTokensServer 不是 Switchyard，不应发送 Switchyard 头 |
| Agent 启动器 | 运维工具，与代理逻辑无关 |

---

## 6. 预期效果

| 指标 | 当前 | 优化后 |
|------|------|--------|
| Agent 识别种类 | 6 | 15+ |
| Session 识别来源 | body + 3 个 Anthropic 头 | body + 6+ 个头（含 Codex JSON 头） |
| 高阶 Agent 白名单 | 5 | 15+ |
| 合成 Session 白名单 | 2 | 7 |
