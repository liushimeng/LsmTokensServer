# LsmTokensServer OpenAI ↔ Anthropic 协议转换优化方案

> 日期：2026-08-13（第 01 版）
> 输入：[`Switchyard_OpenAI_Anthropic_Exchange.md`](./Switchyard_OpenAI_Anthropic_Exchange.md)（NVIDIA Switchyard 翻译层调研）+ LsmTokensServer 协议转换模块现状审计
> 目标版本：v2.0.72
> 范围：协议转换模块（`protocol_types.go` / `protocol_openai_to_anthropic.go` / `protocol_anthropic_to_openai.go` / `protocol_sse.go` / `protocol_analyzer.go`）+ 代理热路径转换调用点（`server_http_ai_proxy_utils.go`）

---

## 1. 现状审计结论（问题清单）

现状审计发现 27 项问题，其中 7 项 P0 功能性缺陷可直接在线上复现。完整清单见本文件 §6；核心问题：

| # | 严重度 | 问题 | 位置 |
|---|--------|------|------|
| P0-1 | 致命 | `AnthropicContentBlock` 把 `tool_use`/`tool_result` 建模为**嵌套字段**（`{"type":"tool_use","tool_use":{...}}`），真实线格式是**平铺**（`{"type":"tool_use","id":...,"name":...,"input":{...}}`）。后果：a2o 非流式响应的 tool_calls 整体丢失但 `finish_reason=tool_calls` 照发 → 客户端 Agent 工具循环必断；o2a 响应序列化产出非法嵌套结构 | `protocol_types.go:132-133` |
| P0-2 | 致命 | 代理热路径对上游 SSE **整包缓冲**（`io.ReadAll`）再聚合转换，流式体验完全丧失 | `server_http_ai_proxy_utils.go:575` |
| P0-3 | 致命 | 上游 SSE → 客户端 stream 场景输出自相矛盾：聚合后返回单个 JSON blob，却保留 `Content-Type: text/event-stream`，客户端 SSE 解析器必炸 | `server_http_ai_proxy_utils.go:579-614` |
| P0-4 | 严重 | a2o 请求中 `tool_result` 块被降级为拼进 user 消息的纯文本，不产生 `role=tool` 消息，tool_call_id 关联丢失 | `protocol_anthropic_to_openai.go:139-148` |
| P0-5 | 严重 | o2a 中 `image_url` 转成无 `source` 的空 image 块（Anthropic 必 400）；a2o 方向 image 块直接消失 | `protocol_openai_to_anthropic.go:160-162` |
| P0-6 | 严重 | `parseSSEEvents` 不支持 CRLF：`\r\n` 流中空白行是 `"\r"`，事件永不 flush，data 粘连全流解析失败 | `server_http_ai_proxy_utils.go:193-194` |
| P0-7 | 严重 | `wrapAnthropicResponseAsSSE` 对 tool_use 块只发 `{"type":"tool_use"}`，id/name/input 全丢，且不发 `input_json_delta` | `server_http_ai_proxy_utils.go:703-706` |
| P1-8 | 高 | `max_tokens` 双向无默认值：o2a 在双字段缺失时产出 `max_tokens:0` 非法 Anthropic 请求（Anthropic 必填） | `protocol_openai_to_anthropic.go:27-31` |
| P1-9 | 高 | `temperature`/`top_p` 带 `omitempty`，显式 0 值被静默丢弃 | `protocol_types.go:15-16,111-112` |
| P1-10 | 高 | 连续多条 `role=tool` 消息 → 多条连续 user 消息，违反 Anthropic user/assistant 交替约束 | `protocol_openai_to_anthropic.go:60-63` |
| P1-11 | 中 | tool_call `Arguments` JSON 解析失败被 `_ =` 吞掉置 nil；tool_result `IsError` 恒 false | `protocol_openai_to_anthropic.go:119,198` |
| P1-12 | 中 | a2o 响应 `Created` 硬编码 0；id 前缀不转换（`msg_xxx` 出现在 OpenAI 响应） | `protocol_anthropic_to_openai.go:257` |
| P1-13 | 中 | finish/stop 映射缺 `content_filter`/`refusal`/`pause_turn`/`model_context_window_exceeded`/`function_call` → 字段消失 | `protocol_anthropic_to_openai.go:296-306,385-392` |
| P1-14 | 中 | Anthropic system 数组形态（含 cache_control 块）被 JSON dump 成字符串 | `protocol_analyzer.go:487-500` |
| P1-15 | 中 | 未知 role 原样透传给 Anthropic → 上游 400 | `protocol_openai_to_anthropic.go:65-69` |
| P1-16 | 中 | OpenAI SSE 聚合用 delta 数组**位置下标**代替协议 `index` 字段，`choice.Index*1000+idx` 分桶 idx≥1000 串桶 | `protocol_sse.go:43-46` |
| P1-17 | 中 | cache 相关 token 统计字段（`cache_creation_input_tokens`/`cache_read_input_tokens`/`prompt_tokens_details.cached_tokens`）全丢 | 双向 usage 转换 |

P2 健壮性/工程问题（无效重试、metrics 高估、映射表无一致性校验等）见 §6，本版不全部处理。

---

## 2. Switchyard 可借鉴点 → 本方案的映射

Switchyard 是「中性 IR + 双向 codec」架构（详见知识库文档）。LsmTokensServer 当前是点对点转换器，**本版不做 IR 重构**（收益主要是扩展第三格式，当前无此需求，重构风险大），但采纳其经过验证的转换规则与工程实践：

| Switchyard 实践 | 本方案采纳点 |
|---|---|
| `tool_use.input` 必须是 object，解析失败包 `{"raw": text}` 而非丢弃（`anthropic_tool_input`） | 修复 P1-11：o2a 的 Arguments 解析失败包 `{"raw": ...}` |
| 连续 tool-result-only 消息**合并**为一条 user 消息（`encode_anthropic_messages`）；反向**拆分**为多条 `role=tool` 消息（`encode_message_with_tool_results_to_openai`） | 修复 P1-10（o2a 合并）+ P0-4（a2o 拆分） |
| Anthropic `max_tokens` 必填，缺失时强制默认 64000 | 修复 P1-8（缺省 8192，比 64000 保守，防止意外高额计费） |
| image source 三态 `Url/Base64/Raw`，base64 走 `data:` URI 解析 | 修复 P0-5：o2a 解析 `image_url`（url 直传 / data URI 拆 base64+media_type）；a2o 反向生成 `image_url` part |
| stop/finish 映射表六值枚举 + 未识别值透传 | 修复 P1-13：补齐 `content_filter↔content_filter`、`function_call→tool_use`、`refusal/pause_turn/model_context_window_exceeded→stop`（响应侧宽松），未识别 OpenAI finish_reason 原样透传 |
| 流式 id 前缀改写（`msg_`↔`chatcmpl_`）让 id"长得像"目标格式 | 修复 P1-12（id 改写 + `Created=time.Now().Unix()`） |
| 缺失 tool_call id 生成确定性 id（`sw_NNNNNNNN`）；Anthropic tool_use id 字符白名单清洗 | 采纳：o2a 缺失 id 生成 `toolu_` 前缀确定性 id；非法字符替换 `_` |
| usage 缓存明细字段双向映射（cache_read/cache_creation ↔ prompt_tokens_details.cached_tokens） | 修复 P1-17 |
| 请求严格/响应宽松的不对称校验 | 修复 P1-15：o2a 未知 role 归并为 user（代理场景放行优于 400，记 warning） |
| tool arguments 流式"字符串直通"，末端一次性解析且失败降级 | 已是现状（`partialJSON` 拼接 + `{"_raw_json": ...}` 兜底），保留；修复 P1-16 的 index 分桶 |
| SSE 帧解析容忍 CRLF + 流尾残留帧不丢 | 修复 P0-6 |
| 空响应也要补空 text content_block 才是合法 Anthropic 流 | 采纳进 P0-3/P0-7 的包装路径 |

**不采纳（本版）**：完整 IR 重构、流式逐事件状态机（真 streaming 转换）、preservation 无损回放、policy 旋钮体系。其中**真流式转换**（消除 P0-2 的整包缓冲）单列为 §5 二期方案——工作量大、风险高，本版先把"聚合→转换→重包装"链路修正确（P0-3），保证任何路径输出协议自洽。

---

## 3. 本期实施范围（v2.0.72）

### 3.1 数据模型修复（`protocol_types.go`）

**修复 P0-1：AnthropicContentBlock 改为平铺线格式**

```go
type AnthropicContentBlock struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	// tool_use 平铺字段
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     map[string]interface{} `json:"input,omitempty"`
	// tool_result 平铺字段
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
	IsError   bool        `json:"is_error,omitempty"`
	// image 块
	Source    map[string]interface{} `json:"source,omitempty"`
	// thinking 块
	Thinking  string      `json:"thinking,omitempty"`
	Signature string      `json:"signature,omitempty"`
}
```

- 删除 `ToolUse *AnthropicToolUse` / `ToolResult *AnthropicToolResult` 嵌套字段；`AnthropicToolUse`/`AnthropicToolResult` 类型如无人引用则一并删除。
- 同步改造全部使用点：`protocol_openai_to_anthropic.go`（构造 tool_use/tool_result 块）、`protocol_anthropic_to_openai.go`（读取）、`protocol_sse.go`（`anthropicContentBlockFromMap` 本就按平铺读取 map，改为直接填平铺字段）、`server_http_ai_proxy_utils.go` 的 `wrapAnthropicResponseAsSSE`。
- `AnthropicToolUse.Input` 原为 `map[string]interface{}`；平铺后 `Input` 保持 map（ Anthropic 契约要求 object）。
- **测试锁定**：新增"真实线格式 JSON → unmarshal → a2o 响应转换 → tool_calls 不丢"的反向断言测试（现有测试全部用 Go struct 构造输入，恰好绕过此缺陷——这是缺陷潜伏至今的原因）。

**修复 P1-9：temperature/top_p 改指针**

`OpenAIChatCompletionRequest.Temperature/TopP`、`AnthropicMessagesRequest.Temperature/TopP` 改为 `*float64`（保留 `omitempty`），显式 0 值可透传。同步更新所有构造/读取点与测试。

### 3.2 o2a 请求转换（`protocol_openai_to_anthropic.go`）

1. **P1-8**：`max_completion_tokens` → `max_tokens` → 默认 `8192` 三级回退。
2. **P1-10**：`ConvertOpenAIToAnthropicRequest` 主循环增加合并 pass——连续 `role=tool` 消息产生的 tool_result 块合并进**同一条** user 消息（`anthropic_user_message{content: [tool_result, tool_result, ...]}`）。
3. **P0-5**：`image_url` 块转换：
   - `image_url` 为 `{"url": "https://..."}` → `{"type":"image","source":{"type":"url","url":...}}`
   - `image_url` 为 `{"url": "data:image/png;base64,...."}` → 解析 data URI → `{"type":"image","source":{"type":"base64","media_type":"image/png","data":...}}`
   - `image_url` 为裸字符串 → 按 url 处理
   - 解析失败 → 降级为文本块 `[image: <url 截断>]` 而非非法空块
4. **P1-11**：tool_call `Arguments` 解析失败包 `{"raw": 原文}`；`Arguments` 为空串 → `Input = {}`。
5. **P1-15**：未知 role 归并为 user（保留原 content），不原样透传。
6. assistant 消息 content 为数组 + 带 tool_calls 时，正确提取 text 块（不再整体 JSON dump）。
7. 缺失 tool_call id → 生成 `toolu_o2a_%08d` 确定性 id（按消息内序号）；id 非法字符（非 `[a-zA-Z0-9_-]`）替换为 `_`。

### 3.3 a2o 请求转换（`protocol_anthropic_to_openai.go`）

1. **P0-4**：消息拆分 pass——一条 Anthropic 消息中混有 tool_result 块时：
   - 每个 tool_result 块 → 独立 `{"role":"tool","tool_call_id":...,"content":...}` 消息（`is_error=true` 时 content 前缀 `[ERROR] ` 保留语义）；
   - 其余内容按原 role 另发一条消息；
   - tool_result 的 content 为数组时拍平为文本（text 块提取、非 text 块 JSON 字符串化）。
2. **P0-5 反向**：image 块 → `{"type":"image_url","image_url":{"url":...}}`（url source 直传；base64 source 拼 `data:` URI）。
3. **P1-14**：system 为数组形态时拼接各 text 块的 text（`"\n\n"` 连接），不再 JSON dump；非 text 块跳过。
4. 平铺格式读取 tool_use（配合 §3.1）：`block.ID/Name/Input` → `tool_calls`。
5. `metadata.user_id` → OpenAI `user` 字段（映射表已声称实现，补齐代码，消除文档与实现漂移）。

### 3.4 响应转换（`protocol_anthropic_to_openai.go`）

1. **P0-1 响应侧**：`ConvertAnthropicToOpenAIResponse` 读平铺 tool_use 块 → `tool_calls`；`ConvertOpenAIToAnthropicResponse` 产出平铺 tool_use 块。
2. **P1-13**：finish/stop 映射补齐：
   - a2o：`end_turn→stop`、`max_tokens→length`、`tool_use→tool_calls`、`stop_sequence→stop`、`refusal→content_filter`、`pause_turn→stop`、`model_context_window_exceeded→length`、未识别→原样透传；
   - o2a：`stop→end_turn`、`length→max_tokens`、`tool_calls→tool_use`、`function_call→tool_use`、`content_filter→refusal`、未识别→原样透传。
3. **P1-12**：a2o 响应 `Created = time.Now().Unix()`；id 前缀改写 `msg_xxx→chatcmpl_xxx`（o2a 反向 `chatcmpl_xxx→msg_xxx`）。
4. **P1-17**：usage 增加 cache 字段：
   - `AnthropicUsage` 增加 `CacheCreationInputTokens` / `CacheReadInputTokens`；
   - `OpenAIUsage` 增加 `PromptTokensDetails{CachedTokens}` / `CompletionTokensDetails{ReasoningTokens}`；
   - a2o：`prompt_tokens = input + cache_read + cache_creation`，有 cache 才写 details；o2a：`cached_tokens>0` 时写 `cache_read_input_tokens`。

### 3.5 SSE 与代理热路径（`protocol_sse.go` + `server_http_ai_proxy_utils.go`）

1. **P0-6**：`parseSSEEvents` 每行 `strings.TrimRight(line, "\r")`；函数返回前 flush 流尾残留的未完成事件（上游漏发末尾空行不丢最后一个事件）。
2. **P0-3**：`convertProxyResponse` 的 SSE 分支——`ConvertProtocolResponseSSE` 返回的聚合转换结果，经 `wrapConvertedResponseAsSSE` 按**目标协议**重新包装成合法 SSE 事件流再写出（o2a 发 Anthropic 事件序列，a2o 发 OpenAI chunk 序列 + `[DONE]`）。Content-Type 与body 始终自洽。
3. **P0-7**：`wrapAnthropicResponseAsSSE` 的 tool_use 块：
   - `content_block_start` 携带完整 `{"type":"tool_use","id":...,"name":...,"input":{}}`；
   - 随后发 `content_block_delta{"type":"input_json_delta","partial_json":<input 序列化>}`；
   - 空 content 响应补一对空 text 块（合法 Anthropic 流）。
4. **P1-16**：`OpenAIToolCall` 增加 `Index int \`json:"index,omitempty"\``；OpenAI SSE 聚合按协议 `index` 分桶（缺省回退数组位置下标），分桶 key 改 `(choiceIndex, toolCallIndex)` 二元组 map，废除 `*1000+idx`。
5. `AggregateAnthropicSSEToResponse` 的 `input_json_delta` 在块不是 tool_use 时，若尚无块则按 tool_use 建块（而非丢弃）——容错乱序事件。

### 3.6 明确不做（本版边界）

- ⛔ 不引入 IR 中间层重构（点对点转换器保留）。
- ⛔ 不实现真流式逐事件转换（见 §5 二期）。本版 P0-2 的整包缓冲保留，仅保证输出协议自洽（P0-3 修复后客户端能正确解析，延迟问题仍存）。
- ⛔ 不改 `ProtocolConvertAnalyzer` 页面与 metrics 权重（P2-22/23/27 另立版本）。
- ⛔ 不改请求转换失败的重试策略（P2-20，需路由层设计讨论）。

---

## 4. 测试计划

新增 `v2072_protocol_converter_fix_test.go`，每个 P0/P1 修复至少一个子测试，**全部用真实线格式 JSON 字符串作为输入**（禁止 Go struct 构造输入绕过 unmarshal 路径）：

| 测试 | 锁定 |
|---|---|
| `TestV2072_AnthropicWireFormat_ToolUseFlatUnmarshal` | 平铺 `{"type":"tool_use","id","name","input"}` JSON → struct 字段齐全（P0-1 反向断言） |
| `TestV2072_A2OResponse_ToolCallsSurvive` | 真实 Anthropic 响应 JSON → OpenAI 响应含 `tool_calls`，`finish_reason=tool_calls` 与 tool_calls 同时在场 |
| `TestV2072_O2AResponse_ToolUseFlatSerialize` | o2a 响应输出平铺 tool_use，无嵌套 `tool_use` key |
| `TestV2072_A2ORequest_ToolResultSplitsToToolMessages` | 混合内容 Anthropic 消息 → N 条 `role=tool` 消息 + tool_call_id 保留（P0-4） |
| `TestV2072_O2ARequest_ImageURLToSource` | http url / data URI / 裸字符串三形态 → 合法 image source（P0-5） |
| `TestV2072_A2ORequest_ImageBlockToImageURL` | base64 source → `data:` URI（P0-5 反向） |
| `TestV2072_ParseSSEEvents_CRLF` | `\r\n` 流事件正确切分 + 流尾无空行残留事件不丢（P0-6） |
| `TestV2072_WrapAnthropicResponseAsSSE_ToolUse` | 伪流式包装含 id/name/input_json_delta（P0-7） |
| `TestV2072_ConvertProxyResponse_StreamToStream_SelfConsistent` | 上游 SSE → 客户端收到合法目标协议 SSE（P0-3） |
| `TestV2072_O2ARequest_DefaultMaxTokens` | 双字段缺失 → `max_tokens=8192`（P1-8） |
| `TestV2072_TemperatureZero_Preserved` | 显式 `temperature:0` 转换后仍为 0（P1-9） |
| `TestV2072_O2ARequest_ConsecutiveToolMessagesMerge` | 3 条连续 tool 消息 → 1 条 user 消息 3 个 tool_result 块（P1-10） |
| `TestV2072_O2ARequest_BadArgumentsWrappedRaw` | 非法 JSON arguments → `{"raw": ...}`（P1-11） |
| `TestV2072_A2OResponse_CreatedAndIDPrefix` | Created>0 且 id 前缀 `chatcmpl_`（P1-12） |
| `TestV2072_StopFinishReason_NewMappings` | content_filter/refusal/pause_turn/function_call/未识别透传（P1-13） |
| `TestV2072_A2ORequest_SystemArrayForm` | 数组 system → 拼接文本（P1-14） |
| `TestV2072_O2ARequest_UnknownRoleCoercesToUser` | `"role":"api"` → user（P1-15） |
| `TestV2072_OpenAISSE_ToolCallIndexField` | 稀疏/乱序 index 正确分桶（P1-16） |
| `TestV2072_Usage_CacheTokensBothDirections` | cache 字段双向映射 + prompt_tokens 加回（P1-17） |

回归：既有 `test_protocol_converter_*_test.go` 四个文件全部保持通过（其中用 Go struct 构造 tool_use 的测试需适配新平铺字段）。

---

## 5. 二期方案（不在本期实施）：真流式逐事件转换

**目标**：消除 P0-2 整包缓冲，客户端首字节延迟从"全量生成时间"降为"上游首个事件时间"。

**设计要点**（直接借鉴 Switchyard `StreamTranslationState` 状态机，见知识库文档 §5）：

1. 新增 `protocol_stream_state.go`：`StreamConvertState` 显式字段化（message_start 已发/已见、text 块开关、tool index→content index 映射、stop_reason 记忆、errored 守卫、usage 记账）。
2. `convertProxyResponse` 的 SSE 分支改为**逐事件管道**：`parseSSEEvents` 边读边吐 → 每事件 decode 到中性 chunk（TextDelta/ToolCallDelta/Usage/MessageStop 等 7 种）→ 按目标协议 encode 写出 + `Flusher.Flush()`。
3. 关键不变量（Switchyard 已验证）：
   - 终止事件统一由流结束时的 `finish()` 补发，中途 MessageStop 只记账（防双发）；
   - Anthropic `message_stop` 不带 reason，必须在 `message_delta` 时记住回放；
   - 空响应补空 text content_block；
   - 后端没报 usage 时用 TextDelta 计数兜底 output_tokens；
   - tool arguments 全程字符串直通，绝不中途 parse。
4. 前置依赖：本期 P0-3/P0-6/P0-7 修复（SSE 解析健壮性 + 包装正确性）是二期的地基。

**工作量预估**：状态机 + 双向 encode/decode + 测试约 800-1000 行，建议独立版本（v2.0.73+）实施。

---

## 6. 附录：完整问题清单（含本期不处理的 P2）

| # | 问题 | 本期处理 |
|---|------|---------|
| P2-18 | `input_json_delta` 在块非 tool_use 时静默丢弃；`signature_delta`/citations 未处理 | 部分（§3.5-5 容错） |
| P2-19 | assistant content 数组+tool_calls 时整体 JSON dump | ✅ §3.2-6 |
| P2-20 | 请求转换失败按源站失败重试（确定性错误无效重试） | 否 |
| P2-21 | 响应转换失败 502 无回退（如透传原始响应 + warning header） | 否 |
| P2-22 | `SemanticMappingRate` 按顶级字段记账，嵌套丢弃不计，系统性高估 | 否 |
| P2-23 | `ProtocolConvertAnalyzerEnabled` 裸 bool 无原子保护 | 否 |
| P2-24 | header 白名单双实现漂移；a2o ratelimit 头规则自相矛盾 | 否 |
| P2-25 | o2a 后 Authorization 仍是 Bearer，未设 `x-api-key` | 否（依赖目标端兼容，改动有认证风险） |
| P2-26 | `AnthropicSSEEvent` 死代码 | ✅ 顺手删除 |
| P2-27 | `BuildProtocolConvertAnalyzerMapping` 与代码实现无一致性校验 | 部分（metadata.user_id↔user 补齐实现，消除一处漂移） |

---

## 7. 风险与回滚

- **最大风险点**：`AnthropicContentBlock` 平铺改造波及 SSE 聚合、伪流式包装、分析器回放三条路径。缓解：§4 测试全部走真实 JSON 线格式，改造后 `go test ./...` 全量回归。
- **兼容性**：平铺改造后，MySQL 交易分表中的历史 base64 body 回放路径（学习功能）读取的是**原始抓包 JSON**（本来就是平铺格式），改造反而修复了回放路径的隐性错误——无历史数据迁移。
- **回滚**：单版本独立提交，`git revert` 即可；不涉及数据库 schema 变更。
