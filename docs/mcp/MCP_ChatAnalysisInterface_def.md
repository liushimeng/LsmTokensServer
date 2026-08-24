# MCP ChatAnalysisInterface 接口定义

**版本**: v2.0.68
**接口路径**: `/ChatAnalysisInterface`、`/ChatAnalysisDetailInterface`、`/ChatAnalysisDstModelsInterface`、`/ChatAnalysisAgentToolsInterface`
**请求方法**: POST（`/ChatAnalysisDetailInterface` 同时支持 GET）
**服务地址**: 管理端 Web `http://localhost:9101` ／ 用户端 Web `http://localhost:29001`

> 仅供本地 Agent（Claude Code / Kilo Code / OpenCode / pi / OpenClaw 等）通过 MCP 调用 `LsmTokensServer` 的**浏览记录（ChatAnalysis）**进行数据查询。本文档配套现有 `/ChatAnalysis` 管理页面（`server_api_manager_chat_analysis.go`）与用户页面（`server_api_user_chat_analysis.go`）。

---

## 1. 适用场景

Agent 通过本组接口查询 `TAgentHttpTransactionDataItem` 表里**某个用户+某个模型**最近的请求流水明细，典型用途：

- 排查某次模型调用的完整请求/响应（详情接口按字段懒加载）
- 按 URL / Method / HTTP Status / 协议 / 工具名等条件过滤
- 取目标模型下拉、Agent 工具下拉、详情长字段等

数据源 = `LsmTokensServer` 自管的哈希分表（`cfg.DBMysqlSubTableNumber` 决定分表数，默认 16）。不分页、不命中 longtext 字段。

---

## 2. 默认账号与 Token 获取

### 2.1 默认账号

| 角色 | 字段 | 默认值 |
|------|------|--------|
| 管理员用户名 | `user_name` | `liusm191` |
| 管理员密码 | `password` | **Agent 必须向用户索要**（生产值在 `LsmTokensServer.conf` / `TAgentHttpAdminInfo` 表中维护，**不在代码中硬编码**） |
| 默认演示模型 | `model_name` | `liusm191-server-model` |
| 默认演示模型 ID | （数据库主键） | 由 `TAgentHttpUserModelInfo` 分配，`/ModelInfoInterface` 可查询 |

> 说明：用户「liusm191」是 `OpenAIAnalysis/OpenAIRawSamples.json` 中的演示用户名，仅用于帮助 Agent 拼装请求样例时给出真实存在的用户名。**密码 / Token 都不得写死在 MCP 文档里**。

### 2.2 获取管理员 Cookie / Token

管理端不走 JWT Cookie（`/ManagerLogin` 与 `/ManagerLoginInterface` 仅做页面登录校验，**未对外暴露 Token 端点**）。本组接口为「内部 API」：

- 当前实现**直接信任**请求体内的 `user_name` + `model_name`（参见 `chatAnalysisInterfaceHandle` 在 `server_api_manager_chat_analysis.go:64-145`）
- 由于调用入口为同机进程内的 Web 监听（管理端 9101 端口），**仅本机 / 同子网可信客户端**可访问；生产环境如对外暴露必须挂反向代理 + IP 白名单

> **特别警告**：调用前请确认 Agent 所在机器与 `http://localhost:9101`（或对应的 `managerWebListenPort`）网络可达。生产环境若端口为 `8.130.85.252:9101`，请替换为对应 `agentProductListenAddr`。

### 2.3 用户端 Token（如果 Agent 想改走用户端 29001）

用户端强校验 JWT（`getUserToken` 在 `server_api_user_login.go:407`），3 步：

1. `POST /UserLoginInterface`，body：`{"username": "...", "password": "...", "model_name": "..."}`
2. 从响应 `Set-Cookie` 头取 `lsm_user_token=<jwt>`（或者从响应 body 的 `data.token` 字段，由接口决定）
3. 后续请求带：

   ```text
   Cookie: lsm_user_token=<jwt>
   # 或
   Authorization: Bearer <jwt>
   ```

Token TTL = 24h（`userTokenExpireDuration = time.Hour * 24 * 1`）。过期重发登录。

---

## 3. 接口 1：分页查询 `/ChatAnalysisInterface`

按 `(user_name, model_name)` 在哈希分表中查询最新 N 条记录，按 `id DESC` 倒序（最新优先），与 `/ChatAnalysis` 页面行为一致。

### 3.1 URL

```text
POST http://localhost:9101/ChatAnalysisInterface
```

### 3.2 Request Headers

| Header | 必填 | 示例 | 说明 |
|--------|------|------|------|
| `Content-Type` | 是 | `application/json` | JSON 请求体 |
| `X-Request-ID` | 否 | `0123abcdef45` | 12 位小写 hex，与 `request_id` 规范一致 |

无 `Authorization`：管理端 API 当前不校验 JWT。

### 3.3 Request Body（`ChatAnalysisInterfaceRequest`）

字段定义见 `server_api_manager_chat_analysis.go:21-37`：

```json
{
  "user_name":        "liusm191",
  "model_name":       "liusm191-server-model",
  "page":             1,
  "page_size":        10,
  "filter_url":       "",
  "filter_method":    "",
  "filter_status":    "",
  "filter_status_not": false,
  "filter_protocol_type": 0,
  "filter_dst_model_name": "",
  "filter_tools":     "",
  "filter_agent_tool_name": "",
  "days":             3,
  "filter_input_tokens_nonzero":  0,
  "filter_output_tokens_nonzero": 0
}
```

字段说明：

| 字段 | 类型 | 必填 | 默认 | 说明 |
|------|------|------|------|------|
| `user_name` | string | 是 | – | 平台用户名（如 `liusm191`） |
| `model_name` | string | 是 | – | 平台配置模型名（如 `liusm191-server-model`） |
| `page` | int | 否 | 1 | 页码（小于 1 自动归 1） |
| `page_size` | int | 否 | 3 | 白名单：`1 / 3 / 5 / 10 / 15 / 20 / 50 / 100`，其他回落到 3 |
| `filter_url` | string | 否 | "" | 子串匹配 `request_url` |
| `filter_method` | string | 否 | "" | 精确匹配 HTTP Method（`GET`/`POST`/...） |
| `filter_status` | string | 否 | "" | **精确等值**匹配 `response_status`（SQL 是 `response_status = ?`）。入库值带原因短语，必须传 `"200 OK"`；传 `"200"` **返回 0 条** |
| `filter_status_not` | bool | 否 | false | true = 取「非此状态」（即 `status != filter_status`） |
| `filter_protocol_type` | int | 否 | 0 | `0=不限` / `1=OpenAI` / `2=Anthropic` / `3=OpenAI→Anthropic` / `4=Anthropic→OpenAI` |
| `filter_dst_model_name` | string | 否 | "" | 精确匹配 `dst_model_name`（调用 `/ChatAnalysisDstModelsInterface` 取可选值） |
| `filter_tools` | string | 否 | "" | 工具名匹配 `request_tools`（逗号分隔多个 = AND；按**完整工具名**匹配，不会用 `read` 误命中 `readFile`） |
| `filter_agent_tool_name` | string | 否 | "" | 精确匹配 `agent_tool_name`（调用 `/ChatAnalysisAgentToolsInterface` 取可选值） |
| `days` | int | 否 | 3 | 时间窗口白名单（见下表） |
| `filter_input_tokens_nonzero` | int | 否 | 0 | `0=全部` / `1=非零` / `2=为零` |
| `filter_output_tokens_nonzero` | int | 否 | 0 | `0=全部` / `1=非零` / `2=为零` |

#### `days` 白名单（`normalizeChatAnalysisDays` 在 `server_api_manager_chat_analysis.go:54-61`）

```text
{ -12, -6, -4, -2, -1, 0, 1, 3, 5, 7, 14, 30, 60, 90 }
```

| 值 | 语义 |
|----|------|
| `0` | **无限制**（不过滤 `created_at`） |
| `>0` N | 最近 N 天 |
| `<0` -N | 最近 N 小时（`-1=1小时`、`-12=12小时`） |
| 其它 | 回落到 `days=3` |

> 该白名单 v2.0.41 与 `/AIRouteManage` 对齐，避免脏数据触发大表全扫。

### 3.4 Response Headers

```text
Content-Type: application/json
Cache-Control: no-store, no-cache, must-revalidate
Pragma: no-cache
```

### 3.5 Response Body（`ChatAnalysisInterfaceResponse`）

字段定义见 `server_api_manager_chat_analysis.go:40-44` 与 `11-18`：

下面是**实测**（v2.0.68，`id=1008`）的真实返回，字段名以 `TAgentHttpTransactionDataItem`（`mysql_http_agent_model.go:89-155`）为准：

```json
{
  "success": true,
  "message": "查询成功",
  "data": {
    "records": [
      {
        "id":                            1008,
        "created_at":                    "2026-08-04T15:10:19.558+08:00",
        "updated_at":                    "2026-08-04T15:10:19.558+08:00",
        "deleted_at":                    null,

        "user_id":                       1,
        "user_name":                     "liusm191",
        "model_name":                    "liusm191-server-model",
        "dst_model_name":                "MiniMax-M3-highspeed",
        "api_key":                       "",
        "dst_endpoint_id":               44,
        "dst_endpoint_algorithm_type":   1,
        "protocol_type":                 1,

        "request_method":                "POST",
        "request_url":                   "https://api.minimaxi.com/anthropic/v1/messages?beta=true",
        "request_remote_addr":           "127.0.0.1:46942",
        "request_content_length":        253858,
        "request_headers":               "",
        "request_src_protocol_headers":  "",
        "request_body":                  "",
        "request_src_protocol_body":     "",

        "response_status":               "200 OK",
        "response_content_length":       1975,
        "response_headers":              "",
        "response_src_protocol_headers": "",
        "response_body":                 "",
        "response_src_protocol_body":    "",

        "tokens_input_size":             0,
        "tokens_output_size":            76,
        "tokens_all_size":               76,

        "elapsed_ms":                    2536,
        "request_start_at":              "2026-08-04T15:10:16.983+08:00",
        "request_end_at":                "2026-08-04T15:10:16.983+08:00",
        "response_start_at":             "2026-08-04T15:10:16.983+08:00",
        "response_end_at":               "2026-08-04T15:10:19.52+08:00",
        "tool_identifier":               "claude-cli/2.1.221 (external, cli)",

        "agent_tool_name":               "claude-cli",
        "agent_tool_info":               "...",

        "is_parsed":                     true,
        "is_task":                       false,
        "task_model":                    "",
        "is_stream":                     true,
        "has_system_prompt":             true,
        "has_tool_call":                 false,
        "message_count":                 12,
        "user_message_count":            5,

        "request_tools":                 "read,write,edit",
        "session_id":                    "unknown_session_id"
      }
    ],
    "totalCount":  87,
    "totalPages":  29,
    "currentPage": 1,
    "pageSize":    3
  }
}
```

字段语义：

| 字段 | 类型 | 说明 |
|------|------|------|
| `success` | bool | true = 查询成功 |
| `message` | string | 错误或成功文案 |
| `data.records` | array | 当前页记录（最多 `page_size` 条） |
| `data.totalCount` | int64 | 满足条件的总数（所有分表合并） |
| `data.totalPages` | int | `ceil(totalCount / pageSize)` |
| `data.currentPage` | int | 实际命中的页（`page` 超过 `totalPages` 时夹到末页） |
| `data.pageSize` | int | 实际使用的 `page_size`（非白名单被回落后也会回填这里） |

常用记录字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | uint64 | 主键，**调用详情接口的定位键** |
| `created_at` | RFC3339 | 记录写入时间（列表默认按此倒序，最新在前） |
| `dst_model_name` | string | 实际命中的源站模型名 |
| `dst_endpoint_id` | uint64 | 命中的源站接入点 ID |
| `dst_endpoint_algorithm_type` | int | `1=协议直连` / `2=协议转换器` |
| `protocol_type` | int | `1=Anthropic` / `2=OpenAI`（**注意与 `filter_protocol_type` 的取值含义不同**） |
| `response_status` | string | 形如 `"200 OK"`（**带原因短语，不是纯数字**） |
| `tokens_input_size` / `tokens_output_size` / `tokens_all_size` | uint64 | Tokens 统计（不是 `tokens_input`/`tokens_output`） |
| `elapsed_ms` | int64 | 总耗时 TTLT（毫秒） |
| `agent_tool_name` | string | `claude-cli` / `opencode` / `Kilo-Code` / `OpenClaw` / `pi` |
| `request_tools` | string | 逗号分隔的工具名列表 |
| `session_id` | string | `unknown_session_id` 表示未识别 |

> ⚠ **v2.0.39 / v2.0.42 N+1 规则**：`records[]` 中 6 个长字段（`request_headers` / `request_body` / `request_src_protocol_body` / `response_headers` / `response_body` / `response_src_protocol_body`）**恒为空字符串 `""`**，键存在但无内容。这不是数据缺失，而是列表 SQL 根本不 SELECT 这些列。需要时**单独**调用下方 `/ChatAnalysisDetailInterface` 懒加载。禁止一次性把整行 longtext 拉进上下文。

#### 失败响应

```json
{ "success": false, "message": "缺少 user_name 或 model_name 参数" }
{ "success": false, "message": "缺少 user_name 或 model_name 参数" }
{ "success": false, "message": "无效的请求体: <json err>" }
{ "success": false, "message": "查询失败: <driver err>" }
```

HTTP 状态：**恒为 200**（错误用 `success:false` 表示）。这是现有实现的契约，不要按 HTTP 4xx/5xx 判断业务失败。

---

## 4. 接口 2：单条详情字段懒加载 `/ChatAnalysisDetailInterface`

> **v2.0.42 强制规则**：6 个长字段（4 个 longtext + 2 个 text）必须按字段懒加载。**严禁**在 `/ChatAnalysisInterface` 的 records 里拉这些字段。

### 4.1 URL

```text
POST http://localhost:9101/ChatAnalysisDetailInterface
GET  http://localhost:9101/ChatAnalysisDetailInterface?id=<id>&user_name=...&model_name=...&field=...
```

实现见 `server_api_manager_chat_analysis.go:256-321`。GET 路径便于浏览器扩展、curl 直接拼。

### 4.2 Request Headers（POST）

| Header | 必填 | 示例 |
|--------|------|------|
| `Content-Type` | 是 | `application/json` |

GET 不用 body，参数全走 querystring。

### 4.3 Request Body（POST）— `ChatAnalysisDetailRequest`

```json
{
  "id":         1024,
  "user_name":  "liusm191",
  "model_name": "liusm191-server-model",
  "field":      "request_body"
}
```

### 4.4 必填与字段白名单

请求缺任一字段 → `success:false, message:"缺少必要参数"`。

`field` **必须**命中下表（`chatAnalysisDetailFieldColumns` 在 `mysql_http_agent_sub_table.go:858-867`），否则 `success:false, message:"不支持的详情字段"`：

| `field` 取值 | 实际列 | 类型 | 是否含敏感数据 |
|---|---|---|---|
| `request_headers` | `request_headers` | text | 含 `Authorization`（**已脱敏**，后端正则替成 `************************`） |
| `request_body` | `request_body` | longtext | 含完整 Prompt / Tool Calls |
| `request_src_protocol_body` | `request_src_protocol_body` | longtext | OpenAI→Anthropic 转换前的 OpenAI body |
| `response_headers` | `response_headers` | text | 已脱敏 |
| `response_body` | `response_body` | longtext | 含完整模型回复、tool_use blocks |
| `response_src_protocol_body` | `response_src_protocol_body` | longtext | 转换前的源协议 body |

**禁止传入** `field` 之外未列出的字段：服务端白名单是常量 map `field → column`，客户端传的 `field` 不直接拼到 SQL，避免注入与白名单外大字段（如 `request_url` 等短字段不走详情接口）。

### 4.5 Response Body — `ChatAnalysisDetailResponse`

```json
{
  "success": true,
  "message": "查询成功",
  "field":   "request_body",
  "value":   "{\"model\":\"gpt-4o-mini\",\"messages\":[...],\"stream\":true}"
}
```

| 字段 | 类型 | 说明 |
|------|------|------|
| `success` | bool | true = 命中记录且查到字段 |
| `message` | string | 错误或成功文案 |
| `field` | string | 回传请求里的 `field`（便于多并发去重检查） |
| `value` | string | 字段的字符串值（longtext 可能很长，本接口原样返回，不截断） |

#### ⚠ `value` 的编码：headers 是明文，body 可能是 base64

实测（v2.0.68）：

- `request_headers` / `response_headers` → **明文**，换行分隔的 `Key: Value`，其中 `Authorization` 已由后端脱敏成 `Bearer ************************`
- `request_body` / `response_body` / `*_src_protocol_body` → 若原始响应是 **SSE 流式**（`stream:true`，Agent 场景占绝大多数），入库时为 **base64 编码**，`value` 拿到的是 base64 串

Agent 必须先探测再解码，例如：

```bash
# 取 response_body 并尝试 base64 解码；失败则说明本来就是明文 JSON
curl -sS "http://localhost:9101/ChatAnalysisDetailInterface?id=1008&user_name=liusm191&model_name=liusm191-server-model&field=response_body" \
  | python3 -c 'import sys,json,base64
v=json.load(sys.stdin).get("value","")
try:    print(base64.b64decode(v, validate=True).decode("utf-8","replace"))
except Exception: print(v)'
```

解码后即为原始 SSE 帧（`event: message_start` / `data: {...}` …）。**不要**把 base64 原文直接塞进模型上下文 —— 既不可读又极占 token。

#### 失败响应

```json
{ "success": false, "message": "缺少必要参数" }
{ "success": false, "message": "不支持的详情字段" }
{ "success": false, "message": "查询失败: record not found" }
```

#### 唯一性契约

`(id, user_name, model_name)` 在同一哈希分表内**唯一**（`id` 自增主键）。但**不**做跨分表去重 —— 客户端必须按 `ChatAnalysisInterface` 返回的 `user_name + model_name` 来定位记录，再调用本接口。

---

## 5. 辅助接口（可选，减少 Agent 拼装枚举）

### 5.1 目标模型下拉 `/ChatAnalysisDstModelsInterface`

```text
POST http://localhost:9101/ChatAnalysisDstModelsInterface
Content-Type: application/json
```

Body：

```json
{ "user_name": "liusm191", "model_name": "liusm191-server-model" }
```

Response：

```json
{ "success": true, "message": "查询成功", "data": ["gpt-4o-mini", "claude-3-5-sonnet", ...] }
```

实现：`GetDistinctDstModelNames`（`mysql_http_agent_sub_table.go:837`），返回 `dst_model_name` 去重列表。

### 5.2 Agent 工具下拉 `/ChatAnalysisAgentToolsInterface`

```text
GET 或 POST http://localhost:9101/ChatAnalysisAgentToolsInterface
```

Body 可为空。

Response：

```json
{ "success": true, "message": "查询成功", "data": ["claude-cli", "opencode", "Kilo-Code", ...] }
```

实现：`GetDistinctAgentToolNames`（`mysql_agent_info_manage.go`），**全站去重**，与用户/模型无关。

---

## 6. 典型 Agent 工作流（推荐）

```text
┌───────────────────────────────────────────────────────────────────────┐
│  1. Agent 已知 user_name = "liusm191"，model_name = "liusm191-server-model"  │
│                                                                       │
│  2. POST /ChatAnalysisInterface                                       │
│     { user_name, model_name, page:1, page_size:10, days:3 }           │
│     → 拿 records[] + totalCount                                       │
│                                                                       │
│  3. （可选）POST /ChatAnalysisDstModelsInterface                       │
│     → dst_model_name 下拉                                             │
│                                                                       │
│  4. （可选）POST /ChatAnalysisInterface 加 filter_dst_model_name /     │
│     filter_status / filter_tools 二次过滤                              │
│                                                                       │
│  5. 取某条详情（按需懒加载，按字段拆分避免上下文爆）：                  │
│     POST /ChatAnalysisDetailInterface { id, user_name, model_name,     │
│                                         field:"request_body" }        │
│     POST /ChatAnalysisDetailInterface { id, user_name, model_name,     │
│                                         field:"response_body" }       │
│     POST /ChatAnalysisDetailInterface { id, user_name, model_name,     │
│                                         field:"request_headers" }     │
└───────────────────────────────────────────────────────────────────────┘
```

---

## 7. 完整示例（curl）

### 7.1 列出最近 10 条

```bash
curl -sS -X POST http://localhost:9101/ChatAnalysisInterface \
  -H 'Content-Type: application/json' \
  -d '{
    "user_name":  "liusm191",
    "model_name": "liusm191-server-model",
    "page": 1, "page_size": 10, "days": 3
  }'
```

### 7.2 按目标模型过滤 + 状态过滤

```bash
curl -sS -X POST http://localhost:9101/ChatAnalysisInterface \
  -H 'Content-Type: application/json' \
  -d '{
    "user_name":"liusm191",
    "model_name":"liusm191-server-model",
    "page":1, "page_size":20, "days":1,
    "filter_dst_model_name":"MiniMax-M3-highspeed",
    "filter_status":"200 OK"
  }'
```

> 实测：`filter_status:"200 OK"` 命中 729 条；写成 `"200"` 命中 **0 条**（等值匹配，非子串）。

### 7.3 取某条请求头（懒加载）

```bash
curl -sS -X POST http://localhost:9101/ChatAnalysisDetailInterface \
  -H 'Content-Type: application/json' \
  -d '{
    "id":1024,
    "user_name":"liusm191",
    "model_name":"liusm191-server-model",
    "field":"request_headers"
  }'
```

### 7.4 等价 GET（便于 bash 嵌入）

```bash
curl -sS "http://localhost:9101/ChatAnalysisDetailInterface?id=1024&user_name=liusm191&model_name=liusm191-server-model&field=response_body"
```

---

## 8. 排错速查

| 现象 | 根因 | 解决 |
|------|------|------|
| `success:false, message:"缺少 user_name 或 model_name 参数"` | 必填参数空 | 检查 body，**注意中文标点** |
| `success:false, message:"不支持的详情字段"` | `field` 不在白名单 | 必须用：`request_headers` / `request_body` / `request_src_protocol_body` / `response_headers` / `response_body` / `response_src_protocol_body` |
| 列表完全为空 | 用了不存在的 `(user, model)` 或 `days` 太严 | 先 `days:0` 看全量；调整 user/model |
| `filter_status:"200"` 返回 0 条 | 等值匹配，入库值是 `"200 OK"` | 传完整的 `"200 OK"` |
| `filter_dst_model_name` / `filter_agent_tool_name` 返回 0 条 | 二者都是**等值**匹配 | 先调 §5 两个下拉接口取真实枚举值 |
| `records[]` 里 body/headers 全是 `""` | 设计如此（懒加载） | 用 `/ChatAnalysisDetailInterface` 按字段取 |
| `response_body` 是一串乱码字母 | SSE 流式响应入库为 base64 | 按 §4.5 先 base64 解码 |
| 长字段返回空字符串 | 该列为 NULL（请求流没有触发） | 详见数据库约束；属正常 |
| HTTP `connection refused` | 端口不通 / 反向代理拦了 | 确认 `managerWebListenPort`（默认 9101）+ 主机可达 |
| 用户端报「未登录」 | JWT 过期 / 没带 Cookie | 走 §2.3 重发 `/UserLoginInterface` |

---

## 9. 与 `/ChatAnalysis` 页面的契约

- 同一接口、同一份 SQL、同一份白名单 —— 本文档即「页面背后的 API」完整契约
- 页面 `agentPageTemplate` 仍是单一拼装入口；本文档不替换前端模板，只是为 Agent 提供「无浏览器」查询通道
- v2.0.55 起的 WS 增量推送（`/ChatAnalysisTotalWS`）只服务于 `/ChatAnalysisTotal` 全站统计，**与本接口无关**；本接口仍是分页同步请求

---

## 10. 强制规则（必须遵守）

> ✅ `/ChatAnalysisInterface` 返回的 `records[]` 中**不含** 4 个 longtext 字段；如需 must use `/ChatAnalysisDetailInterface` 按字段懒加载（v2.0.42）。
> ✅ `field` 必须是 `chatAnalysisDetailFieldColumns` 白名单中的 6 个值之一；禁止把客户端 `field` 直接拼进 SQL（防注入）。
> ✅ `user_name` + `model_name` 是哈希分表定位键，必须成对传入；缺少任一字段接口直接拒绝。
> ✅ `page_size` 与 `days` 都有白名单校验，越界自动回落（`page_size → 3`，`days → 3`）；Agent 不要假设「传啥都生效」。
> ✅ 所有 HTTP 状态恒为 200，业务成功请看 `success` 字段；不要被 200 + `success:false` 误导。
> ✅ 严禁对 `/ChatAnalysis*` 接口用 `select *` / 不带白名单的全表扫描（v2.0.39 N+1 规则）。

---

## 11. 相关源码索引

| 文件 | 内容 |
|------|------|
| `server_api_manager_chat_analysis.go` | 管理端 4 个 chat analysis 接口实现 |
| `server_api_user_chat_analysis.go` | 用户端 4 个 chat analysis 接口（带 JWT 校验） |
| `mysql_http_agent_sub_table.go:858-867` | 详情白名单 `chatAnalysisDetailFieldColumns` |
| `mysql_http_agent_sub_table.go:836` | `GetDistinctDstModelNames` 目标模型下拉 |
| `mysql_agent_info_manage.go` | `GetDistinctAgentToolNames` Agent 工具下拉 |
| `server_api_user_login.go:407` | `getUserToken` JWT 解析（用户端 29001 必读） |
| `CLAUDE.md` / `AGENT.md` / `AGENT_INDEX.md` | 项目级规范与源码目录 |

---

**变更记录**

| 版本 | 日期 | 摘要 |
|------|------|------|
| v2.0.68 | 2026-08-04 | 初版：暴露 `/ChatAnalysisInterface` + `/ChatAnalysisDetailInterface` 给本地 Agent（默认账号 `liusm191`，默认模型 `liusm191-server-model`），含 4 个辅助接口与懒加载详情的 6 字段白名单契约 |
