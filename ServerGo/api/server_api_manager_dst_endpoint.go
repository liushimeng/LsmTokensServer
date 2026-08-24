package api

import (
	"encoding/json"
	"fmt"
	modelsdb "github.com/lishimeng/LsmTokensServer/models"
	"github.com/lishimeng/LsmTokensServer/system"
	"net/http"
	"strings"
)

// dstEndPointManageInterfaceHandle 源站管理 API
func dstEndPointManageInterfaceHandle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	setNoCacheHeaders(w)
	if r.Method != http.MethodPost {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "仅支持 POST"})
		return
	}
	var req struct {
		Action       string   `json:"action"`
		ID           uint64   `json:"id"`
		IDs          []uint64 `json:"ids"`
		UserID       uint64   `json:"user_id"`
		PlatformName string   `json:"platform_name"`
		ModelName    string   `json:"model_name"`
		ProtocolType int      `json:"protocol_type"`
		URLAddress   string   `json:"url_address"`
		APIKey       string   `json:"api_key"`
		AuthType     int      `json:"auth_type"`
		Status       int      `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
		return
	}

	switch req.Action {
	case "list":
		var endpoints []modelsdb.TAgentDstEndPoint
		var err error
		if req.UserID == 0 {
			// 返回所有用户的源站
			users, uerr := modelsdb.GetAllUsers(0, 0)
			if uerr != nil {
				json.NewEncoder(w).Encode(userManageResp{Success: false, Message: uerr.Error()})
				return
			}
			for _, u := range users {
				eps, eerr := modelsdb.GetDstEndPointsByUserID(u.ID)
				if eerr == nil {
					endpoints = append(endpoints, eps...)
				}
			}
		} else {
			endpoints, err = modelsdb.GetDstEndPointsByUserID(req.UserID)
			if err != nil {
				json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
				return
			}
		}
		// 过滤掉 API Key，不在列表响应中暴露
		var result []map[string]any
		for _, ep := range endpoints {
			result = append(result, map[string]any{
				"id":            ep.ID,
				"user_id":       ep.UserID,
				"platform_name": ep.PlatformName,
				"model_name":    ep.ModelName,
				"protocol_type": ep.ProtocolType,
				"url_address":   ep.URLAddress,
				"auth_type":     ep.AuthType,
				"status":        ep.Status,
			})
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: result})
	case "list_platforms":
		names, err := modelsdb.GetDistinctPlatformNamesByUserID(req.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: names})
	case "list_models":
		names, err := modelsdb.GetDistinctModelNamesByUserID(req.UserID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Data: names})
	case "add":
		item := &modelsdb.TAgentDstEndPoint{
			UserID:       req.UserID,
			PlatformName: strings.TrimSpace(req.PlatformName),
			ModelName:    strings.TrimSpace(req.ModelName),
			ProtocolType: req.ProtocolType,
			URLAddress:   strings.TrimSpace(req.URLAddress),
			APIKey:       strings.TrimSpace(req.APIKey),
			AuthType:     req.AuthType,
		}
		// 保存前进行 API 连通性测试；失败时返回完整的请求/响应信息（含 header + body），
		// 方便用户在前端弹窗中排查配置错误（URL / API Key / 模型名 / 协议类型等）
		testResult := system.TestDstEndPointConnectivityWithResult(item, "", 0)
		if !testResult.Success {
			json.NewEncoder(w).Encode(userManageResp{
				Success: false,
				Message: "API 测试失败: " + testResult.Message,
				Data:    testResult,
			})
			return
		}
		if err := modelsdb.AddDstEndPoint(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "添加成功", Data: item})
	case "update":
		apiKey := strings.TrimSpace(req.APIKey)
		var oldItem *modelsdb.TAgentDstEndPoint
		// 如果前端未提供 API Key（编辑时不显示该字段），保留原值
		if apiKey == "" {
			var err error
			oldItem, err = modelsdb.GetDstEndPointByID(req.ID)
			if err == nil && oldItem != nil {
				apiKey = oldItem.APIKey
			}
		}
		// 如果 oldItem 仍未获取（前端提供了 API Key 的情况），查询原记录以保留状态
		if oldItem == nil {
			var err error
			oldItem, err = modelsdb.GetDstEndPointByID(req.ID)
			if err != nil {
				json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "获取原记录失败: " + err.Error()})
				return
			}
		}
		item := &modelsdb.TAgentDstEndPoint{
			ID:           req.ID,
			UserID:       req.UserID,
			PlatformName: strings.TrimSpace(req.PlatformName),
			ModelName:    strings.TrimSpace(req.ModelName),
			ProtocolType: req.ProtocolType,
			URLAddress:   strings.TrimSpace(req.URLAddress),
			APIKey:       apiKey,
			AuthType:     req.AuthType,
			Status:       oldItem.Status, // 保留原状态，编辑时不修改启用/禁用状态
		}
		if err := modelsdb.UpdateDstEndPoint(item); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "更新成功"})
	case "test":
		// 根据 ID 获取完整源站信息（含 API Key）
		item, err := modelsdb.GetDstEndPointByID(req.ID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "获取源站信息失败: " + err.Error()})
			return
		}
		if item == nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "源站不存在"})
			return
		}
		// 管理员端测试：userName 为空，saveTestRecordToSubTable 会自动使用 modelName 填充
		result := system.TestDstEndPointConnectivityWithResult(item, "", 0)
		json.NewEncoder(w).Encode(userManageResp{Success: result.Success, Message: result.Message, Data: result})
	case "toggle_status":
		if err := modelsdb.UpdateDstEndPointStatus(req.ID, req.Status); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "状态更新成功"})
	case "delete":
		if err := modelsdb.DeleteDstEndPoint(req.ID); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: err.Error()})
			return
		}
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "删除成功（已同步清理智能路由中的关联引用）"})
	case "batch_disable":
		updatedCount, errs := modelsdb.BatchUpdateDstEndPointStatus(req.IDs, 0)
		batchRespondToWriter(w, "禁用", updatedCount, errs, len(req.IDs))
	case "batch_enable":
		updatedCount, errs := modelsdb.BatchUpdateDstEndPointStatus(req.IDs, 1)
		batchRespondToWriter(w, "启用", updatedCount, errs, len(req.IDs))
	case "batch_delete":
		deletedCount, errs := modelsdb.BatchDeleteDstEndPoints(req.IDs)
		batchRespondToWriter(w, "删除", deletedCount, errs, len(req.IDs))
	case "chat_info":
		// 返回源站完整信息（含 API Key），用于前端对话功能
		item, err := modelsdb.GetDstEndPointByID(req.ID)
		if err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "获取源站信息失败: " + err.Error()})
			return
		}
		if item == nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "源站不存在"})
			return
		}
		// 同时返回服务器内存中的对话历史
		session := modelsdb.GetOrCreateChatSession(req.ID)
		json.NewEncoder(w).Encode(userManageResp{
			Success: true,
			Data: map[string]interface{}{
				"id":                   item.ID,
				"platform_name":        item.PlatformName,
				"model_name":           item.ModelName,
				"protocol_type":        item.ProtocolType,
				"url_address":          item.URLAddress,
				"api_key":              item.APIKey,
				"status":               item.Status,
				"server_history":       session.Messages,
				"server_system_prompt": session.SystemPrompt,
			},
		})
	case "chat_sync":
		// 前端同步对话历史到服务器内存
		var syncReq struct {
			ID           uint64                     `json:"id"`
			SystemPrompt string                     `json:"system_prompt"`
			Messages     []modelsdb.ChatHistoryItem `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&syncReq); err != nil {
			json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "请求解析失败: " + err.Error()})
			return
		}
		modelsdb.UpdateChatSession(syncReq.ID, syncReq.SystemPrompt, syncReq.Messages)
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "同步成功"})
	case "chat_clear":
		// 清空服务器内存中的对话历史
		modelsdb.ClearChatSession(req.ID)
		json.NewEncoder(w).Encode(userManageResp{Success: true, Message: "已清空服务器对话历史"})
	default:
		json.NewEncoder(w).Encode(userManageResp{Success: false, Message: "未知操作: " + req.Action})
	}
}

// batchRespondToWriter 批量操作的统一响应封装（与 v2.0.32 batch_update / batch_delete 风格一致）
// countKeyName 决定 Data 字段的 key 名（updated_count / deleted_count）
func batchRespondToWriter(w http.ResponseWriter, actionName string, count int64, errs []error, totalCount int) {
	if len(errs) > 0 {
		errMsgs := make([]string, 0, len(errs))
		for _, e := range errs {
			errMsgs = append(errMsgs, e.Error())
		}
		countKey := "updated_count"
		errorCountKey := "error_count"
		if actionName == "删除" {
			countKey = "deleted_count"
			errorCountKey = "error_count"
		}
		json.NewEncoder(w).Encode(userManageResp{
			Success: count > 0,
			Message: fmt.Sprintf("部分%s成功（%d/%d），%d 条失败", actionName, count, totalCount, len(errs)),
			Data: map[string]interface{}{
				countKey:      count,
				errorCountKey: len(errs),
				"errors":      errMsgs,
			},
		})
		return
	}
	countKey := "updated_count"
	if actionName == "删除" {
		countKey = "deleted_count"
	}
	json.NewEncoder(w).Encode(userManageResp{
		Success: true,
		Message: fmt.Sprintf("批量%s成功，共%s %d 条", actionName, actionName, count),
		Data:    map[string]interface{}{countKey: count},
	})
}
