# cc-switch OpenAI ↔ Anthropic 协议转换知识库

> 日期：2026-08-13
> 来源工程：[cc-switch](https://github.com/lencx/cc-switch)（Rust/Tauri 桌面代理，`src-tauri/src/proxy/`）
> 用途：为 LsmTokensServer（Go）协议转换模块的优化提供跨语言参考实现与工程实践对照

---

## 1. 工程定位与模块布局

cc-switch 是一个 Rust + Tauri 桌面代理，内嵌 HTTP 代理服务器（`src-tauri/src/proxy/`），在 AI 客户端（Claude Desktop / Codex CLI / Gemini CLI）与上游供应商之间做协议翻译。协议转换核心代码：

| 文件 | 职责 |
|------|------|
| `providers/transform.rs` (2000 行) | Anthropic ↔ OpenAI Chat Completions 双向 body 转换（a2o / o2a） |
| `providers/transform_codex_anthropic.rs` (3020 行) | OpenAI Responses API ↔ Anthropic Messages 非流式转换 |
| `providers/streaming_codex_anthropic.rs` (1218 行) | Responses ↔ Anthropic SSE 流式转换（显式状态机） |
| `providers/models/anthropic.rs` | Anthropic 类型定义（**文档性质**，实际转换不走这些 struct） |
| `providers/models/openai.rs` | OpenAI 类型定义（**文档性质**） |
| `providers/mod.rs` (541 行) | Provider 分发 / ProviderType 枚举 |
| `sse.rs` | SSE 解析基础设施（`strip_sse_field` / `take_sse_block` / `append_utf8_safe`） |
| `error.rs` + `error_mapper.rs` | ProxyError 枚举 + 错误 → HTTP 状态码映射 |
| `tool_media.rs` | 从 tool 输出中递归提取媒体（图片等，深度上限 32） |
| `reasoning_bridge.rs` | OpenAI reasoning 项经 Anthropic thinking/redacted_thinking 的不透明传输（版本前缀 + base64url） |
| `thinking_optimizer.rs` | 自适应 thinking 检测与注入 |
| `response_processor.rs` | 响应处理 + SSE usage 累加器 |
| `forwarder.rs` | 请求转发 + 连接守卫 |
| `handlers.rs` | HTTP 请求处理器 |

---

## 2. 类型建模方式：Value-Centric 动态转换

**关键发现**：`models/` 目录下的 typed struct（`AnthropicContentBlock` 嵌套枚举、`OpenAIMessage` 等）**仅具文档/参考性质**。实际转换代码全程操作 `serde_json::Value`，通过 `.get()` / `.pointer()` / `.and_then(|v| v.as_str())` 链动态访问字段。

这意味着 cc-switch **主动放弃了静态类型建模**，把"类型系统"等同于"线格式 JSON 本身"，在运行时动态校验。这与 LsmTokensServer v2.0.72 的方向（平铺 struct 建模 + 真实线格式测试锁死）是不同的工程取舍：

| 维度 | cc-switch（Value-Centric） | LsmTokensServer v2.0.72（平铺 Struct） |
|------|---------------------------|-------------------------------------|
| 建模与线格式一致性 | 天然一致（直接操作 JSON） | 需靠测试锁死（struct 易与线格式漂移） |
| 编译期校验 | 无 | 有（字段名/类型） |
| 字段缺失行为 | 运行时返回 None，需逐点防御 | 零值，需区分"未设置"与"显式 0" |
| 重构安全性 | 弱（字段名拼写错误编译期不报错） | 强 |

**可借鉴点**：cc-switch 的 Value-Centric 方式天然规避了"建模与线格式不符"的缺陷（这正是 LsmTokensServer P0-1 潜伏至今的根因）。但 LsmTokensServer 已通过 v2.0.72 的强制规则（平铺建模 + 真实 JSON 输入测试）弥补，不必改弦更张。

---

## 3. 请求转换（Anthropic → OpenAI，"a2o"）

入口：`transform.rs::anthropic_to_openai()` (line 131) 与 `anthropic_to_openai_with_reasoning_content()` (line 140)。

### 3.1 o-series 模型检测

```rust
pub fn is_openai_o_series(model: &str) -> bool {
    model.len() > 1
        && model.starts_with('o')
        && model.as_bytes().get(1).is_some_and(|b| b.is_ascii_digit())
}
```

o1 / o3 / o4-mini 等 o 系列模型**不接受 `max_tokens`**，必须映射为 `max_completion_tokens`。cc-switch 在 a2o 请求转换中检测 o-series 并做字段切换。

### 3.2 Temperature / Top_p 透传

直接 clone：
```rust
if let Some(v) = body.get("temperature") { result["temperature"] = v.clone(); }
if let Some(v) = body.get("top_p") { result["top_p"] = v.clone(); }
```

### 3.3 tool_use → tool_calls

```rust
"tool_use" => {
    let id = block.get("id").and_then(|i| i.as_str()).unwrap_or("");
    let name = block.get("name").and_then(|n| n.as_str()).unwrap_or("");
    let input = block.get("input").cloned().unwrap_or(json!({}));
    tool_calls.push(json!({
        "id": id, "type": "function",
        "function": { "name": name, "arguments": canonical_json_string(&input) }
    }));
}
```

`canonical_json_string()`（`json_canonical.rs`）将 input Value 序列化为紧凑、确定性 JSON 字符串作为 `arguments`。

### 3.4 图片多模态（tool_media.rs）

`chat_media_part_from_tool_part()` 处理 Anthropic image 块 → OpenAI `image_url` part：
- `{"type":"image","source":{"type":"base64","media_type":"...","data":"..."}}` → `image_url: "data:<media_type>;base64,<data>"`
- `{"type":"image","source":{"type":"url","url":"..."}}` → `image_url: {"url": "..."}`
- MCP image 形状 `{"type":"image","mimeType":"...","data":"..."}` → data URI

### 3.5 System 消息处理

```rust
if let Some(system) = body.get("system") {
    if let Some(text) = system.as_str() {
        messages.push(json!({"role": "system", "content": text}));
    } else if let Some(arr) = system.as_array() {
        for msg in arr {
            if let Some(text) = msg.get("text").and_then(|t| t.as_str()) {
                messages.push(json!({"role": "system", "content": text}));
            }
        }
    }
}
```

然后 `normalize_openai_system_messages()` (line 308) 做三件事：
1. 若恰有一条 system 消息不在 index 0 → 移到 index 0
2. 若有多条 system 消息 → 合并为一条（`\n` 拼接）
3. **从 system 消息中剥离 `cache_control` 字段**（回归测试 `test_regression_gh3805_no_cache_control_leak_to_openai` 锁死）

### 3.6 tool_result → role=tool 消息

Anthropic `tool_result` 块 → 独立 OpenAI `role: "tool"` 消息，保留 `tool_call_id`：
```rust
"tool_result" => {
    let tool_use_id = block.get("tool_use_id").and_then(|i| i.as_str()).unwrap_or("");
    result.push(json!({
        "role": "tool", "tool_call_id": tool_use_id, "content": content_str
    }));
}
```

### 3.7 Thinking / Reasoning Effort 映射

`resolve_reasoning_effort()` (line 94) 将 Anthropic `thinking` 配置映射为 OpenAI `reasoning_effort`：
1. 优先：`output_config.effort`（low/medium/high/max → xhigh）
2. 回退：`thinking.type` + `budget_tokens`：
   - `adaptive` → `xhigh`
   - `enabled` + budget < 4000 → `low`，4000–15999 → `medium`，≥ 16000 → `high`
   - `enabled` 无 budget → `high`

### 3.8 Tool Choice 映射

`map_tool_choice_to_chat()` (line 285)：
- `"any"` → `"required"`（OpenAI 无 "any"）
- `{"type":"tool","name":"X"}` → `{"type":"function","function":{"name":"X"}}`
- `"auto"` / `"none"` 透传

---

## 4. 响应转换（OpenAI → Anthropic，"o2a"）

入口：`transform.rs::openai_to_anthropic()` (line 552)。

### 4.1 tool_calls → tool_use 块

```rust
if let Some(tool_calls) = message.get("tool_calls").and_then(|t| t.as_array()) {
    for tc in tool_calls {
        let id = tc.get("id").and_then(|i| i.as_str()).unwrap_or("");
        let func = tc.get("function").unwrap_or(&empty_obj);
        let name = func.get("name").and_then(|n| n.as_str()).unwrap_or("");
        let args_str = func.get("arguments").and_then(|a| a.as_str()).unwrap_or("{}");
        let input: Value = serde_json::from_str(args_str).unwrap_or(json!({}));
        content.push(json!({
            "type": "tool_use", "id": id, "name": name, "input": input
        }));
    }
}
```

Legacy `function_call` 也作为回退支持。

### 4.2 Content 块处理

- `reasoning_content` → `{"type":"thinking","thinking":...}`
- 字符串 content → `{"type":"text","text":...}`
- Content 数组：`text` / `output_text` → text 块，`refusal` → text 块
- 消息级 `refusal` → text 块

### 4.3 Finish Reason 映射

```rust
let stop_reason = choice.get("finish_reason").and_then(|r| r.as_str()).map(|r| match r {
    "stop" => "end_turn",
    "length" => "max_tokens",
    "tool_calls" | "function_call" => "tool_use",
    "content_filter" => "end_turn",
    other => { log::warn!("Unknown finish_reason: {other}"); "end_turn" }
}).or(if has_tool_use { Some("tool_use") } else { None });
```

**注意**：cc-switch 对未知 finish_reason **降级为 `"end_turn"`**（带 warning 日志），而非原样透传。这与 LsmTokensServer v2.0.72 的"未识别值原样透传"策略不同——cc-switch 更保守（Anthropic 只接受有限 stop_reason 值）。

### 4.4 ID 前缀

cc-switch **不做** `msg_` ↔ `chatcmpl_` 前缀改写——id 原样透传。理由：代理向客户端呈现为目标协议端点，客户端已预期对应格式的 id。

### 4.5 Cache Token 映射

Anthropic `input_tokens` = 新鲜（非缓存）输入。OpenAI `prompt_tokens` = 含缓存的总量。转换时减去缓存部分：

```rust
let cached = usage.get("cache_read_input_tokens").and_then(|v| v.as_u64())
    .or_else(|| usage.pointer("/prompt_tokens_details/cached_tokens").and_then(|v| v.as_u64()))
    .unwrap_or(0);
let cache_creation = usage.get("cache_creation_input_tokens").and_then(|v| v.as_u64())
    .or_else(|| usage.pointer("/prompt_tokens_details/cache_write_tokens")
        .or_else(|| usage.pointer("/input_tokens_details/cache_write_tokens"))
        .and_then(|v| v.as_u64())).unwrap_or(0);
let input_tokens = usage.get("prompt_tokens").and_then(|v| v.as_u64()).unwrap_or(0)
    .saturating_sub(cached).saturating_sub(cache_creation) as u32;
```

恒等式：`input + cache_read + cache_creation == prompt_tokens`。

### 4.6 Created 时间戳

OpenAI `created` 时间戳在 o2a 方向**直接丢弃**（Anthropic 响应无 `created` 字段）。

---

## 5. SSE / 流式转换

### 5.1 SSE 解析基础设施（sse.rs）

- `strip_sse_field(line, field)` — 从 `field: value` 或 `field:value`（可选空格）中提取值
- `take_sse_block(buffer)` — 按 `\r\n\r\n` 或 `\n\n` 分割，返回完整 SSE 块
- `append_utf8_safe(buffer, remainder, new_bytes)` — **处理跨 chunk 边界的 UTF-8 多字节字符**（LsmTokensServer 当前未处理）

### 5.2 流翻译状态机

`AnthropicToResponsesState` struct (line 54) 维护：
- `response_started`, `completed` — 生命周期标志
- `response_id`, `model` — 来自 `message_start`
- `next_output_index` — 输出项单调递增索引
- `blocks: BTreeMap<u64, BlockState>` — 按 Anthropic index 键的每块状态
- `output_items: Vec<(u32, Value)>` — 已完成输出项
- `anthropic_usage`, `stop_reason`, `stream_truncated`, `tool_context`

每个 `BlockState` (line 38) 跟踪：`kind: BlockKind`（Text/Tool/Thinking）、`output_index`、`item_id`、`call_id`、`name`、`accum: String`（累积 text/json）、`start_input: String`（来自 `content_block_start` 的回退）、`source_block: Value`、`has_visible_summary`、`done`。

### 5.3 事件流

1. `message_start` → 发出 `response.created` + `response.in_progress`
2. `content_block_start` → 创建 BlockState，发出 item-added 事件
3. `content_block_delta` → 累加到块，发出 delta 事件
4. `content_block_stop` → 关闭块，发出 done 事件
5. `message_delta` → 捕获 stop_reason 和 usage
6. `message_stop` → 以 `response.completed` 收尾

### 5.4 content_block_start 携带 id/name/input

`tool_use` 块的 `content_block_start` 携带 `id`、`name` 和可选 `input`：
```rust
"tool_use" => {
    let call_id = block.get("id").and_then(|v| v.as_str()).unwrap_or("");
    let name = block.get("name").and_then(|v| v.as_str()).unwrap_or("");
    let start_input = block.get("input")
        .filter(|v| v.as_object().map(|o| !o.is_empty()).unwrap_or(false))
        .map(|v| v.to_string()).unwrap_or_default();
}
```

### 5.5 input_json_delta 排序与回退

`input_json_delta` 按序累加到 `block.accum`。在 `content_block_stop` 时解析：
```rust
let raw_input = if text.trim().is_empty() {
    block.start_input.clone()  // 回退到 start 携带的 input
} else { text };
let arguments = if raw_input.trim().is_empty() {
    "{}".to_string()
} else if name == "Read" {
    sanitize_anthropic_tool_use_input_json("Read", &raw_input)
} else {
    canonicalize_tool_arguments_str(&raw_input)
};
```

**关键**：当 `input_json_delta` 先于 `content_block_start` 到达时，按 `tool_use` 建块（LsmTokensServer v2.0.72 已采纳）。

### 5.6 signature_delta

`signature_delta` 不产生 SSE 事件——存入 `source_block["signature"]` 供后续重建：
```rust
"signature_delta" => {
    if let Some(signature) = delta.get("signature").and_then(Value::as_str) {
        block.source_block["signature"] = json!(signature);
    }
    Vec::new()  // 不发事件
}
```

### 5.7 thinking_delta

`thinking_delta` → Responses 协议的 `reasoning_summary_text_delta`：
```rust
"thinking_delta" => {
    let text = delta.get("thinking").and_then(|t| t.as_str()).unwrap_or("");
    block.accum.push_str(text);
    block.source_block["thinking"] = json!(block.accum);
    vec![sse::reasoning_summary_text_delta(output_index, &item_id, text)]
}
```

### 5.8 空 content 处理

- 空 text 块跳过（不发出）
- 空 arguments 的 tool call 默认为 `"{}"`
- thinking-only 消息（无 text/tool_use）在 a2o 方向不产出输出消息

### 5.9 流尾残留处理

流结束后，若缓冲区有残留内容（无尾部空行）：
```rust
if !stream_failed && !buffer.trim().is_empty() {
    if !state.response_started {
        // 尝试作为完整 JSON 文档（网关忽略了 stream:true）
        if let Some(candidate) = json_document_candidate(&buffer) {
            if let Ok(body) = serde_json::from_str::<Value>(candidate) {
                for event in responses_sse_events_from_anthropic_message(&body, ...) {
                    yield Ok(event);
                }
            }
        }
    }
    if !state.completed {
        let (events, failed) = process_anthropic_sse_block(&mut state, &buffer);
        // ...
    }
}
```

### 5.10 流截断检测

流在无 `message_stop` 时结束：
- 有输出 → 报告 `incomplete`，reason=`max_output_tokens`
- 无输出 → 报告 `failed`，`stream_truncated`

```rust
if state.stop_reason.is_some() {
    // 正常收尾
} else if state.has_substantive_output() {
    state.stop_reason = Some("max_tokens".to_string());
    state.stream_truncated = true;
    // 作为 incomplete 收尾
} else {
    // failed_event: "Upstream Anthropic stream ended before message_stop"
}
```

---

## 6. 错误转换

### 6.1 上游错误信封

```rust
if body.get("type").and_then(Value::as_str) == Some("error") || body.get("error").is_some() {
    let error = body.get("error").unwrap_or(&body);
    let message = error.get("message").and_then(Value::as_str)
        .or_else(|| error.as_str())
        .unwrap_or("Anthropic upstream returned an error envelope");
    let error_type = error.get("type").and_then(Value::as_str).unwrap_or("error");
    return Err(ProxyError::TransformError(format!("Anthropic upstream {error_type}: {message}")));
}
```

### 6.2 SSE error 事件

`process_anthropic_sse_block()` 中 `error` 事件类型触发 `failed_event()`。`extract_anthropic_sse_error()` 同时处理字符串错误和结构化 `{"type":"...","message":"..."}` 错误。

### 6.3 ProxyError → HTTP 状态码

| ProxyError | HTTP 状态 |
|---|---|
| `UpstreamError { status, .. }` | 直接使用上游状态码 |
| `TransformError(_)` | 422 Unprocessable Entity |
| `Timeout(_)` / `StreamIdleTimeout(_)` | 504 Gateway Timeout |
| `AuthError(_)` | 401 Unauthorized |
| `InvalidRequest(_)` | 400 Bad Request |

---

## 7. 值得借鉴的设计模式

### 7.1 不完成工具轮次丢弃（`drop_incomplete_tool_turns`）

`transform_codex_anthropic.rs:924`：校验 assistant `tool_use` 块在紧随的 user 消息中有匹配的 `tool_result` 块。不完成轮次**整体丢弃**，避免 Anthropic 400 错误。

### 7.2 首条 user 消息保证（`ensure_leading_user_message`）

`transform_codex_anthropic.rs:902`：若对话历史以 assistant 消息开头，插入合成 `"(continuing the conversation)"` user 消息——Anthropic 要求第一条消息必须来自 user。

### 7.3 Thinking 预算钳制

o2a 方向将 Responses `reasoning.effort` 转为 Anthropic `budget_tokens` 时，钳制到 `max_tokens / 2`，确保可见答案有足够空间：
```rust
let ceiling = max_tokens / 2;
thinking_budget = thinking_budget.min(ceiling);
if thinking_budget < 1024 {
    thinking_enabled = false;
}
```

### 7.4 强制 tool_choice 与 thinking 冲突处理

客户端请求强制 tool_choice（`required` 或指定工具）且 thinking 启用时，转换器**禁用 thinking**（Anthropic 拒绝强制工具 + thinking 组合）：
```rust
if thinking_enabled && forced {
    result["thinking"] = json!({ "type": "disabled" });
    result.as_object_mut().unwrap().remove("output_config");
    // 恢复 temperature/top_p
}
```

### 7.5 Thinking 签名桥（reasoning_bridge.rs）

OpenAI Responses `reasoning` 项经 Anthropic `thinking`/`redacted_thinking` 块的不透明传输，使用版本前缀 `ccswitch-openai-reasoning-v1:` + base64url 编码：
```rust
pub(crate) const OPENAI_REASONING_ITEM_PREFIX: &str = "ccswitch-openai-reasoning-v1:";
pub(crate) fn encode_openai_reasoning_item(item: &Value) -> Option<String> {
    let bytes = serde_json::to_vec(item).ok()?;
    Some(format!("{OPENAI_REASONING_ITEM_PREFIX}{}", URL_SAFE_NO_PAD.encode(bytes)))
}
```

对称地，`ANTHROPIC_THINKING_ENCRYPTED_PREFIX` 携带 Anthropic 签名 thinking 块经 Responses `encrypted_content` 字段。

### 7.6 工具输出媒体递归提取（tool_media.rs）

- 标量字符串 data URI
- JSON 字符串编码 content（含媒体块）
- 嵌套对象遍历（深度上限 32）
- 大残留 base64 负载钳制

### 7.7 工具命名空间解析（CodexToolContext）

跟踪工具命名空间映射，使 `mcp_files__read` 等命名工具在响应路径上能拆回 `namespace: "mcp_files", name: "read"`。

---

## 8. 测试方法

### 8.1 线格式 JSON 测试

测试使用 `serde_json::json!()` 宏构造**真实线格式 JSON**，而非 typed struct。这验证的是实际线格式，而非中间类型模型。

### 8.2 SSE 流测试

构造原始 SSE 文本字符串，通过转换器后断言输出字符串：
```rust
#[tokio::test]
async fn test_text_stream() {
    let input = concat!(
        "event: message_start\n",
        "data: {\"type\":\"message_start\",\"message\":{...}}\n\n",
        "event: content_block_start\n", ...
    );
    let merged = run(input).await;
    assert!(merged.contains("event: response.created"));
    assert!(merged.contains("\"delta\":\"Hello\""));
}
```

### 8.3 回归测试关联 Issue

- `test_regression_gh3805_no_cache_control_leak_to_openai` — cache_control 不泄漏到 OpenAI
- `test_anthropic_to_openai_strips_cache_control_from_merged_system` — cache_control 剥离
- `test_openai_to_anthropic_clamps_input_when_cache_exceeds_prompt` — saturating_sub 守卫

### 8.4 边界情况覆盖

- 空 arguments 解析为 `{}`
- tool input 仅在 `content_block_start` 中（无 delta）
- 截断流（有/无输出）
- 非 object content 块（形状守卫）
- 非 object 消息体（防 panic）
- 原始 JSON 体（网关忽略 `stream:true`）
- 缺失尾部空行
- `message_stop` 后的 error 事件（不重复终端事件）
- 未知 tool_choice 对象形状降级为 `auto`
- 孤儿 tool_result 块丢弃
- 纯空白 assistant 文本修剪
- `pause_turn` stop_reason（意外，记日志 + 视为 completed）

---

## 9. 与 LsmTokensServer v2.0.72 的差异对照

| 维度 | cc-switch | LsmTokensServer v2.0.72 | 备注 |
|------|-----------|----------------------|------|
| 类型建模 | Value-Centric（动态 JSON） | 平铺 Struct（静态） | 两种有效取舍 |
| 未知 finish_reason | 降级为 `"end_turn"` + warning | **原样透传** | cc-switch 更保守 |
| ID 前缀改写 | **不做**（原样透传） | 做（`msg_` ↔ `chatcmpl_`） | cc-switch 认为不必要 |
| o-series 检测 | 有（`max_tokens` → `max_completion_tokens`） | **无** | LsmTokensServer 缺失 |
| System 消息规范化 | 有（移 index 0 / 合并 / 剥 cache_control） | **无**（仅 prepend） | LsmTokensServer 缺失 |
| Thinking ↔ Reasoning Effort 映射 | 有（双向） | **无**（thinking 丢弃/当文本） | LsmTokensServer 缺失 |
| Thinking 预算钳制（max/2） | 有 | **无** | LsmTokensServer 缺失 |
| 强制 tool_choice vs thinking 冲突 | 有（禁用 thinking） | **无** | LsmTokensServer 缺失 |
| 不完成工具轮次丢弃 | 有 | **无** | LsmTokensServer 缺失 |
| 首条 user 消息保证 | 有 | **无** | LsmTokensServer 缺失 |
| SSE UTF-8 跨 chunk 边界处理 | 有（`append_utf8_safe`） | **无** | LsmTokensServer 缺失 |
| 流截断检测 | 有（incomplete/failed 区分） | **无**（静默聚合） | LsmTokensServer 缺失 |
| 上游错误信封检测（非流式） | 有 | 有（`ConvertProtocolErrorResponseBody`） | 已有 |
| Refusal → text 块 | 有 | **部分**（extractTextPartsContent 依赖 content 数组形态） | 需补消息级 refusal |
| 工具输出媒体递归提取 | 有（深度 32） | **无** | LsmTokensServer 缺失 |
| Thinking 签名桥（不透明传输） | 有（base64url + 版本前缀） | **无** | LsmTokensServer 缺失，高复杂度 |
