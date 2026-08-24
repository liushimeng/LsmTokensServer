# MCP GetSpiderDailyInfo 接口定义

**版本**: v2.0.5  
**接口路径**: `/GetSpiderDailyInfo`  
**请求方法**: POST 或 GET  
**服务地址**: `http://localhost:29002`

## 概述

查询 `/InputSpiderDailyInfo` 已保存的爬取数据。三种查询模式（互斥优先）：

| 模式 | 触发条件 | 行为 |
|------|----------|------|
| 单条 | `id > 0` | 返回单条，忽略其他过滤/分页 |
| 批量 | `ids` 非空 | 返回 ID 列表对应记录（最多 100 条） |
| 分页 | 其他 | 按过滤条件 + 分页返回 |

> 零值字段（`0`/空串/`nil`）不参与过滤。

## 请求参数

```json
{
  "id":              { "type": "number", "description": "单条查询 ID，精确匹配" },
  "ids":             { "type": "array",  "items": { "type": "number" }, "description": "批量查询 ID 列表，最多 100" },
  "data_source_id":  { "type": "number", "description": "数据源 ID（0=不过滤）" },
  "platform_name":   { "type": "string", "description": "平台名称，模糊匹配" },
  "title":           { "type": "string", "description": "标题，模糊匹配" },
  "url":             { "type": "string", "description": "原文链接，精确匹配" },
  "crawl_time_start":{ "type": "string", "description": "爬取时间起始 ISO 8601" },
  "crawl_time_end":  { "type": "string", "description": "爬取时间截止 ISO 8601（00:00:00 扩展到当天 23:59:59）" },
  "include_raw_data":{ "type": "boolean","description": "是否返回 raw_data（默认 false）" },
  "page":            { "type": "number", "description": "页码，默认 1" },
  "page_size":       { "type": "number", "description": "每页条数，默认 20，上限 100" }
}
```

## 请求示例

```json
{ "id": 42 }                                           // 单条
{ "ids": [1, 5, 12, 38] }                              // 批量
{ "data_source_id": 1, "page": 1, "page_size": 10 }   // 分页
{ "data_source_id": 1, "platform_name": "机器", "crawl_time_start": "2026-06-15T00:00:00Z", "include_raw_data": true, "page": 1, "page_size": 5 }
```

GET：`/GetSpiderDailyInfo?id=42` / `?ids=1,5,12,38` / `?data_source_id=1&page=1&page_size=10`

## 响应格式

```json
{
  "success":  { "type": "boolean" },
  "message":  { "type": "string" },
  "data": {
    "items": [{
      "id": 42,
      "created_at":   "2026-06-23T08:30:00Z",
      "updated_at":   "2026-06-23T08:30:00Z",
      "data_source_id": 1,
      "platform_name":  "机器之心",
      "title":          "...",
      "title_zh":       "...",
      "content":        "...",
      "content_zh":     "...",
      "raw_data":       "...",
      "crawl_time":     "2026-06-23T08:00:00Z",
      "url":            "https://...",
      "translated_at":  "..."
    }],
    "total_count": 42,
    "page":        1,
    "page_size":   20
  }
}
```

**失败**：`{ "success": false, "message": "Failed to query daily info: ..." }`

## 过滤维度

| 字段 | 匹配方式 | 说明 |
|------|----------|------|
| `data_source_id` | 精确 | 0=不参与 |
| `platform_name` | 模糊 | 例 "机器" 匹配 "机器之心" |
| `title` | 模糊 | 例 "AI" 匹配包含 AI 的标题 |
| `url` | 精确 | 完整 URL 匹配 |
| `crawl_time_start` | ≥ | ISO 8601 |
| `crawl_time_end` | ≤ | 同上，00:00:00 自动扩展到 23:59:59 |

多个条件 AND 组合。结果按 `crawl_time DESC` 排序。

## 最佳实践

- 验证写入：`{ "data_source_id": 1 }`
- 获取最新 N 条：`{ "data_source_id": 1, "page_size": 5 }`
- 按时间范围：`{ "crawl_time_start": "2026-06-20T00:00:00Z" }`
- 标题搜索：`{ "title": "AI" }`
- 查看原始数据：`{ "id": 42, "include_raw_data": true }`

## 错误码

| HTTP | 说明 |
|------|------|
| 200 | 请求处理（空结果也 200） |
| 405 | 方法不允许 |

## 注意事项

1. 默认不返回 `raw_data`（longtext），需要时显式传 `include_raw_data:true`
2. `ids` 最多 100 条，超出截断
3. `page_size` 范围 1-100，默认 20
4. `total_count` 不受分页影响
5. ISO 8601 UTC
6. `title_zh`/`content_zh`/`translated_at` 为 v1.3.0+ 字段，历史数据可能为空
