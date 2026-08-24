package models

// 协议转换分析器记录查询（迁移自旧工程 server_web_manager_protocol_converter.go 的数据层部分）。
// 列表查询排除 longtext 大字段（request_body/response_body 等），详情按需单条加载。

import (
	"encoding/base64"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lishimeng/LsmTokensServer/config"
	"github.com/lishimeng/LsmTokensServer/database"
)

// ProtocolConvertAnalyzerRecordItem 协议转换分析器页面使用的精简记录项
type ProtocolConvertAnalyzerRecordItem struct {
	ID                      uint64    `json:"id"`
	CreatedAt               time.Time `json:"created_at"`
	UserName                string    `json:"user_name"`
	ModelName               string    `json:"model_name"`
	ProtocolType            int       `json:"protocol_type"`
	RequestURL              string    `json:"request_url"`
	RequestBody             string    `json:"request_body"`
	RequestSrcProtocolBody  string    `json:"request_src_protocol_body"`
	ResponseBody            string    `json:"response_body"`
	ResponseSrcProtocolBody string    `json:"response_src_protocol_body"`
	IsStream                bool      `json:"is_stream"`
}

// ProtocolConvertAnalyzerRecordDetail 协议转换分析器按需加载的完整记录数据。
type ProtocolConvertAnalyzerRecordDetail struct {
	ID                         uint64    `json:"id"`
	CreatedAt                  time.Time `json:"created_at"`
	UserName                   string    `json:"user_name"`
	ModelName                  string    `json:"model_name"`
	ProtocolType               int       `json:"protocol_type"`
	RequestURL                 string    `json:"request_url"`
	RequestHeaders             string    `json:"request_headers"`
	RequestSrcProtocolHeaders  string    `json:"request_src_protocol_headers"`
	ResponseHeaders            string    `json:"response_headers"`
	ResponseSrcProtocolHeaders string    `json:"response_src_protocol_headers"`
	RequestBody                string    `json:"request_body"`
	RequestSrcProtocolBody     string    `json:"request_src_protocol_body"`
	ResponseBody               string    `json:"response_body"`
	ResponseSrcProtocolBody    string    `json:"response_src_protocol_body"`
	IsStream                   bool      `json:"is_stream"`
}

// analyzerDaysFilter days 参数语义：
//   days = 0 或 days = -1  → 查询全部数据（无时间限制）
//   days > 0                → 查询最近 days 天（最大 90 天）
func analyzerDaysFilter(days int) (time.Time, bool) {
	if days <= 0 {
		return time.Time{}, false
	}
	if days > 90 {
		days = 90
	}
	return time.Now().AddDate(0, 0, -days), true
}

// GetProtocolConvertAnalyzerRecordDetailByID 根据用户+模型定位分表并加载单条大字段详情。
func GetProtocolConvertAnalyzerRecordDetailByID(userName, modelName string, subTableNum int, id uint64) (*ProtocolConvertAnalyzerRecordDetail, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if userName == "" || modelName == "" || id == 0 {
		return nil, fmt.Errorf("id, user_name and model_name are required")
	}

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !IsTableExists(tableName) {
		return nil, fmt.Errorf("record table not found")
	}

	var detail ProtocolConvertAnalyzerRecordDetail
	err := database.DB.Table(tableName).Select(
		"id", "created_at", "user_name", "model_name", "protocol_type", "request_url",
		"request_headers", "request_src_protocol_headers", "response_headers", "response_src_protocol_headers",
		"request_body", "request_src_protocol_body", "response_body", "response_src_protocol_body", "is_stream",
	).Where("id = ? AND user_name = ? AND model_name = ?", id, userName, modelName).First(&detail).Error
	if err != nil {
		return nil, fmt.Errorf("failed to query protocol converter detail: %w", err)
	}
	detail.RequestBody = DecodeAnalyzerBody(detail.RequestBody)
	detail.RequestSrcProtocolBody = DecodeAnalyzerBody(detail.RequestSrcProtocolBody)
	detail.ResponseBody = DecodeAnalyzerBody(detail.ResponseBody)
	detail.ResponseSrcProtocolBody = DecodeAnalyzerBody(detail.ResponseSrcProtocolBody)
	detail.RequestHeaders = RedactAuthorizationBearerHeaderText(detail.RequestHeaders)
	detail.RequestSrcProtocolHeaders = RedactAuthorizationBearerHeaderText(detail.RequestSrcProtocolHeaders)
	return &detail, nil
}

// DecodeAnalyzerBody base64 解码记录体字段（包含 [truncated] 标记的截断体跳过解码）
func DecodeAnalyzerBody(body string) string {
	if body == "" {
		return ""
	}
	decoded := body
	if !strings.Contains(body, "[truncated]") {
		if raw, err := base64.StdEncoding.DecodeString(body); err == nil {
			decoded = string(raw)
		}
	}
	return decoded
}

// QueryProtocolConvertAnalyzerRecords 全平台跨分表查询记录（用于协议转换分析器调试页面）
// 按创建时间倒序，支持分页、协议类型筛选、用户名筛选、模型筛选和时间范围过滤。
// 性能优化要点：
//  1. days=0 或 days=-1 时查询全部数据（无时间限制）；days>0 时查询最近 days 天
//  2. 列表查询排除大字段（longtext），减少网络传输和内存占用
//  3. 使用 sort.Slice 替代冒泡排序（O(n log n) vs O(n²)）
//  4. 每个分表查询使用合理的 LIMIT（深分页时自动增加每表取样数）
//  5. 支持仅按模型筛选（不指定用户），遍历所有分表查询该模型
func QueryProtocolConvertAnalyzerRecords(subTableNum, page, pageSize, protocolType int, userName, modelName string, days int) ([]ProtocolConvertAnalyzerRecordItem, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 50 {
		pageSize = 50 // 限制最大页面大小
	}

	timeFilter, hasTimeFilter := analyzerDaysFilter(days)

	// 先统计总数（跨所有分表）
	var total int64
	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}
		var count int64
		query := database.DB.Table(tableName)
		if hasTimeFilter {
			query = query.Where("created_at >= ?", timeFilter)
		}
		if protocolType > 0 {
			query = query.Where("protocol_type = ?", protocolType)
		}
		if userName != "" {
			query = query.Where("user_name = ?", userName)
		}
		if modelName != "" {
			query = query.Where("model_name = ?", modelName)
		}
		if err := query.Count(&count).Error; err != nil {
			continue // 忽略单表错误
		}
		total += count
	}

	if total == 0 {
		return []ProtocolConvertAnalyzerRecordItem{}, 0, nil
	}

	// 计算需要查询的偏移量
	offset := (page - 1) * pageSize

	// 跨分表查询：每个分表取足够的条数，确保内存排序后有足够数据分页
	// 策略：基础取样数 = pageSize * 4（确保即使数据分布不均也能取到足够数据）
	// 深分页时：增加取样数以减少数据丢失概率
	perTableLimit := pageSize * 4
	if offset > 0 {
		// 深分页时增加取样：offset 越大，每表取样越多
		extraSamples := (offset / pageSize) * pageSize * 2
		perTableLimit += extraSamples
	}
	// 限制单个分表最大取样数，防止内存溢出
	if perTableLimit > 500 {
		perTableLimit = 500
	}

	var allRecords []ProtocolConvertAnalyzerRecordItem

	for i := 0; i < subTableNum; i++ {
		tableName := fmt.Sprintf("TAgentHttpTransactionDataItem_%02d", i)
		if !IsTableExists(tableName) {
			continue
		}

		var records []ProtocolConvertAnalyzerRecordItem
		// 列表查询排除大字段（longtext），减少网络传输和内存占用
		// request_body/response_body 在列表中不需要，详情页按需加载
		query := database.DB.Table(tableName).Select(
			"id", "created_at", "user_name", "model_name", "protocol_type",
			"request_url", "is_stream",
		)
		if hasTimeFilter {
			query = query.Where("created_at >= ?", timeFilter)
		}
		if protocolType > 0 {
			query = query.Where("protocol_type = ?", protocolType)
		}
		if userName != "" {
			query = query.Where("user_name = ?", userName)
		}
		if modelName != "" {
			query = query.Where("model_name = ?", modelName)
		}
		err := query.Order("created_at DESC").Limit(perTableLimit).Find(&records).Error
		if err != nil {
			continue // 忽略单表错误
		}
		allRecords = append(allRecords, records...)
	}

	// 按创建时间倒序排序（使用 sort.Slice，O(n log n)）
	sort.Slice(allRecords, func(i, j int) bool {
		return allRecords[i].CreatedAt.After(allRecords[j].CreatedAt)
	})

	// 内存分页
	start := offset
	if start > len(allRecords) {
		start = len(allRecords)
	}
	end := start + pageSize
	if end > len(allRecords) {
		end = len(allRecords)
	}

	return allRecords[start:end], total, nil
}

// QueryProtocolConvertAnalyzerRecordsByModel 按用户+模型名称精确查询单张分表记录
// 利用哈希分表规则直接定位到特定分表，避免跨所有分表扫描，数据完整且性能最优
func QueryProtocolConvertAnalyzerRecordsByModel(subTableNum, page, pageSize, protocolType int, userName, modelName string, days int) ([]ProtocolConvertAnalyzerRecordItem, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("database not initialized")
	}
	if subTableNum <= 0 {
		subTableNum = config.DEFAULT_SUB_TABLE_NUM
	}
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	if pageSize > 50 {
		pageSize = 50
	}

	timeFilter, hasTimeFilter := analyzerDaysFilter(days)

	tableName := GetAgentHttpTableName(userName, modelName, subTableNum)
	if !IsTableExists(tableName) {
		return []ProtocolConvertAnalyzerRecordItem{}, 0, nil
	}

	// 统计总数（单表，带所有过滤条件）
	var total int64
	countQuery := database.DB.Table(tableName).Where("user_name = ? AND model_name = ?", userName, modelName)
	if hasTimeFilter {
		countQuery = countQuery.Where("created_at >= ?", timeFilter)
	}
	if protocolType > 0 {
		countQuery = countQuery.Where("protocol_type = ?", protocolType)
	}
	if err := countQuery.Count(&total).Error; err != nil {
		return nil, 0, fmt.Errorf("failed to count records: %w", err)
	}

	if total == 0 {
		return []ProtocolConvertAnalyzerRecordItem{}, 0, nil
	}

	offset := (page - 1) * pageSize

	// 单表精确查询，直接使用数据库 OFFSET/LIMIT，数据完整无丢失
	var records []ProtocolConvertAnalyzerRecordItem
	query := database.DB.Table(tableName).Select(
		"id", "created_at", "user_name", "model_name", "protocol_type",
		"request_url", "is_stream",
	).Where("user_name = ? AND model_name = ?", userName, modelName)
	if hasTimeFilter {
		query = query.Where("created_at >= ?", timeFilter)
	}
	if protocolType > 0 {
		query = query.Where("protocol_type = ?", protocolType)
	}
	err := query.Order("created_at DESC").Limit(pageSize).Offset(offset).Find(&records).Error
	if err != nil {
		return nil, 0, fmt.Errorf("failed to query records: %w", err)
	}

	return records, total, nil
}
