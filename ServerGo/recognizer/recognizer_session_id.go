package recognizer

import (
	"encoding/json"
	"github.com/lishimeng/LsmTokensServer/protocol"
	"net/http"
	"strings"
)

// ============================================================================
// Session 识别 —— 协议无关抽象层
// ============================================================================
//
// 设计目标：将 session_id 识别从经济型算法中解耦，供所有算法策略复用。
// 当前 OpenAI 和 Anthropic 协议的 session_id 解析逻辑相同（均从
// metadata.user_id 内嵌 JSON 中提取），但分别实现以预留未来扩展空间。
//
// 使用方式：
//   sessionID := RecognizeSessionID(bodyBytes, protocolType, headers)
//
// 扩展指南：
//   - 若某协议新增独立的 session 字段（如 anthropic-beta: session-id 头），
//     在对应协议的 recognizer 中实现，不影响其他协议。
//   - 若新增协议（如 Gemini），新增 recognizer 文件并注册到 sessionRecognizers。
//
// ============================================================================

// SessionRecognizer 协议级 session 识别接口
type SessionRecognizer interface {
	// Recognize 从请求 body 和 headers 中解析 session_id；返回空字符串表示未识别。
	// headers 参数预留用于未来按 Agent 工具类型从不同来源识别 session（如自定义请求头）。
	Recognize(body []byte, headers http.Header) string
	// ProtocolType 返回该识别器对应的协议类型（protocol.AgentProtocolType_*）
	ProtocolType() int
}

// sessionRecognizers 按协议类型索引的识别器注册表
var sessionRecognizers = map[int]SessionRecognizer{
	protocol.AgentProtocolType_Anthropic: &anthropicSessionRecognizer{},
	protocol.AgentProtocolType_OpenAI:    &openAISessionRecognizer{},
}

// RecognizeSessionID 按协议类型从请求 body 和 headers 中识别 session_id。
// 未注册协议或识别失败时返回空字符串。
// headers 参数预留用于未来按 Agent 工具类型从不同来源识别 session（如自定义请求头）。
func RecognizeSessionID(body []byte, protocolType int, headers http.Header) string {
	if recognizer, ok := sessionRecognizers[protocolType]; ok {
		return recognizer.Recognize(body, headers)
	}
	return ""
}

// SessionRecognitionResult Session 识别结果（v2.0.76 阶段BD：区分原生识别与最终生效值）。
//
//   - AgentToolSessionID：Agent 工具原生识别出的 session ID（来自请求头或请求体）；
//     未识别时为空字符串（不用 unknown 占位，便于统计 Agent 的 session 透传率）。
//   - EffectiveSessionID：最终生效的 session ID；识别成功时与 AgentToolSessionID 同值，
//     未识别时为空字符串，由调用方决定合成兜底（self_generate_*）或 unknown 占位。
type SessionRecognitionResult struct {
	AgentToolSessionID string
	EffectiveSessionID string
}

// RecognizeSessionIDWithSource 按协议类型识别 session_id，并区分原生识别结果与生效值
// （v2.0.76 阶段BD）。
//
// 语义约定：所有现有识别路径（协议级头 + body + Agent 工具级识别器）命中即视为
// 「Agent 工具原生识别」，两字段同值；未识别时 AgentToolSessionID 保持空字符串。
// 原 RecognizeSessionID() 行为完全兼容（等价于仅取 EffectiveSessionID）。
func RecognizeSessionIDWithSource(body []byte, protocolType int, headers http.Header) SessionRecognitionResult {
	sid := RecognizeSessionID(body, protocolType, headers)
	return SessionRecognitionResult{
		AgentToolSessionID: sid,
		EffectiveSessionID: sid,
	}
}

// RegisterSessionRecognizer 注册自定义 session 识别器（用于测试或扩展）
func RegisterSessionRecognizer(protocolType int, recognizer SessionRecognizer) {
	sessionRecognizers[protocolType] = recognizer
}

// parseSessionIDFromMetadataUserID 共用底层实现：
// 从 metadata.user_id 内嵌 JSON 字符串中提取 session_id。
// 两层 Unmarshal 避免全量解析大 body，性能优先。
// headers 参数预留用于未来按 Agent 工具类型从不同来源识别 session（如自定义请求头）。
//
// 解析失败时回退到启发式扫描：直接从 metadata.user_id 字符串中按常见 key 名
// (session_id / sessionId / conversation_id / thread_id) 截取值；这是为了应对
// claude-cli 等客户端偶尔发送非标准 JSON 转义（如末尾多余逗号、未转义引号）导致
// 严格 Unmarshal 失败、但字符串中明显包含 session_id 的真实场景。
func parseSessionIDFromMetadataUserID(body []byte, headers http.Header) string {
	// headers 参数当前未直接读取，保留以便未来按 UA / 自定义头切换内嵌 key 集合
	_ = headers
	var outer struct {
		Metadata struct {
			UserID string `json:"user_id"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(body, &outer); err != nil || outer.Metadata.UserID == "" {
		return ""
	}
	var inner struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal([]byte(outer.Metadata.UserID), &inner); err == nil && inner.SessionID != "" {
		return inner.SessionID
	}
	// 启发式 fallback：从内嵌 user_id 字符串中按 key 名扫描
	if sid := extractSessionIDHeuristic(outer.Metadata.UserID); sid != "" {
		return sid
	}
	return ""
}

// sessionIDHeuristicKeys 内嵌 user_id 字符串中常见的 session 标识 key 名。
// 优先级：session_id > sessionId > conversation_id > thread_id。
var sessionIDHeuristicKeys = []string{
	`"session_id":"`,
	`"sessionId":"`,
	`"conversation_id":"`,
	`"thread_id":"`,
}

// extractSessionIDHeuristic 在内嵌 user_id 字符串中按已知 key 名扫描取值。
// 仅在严格 JSON 解析失败时使用，避免漏掉转义异常的请求体。
func extractSessionIDHeuristic(s string) string {
	for _, key := range sessionIDHeuristicKeys {
		idx := strings.Index(s, key)
		if idx < 0 {
			continue
		}
		rest := s[idx+len(key):]
		end := strings.IndexAny(rest, `"`)
		if end <= 0 {
			continue
		}
		val := rest[:end]
		if val != "" {
			return val
		}
	}
	return ""
}

// sessionIDMinHeaderLen HTTP 头来源 session_id 最小长度。
// 有效 session_id 通常是 UUID 格式（36 字符）或类似格式。
// 阈值设为 10（比 cc-switch 的 20 更宽松），因为 LsmTokensServer 面向更多客户端。
const sessionIDMinHeaderLen = 10

// parseSessionIDFromAnthropicHeaders 从 Anthropic 协议请求头识别 session_id。
// 当前实现：
//  1. x-claude-code-session-id / claude-code-session-id (Claude Code CLI 原生头，借鉴 Switchyard)
//  2. anthropic-beta: ...; session-id=xxx; ...  (Anthropic 官方 beta header)
//  3. X-Session-Id / X-Anthropic-Session-Id 自定义头
//
// headers 为 nil 时直接返回 ""。
func parseSessionIDFromAnthropicHeaders(headers http.Header) string {
	if headers == nil {
		return ""
	}
	// Claude Code CLI 原生头（v2.0.73 支持两种大小写变体，借鉴 cc-switch extract_claude_session）
	// 优先级最高：Claude Code 是最常用的 Anthropic 协议客户端
	for _, h := range []string{"X-Claude-Code-Session-Id", "Claude-Code-Session-Id"} {
		if v := strings.TrimSpace(headers.Get(h)); v != "" && len(v) >= sessionIDMinHeaderLen {
			return v
		}
	}
	// anthropic-beta 头是分号分隔的 key=value 列表
	if beta := headers.Get("anthropic-beta"); beta != "" {
		for _, part := range strings.Split(beta, ";") {
			kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
			if len(kv) != 2 {
				continue
			}
			if strings.EqualFold(strings.TrimSpace(kv[0]), "session-id") {
				if v := strings.TrimSpace(kv[1]); v != "" && len(v) >= sessionIDMinHeaderLen {
					return v
				}
			}
		}
	}
	for _, h := range []string{"X-Session-Id", "X-Anthropic-Session-Id"} {
		if v := strings.TrimSpace(headers.Get(h)); v != "" && len(v) >= sessionIDMinHeaderLen {
			return v
		}
	}
	return ""
}

// parseSessionIDFromTopLevelField 从 body 顶层识别 session_id。
// 用于某些非标准客户端把 session_id 直接挂在请求顶层（而非 metadata.user_id 内嵌）的场景。
func parseSessionIDFromTopLevelField(body []byte) string {
	var outer struct {
		SessionID string `json:"session_id"`
	}
	if err := json.Unmarshal(body, &outer); err == nil && outer.SessionID != "" {
		return outer.SessionID
	}
	// 启发式扫描（应对顶层 JSON 转义异常但确实含 session_id 字段的样本）
	return extractSessionIDHeuristic(string(body))
}

// parseSessionIDFromGrokHeaders 从 Grok Build 请求头识别 session_id（v2.0.73，借鉴 cc-switch session.rs）。
// Grok Build 使用两个独立头：
//  1. x-grok-conv-id — 对话 ID（跨多轮稳定，优先级高）
//  2. x-grok-session-id — Session ID（作为 conv-id 缺失时的回退）
//
// 注意：x-grok-req-id 是逐请求 ID，不能用于 session 聚合（cc-switch 明确排除该头）。
// headers 为 nil 时直接返回 ""。
func parseSessionIDFromGrokHeaders(headers http.Header) string {
	if headers == nil {
		return ""
	}
	for _, h := range []string{"X-Grok-Conv-Id", "X-Grok-Session-Id"} {
		if v := strings.TrimSpace(headers.Get(h)); v != "" && len(v) >= sessionIDMinHeaderLen {
			return v
		}
	}
	return ""
}

// parseSessionIDFromCodexTurnMetadata 从 x-codex-turn-metadata JSON 头提取 session_id。
// 借鉴 Switchyard 的嵌套 JSON 头解析策略：Codex CLI 将多个元数据字段打包在一个
// JSON 对象头中，用点路径寻址。LsmTokensServer 直接解析 JSON 值提取 session_id。
//
// 识别顺序：
//  1. x-codex-turn-metadata.session_id（Codex 主字段）
//  2. x-codex-turn-metadata.thread_id（Codex 备选，与 session_id 同值）
//
// 实现：用 map[string]interface{} 局部解析以容忍非字符串值，与
// parseSessionIDFromClientMetadata 行为保持一致。
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
			if sid := strings.TrimSpace(s); sid != "" && len(sid) >= sessionIDMinHeaderLen {
				return sid
			}
		}
	}
	// 备选 thread_id（在 Codex 实现里与 session_id 同值）
	if v, ok := meta["thread_id"]; ok {
		if s, ok := v.(string); ok {
			if tid := strings.TrimSpace(s); tid != "" && len(tid) >= sessionIDMinHeaderLen {
				return tid
			}
		}
	}
	return ""
}

// parseSessionIDFromClientMetadata 从 body.client_metadata 嵌套对象识别 session_id。
// 适用于 OpenAI Codex CLI / 等新型 Agent 客户端：它们不再把 session 信息塞进
// metadata.user_id 内嵌 JSON，而是使用顶层 client_metadata.session_id /
// client_metadata.thread_id 字段，且与顶层 prompt_cache_key 值相同（三个字段
// 在 Codex 协议里都代表「同一对话的稳定会话标识」）。
//
// 识别顺序：
//  1. client_metadata.session_id（Codex 主字段）
//  2. client_metadata.thread_id（Codex 备选，与 session_id 在 Codex 实现里同值）
//
// 命中任一即返回；解析失败 / 字段缺失 / 类型不匹配返回 ""。这是启发式兜底，
// 不影响 parseSessionIDFromMetadataUserID 的命中结果（后者优先级更高）。
//
// 实现：用 map[string]interface{} 局部解析以容忍非字符串值（例如 session_id
// 误写为数字），单个字段类型不匹配不会让整段解析失败丢光其它字段。
func parseSessionIDFromClientMetadata(body []byte) string {
	var outer struct {
		ClientMetadata map[string]interface{} `json:"client_metadata"`
	}
	if err := json.Unmarshal(body, &outer); err != nil || outer.ClientMetadata == nil {
		return ""
	}
	if v, ok := outer.ClientMetadata["session_id"]; ok {
		if s, ok := v.(string); ok {
			if sid := strings.TrimSpace(s); sid != "" && len(sid) >= sessionIDMinHeaderLen {
				return sid
			}
		}
	}
	if v, ok := outer.ClientMetadata["thread_id"]; ok {
		if s, ok := v.(string); ok {
			if tid := strings.TrimSpace(s); tid != "" && len(tid) >= sessionIDMinHeaderLen {
				return tid
			}
		}
	}
	return ""
}

// parseSessionIDFromPromptCacheKey 从 body 顶层 prompt_cache_key 识别 session_id。
// OpenAI Codex CLI 在 OpenAI Chat Completions 协议中会把对话稳定标识同时写入
// 顶层 prompt_cache_key（用于 OpenAI 端 prompt cache 命中）；该字段在 Codex
// 协议里与 client_metadata.session_id / client_metadata.thread_id 同值。
//
// 仅当 client_metadata 路径未命中时调用 —— 这是兜底中的兜底，避免对其它
// 自定义使用 prompt_cache_key 的客户端造成误判（heuristic 扫描兜底仍由
// parseSessionIDFromTopLevelField 提供）。
//
// 实现：用 map[string]interface{} 局部解析，非字符串值（例如数字 / 布尔）
// 直接当作未识别返回 ""，与 parseSessionIDFromClientMetadata 行为保持一致。
func parseSessionIDFromPromptCacheKey(body []byte) string {
	var outer map[string]interface{}
	if err := json.Unmarshal(body, &outer); err != nil {
		return ""
	}
	v, ok := outer["prompt_cache_key"]
	if !ok {
		return ""
	}
	s, ok := v.(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(s)
}

// ============================================================================
// OpenAI 协议 Session 识别（调度器）
// ============================================================================
//
// 识别顺序（命中即返回）：
//  1. Agent 工具级：按 User-Agent 匹配已注册的 AgentToolSessionRecognizer
//     （如 OpenClaw → 从 system content 提取 sessionId=）。
//  2. HTTP 头识别（v2.0.73，借鉴 Switchyard header-first 策略）：
//     a. x-codex-turn-metadata JSON 头（Codex CLI 原生嵌套 JSON 头）
//     b. x-session-id 头（OpenCode 原生）
//     c. session-id 头（通用 Codex 兼容）
//  3. metadata.user_id 内嵌 JSON：claude-cli / 多数 OpenAI 客户端传统路径。
//  4. body.client_metadata.session_id / client_metadata.thread_id：
//     OpenAI Codex CLI 等新型 Agent 的「client_metadata」扩展（v2.0.23）。
//  5. 顶层 prompt_cache_key：Codex CLI 同步写入的对话稳定标识
//     （与 client_metadata.session_id 同值；兜底中的兜底）。
//  6. 顶层 session_id：少数非标准客户端直接挂顶层字段。
//
// 扩展指南：
//   - 新增路径：在 openAISessionRecognizer.Recognize 按上述顺序追加。
//   - 保留「命中即返回」语义，避免不同来源的 session_id 互相覆盖。
//
// ============================================================================

type openAISessionRecognizer struct{}

func (r *openAISessionRecognizer) Recognize(body []byte, headers http.Header) string {
	// 1. 先尝试 Agent 工具级识别（UA 触发，如 OpenClaw）
	if sid := tryAgentToolRecognizers(body, headers); sid != "" {
		return sid
	}
	// 2. v2.0.73：HTTP 头识别路径（借鉴 Switchyard header-first 策略）
	//    2a. x-codex-turn-metadata JSON 头（Codex CLI 原生嵌套 JSON 头）
	if sid := parseSessionIDFromCodexTurnMetadata(headers); sid != "" {
		return sid
	}
	//    2b. Grok Build 专用头（x-grok-conv-id / x-grok-session-id）
	if sid := parseSessionIDFromGrokHeaders(headers); sid != "" {
		return sid
	}
	//    2c. x-session-id 头（OpenCode 原生）/ session-id 头（通用 Codex 兼容）
	//        + x-opencode-session-id（OpenCode 特有头，v2.0.73 新增）
	//    2d. 常见 Agent 工具专用会话头变体（v2.0.76 阶段BD：aider/continue/cursor/cline/
	//        copilot/kilo-code/windsurf 等，均走最小长度校验，插在通用头之前避免被遮蔽）
	if headers != nil {
		for _, h := range []string{
			"X-OpenCode-Session-Id",
			// v2.0.76 阶段BD 新增 Agent 工具专用头
			"X-Aider-Session-Id",
			"X-Continue-Session-Id",
			"X-Cursor-Session-Id",
			"X-Cline-Session-Id",
			"X-Github-Copilot-Session-Id",
			"X-Kilo-Code-Session-Id",
			"X-Windsurf-Session-Id",
			// 通用兜底头
			"X-Session-Id",
			"Session-Id",
		} {
			if v := strings.TrimSpace(headers.Get(h)); v != "" && len(v) >= sessionIDMinHeaderLen {
				return v
			}
		}
	}
	// 3. 协议级通用：metadata.user_id 内嵌 JSON（最常见路径）
	if sid := parseSessionIDFromMetadataUserID(body, headers); sid != "" {
		return sid
	}
	// 4. v2.0.23：OpenAI Codex CLI 等 Agent 走 client_metadata.session_id /
	//    client_metadata.thread_id；metadata.user_id 完全缺失时也要能识别。
	if sid := parseSessionIDFromClientMetadata(body); sid != "" {
		return sid
	}
	// 5. v2.0.23：兜底用 prompt_cache_key（与 client_metadata.session_id 同值）。
	if sid := parseSessionIDFromPromptCacheKey(body); sid != "" {
		return sid
	}
	// 6. 顶层 session_id 兜底（非标准客户端偶尔直接把 session_id 挂顶层）
	if sid := parseSessionIDFromTopLevelField(body); sid != "" {
		return sid
	}
	return ""
}

func (r *openAISessionRecognizer) ProtocolType() int {
	return protocol.AgentProtocolType_OpenAI
}

// ============================================================================
// Anthropic 协议 Session 识别
// ============================================================================
//
// 识别顺序（命中即返回）：
//  1. metadata.user_id 内嵌 JSON（最常见路径）
//  2. Anthropic 协议专用头（v2.0.73 优先级调整）：
//     a. x-claude-code-session-id（Claude Code CLI 原生头，借鉴 Switchyard）
//     b. anthropic-beta: ...; session-id=xxx; ...（Anthropic 官方 beta header）
//     c. X-Session-Id / X-Anthropic-Session-Id 自定义头
//  3. 顶层 session_id 字段兜底
//
// ============================================================================

type anthropicSessionRecognizer struct{}

func (r *anthropicSessionRecognizer) Recognize(body []byte, headers http.Header) string {
	// 1. v2.0.73：Anthropic 协议专用头（Claude Code 原生头优先，借鉴 Switchyard）
	//    Claude Code CLI 发送 x-claude-code-session-id 头但不一定有 metadata.user_id，
	//    因此头识别放在 body 之前以避免 body 解析提前返回空值遮蔽有效头。
	if sid := parseSessionIDFromAnthropicHeaders(headers); sid != "" {
		return sid
	}
	// 2. 协议级通用：metadata.user_id 内嵌 JSON（最常见路径）
	if sid := parseSessionIDFromMetadataUserID(body, headers); sid != "" {
		return sid
	}
	// 3. 顶层 session_id 字段兜底（非标准客户端偶尔直接把 session_id 挂顶层）
	if sid := parseSessionIDFromTopLevelField(body); sid != "" {
		return sid
	}
	return ""
}

func (r *anthropicSessionRecognizer) ProtocolType() int {
	return protocol.AgentProtocolType_Anthropic
}

// ============================================================================
// Agent 工具级 Session 识别 —— OpenClaw 等专用识别器
// ============================================================================
//
// 设计目标：
//   - 协议级 Recognizer (openAISessionRecognizer / anthropicSessionRecognizer)
//     负责通用识别（metadata.user_id 内嵌 JSON）。
//   - 某些 Agent 工具（如 OpenClaw）有独立的 session_id 标识方式，需要在
//     协议级识别之前优先匹配。
//   - 通过 User-Agent 触发：例如 User-Agent 含 "OpenAI/JS" 时，按 OpenClaw
//     规则从 system message content 的 "sessionId=... | " 截取。
//
// 扩展指南：
//   - 新增 Agent 工具识别：在 agentToolSessionRecognizers 注册新条目，
//     提供 CanTrigger(UA) + Recognize(body) 即可。
//   - 触发顺序：OpenAI/Anthropic 调度器按注册顺序匹配 UA；先命中先用。
//   - 未命中任何 Agent 工具 → 退回协议级通用识别。
//
// 性能考量：
//   - UA 匹配用 strings.Contains，O(n) 且 n 较小（一般 < 200 字节）。
//   - body 解析只 Unmarshal 顶层 messages 数组 + 第一条 system message，
//     避免遍历整段 body。system message content 通常 < 32KB。
//
// ============================================================================

// AgentToolSessionRecognizer Agent 工具级 session 识别接口。
// 协议级 Recognizer 在通用识别前，按 CanTrigger 匹配 UA，命中后调用 Recognize。
type AgentToolSessionRecognizer interface {
	// AgentName 返回 Agent 工具名称（如 "OpenClaw"），用于日志/调试。
	AgentName() string
	// CanTrigger 根据 User-Agent 判定是否由本识别器处理。
	CanTrigger(userAgent string) bool
	// Recognize 从请求 body 中提取 session_id；返回空字符串表示未识别。
	Recognize(body []byte) string
}

// agentToolSessionRecognizers Agent 工具识别器注册表（按注册顺序匹配 UA）。
var agentToolSessionRecognizers = []AgentToolSessionRecognizer{
	&openClawSessionRecognizer{},
}

// RegisterAgentToolSessionRecognizer 注册 Agent 工具级 session 识别器。
// 用于测试或新增第三方 Agent 工具。
func RegisterAgentToolSessionRecognizer(r AgentToolSessionRecognizer) {
	agentToolSessionRecognizers = append(agentToolSessionRecognizers, r)
}

// tryAgentToolRecognizers 按 UA 顺序尝试匹配 Agent 工具识别器；全部未命中返回 ""。
// 仅在协议级 Recognizer 内部调用，外部请使用 RecognizeSessionID。
func tryAgentToolRecognizers(body []byte, headers http.Header) string {
	if headers == nil || body == nil {
		return ""
	}
	ua := headers.Get("User-Agent")
	if ua == "" {
		return ""
	}
	for _, r := range agentToolSessionRecognizers {
		if !r.CanTrigger(ua) {
			continue
		}
		if sid := r.Recognize(body); sid != "" {
			return sid
		}
		// UA 命中但 body 中未找到 session_id 也停止后续匹配，避免不同 Agent 工具误抢。
		// （例如同 UA 子串被多个识别器包含时，先注册者胜。）
		break
	}
	return ""
}

// ============================================================================
// OpenClaw Agent 工具识别器
// ============================================================================
//
// 触发条件：User-Agent 含 "OpenAI/JS"（OpenClaw 的 WebChat/SSE 客户端标识）。
//
// 提取规则：
//   1. 解析顶层 messages 数组，找到第一条 {"role": "system", ...}。
//   2. 在其 content 字符串中查找 "sessionId=" 子串。
//   3. 取 "sessionId=" 后到下一个 " | "（注意两侧空格）之间的子串作为 session_id。
//
// 样例 body（节选）：
//   "Runtime: agent=main | session=agent:main:lsminterserver |
//    sessionId=144ca9ed-c216-40f2-87a7-cd9df1dc7f3c | host=iZ0jlg..."
//
// 失败模式（全部返回 ""）：
//   - 找不到 messages 字段
//   - 没有 role=system 的消息
//   - system content 不含 "sessionId="
//   - "sessionId=" 后没有 " | " 终止符（说明格式异常）
//   - body 非合法 JSON
//
// ============================================================================

const openClawUserAgentMarker = "OpenAI/JS"

// openClawSessionRecognizer OpenClaw 专用 session 识别器
type openClawSessionRecognizer struct{}

func (r *openClawSessionRecognizer) AgentName() string {
	return "OpenClaw"
}

func (r *openClawSessionRecognizer) CanTrigger(userAgent string) bool {
	return strings.Contains(userAgent, openClawUserAgentMarker)
}

func (r *openClawSessionRecognizer) Recognize(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	content, ok := extractFirstSystemMessageContent(body)
	if ok {
		return extractSessionIDFromOpenClawSystemContent(content)
	}

	// 部分真实 OpenClaw 样本的 system content 中包含未转义换行，严格 JSON
	// 解析会失败。UA 已命中 OpenClaw 时，若原始 body 明确包含 system role，
	// 退化为在原始文本中扫描 sessionId，保证 session 识别不被样本转义问题中断。
	raw := string(body)
	if strings.Contains(raw, `"role": "system"`) || strings.Contains(raw, `"role":"system"`) {
		return extractSessionIDFromOpenClawSystemContent(raw)
	}
	return ""
}

// extractFirstSystemMessageContent 从请求 body 中提取第一条 system message 的 content 字段。
// 用 json.RawMessage 局部解析，避免全量 Unmarshal 巨大 body。
// 成功返回 (content, true)；失败返回 ("", false)。
func extractFirstSystemMessageContent(body []byte) (string, bool) {
	var outer struct {
		Messages []json.RawMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &outer); err != nil || len(outer.Messages) == 0 {
		return "", false
	}
	for _, raw := range outer.Messages {
		var msg struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Role != "system" {
			continue
		}
		// content 可能是字符串或数组（多模态）；仅支持字符串形式，OpenClaw 当前正是字符串。
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil && s != "" {
			return s, true
		}
		return "", false
	}
	return "", false
}

// extractSessionIDFromOpenClawSystemContent 从 OpenClaw system content 中提取 session_id。
// 规则：找到第一个 "sessionId="，取其后到下一个 " | "（两侧空格）之间的子串。
// 容错：终止符退化为 "| " 或 "|"；未找到终止符返回 ""。
func extractSessionIDFromOpenClawSystemContent(content string) string {
	const key = "sessionId="
	idx := strings.Index(content, key)
	if idx < 0 {
		return ""
	}
	rest := content[idx+len(key):]
	// 优先匹配 " | "（与样本一致），否则尝试 "| "（紧凑格式），最后试 "|"
	for _, sep := range []string{" | ", "| ", "|"} {
		if end := strings.Index(rest, sep); end >= 0 {
			return strings.TrimSpace(rest[:end])
		}
	}
	// 终止符缺失：整段剩余视为 session_id（去掉首尾空白）
	return strings.TrimSpace(rest)
}
