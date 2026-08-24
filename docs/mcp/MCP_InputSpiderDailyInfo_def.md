# MCP InputSpiderDailyInfo 接口定义

**版本**: v2.0.0  
**接口路径**: `/InputSpiderDailyInfo`  
**请求方法**: POST  
**服务地址**: `http://localhost:29002`

## 概述

将爬取到的数据保存到数据库，按月份分表存储。v2.0.0 起 **Agent 必须显式调用**（无自动保存）。

## 请求参数

```json
{
  "data_source_id": { "type": "number", "description": "数据源 ID，必填" },
  "platform_name":  { "type": "string", "description": "平台名称" },
  "title":          { "type": "string", "description": "标题（Agent 处理后）" },
  "content":        { "type": "string", "description": "内容摘要（Agent 处理后）" },
  "raw_data":       { "type": "string", "description": "原始 HTML/JSON，建议保存以便重处理" },
  "crawl_time":     { "type": "string", "format": "date-time", "description": "爬取时间 ISO 8601，默认当前时间" },
  "url":            { "type": "string", "description": "原文链接" }
}
```

## 请求示例

```json
{
  "data_source_id": 8,
  "platform_name": "Example AI",
  "title":   "AI News Roundup",
  "content": "Article 1: ...\n\n---\n\nArticle 2: ...",
  "raw_data":"<!DOCTYPE html>...",
  "crawl_time": "2026-06-17T14:30:00Z",
  "url": "https://example.com/category/ai"
}
```

## 响应格式

成功：`{ "success": true, "message": "Daily info saved" }`
失败：`{ "success": false, "message": "Failed to save daily info: ..." }`

## 分表说明

按 `crawl_time` 月份分表，表名格式 `TSpiderDailyInfo_YYYYMM`：

| 爬取时间 | 分表名 |
| -------- | ------ |
| 2026-06-15 | TSpiderDailyInfo_202606 |
| 2026-07-01 | TSpiderDailyInfo_202607 |

分表自动创建，含索引：`idx_created_at`、`idx_updated_at`、`idx_data_source_id`、`idx_platform_name`、`idx_crawl_time`、`idx_ds_crawl_time`、`idx_platform_crawl_time`。

## 数据库字段

| 字段 | 类型 | 说明 |
|------|------|------|
| id | bigint unsigned | 主键自增 |
| created_at | datetime | 创建时间 |
| updated_at | datetime | 更新时间 |
| data_source_id | bigint unsigned | 数据源 ID |
| platform_name | varchar(64) | 平台名称 |
| title | varchar(512) | 标题 |
| content | text | 内容摘要 |
| raw_data | longtext | 原始数据 |
| crawl_time | datetime | 爬取时间 |
| url | varchar(512) | 原文链接 |

## Agent 处理流程

1. `/GetSpiderDataSource` 获取数据源列表
2. 解析 `description` 确定处理策略
3. `/SpiderWebData` 爬取原始网页（可多轮 `session_id`）
4. Agent 处理数据：清洗、提取、过滤、截断、翻译
5. **必须调用本接口**保存最终结果

## 错误码

| HTTP | 说明 |
|------|------|
| 200 | 处理完成 |
| 405 | 方法不允许 |

## 注意事项

1. `data_source_id` 唯一必填
2. `crawl_time` ISO 8601，建议 UTC
3. 同数据多次调用会创建多条记录（不会覆盖）
4. v2.0.0 移除了 `translated_title` / `translated_content` / `language` / `use_translated` 字段（忽略旧字段即可）
