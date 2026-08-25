# Switchyard OpenAI ↔ Anthropic 协议转换知识库

> 调研对象：`/usr/local/LsmGitOpenSource/Switchyard`（NVIDIA NeMo Switchyard，Rust 实现的 LLM 流量代理/库）
> 重点 crate：`crates/switchyard-translation/`；IR 类型定义：`crates/protocol/`（crate 名 `switchyard-protocol`）
> 整理日期：2026-08-13；用途：为 LsmTokensServer 协议转换模块优化提供参考

---

## 1. 整体架构

### 1.1 核心思想：中性 IR + 双向 Codec

Switchyard **不做** "OpenAI ↔ Anthropic 点对点转换"，而是定义一套 provider 中立的会话 IR（Intermediate Representation）。每种 wire format 只需实现"自家格式 ↔ IR"的 codec，任何 A→B 转换都是两段式：

```
decode(A) → IR → encode(B)
```

天然支持 N 种格式只需 N 个 codec（工程内已有第三种格式 OpenAI Responses API 证明可扩展性）。

依据：
- `crates/switchyard-translation/src/lib.rs:4-8`："translates provider wire formats through a neutral Switchyard conversation IR. It intentionally has no dependency on provider SDKs, HTTP servers, Python objects, or FFI bindings."
- `crates/switchyard-translation/src/engine.rs:148-169` `TranslationEngine::translate_request`：先 `codec(source).decode_request`，再 `codec(target).encode_request`。

### 1.2 模块划分（`crates/switchyard-translation/src/`）

| 文件                                          | 职责                                                                                                                                            |
| --------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------- |
| `lib.rs`                                      | crate 入口，re-export IR 类型（`format` / `llm` / `stream` 模块）                                                                               |
| `engine.rs`                                   | `FormatRegistry`（格式→codec 注册表）+ `TranslationEngine`（无状态翻译门面）                                                                    |
| `policy.rs`                                   | `TranslationPolicy` 策略旋钮（见 §1.4）                                                                                                         |
| `error.rs`                                    | `TranslationError` 枚举（thiserror）                                                                                                            |
| `diagnostic.rs`                               | `TranslationDiagnostic` 结构化告警（severity/code/message/source/target/path）                                                                  |
| `util.rs`                                     | 校验、preservation（无损回放）、tool_use id 清洗等共享工具                                                                                      |
| `helpers.rs`                                  | 便捷函数：`decode_request` / `encode_request` / `decode_aggregated_response` / `encode_aggregated_response` / `decode_stream` / `encode_stream` |
| `sse.rs`                                      | 极简 SSE 帧解析器（`SseFrame::{Empty, Done, Data(Value)}`），不含任何 HTTP 类型                                                                 |
| `codecs/mod.rs`                               | `FormatCodec` trait + `DecodedRequest/EncodedRequest/DecodedResponse/EncodedResponse` 四元组                                                    |
| `codecs/common.rs`                            | 三个 codec 共享的小函数（角色名白名单、文本拼接、未知字段收集）                                                                                 |
| `codecs/anthropic/{mod,buffered,stream}.rs`   | Anthropic Messages codec                                                                                                                        |
| `codecs/openai_chat/{mod,buffered,stream}.rs` | OpenAI Chat Completions codec                                                                                                                   |
| `codecs/responses/{mod,buffered,stream}.rs`   | OpenAI Responses API codec（第三格式）                                                                                                          |
| `codecs/stream.rs`                            | `StreamCodec` trait + `StreamCodecRegistry` + `StreamTranslationState` 状态机                                                                   |

### 1.3 Codec 抽象

**Buffered 模式**（`codecs/mod.rs:45-68`）：

```rust
pub trait FormatCodec: Send + Sync {
    fn format(&self) -> FormatId;
    fn decode_request(&self, body: &Value, policy: &TranslationPolicy) -> Result<DecodedRequest>;
    fn encode_request(&self, request: &LlmRequest, policy: &TranslationPolicy) -> Result<EncodedRequest>;
    fn decode_response(&self, body: &Value, policy: &TranslationPolicy) -> Result<DecodedResponse>;
    fn encode_response(&self, response: &AggLlmResponse, policy: &TranslationPolicy) -> Result<EncodedResponse>;
}
```

关键决策：codec 直接操作 `serde_json::Value`，**不为每种 provider 定义 serde struct**——所有建模负担集中在 IR 上，provider 侧用 `Value` + 显式字段提取。四个方向各自返回 `diagnostics: Vec<TranslationDiagnostic>`，降级信息全程可观测。

**Stream 模式**（`codecs/stream.rs`）：

```rust
pub trait StreamCodec: Send + Sync {
    fn format(&self) -> FormatId;
    fn decode_event(&self, state: &mut StreamTranslationState, event: &Value) -> Vec<LlmResponseChunk>;
    fn encode_event(&self, state: &mut StreamTranslationState, event: LlmResponseChunk) -> Vec<Value>;
    fn observe_replayed_event(&self, state: &mut ..., raw: &Value, normalized: Vec<LlmResponseChunk>) { ... }
    fn finish(&self, state: &mut StreamTranslationState) -> Vec<Value>;
}
```

- 一个输入事件可产生 **0..n** 个输出事件（`Vec` 返回）——例如 Anthropic 一个 `content_block_delta` 可能要先补 `content_block_start`。
- `finish()` 是**强制实现**（no-op 也要显式返回空 vec）：源流正常 EOF 后，目标格式可能还需补发终止事件（Anthropic 的 `message_delta`/`message_stop`、Responses 的 `response.completed`）。
- `observe_replayed_event` 处理"同格式原样回放"：回放跳过 encode，但 encoder 状态机仍要观察归一化 chunk，否则 `finish` 会重复发或漏发终止事件。

**引擎**：`TranslationEngine`（`engine.rs:83`）持有 `FormatRegistry` + `StreamCodecRegistry`；`translate_event(state, source, target, event)` 逐事件 decode→encode。注册表开放——`tests/extension_points.rs` 证明第三方可注册自定义 `FormatId`/codec 而不动内置格式。

### 1.4 policy.rs：TranslationPolicy 五个旋钮

| 字段                      | 取值                                                     | 作用                                                                                                                 |
| ------------------------- | -------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------- |
| `unknown_field_policy`    | `Preserve` / `DropWithWarning` / `Reject`                | 未知 provider 字段如何处理                                                                                           |
| `lossy_conversion_policy` | `AllowWithDiagnostics` / `Reject`                        | 已知有损转换是告警放行还是报错                                                                                       |
| `deterministic_ids`       | `Preserve` / `GenerateStable{prefix}`（默认前缀 `"sw"`） | 缺失 tool_call id 时生成稳定 id（`sw_00000001`）                                                                     |
| `preservation`            | `InMemory`（默认）/ `Embed` / `Disabled`                 | 是否在 IR 保留原始 body 实现同格式无损回放；`Embed` 把原始 body 塞进 `metadata._switchyard_translation` 支持多跳往返 |
| `target_capabilities`     | `TargetCapabilities`（supports_tools/images/audio/...）  | 目标端能力声明，`validate_request_capabilities`（`util.rs:124`）据此 fail-fast 或告警                                |

默认值（`policy.rs:80-92`）：Preserve + AllowWithDiagnostics + GenerateStable("sw") + InMemory——即"尽量转换成功、降级留痕"。

---

## 2. 数据模型

### 2.1 中性 IR（`crates/protocol/src/llm.rs`）

全部 serde 类型化 struct/enum，snake_case 序列化：

- **`LlmRequest`**（llm.rs:302）：
  - `model: Option<String>`
  - `instructions: Vec<InstructionBlock>` —— system/developer 指令**与会话消息分离**（关键设计）
  - `messages: Vec<Message>`
  - `tools` / `tool_choice`
  - `sampling: SamplingParams{temperature, top_p, top_k}`
  - `output: OutputParams{max_output_tokens, response_format}`
  - `reasoning: ReasoningParams{effort, raw}`
  - `stream: bool`
  - `extensions: ProviderExtensions` / `preservation: PreservationMetadata`
- **`Role`**（llm.rs:16）：`System | Developer | User | Assistant | Tool` 五值枚举。
- **`ContentBlock`**（llm.rs:78，`#[serde(tag = "type")]`）：
  `Text / Reasoning{text, signature} / Image / Audio / Video / File / ToolCall(ToolCall) / ToolResult(ToolResult) / Refusal / Unknown{provider, raw}`。
  **`Unknown` 变体保留原始 JSON** 是容忍未知块的核心机制。
- **`ImageSource`/`FileSource`/`MediaSource`**：各自 `Url{...} / Base64{...} / Raw(Value)` 三态，`Raw` 兜底无法归一化的形状。
- **`ToolCall{id, name, arguments: Value}`**、**`ToolResult{tool_call_id, content, is_error}`**、**`ToolDefinition{name, description, parameters: Value, strict}`**、**`ToolChoice::{Auto, Required, None, Tool{name}, Raw(Value)}`**。
- **`AggLlmResponse`**（llm.rs:435）：`id / model / outputs: Vec<ResponseOutput{role, content, stop_reason}> / usage / extensions / preservation`。
- **`Usage`**（llm.rs:331）：`input_tokens / cache: Option<Box<InputCacheUsage>>（flatten）/ output_tokens / total_tokens / reasoning_tokens`。注意 `input_tokens` 语义是**非缓存输入 token**——OpenAI decode 时会从 `prompt_tokens` 减去 cached 部分（见 §4.2）。
- **`StopReason`**（llm.rs:405）：`EndTurn / MaxTokens / ToolUse / ContentFilter / Error / Unknown` 六值。

**未知字段容忍策略**：不用 serde `deny_unknown_fields`，而是解码后调用 `provider_extensions(body, &[已知字段名单])`（`codecs/common.rs:45-56`）把所有未识别 key 原样收进 `extensions.fields` map。编码时按白名单挑回（如 OpenAI 编码器 `copy_openai_chat_request_extensions`，`openai_chat/buffered.rs:813-837`，白名单含 `metadata/parallel_tool_calls/prompt_cache_key/service_tier/stream_options/user` 等，`stop_sequences` 反向映射回 `stop`）。

### 2.2 流式 IR（`crates/protocol/src/stream.rs`）

`LlmResponseChunk`（stream.rs:253）七种：

```rust
MessageStart { id, model }
TextDelta { index, text }
ReasoningDelta { index, text }
ToolCallDelta { index, id, name, arguments_delta }   // id/name 通常只在首个 delta 出现
Usage(Usage)
MessageStop { reason: Option<String> }               // reason 保留 provider 原始字符串
StreamError { message } / DecodeError { message }
```

`LlmResponseStreamEvent{ preservation: Option<ProviderStreamEvent>, normalized: Vec<LlmResponseChunk> }`：每个流事件同时携带归一化 chunk 和原始 provider JSON；同格式回放时直接放行原始 JSON（`codecs/stream.rs` 中 `if &source == target { ...; return vec![raw]; }`）。

`ResponseAccumulator`（stream.rs:320）把 chunk 流折叠回 `AggLlmResponse`：tool arguments 字符串拼接后最后一次性 `serde_json::from_str`，解析失败降级为 `Value::String`，空串降级为 `{}`（`parse_tool_arguments`，stream.rs:423-428）。

---

## 3. 请求转换算法（双向）

所有转换都是"OpenAI Chat codec 的 decode/encode"与"Anthropic codec 的 decode/encode"经 IR 组合，**不存在专门的 OpenAI→Anthropic 函数**。以下按 IR 两侧的映射规则描述。

### 3.1 system prompt 映射

- **OpenAI → IR**（`openai_chat/buffered.rs:136-141`）：`role=system|developer` 的消息不进 `messages`，推入 `request.instructions`，保留 role。
- **IR → Anthropic**（`anthropic/buffered.rs:178-190`）：所有 instructions 的 Text/Refusal 块取出，**用 `"\n\n"` 拼接成单个字符串**写入顶层 `system` 字段；空则不写。测试 `openai_request_translates_system_developer_and_reasoning_to_anthropic`（`tests/request_translation.rs:1036`）断言 `"system" == "System rules.\n\nDeveloper rules."`。
- **Anthropic → IR**（`anthropic/buffered.rs:427-454` `decode_anthropic_system`）：顶层 `system` 支持**字符串或 text 块数组**两种形态；空串/null 不产生指令；其他类型报 `InvalidType`。
- **IR → OpenAI**：每条 instruction 编码为独立消息，role 按 `Developer→"developer"`、其余→`"system"`（`openai_chat/buffered.rs:195-204`）。
- 边界注意：多条 system/developer 消息的边界在 OpenAI 侧保留，到 Anthropic 侧被拍平成一个字符串（Anthropic 顶层 system 天然单字段）；反向时 Anthropic 数组型 system 的块边界到 OpenAI 会丢失。

### 3.2 多模态内容块映射

- OpenAI `content` 字符串 → 单 `Text` 块；数组逐块映射（`decode_openai_content`，`openai_chat/buffered.rs:430-501`）：`text/input_text/output_text→Text`、`refusal→Refusal`、`image_url/input_image→Image`、`file/input_file→File`、未识别→`Unknown{provider, raw}`。
- OpenAI `image_url` 兼容两种形状：`"image_url": "url字符串"` 和 `"image_url": {"url": ..., "detail": ...}`（`decode_image_source`，:504-539）。
- Anthropic `image` 块的 `source` 原样保留为 `ImageSource::Raw`（`anthropic/buffered.rs:557-564`）；编码到 OpenAI 时 `openai_raw_image_part`（:957-974）尝试识别 `{url}` / `{image_url}` / `{data, media_type}` 三种常见形状，转成 `image_url` part（base64 走 `data:` URI），识别不了则 `push_lossy` 告警并降级为文本 part。
- **Anthropic → OpenAI 的 image source 映射**（`encode_one_anthropic_block`，`anthropic/buffered.rs:823-836`）：`Url→{"type":"url"}`、`Base64→{"type":"base64", media_type 缺省 "image/png"}`。
- 关键边界：**OpenAI 非 user 角色消息只支持文本**——`encode_openai_content`（:845-871）检测到非文本块且 role≠User 时 `push_lossy` 并退化为纯文本拼接。
- Anthropic 的 `document`/`audio`/`video` 块在 encode 时也有映射（:837-878），OpenAI 侧 audio/video 尚无稳定映射，统一降级为文本 + 告警。

### 3.3 Tool/function calling 双向映射

**tools 定义**：
- OpenAI：`{"type":"function","function":{"name","description","parameters","strict"}}` ↔ IR `ToolDefinition{name, description, parameters, strict}`（`decode_openai_tools`，:614-654；容忍无 `function` 包装层的裸工具）。
- Anthropic：`{"name","description","input_schema"}` ↔ IR（`decode_anthropic_tools`，`anthropic/buffered.rs:613-635`；`input_schema` 缺省 `{}`；**无 name 的工具直接丢弃**——测试 `anthropic_tool_without_name_is_dropped_before_openai_chat`）。

**tool_choice**：
- OpenAI `"auto"/"required"/"none"/{"type":"function","function":{"name"}}` ↔ IR `Auto/Required/None/Tool{name}`，其他形状收进 `Raw(Value)` 原样透传。
- Anthropic：decode `"any"→Required`、`{"type":"tool","name"}→Tool{name}`；encode `Required→{"type":"any"}`、`Tool{name}→{"type":"tool","name"}`（`encode_anthropic_tool_choice`，:922-930）。

**assistant 的 tool_calls ↔ tool_use**：
- OpenAI decode（:97-111）：assistant 消息若有 `tool_calls` 数组且 content 只是占位空文本则清空，逐个转为 `ContentBlock::ToolCall`。arguments 是**字符串**，用 `parse_arguments`（:606-611）尝试 JSON 解析，失败包成 `{"raw": 原文}`。
- Anthropic decode（`anthropic/buffered.rs:529-547`）：`tool_use` 块 → `ToolCall{id, name, arguments: input 原样（本来就是 object）}`；id 为空时按 `deterministic_ids` 策略生成 `sw_NNNNNNNN` 稳定 id。
- IR → Anthropic（`encode_one_anthropic_block` :812-817 + `anthropic_tool_input` :885-903）：**Anthropic 要求 `tool_use.input` 必须是 object**。IR 中 arguments 若是字符串（典型来自 OpenAI），先尝试解析 JSON；解析失败包 `{"raw": text}`；非 object 的 JSON 包 `{"value": ...}`；null→`{}`。**宁可包装也不丢数据**。id 经 `sanitize_anthropic_tool_use_id`（`util.rs:331-347`）清洗为 `[a-zA-Z0-9_-]`，非法字符替换 `_`，空 id 变 `toolu_empty`。
- IR → OpenAI（`encode_message_without_tool_results_to_openai`，:755-802）：`ToolCall` 块收集为 `tool_calls` 数组，arguments 用 `json_string` 重新字符串化；**有 tool_calls 且 content 为空串时 content 置 null**（符合 OpenAI 习惯）。

**tool 结果消息 ↔ tool_result**：
- OpenAI decode（:112-135）：`role=tool` 消息整体转为 **`Role::User` + 单 `ToolResult` 块** 的 IR 消息（is_error 信息丢失，置 None）。
- IR → Anthropic（`encode_anthropic_messages`，:692-723）：**连续的"只含 ToolResult 块"的消息被合并成一条 user 消息**，content 为多个 `tool_result` 块——满足 Anthropic"tool_result 必须在 user 消息里且相邻"的契约。判定函数 `message_is_tool_result_only`（:735-742）。
- IR → OpenAI（`encode_message_with_tool_results_to_openai`，:693-728）：反过来，一条 IR 消息里混有 ToolResult 块时**拆分**成多条消息：每个 ToolResult 变一条独立 `role:"tool"` + `tool_call_id` 消息，夹在中间的普通内容按原 role 另发。测试 `anthropic_tool_result_followup_text_splits_to_openai_messages` 锁定此行为。
- Anthropic `tool_result` 的 content（可能是数组）统一拍平为文本（`decode_tool_result_content`，:579-610：text 块提取、非 text 块 JSON 字符串化、空格连接）。

### 3.4 参数映射

| 参数                | OpenAI → IR                                                                      | IR → Anthropic                                                                                                                                                                                  | 备注                                                                                                        |
| ------------------- | -------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------------------------------------------------------------------------------------------- |
| model               | 透传（空串视为 None）                                                            | 透传                                                                                                                                                                                            |                                                                                                             |
| max_tokens          | `max_completion_tokens` 优先，fallback `max_tokens` → `output.max_output_tokens` | 有值写 `max_tokens`；**无值强制写 64000**（`anthropic/buffered.rs:215-219`）                                                                                                                    | Anthropic `max_tokens` 必填，测试 `openai_request_to_anthropic_adds_required_default_max_tokens` 锁定 64000 |
| temperature / top_p | 透传                                                                             | 透传                                                                                                                                                                                            | Anthropic 方向额外支持 `top_k`（OpenAI 侧 top_k 恒 None）                                                   |
| stop                | OpenAI `stop` 无一等 IR 字段，落入 `extensions.fields["stop"]`                   | `anthropic_stop_sequences_from_extensions`（:726-732）：字符串包成单元素数组，数组原样 → `stop_sequences`                                                                                       | 反向：Anthropic `stop_sequences` 进 extensions，OpenAI 编码时改名回 `stop`（:834-836）                      |
| stream              | 透传 bool                                                                        | 仅 true 时写入                                                                                                                                                                                  |                                                                                                             |
| reasoning           | OpenAI `reasoning_effort` → `reasoning.effort`                                   | 有 effort 时写 `thinking:{"type":"adaptive"}` + `output_config:{"effort":...}`；Anthropic `thinking` 原文进 `reasoning.raw`                                                                     | reasoning 跨格式**不互相伪造**，只走各自私有通道                                                            |
| response_format     | OpenAI 原文进 `output.response_format`                                           | 仅接受 `json_schema` 类型，写入 `output_config.format`；**递归剥离 Anthropic 不支持的 `minimum/maximum/minLength/maxLength` 约束并告警**（`strip_anthropic_unsupported_constraints`，:403-424） |                                                                                                             |

### 3.5 角色映射与边界情况

- **OpenAI decode**（`role_from_openai`，:417-427）：`system/developer/assistant/tool` 精确映射；缺 role → User；legacy `function` → User（历史兼容）；**完全未知的 role（如 `"api"`）直接报错** `unsupported_role`，错误文案刻意模仿 provider 的 invalid_value（`error.rs:61-69`）——"透明代理必须拒绝 provider 会拒绝的东西，而不是悄悄 coerce 成 user"。
- **Anthropic decode**（`anthropic/buffered.rs:109-119`）：只认 `assistant`，其余已知 role 全部归 User，未知 role 同样报错。
- **IR → Anthropic 的角色坍缩**（`encode_anthropic_message`，:666-669）：`User|Tool|System|Developer` 全编成 `"user"`（System/Developer 正常不会出现在 messages 里，它们在 instructions）。
- **连续同角色合并**：只有"连续 tool-result-only 消息合并"是显式实现的（§3.3）。
- 响应侧解码更宽松（`responses/buffered.rs:684-687`）：provider **响应**里的意外 role coerce 为 user 而不是整个响应报错——**请求严格、响应宽松**的不对称策略。

---

## 4. 响应转换算法（双向）

### 4.1 stop_reason ↔ finish_reason 映射表

IR 用 `StopReason` 六值枚举：

**Anthropic → IR**（`map_anthropic_stop_reason`，`anthropic/buffered.rs:976-983`）：
`max_tokens→MaxTokens`，`tool_use→ToolUse`，`end_turn`/None→EndTurn，其他→Unknown。

**IR → Anthropic**（`anthropic_stop_reason`，:986-995）：
`MaxTokens→"max_tokens"`，`ToolUse→"tool_use"`，**`EndTurn|ContentFilter|Error|Unknown` 全部坍缩为 `"end_turn"`**（有损）。

**OpenAI → IR**（`map_openai_finish_reason`，`openai_chat/buffered.rs:1133-1141`）：
`length→MaxTokens`，`tool_calls|function_call→ToolUse`，`content_filter→ContentFilter`，`stop`/None→EndTurn，其他→Unknown。

**IR → OpenAI**（`openai_finish_reason`，:1144-1151`）：
`MaxTokens→"length"`，`ToolUse→"tool_calls"`，`ContentFilter→"content_filter"`，`EndTurn|Unknown|Error→"stop"`。

流式路径另有字符串级映射（不走枚举）：`anthropic/stream.rs:571-580` 与 `openai_chat/stream.rs:395-402` 直接做 provider 字符串 → provider 字符串，**未识别的 OpenAI 原因原样透传**（`Some(other) => other.to_string()`）。

### 4.2 usage 映射

IR `Usage.input_tokens` 语义 = **非缓存输入 token**：

- **OpenAI decode**（`decode_openai_usage`，`openai_chat/buffered.rs:1065-1103`）：`prompt_tokens - cached_tokens - cache_write_tokens`（saturating_sub）→ `input_tokens`；cache 明细进 `cache`；`completion_tokens→output_tokens`；`completion_tokens_details.reasoning_tokens`→`reasoning_tokens`；`total_tokens` 原样。
- **Anthropic decode**（`decode_anthropic_usage`，`anthropic/buffered.rs:933-958`）：`input_tokens` 直接用；`cache_read_input_tokens`/`cache_creation_input_tokens` 进 cache 明细；`total_tokens` **计算得出** = input + cache_read + cache_creation + output。
- **IR → OpenAI**（`encode_openai_usage`，:1106-1130）：`prompt_tokens = input + cache_read + cache_creation`（加回来）；有 cache 明细才写 `prompt_tokens_details`；有 reasoning 才写 `completion_tokens_details`。
- **IR → Anthropic**（`encode_anthropic_usage`，:961-973`）：只写 `input_tokens`/`output_tokens` + 两个 cache 字段。
- 缓存写别名兼容：`cache_write_tokens` 或 `cache_creation_tokens` 都认（:1073-1080）。

### 4.3 id/model 处理

- 缺省兜底：Anthropic 响应 id 缺省 `"msg_switchyard"`、OpenAI 缺省 `"chatcmpl_switchyard"`，model 缺省 `"unknown"`。
- 流式 id 改写（`openai_chat/stream.rs:381-392`）：OpenAI chunk id 若上游是 `msg_xxx`/`resp_xxx` 则剥前缀改 `chatcmpl_xxx`；Anthropic 侧反向补 `msg_` 前缀（`anthropic/stream.rs:559-568`）。让客户端看到的 id"长得像"目标格式的 id。
- `served_model` 覆盖（`helpers.rs:54-67` + `stamp_streamed_response_model`，:138-164）：把"实际服务的模型名"盖到响应 model 字段上（路由层选了别的模型时客户端能看到真实来源），流式按格式精确插到 `message.model`/`response.model`/chunk 顶层 `model`。

---

## 5. 流式转换（SSE）

### 5.1 SSE 帧层

`sse.rs` 的 `parse_json_sse_frame` 只认 `data:` 行（忽略 `event:`、注释 `:...`），多 data 行拼接，`[DONE]` 作为可选终止标记（三种格式都接受）。

`helpers.rs:174-247` `decode_stream`：`BufReader::lines()` 重组跨网络 chunk 的行（含多字节 UTF-8），空行切帧；**上游漏发末尾空行时，流尾残留的非空 frame 也会被解析一次，不丢最后一个 chunk**。每个事件都是 `LlmResponseStreamEvent::preserved(source, raw, normalized)`——原始 JSON 全程随行。

### 5.2 状态机：StreamTranslationState（`codecs/stream.rs`）

一条流一个 state，关键字段：
- 身份：`model/message_id`（源端观察到的）+ `target_model/target_message_id`（路由层覆盖）。
- 生命周期：`saw_message_start / emitted_message_start / finished / errored`。
- 内容块游标：`next_content_index`、`text_block_index/started`、`reasoning_block_index/started`、`emitted_content_block`、`emitted_message_delta`。
- 工具：`tool_states: BTreeMap<usize, StreamToolState{id, name, arguments, pending_arguments, started, content_index, ...}>`，**按 tool_call index 分桶**。
- 用量：`usage` + `saw_backend_usage` + `output_tokens_seen`（后端没报 usage 时用 TextDelta 计数兜底）。

### 5.3 Anthropic 事件流 → IR（`anthropic/stream.rs:68-143`）

- `message_start`：提取 id/model/初始 usage → `MessageStart`。
- `content_block_start`：`text`→`TextDelta`（仅当自带非空 text）、`thinking`→`ReasoningDelta`、`tool_use`→`ToolCallDelta{index, id, name, arguments_delta: input 序列化}`。
- `content_block_delta`：`text_delta`→`TextDelta`，`thinking_delta`→`ReasoningDelta`，**`signature_delta` 丢弃**，`input_json_delta`→`ToolCallDelta{id:None, name:None, arguments_delta: partial_json}`。
- `message_delta`：usage → `Usage` chunk；`delta.stop_reason` **记入 state 并立刻发 `MessageStop{reason}`**。
- `message_stop`：发 `MessageStop{reason: state.stop_reason}`——**重放 message_delta 里记住的 reason**。原因：Anthropic 的 `message_stop` 自身不带 reason，若发无 reason 的 MessageStop，下游 accumulator 会把真正的 `max_tokens` 覆盖成 `EndTurn`（测试 `anthropic_message_stop_does_not_overwrite_max_tokens_stop_reason`）。
- `error` → `StreamError`；未识别事件类型 → 空 vec（静默跳过）。

### 5.4 IR → Anthropic 事件流（`encode_anthropic_stream`，:146-217）

- `MessageStart`：合成完整 `message_start`（含空 content、null stop_reason、0 用量 usage 骨架）；重复 MessageStart 只发一次。
- `TextDelta`：`ensure_anthropic_text_block` 先按需发 `content_block_start{type:text}`（并在发 text 前关闭打开的 thinking 块），再发 `content_block_delta{text_delta}`。content index 由 `next_content_index` 统一分配。
- `ReasoningDelta`：对称地开 `thinking` 块；`close_anthropic_reasoning_block` 在关闭 thinking 块前补一个空 `signature_delta`（满足 Anthropic thinking 块必须有 signature 的契约）。
- `ToolCallDelta`（`encode_anthropic_tool_delta`，:436-511）：按 index 取 `StreamToolState`，累积 id/name/arguments；**name 到齐才发 `content_block_start{type:tool_use, input:{}}`**，积压的 `pending_arguments` 立刻作为第一个 `input_json_delta` 补发；id 缺省合成 `toolu_{index}`。
- `Usage` chunk 只记账不转发；`MessageStop` 只记 reason 不转发——**终止事件统一由 `finish` 发**。
- `finish_anthropic_stream`（:220-276）EOF 时补齐：缺 message_start 补发、关闭所有未关的 text/thinking/tool 块、**一个内容块都没发过则补一对空 text 块**（空响应也是合法 Anthropic 流）、补 `message_delta`（带 stop_reason 映射 + usage；后端没报 usage 时 output_tokens 用 TextDelta 计数兜底）、最后 `message_stop`。
- 错误终态：收到 `StreamError/DecodeError` 发 `{"type":"error"}` 后置 `errored=true`，之后所有 chunk 被入口守卫丢弃。

### 5.5 OpenAI chunk ↔ IR（`openai_chat/stream.rs`）

**decode**（:46-149）：OpenAI 没有显式 start 事件，**第一个 chunk 隐含 MessageStart**（提取 id/model）。`delta.content`→TextDelta；`delta.reasoning_content`/`reasoning`→ReasoningDelta；`delta.tool_calls[]`→ToolCallDelta（index/id/name/arguments 直取）；`finish_reason`→MessageStop；顶层 `usage`（stream_options.include_usage）→Usage；**顶层 `error` 帧（无 choices）→ StreamError**。

**encode**（:152-237）：
- `MessageStart`：仅当源格式是 Anthropic/Responses 时才合成首个 role chunk `{"delta":{"role":"assistant"}}`（OpenAI→OpenAI 回放不重复发）。
- `TextDelta`→`{"delta":{"content":...}}`；`ReasoningDelta`→`{"delta":{"reasoning_content":...}}`。
- `ToolCallDelta`→`{"delta":{"tool_calls":[{"index","type":"function","function":{...}}]}}`，id/name/arguments 有哪个写哪个——**OpenAI 的增量模型与 IR 的 ToolCallDelta 几乎一一对应，OpenAI 侧无累积逻辑**。
- `MessageStop`：发终结 chunk（`finish_reason` 映射 + 按需附 usage），置 `finished`。
- `finish_openai_chat_stream`（:240-251）：源流干净 EOF 但没收到 stop 时合成终结 chunk。`openai_chat_emits_usage_arriving_after_stop` 锁定"usage 在 stop 之后才到"时补发 `choices:[]` 的 usage-only chunk（:316-320）。

### 5.6 跨 chunk 部分 JSON 的处理原则

**tool arguments 在流式路径全程按不透明字符串传递，绝不中途解析**：
- Anthropic `input_json_delta.partial_json`（字符串）→ IR `arguments_delta`（字符串）→ OpenAI `function.arguments`（字符串），反向同理，原样字符串拼接/转发（`StreamToolState.arguments.push_str(&delta)`）。
- 唯一解析点是 `ResponseAccumulator` 折叠成 buffered 响应时（`parse_tool_arguments`），解析失败降级为字符串、空串降级为 `{}`，永不报错。
- Anthropic `content_block_start` 里完整给出的 `input` object 通过 `tool_input_delta`（:583-589）序列化成字符串进入 delta 流。

**结论：增量场景下"字符串直通 + 末端一次性解析"是最稳的选择。**

---

## 6. 错误处理与降级策略

### 6.1 错误类型（`error.rs`）

`TranslationError`：`InvalidJson / InvalidType{path, expected} / UnsupportedTranslation{from,to} / LossyConversion(msg) / UnknownField{path} / InvalidValue{path, message} / Other`。所有错误都带 JSON-path 风格的 `path`（如 `$.messages[3].role`），刻意让代理的错误读起来像 provider 自己的校验错误。

### 6.2 分层降级哲学

1. **硬错误**（直接 `Err`，整个转换失败）：顶层非 object、未知 role、policy=Reject 时的有损转换/未知字段。
2. **有损放行 + 诊断**（默认）：`push_lossy`（`util.rs:83-96`）按 policy 决定"记一条 `lossy_conversion` warning 继续"还是"返回 `LossyConversion` 错误"。触发点：消息/内容块不是 object、Anthropic 不支持的 JSON Schema 约束被剥离、audio/video 降级为文本、Unknown 块编码为文本等。
3. **静默透传**：`extensions.fields` 收未知字段；`ContentBlock::Unknown{raw}` / `ToolChoice::Raw` / `ImageSource::Raw` 保留原文，同格式编码时原样输出。
4. **无损回放**（preservation）：`capture_request_preservation` 把原始 body 按格式存进 IR；`exact_preserved_request`（`util.rs:251-260`）发现目标格式 == 源格式时**直接返回原始 body，跳过整个 encode**——同格式代理转发字节级无损。`Embed` 模式进一步支持 A→B→A 多跳后仍还原（`tests/lossless_roundtrip.rs` 有全格式对 + 三格式循环的精确往返测试）。

### 6.3 未知/不支持字段总表

| 场景                           | 策略                                                                                                         |
| ------------------------------ | ------------------------------------------------------------------------------------------------------------ |
| 请求顶层未知字段               | 收进 `extensions.fields`，目标格式白名单匹配则带回（如 OpenAI 的 `user`/`service_tier`），否则留在 IR        |
| 未知 content block             | `ContentBlock::Unknown{provider, raw}`；跨格式编码时 JSON 字符串化为文本 + lossy 告警                        |
| 未知 tool_choice 形状          | `ToolChoice::Raw` 原样透传                                                                                   |
| 未知 role（请求）              | 硬错误（模仿 provider invalid_value）                                                                        |
| 未知 role（响应）              | coerce 为 user（宽松）                                                                                       |
| 流式未知事件类型               | 跳过（空 vec）                                                                                               |
| 流中错误帧                     | 转 `StreamError` chunk，目标侧发格式对应的 error 事件后终流                                                  |
| 缺失 tool_call id              | `GenerateStable` 策略生成 `sw_00000001` 稳定 id                                                              |
| Anthropic tool_use id 非法字符 | `sanitize_anthropic_tool_use_id` 替换为 `_`；冲突时加 FNV-1a 哈希后缀（`mapped_tool_id`，`util.rs:416-443`） |

---

## 7. 对 LsmTokensServer 可借鉴的工程实践

1. **中性 IR + 双向 codec，而非点对点转换器**。即便只支持两种格式，IR 也强制把"两家各自能表达什么"显式建模（`ContentBlock::Unknown`、`ToolChoice::Raw` 等 Raw 变体），消灭隐式丢字段。加第三种格式（如 Responses API）时只写 codec 不动转换逻辑。
2. **diagnostics 作为一等输出**。每次转换返回 `Vec<TranslationDiagnostic>`（含 code/path/source/target），降级不再静默——与 LsmTokensServer"禁止把故障静默降级成正常状态"的强制规则同构：lossy 事件应进日志/浏览记录，而不是默默发生。
3. **policy 旋钮集中化**：`LossyConversionPolicy::{AllowWithDiagnostics, Reject}` 一个开关统一"尽量转"与"严格拒绝"两种运维模式；`TargetCapabilities` 让"目标模型不支持图片/工具"成为可配置的 fail-fast 校验。
4. **preservation 模式实现同格式字节级无损**：源 == 目标时直接回放原始 body（含流式逐事件回放）。注意 `observe_replayed_event` 教训：回放跳过 encoder 也必须喂状态机，否则 EOF 时 `finish` 会重复/缺失终止事件。
5. **流式状态机显式字段化**：`StreamTranslationState` 把"text 块开着吗、thinking 块开着吗、tool index→content index 映射、stop_reason 记忆、errored 守卫"全部显式字段化并 `Serialize`——可单测、可快照。关键不变量：
   - 终止事件统一由 `finish()` 发，中途的 MessageStop 只记账（避免双发）；
   - Anthropic `message_stop` 不带 reason，必须在 `message_delta` 时记住并回放；
   - 空响应也要补一对空 text content_block，否则不是合法 Anthropic 流；
   - 后端没报 usage 时用 TextDelta 计数兜底 output_tokens。
6. **tool arguments 流式"字符串直通"**：增量路径绝不 parse JSON，末端 accumulator 一次性解析且失败有降级——跨 chunk 部分 JSON 问题的最简正解。
7. **请求严格 / 响应宽松的不对称校验**：请求侧未知 role 直接报错（错误文案模仿 provider），响应侧 coerce 放行。
8. **id 卫生**：`sanitize_anthropic_tool_use_id`（字符白名单 + FNV-1a 冲突后缀）、流式 id 前缀改写（`msg_`↔`chatcmpl_`）；缺失 id 用确定性计数器 id 而非随机 UUID，测试可重现。
9. **测试组织**（`crates/switchyard-translation/tests/`，约 4300 行）：
   - `request_translation.rs`（1590 行）按"方向 + 特性"组织，测试名即契约；
   - `lossless_roundtrip.rs` 用 fixture 做全格式对 + 三格式循环的**精确相等**断言；
   - `extension_points.rs` 验证注册表开放性；
   - 大量"防泄漏"反向断言：Anthropic thinking 块不得漏进 OpenAI 消息文本、OpenAI reasoning_content 不得伪造 Anthropic thinking 块、unknown 块不得跨格式漏出。
10. **边界情况清单**（可直接抄为 checklist）：空 content/null content、空 text 占位清理、assistant 带 tool_calls 时 content 置 null、连续 tool 结果合并（→Anthropic）与拆分（→OpenAI）、tool 无 name 丢弃、`stop` 字符串 vs 数组、`max_tokens` 缺省 64000、cache 写字段双别名、usage 在 stop 之后才到达、错误帧后丢弃后续 chunk、同格式回放后 `finish` 不重复发终止事件。

---

## 附：关键文件索引

| 主题                 | 路径                                                                                                                                       |
| -------------------- | ------------------------------------------------------------------------------------------------------------------------------------------ |
| IR 类型              | `crates/protocol/src/llm.rs`、`crates/protocol/src/stream.rs`、`crates/protocol/src/format.rs`                                             |
| 引擎与注册表         | `crates/switchyard-translation/src/engine.rs`、`src/codecs/stream.rs`                                                                      |
| 策略                 | `crates/switchyard-translation/src/policy.rs`                                                                                              |
| OpenAI Chat buffered | `crates/switchyard-translation/src/codecs/openai_chat/buffered.rs`                                                                         |
| OpenAI Chat stream   | `crates/switchyard-translation/src/codecs/openai_chat/stream.rs`                                                                           |
| Anthropic buffered   | `crates/switchyard-translation/src/codecs/anthropic/buffered.rs`                                                                           |
| Anthropic stream     | `crates/switchyard-translation/src/codecs/anthropic/stream.rs`                                                                             |
| 共享工具             | `crates/switchyard-translation/src/util.rs`（preservation、id 清洗）、`src/codecs/common.rs`                                               |
| SSE/字节流           | `crates/switchyard-translation/src/sse.rs`、`src/helpers.rs`                                                                               |
| 测试                 | `crates/switchyard-translation/tests/{request_translation,response_translation,stream_translation,lossless_roundtrip,extension_points}.rs` |
| 架构文档             | `docs/architecture.md`（53-60 行：decode 到中立类型 → 路由 → encode 到目标格式）                                                           |
