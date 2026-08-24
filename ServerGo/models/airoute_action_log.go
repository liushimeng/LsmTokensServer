package models

import (
	"fmt"
	"strings"

	"github.com/lishimeng/LsmTokensServer/logger"
)

// LogAIRouteAction 记录智能路由操作日志（使用名称而非ID，便于人类阅读）
func LogAIRouteAction(action string, username string, modelID uint64, endpointList string) {
	// 查询模型名称
	modelName := "未知模型"
	model, err := GetUserModelByID(modelID)
	if err == nil && model != nil {
		modelName = model.ModelName
	}

	// 解析源站ID列表并查询名称
	endpointNames := []string{}
	ids, err := ParseDstEndPointIDList(endpointList)
	if err == nil && len(ids) > 0 {
		for _, id := range ids {
			ep, err := GetDstEndPointByID(id)
			if err == nil && ep != nil {
				endpointNames = append(endpointNames, fmt.Sprintf("%s/%s", ep.PlatformName, ep.ModelName))
			} else {
				endpointNames = append(endpointNames, fmt.Sprintf("ID:%d", id))
			}
		}
	} else {
		endpointNames = append(endpointNames, endpointList)
	}

	// 查询用户信息（如果username为空）
	userName := username
	if userName == "" {
		userName = "未知用户"
	}

	details := fmt.Sprintf("模型=%s 源站=[%s]", modelName, strings.Join(endpointNames, ", "))
	logger.LogUserAction(action, userName, details)
}
