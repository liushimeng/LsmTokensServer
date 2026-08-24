package protocol

import (
	"strings"
)

// SSE 事件解析（自旧工程 server_http_ai_proxy_utils.go 提取，属协议层公共能力）

type SSEEvent struct {
	Event string
	Data  string
}

func ParseSSEEvents(text string) []SSEEvent {
	// 截断末尾不完整的多字节 UTF-8 序列（防御性）
	text = trimIncompleteTrailingUTF8(text)
	var events []SSEEvent
	lines := strings.Split(text, "\n")
	var currentEvent SSEEvent

	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if currentEvent.Data != "" || currentEvent.Event != "" {
				events = append(events, currentEvent)
				currentEvent = SSEEvent{}
			}
		} else if strings.HasPrefix(line, "event:") {
			currentEvent.Event = strings.TrimSpace(line[6:])
		} else if strings.HasPrefix(line, "data:") {
			dataPart := line[5:]
			if currentEvent.Data == "" {
				currentEvent.Data = dataPart
			} else {
				currentEvent.Data += "\n" + dataPart
			}
		}
	}

	// 流尾残留的未完成事件不丢（上游漏发末尾空行时最后一个事件仍能解析）
	if currentEvent.Data != "" || currentEvent.Event != "" {
		events = append(events, currentEvent)
	}

	return events
}

// trimIncompleteTrailingUTF8 截断末尾不完整 UTF-8 序列
func trimIncompleteTrailingUTF8(s string) string {
	n := len(s)
	if n == 0 {
		return s
	}
	// 从末尾向前找到最后一个非延续字节（即一个 rune 的起始字节或 ASCII）
	i := n - 1
	for i >= 0 && s[i]&0xC0 == 0x80 {
		i--
	}
	if i < 0 {
		// 全部是延续字节，无起始字节 → 全截断
		return ""
	}
	startByte := s[i]
	var expectedLen int
	switch {
	case startByte&0x80 == 0x00:
		expectedLen = 1 // ASCII (0xxxxxxx)
	case startByte&0xE0 == 0xC0:
		expectedLen = 2 // 110xxxxx
	case startByte&0xF0 == 0xE0:
		expectedLen = 3 // 1110xxxx
	case startByte&0xF8 == 0xF0:
		expectedLen = 4 // 11110xxx
	default:
		// 非法起始字节（单独的延续字节不应到达此处），截断
		return s[:i]
	}
	if i+expectedLen == n {
		return s // 完整
	}
	if i+expectedLen > n {
		return s[:i] // 末尾 rune 不完整，截断
	}
	return s
}
