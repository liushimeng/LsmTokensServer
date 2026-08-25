package models

import (
	"net"
	"net/http"
	"strings"
)

// ============================================================================
// 算法策略类型常量
// ============================================================================
// 指定型 (1): 始终使用 DstEndPointIDList 的第一个 ID。
//
//	用户通过 Web 界面调整列表顺序来控制优先级。最简单、最可控的策略。
//
// 稳定型 (2): 遇到服务端错误或流控错误时，自动切换到列表中的下一个源站。
//   - 触发切换的错误码: 402 (Payment Required, 源站账号余额不足，换站可恢复),
//     429 (Too Many Requests), 500 (Internal Server Error),
//     502 (Bad Gateway), 503 (Service Unavailable), 504 (Gateway Timeout),
//     以及底层网络连接错误（超时、拒绝连接等）。
//   - 不触发的错误: 400 (Bad Request) 等客户端错误，因为换源站后同样的请求大概率仍会失败。
//   - 目标源站列表做滚动处理：连续 3 次 API 接口出错后，列表第 0 个源站滚动到末尾，
//     新的第 0 个成为当前生效源站。滚动直接修改 database.DB 中的 dst_endpoint_id_list /
//     dst_endpoint_algorithm_type_list 并同步内存缓存，重启后顺序保留。
//   - 故障转移仅在接收到目标源站首字节（TTFB）之前生效。
//     一旦开始透传流式数据（SSE），如果中途断开，代理无法再进行无感切换，
//     只能断开客户端连接，由 Claude/Kilo Code 客户端自己发起重试请求。
//   - 当前队首源站连续失败计数仅保存在内存中，重启清零是合理的。
//
// 经济型 (3): Session 级别负载均衡（实时源站列表消费）。根据 Anthropic/OpenAI 请求中
//
//	metadata.user_id 内的 session_id，把不同会话分配到路由配置的各个目标源站。
//	新 session 从实时源站列表（livePool）随机取一个、分配后弹出；livePool 空时
//	按路由配置 DstEndPointIDs 重新洗牌填充。支持 Anthropic 和 OpenAI 协议。
//	Web 增/删源站或连续 3 次失败自动移除时同步 livePool、session 队列与失败计数。
//	无 session_id 时退化为返回 DstEndPointIDs[0]，不消费 livePool。
//
// 智能型 (4): 开发中。计划根据历史成功率、延迟、价格等多维度综合评分动态选择最优源站。
//
//	当前使用默认算法（指定型逻辑）。
//
// ============================================================================
const (
	AlgorithmStrategyType_FirstID     = 1 // 指定型：始终使用列表第一个
	AlgorithmStrategyType_Stable      = 2 // 稳定型：故障自动切换
	AlgorithmStrategyType_Economic    = 3 // 经济型：Session 负载均衡（仅 Anthropic）
	AlgorithmStrategyType_Intelligent = 4 // 智能型：开发中
)

// GetAlgorithmName 获取算法名称
func GetAlgorithmName(t int) string {
	switch t {
	case AlgorithmStrategyType_FirstID:
		return "指定型"
	case AlgorithmStrategyType_Stable:
		return "稳定型"
	case AlgorithmStrategyType_Economic:
		return "经济型"
	case AlgorithmStrategyType_Intelligent:
		return "智能型"
	default:
		return "未知"
	}
}

// GetAlgorithmDescription 获取算法说明（用于 Web 界面展示）
func GetAlgorithmDescription(t int) string {
	switch t {
	case AlgorithmStrategyType_FirstID:
		return "始终使用目标源站列表中的第一个。您可以通过调整列表顺序来控制优先级。"
	case AlgorithmStrategyType_Stable:
		return "遇到服务端错误（402/429/500/502/503/504）或连接超时，自动切换到下一个源站。目标源站列表做滚动处理，连续 3 次 API 接口出错后切换到下一个模型。"
	case AlgorithmStrategyType_Economic:
		return "Session 级别负载均衡（实时源站列表消费）：根据 Anthropic/OpenAI 请求中的 session_id 分配会话到源站，新 session 从实时源站列表中随机取一个并弹出；列表空时按路由配置重新洗牌填充。Web 增/删源站或连续 3 次失败自动移除时同步更新实时列表与 session 队列。支持 Anthropic 和 OpenAI 协议。"
	case AlgorithmStrategyType_Intelligent:
		return "根据历史成功率、延迟、价格等多维度评分动态选择最优源站。（开发中，当前使用指定型逻辑）"
	default:
		return "未知算法类型"
	}
}

// IsAlgorithmImplemented 判断算法是否已实现（智能型暂未实现）
func IsAlgorithmImplemented(t int) bool {
	return t == AlgorithmStrategyType_FirstID || t == AlgorithmStrategyType_Stable || t == AlgorithmStrategyType_Economic
}

// IsFailoverError 判断是否触发故障转移的错误。
// 触发条件：服务端错误或流控错误，以及底层网络连接错误。
// 402（余额不足）属于源站账号级错误，切换到其它源站即可恢复，因此也触发。
// 不触发：400（客户端传参错误，换源站后同样的请求大概率仍会失败）
func IsFailoverError(statusCode int, err error) bool {
	if err != nil {
		// 网络连接错误（超时、拒绝连接、重置等）
		if netErr, ok := err.(net.Error); ok {
			// 包含超时和临时错误
			_ = netErr
			return true
		}
		// 其他网络相关错误（如 "connection refused", "no such host" 等）
		errStr := err.Error()
		if strings.Contains(errStr, "connection") ||
			strings.Contains(errStr, "timeout") ||
			strings.Contains(errStr, "refused") ||
			strings.Contains(errStr, "reset") ||
			strings.Contains(errStr, "broken pipe") ||
			strings.Contains(errStr, "no such host") {
			return true
		}
		return false
	}
	switch statusCode {
	case http.StatusPaymentRequired, // 402：源站账号余额不足，换站可恢复
		http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		http.StatusGatewayTimeout:      // 504
		return true
	default:
		return false
	}
}

// AlgorithmSelector 算法选择器接口
type AlgorithmSelector interface {
	// Select 根据算法策略选择目标源站 ID
	// route: 路由配置（含已预解析的 DstEndPointIDs）
	// 返回选中的 endpoint ID 和是否成功
	Select(route *CachedAIRoute) (uint64, bool)
}

// GetAlgorithmSelector 根据算法类型返回对应的选择器
func GetAlgorithmSelector(strategyType int) AlgorithmSelector {
	switch strategyType {
	case AlgorithmStrategyType_Stable:
		return &StableAlgorithmSelector{}
	case AlgorithmStrategyType_Economic:
		return &EconomicAlgorithmSelector{}
	case AlgorithmStrategyType_FirstID,
		AlgorithmStrategyType_Intelligent:
		fallthrough
	default:
		return &FirstIDAlgorithmSelector{}
	}
}

// FirstIDAlgorithmSelector 指定型算法：返回第一个状态为启用的源站 ID
// 若全部禁用，返回 (0, false)
type FirstIDAlgorithmSelector struct{}

func (s *FirstIDAlgorithmSelector) Select(route *CachedAIRoute) (uint64, bool) {
	if route == nil || len(route.DstEndPointIDs) == 0 {
		return 0, false
	}
	for i, id := range route.DstEndPointIDs {
		if i < len(route.DstEndPointIDStatuses) && route.DstEndPointIDStatuses[i] == 1 {
			return id, true
		}
		// 兼容：状态列表缺失或长度不足时，默认启用
		if i >= len(route.DstEndPointIDStatuses) {
			return id, true
		}
	}
	return 0, false
}
