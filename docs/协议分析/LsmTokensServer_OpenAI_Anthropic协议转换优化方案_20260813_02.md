# LsmTokensServer OpenAI ↔ Anthropic 协议转换优化方案（第 02 版）

> 日期：2026-08-13
> 输入：[`cc-switch_OpenAI_Anthropic_Exchange.md`](./cc-switch_OpenAI_Anthropic_Exchange.md)（cc-switch 开源翻译层调研）+ [`LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md`](./LsmTokensServer_OpenAI_Anthropic协议转换优化方案_20260813_01.md)（v2.0.72 已落地项）+ LsmTokensServer 当前实现审计
> 目标版本：v2.0.73
> 范围：协议转换模块（`protocol_types.go` / `protocol_openai_to_anthropic.go` / `protocol_anthropic_to_openai.go` / `protocol_sse.go` / `protocol_analyzer.go`）+ 代理热路径（`server_http_ai_proxy_utils.go`）

---

## 0. v2.0.72 已落地回顾（01 版）

v2.0.72 已修复 27 项问题中的核心 P0/P1 项（平铺线格式建模、tool_result 拆分、多模态双向转换、SSE CRLF 兼容、stream 输出协议自洽、wrapAnthropicResponseAsSSE tool_use 完整化、max_tokens 默认值、Temperature 指针、连续 tool 消息合并、Arguments 解析兜底、id 前缀改写、finish/stop 映射补齐、cache token 双向映射等）。详见 01 版文档。

**本版（02）在 v2.0.72 基础上，针对 cc-switch 调研发现的「仍有差距」项进行增量优化。**

---

## 1. 现状审计：v2.0.72 之后仍与 cc-switch 有差距的项

对照 cc-switch 知识库（§9 差异表），以下能力当前实现**缺失或不完整**：

| # | 严重度 | 能力 | 位置 | cc-switch 参照 |
|---|--------|------|------|---------------|
| P0-1 | 致命 | o-series 模型（o1/o3/o4）a2o 请求中 `max_tokens` 未切换为 `max_completion_tokens`，上游 o 模型必 400 | `protocol_anthropic_to_openai.go:27-30` | `is_openai_o_series()` + a2o 请求字段切换 |
| P0-2 | 致命 | o2a 请求缺少「首条 user 消息保证」——对话历史以 assistant 开头时 Anthropic 400 | `protocol_openai_to_anthropic.go` 消息循环 | `ensure_leading_user_message()` |
| P0-3 | 严重 | o2a 请求缺少「不完成工具轮次丢弃」——assistant tool_use 无匹配 tool_result 时 Anthropic 400 | `protocol_openai_to_anthropic.go` tool_result 合并 | `drop_incomplete_tool_turns()` |
| P1-4 | 高 | a2o 请求 system 未规范化（多条未合并、未移 index 0、cache_control 泄漏到 OpenAI 消息） | `protocol_anthropic_to_openai.go:43-51` | `normalize_openai_system_messages()` |
| P1-5 | 高 | Thinking ↔ Reasoning Effort 双向映射缺失（Anthropic `thinking` 在 a2o 方向被丢弃；OpenAI `reasoning_effort` 在 o2a 方向未转为 `thinking`） | 双向请求转换 | `resolve_reasoning_effort()` + o2a thinking 注入 |
| P1-6 | 高 | o2a 请求 thinking 预算未钳制（应 ≤ `max_tokens/2`，否则可见答案空间不足） | `protocol_openai_to_anthropic.go` | thinking budget clamping |
| P1-7 | 高 | o2a 请求「强制 tool_choice + thinking 同时启用」冲突未处理（Anthropic 拒绝此组合） | `protocol_openai_to_anthropic.go` tool_choice 转换 | forced tool_choice vs thinking 冲突处理 |
| P1-8 | 中 | o2a 响应消息级 `refusal` 字段未转为 text 块（仅处理了 content 数组中的 refusal part） | `protocol_openai_to_anthropic.go:516-524` | message-level refusal → text block |
| P1-9 | 中 | SSE 聚合缺少「流截断检测」——上游未发 terminal 事件时静默产出响应，客户端无法感知流被截断 | `protocol_sse.go` 两处 Aggregate 函数 | 流截断检测（incomplete/failed 区分） |
| P1-10 | 中 | `parseSSEEvents` 未处理 UTF-8 多字节跨 chunk 边界（大 chunk 拆分时可能产出非法 UTF-8） | `server_http_ai_proxy_utils.go:189-280` | `append_utf8_safe()` |
| P2-11 | 低 | 非流式 o2a 响应未检测上游错误信封 `{"type":"error"}`（直接当正常响应转换会产出畸形结果） | `protocol_anthropic_to_openai.go` 响应转换 | 上游错误信封检测 |
| P2-12 | 低 | 工具输出媒体递归提取缺失（tool_result 中含 image 块时静默丢弃） | `flattenAnthropicToolResultContent` | `tool_media.rs` 递归提取（深度 32） |

**不采纳（本版）**：
- **Thinking 签名桥**（不透明传输 reasoning 项）：高复杂度，当前 LsmTokensServer 无 OpenAI Responses API 客户端，收益不足以覆盖风险。
- **Value-Centric 重构**：v2.0.72 已选平铺 Struct + 真实 JSON 测试路线，改弦更张风险大且收益已被测试规则覆盖。
- **工具命名空间解析**（CodexToolContext）：LsmTokensServer 不涉及 Codex Responses API 的命名工具。

---

## 2. cc-switch 可借鉴点 → 本版的映射

| cc-switch 实践 | 本版采纳点 | 修复编号 |
|---|---|---|
| `is_openai_o_series()` 检测 o 系列模型，a2o 请求 `max_tokens` → `max_completion_tokens` | 新增 `isOpenAIOSeries(model)` + a2o 请求字段切换 | P0-1 |
| `ensure_leading_user_message()` 插入合成 user 消息 | o2a 请求：历史以 assistant/tool 开头时前置 `"(continuing the conversation)"` user 消息 | P0-2 |
| `drop_incomplete_tool_turns()` 校验 tool_use/tool_result 配对 | o2a 请求：assistant tool_use 块在紧随 user 消息中无匹配 tool_result 时整体丢弃该轮次 | P0-3 |
| `normalize_openai_system_messages()` 移 index 0 / 合并 / 剥 cache_control | a2o 请求：system 消息规范化（移首、合并、剥 cache_control） | P1-4 |
| `resolve_reasoning_effort()` thinking → reasoning_effort | a2o 请求：Anthropic `thinking` 配置映射为 OpenAI `reasoning_effort`（low/medium/high/xhigh） | P1-5a |
| o2a 方向 reasoning_effort → thinking | o2a 请求：OpenAI `reasoning_effort` 映射为 Anthropic `thinking`（budget_tokens 按档位） | P1-5b |
| Thinking budget clamping（max_tokens/2） | o2a 请求：thinking budget 钳制到 `max_tokens/2`，< 1024 则禁用 | P1-6 |
| Forced tool_choice + thinking 冲突 → 禁用 thinking | o2a 请求：tool_choice 为 required/指定工具时禁用 thinking | P1-7 |
| message-level refusal → text block | o2a 响应：消息级 `refusal` 字段转为 `{"type":"text","text":refusal}` 块 | P1-8 |
| 流截断检测（incomplete/failed 区分） | SSE 聚合：检测缺失 terminal 事件，在响应中注入 warning；非流式路径不变 | P1-9 |
| `append_utf8_safe()` UTF-8 跨 chunk 边界处理 | `parseSSEEvents`：缓冲不完整多字节序列到下一读入 | P1-10 |
| 上游错误信封检测 | 非流式 o2a 响应：检测 `{"type":"error"}` 并走错误转换路径 | P2-11 |
| 工具输出媒体递归提取（深度 32） | `flattenAnthropicToolResultContent`：递归提取 tool_result 中的 image 块 | P2-12 |

---

## 3. 详细修复方案

### 修复 ① P0-1：a2o 请求 o-series 模型 `max_tokens` → `max_completion_tokens`

**根因**：Anthropic 客户端发 o1/o3/o4 模型请求时带 `max_tokens`，但 o 系列 OpenAI 模型**不接受 `max_tokens` 字段**，只接受 `max_completion_tokens`。当前 `ConvertAnthropicToOpenAIRequest` 直接拷贝 `MaxTokens`，o 模型上游必 400。

**方案**：
```go
// isOpenAIOSeries 检测 OpenAI o 系列模型（o1/o3/o4 等）：以 'o' 开头且第二位是数字
func isOpenAIOSeries(model string) bool {
    if len(model) < 2 {
        return false
    }
    b := model[0]
    if b != 'o' && b != 'O' {
        return false
    }
    return model[1] >= '0' && model[1] <= '9'
}
```

在 `ConvertAnthropicToOpenAIRequest` 中：
```go
// max_tokens 映射：o 系列模型用 max_completion_tokens（o1/o3/o4 不接受 max_tokens 字段）
if isOpenAIOSeries(openAIReq.Model) {
    if anthropicReq.MaxTokens > 0 {
        openAIReq.MaxCompletionTokens = anthropicReq.MaxTokens
    }
} else {
    if anthropicReq.MaxTokens > 0 {
        openAIReq.MaxTokens = anthropicReq.MaxTokens
    }
}
```

**测试**：`TestConvertAnthropicToOpenAIRequest_OSeries_UsesMaxCompletionTokens`、`TestConvertAnthropicToOpenAIRequest_NonOSeries_UsesMaxTokens`、`TestIsOpenAIOSeries_Boundary`（o1/o3/o4-mini/o1-preview/claude-3-opus/gpt-4o）。

---

### 修复 ② P0-2：o2a 请求「首条 user 消息保证」

**根因**：Anthropic 要求对话历史第一条消息必须来自 user。当 OpenAI 客户端发送的历史以 assistant（含 tool_calls）开头时，直接转换后 Anthropic 上游 400。

**方案**：在 o2a 消息转换末尾（system 提取后、messages 组装前），若 `anthropicMessages[0].Role != "user"`，前置一条合成 user 消息：
```go
// ensureLeadingUserMessage 保证第一条消息来自 user（Anthropic 约束）。
// 若历史以 assistant/tool 开头，前置合成 "(continuing the conversation)" user 消息。
func ensureLeadingUserMessage(msgs []AnthropicMessage) []AnthropicMessage {
    if len(msgs) == 0 {
        return msgs
    }
    if msgs[0].Role == "user" {
        return msgs
    }
    leading := AnthropicMessage{
        Role:    "user",
        Content: []AnthropicContentBlock{{Type: "text", Text: "(continuing the conversation)"}},
    }
    return append([]AnthropicMessage{leading}, msgs...)
}
```

**测试**：`TestEnsureLeadingUserMessage_AssistantFirst_PrependsUser`、`TestEnsureLeadingUserMessage_UserFirst_NoOp`、`TestEnsureLeadingUserMessage_Empty_NoOp`、端到端 `TestConvertOpenAIToAnthropicRequest_LeadingUserMessageGuaranteed`。

---

### 修复 ③ P0-3：o2a 请求「不完成工具轮次丢弃」

**根因**：OpenAI 客户端发送 assistant tool_calls 后，若紧随的 user 消息中缺少对应 tool_call_id 的 tool_result（工具调用未被执行/结果丢失），直接转发给 Anthropic 会 400（Anthropic 要求 tool_use 必须有匹配的 tool_result）。

**方案**：在连续 tool_result 合并后，校验每个 assistant tool_use 块是否在紧随的 user 消息中有匹配 tool_result。不匹配的轮次整体丢弃：
```go
// dropIncompleteToolTurns 校验 assistant tool_use 块在紧随的 user 消息中有匹配 tool_result。
// 不完成轮次整体丢弃，避免 Anthropic 400（借鉴 cc-switch drop_incomplete_tool_turns）。
func dropIncompleteToolTurns(msgs []AnthropicMessage) []AnthropicMessage {
    // 收集所有 assistant tool_use id
    // 对每个 user 消息，检查其 tool_result 是否覆盖了前一个 assistant 的 tool_use
    // 若 assistant 的某 tool_use 在紧随 user 中无 tool_result，丢弃该 assistant 消息
    // （保守策略：丢弃整个不完成轮次而非单个块，避免部分结果引发 400）
    ...
}
```

**实现策略**：遍历消息，维护「待匹配 tool_use id 集合」。遇到 assistant 消息时记录其 tool_use id；遇到 user 消息时从其 tool_result 块中移除已匹配 id。若 assistant 消息后紧跟的 user 消息未能匹配全部 tool_use id，丢弃该 assistant 消息及其未匹配的 tool_result。

**测试**：`TestDropIncompleteToolTurns_CompleteTurn_Kept`、`TestDropIncompleteToolTurns_IncompleteTurn_Dropped`、`TestDropIncompleteToolTurns_PartialMatch_Dropped`、`TestDropIncompleteToolTurns_NoTools_NoOp`。

---

### 修复 ④ P1-4：a2o 请求 system 消息规范化

**根因**：Anthropic 客户端可能发送多条 system 消息（或 system 消息不在第一条），或 system 块含 `cache_control` 字段。当前实现仅 prepend，未做规范化，导致：(a) 多条 system 消息被 OpenAI 拒绝或语义错乱；(b) `cache_control` 泄漏到 OpenAI 请求（OpenAI 不识别此字段）。

**方案**：在 a2o system 提取后，增加 `normalizeOpenAISystemMessages`：
```go
// normalizeOpenAISystemMessages 规范化 OpenAI system 消息（借鉴 cc-switch normalize_openai_system_messages）：
// 1. 多条 system 消息合并为一条（"\n" 拼接）
// 2. system 消息不在 index 0 时移到 index 0
// 3. 剥离 system 消息中的 cache_control 字段（防止泄漏到 OpenAI 请求）
func normalizeOpenAISystemMessages(msgs []OpenAIMessage) []OpenAIMessage {
    var systemTexts []string
    var others []OpenAIMessage
    for _, m := range msgs {
        if m.Role == "system" || m.Role == "developer" {
            text := extractTextPartsContent(stripCacheControlFromContent(m.Content))
            if text != "" {
                systemTexts = append(systemTexts, text)
            }
        } else {
            others = append(others, m)
        }
    }
    if len(systemTexts) == 0 {
        return others
    }
    systemMsg := OpenAIMessage{Role: "system", Content: strings.Join(systemTexts, "\n")}
    return append([]OpenAIMessage{systemMsg}, others...)
}

// stripCacheControlFromContent 剥离 content 中的 cache_control 字段（递归）
func stripCacheControlFromContent(content interface{}) interface{} {
    ...
}
```

**测试**：`TestNormalizeOpenAISystemMessages_MergeMultiple`、`TestNormalizeOpenAISystemMessages_MoveToIndex0`、`TestNormalizeOpenAISystemMessages_StripCacheControl`、回归测试 `TestNormalizeOpenAISystemMessages_NoCacheControlLeak`（锁死 cc-switch gh3805 同款缺陷）。

---

### 修复 ⑤ P1-5：Thinking ↔ Reasoning Effort 双向映射

**根因**：Anthropic `thinking` 配置（`{"type":"enabled","budget_tokens":N}` 或 `output_config.effort`）在 a2o 方向被丢弃，OpenAI 客户端无法获知 thinking 请求；OpenAI `reasoning_effort` 在 o2a 方向未转为 Anthropic `thinking`，推理模型无法启用思考。

**方案**：

**a2o 方向**（`ConvertAnthropicToOpenAIRequest`）：
```go
// resolveReasoningEffort 将 Anthropic thinking 配置映射为 OpenAI reasoning_effort
// （借鉴 cc-switch resolve_reasoning_effort）
func resolveReasoningEffort(anthropicReq *AnthropicMessagesRequest) string {
    // 优先 output_config.effort
    if oc, ok := anthropicReq.OutputConfig.(map[string]interface{}); ok {
        if effort, ok := oc["effort"].(string); ok && effort != "" {
            return effort // low/medium/high/max → OpenAI 接受这些值
        }
    }
    if thinking, ok := anthropicReq.Thinking.(map[string]interface{}); ok {
        tType, _ := thinking["type"].(string)
        if tType == "adaptive" {
            return "high"
        }
        if tType == "enabled" {
            budget, _ := thinking["budget_tokens"].(float64)
            if budget == 0 {
                return "high"
            }
            if budget < 4000 {
                return "low"
            }
            if budget < 16000 {
                return "medium"
            }
            return "high"
        }
    }
    return ""
}
```

**o2a 方向**（`ConvertOpenAIToAnthropicRequest`）：
```go
// reasoningEffortToThinking 将 OpenAI reasoning_effort 映射为 Anthropic thinking 配置
// （借鉴 cc-switch thinking budget clamping 的档位逻辑）
func reasoningEffortToThinking(effort string, maxTokens int) interface{} {
    if effort == "" {
        return nil
    }
    budgetMap := map[string]int{
        "low":    1024,
        "medium": 4000,
        "high":   16000,
        "xhigh":  32000,
    }
    budget, ok := budgetMap[effort]
    if !ok {
        budget = 4000
    }
    // 钳制到 max_tokens/2（见 P1-6）
    ceiling := maxTokens / 2
    if budget > ceiling {
        budget = ceiling
    }
    if budget < 1024 {
        return nil // 预算过小，禁用 thinking
    }
    return map[string]interface{}{"type": "enabled", "budget_tokens": budget}
}
```

**测试**：`TestResolveReasoningEffort_OutputConfigPriority`、`TestResolveReasoningEffort_BudgetTiers`、`TestReasoningEffortToThinking_Tiers`、`TestReasoningEffortToThinking_Empty_Nil`。

---

### 修复 ⑥ P1-6：o2a 请求 thinking 预算钳制

**根因**：OpenAI 客户端 `reasoning_effort` 映射出的 thinking budget 可能超过 `max_tokens/2`，导致 Anthropic 可见答案空间不足甚至 400。

**方案**：已在修复 ⑤ 的 `reasoningEffortToThinking` 中内联钳制（`ceiling := maxTokens / 2`；`budget < 1024` 禁用）。单独测试：`TestReasoningEffortToThinking_ClampedToHalfMaxTokens`、`TestReasoningEffortToThinking_Below1024_Disabled`。

---

### 修复 ⑦ P1-7：o2a 请求「强制 tool_choice + thinking 冲突」处理

**根因**：Anthropic 拒绝「强制 tool_choice（any/tool 指定）+ thinking 启用」组合。当 OpenAI 客户端同时传 `tool_choice: "required"`（或指定工具）和 `reasoning_effort` 时，o2a 转换产出的 Anthropic 请求必 400。

**方案**：在 o2a 转换末尾，检测冲突并禁用 thinking：
```go
// disableThinkingOnForcedToolChoice 当 tool_choice 为强制模式（required/指定工具）时禁用 thinking。
// Anthropic 拒绝强制工具 + thinking 组合（借鉴 cc-switch forced tool_choice vs thinking 冲突处理）。
func disableThinkingOnForcedToolChoice(req *AnthropicMessagesRequest, toolChoice interface{}) {
    forced := false
    switch tc := toolChoice.(type) {
    case string:
        forced = tc == "required"
    case map[string]interface{}:
        if t, ok := tc["type"].(string); ok && t == "tool" {
            forced = true
        }
    }
    if !forced {
        return
    }
    req.Thinking = nil
    req.OutputConfig = nil
}
```

**测试**：`TestDisableThinkingOnForcedToolChoice_Required_Disabled`、`TestDisableThinkingOnForcedToolChoice_NamedTool_Disabled`、`TestDisableThinkingOnForcedToolChoice_Auto_Kept`。

---

### 修复 ⑧ P1-8：o2a 响应消息级 `refusal` 字段处理

**根因**：OpenAI o 系列模型在触发安全拒绝时，`message.refusal` 字段含拒绝原因文本，`content` 为 nil。当前 `ConvertOpenAIToAnthropicResponse` 只处理 `message.Content`，`refusal` 被静默丢弃，Anthropic 客户端收到空响应。

**方案**：在 `ConvertOpenAIToAnthropicResponse` 的内容块构建中，补充 refusal 处理：
```go
if message != nil {
    if message.Content != nil {
        text := extractTextPartsContent(message.Content)
        if text != "" {
            contentBlocks = append(contentBlocks, AnthropicContentBlock{Type: "text", Text: text})
        }
    }
    // 消息级 refusal 字段（o 系列模型安全拒绝时 content 为 nil，refusal 含原因）
    if contentBlocks == nil || len(contentBlocks) == 0 {
        if refusal := extractStringContent(refusalField(message)); refusal != "" {
            contentBlocks = append(contentBlocks, AnthropicContentBlock{
                Type: "text", Text: refusal,
            })
        }
    }
}
```

注：`OpenAIMessage` 需新增 `Refusal string` 字段（`json:"refusal,omitempty"`）。

**测试**：`TestConvertOpenAIToAnthropicResponse_Refusal_ToTextBlock`、`TestConvertOpenAIToAnthropicResponse_ContentAndRefusal_ContentWins`。

---

### 修复 ⑨ P1-9：SSE 聚合「流截断检测」

**根因**：上游 SSE 流在未发 terminal 事件（OpenAI `[DONE]` / Anthropic `message_stop`）时中断（网络抖动、上游超时），当前聚合函数静默产出响应，客户端无法感知流被截断，Agent 工具循环可能拿到不完整 tool_call。

**方案**：在 `AggregateOpenAISSEToResponse` 和 `AggregateAnthropicSSEToResponse` 中检测 terminal 事件，缺失时在 warnings 中注入截断提示：
```go
// AggregateOpenAISSEToResponse: 新增 sawDone bool
var sawDone bool
for _, ev := range events {
    data := strings.TrimSpace(ev.Data)
    if data == "[DONE]" { sawDone = true; continue }
    ...
}
if !sDone && resp.Choices[0].FinishReason == "" && content.Len() > 0 {
    warnings = append(warnings, "stream truncated: no [DONE] event received")
    resp.Choices[0].FinishReason = "length" // 标记为不完整
}
```

Anthropic 方向同理检测 `message_stop`。

**测试**：`TestAggregateOpenAISSEToResponse_Truncated_NoDone_Warning`、`TestAggregateOpenAISSEToResponse_Complete_NoWarning`、`TestAggregateAnthropicSSEToResponse_Truncated_NoMessageStop_Warning`。

---

### 修复 ⑩ P1-10：`parseSSEEvents` UTF-8 跨 chunk 边界处理

**根因**：`io.ReadAll` 一次性读入时不会拆分 UTF-8 多字节字符（因为是一次读完）。但未来若改为流式逐 chunk 读取（二期真流式转换），跨 chunk 边界的 UTF-8 多字节字符会被拆分，产出非法 UTF-8。本版预先加固。

**方案**：在 `parseSSEEvents` 中增加 UTF-8 完整性校验，不完整多字节序列保留到下一次处理。由于当前是 `ReadAll` 一次性读入，此修复主要是防御性加固：
```go
// ensureUTF8Complete 检查末尾是否有不完整的多字节 UTF-8 序列，
// 有则截断（当前 ReadAll 路径不会触发，为未来流式路径预留）
func trimIncompleteTrailingUTF8(s string) string {
    for i := 1; i <= 3 && i <= len(s); i++ {
        b := s[len(s)-i]
        if b&0xC0 != 0x80 { // 不是延续字节
            if b >= 0xC0 {   // 是起始字节但不完整
                return s[:len(s)-i]
            }
            return s // 完整 ASCII
        }
    }
    return s
}
```

**测试**：`TestTrimIncompleteTrailingUTF8_Complete`、`TestTrimIncompleteTrailingUTF8_Truncates2Byte`、`TestTrimIncompleteTrailingUTF8_Truncates3Byte`、`TestTrimIncompleteTrailingUTF8_Truncates4Byte`、`TestParseSSEEvents_UTF8Multibyte_NotCorrupted`。

---

### 修复 ⑪ P2-11：非流式 o2a 响应上游错误信封检测

**根因**：上游 Anthropic 返回 `{"type":"error","error":{"type":"...","message":"..."}}` 时，当前非流式 o2a 路径直接当正常响应转换，产出畸形的 `AnthropicMessagesResponse`（`stop_reason` 为空、`content` 为空）。

**方案**：在 `ConvertOpenAIToAnthropicResponse` 入口检测错误信封：
```go
// 检测上游错误信封（借鉴 cc-switch anthropic_response_to_responses_with_context）
if openAIResp 中包含错误特征 {
    return nil, fmt.Errorf("upstream error: ...")
}
```

实际上 OpenAI 错误格式为 `{"error":{"message":"...","type":"..."}}`。在 `convertProxyResponse` 中已有 `ConvertProtocolErrorResponseBody` 处理非 2xx 状态码的错误体，但 2xx 状态码带错误体的场景未覆盖。本版在 `ConvertOpenAIToAnthropicResponse` 和 `ConvertAnthropicToOpenAIResponse` 入口增加错误信封检测。

**测试**：`TestConvertOpenAIToAnthropicResponse_ErrorEnvelope_Detected`、`TestConvertAnthropicToOpenAIResponse_ErrorEnvelope_Detected`。

---

### 修复 ⑫ P2-12：工具输出媒体递归提取

**根因**：tool_result 的 content 可能含嵌套的 image 块（如 `{"type":"image","source":{...}}`），当前 `flattenAnthropicToolResultContent` 只提取 text 块，image 块被 JSON 字符串化，a2o 方向多模态工具输出丢失。

**方案**：扩展 `flattenAnthropicToolResultContent`，递归提取 image 块为 data URI 文本引用：
```go
// 递归提取 tool_result content 中的 image 块为 data URI 文本引用
// （借鉴 cc-switch tool_media.rs 递归提取，深度上限 32）
func flattenAnthropicToolResultContent(content interface{}) string {
    return flattenToolResultContentRecursive(content, 0, 32)
}

func flattenToolResultContentRecursive(content interface{}, depth, maxDepth int) string {
    if depth > maxDepth {
        return "[content too deep]"
    }
    switch c := content.(type) {
    case string:
        return c
    case []interface{}:
        var parts []string
        for _, item := range c {
            m, ok := item.(map[string]interface{})
            if !ok { continue }
            switch m["type"] {
            case "text":
                if t, _ := m["text"].(string); t != "" { parts = append(parts, t) }
            case "image":
                if uri := extractImageAsDataURI(m); uri != "" {
                    parts = append(parts, "[image: "+uri+"]")
                }
            default:
                if b, err := json.Marshal(item); err == nil {
                    parts = append(parts, string(b))
                }
            }
        }
        return strings.Join(parts, " ")
    }
    return extractStringContent(content)
}
```

**测试**：`TestFlattenAnthropicToolResultContent_ImageBlock_AsDataURI`、`TestFlattenAnthropicToolResultContent_Nested_DepthLimited`、`TestFlattenAnthropicToolResultContent_Mixed_TextAndImage`。

---

## 4. 版本同步

- `APP_VERSION` 升级至 `v2.0.73`。
- 新增 `v2073_protocol_converter_ccswitch_enhancements_test.go`，覆盖上述 12 项修复的核心契约与边界情况。

---

## 5. 测试策略（继承 v2.0.72 强制规则）

- ✅ 协议转换测试必须用真实线格式 JSON 字符串做输入（v2.0.72 强制规则延续）。
- ✅ `AnthropicContentBlock` 必须保持平铺线格式建模（v2.0.72 强制规则延续）。
- ✅ 新增修复的测试必须覆盖「缺失输入 / NilDB / 边界值」三类防御场景。
- ✅ 回归测试必须显式锁死 cc-switch gh3805 同款缺陷（cache_control 泄漏）。

---

## 6. 完整问题清单（01 版遗留 + 02 版新增）

| # | 严重度 | 问题 | 状态 |
|---|--------|------|------|
| P0-1 | 致命 | o-series 模型 a2o 请求 `max_tokens` 未切换为 `max_completion_tokens` | 本版修复 |
| P0-2 | 致命 | o2a 请求缺少首条 user 消息保证 | 本版修复 |
| P0-3 | 严重 | o2a 请求缺少不完成工具轮次丢弃 | 本版修复 |
| P1-4 | 高 | a2o 请求 system 未规范化 | 本版修复 |
| P1-5 | 高 | Thinking ↔ Reasoning Effort 双向映射缺失 | 本版修复 |
| P1-6 | 高 | o2a 请求 thinking 预算未钳制 | 本版修复 |
| P1-7 | 高 | 强制 tool_choice + thinking 冲突未处理 | 本版修复 |
| P1-8 | 中 | o2a 响应消息级 refusal 未转 text 块 | 本版修复 |
| P1-9 | 中 | SSE 聚合缺少流截断检测 | 本版修复 |
| P1-10 | 中 | parseSSEEvents UTF-8 跨 chunk 边界 | 本版修复（防御性） |
| P2-11 | 低 | 非流式 o2a 响应上游错误信封检测 | 本版修复 |
| P2-12 | 低 | 工具输出媒体递归提取 | 本版修复 |

---

## 7. 二期方案（本版不处理）

- **真流式逐事件转换**（消除 `io.ReadAll` 整包缓冲）：需实现 `StreamTranslationState` 状态机，双向事件流逐事件转换，工作量大、风险高，列入二期。
- **Thinking 签名桥**（不透明传输 reasoning 项）：高复杂度，当前无 OpenAI Responses API 客户端，收益不足。
- **Value-Centric 重构**：v2.0.72 已选 Struct 路线，不改。
