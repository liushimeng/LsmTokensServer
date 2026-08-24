# MCP GetSpiderDataSource 接口定义

**版本**: v2.0.4  
**接口路径**: `/GetSpiderDataSource`  
**请求方法**: POST 或 GET  
**服务地址**: `http://localhost:29002`

## 概述

获取爬虫数据源列表，支持多维度过滤查询（AND 关系）。Agent **必须跳过 `status=0`** 的数据源，并仔细阅读每个数据源的 `description` 字段确定处理策略。

## 请求参数

```json
{
  "user_id":       { "type": "number",  "description": "用户 ID，默认 0（公共）" },
  "is_admin":      { "type": "boolean", "description": "是否管理员，默认 false" },
  "id":            { "type": "number",  "description": "数据源 ID，精确匹配" },
  "platform_name": { "type": "string",  "description": "平台名称，模糊匹配（不区分大小写）" },
  "status":        { "type": "number",  "description": "状态筛选：1=启用，0=禁用" }
}
```

## 过滤示例

```json
{ "user_id": 1, "is_admin": false, "id": 6 }                              // 精确 ID
{ "user_id": 1, "is_admin": false, "platform_name": "机器" }              // 模糊匹配
{ "user_id": 1, "is_admin": false, "status": 1 }                          // 仅启用
{ "user_id": 1, "is_admin": false, "platform_name": "Tech", "status": 1 }  // 组合
```

GET：`/GetSpiderDataSource?user_id=1&is_admin=false&id=6`

## 响应格式

```json
{
  "success": { "type": "boolean" },
  "message": { "type": "string" },
  "data": [{
    "id":            1,
    "user_id":       0,
    "platform_name": "机器之心",
    "url_address":   "https://www.jiqizhixin.com",
    "description":   "1. 抓取首页文章列表...2. 字数控制在 2000 以内",
    "remark":        "中文AI资讯平台",
    "status":        1
  }]
}
```

**失败**：`{ "success": false, "message": "Failed to get data sources: ..." }`

## 权限与过滤

| 角色 | 可见范围 |
|------|----------|
| 管理员（`is_admin=true`） | 全部数据源 |
| 普通用户（`is_admin=false`） | 自己的数据源 + 公共数据源（`user_id=0`） |

| 过滤字段 | 匹配方式 | 示例 |
|----------|----------|------|
| `id` | 精确 | `id=6` |
| `platform_name` | 模糊（不区分大小写） | `platform_name=Tech` → TechCrunch / TechNews |
| `status` | 精确 | `status=1` |

## Description 解析指南

Agent 处理网页的唯一依据。必须：

1. **优先**阅读并理解 `description`
2. **完整**执行所有规则，不能选择性执行
3. **自行**解析（v2.0.0 起服务端不再解析）

### 关键词对照

| 关键词 | 含义 | Agent 动作 |
|--------|------|------------|
| 英文网站 / 翻译成中文 | 需要翻译 | 启用翻译（Agent 自己实现） |
| 抓取文章列表 / 提取文章列表 | 需要获取列表 | 提取链接 + 多轮访问子页面 |
| 滚动更新 / 鼠标滚动 | 滚动加载 | 用 scroll action |
| 点击链接 / 打开子页面 | 需要访问详情页 | navigate/click 多轮访问 |
| 过滤内容 / 移除广告 | 需要清理 | 提取后按要求过滤 |
| 控制在 X 字以内 | 长度限制 | 字数截断 |
| 最新 N 小时/天 | 时间窗口 | 只保留窗口内内容 |
| 业界/人工智能/智能驾驶 | 导航关键词 | 优先点击含这些关键词的链接/栏目 |

### 标准模板

```
[平台类型]，执行以下处理：
1. 提取[指定内容区域]
2. 过滤[无关内容]
3. 优先查找包含'[关键词1]'、'[关键词2]'的内容
4. 摘要长度控制在[最小]-[最大]字符
5. [额外要求]
```

## Agent 处理流程

1. 调用此接口获取数据源列表（可用过滤参数缩小范围）
2. **解析 Description**（强制第一步）
3. 识别处理规则：是否翻译、是否多轮交互等
4. 跳过 `status=0`（也可直接 `status=1` 筛选）
5. 调用 `/SpiderWebData` 爬取原始内容（传入 `data_source_id`）
6. Agent 处理数据：清洗、提取、过滤、截断、翻译
7. 调用 `/InputSpiderDailyInfo` 保存结果（必须）

## 错误码

| HTTP | 说明 |
|------|------|
| 200 | 请求处理 |
| 405 | 方法不允许 |
