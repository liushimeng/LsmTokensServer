package api

// 20260826 时间跨度动态档位：/TimeSpanConfigInterface 轻量配置下发接口。
//
// 背景：交易明细按 transactionRetentionDays 物理清理，查询跨度远大于保留期没有意义。
// 前端「时间跨度」通用组件需要知道上限（transactionRetentionDays + 1 天，同时受
// 后端统计上限 365 天约束）来动态生成 10 档选项（最小 1 小时）。
//
// 该接口双端注册（管理端 / 用户端），返回的均为非敏感运维参数：
//   - transaction_retention_days：配置的明细保留天数（0 = 禁用清理，数据永久保留）
//   - max_span_days / max_span_hours：前端档位上限（禁用清理时回落 365 天统计上限）
//   - min_span_hours / levels：档位下限（1 小时）与档数（10）
//
// 响应字段为蛇形命名，与 CleanupReportInterface state 快照的 retention_days 命名风格一致。

import (
	"encoding/json"
	"net/http"

	"github.com/lishimeng/LsmTokensServer/config"
)

// timeSpanConfigResponse /TimeSpanConfigInterface 响应体
type timeSpanConfigResponse struct {
	Success                  bool `json:"success"`
	TransactionRetentionDays int  `json:"transaction_retention_days"`
	MaxSpanDays              int  `json:"max_span_days"`
	MaxSpanHours             int  `json:"max_span_hours"`
	MinSpanHours             int  `json:"min_span_hours"`
	Levels                   int  `json:"levels"`
}

// timeSpanMaxDays 由保留天数推导前端档位上限：
//   - retention > 0：retention + 1 天，同时不超过后端统计天上限 365（超出部分会被裁剪）
//   - retention == 0：清理禁用（数据永久保留）→ 回落 365 天统计上限
func timeSpanMaxDays(retentionDays int) int {
	if retentionDays <= 0 {
		return 365
	}
	if retentionDays+1 > 365 {
		return 365
	}
	return retentionDays + 1
}

// timeSpanConfigInterfaceHandle 处理 /TimeSpanConfigInterface（GET）
func timeSpanConfigInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)

	if r.Method != http.MethodGet {
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": false,
			"message": "仅支持 GET 请求",
		})
		return
	}

	retention := 0
	if config.G != nil {
		retention = config.G.TransactionRetentionDays
	}
	if retention < 0 {
		retention = 0 // 防御：非法负值视同禁用
	}
	maxDays := timeSpanMaxDays(retention)

	json.NewEncoder(w).Encode(timeSpanConfigResponse{
		Success:                  true,
		TransactionRetentionDays: retention,
		MaxSpanDays:              maxDays,
		MaxSpanHours:             maxDays * 24,
		MinSpanHours:             1,
		Levels:                   10,
	})
}
