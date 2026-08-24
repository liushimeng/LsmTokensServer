package api

// 协议转换分析器 Web API（迁移自旧工程 server_web_manager_protocol_converter.go /
// server_web_user_protocol_converter.go 的 JSON 接口部分；HTML 页面由 ClientWeb SPA 实现）。
// 管理端：Status/Toggle/Test/Records/RecordDetail/Users/Mapping 共 7 条；
// 用户端：Status/Test/Records/RecordDetail/Mapping 共 5 条（无 Toggle/Users）。

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/logger"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/protocol"
)

// ProtocolConvertAnalyzerEnabled 协议转换分析器全局开关（默认关闭）
var ProtocolConvertAnalyzerEnabled = false

// protocolConvertAnalyzerStatusInterface 获取协议转换分析器状态
func protocolConvertAnalyzerStatusInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"enabled": ProtocolConvertAnalyzerEnabled,
	})
}

// protocolConvertAnalyzerToggleInterface 切换协议转换分析器开关（仅管理端）
func protocolConvertAnalyzerToggleInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	ProtocolConvertAnalyzerEnabled = req.Enabled
	logger.Printf("[PROTOCOL_CONVERTER] Status changed to: enabled=%v", ProtocolConvertAnalyzerEnabled)

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"enabled": ProtocolConvertAnalyzerEnabled,
	})
}

// protocolConvertAnalyzerTestInterface 协议转换测试接口（核心转换入口，管理端/用户端复用）
func protocolConvertAnalyzerTestInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	if r.Method != http.MethodPost {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	var req protocol.ProtocolConvertAnalyzerTestRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error":"Invalid request body"}`, http.StatusBadRequest)
		return
	}

	response, err := protocol.ConvertProtocolAnalyzerInput(req)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(response)
}

// protocolConvertAnalyzerMappingInterface 获取协议字段映射表（管理端/用户端复用）
func protocolConvertAnalyzerMappingInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)
	_ = json.NewEncoder(w).Encode(BuildProtocolConvertAnalyzerMapping())
}

// BuildProtocolConvertAnalyzerMapping 构造协议转换知识库，供管理员端和用户端复用。
// 基于真实协议数据（AnthropicAnalysis.md / OpenAIAnalysis.md）构建完整映射表。
func BuildProtocolConvertAnalyzerMapping() map[string]interface{} {
	return map[string]interface{}{
		"request_fields": []map[string]interface{}{
			{"openai": "model", "anthropic": "model", "type": "string", "note": "模型名称"},
			{"openai": "messages", "anthropic": "messages", "type": "array", "note": "消息数组"},
			{"openai": "max_tokens", "anthropic": "max_tokens", "type": "int", "note": "最大token数"},
			{"openai": "max_completion_tokens", "anthropic": "max_tokens", "type": "int", "note": "OpenAI新参数，映射到max_tokens"},
			{"openai": "stream", "anthropic": "stream", "type": "bool", "note": "是否流式"},
			{"openai": "stream_options", "anthropic": "-", "type": "object", "note": "OpenAI独有：include_usage等，Anthropic不支持"},
			{"openai": "temperature", "anthropic": "temperature", "type": "float", "note": "温度参数"},
			{"openai": "top_p", "anthropic": "top_p", "type": "float", "note": "核采样"},
			{"openai": "tools", "anthropic": "tools", "type": "array", "note": "工具定义"},
			{"openai": "tool_choice", "anthropic": "tool_choice", "type": "object/string", "note": "工具选择策略"},
			{"openai": "messages[].role=system", "anthropic": "system", "type": "string/array", "note": "OpenAI在messages中，Anthropic为独立参数（支持数组+缓存控制）"},
			{"openai": "presence_penalty", "anthropic": "-", "type": "float", "note": "Anthropic不支持"},
			{"openai": "frequency_penalty", "anthropic": "-", "type": "float", "note": "Anthropic不支持"},
			{"openai": "response_format", "anthropic": "-", "type": "object", "note": "Anthropic不支持"},
			{"openai": "seed", "anthropic": "-", "type": "int", "note": "Anthropic不支持"},
			{"openai": "stop", "anthropic": "stop_sequences", "type": "string/array", "note": "OpenAI用stop，Anthropic用stop_sequences"},
			{"openai": "user", "anthropic": "metadata.user_id", "type": "string", "note": "OpenAI直接传user，Anthropic放metadata中"},
			{"openai": "-", "anthropic": "system", "type": "array/string", "note": "Anthropic独立system参数（支持数组+缓存控制）"},
			{"openai": "-", "anthropic": "top_k", "type": "int", "note": "OpenAI不支持"},
			{"openai": "-", "anthropic": "thinking", "type": "object", "note": "OpenAI不支持"},
			{"openai": "-", "anthropic": "metadata", "type": "object", "note": "OpenAI不支持"},
			{"openai": "-", "anthropic": "output_config", "type": "object", "note": "OpenAI不支持（effort配置）"},
		},
		"response_fields": []map[string]interface{}{
			{"openai": "id", "anthropic": "id", "type": "string", "note": "消息ID"},
			{"openai": "object", "anthropic": "type", "type": "string", "note": "对象类型"},
			{"openai": "choices[0].message.role", "anthropic": "role", "type": "string", "note": "角色"},
			{"openai": "choices[0].message.content", "anthropic": "content[0].text", "type": "string", "note": "文本内容"},
			{"openai": "choices[0].message.tool_calls", "anthropic": "content[].tool_use", "type": "array", "note": "工具调用"},
			{"openai": "choices[0].finish_reason", "anthropic": "stop_reason", "type": "string", "note": "停止原因"},
			{"openai": "usage.prompt_tokens", "anthropic": "usage.input_tokens", "type": "int", "note": "输入token数"},
			{"openai": "usage.completion_tokens", "anthropic": "usage.output_tokens", "type": "int", "note": "输出token数"},
			{"openai": "usage.total_tokens", "anthropic": "-", "type": "int", "note": "Anthropic不直接提供total_tokens"},
			{"openai": "-", "anthropic": "stop_sequence", "type": "string", "note": "OpenAI不支持"},
			{"openai": "-", "anthropic": "content[].thinking", "type": "string", "note": "Anthropic thinking内容块，OpenAI不支持"},
			{"openai": "-", "anthropic": "content[].tool_result", "type": "object", "note": "Anthropic工具结果块，OpenAI用role=tool消息"},
		},
		"role_mapping": []map[string]interface{}{
			{"openai": "user", "anthropic": "user", "note": "用户消息"},
			{"openai": "assistant", "anthropic": "assistant", "note": "助手消息"},
			{"openai": "system", "anthropic": "system(独立参数)", "note": "系统提示"},
			{"openai": "tool", "anthropic": "user(tool_result)", "note": "工具结果——OpenAI独立role，Anthropic作为user消息的内容块"},
			{"openai": "developer", "anthropic": "system(独立参数)", "note": "开发者消息"},
		},
		"finish_reason_mapping": []map[string]interface{}{
			{"openai": "stop", "anthropic": "end_turn", "note": "正常结束"},
			{"openai": "length", "anthropic": "max_tokens", "note": "token限制"},
			{"openai": "tool_calls", "anthropic": "tool_use", "note": "工具调用"},
			{"openai": "content_filter", "anthropic": "-", "note": "Anthropic无对应"},
		},
		"request_header_fields": []map[string]interface{}{
			{"openai": "Authorization: Bearer sk-...", "anthropic": "x-api-key: sk-ant-...", "type": "secret", "note": "实验性页面透传真实密钥和敏感头，便于分析协议转换的真实性"},
			{"openai": "Content-Type", "anthropic": "Content-Type", "type": "header", "note": "通常为 application/json"},
			{"openai": "OpenAI-Beta", "anthropic": "Anthropic-Beta", "type": "header", "note": "Beta 功能 header 无完全自动等价，仅提示兼容风险"},
			{"openai": "-", "anthropic": "Anthropic-Version: 2023-06-01", "type": "required", "note": "Anthropic Messages API 必需版本 header"},
		},
		"response_header_fields": []map[string]interface{}{
			{"openai": "Content-Type", "anthropic": "Content-Type", "type": "header", "note": "JSON 或 text/event-stream"},
			{"openai": "x-ratelimit-*", "anthropic": "anthropic-ratelimit-*", "type": "header", "note": "限流 header 语义相近但名称不同"},
			{"openai": "openai-processing-ms", "anthropic": "-", "type": "provider", "note": "Provider 内部 header，转换时提示并丢弃"},
		},
		"tool_use_mapping": []map[string]interface{}{
			{"openai": "tools[].type=function", "anthropic": "tools[].name", "note": "工具定义——OpenAI用type+function包装，Anthropic直接用name+input_schema"},
			{"openai": "tools[].function.name", "anthropic": "tools[].name", "note": "工具名称"},
			{"openai": "tools[].function.description", "anthropic": "tools[].description", "note": "工具描述"},
			{"openai": "tools[].function.parameters", "anthropic": "tools[].input_schema", "note": "工具入参 JSON Schema"},
			{"openai": "assistant.tool_calls[].id", "anthropic": "content[].tool_use.id", "note": "工具调用ID"},
			{"openai": "assistant.tool_calls[].type=function", "anthropic": "content[].tool_use.type=tool_use", "note": "工具调用类型"},
			{"openai": "assistant.tool_calls[].function.name", "anthropic": "content[].tool_use.name", "note": "调用的工具名称"},
			{"openai": "assistant.tool_calls[].function.arguments(JSON字符串)", "anthropic": "content[].tool_use.input(JSON对象)", "note": "工具参数——OpenAI是JSON字符串，Anthropic是JSON对象"},
			{"openai": "role=tool + tool_call_id", "anthropic": "user content[].type=tool_result + tool_use_id", "note": "工具结果回传"},
		},
		"sse_event_mapping": []map[string]interface{}{
			{"openai": "data: {choices[].delta.content}", "anthropic": "event: content_block_delta / text_delta", "note": "文本增量"},
			{"openai": "data: [DONE]", "anthropic": "event: message_stop", "note": "流结束"},
			{"openai": "choices[].delta.tool_calls[].function.arguments", "anthropic": "input_json_delta.partial_json", "note": "工具参数流式增量"},
			{"openai": "finish_reason", "anthropic": "message_delta.stop_reason", "note": "停止原因"},
			{"openai": "choices[].delta.role", "anthropic": "message_start.message.role", "note": "角色信息"},
			{"openai": "usage", "anthropic": "message_delta.usage / message_start.usage", "note": "Token使用量"},
		},
		"content_block_mapping": []map[string]interface{}{
			{"openai": "content: string", "anthropic": "content[].type=text", "note": "纯文本内容"},
			{"openai": "content: array[text]", "anthropic": "content[].type=text", "note": "多模态文本"},
			{"openai": "content: array[image_url]", "anthropic": "content[].type=image", "note": "图片内容——结构不同"},
			{"openai": "tool_calls", "anthropic": "content[].type=tool_use", "note": "工具调用"},
			{"openai": "role=tool", "anthropic": "content[].type=tool_result", "note": "工具结果"},
			{"openai": "-", "anthropic": "content[].cache_control", "type": "object", "note": "Anthropic缓存控制（ephemeral），OpenAI不支持"},
			{"openai": "-", "anthropic": "content[].type=thinking", "type": "string", "note": "Anthropic thinking内容块，OpenAI不支持"},
		},
	}
}

// protocolConvertAnalyzerRecordsInterface 获取全平台记录列表（管理端）
// 支持三种查询模式：
//  1. 全平台跨分表查询（user_name 为空且 model_name 为空）
//  2. 单分表精确查询（user_name 和 model_name 均提供）
//  3. 按模型跨分表查询（仅提供 model_name，遍历所有分表）
func protocolConvertAnalyzerRecordsInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	// 解析分页参数
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 10
	}
	protocolType, _ := strconv.Atoi(r.URL.Query().Get("protocol_type"))
	userName := r.URL.Query().Get("user_name")
	modelName := r.URL.Query().Get("model_name")
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	var records []modelsdb.ProtocolConvertAnalyzerRecordItem
	var total int64
	var err error

	if userName != "" && modelName != "" {
		// 模式2：按用户+模型精确查询单张分表（数据完整，性能最优）
		records, total, err = modelsdb.QueryProtocolConvertAnalyzerRecordsByModel(subTableNum(), page, pageSize, protocolType, userName, modelName, days)
	} else {
		// 模式1/3：跨分表查询（仅 user_name / 仅 model_name / 两者皆空）
		records, total, err = modelsdb.QueryProtocolConvertAnalyzerRecords(subTableNum(), page, pageSize, protocolType, userName, modelName, days)
	}

	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"records":       records,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"protocol_type": protocolType,
		"user_name":     userName,
		"model_name":    modelName,
		"days":          days,
	})
}

// protocolConvertAnalyzerRecordDetailInterface 获取单条记录大字段详情（管理端）
func protocolConvertAnalyzerRecordDetailInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}
	id, _ := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	userName := r.URL.Query().Get("user_name")
	modelName := r.URL.Query().Get("model_name")
	detail, err := modelsdb.GetProtocolConvertAnalyzerRecordDetailByID(userName, modelName, subTableNum(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"detail":  detail,
	})
}

// protocolConvertAnalyzerUsersInterface 获取用户列表（用于前端下拉选择，仅管理端）
func protocolConvertAnalyzerUsersInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	users, err := modelsdb.GetAllUsers(0, 0)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	// 精简用户信息（只返回用户名和模型列表）
	var userList []map[string]interface{}
	for _, u := range users {
		models, _ := modelsdb.GetUserModelsByUserID(u.ID)
		var modelNames []string
		for _, m := range models {
			modelNames = append(modelNames, m.ModelName)
		}
		userList = append(userList, map[string]interface{}{
			"id":          u.ID,
			"user_name":   u.UserName,
			"model_names": modelNames,
		})
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"users":   userList,
	})
}

// userProtocolConvertAnalyzerRecordsInterface 用户端获取记录列表
// 自动按当前登录用户的用户名过滤
func userProtocolConvertAnalyzerRecordsInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	claims := getUserToken(r)
	if claims.UserID == 0 {
		http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
		return
	}

	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("page_size"))
	if pageSize < 1 {
		pageSize = 10
	}
	protocolType, _ := strconv.Atoi(r.URL.Query().Get("protocol_type"))
	modelName := r.URL.Query().Get("model_name")
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))

	var records []modelsdb.ProtocolConvertAnalyzerRecordItem
	var total int64
	var err error

	if modelName != "" {
		// 按用户+模型精确查询单张分表（数据完整，性能最优）
		records, total, err = modelsdb.QueryProtocolConvertAnalyzerRecordsByModel(subTableNum(), page, pageSize, protocolType, claims.UserName, modelName, days)
	} else {
		// 用户端强制按当前登录用户过滤（跨分表查询）
		records, total, err = modelsdb.QueryProtocolConvertAnalyzerRecords(subTableNum(), page, pageSize, protocolType, claims.UserName, "", days)
	}
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}

	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success":       true,
		"records":       records,
		"total":         total,
		"page":          page,
		"page_size":     pageSize,
		"protocol_type": protocolType,
		"user_name":     claims.UserName,
		"model_name":    modelName,
		"days":          days,
	})
}

// userProtocolConvertAnalyzerRecordDetailInterface 用户端获取记录详情（强制本人数据）
func userProtocolConvertAnalyzerRecordDetailInterface(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	setNoCacheHeaders(w)

	claims := getUserToken(r)
	if claims.UserID == 0 {
		http.Error(w, `{"error":"未登录"}`, http.StatusUnauthorized)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"Method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	id, _ := strconv.ParseUint(r.URL.Query().Get("id"), 10, 64)
	modelName := r.URL.Query().Get("model_name")
	detail, err := modelsdb.GetProtocolConvertAnalyzerRecordDetailByID(claims.UserName, modelName, subTableNum(), id)
	if err != nil {
		http.Error(w, `{"error":"`+err.Error()+`"}`, http.StatusBadRequest)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"detail":  detail,
	})
}

// subTableNum 从全局配置读取分表数量
func subTableNum() int {
	if config.G != nil && config.G.DBMysqlSubTableNumber > 0 {
		return config.G.DBMysqlSubTableNumber
	}
	return config.DEFAULT_SUB_TABLE_NUM
}
