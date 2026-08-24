package models

// 协议转换分析器「学习记录」转换视图。
//
// 旧工程 server_web_manager_protocol_converter.go 的 BuildProtocolConvertAnalyzerRecordConversion
// 及配套类型/helper 原样迁移（仅适配包路径：protocol 转换入口与 Security 脱敏已拆分到 protocol / models）。

import (
	"encoding/json"
	"strings"

	"github.com/lishimeng/LsmTokensServer/protocol"
)

// ProtocolConvertAnalyzerSectionPair 分析器单个数据段（请求头/请求体/响应头/响应体）的
// 转换前后对照。
type ProtocolConvertAnalyzerSectionPair struct {
	Input    string   `json:"input"`
	Output   string   `json:"output"`
	Format   string   `json:"format"`
	Warnings []string `json:"warnings,omitempty"`
	Error    string   `json:"error,omitempty"`
}

// ProtocolConvertAnalyzerRecordConversion 一条「学习记录」的完整转换视图。
type ProtocolConvertAnalyzerRecordConversion struct {
	Direction       string                             `json:"direction"`
	RequestHeaders  ProtocolConvertAnalyzerSectionPair `json:"request_headers"`
	RequestBody     ProtocolConvertAnalyzerSectionPair `json:"request_body"`
	ResponseHeaders ProtocolConvertAnalyzerSectionPair `json:"response_headers"`
	ResponseBody    ProtocolConvertAnalyzerSectionPair `json:"response_body"`
}

// BuildProtocolConvertAnalyzerRecordConversion 把一条存量的原始记录（含源协议/目标协议原始报文）
// 转换为四段「转换前后」对照视图，供分析器记录详情页展示。
func BuildProtocolConvertAnalyzerRecordConversion(detail *ProtocolConvertAnalyzerRecordDetail, direction string) ProtocolConvertAnalyzerRecordConversion {
	conversion := ProtocolConvertAnalyzerRecordConversion{Direction: direction}
	requestHeaders := RedactAuthorizationBearerHeaderText(firstNonEmpty(detail.RequestSrcProtocolHeaders, detail.RequestHeaders))
	conversion.RequestHeaders = convertAnalyzerSectionPair(requestHeaders, direction, protocol.ProtocolAnalyzerSectionRequestHeaders, false)
	conversion.RequestHeaders.Output = RedactAuthorizationBearerHeaderText(conversion.RequestHeaders.Output)
	conversion.RequestBody = convertAnalyzerSectionPair(firstNonEmpty(detail.RequestSrcProtocolBody, detail.RequestBody), direction, protocol.ProtocolAnalyzerSectionRequestBody, false)
	conversion.ResponseHeaders = convertAnalyzerSectionPair(firstNonEmpty(detail.ResponseSrcProtocolHeaders, detail.ResponseHeaders), direction, protocol.ProtocolAnalyzerSectionResponseHeaders, detail.IsStream)
	conversion.ResponseBody = convertAnalyzerSectionPair(firstNonEmpty(detail.ResponseSrcProtocolBody, detail.ResponseBody), direction, protocol.ProtocolAnalyzerSectionResponseBody, detail.IsStream)
	return conversion
}

// convertAnalyzerSectionPair 对单个数据段执行协议转换，得到 Input/Output 对照。
func convertAnalyzerSectionPair(input, direction, section string, isStream bool) ProtocolConvertAnalyzerSectionPair {
	pair := ProtocolConvertAnalyzerSectionPair{Input: formatAnalyzerDisplayValue(input, section), Format: "json"}
	if strings.TrimSpace(input) == "" {
		return pair
	}
	req := protocol.ProtocolConvertAnalyzerTestRequest{Direction: direction, Section: section, Format: "json", IsStream: isStream}
	if section == protocol.ProtocolAnalyzerSectionRequestHeaders || section == protocol.ProtocolAnalyzerSectionResponseHeaders || isStream {
		req.TextInput = input
		req.Input = json.RawMessage(`{}`)
		if isStream && section == protocol.ProtocolAnalyzerSectionResponseBody {
			req.Format = "sse"
		}
	} else {
		req.Input = json.RawMessage(input)
	}
	resp, err := protocol.ConvertProtocolAnalyzerInput(req)
	if err != nil {
		pair.Error = err.Error()
		return pair
	}
	pair.Warnings = resp.Warnings
	pair.Format = resp.Format
	if resp.Format == "headers" {
		pair.Output = resp.Text
	} else if len(resp.Output) > 0 {
		pair.Output = formatAnalyzerDisplayValue(string(resp.Output), section)
	}
	return pair
}

// formatAnalyzerDisplayValue 对 JSON 段做缩进美化（headers 段原样返回）。
func formatAnalyzerDisplayValue(input, section string) string {
	if strings.TrimSpace(input) == "" || strings.Contains(section, "headers") {
		return input
	}
	var value interface{}
	if err := json.Unmarshal([]byte(input), &value); err != nil {
		return input
	}
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return input
	}
	return string(out)
}

// firstNonEmpty 返回第一个非空字符串。
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

// ProtocolAnalyzerDirectionFromProtocolType 根据协议类型得到转换方向（1=Anthropic→a2o，其余→o2a）。
func ProtocolAnalyzerDirectionFromProtocolType(protocolType int) string {
	if protocolType == 1 {
		return "a2o"
	}
	return "o2a"
}
