package models

import (
	"time"

	"gorm.io/gorm"
)

const DstEndPointAlgorithmType_Direct = 1
const DstEndPointAlgorithmType_ProtocolConverter = 2

const UserModelStatus_Enabled = 1
const UserModelStatus_Disabled = 2

const UserStatus_Enabled = 1
const UserStatus_Disabled = 2

// TAgentHttpUserInfo 用户信息表
type TAgentHttpUserInfo struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	UserName         string `json:"user_name" gorm:"size:50;index;unique;comment:用户名"`
	Password         string `json:"password" gorm:"size:128;comment:密码"`
	Phone            string `json:"phone" gorm:"size:20;index;comment:手机号"`
	Status           int    `json:"status" gorm:"default:1;index;comment:状态:1=启用,2=禁用"`
	AnthropicEnabled bool   `json:"anthropic_enabled" gorm:"comment:Anthropic协议是否启用"`
	OpenAIEnabled    bool   `json:"openai_enabled" gorm:"column:openai_enabled;comment:OpenAI协议是否启用"`
}

// TAgentHttpUserModelInfo 用户模型信息表
type TAgentHttpUserModelInfo struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	UserID    uint64 `json:"user_id" gorm:"index;comment:关联用户ID"`
	ModelName string `json:"model_name" gorm:"size:64;uniqueIndex:idx_user_model;comment:平台模型名称（用户维度唯一）"`
	APIKey    string `json:"api_key" gorm:"size:128;index;unique;comment:模型API Key（全平台唯一）"`
	Status    int    `json:"status" gorm:"default:1;index;comment:状态:1=启用,2=禁用"`
}

// TAgentDstEndPoint 代理目标接入点信息数据表
type TAgentDstEndPoint struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	UserID       uint64 `json:"user_id" gorm:"index;comment:关联用户ID"`
	PlatformName string `json:"platform_name" gorm:"size:64;index;comment:平台名称"`
	ModelName    string `json:"model_name" gorm:"size:64;index;comment:模型名称"`
	ProtocolType int    `json:"protocol_type" gorm:"index;comment:协议类型:1=Anthropic,2=OpenAI"`
	URLAddress   string `json:"url_address" gorm:"size:160;index;comment:源站URL地址"`
	APIKey       string `json:"api_key" gorm:"size:180;index;comment:源站API Key"`
	// AuthType 认证头方式：0=协议默认（Anthropic→x-api-key, OpenAI→Authorization Bearer），
	// 1=强制 x-api-key，2=强制 Authorization Bearer。
	// 部分代理站 URL 路径是 Anthropic 形态但只接受 Authorization Bearer（如美团 LongCat
	// api.longcat.chat/anthropic），用 0/默认会出现 missing_api_key；让用户在添加源站时
	// 显式选认证方式，避免试错降级的隐式行为。
	AuthType int `json:"auth_type" gorm:"default:0;index;comment:认证头:0=协议默认,1=x-api-key,2=Authorization Bearer"`
	Status   int `json:"status" gorm:"index;comment:状态:1=启用,0=禁用"`
}

// TAgentHttpAIRoute 智能路由表
type TAgentHttpAIRoute struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	UserID                       uint64 `json:"user_id" gorm:"index;comment:关联用户ID"`
	UserModelID                  uint64 `json:"user_model_id" gorm:"index;comment:关联用户模型ID"`
	ProtocolType                 int    `json:"protocol_type" gorm:"index;comment:协议类型:1=Anthropic,2=OpenAI"`
	DstEndPointIDList            string `json:"dst_endpoint_id_list" gorm:"column:dst_endpoint_id_list;size:256;comment:目标源站ID列表，逗号分隔"`
	DstEndPointIDStatusList      string `json:"dst_endpoint_id_status_list" gorm:"column:dst_endpoint_id_status_list;size:256;comment:目标源站可用状态列表，逗号分隔，与目标源站ID列表一一对应:1=启用,0=禁用"`
	DstEndPointAlgorithmTypeList string `json:"dst_endpoint_algorithm_type_list" gorm:"column:dst_endpoint_algorithm_type_list;size:256;comment:目标源站协议转换算法类型列表，逗号分隔，与目标源站ID列表一一对应:1=协议直连,2=协议转换器"`
	DstEndPointIDNumber          int    `json:"dst_endpoint_id_number" gorm:"column:dst_endpoint_id_number;comment:目标源站ID数量"`
	AlgorithmStrategyType        int    `json:"algorithm_strategy_type" gorm:"column:algorithm_strategy_type;comment:算法策略类型:1=使用第一个ID"`
}

// TAgentHttpTransactionDataItem 存储每次 HTTP 代理请求和响应信息
type TAgentHttpTransactionDataItem struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index;index:idx_user_model_created,priority:3"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// 用户信息
	UserID                   uint64 `json:"user_id" gorm:"index;comment:用户ID"`
	UserName                 string `json:"user_name" gorm:"size:50;index;index:idx_user_model_created,priority:1;comment:用户名"`
	ModelName                string `json:"model_name" gorm:"size:64;index;index:idx_user_model_created,priority:2;comment:平台模型名称"`
	DstModelName             string `json:"dst_model_name" gorm:"size:64;index;comment:目标源站模型名称"`
	APIKey                   string `json:"api_key" gorm:"size:128;index;comment:请求使用的模型API Key（全平台唯一）"`
	DstEndPointID            uint64 `json:"dst_endpoint_id" gorm:"column:dst_endpoint_id;index;comment:关联目标源站接入点ID"`
	DstEndPointAlgorithmType int    `json:"dst_endpoint_algorithm_type" gorm:"column:dst_endpoint_algorithm_type;index;comment:目标源站协议转换算法类型:1=协议直连,2=协议转换器"`
	ProtocolType             int    `json:"protocol_type" gorm:"index;comment:协议类型:1=Anthropic,2=OpenAI"`

	// 请求信息
	RequestMethod             string `json:"request_method" gorm:"size:50;index"`
	RequestURL                string `json:"request_url" gorm:"size:2048"`
	RequestRemoteAddr         string `json:"request_remote_addr" gorm:"size:180;index"`
	RequestContentLength      uint64 `json:"request_content_length" gorm:"index"`
	RequestHeaders            string `json:"request_headers" gorm:"type:text;comment:实际转发给目标源站的请求头（可能经过协议转换）"`
	RequestSrcProtocolHeaders string `json:"request_src_protocol_headers" gorm:"type:text;comment:客户端原始请求头（未经协议转换）"`
	RequestBody               string `json:"request_body" gorm:"type:longtext;comment:实际转发给目标源站的请求体（可能经过协议转换）"`
	RequestSrcProtocolBody    string `json:"request_src_protocol_body" gorm:"type:longtext;comment:客户端原始请求体（未经协议转换）"`

	// 响应信息
	ResponseStatus             string `json:"response_status" gorm:"size:50;index"`
	ResponseContentLength      uint64 `json:"response_content_length" gorm:"index"`
	ResponseHeaders            string `json:"response_headers" gorm:"type:text;comment:实际返回给客户端的响应头（可能经过协议转换）"`
	ResponseSrcProtocolHeaders string `json:"response_src_protocol_headers" gorm:"type:text;comment:目标源站原始响应头（未经协议转换）"`
	ResponseBody               string `json:"response_body" gorm:"type:longtext;comment:实际返回给客户端的响应体（可能经过协议转换）"`
	ResponseSrcProtocolBody    string `json:"response_src_protocol_body" gorm:"type:longtext;comment:目标源站原始响应体（未经协议转换）"`

	// Tokens 使用统计
	TokensInputSize  uint64 `json:"tokens_input_size" gorm:"index;comment:输入Tokens大小"`
	TokensOutputSize uint64 `json:"tokens_output_size" gorm:"index;comment:输出Tokens大小"`
	TokensAllSize    uint64 `json:"tokens_all_size" gorm:"index;comment:总Tokens大小"`

	// 处理信息
	ElapsedMs       int64     `json:"elapsed_ms" gorm:"index;comment:总耗时(TTLT),毫秒"`
	RequestStartAt  time.Time `json:"request_start_at" gorm:"index;comment:代理服务收到请求的开始时间戳"`
	RequestEndAt    time.Time `json:"request_end_at" gorm:"index;comment:代理服务发送请求到目标服务的时间戳"`
	ResponseStartAt time.Time `json:"response_start_at" gorm:"index;comment:代理服务收到目标服务响应第一个字节的时间戳"`
	ResponseEndAt   time.Time `json:"response_end_at" gorm:"index;comment:代理服务收到完整响应的时间戳"`
	ToolIdentifier  string    `json:"tool_identifier" gorm:"size:160;index;comment:调用工具标识"`

	// AI Agent 工具信息
	AgentToolName string `json:"agent_tool_name" gorm:"size:64;index;comment:AI Agent工具名称，如claude-cli/opencode等"`
	AgentToolInfo string `json:"agent_tool_info" gorm:"size:512;comment:AI Agent工具扩展信息，含版本、运行时等"`

	// 预解析的 Task 特征（避免查询时重复解析 request_body）
	IsParsed         bool   `json:"is_parsed" gorm:"index;comment:是否已解析request_body特征"`
	IsTask           bool   `json:"is_task" gorm:"comment:是否为Task请求"`
	TaskModel        string `json:"task_model" gorm:"size:64;comment:Task使用的模型名称"`
	IsStream         bool   `json:"is_stream" gorm:"comment:是否为流式请求"`
	HasSystemPrompt  bool   `json:"has_system_prompt" gorm:"comment:是否包含System Prompt"`
	HasToolCall      bool   `json:"has_tool_call" gorm:"comment:是否包含工具调用"`
	MessageCount     int    `json:"message_count" gorm:"comment:消息数量"`
	UserMessageCount int    `json:"user_message_count" gorm:"comment:用户消息数量"`

	// 请求体中解析出的工具列表名称（逗号分隔）
	RequestTools string `json:"request_tools" gorm:"size:512;index;comment:请求体中解析出的工具列表名称，逗号分隔"`

	// 识别出的 Session ID（unknown_session_id 表示未识别）
	SessionID string `json:"session_id" gorm:"size:128;index;comment:识别出的Session ID，unknown_session_id表示未识别"`
}

// TAgentHttpAgentInfo AI Agent 工具信息表
type TAgentHttpAgentInfo struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	AgentToolName string    `json:"agent_tool_name" gorm:"size:64;uniqueIndex;comment:AI Agent工具名称（唯一）"`
	FirstSeenAt   time.Time `json:"first_seen_at" gorm:"index;comment:首次出现时间"`
	LastSeenAt    time.Time `json:"last_seen_at" gorm:"index;comment:最后出现时间"`
	UsageCount    uint64    `json:"usage_count" gorm:"index;comment:使用次数统计"`
}

// TAgentHttpTransactionCleanupReport 过期数据清理统计表（v2.0.47）
//
// 后台清理服务（mysql_http_agent_cleanup.go）每天凌晨执行一次：
//  1. 遍历 8 张分表（TAgentHttpTransactionDataItem_00 ~ _07）
//  2. 按 created_at < cutoff 删除过期记录
//  3. OPTIMIZE TABLE 释放磁盘空间
//  4. 本表写一行报告（按 cleanup_date + sub_table_index 唯一索引）
//
// 字段语义：
//   - CleanupDate: 清理日期（YYYY-MM-DD），便于按天聚合展示
//   - SubTableIndex: 分表索引 0~7
//   - DeletedRows/DeletedTokensIn/Out/All: 本次清理的统计指标
//   - FreedBytes: 释放磁盘空间字节数（来自 information_schema.TABLES DATA_FREE）
//   - DurationMs: 本次清理总耗时
//   - Status: success/failed/partial（单表清理状态，不影响其他表继续）
//   - ErrorMsg: 失败时的错误信息（成功时为空）
//
// 设计决策：
//   - 单条记录（不分表）：每天 8 张分表各写 1 条 → 8 行/天，1 年 ≈ 2920 行，可忽略
//   - 不软删：与 TAgentHttpTransactionDataItem 一致（项目架构视清理记录为流水）
type TAgentHttpTransactionCleanupReport struct {
	ID        uint64    `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time `json:"created_at" gorm:"not null;index;comment:报告写入时间"`

	CleanupDate   string `json:"cleanup_date" gorm:"size:10;uniqueIndex:idx_cleanup_date_table,priority:1;comment:清理日期 YYYY-MM-DD"`
	SubTableIndex int    `json:"sub_table_index" gorm:"uniqueIndex:idx_cleanup_date_table,priority:2;comment:分表索引 0-7"`
	SubTableName  string `json:"sub_table_name" gorm:"size:64;index;comment:分表名"`

	DeletedRows      int64     `json:"deleted_rows" gorm:"comment:本次删除的记录数"`
	DeletedTokensIn  uint64    `json:"deleted_tokens_in" gorm:"comment:本次删除记录的输入 Tokens"`
	DeletedTokensOut uint64    `json:"deleted_tokens_out" gorm:"comment:本次删除记录的输出 Tokens"`
	DeletedTokensAll uint64    `json:"deleted_tokens_all" gorm:"comment:本次删除记录的总 Tokens"`
	FreedBytes       int64     `json:"freed_bytes" gorm:"comment:释放磁盘空间字节数（information_schema.TABLES DATA_FREE 估算）"`
	DurationMs       int64     `json:"duration_ms" gorm:"comment:本次清理总耗时毫秒"`
	CutoffTime       time.Time `json:"cutoff_time" gorm:"index;comment:本次删除的 cutoff 时间戳（cutoff 之前的记录被删除）"`
	RetentionDays    int       `json:"retention_days" gorm:"comment:本次使用的保留天数"`

	Status   string `json:"status" gorm:"size:16;index;comment:状态:success=成功/failed=失败/partial=部分成功"`
	ErrorMsg string `json:"error_msg" gorm:"size:512;comment:错误信息（成功时为空）"`
}

// CleanupReportTableName 清理报告表名常量
const CleanupReportTableName = "TAgentHttpTransactionCleanupReport"

// ==================== v2.0.18 patch3：路由编辑顺序错位修复辅助函数 ====================
//
// 来源 bug：路由编辑 (管理员 / 用户端) 调整源站顺序后，禁用源站的位置没有跟随真实
//   源站 ID — 后端 UpdateAIRoute 直接沿用旧 status list，导致 [A=启用,B=启用,C=禁用]
//   调整顺序为 [C,A,B] 后，新 status list 仍是 "1,1,0"，C 重新变成启用。
// 根因：前端编辑模态框未提交 dst_endpoint_id_status_list 字段。
// 修复策略：
//   1. 前端维护 selectedEndpointStatuses 数组，提交时一并发送（前端修复）；
//   2. API 层 server_api_manager_ai_route.go / server_api_user_ai_route.go
//      增加 DstEndPointIDStatusList 字段接收（API 修复）；
//   3. 当前端只改顺序未传 status list 时，本函数按"原 ID → 旧状态"映射表把
//      旧状态按新顺序对齐（后端兜底，避免前端遗漏导致错位）。
//   4. 算法列表 algorithm type 同步重排（同一根因，同一修复）。

// remapStatusListByIDs 按"原 ID 索引"把旧状态列表映射到新顺序。
// 入参：
//   - oldIDList: 旧 DstEndPointIDList 字符串
//   - oldStatusList: 旧 DstEndPointIDStatusList 字符串（可为空）
//   - newIDList: 新 DstEndPointIDList 字符串
//
// 返回：规范化后的新 DstEndPointIDStatusList 字符串。
func remapStatusListByIDs(oldIDList, oldStatusList, newIDList string) string {
	newIDs, err := ParseDstEndPointIDList(newIDList)
	if err != nil || len(newIDs) == 0 {
		return BuildDefaultDstEndPointIDStatusList(0)
	}
	oldIDs, _ := ParseDstEndPointIDList(oldIDList)
	oldStatuses, err := ParseDstEndPointIDStatusList(oldStatusList)
	if err != nil || len(oldStatuses) == 0 {
		// 旧状态不可解析 → 按 1=启用 兜底
		oldStatuses = make([]int, len(oldIDs))
		for i := range oldStatuses {
			oldStatuses[i] = 1
		}
	}

	// 构造 oldID → 旧状态 的 map（保留首次出现的索引；后续重复 ID 也用首次位置状态）
	oldStatusByID := make(map[uint64]int, len(oldIDs))
	for i, id := range oldIDs {
		if _, ok := oldStatusByID[id]; !ok {
			if i < len(oldStatuses) {
				oldStatusByID[id] = oldStatuses[i]
			} else {
				oldStatusByID[id] = 1
			}
		}
	}

	remapped := make([]int, len(newIDs))
	for i, id := range newIDs {
		if s, ok := oldStatusByID[id]; ok {
			remapped[i] = s
		} else {
			remapped[i] = 1 // 新加入的 ID 默认启用
		}
	}
	return FormatDstEndPointIDStatusList(remapped)
}

// remapAlgorithmTypeListByIDs 按"原 ID 索引"把旧算法列表映射到新顺序。
// 与 remapStatusListByIDs 同源逻辑，应用在算法类型列表上。
func remapAlgorithmTypeListByIDs(oldIDList, oldAlgoList, newIDList string) string {
	newIDs, err := ParseDstEndPointIDList(newIDList)
	if err != nil || len(newIDs) == 0 {
		return ""
	}
	oldIDs, _ := ParseDstEndPointIDList(oldIDList)
	oldAlgos, err := ParseDstEndPointAlgorithmTypeList(oldAlgoList)
	if err != nil || len(oldAlgos) == 0 {
		oldAlgos = make([]int, len(oldIDs))
		for i := range oldAlgos {
			oldAlgos[i] = DstEndPointAlgorithmType_Direct
		}
	}

	oldAlgoByID := make(map[uint64]int, len(oldIDs))
	for i, id := range oldIDs {
		if _, ok := oldAlgoByID[id]; !ok {
			if i < len(oldAlgos) {
				oldAlgoByID[id] = oldAlgos[i]
			} else {
				oldAlgoByID[id] = DstEndPointAlgorithmType_Direct
			}
		}
	}

	remapped := make([]int, len(newIDs))
	for i, id := range newIDs {
		if a, ok := oldAlgoByID[id]; ok {
			remapped[i] = a
		} else {
			remapped[i] = DstEndPointAlgorithmType_Direct
		}
	}
	return FormatDstEndPointAlgorithmTypeList(remapped)
}
