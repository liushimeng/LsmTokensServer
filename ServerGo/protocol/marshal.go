package protocol

import (
	"encoding/json"
)

// MarshalConvertedProtocolBody 转换后 body 的 marshal（自旧工程 proxy utils 提取，协议层公共能力）
func MarshalConvertedProtocolBody(v interface{}) ([]byte, error) {
	switch data := v.(type) {
	case []byte:
		return data, nil
	case json.RawMessage:
		return data, nil
	case string:
		return []byte(data), nil
	default:
		return json.Marshal(data)
	}
}
