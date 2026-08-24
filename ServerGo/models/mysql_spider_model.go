package models

import (
	"fmt"
	"github.com/lishimeng/LsmTokensServer/database"
	"github.com/lishimeng/LsmTokensServer/logger"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

// ============================================================================
// 数据模型定义
// ============================================================================

// TSpiderDataSource 爬虫数据源配置
type TSpiderDataSource struct {
	ID        uint64         `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt time.Time      `json:"created_at" gorm:"not null;index;comment:创建时间"`
	UpdatedAt time.Time      `json:"updated_at" gorm:"index;comment:更新时间"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index;comment:删除时间"`

	UserID       uint64 `json:"user_id" gorm:"index;index:idx_user_status,composite:1;comment:所属用户ID(0=公共)"`
	PlatformName string `json:"platform_name" gorm:"size:64;index;comment:平台名称"`
	URLAddress   string `json:"url_address" gorm:"size:256;uniqueIndex;comment:URL地址"`
	Description  string `json:"description" gorm:"size:512;comment:源信息描述"`
	Remark       string `json:"remark" gorm:"size:256;index;comment:备注"`
	Status       int    `json:"status" gorm:"index;index:idx_user_status,composite:2;comment:状态:1=启用,0=禁用"`
}

// TableName 指定表名映射
func (TSpiderDataSource) TableName() string {
	return "t_spider_data_sources"
}

// TSpiderDailyInfo 爬取的每日信息（没有软删除，使用硬删除）
// v1.3.0 新增 TitleZh / ContentZh / TranslatedAt：用于持久化翻译结果，
// 避免前端必须根据 use_translated 字段在 title/content 之间二选一。
// 双版本都保留，渲染层决定显示哪个。
type TSpiderDailyInfo struct {
	ID           uint64     `json:"id" gorm:"primaryKey;autoIncrement;comment:主键ID"`
	CreatedAt    time.Time  `json:"created_at" gorm:"not null;index;comment:创建时间"`
	UpdatedAt    time.Time  `json:"updated_at" gorm:"index;comment:更新时间"`
	DataSourceID uint64     `json:"data_source_id" gorm:"index;comment:数据源ID"`
	PlatformName string     `json:"platform_name" gorm:"size:64;index;comment:平台名称"`
	Title        string     `json:"title" gorm:"size:512;comment:标题（原文或中文）"`
	TitleZh      string     `json:"title_zh" gorm:"size:512;comment:中文标题（v1.3.0）"`
	Content      string     `json:"content" gorm:"type:text;comment:内容摘要（原文或中文）"`
	ContentZh    string     `json:"content_zh" gorm:"type:text;comment:中文内容摘要（v1.3.0）"`
	RawData      string     `json:"raw_data" gorm:"type:longtext;comment:原始HTML/JSON"`
	CrawlTime    time.Time  `json:"crawl_time" gorm:"index;comment:爬取时间"`
	URL          string     `json:"url" gorm:"size:512;comment:原文链接"`
	TranslatedAt *time.Time `json:"translated_at" gorm:"comment:翻译时间（v1.3.0）"`
}

// ============================================================================
// 单表常量
// ============================================================================

// SpiderDailyInfoTableName 爬虫每日信息单表名（v2.0.5 去分表，改为单表）
const SpiderDailyInfoTableName = "t_spider_daily_info"

// ============================================================================
// 数据库初始化
// ============================================================================

// InitSpiderTables 初始化爬虫相关表（v2.0.5：单表，不再分表）
func InitSpiderTables() error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	logger.Printf("[database.DB] Initializing spider tables...")

	// 1. 创建数据源表（单表）
	if err := database.DB.AutoMigrate(&TSpiderDataSource{}); err != nil {
		return fmt.Errorf("创建数据源表失败: %w", err)
	}

	// 2. 创建每日信息单表
	if err := database.DB.Table(SpiderDailyInfoTableName).AutoMigrate(&TSpiderDailyInfo{}); err != nil {
		return fmt.Errorf("创建每日信息表失败: %w", err)
	}

	// 3. 创建索引（如果不存在）
	indexes := []struct{ name, cols string }{
		{"idx_ds_crawl_time", "data_source_id, crawl_time DESC"},
		{"idx_platform_crawl_time", "platform_name, crawl_time DESC"},
		{"idx_crawl_time_desc", "crawl_time DESC"},
	}
	for _, idx := range indexes {
		sql := fmt.Sprintf("CREATE INDEX IF NOT EXISTS %s ON %s (%s)", idx.name, SpiderDailyInfoTableName, idx.cols)
		if err := database.DB.Exec(sql).Error; err != nil {
			logger.Printf("[WARNING] 创建索引 %s 失败: %v", idx.name, err)
		}
	}

	logger.Printf("[database.DB] Spider tables initialized successfully (single table: %s)", SpiderDailyInfoTableName)
	return nil
}

// ============================================================================
// 数据源 CRUD
// ============================================================================

// CreateSpiderDataSource 创建数据源
func CreateSpiderDataSource(ds *TSpiderDataSource) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	// 设置默认值
	if ds.Status != 1 && ds.Status != 0 {
		ds.Status = 1
	}

	if err := database.DB.Create(ds).Error; err != nil {
		return fmt.Errorf("创建数据源失败: %w", err)
	}

	// 增量添加到缓存
	AddSpiderDataSourceToCache(ds)

	return nil
}

// UpdateSpiderDataSource 更新数据源
func UpdateSpiderDataSource(ds *TSpiderDataSource) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	if err := database.DB.Save(ds).Error; err != nil {
		return fmt.Errorf("更新数据源失败: %w", err)
	}

	// 增量更新缓存
	UpdateSpiderDataSourceInCache(ds)

	return nil
}

// DeleteSpiderDataSource 删除数据源（软删除）
func DeleteSpiderDataSource(id uint64) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	if err := database.DB.Delete(&TSpiderDataSource{}, id).Error; err != nil {
		return fmt.Errorf("删除数据源失败: %w", err)
	}

	// 增量删除缓存中的项
	RemoveSpiderDataSourceFromCache(id)

	return nil
}

// GetSpiderDataSourceByID 根据ID获取数据源
func GetSpiderDataSourceByID(id uint64) (*TSpiderDataSource, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	var ds TSpiderDataSource
	if err := database.DB.First(&ds, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("查询数据源失败: %w", err)
	}

	return &ds, nil
}

// ListSpiderDataSourcesWithFilter 列出数据源（支持多维度过滤：ID、PlatformName、Status）
// 管理员看到所有数据源，用户看到自己的+公共的
func ListSpiderDataSourcesWithFilter(userID uint64, isAdmin bool, id uint64, platformName string, status *int) ([]TSpiderDataSource, error) {
	// 优先使用缓存（带过滤条件）
	cachedList := GetCachedSpiderDataSourcesList()
	if len(cachedList) > 0 {
		var result []TSpiderDataSource
		for _, ds := range cachedList {
			// 权限过滤
			if !isAdmin {
				if ds.UserID != userID && ds.UserID != 0 {
					continue
				}
			}

			// ID 精确匹配过滤
			if id > 0 && ds.ID != id {
				continue
			}

			// PlatformName 模糊匹配过滤（支持部分匹配，不区分大小写）
			if platformName != "" && !stringsContainsIgnoreCase(ds.PlatformName, platformName) {
				continue
			}

			// Status 精确匹配过滤
			if status != nil && ds.Status != *status {
				continue
			}

			result = append(result, *ds)
		}
		// 按 id 倒序排列
		sort.Slice(result, func(i, j int) bool {
			return result[i].ID > result[j].ID
		})
		return result, nil
	}

	// 缓存未初始化时回退到数据库查询
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	var dss []TSpiderDataSource
	query := database.DB.Model(&TSpiderDataSource{})

	// 权限过滤
	if !isAdmin {
		query = query.Where("user_id = ? OR user_id = 0", userID)
	}

	// ID 精确匹配
	if id > 0 {
		query = query.Where("id = ?", id)
	}

	// PlatformName 模糊匹配
	if platformName != "" {
		query = query.Where("platform_name LIKE ?", "%"+platformName+"%")
	}

	// Status 精确匹配
	if status != nil {
		query = query.Where("status = ?", *status)
	}

	// 限制最大返回数量，防止内存溢出和MySQL IO过载
	const maxDataSourceLimit = 1000
	if err := query.Order("id DESC").Limit(maxDataSourceLimit).Find(&dss).Error; err != nil {
		return nil, fmt.Errorf("查询数据源列表失败: %w", err)
	}

	return dss, nil
}

// stringsContainsIgnoreCase 不区分大小写的字符串包含检查
func stringsContainsIgnoreCase(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// ListSpiderDataSources 列出所有数据源（管理员看到所有，用户看到自己的+公共的）
// 保持向后兼容，内部调用 ListSpiderDataSourcesWithFilter
func ListSpiderDataSources(userID uint64, isAdmin bool) ([]TSpiderDataSource, error) {
	return ListSpiderDataSourcesWithFilter(userID, isAdmin, 0, "", nil)
}

// ListEnabledSpiderDataSources 列出所有启用的数据源（用于调度器）
func ListEnabledSpiderDataSources() ([]TSpiderDataSource, error) {
	// 优先使用缓存
	cachedList := GetCachedSpiderDataSourcesList()
	if len(cachedList) > 0 {
		var result []TSpiderDataSource
		for _, ds := range cachedList {
			if ds.Status == 1 {
				result = append(result, *ds)
			}
		}
		return result, nil
	}

	// 缓存未初始化时回退到数据库查询
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	var dss []TSpiderDataSource
	if err := database.DB.Where("status = 1").Find(&dss).Error; err != nil {
		return nil, fmt.Errorf("查询启用数据源失败: %w", err)
	}

	return dss, nil
}

// ToggleSpiderDataSourceStatus 切换数据源状态
func ToggleSpiderDataSourceStatus(id uint64, status int) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	if err := database.DB.Model(&TSpiderDataSource{}).Where("id = ?", id).
		Update("status", status).Error; err != nil {
		return fmt.Errorf("切换状态失败: %w", err)
	}

	// 增量更新缓存中的状态
	UpdateCachedSpiderDataSourceStatus(id, status)

	return nil
}

// BatchToggleSpiderDataSourceStatus 批量切换数据源状态
func BatchToggleSpiderDataSourceStatus(ids []uint64, status int) (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}
	if len(ids) == 0 {
		return 0, nil
	}

	result := database.DB.Model(&TSpiderDataSource{}).Where("id IN ?", ids).
		Update("status", status)
	if result.Error != nil {
		return 0, fmt.Errorf("批量切换状态失败: %w", result.Error)
	}

	// 增量更新缓存中的状态
	for _, id := range ids {
		UpdateCachedSpiderDataSourceStatus(id, status)
	}

	return result.RowsAffected, nil
}

// ============================================================================
// 每日信息 CRUD（优化版本）
// ============================================================================

// SaveSpiderDailyInfo 保存爬取的信息（v2.0.5：写入单表）
//
// v2.0.24：在 database.DB 层兜底校验 title / url / content 非空；
// 防止 MCP handler 被绕过或调用方忘了校验时把空记录写进 t_spider_daily_info。
// 双层防护原则：handler 校验（友好错误） + database.DB 校验（兜底），任一层都不依赖对方。
func SaveSpiderDailyInfo(info *TSpiderDailyInfo) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	if IsEmptySpiderDailyInfo(info) {
		return fmt.Errorf("拒绝保存空记录：data_source_id=%d，title/url/content 至少一项为空", info.DataSourceID)
	}

	if err := database.DB.Table(SpiderDailyInfoTableName).Create(info).Error; err != nil {
		return fmt.Errorf("保存爬取信息失败: %w", err)
	}

	// v2.0.24：写入后失效列表缓存，避免 2 分钟 TTL 窗口内前端看到不一致状态。
	invalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")

	return nil
}

// IsEmptySpiderDailyInfo 判断一条记录是否为空（v2.0.24）
//
// 「空」的定义：title / url / content 全部为空（TrimSpace 后）。
// 只要其中任一项有非空白内容就视为有效；这是从 ID=544 空记录事件总结出的
// 最小业务不变量：用户看到的每日信息必须能告诉 Agent「这是什么、链接是什么、
// 正文讲什么」，三项缺一不可。
func IsEmptySpiderDailyInfo(info *TSpiderDailyInfo) bool {
	if info == nil {
		return true
	}
	return strings.TrimSpace(info.Title) == "" ||
		strings.TrimSpace(info.URL) == "" ||
		strings.TrimSpace(info.Content) == ""
}

// CleanupEmptySpiderDailyInfos 一次性清理历史空记录（v2.0.24）
//
// 用法：LSM_CLEANUP_EMPTY_DAILY_INFO=1 启动钩子在 main.go init 路径调用一次。
// WHERE 条件覆盖 title/url/content 任一为空的记录，与 IsEmptySpiderDailyInfo
// 保持一致；返回删除条数 + 错误，供运维判断清理是否彻底。
func CleanupEmptySpiderDailyInfos() (int64, error) {
	if database.DB == nil {
		return 0, fmt.Errorf("数据库未初始化")
	}

	result := database.DB.Table(SpiderDailyInfoTableName).
		Where("TRIM(title) = '' OR TRIM(url) = '' OR TRIM(content) = ''").
		Unscoped().
		Delete(&TSpiderDailyInfo{})
	if result.Error != nil {
		return 0, fmt.Errorf("清理空记录失败: %w", result.Error)
	}

	if result.RowsAffected > 0 {
		logger.Printf("[SPIDER-CLEANUP] removed %d empty daily info records", result.RowsAffected)
		// 清理后失效缓存，避免前端 2 分钟内继续看到已删记录。
		invalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")
	}

	return result.RowsAffected, nil
}

// monthQueryResult 单个月份的查询结果
type monthQueryResult struct {
	infos []TSpiderDailyInfo
	count int64
	err   error
}

// QuerySpiderDailyInfo 分页查询爬取的信息（v2.0.5：单表查询）
func QuerySpiderDailyInfo(
	userID uint64,
	isAdmin bool,
	page int,
	pageSize int,
	platformFilter string,
	startDate time.Time,
	endDate time.Time,
) ([]TSpiderDailyInfo, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("数据库未初始化")
	}

	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}

	// 尝试从缓存获取
	cacheKey := makeSpiderDailyInfoCacheKey(userID, isAdmin, page, pageSize, platformFilter, startDate, endDate)
	if cached, ok := getSpiderDailyInfoFromCache(cacheKey); ok {
		return cached.Infos, cached.Total, nil
	}

	// 获取可访问的数据源ID列表（一次查询）
	dsIDs, err := getAccessibleDataSourceIDs(userID, isAdmin)
	if err != nil || len(dsIDs) == 0 {
		return []TSpiderDailyInfo{}, 0, nil
	}

	// 单表查询
	query := database.DB.Table(SpiderDailyInfoTableName).Where("data_source_id IN ?", dsIDs)
	if platformFilter != "" {
		query = query.Where("platform_name = ?", platformFilter)
	}
	if !startDate.IsZero() {
		query = query.Where("crawl_time >= ?", startDate)
	}
	if !endDate.IsZero() {
		endCopy := endDate
		if endCopy.Hour() == 0 && endCopy.Minute() == 0 && endCopy.Second() == 0 {
			endCopy = endCopy.Add(24*time.Hour - time.Second)
		}
		query = query.Where("crawl_time <= ?", endCopy)
	}

	// COUNT
	var totalCount int64
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, fmt.Errorf("统计总数失败: %w", err)
	}

	if totalCount == 0 {
		return []TSpiderDailyInfo{}, 0, nil
	}

	// 分页查询
	var infos []TSpiderDailyInfo
	offset := (page - 1) * pageSize
	if err := query.
		Select("id, created_at, updated_at, data_source_id, platform_name, title, crawl_time, url").
		Order("crawl_time DESC").
		Limit(pageSize).
		Offset(offset).
		Find(&infos).Error; err != nil {
		return nil, 0, fmt.Errorf("查询列表失败: %w", err)
	}

	// 写入缓存
	setSpiderDailyInfoToCache(cacheKey, infos, totalCount)

	return infos, totalCount, nil
}

// GetSpiderDailyInfoByID 获取单条信息详情（v2.0.5：单表，不需要 crawlTime）
func GetSpiderDailyInfoByID(id uint64) (*TSpiderDailyInfo, error) {
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	var info TSpiderDailyInfo
	// 优化：排除大字段 raw_data，按需加载
	if err := database.DB.Table(SpiderDailyInfoTableName).Select("id, created_at, updated_at, data_source_id, platform_name, title, title_zh, content, content_zh, crawl_time, url, translated_at").First(&info, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("查询详情失败: %w", err)
	}

	return &info, nil
}

// GetSpiderDailyInfoContent 获取单条信息的 content 和 raw_data（v2.0.5：单表，不需要 crawlTime）
func GetSpiderDailyInfoContent(id uint64) (string, string, error) {
	if database.DB == nil {
		return "", "", fmt.Errorf("数据库未初始化")
	}

	var info TSpiderDailyInfo
	if err := database.DB.Table(SpiderDailyInfoTableName).Select("content, raw_data").First(&info, id).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", "", nil
		}
		return "", "", fmt.Errorf("查询内容失败: %w", err)
	}

	return info.Content, info.RawData, nil
}

// DeleteSpiderDailyInfo 硬删除每日信息（v2.0.5：单表，不需要 crawlTime）
//
// v2.0.24：删除后立即失效列表缓存，避免 2 分钟 TTL 窗口内前端继续看到已删记录。
func DeleteSpiderDailyInfo(id uint64) error {
	if database.DB == nil {
		return fmt.Errorf("数据库未初始化")
	}

	if err := database.DB.Table(SpiderDailyInfoTableName).Unscoped().Delete(&TSpiderDailyInfo{}, id).Error; err != nil {
		return fmt.Errorf("删除信息失败: %w", err)
	}

	invalidateSpiderDailyInfoCacheByPrefix("spider_daily_info:")

	return nil
}

// MCPGetSpiderDailyInfoRequest MCP 查询请求结构体（供 QuerySpiderDailyInfoForMCP 使用）
// 0 值 / 空字符串 / nil 均表示"不参与过滤"。
type MCPGetSpiderDailyInfoRequest struct {
	ID             uint64   `json:"id,omitempty"`               // 单条查询：精确匹配
	IDs            []uint64 `json:"ids,omitempty"`              // 批量查询：ID 列表（最多 100）
	DataSourceID   uint64   `json:"data_source_id,omitempty"`   // 数据源 ID
	PlatformName   string   `json:"platform_name,omitempty"`    // 平台名称（模糊匹配）
	Title          string   `json:"title,omitempty"`            // 标题（模糊匹配）
	URL            string   `json:"url,omitempty"`              // URL（精确匹配）
	CrawlTimeStart string   `json:"crawl_time_start,omitempty"` // 爬取时间起始（ISO 8601）
	CrawlTimeEnd   string   `json:"crawl_time_end,omitempty"`   // 爬取时间截止（ISO 8601）
	IncludeRawData bool     `json:"include_raw_data,omitempty"` // 是否返回 raw_data
	Page           int      `json:"page,omitempty"`             // 页码，默认 1
	PageSize       int      `json:"page_size,omitempty"`        // 每页条数，默认 20，上限 100
}

// QuerySpiderDailyInfoForMCP 灵活多维度查询爬取信息（供 /GetSpiderDailyInfo 接口使用）
// 支持：单条（id）、批量（ids）、分页 + 多维度过滤。
// 0 值 / 空字符串 / nil 均不参与 WHERE 条件。
func QuerySpiderDailyInfoForMCP(req MCPGetSpiderDailyInfoRequest) ([]TSpiderDailyInfo, int64, error) {
	if database.DB == nil {
		return nil, 0, fmt.Errorf("数据库未初始化")
	}

	// ---- 单条查询 ----
	if req.ID > 0 {
		var info TSpiderDailyInfo
		q := database.DB.Table(SpiderDailyInfoTableName)
		if !req.IncludeRawData {
			q = q.Select("id, created_at, updated_at, data_source_id, platform_name, title, title_zh, content, content_zh, crawl_time, url, translated_at")
		}
		if err := q.First(&info, req.ID).Error; err != nil {
			if err == gorm.ErrRecordNotFound {
				return []TSpiderDailyInfo{}, 0, nil
			}
			return nil, 0, fmt.Errorf("查询详情失败: %w", err)
		}
		return []TSpiderDailyInfo{info}, 1, nil
	}

	// ---- 批量查询（最多 100 条，不走分页） ----
	if len(req.IDs) > 0 {
		if len(req.IDs) > 100 {
			req.IDs = req.IDs[:100]
		}
		var infos []TSpiderDailyInfo
		q := database.DB.Table(SpiderDailyInfoTableName).Where("id IN ?", req.IDs)
		if !req.IncludeRawData {
			q = q.Select("id, created_at, updated_at, data_source_id, platform_name, title, title_zh, content, content_zh, crawl_time, url, translated_at")
		}
		if err := q.Order("crawl_time DESC").Find(&infos).Error; err != nil {
			return nil, 0, fmt.Errorf("批量查询失败: %w", err)
		}
		return infos, int64(len(infos)), nil
	}

	// ---- 分页 + 多维度过滤 ----
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 20
	}
	if req.PageSize > 100 {
		req.PageSize = 100
	}

	query := database.DB.Table(SpiderDailyInfoTableName)

	if req.DataSourceID > 0 {
		query = query.Where("data_source_id = ?", req.DataSourceID)
	}
	if req.PlatformName != "" {
		query = query.Where("platform_name LIKE ?", "%"+req.PlatformName+"%")
	}
	if req.Title != "" {
		query = query.Where("title LIKE ?", "%"+req.Title+"%")
	}
	if req.URL != "" {
		query = query.Where("url = ?", req.URL)
	}
	if req.CrawlTimeStart != "" {
		if t, err := time.Parse(time.RFC3339, req.CrawlTimeStart); err == nil {
			query = query.Where("crawl_time >= ?", t.UTC())
		}
	}
	if req.CrawlTimeEnd != "" {
		if t, err := time.Parse(time.RFC3339, req.CrawlTimeEnd); err == nil {
			endCopy := t.UTC()
			if endCopy.Hour() == 0 && endCopy.Minute() == 0 && endCopy.Second() == 0 {
				endCopy = endCopy.Add(24*time.Hour - time.Second)
			}
			query = query.Where("crawl_time <= ?", endCopy)
		}
	}

	// COUNT
	var totalCount int64
	if err := query.Count(&totalCount).Error; err != nil {
		return nil, 0, fmt.Errorf("统计总数失败: %w", err)
	}
	if totalCount == 0 {
		return []TSpiderDailyInfo{}, 0, nil
	}

	// SELECT + 分页
	if !req.IncludeRawData {
		query = query.Select("id, created_at, updated_at, data_source_id, platform_name, title, title_zh, content, content_zh, crawl_time, url, translated_at")
	}

	var infos []TSpiderDailyInfo
	offset := (req.Page - 1) * req.PageSize
	if err := query.Order("crawl_time DESC").Limit(req.PageSize).Offset(offset).Find(&infos).Error; err != nil {
		return nil, 0, fmt.Errorf("查询列表失败: %w", err)
	}

	return infos, totalCount, nil
}

// GetDistinctSpiderPlatforms 获取去重的平台名称列表（从缓存获取，避免查询数据库）
func GetDistinctSpiderPlatforms(userID uint64, isAdmin bool) ([]string, error) {
	// 优先使用缓存
	cachedList := GetCachedSpiderDataSourcesList()
	if len(cachedList) > 0 {
		platformMap := make(map[string]bool)
		for _, ds := range cachedList {
			if isAdmin {
				// 管理员看所有
				platformMap[ds.PlatformName] = true
			} else {
				// 普通用户看自己的 + 公共的
				if ds.UserID == userID || ds.UserID == 0 {
					platformMap[ds.PlatformName] = true
				}
			}
		}
		// 转成 slice
		var platforms []string
		for p := range platformMap {
			if p != "" {
				platforms = append(platforms, p)
			}
		}
		return platforms, nil
	}

	// 缓存未初始化时回退到数据库查询
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	// 从数据源表获取（更准确）
	var platforms []string
	query := database.DB.Model(&TSpiderDataSource{}).Distinct("platform_name")

	if !isAdmin {
		query = query.Where("user_id = ? OR user_id = 0", userID)
	}

	if err := query.Pluck("platform_name", &platforms).Error; err != nil {
		return nil, fmt.Errorf("查询平台列表失败: %w", err)
	}

	return platforms, nil
}

// ============================================================================
// 辅助函数
// ============================================================================

// getAccessibleDataSourceIDs 获取用户可访问的数据源ID列表（优化版本）
func getAccessibleDataSourceIDs(userID uint64, isAdmin bool) ([]uint64, error) {
	// 先尝试从缓存获取
	cachedList := GetCachedSpiderDataSourcesList()
	if len(cachedList) > 0 {
		var ids []uint64
		for _, ds := range cachedList {
			if isAdmin {
				ids = append(ids, ds.ID)
			} else {
				if ds.UserID == userID || ds.UserID == 0 {
					ids = append(ids, ds.ID)
				}
			}
		}
		return ids, nil
	}

	// 缓存未命中时查询数据库
	if database.DB == nil {
		return nil, fmt.Errorf("数据库未初始化")
	}

	var ids []uint64
	query := database.DB.Model(&TSpiderDataSource{}).Select("id")

	if !isAdmin {
		query = query.Where("user_id = ? OR user_id = 0", userID)
	}

	if err := query.Pluck("id", &ids).Error; err != nil {
		return nil, err
	}

	return ids, nil
}

// ============================================================================
// SpiderDailyInfo 查询缓存（v2.0.5 新增）
// 参考 mysql_http_stats_cache.go 的缓存模式，避免重复查询MySQL分表
// ============================================================================

// SpiderDailyInfoCacheEntry 爬虫每日信息缓存条目
type SpiderDailyInfoCacheEntry struct {
	Infos    []TSpiderDailyInfo
	Total    int64
	CachedAt time.Time
}

// IsExpired 检查缓存是否过期（默认TTL 2分钟）
func (e *SpiderDailyInfoCacheEntry) IsExpired(ttl time.Duration) bool {
	if ttl <= 0 {
		ttl = 2 * time.Minute
	}
	return time.Since(e.CachedAt) > ttl
}

// spiderDailyInfoCache 爬虫每日信息查询缓存
var spiderDailyInfoCache = struct {
	entries map[string]*SpiderDailyInfoCacheEntry
	mu      sync.RWMutex
}{}

// spiderDailyInfoCacheTTL 缓存默认TTL（2分钟，平衡实时性与性能）
const spiderDailyInfoCacheTTL = 2 * time.Minute

// makeSpiderDailyInfoCacheKey 生成缓存键
// 格式: "spider_daily_info:{userID}:{isAdmin}:{page}:{pageSize}:{platformFilter}:{startDate}:{endDate}"
func makeSpiderDailyInfoCacheKey(userID uint64, isAdmin bool, page, pageSize int, platformFilter string, startDate, endDate time.Time) string {
	startStr := ""
	if !startDate.IsZero() {
		startStr = startDate.Format("20060102")
	}
	endStr := ""
	if !endDate.IsZero() {
		endStr = endDate.Format("20060102")
	}
	return fmt.Sprintf("spider_daily_info:%d:%v:%d:%d:%s:%s:%s",
		userID, isAdmin, page, pageSize, platformFilter, startStr, endStr)
}

// getSpiderDailyInfoFromCache 从缓存获取查询结果
func getSpiderDailyInfoFromCache(key string) (*SpiderDailyInfoCacheEntry, bool) {
	spiderDailyInfoCache.mu.RLock()
	defer spiderDailyInfoCache.mu.RUnlock()

	if spiderDailyInfoCache.entries == nil {
		return nil, false
	}

	entry, ok := spiderDailyInfoCache.entries[key]
	if !ok || entry == nil {
		return nil, false
	}

	if entry.IsExpired(spiderDailyInfoCacheTTL) {
		return nil, false
	}

	return entry, true
}

// setSpiderDailyInfoToCache 将查询结果写入缓存
func setSpiderDailyInfoToCache(key string, infos []TSpiderDailyInfo, total int64) {
	spiderDailyInfoCache.mu.Lock()
	defer spiderDailyInfoCache.mu.Unlock()

	if spiderDailyInfoCache.entries == nil {
		spiderDailyInfoCache.entries = make(map[string]*SpiderDailyInfoCacheEntry)
	}

	spiderDailyInfoCache.entries[key] = &SpiderDailyInfoCacheEntry{
		Infos:    infos,
		Total:    total,
		CachedAt: time.Now(),
	}
}

// invalidateSpiderDailyInfoCache 使指定键的缓存失效
func invalidateSpiderDailyInfoCache(key string) {
	spiderDailyInfoCache.mu.Lock()
	defer spiderDailyInfoCache.mu.Unlock()

	if spiderDailyInfoCache.entries == nil {
		return
	}

	delete(spiderDailyInfoCache.entries, key)
}

// invalidateSpiderDailyInfoCacheByPrefix 按前缀使缓存失效（用于数据写入后批量清除）
func invalidateSpiderDailyInfoCacheByPrefix(prefix string) {
	spiderDailyInfoCache.mu.Lock()
	defer spiderDailyInfoCache.mu.Unlock()

	if spiderDailyInfoCache.entries == nil {
		return
	}

	for key := range spiderDailyInfoCache.entries {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			delete(spiderDailyInfoCache.entries, key)
		}
	}
}

// cleanExpiredSpiderDailyInfoCache 清理过期缓存条目（可定时调用）
func cleanExpiredSpiderDailyInfoCache() {
	spiderDailyInfoCache.mu.Lock()
	defer spiderDailyInfoCache.mu.Unlock()

	if spiderDailyInfoCache.entries == nil {
		return
	}

	now := time.Now()
	for key, entry := range spiderDailyInfoCache.entries {
		if now.Sub(entry.CachedAt) > spiderDailyInfoCacheTTL {
			delete(spiderDailyInfoCache.entries, key)
		}
	}
}
