package protocol

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ============================================================================
// 协议转换率计算（增强版）
// ============================================================================

// ConversionMetrics 协议转换率统计结果
// 映射比率计算逻辑：
//   - 字段覆盖率 (FieldCoverageRate) = 输入字段中被目标协议支持的占比
//   - 语义映射率 (SemanticMappingRate) = 考虑字段语义等价的综合映射成功率
//   - 综合转换率 (ConversionRate) = 字段覆盖率与语义映射率的加权平均
//   - 结构转换成功率 (StructureSuccessRate) = JSON解析成功 + 实际转换成功 + 输出可序列化的综合成功率
//   - 字段转换率 (FieldConversionRate) = 实际被转换的字段值占所有输入字段值的比例
//
// 所有比率均为 0.0 ~ 1.0 的浮点数，前端显示为百分比
type ConversionMetrics struct {
	TotalInputFields     int      `json:"total_input_fields"`        // 输入数据中的总字段数（递归统计所有 JSON 路径）
	ConvertedFields      int      `json:"converted_fields"`          // 已成功映射的字段数
	UnmappedFields       []string `json:"unmapped_fields"`           // 未被目标协议采纳的字段路径列表（去重后的顶级字段名）
	TargetExtraFields    []string `json:"target_extra_fields"`       // 目标协议支持但输入未提供的字段
	ConversionRate       float64  `json:"conversion_rate"`           // 综合转换率 (0.0 - 1.0)，前端显示为百分比
	FieldCoverageRate    float64  `json:"field_coverage_rate"`       // 字段覆盖率：输入字段中被目标协议支持的比例
	SemanticMappingRate  float64  `json:"semantic_mapping_rate"`     // 语义映射率：考虑字段语义等价性的映射成功率
	StructureSuccessRate float64  `json:"structure_success_rate"`    // 结构转换成功率：JSON解析+转换+序列化整体成功率
	FieldConversionRate  float64  `json:"field_conversion_rate"`     // 字段转换率：实际被转换的字段值占所有输入字段值的比例
	InputTopLevelCount   int      `json:"input_top_level_count"`     // 输入数据顶级字段数量
	MappedTopLevelCount  int      `json:"mapped_top_level_count"`    // 已映射的顶级字段数量
	ParsedOK             bool     `json:"parsed_ok"`                 // 输入 JSON 是否解析成功
	ConvertedOK          bool     `json:"converted_ok"`              // 转换是否成功（无错误）
	OutputValid          bool     `json:"output_valid"`              // 输出是否可序列化为有效 JSON
	ParseError           string   `json:"parse_error,omitempty"`     // 解析错误信息
	ConvertError         string   `json:"convert_error,omitempty"`   // 转换错误信息
	SerializeError       string   `json:"serialize_error,omitempty"` // 序列化错误信息
}

// CalculateConversionMetrics 计算 JSON 数据的协议转换率（增强版，包含结构转换成功率和字段转换率）
// direction: "o2a" = OpenAI→Anthropic, "a2o" = Anthropic→OpenAI
func CalculateConversionMetrics(inputJSON []byte, direction string) (*ConversionMetrics, error) {
	return CalculateConversionMetricsForSection(inputJSON, direction, ProtocolAnalyzerSectionRequestBody)
}

// CalculateConversionMetricsForSection 按请求/响应 body 场景计算协议转换率（增强版）。
// 新增结构转换成功率和字段转换率计算：
//   - StructureSuccessRate: 综合 JSON解析+转换+序列化 的成功率
//   - FieldConversionRate: 输出中实际包含的转换后字段数 / 输入中应被转换的字段数
func CalculateConversionMetricsForSection(inputJSON []byte, direction, section string) (*ConversionMetrics, error) {
	// 先计算基础指标（字段覆盖率、语义映射率等）
	metrics := calculateBasicMetrics(inputJSON, direction, section)

	// 再计算结构转换成功率和字段转换率（需要实际执行转换）
	metrics.calculateStructureAndFieldRates(inputJSON, direction, section)

	return metrics, nil
}

// calculateBasicMetrics 计算基础指标（字段覆盖率、语义映射率、综合转换率）
// 不包含实际转换执行，避免递归调用
func calculateBasicMetrics(inputJSON []byte, direction, section string) *ConversionMetrics {
	metrics := &ConversionMetrics{
		UnmappedFields:    make([]string, 0),
		TargetExtraFields: make([]string, 0),
		ParsedOK:          true,
		ConvertedOK:       true,
		OutputValid:       true,
	}

	var inputMap map[string]interface{}
	if err := json.Unmarshal(inputJSON, &inputMap); err != nil {
		metrics.ParsedOK = false
		metrics.ParseError = err.Error()
		// 即使解析失败也继续计算基础指标
		inputMap = make(map[string]interface{})
	}

	// 定义各协议支持的关键字段知识库（包含嵌套路径）
	openAIRequestFields := map[string]bool{
		"model": true, "messages": true, "temperature": true, "top_p": true,
		"max_tokens": true, "max_completion_tokens": true, "tools": true,
		"tool_choice": true, "stream": true, "stream_options": true,
		"stop": true, "presence_penalty": true, "frequency_penalty": true,
		"response_format": true, "seed": true, "user": true,
		"logprobs": true, "top_logprobs": true, "n": true,
	}
	anthropicRequestFields := map[string]bool{
		"model": true, "messages": true, "system": true, "max_tokens": true,
		"temperature": true, "top_p": true, "top_k": true, "tools": true,
		"tool_choice": true, "stream": true, "metadata": true,
		"stop_sequences": true, "thinking": true, "output_config": true,
	}
	openAIResponseFields := map[string]bool{
		"id": true, "object": true, "created": true, "model": true,
		"choices": true, "usage": true,
	}
	anthropicResponseFields := map[string]bool{
		"id": true, "type": true, "role": true, "model": true,
		"content": true, "stop_reason": true, "stop_sequence": true, "usage": true,
	}

	var dstSchema map[string]bool
	if section == ProtocolAnalyzerSectionResponseBody {
		if direction == "o2a" {
			dstSchema = anthropicResponseFields
		} else {
			dstSchema = openAIResponseFields
		}
	} else if direction == "o2a" {
		dstSchema = anthropicRequestFields
	} else {
		dstSchema = openAIRequestFields
	}

	// 提取输入数据的顶级字段
	inputTopLevel := make(map[string]bool)
	for key := range inputMap {
		inputTopLevel[key] = true
	}
	metrics.InputTopLevelCount = len(inputTopLevel)

	// 统计顶级字段映射情况
	for key := range inputTopLevel {
		if dstSchema[key] {
			metrics.MappedTopLevelCount++
		} else {
			metrics.UnmappedFields = append(metrics.UnmappedFields, key)
		}
	}

	// 递归收集所有字段路径（用于字段覆盖率计算）
	inputPaths := collectJSONPaths(inputMap, "")

	// 统计输入字段
	for path := range inputPaths {
		metrics.TotalInputFields++
		// 检查顶级字段是否在目标协议中
		topKey := path
		if idx := strings.Index(path, "."); idx > 0 {
			topKey = path[:idx]
		}
		if idx := strings.Index(path, "["); idx > 0 {
			topKey = path[:idx]
		}
		if dstSchema[topKey] {
			metrics.ConvertedFields++
		}
	}

	// 统计目标协议有但输入没有的字段
	for key := range dstSchema {
		if !inputTopLevel[key] {
			metrics.TargetExtraFields = append(metrics.TargetExtraFields, key)
		}
	}

	// 计算字段覆盖率：输入顶级字段中被目标协议支持的比例
	if metrics.InputTopLevelCount > 0 {
		metrics.FieldCoverageRate = float64(metrics.MappedTopLevelCount) / float64(metrics.InputTopLevelCount)
	} else {
		metrics.FieldCoverageRate = 1.0
	}

	// 计算语义映射率：考虑所有递归字段的映射成功率
	if metrics.TotalInputFields > 0 {
		metrics.SemanticMappingRate = float64(metrics.ConvertedFields) / float64(metrics.TotalInputFields)
	} else {
		metrics.SemanticMappingRate = 1.0
	}

	// 综合转换率 = 字段覆盖率 * 0.6 + 语义映射率 * 0.4
	// 字段覆盖率更重要（顶级字段决定协议兼容性）
	metrics.ConversionRate = metrics.FieldCoverageRate*0.6 + metrics.SemanticMappingRate*0.4

	return metrics
}

// calculateStructureAndFieldRates 计算结构转换成功率和字段转换率
// 通过实际执行转换来评估：
//   - StructureSuccessRate: JSON解析成功(0.3) + 转换成功(0.4) + 输出可序列化(0.3) 的加权
//   - FieldConversionRate: 输出中实际包含的转换后字段数 / 输入中应被转换的字段数
func (m *ConversionMetrics) calculateStructureAndFieldRates(inputJSON []byte, direction, section string) {
	// 如果输入解析失败，结构转换成功率直接为 0
	if !m.ParsedOK {
		m.StructureSuccessRate = 0.0
		m.FieldConversionRate = 0.0
		return
	}

	// 尝试实际转换
	var output interface{}
	var convertErr error

	switch section {
	case ProtocolAnalyzerSectionRequestBody:
		output, convertErr = convertRequestBodyOnly(inputJSON, direction)
	case ProtocolAnalyzerSectionResponseBody:
		output, convertErr = convertResponseBodyOnly(inputJSON, direction)
	default:
		// headers 不计算结构转换率
		m.StructureSuccessRate = 1.0
		m.FieldConversionRate = m.FieldCoverageRate
		return
	}

	if convertErr != nil {
		m.ConvertedOK = false
		m.ConvertError = convertErr.Error()
		m.StructureSuccessRate = 0.3 // 只有解析成功
		m.FieldConversionRate = 0.0
		return
	}

	// 检查输出是否可序列化
	outputJSON, serializeErr := json.Marshal(output)
	if serializeErr != nil {
		m.OutputValid = false
		m.SerializeError = serializeErr.Error()
		m.StructureSuccessRate = 0.7 // 解析+转换成功，但序列化失败
		m.FieldConversionRate = 0.0
		return
	}

	// 全部成功：解析(0.3) + 转换(0.4) + 序列化(0.3) = 1.0
	m.StructureSuccessRate = 1.0

	// 计算字段转换率：比较输出中的字段与输入中应被转换的字段
	var outputMap map[string]interface{}
	if err := json.Unmarshal(outputJSON, &outputMap); err == nil {
		m.FieldConversionRate = m.calculateFieldConversionRate(outputMap, direction, section)
	} else {
		m.FieldConversionRate = m.FieldCoverageRate
	}
}

// convertRequestBodyOnly 仅执行请求体转换，不计算指标（避免递归）
func convertRequestBodyOnly(input []byte, direction string) (interface{}, error) {
	switch direction {
	case "o2a":
		var openAIReq OpenAIChatCompletionRequest
		if err := json.Unmarshal(input, &openAIReq); err != nil {
			return nil, fmt.Errorf("failed to parse OpenAI request: %w", err)
		}
		return ConvertOpenAIToAnthropicRequest(&openAIReq)
	case "a2o":
		var anthropicReq AnthropicMessagesRequest
		if err := json.Unmarshal(input, &anthropicReq); err != nil {
			return nil, fmt.Errorf("failed to parse Anthropic request: %w", err)
		}
		return ConvertAnthropicToOpenAIRequest(&anthropicReq)
	default:
		return nil, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
}

// convertResponseBodyOnly 仅执行响应体转换，不计算指标（避免递归）
func convertResponseBodyOnly(input []byte, direction string) (interface{}, error) {
	switch direction {
	case "o2a":
		var openAIResp OpenAIChatCompletionResponse
		if err := json.Unmarshal(input, &openAIResp); err != nil {
			return nil, fmt.Errorf("failed to parse OpenAI response: %w", err)
		}
		return ConvertOpenAIToAnthropicResponse(&openAIResp)
	case "a2o":
		var anthropicResp AnthropicMessagesResponse
		if err := json.Unmarshal(input, &anthropicResp); err != nil {
			return nil, fmt.Errorf("failed to parse Anthropic response: %w", err)
		}
		return ConvertAnthropicToOpenAIResponse(&anthropicResp)
	default:
		return nil, fmt.Errorf("invalid direction, use 'o2a' or 'a2o'")
	}
}

// calculateFieldConversionRate 计算字段转换率
// 比较输出中的实际字段与输入中应被转换的字段
func (m *ConversionMetrics) calculateFieldConversionRate(outputMap map[string]interface{}, direction, section string) float64 {
	if m.MappedTopLevelCount == 0 {
		return 1.0
	}

	// 定义各协议的标准字段集合（用于判断输出是否包含预期字段）
	openAIRequestExpected := map[string]bool{
		"model": true, "messages": true, "stream": true,
		"max_tokens": true, "max_completion_tokens": true,
		"temperature": true, "top_p": true, "tools": true,
		"tool_choice": true, "stop": true,
	}
	anthropicRequestExpected := map[string]bool{
		"model": true, "messages": true, "max_tokens": true,
		"stream": true, "temperature": true, "top_p": true,
		"tools": true, "tool_choice": true, "system": true,
		"metadata": true, "stop_sequences": true,
	}
	openAIResponseExpected := map[string]bool{
		"id": true, "object": true, "created": true,
		"model": true, "choices": true, "usage": true,
	}
	anthropicResponseExpected := map[string]bool{
		"id": true, "type": true, "role": true,
		"model": true, "content": true, "usage": true,
		"stop_reason": true,
	}

	var expectedSchema map[string]bool
	if section == ProtocolAnalyzerSectionResponseBody {
		if direction == "o2a" {
			expectedSchema = anthropicResponseExpected
		} else {
			expectedSchema = openAIResponseExpected
		}
	} else if direction == "o2a" {
		expectedSchema = anthropicRequestExpected
	} else {
		expectedSchema = openAIRequestExpected
	}

	// 统计输出中实际包含的、且属于预期字段的数量
	actualConvertedCount := 0
	for key := range outputMap {
		if expectedSchema[key] {
			actualConvertedCount++
		}
	}

	// 字段转换率 = 输出中实际包含的预期字段 / 输入中应被映射的顶级字段
	if m.MappedTopLevelCount > 0 {
		return float64(actualConvertedCount) / float64(m.MappedTopLevelCount)
	}
	return 1.0
}

// collectJSONPaths 递归收集 JSON 对象中的所有字段路径
func collectJSONPaths(data interface{}, prefix string) map[string]bool {
	paths := make(map[string]bool)

	switch v := data.(type) {
	case map[string]interface{}:
		for key, val := range v {
			path := key
			if prefix != "" {
				path = prefix + "." + key
			}
			paths[path] = true
			// 递归收集子字段
			subPaths := collectJSONPaths(val, path)
			for sp := range subPaths {
				paths[sp] = true
			}
		}
	case []interface{}:
		for i, item := range v {
			path := fmt.Sprintf("%s[%d]", prefix, i)
			paths[path] = true
			subPaths := collectJSONPaths(item, path)
			for sp := range subPaths {
				paths[sp] = true
			}
		}
	}

	return paths
}
