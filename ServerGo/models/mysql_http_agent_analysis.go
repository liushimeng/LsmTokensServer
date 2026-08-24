package models

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/recognizer"
	"strings"
	"time"
)

const (
	// SessionGapMinutes Session 超时判定间隔（分钟），超过此间隔视为新 Session
	SessionGapMinutes = 5
	// SessionAnalysisLimit Session 分析查询记录数上限
	SessionAnalysisLimit = 5000
	// TaskAnalysisLimit Task 分析查询记录数上限
	TaskAnalysisLimit = 5000
)

// SessionInfo Session 信息
type SessionInfo struct {
	SessionID        string    `json:"session_id"`
	StartTime        time.Time `json:"start_time"`
	EndTime          time.Time `json:"end_time"`
	DurationMin      float64   `json:"duration_min"`
	RequestCount     int64     `json:"request_count"`
	TaskCount        int64     `json:"task_count"`
	RemoteAddr       string    `json:"remote_addr"`
	FirstURL         string    `json:"first_url"`
	LastURL          string    `json:"last_url"`
	AvgElapsedMs     int64     `json:"avg_elapsed_ms"`
	TotalElapsedMs   int64     `json:"total_elapsed_ms"`
	TotalReqSize     int64     `json:"total_req_size"`
	TotalRespSize    int64     `json:"total_resp_size"`
	TokensInputSize  uint64    `json:"tokens_input_size"`
	TokensOutputSize uint64    `json:"tokens_output_size"`
	TokensAllSize    uint64    `json:"tokens_all_size"`
	Models           []string  `json:"models"`
	HasSystemPrompt  bool      `json:"has_system_prompt"`
	HasToolCall      bool      `json:"has_tool_call"`
	IsStream         bool      `json:"is_stream"`
	FirstRecordID    uint64    `json:"first_record_id"`
	LastRecordID     uint64    `json:"last_record_id"`
}

// TaskInfo Task 信息
type TaskInfo struct {
	ID                   uint64 `json:"id"`
	CreatedAt            string `json:"created_at"`
	RequestURL           string `json:"request_url"`
	RequestMethod        string `json:"request_method"`
	ResponseStatus       string `json:"response_status"`
	RemoteAddr           string `json:"remote_addr"`
	ElapsedMs            int64  `json:"elapsed_ms"`
	ReqSize              uint64 `json:"req_size"`
	RespSize             uint64 `json:"resp_size"`
	Model                string `json:"model"`
	Stream               bool   `json:"stream"`
	MessageCount         int    `json:"message_count"`
	HasSystemPrompt      bool   `json:"has_system_prompt"`
	HasToolCall          bool   `json:"has_tool_call"`
	UserMessageCount     int    `json:"user_message_count"`
	SessionID            string `json:"session_id"`
	SessionFirstRecordID uint64 `json:"session_first_record_id"`
	SessionLastRecordID  uint64 `json:"session_last_record_id"`
	SessionRecordCount   int64  `json:"session_record_count"`
}

// SessionAnalysisResult Session 分析结果
type SessionAnalysisResult struct {
	Sessions           []SessionInfo `json:"sessions"`
	TotalSessions      int           `json:"total_sessions"`
	TotalTasks         int64         `json:"total_tasks"`
	TotalRequests      int64         `json:"total_requests"`
	AvgDurationMin     float64       `json:"avg_duration_min"`
	AvgTasksPerSession float64       `json:"avg_tasks_per_session"`
}

// TaskAnalysisResult Task 分析结果
type TaskAnalysisResult struct {
	Tasks           []TaskInfo       `json:"tasks"`
	TotalTasks      int64            `json:"total_tasks"`
	ModelStats      map[string]int64 `json:"model_stats"`
	StreamCount     int64            `json:"stream_count"`
	NonStreamCount  int64            `json:"non_stream_count"`
	HasSystemPrompt int64            `json:"has_system_prompt"`
	HasToolCall     int64            `json:"has_tool_call"`
	AvgElapsedMs    int64            `json:"avg_elapsed_ms"`
	AvgMessages     float64          `json:"avg_messages"`
}

// GetSessionAnalysis 获取 Session 分析数据
func GetSessionAnalysis(userName, modelName string, subTableNum int, days int) (*SessionAnalysisResult, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days > 365 {
		days = 365
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	var records []TAgentHttpTransactionDataItem
	// 使用预解析字段，完全排除 request_body/response_body 等大字段
	err := database.DB.Table(tableName).
		Select("id", "created_at", "request_method", "request_url", "request_remote_addr",
			"request_content_length", "response_content_length", "elapsed_ms",
			"tokens_input_size", "tokens_output_size", "tokens_all_size",
			"is_parsed", "is_task", "task_model", "is_stream", "has_system_prompt", "has_tool_call", "message_count").
		Where("user_name = ? AND model_name = ? AND created_at >= DATE_SUB(CURDATE(), INTERVAL ? DAY)", userName, modelName, days).
		Order("created_at ASC").
		Limit(SessionAnalysisLimit).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query records for session analysis: %w", err)
	}

	return aggregateSessions(records, userName+"-"+modelName), nil
}

// computeSessionList 将记录聚合为 Session 列表（共享逻辑）
// records 必须按 created_at ASC 排序
func computeSessionList(records []TAgentHttpTransactionDataItem, serviceName string) []SessionInfo {
	if len(records) == 0 {
		return nil
	}

	gapThreshold := time.Duration(SessionGapMinutes) * time.Minute
	var sessions []SessionInfo
	var currentSession *SessionInfo
	var sessionIndex int

	for i, rec := range records {
		isTask := isTaskRequest(rec)
		taskDetail := parseTaskDetail(rec)

		if currentSession == nil || rec.CreatedAt.Sub(currentSession.EndTime) > gapThreshold {
			if currentSession != nil {
				sessions = append(sessions, *currentSession)
			}
			sessionIndex++
			currentSession = &SessionInfo{
				SessionID:     fmt.Sprintf("%s-%d", serviceName, sessionIndex),
				StartTime:     rec.CreatedAt,
				EndTime:       rec.CreatedAt,
				RemoteAddr:    rec.RequestRemoteAddr,
				FirstURL:      rec.RequestURL,
				LastURL:       rec.RequestURL,
				FirstRecordID: rec.ID,
				LastRecordID:  rec.ID,
				Models:        make([]string, 0),
			}
		}

		currentSession.EndTime = rec.CreatedAt
		currentSession.LastURL = rec.RequestURL
		currentSession.LastRecordID = rec.ID
		currentSession.RequestCount++
		currentSession.TotalElapsedMs += rec.ElapsedMs
		currentSession.TotalReqSize += int64(rec.RequestContentLength)
		currentSession.TotalRespSize += int64(rec.ResponseContentLength)
		currentSession.TokensInputSize += rec.TokensInputSize
		currentSession.TokensOutputSize += rec.TokensOutputSize
		currentSession.TokensAllSize += rec.TokensAllSize

		if isTask {
			currentSession.TaskCount++
			if taskDetail.Model != "" && !containsString(currentSession.Models, taskDetail.Model) {
				currentSession.Models = append(currentSession.Models, taskDetail.Model)
			}
			if taskDetail.HasSystemPrompt {
				currentSession.HasSystemPrompt = true
			}
			if taskDetail.HasToolCall {
				currentSession.HasToolCall = true
			}
			if taskDetail.Stream {
				currentSession.IsStream = true
			}
		}

		if i == len(records)-1 && currentSession != nil {
			sessions = append(sessions, *currentSession)
		}
	}

	for i := range sessions {
		s := &sessions[i]
		s.DurationMin = s.EndTime.Sub(s.StartTime).Minutes()
		if s.RequestCount > 0 {
			s.AvgElapsedMs = s.TotalElapsedMs / s.RequestCount
		}
	}

	return sessions
}

// aggregateSessions 将记录聚合为 Session
func aggregateSessions(records []TAgentHttpTransactionDataItem, serviceName string) *SessionAnalysisResult {
	result := &SessionAnalysisResult{
		Sessions: make([]SessionInfo, 0),
	}
	if len(records) == 0 {
		return result
	}

	sessions := computeSessionList(records, serviceName)
	result.Sessions = sessions

	// 反转 Session 列表，使最新的 Session 显示在最前面（与 Task 页面保持一致）
	for i, j := 0, len(result.Sessions)-1; i < j; i, j = i+1, j-1 {
		result.Sessions[i], result.Sessions[j] = result.Sessions[j], result.Sessions[i]
	}

	result.TotalSessions = len(result.Sessions)
	var totalDuration float64
	for _, s := range result.Sessions {
		result.TotalTasks += s.TaskCount
		result.TotalRequests += s.RequestCount
		totalDuration += s.DurationMin
	}
	if result.TotalSessions > 0 {
		result.AvgDurationMin = totalDuration / float64(result.TotalSessions)
		result.AvgTasksPerSession = float64(result.TotalTasks) / float64(result.TotalSessions)
	}

	return result
}

// GetTaskAnalysis 获取 Task 分析数据
func GetTaskAnalysis(userName, modelName string, subTableNum int, days int) (*TaskAnalysisResult, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	var records []TAgentHttpTransactionDataItem
	// 使用预解析字段，完全排除 request_body/response_body 等大字段
	err := database.DB.Table(tableName).
		Select("id", "created_at", "request_method", "request_url", "response_status",
			"request_remote_addr", "request_content_length", "response_content_length",
			"elapsed_ms",
			"is_parsed", "is_task", "task_model", "is_stream", "has_system_prompt", "has_tool_call", "message_count").
		Where("user_name = ? AND model_name = ? AND created_at >= DATE_SUB(CURDATE(), INTERVAL ? DAY)", userName, modelName, days).
		Order("created_at DESC").
		Limit(TaskAnalysisLimit).
		Find(&records).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query records for task analysis: %w", err)
	}

	return aggregateTasks(records), nil
}

// aggregateTasks 将记录聚合为 Task
func aggregateTasks(records []TAgentHttpTransactionDataItem) *TaskAnalysisResult {
	result := &TaskAnalysisResult{
		Tasks:      make([]TaskInfo, 0),
		ModelStats: make(map[string]int64),
	}

	var totalElapsed int64
	var totalMessages int

	// 计算 Session 列表（用于给每个 Task 附加 Session 上下文）
	var sessions []SessionInfo
	if len(records) > 0 {
		ascRecords := make([]TAgentHttpTransactionDataItem, len(records))
		copy(ascRecords, records)
		// 按时间升序排序
		for i, j := 0, len(ascRecords)-1; i < j; i, j = i+1, j-1 {
			ascRecords[i], ascRecords[j] = ascRecords[j], ascRecords[i]
		}
		sessions = computeSessionList(ascRecords, "")
	}

	for _, rec := range records {
		if !isTaskRequest(rec) {
			continue
		}

		taskDetail := parseTaskDetail(rec)
		task := TaskInfo{
			ID:               rec.ID,
			CreatedAt:        rec.CreatedAt.Format("2006-01-02 15:04:05"),
			RequestURL:       rec.RequestURL,
			RequestMethod:    rec.RequestMethod,
			ResponseStatus:   rec.ResponseStatus,
			RemoteAddr:       rec.RequestRemoteAddr,
			ElapsedMs:        rec.ElapsedMs,
			ReqSize:          rec.RequestContentLength,
			RespSize:         rec.ResponseContentLength,
			Model:            taskDetail.Model,
			Stream:           taskDetail.Stream,
			MessageCount:     taskDetail.MessageCount,
			HasSystemPrompt:  taskDetail.HasSystemPrompt,
			HasToolCall:      taskDetail.HasToolCall,
			UserMessageCount: taskDetail.UserMessageCount,
		}

		// 查找 Task 所属的 Session
		for _, session := range sessions {
			if rec.RequestRemoteAddr == session.RemoteAddr &&
				!rec.CreatedAt.Before(session.StartTime) &&
				!rec.CreatedAt.After(session.EndTime) {
				task.SessionID = session.SessionID
				task.SessionFirstRecordID = session.FirstRecordID
				task.SessionLastRecordID = session.LastRecordID
				task.SessionRecordCount = session.RequestCount
				break
			}
		}

		result.Tasks = append(result.Tasks, task)
		result.TotalTasks++
		totalElapsed += rec.ElapsedMs

		if taskDetail.Model != "" {
			result.ModelStats[taskDetail.Model]++
		}
		if taskDetail.Stream {
			result.StreamCount++
		} else {
			result.NonStreamCount++
		}
		if taskDetail.HasSystemPrompt {
			result.HasSystemPrompt++
		}
		if taskDetail.HasToolCall {
			result.HasToolCall++
		}
		totalMessages += taskDetail.MessageCount
	}

	if result.TotalTasks > 0 {
		result.AvgElapsedMs = totalElapsed / result.TotalTasks
		result.AvgMessages = float64(totalMessages) / float64(result.TotalTasks)
	}

	return result
}

// taskDetail 解析后的 Task 详情
type taskDetail struct {
	Model            string
	Stream           bool
	MessageCount     int
	HasSystemPrompt  bool
	HasToolCall      bool
	UserMessageCount int
}

// isTaskRequest 判断是否为 Task 请求（优先使用预解析字段）
func isTaskRequest(rec TAgentHttpTransactionDataItem) bool {
	if rec.IsParsed {
		return rec.IsTask
	}
	if rec.RequestMethod != "POST" {
		return false
	}
	url := strings.ToLower(rec.RequestURL)
	return strings.Contains(url, "/messages") || strings.Contains(url, "/chat/completions")
}

// parseRequestBodyFeatures 从 base64 编码的请求体中提取 Task 关键特征
// 返回：isTask, model, isStream, hasSystemPrompt, hasToolCall, messageCount, userMessageCount
func parseRequestBodyFeatures(requestBody, method, url string) (bool, string, bool, bool, bool, int, int) {
	if method != "POST" {
		return false, "", false, false, false, 0, 0
	}
	urlLower := strings.ToLower(url)
	if !strings.Contains(urlLower, "/messages") && !strings.Contains(urlLower, "/chat/completions") {
		return false, "", false, false, false, 0, 0
	}

	if requestBody == "" {
		return true, "", false, false, false, 0, 0
	}

	decoded, err := base64.StdEncoding.DecodeString(requestBody)
	if err != nil {
		return true, "", false, false, false, 0, 0
	}

	var body map[string]interface{}
	if err := json.Unmarshal(decoded, &body); err != nil {
		return true, "", false, false, false, 0, 0
	}

	var model string
	var isStream, hasSystem, hasTools bool
	var msgCount, userMsgCount int

	// 模型
	if m, ok := body["model"].(string); ok {
		model = m
	}

	// 流式
	if stream, ok := body["stream"].(bool); ok {
		isStream = stream
	}

	// System Prompt
	if system, ok := body["system"].(string); ok && system != "" {
		hasSystem = true
	}

	// Messages 分析
	if messages, ok := body["messages"].([]interface{}); ok {
		msgCount = len(messages)
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				if role, ok := msgMap["role"].(string); ok {
					if role == "system" {
						hasSystem = true
					}
					if role == "user" {
						userMsgCount++
					}
				}
			}
		}
	}

	// Tools 分析
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		hasTools = true
	}
	if toolChoice, ok := body["tool_choice"].(string); ok && toolChoice != "" && toolChoice != "none" {
		hasTools = true
	}

	return true, model, isStream, hasSystem, hasTools, msgCount, userMsgCount
}

// parseRequestToolsFromBody 从 base64 编码的请求体中解析 tools 字段，提取工具名称列表
// 支持 OpenAI 和 Anthropic 两种协议格式
// OpenAI: tools[].function.name
// Anthropic: tools[].name
//
// 同时兼容以下边缘场景：
//  1. tools 元素是字符串（部分 SDK 行为）
//  2. tools 出现在 metadata.tools / parameters.tools 嵌套结构
//  3. body 已经被二次 JSON 字符串化（外层是 {"requestBody":"{...}"}）
//  4. body 本身存储时就是明文（不是 base64）
//  5. base64 解码失败时的容错回退
//  6. 兜底：OpenAI messages[].tool_calls[].function.name
func ParseRequestToolsFromBody(requestBody string) string {
	if requestBody == "" {
		return ""
	}

	// 兼容 base64 编码的 body（标准存储格式）
	decoded, err := base64.StdEncoding.DecodeString(requestBody)
	if err != nil {
		// base64 解码失败：可能是明文存储，尝试直接解析
		decoded = []byte(requestBody)
	}

	return parseRequestToolsFromDecodedBody(decoded, 0)
}

func parseRequestToolsFromDecodedBody(decoded []byte, depth int) string {
	if len(decoded) == 0 || depth > 3 {
		return ""
	}

	var body map[string]interface{}
	if err := json.Unmarshal(decoded, &body); err != nil {
		// 兼容 SSE 增量包拼接：提取第一个 JSON 对象
		first := extractFirstJSONObjectSafe(string(decoded))
		if first == "" {
			return ""
		}
		if err := json.Unmarshal([]byte(first), &body); err != nil {
			return ""
		}
	}

	if names := extractToolNamesFromMap(body); names != "" {
		return names
	}

	return extractToolNamesFromWrappedBody(body, depth+1)
}

func extractToolNamesFromWrappedBody(body map[string]interface{}, depth int) string {
	if body == nil || depth > 3 {
		return ""
	}

	for _, key := range []string{"requestBody", "body", "payload"} {
		raw, ok := body[key]
		if !ok || raw == nil {
			continue
		}

		switch v := raw.(type) {
		case string:
			if names := parseRequestToolsFromDecodedBody([]byte(v), depth); names != "" {
				return names
			}
			if decoded, err := base64.StdEncoding.DecodeString(v); err == nil {
				if names := parseRequestToolsFromDecodedBody(decoded, depth); names != "" {
					return names
				}
			}
		case map[string]interface{}:
			if names := extractToolNamesFromMap(v); names != "" {
				return names
			}
			if names := extractToolNamesFromWrappedBody(v, depth+1); names != "" {
				return names
			}
		}
	}

	return ""
}

// extractToolNamesFromMap 共享的提取逻辑（OpenAI / Anthropic / 自定义格式 + 兜底）
// v2.0.16 重构：协议分发到 recognizer_openai_function_call.go / recognizer_anthropic_tool_call.go
func extractToolNamesFromMap(body map[string]interface{}) string {
	if body == nil {
		return ""
	}

	// 协议分发：先按特征判断是 OpenAI 还是 Anthropic 格式
	if recognizer.IsOpenAIFormatBody(body) {
		if names := recognizer.ExtractOpenAIToolNames(body); names != "" {
			return names
		}
		// OpenAI 兜底：从 messages[].tool_calls[].function.name
		if names := recognizer.ExtractOpenAIToolCallsFromMessages(body); names != "" {
			return names
		}
		// 兜底到 metadata.tools / parameters.tools
		if names := extractOpenAIToolNamesFromNestedContainer(body); names != "" {
			return names
		}
		return ""
	}

	if recognizer.IsAnthropicFormatBody(body) {
		if names := recognizer.ExtractAnthropicToolNames(body); names != "" {
			return names
		}
		// 兜底到 metadata.tools / parameters.tools
		if names := extractAnthropicToolNamesFromNestedContainer(body); names != "" {
			return names
		}
		return ""
	}

	// 兜底：无法识别协议时，尝试通用提取（保留旧行为兼容未知格式）
	return extractToolNamesFromMapGeneric(body)
}

// extractToolNamesFromMapGeneric 兜底通用提取（未知协议或混合格式）。
// 兼容字符串 tools / metadata.tools / parameters.tools / messages[].tool_calls。
func extractToolNamesFromMapGeneric(body map[string]interface{}) string {
	// 1. 标准 tools 字段
	toolsRaw, ok := body["tools"].([]interface{})
	if !ok || len(toolsRaw) == 0 {
		// 2. 兜底：metadata.tools / parameters.tools
		for _, key := range []string{"metadata", "parameters"} {
			container, ok := body[key].(map[string]interface{})
			if !ok {
				continue
			}
			if inner, ok := container["tools"].([]interface{}); ok && len(inner) > 0 {
				toolsRaw = inner
				break
			}
		}
	}

	var toolNames []string
	seen := make(map[string]bool)

	if len(toolsRaw) > 0 {
		for _, toolRaw := range toolsRaw {
			name := extractSingleToolName(toolRaw)
			if name != "" && !seen[name] {
				toolNames = append(toolNames, name)
				seen[name] = true
			}
		}
	}

	// 3. 兜底：OpenAI messages[].tool_calls[].function.name
	if len(toolNames) == 0 {
		if messages, ok := body["messages"].([]interface{}); ok {
			for _, msg := range messages {
				msgMap, ok := msg.(map[string]interface{})
				if !ok {
					continue
				}
				tcs, ok := msgMap["tool_calls"].([]interface{})
				if !ok {
					continue
				}
				for _, tc := range tcs {
					tcMap, ok := tc.(map[string]interface{})
					if !ok {
						continue
					}
					var n string
					if fn, ok := tcMap["function"].(map[string]interface{}); ok {
						if name, ok := fn["name"].(string); ok && name != "" {
							n = name
						}
					}
					if n == "" {
						if name, ok := tcMap["name"].(string); ok && name != "" {
							n = name
						}
					}
					if n != "" && !seen[n] {
						toolNames = append(toolNames, n)
						seen[n] = true
					}
				}
			}
		}
	}

	if len(toolNames) == 0 {
		return ""
	}
	return strings.Join(toolNames, ",")
}

// extractSingleToolName 提取单个 tool 元素的名称（兼容多协议格式）
// v2.0.16 改为兼容层：优先 OpenAI，其次 Anthropic，最后自定义字段。
func extractSingleToolName(toolRaw interface{}) string {
	// OpenAI 优先（tools[].function.name）
	if n := recognizer.ExtractOpenAISingleToolName(toolRaw); n != "" {
		return n
	}
	// 兼容 Anthropic（tools[].name）和未拆分格式
	if s, isStr := toolRaw.(string); isStr {
		return s
	}
	toolMap, isMap := toolRaw.(map[string]interface{})
	if !isMap {
		return ""
	}
	// Anthropic 格式 - tools[].name
	if n, ok := toolMap["name"].(string); ok && n != "" {
		return n
	}
	// 自定义字段
	for _, k := range []string{"customName", "displayName", "id", "toolName"} {
		if n, ok := toolMap[k].(string); ok && n != "" {
			return n
		}
	}
	return ""
}

// extractOpenAIToolNamesFromNestedContainer 从 metadata.tools / parameters.tools 提取 OpenAI 格式工具名。
func extractOpenAIToolNamesFromNestedContainer(body map[string]interface{}) string {
	for _, key := range []string{"metadata", "parameters"} {
		container, ok := body[key].(map[string]interface{})
		if !ok {
			continue
		}
		inner, ok := container["tools"].([]interface{})
		if !ok || len(inner) == 0 {
			continue
		}
		var names []string
		seen := make(map[string]bool)
		for _, toolRaw := range inner {
			n := recognizer.ExtractOpenAISingleToolName(toolRaw)
			if n != "" && !seen[n] {
				names = append(names, n)
				seen[n] = true
			}
		}
		if len(names) > 0 {
			return strings.Join(names, ",")
		}
	}
	return ""
}

// extractAnthropicToolNamesFromNestedContainer 从 metadata.tools / parameters.tools 提取 Anthropic 格式工具名。
func extractAnthropicToolNamesFromNestedContainer(body map[string]interface{}) string {
	for _, key := range []string{"metadata", "parameters"} {
		container, ok := body[key].(map[string]interface{})
		if !ok {
			continue
		}
		inner, ok := container["tools"].([]interface{})
		if !ok || len(inner) == 0 {
			continue
		}
		var names []string
		seen := make(map[string]bool)
		for _, toolRaw := range inner {
			n := recognizer.ExtractAnthropicSingleToolName(toolRaw)
			if n != "" && !seen[n] {
				names = append(names, n)
				seen[n] = true
			}
		}
		if len(names) > 0 {
			return strings.Join(names, ",")
		}
	}
	return ""
}

// extractFirstJSONObjectSafe 提取字符串中的第一个 JSON 对象 { ... }
// 用于处理 SSE 增量包拼接的 body。
func extractFirstJSONObjectSafe(s string) string {
	start := strings.Index(s, "{")
	if start < 0 {
		return ""
	}
	depth := 0
	inString := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		switch c {
		case '\\':
			escape = true
		case '"':
			inString = !inString
		case '{':
			if !inString {
				depth++
			}
		case '}':
			if !inString {
				depth--
				if depth == 0 {
					return s[start : i+1]
				}
			}
		}
	}
	return ""
}

// parseTaskDetail 解析请求体获取 Task 详情（优先使用预解析字段）
func parseTaskDetail(rec TAgentHttpTransactionDataItem) taskDetail {
	detail := taskDetail{}
	if rec.IsParsed {
		detail.Model = rec.TaskModel
		detail.Stream = rec.IsStream
		detail.HasSystemPrompt = rec.HasSystemPrompt
		detail.HasToolCall = rec.HasToolCall
		detail.MessageCount = rec.MessageCount
		detail.UserMessageCount = rec.UserMessageCount
		return detail
	}
	if rec.RequestBody == "" {
		return detail
	}

	decoded, err := base64.StdEncoding.DecodeString(rec.RequestBody)
	if err != nil {
		return detail
	}

	var body map[string]interface{}
	if err := json.Unmarshal(decoded, &body); err != nil {
		return detail
	}

	// 模型
	if model, ok := body["model"].(string); ok {
		detail.Model = model
	}

	// 流式
	if stream, ok := body["stream"].(bool); ok {
		detail.Stream = stream
	}

	// System Prompt
	if system, ok := body["system"].(string); ok && system != "" {
		detail.HasSystemPrompt = true
	}

	// Messages 分析
	if messages, ok := body["messages"].([]interface{}); ok {
		detail.MessageCount = len(messages)
		for _, msg := range messages {
			if msgMap, ok := msg.(map[string]interface{}); ok {
				if role, ok := msgMap["role"].(string); ok {
					if role == "system" {
						detail.HasSystemPrompt = true
					}
					if role == "user" {
						detail.UserMessageCount++
					}
				}
			}
		}
	}

	// Tools 分析
	if tools, ok := body["tools"].([]interface{}); ok && len(tools) > 0 {
		detail.HasToolCall = true
	}
	if toolChoice, ok := body["tool_choice"].(string); ok && toolChoice != "" && toolChoice != "none" {
		detail.HasToolCall = true
	}

	return detail
}

// containsString 检查字符串切片是否包含指定字符串
func containsString(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// truncateRequestTools 截断 request_tools 字段以适配 GORM size:512。
// 优先在逗号边界截断，避免切断工具名；不追加 "..." 伪工具名，避免审计和筛选误判。
// 与 mysql_http_agent_model.go 的 RequestTools size:512 保持一致。
const requestToolsMaxLen = 500

func truncateRequestTools(s string) string {
	if len(s) <= requestToolsMaxLen {
		return s
	}
	// 找到 500 字节以内最后一个逗号
	cut := strings.LastIndex(s[:requestToolsMaxLen], ",")
	if cut < 0 {
		// 没有逗号——直接硬截断
		return s[:requestToolsMaxLen]
	}
	return s[:cut]
}

// GetAgentToolStatsByRange 按时间范围获取 Agent 工具统计（按 user_name + model_name 分表）
// v2.0.53: 改用 Go 端聚合 — 旧实现 SELECT agent_tool_name + COUNT/MIN/MAX + GROUP BY
//
//	会让 MySQL 走 "Using temporary; Using filesort"，对 7 天 20K 行场景实测 700ms+。
//	新实现只 SELECT 2 个小字段（agent_tool_name + created_at），
//	符合 v2.0.42 longtext 白名单契约；复合索引 (user_name, model_name, created_at)
//	让 WHERE 走索引范围扫描，Go 端按 agent_tool_name 桶聚合。
//	不再触发 temp table / filesort，性能与首屏 lsmRunInsightsSummary 一致。
func GetAgentToolStatsByRange(userName, modelName string, subTableNum int, days int) (*AgentToolStatsResponse, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if days > 365 {
		days = 365
	}

	// 尝试从缓存获取
	cacheKey := makeStatsCacheKey("GetAgentToolStatsByRange", userName, modelName, subTableNum, days, "")
	if cached, ok := getStatsFromCache(cacheKey); ok {
		if stats, valid := cached.(*AgentToolStatsResponse); valid {
			return stats, nil
		}
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)

	if !isValidTableName(tableName) {
		return nil, fmt.Errorf("invalid table name: %s", tableName)
	}

	// 只 SELECT 2 个小字段：agent_tool_name + created_at。
	// 严格避开 longtext 字段（request_body / response_body 等 8 个），符合 v2.0.42 白名单契约。
	// 不带 GROUP BY / COUNT / MIN / MAX —— 全部走 Go 端桶聚合。
	// 复合索引 idx_user_model_created 让 WHERE 走索引范围扫描，rows 大幅减少。
	type rawRow struct {
		AgentToolName string    `gorm:"column:agent_tool_name"`
		CreatedAt     time.Time `gorm:"column:created_at"`
	}
	var rows []rawRow

	sdb, cancel := database.StatsDB()
	defer cancel()
	query := sdb.Table(tableName).
		Select("agent_tool_name, created_at").
		Where("user_name = ? AND model_name = ? AND agent_tool_name != '' AND agent_tool_name != 'unknown'", userName, modelName)
	if days > 0 {
		query = query.Where("created_at >= DATE_SUB(CURDATE(), INTERVAL ? DAY)", days)
	}

	if err := query.Find(&rows).Error; err != nil {
		return nil, fmt.Errorf("failed to query agent tool stats rows: %w", err)
	}

	// Go 端按 agent_tool_name 桶聚合（同时维护 first/last seen 时间）
	type bucket struct {
		name      string
		count     int64
		firstSeen time.Time
		lastSeen  time.Time
	}
	buckets := make(map[string]*bucket)
	for _, r := range rows {
		if r.AgentToolName == "" || r.AgentToolName == "unknown" {
			continue
		}
		b, ok := buckets[r.AgentToolName]
		if !ok {
			b = &bucket{name: r.AgentToolName, firstSeen: r.CreatedAt, lastSeen: r.CreatedAt}
			buckets[r.AgentToolName] = b
		}
		b.count++
		if r.CreatedAt.Before(b.firstSeen) {
			b.firstSeen = r.CreatedAt
		}
		if r.CreatedAt.After(b.lastSeen) {
			b.lastSeen = r.CreatedAt
		}
	}

	// 计算总数
	var totalCount int64
	for _, b := range buckets {
		totalCount += b.count
	}

	// 构建响应并按 count DESC 排序（与旧实现 ORDER BY count DESC 一致）
	stats := make([]AgentToolStat, 0, len(buckets))
	for _, b := range buckets {
		var percentage float64
		if totalCount > 0 {
			percentage = float64(b.count) / float64(totalCount) * 100
		}
		stats = append(stats, AgentToolStat{
			AgentToolName: b.name,
			Count:         b.count,
			FirstSeenAt:   b.firstSeen.Format("2006-01-02 15:04:05"),
			LastSeenAt:    b.lastSeen.Format("2006-01-02 15:04:05"),
			Percentage:    percentage,
		})
	}
	// 简单冒泡排序（结果集通常较小，<50 个工具）
	for i := 0; i < len(stats); i++ {
		for j := i + 1; j < len(stats); j++ {
			if stats[j].Count > stats[i].Count {
				stats[i], stats[j] = stats[j], stats[i]
			}
		}
	}

	resp := &AgentToolStatsResponse{
		TotalAgentCount: totalCount,
		UniqueTools:     len(stats),
		ToolStats:       stats,
	}

	setStatsToCache(cacheKey, resp)
	return resp, nil
}
