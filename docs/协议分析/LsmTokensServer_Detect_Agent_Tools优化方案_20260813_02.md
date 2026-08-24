# LsmTokensServer Agent 工具检测优化方案

> **文档版本**: v2.0 (2026-08-13)
> **基础文档**: [cc-switch_Detect_Agent_Tools_Sessions.md](cc-switch_Detect_Agent_Tools_Sessions.md)
> **目标版本**: v2.0.73

---

## 1. 优化背景

通过分析 cc-switch 开源项目的 Agent 工具检测与 Session 识别实现（详见知识库文档），发现以下可借鉴的优化点：

| # | 优化点 | 来源 | 优先级 | 影响范围 |
|---|--------|------|--------|---------|
| 1 | 扩展已知 Agent 工具前缀 | cc-switch `AppType` 枚举 + UA presets | P0 | `recognizer_agent_name.go` |
| 2 | Grok Build Session 识别 | cc-switch `session.rs` | P1 | `recognizer_session_id.go` |
| 3 | Session ID 来源追踪 | cc-switch `SessionIdSource` 枚举 | P2 | `recognizer_session_id.go` |
| 4 | Session ID 长度校验 | cc-switch `extract_responses_session` | P1 | `recognizer_session_id.go` |
| 5 | 补齐 Anthropic 头别名 | cc-switch `extract_claude_session` | P1 | `recognizer_session_id.go` |
| 6 | OpenCode Session 头识别增强 | cc-switch + 现有 `X-Session-Id` | P2 | `recognizer_session_id.go` |
| 7 | 测试用例补全 | 以上所有优化 | P1 | 测试文件 |

---

## 2. 优化详细设计

### 2.1 扩展已知 Agent 工具前缀（P0）

**文件**: `recognizer_agent_name.go`

**现状**: `knownAgentPrefixes` 支持 12 个工具。cc-switch 发现了 LsmTokensServer 缺失的工具。

**变更**: 新增以下前缀映射：

```go
var knownAgentPrefixes = map[string]string{
    // ... 现有 12 项保持不变 ...
    "grok":       "grok-build",   // Grok Build (xAI)
    "grok-build": "grok-build",   // Grok Build 全名
    "opencode":   "opencode",     // OpenCode IDE
    "openclaw":   "openclaw",     // OpenClaw WebChat（独立于 OpenAI/JS UA 识别）
    "rovo":       "rovo",         // Rovo Dev CLI (Atlassian)
    "longcat":    "longcat",      // LongCat CLI (美团)
    "kilo-code":  "kilo-code",    // Kilo Code IDE
    "kilo":       "kilo-code",    // Kilo Code 短名
    "amp":        "amp",          // Amp (Sourcegraph)
}
```

**理由**:
- `grok-build` — cc-switch 已支持的 Grok Build 工具
- `opencode` — 已知 AI IDE 工具
- `rovo` — cc-switch UA presets 中出现的 Rovo Dev CLI
- `longcat` — cc-switch 中有 `longcatProviderPresets.ts`
- `kilo-code` — cc-switch UA presets 中出现的 Kilo Code
- `amp` — 已知 AI 编程工具

### 2.2 Grok Build Session 识别（P1）

**文件**: `recognizer_session_id.go`

**新增**: Grok Build 专用 Session 头解析函数

```go
// parseSessionIDFromGrokHeaders 从 Grok Build 请求头识别 session_id。
// Grok Build 使用两个独立头：
//   1. x-grok-conv-id — 对话 ID（跨多轮稳定，优先级高）
//   2. x-grok-session-id — Session ID（作为 conv-id 缺失时的回退）
//
// 注意：x-grok-req-id 是逐请求 ID，不能用于 session 聚合。
func parseSessionIDFromGrokHeaders(headers http.Header) string {
    if headers == nil {
        return ""
    }
    for _, h := range []string{"X-Grok-Conv-Id", "X-Grok-Session-Id"} {
        if v := strings.TrimSpace(headers.Get(h)); v != "" {
            return v
        }
    }
    return ""
}
```

**集成位置**: `openAISessionRecognizer.Recognize()` — 在 Agent 工具级识别之后、metadata.user_id 之前。

### 2.3 Session ID 来源追踪（P2）

**文件**: `recognizer_session_id.go`

**新增**: `SessionIdSource` 类型，记录 session ID 的提取来源

```go
// SessionIdSource session_id 提取来源（借鉴 cc-switch SessionIdSource 枚举）
type SessionIdSource int

const (
    SessionIdSourceUnknown        SessionIdSource = iota // 未识别
    SessionIdSourceAgentTool                             // Agent 工具级识别（如 OpenClaw system content）
    SessionIdSourceHeader                                // HTTP 头（如 x-claude-code-session-id）
    SessionIdSourceMetadataUserID                        // metadata.user_id 内嵌 JSON
    SessionIdSourceClientMetadata                        // client_metadata.session_id
    SessionIdSourcePromptCacheKey                        // prompt_cache_key
    SessionIdSourceTopLevel                              // 顶层 session_id 字段
    SessionIdSourceGrokHeader                            // Grok Build 专用头
)
```

**新增**: 带来源的识别结果类型

```go
// SessionRecognitionResult session 识别结果（含来源追踪）
type SessionRecognitionResult struct {
    SessionID string
    Source    SessionIdSource
}
```

**变更**: `RecognizeSessionID` 保持原有签名不变（向后兼容），新增 `RecognizeSessionIDWithSource` 函数：

```go
func RecognizeSessionIDWithSource(body []byte, protocolType int, headers http.Header) SessionRecognitionResult {
    // 与 RecognizeSessionID 相同逻辑，但返回 SessionRecognitionResult
}
```

**日志集成**: 在 `logAIProxyTransaction` 中记录 session_id 来源，便于排查 session 识别问题。

### 2.4 Session ID 长度校验（P1）

**文件**: `recognizer_session_id.go`

**现状**: 从 HTTP 头提取的 session_id 没有长度校验，可能误匹配短值（如 `"0"`、`"null"`）。

**变更**: 对 HTTP 头来源的 session_id 添加最小长度校验（借鉴 cc-switch 的 `> 20` / `> 10` 规则）：

```go
// sessionIDMinHeaderLen HTTP 头来源 session_id 最小长度
// 有效 session_id 通常是 UUID 格式（36 字符）或类似格式（> 20 字符）
const sessionIDMinHeaderLen = 10

// 在 parseSessionIDFromAnthropicHeaders、parseSessionIDFromCodexTurnMetadata 等函数中：
if v := strings.TrimSpace(headers.Get("X-Claude-Code-Session-Id")); v != "" {
    if len(v) >= sessionIDMinHeaderLen {
        return v
    }
}
```

**注意**: 长度阈值设为 10（比 cc-switch 的 20 更宽松），因为 LsmTokensServer 面向更多客户端，部分客户端的 session_id 可能较短。

### 2.5 补齐 Anthropic 头别名（P1）

**文件**: `recognizer_session_id.go`

**现状**: `parseSessionIDFromAnthropicHeaders` 支持 `X-Claude-Code-Session-Id`，但 cc-switch 还支持不带 `X-` 前缀的 `claude-code-session-id`。

**变更**: 在 `parseSessionIDFromAnthropicHeaders` 中新增 `Claude-Code-Session-Id` 别名：

```go
// Claude Code CLI 原生头（支持两种大小写变体）
for _, h := range []string{"X-Claude-Code-Session-Id", "Claude-Code-Session-Id"} {
    if v := strings.TrimSpace(headers.Get(h)); v != "" {
        if len(v) >= sessionIDMinHeaderLen {
            return v
        }
    }
}
```

### 2.6 OpenCode Session 头识别增强（P2）

**文件**: `recognizer_session_id.go`

**现状**: OpenAI 协议识别已支持 `X-Session-Id` 头（OpenCode 原生），但缺少 OpenCode 特有的 `X-OpenCode-Session-Id` 头。

**变更**: 在 OpenAI 协议头识别路径中新增 OpenCode 特有头：

```go
// 2b. x-session-id 头（OpenCode 原生）/ session-id 头（通用）
//     + x-opencode-session-id（OpenCode 特有头）
if headers != nil {
    for _, h := range []string{"X-OpenCode-Session-Id", "X-Session-Id", "Session-Id"} {
        if v := strings.TrimSpace(headers.Get(h)); v != "" {
            if len(v) >= sessionIDMinHeaderLen {
                return v
            }
        }
    }
}
```

### 2.7 测试用例补全（P1）

**文件**: `recognizer_agent_name_test.go` + `recognizer_session_id_test.go`

**新增测试**:

#### Agent 名称识别测试
- `TestRecognizeAgentTool_GrokBuild` — `"grok-build/1.0"` → `grok-build`
- `TestRecognizeAgentTool_OpenCode` — `"opencode/0.5"` → `opencode`
- `TestRecognizeAgentTool_LongCat` — `"longcat/1.0"` → `longcat`
- `TestRecognizeAgentTool_KiloCode` — `"Kilo-Code/1.0"` → `kilo-code`

#### Session 识别测试
- `TestParseSessionIDFromGrokHeaders_ConvId` — `X-Grok-Conv-Id` 头优先
- `TestParseSessionIDFromGrokHeaders_SessionId` — `X-Grok-Session-Id` 回退
- `TestParseSessionIDFromGrokHeaders_ReqIdIgnored` — `X-Grok-Req-Id` 不作为 session
- `TestSessionIDMinHeaderLen_FilterShort` — 短值（< 10）被过滤
- `TestParseSessionIDFromAnthropicHeaders_NoXPrefix` — `Claude-Code-Session-Id` 无 `X-` 前缀
- `TestRecognizeSessionIDWithSource_TracksSource` — 来源追踪正确

---

## 3. 变更文件清单

| 文件 | 变更类型 | 描述 |
|------|---------|------|
| `recognizer_agent_name.go` | 修改 | 扩展 `knownAgentPrefixes`（+8 项） |
| `recognizer_session_id.go` | 修改 | 新增 Grok 头解析、长度校验、来源追踪、头别名 |
| `recognizer_agent_name_test.go` | 修改 | 新增 Agent 名称识别测试 |
| `recognizer_session_id_test.go` | 修改 | 新增 Session 识别测试 |

**不变更的文件**:
- `recognizer_openai_function_call.go` — 工具名称提取逻辑不受影响
- `recognizer_anthropic_tool_call.go` — 同上
- `protocol_types.go` — 协议类型不变
- `server_http_ai_proxy.go` — 调用入口不变（`RecognizeSessionID` 签名保持兼容）

---

## 4. 向后兼容性

- `RecognizeSessionID(body, protocolType, headers)` 签名不变，返回 `string`
- `RecognizeAgentTool(userAgent)` 签名不变，返回 `AgentToolRecognitionResult`
- `knownAgentPrefixes` 变更是纯增量（新增条目，不修改/删除现有条目）
- 新增的 `RecognizeSessionIDWithSource` 是新增函数，不影响现有调用方
- Session ID 长度校验可能**过滤掉**极短的 session_id（< 10 字符），需确认生产环境中无合法短 session_id

---

## 5. 风险评估

| 风险 | 概率 | 影响 | 缓解措施 |
|------|------|------|---------|
| 长度过滤误杀合法短 session_id | 低 | 中 | 阈值设为 10（宽松）；仅对 HTTP 头来源校验，body 来源不限 |
| 新增 Agent 前缀误匹配 | 低 | 低 | 前缀映射是已知工具的精确匹配 |
| Grok 头名大小写不一致 | 低 | 低 | Go `http.Header.Get` 内置大小写不敏感匹配 |
| 来源追踪增加日志量 | 低 | 低 | 仅在 session_id 非空时记录，一个整数字段 |

---

## 6. 实施顺序

1. **P0**: 扩展 `knownAgentPrefixes`（纯增量，零风险）
2. **P1a**: Grok Build Session 头解析
3. **P1b**: Session ID 长度校验
4. **P1c**: 补齐 Anthropic 头别名
5. **P1d**: 测试用例补全
6. **P2a**: Session ID 来源追踪
7. **P2b**: OpenCode Session 头增强
