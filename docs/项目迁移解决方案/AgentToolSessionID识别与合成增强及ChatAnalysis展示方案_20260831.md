# AgentToolSessionID 识别与合成增强及 ChatAnalysis 展示方案

> 版本：v2.0.76（阶段 BD）
> 日期：2026-08-31
> 状态：已实施
> 关联模块：`ServerGo/recognizer`、`ServerGo/models`、`ServerGo/proxy`、`ServerGo/config`、`ClientWeb/src/pages/chat-analysis`

---

## 1. 背景与问题陈述

LsmTokensServer 的 AI 代理服务已具备 Session ID 识别与合成能力（`TAgentHttpTransactionDataItem.SessionID` 字段），但存在以下不足：

### 1.1 缺少 Agent 工具级 Session ID 独立字段

现有 `SessionID` 字段存储的是「有效 Session ID」，其取值链路为：

```
真实识别（Agent 工具原生发送）→ 合成兜底（服务端生成）→ unknown_session_id 占位
```

三种来源混存在同一字段中，导致：

- 无法区分哪些请求携带了 Agent 工具原生 Session ID（如 Claude Code 的 `x-claude-code-session-id` 头、Codex 的 `x-codex-turn-metadata` 头）；
- 无法统计「各 Agent 工具的 Session ID 透传率」（运营无法判断某工具是否可靠地携带会话标识）；
- 合成 Session 与真实 Session 在分析页面中无法区分，影响会话聚合分析的准确性。

### 1.2 合成 Session ID 缺少标识前缀

`GetOrSynthesizeSessionID()`（`ServerGo/models/agent_algorithm_economic.go:913`）生成的 Session ID 是纯 24 位 hex 随机串，形如 `a3f8c2e19b4d7f0e5c6a8b2d`。它与真实 UUID 格式的 Session ID（如 Claude Code 的 `144ca9ed-c216-40f2-87a7-cd9df1dc7f3c`）在数据表和页面上无任何视觉区分，排查路由粘性问题时无法快速判断「这条记录的 Session 是客户端真实的还是服务端合成的」。

### 1.3 Session 空闲超时不可配置

合成 Session 的空闲超时 `EconomicSyntheticSessionTTL` 硬编码为 15 分钟（`agent_algorithm_economic.go:823`）。该值决定了「同一 userName+modelName 维度下连续请求复用同一合成 Session」的窗口：

- 窗口过长：Agent 长期不交互后仍复用旧 Session，粘性路由无法轮换到其它源站，负载均衡效果下降；
- 窗口过短：同一对话的连续请求被切到不同源站，破坏上下文缓存命中。

不同业务场景（长对话深度推理 vs 高频短问答）对窗口的需求不同，需要配置化。

### 1.4 ChatAnalysis 页面不展示 Session ID

前端对话分析页面（`/ChatAnalysis`）的数据列表没有 Session 维度的展示列，运营无法直观看到：

- 每条请求属于哪个 Agent 会话；
- 哪些请求的 Session 是真实识别的、哪些是服务端合成的。

---

## 2. 设计目标

| # | 目标 | 验收要点 |
|---|------|---------|
| G1 | 新增 `AgentToolSessionID` 字段，独立记录 Agent 工具原生识别的 Session ID | 识别成功落真实值；识别失败落空字符串（不用 unknown 占位） |
| G2 | 合成 Session ID 增加 `self_generate_` 前缀 | 格式 `self_generate_<24位hex>`；DB 列长 128 足够容纳 |
| G3 | Session 空闲超时可配置 | 配置项 `agentSessionIdleTimeoutMinutes`，默认 15，范围 1~1440，超时自动重新生成 |
| G4 | 优化 codex / claude code / opencode 等 Agent 的 Session ID 识别 | 新增识别入口函数 `RecognizeSessionIDWithSource`，补充更多 Agent 专用头 |
| G5 | ChatAnalysis 数据列表在「Agent工具」列后新增「AgentSessionID」列 | 管理端与用户端共用组件，一次修改双端生效；合成 ID 灰色斜体区分 |

---

## 3. 字段区分模型（核心设计）

本方案的核心是**双字段分离**，语义对齐如下：

| 字段 | 来源 | 取值场景 | 用途 |
|------|------|---------|------|
| `AgentToolSessionID`（新增） | `recognizer.RecognizeSessionID()` 原始结果 | 识别成功 → 真实 ID；识别失败 → **空字符串** | 审计/分析：记录 Agent 工具原生发送的 Session ID，统计透传率 |
| `SessionID`（已有，行为不变） | 识别结果 + 合成兜底 + unknown 占位 | 识别成功 → 真实 ID；合成 eligible → `self_generate_xxx`；其他 → `unknown_session_id` | 路由调度：经济型算法 Session 粘性负载均衡 |

### 3.1 为什么不直接改造 SessionID 字段

`SessionID` 是经济型算法 Session 粘性路由的输入（`SelectForSession`），其语义必须是「永远有值的有效 Session ID」（含合成/占位），否则路由逻辑需要全面判空改造，影响面大。新增独立字段可以在**零路由行为变更**的前提下补齐分析能力。

### 3.2 取值矩阵

| 请求场景 | AgentToolSessionID | SessionID |
|---------|-------------------|-----------|
| Claude Code 带 `x-claude-code-session-id` 头 | `144ca9ed-...`（真实值） | `144ca9ed-...`（同值） |
| Codex 带 `x-codex-turn-metadata` 头 | `sess_a1b2c3...`（真实值） | `sess_a1b2c3...`（同值） |
| opencode 无 Session 头（eligible agent） | `""`（空） | `self_generate_a3f8c2e1...` |
| 普通 cURL 无 UA（非 eligible） | `""`（空） | `unknown_session_id` |

---

## 4. 数据库字段设计

### 4.1 字段定义

```go
// Agent 工具信息（TAgentHttpTransactionDataItem 内，紧跟 AgentToolInfo 之后）
AgentToolName       string `json:"agent_tool_name"  gorm:"size:64;index;comment:AI Agent工具名称，如claude-cli/opencode等"`
AgentToolInfo       string `json:"agent_tool_info"  gorm:"size:512;comment:AI Agent工具扩展信息，含版本、运行时等"`
AgentToolSessionID  string `json:"agent_tool_session_id" gorm:"size:128;index;comment:Agent工具原生识别的Session ID（空表示未识别）"`
```

- **列名**：`agent_tool_session_id`（gorm 默认蛇形转换）
- **长度**：`size:128`，与现有 `SessionID` 一致；`self_generate_` 前缀 + 24 hex = 38 字符，远小于上限
- **索引**：单列索引（gorm `index` tag 自动创建 `idx_t_agent_http_transaction_data_items_agent_tool_session_id`），支持后续按 Session 检索/过滤扩展
- **迁移方式**：GORM AutoMigrate 自动加列（`InitAgentHttpSubTables` 启动时对 8 张分表执行），无需手工 SQL

### 4.2 查询列白名单

`selectTransactionColumns()`（`ServerGo/models/subtable.go`）追加 `agent_tool_session_id`，位置紧跟 `agent_tool_info` 之后，保持语义分组。列表查询即可返回该字段，无需额外接口。

---

## 5. Session ID 识别优化

### 5.1 现有识别能力梳理（不改动，仅列出）

识别入口：`recognizer.RecognizeSessionID(body, protocolType, headers)`

**Anthropic 协议**（优先级从高到低）：
1. `x-claude-code-session-id` / `claude-code-session-id` 头（Claude Code 原生）
2. `anthropic-beta: ...; session-id=xxx` 头（官方 beta）
3. `x-session-id` / `x-anthropic-session-id` 头
4. `metadata.user_id` 内嵌 JSON 的 `session_id`
5. 顶层 `session_id` 字段

**OpenAI 协议**（优先级从高到低）：
1. Agent 工具级识别器（OpenClaw：UA 含 `OpenAI/JS` 时从 system content 提取 `sessionId=`）
2. `x-codex-turn-metadata` JSON 头（Codex CLI：`session_id` > `thread_id`）
3. `x-grok-conv-id` / `x-grok-session-id` 头（Grok Build）
4. `x-opencode-session-id` / `x-session-id` / `session-id` 头（OpenCode）
5. `metadata.user_id` 内嵌 JSON
6. `client_metadata.session_id` / `client_metadata.thread_id`（Codex 扩展）
7. `prompt_cache_key`（Codex 同值字段兜底）
8. 顶层 `session_id` 字段

所有路径统一过 `sessionIDMinHeaderLen = 10` 最小长度校验，短值丢弃。

### 5.2 本次新增

#### 5.2.1 新识别入口：`RecognizeSessionIDWithSource`

现有 `RecognizeSessionID()` 只返回一个字符串，调用方无法区分「识别到的真实 ID」与「未识别」。新增：

```go
// SessionRecognitionResult Session 识别结果（区分原生识别与最终生效值）
type SessionRecognitionResult struct {
    AgentToolSessionID string // Agent 工具原生识别出的 session ID；未识别为空字符串
    EffectiveSessionID string // 最终生效的 session ID；识别成功时与 AgentToolSessionID 同值，
                              // 未识别时为空字符串，由调用方决定合成或 unknown 占位
}

func RecognizeSessionIDWithSource(body []byte, protocolType int, headers http.Header) SessionRecognitionResult
```

语义约定：**所有现有识别路径命中即视为「Agent 工具原生识别」**（无论来自头还是 body），两字段同值；未识别时 `AgentToolSessionID` 保持空字符串。原 `RecognizeSessionID()` 保留不动，行为完全兼容（内部委托新函数）。

#### 5.2.2 补充 Agent 专用头（OpenAI 协议识别顺序 4 扩展）

在现有 `X-OpenCode-Session-Id` / `X-Session-Id` / `Session-Id` 基础上，追加常见 Agent 工具的会话头变体：

- `x-aider-session-id`（Aider）
- `x-continue-session-id`（Continue）
- `x-cursor-session-id`（Cursor）
- `x-cline-session-id`（Cline）
- `x-github-copilot-session-id`（Copilot）
- `x-kilo-code-session-id`（Kilo Code）
- `x-windsurf-session-id`（Windsurf）

均走 `sessionIDMinHeaderLen` 长度校验，命中即返回，不改变现有优先级顺序（插在 OpenCode 头之后、通用 `X-Session-Id` 之前）。

---

## 6. 合成 Session ID 增强

### 6.1 前缀标识

```go
// SyntheticSessionIDPrefix 合成 session id 的特殊标识前缀
const SyntheticSessionIDPrefix = "self_generate_"
```

`GetOrSynthesizeSessionID()` 生成逻辑：

```go
sessionID := SyntheticSessionIDPrefix + hex.EncodeToString(b) // self_generate_ + 24 hex = 38 字符
```

新增导出判断函数（供测试与潜在展示逻辑使用）：

```go
// IsSyntheticSessionID 判断 session id 是否为服务端合成（self_generate_ 前缀）
func IsSyntheticSessionID(id string) bool
```

### 6.2 配置化空闲超时

**配置项**：`agentSessionIdleTimeoutMinutes`（JSON 配置，默认 15）

| 配置位置 | 内容 |
|---------|------|
| `LsmTokensServerConfig` 结构体 | `AgentSessionIdleTimeoutMinutes int \`json:"agentSessionIdleTimeoutMinutes"\`` |
| `getDefaultConfig()` | 默认 15 |
| `validateAndFixConfig()` | ≤0 或 >1440 回退默认 15 |
| `rawLsmTokensServerConfig`（旧格式兼容） | 同名字段，>0 时覆盖 |
| `LsmTokensServer.conf.example` | `"agentSessionIdleTimeoutMinutes": 15` |

**TTL 读取函数**（替代硬编码常量的直接引用）：

```go
// GetSyntheticSessionTTL 返回合成 session 的空闲超时；优先读配置，未配置/非法回退默认
func GetSyntheticSessionTTL() time.Duration {
    if config.G != nil && config.G.AgentSessionIdleTimeoutMinutes > 0 {
        return time.Duration(config.G.AgentSessionIdleTimeoutMinutes) * time.Minute
    }
    return EconomicSyntheticSessionTTL // 默认 15 分钟
}
```

原常量 `EconomicSyntheticSessionTTL = 15 * time.Minute` 保留作为默认值与测试基线；`GetOrSynthesizeSessionID()` 内两处过期判断改用 `GetSyntheticSessionTTL()`。

### 6.3 超时自动重新生成（既有机制，随 TTL 配置化生效）

滑动窗口语义保持不变：每次命中缓存会刷新 `LastUsed`；超过 TTL 无请求则缓存条目过期，下次请求生成新 Session（带新前缀），实现「Agent 长期不交互达到阈值后自动重新生成新 Session」。配置化后阈值随配置即时生效（下次请求读取新值）。

---

## 7. 代理层接入（双 Session ID 流水线）

### 7.1 日志落库路径（`proxy/server_http_ai_proxy.go` 11.5 步）

```
识别：recResult := recognizer.RecognizeSessionIDWithSource(bodyBytes, protocolType, r.Header)
  ├── agentToolSessionID := recResult.AgentToolSessionID   // 原生识别（可为空）
  └── sessionID          := recResult.EffectiveSessionID   // 生效值起点
兜底：sessionID == "" 时（顺序不变）：
  ├── eligible agent → GetOrSynthesizeSessionID() → sessionID = "self_generate_xxx"
  └── 仍为空       → sessionID = "unknown_session_id"
落库：logAIProxyTransaction(..., sessionID, agentToolSessionID, ...) 两个值分别透传
```

### 7.2 经济算法路由路径（`proxy/server_http_ai_proxy_utils.go` forwardWithRetry）

路由粘性使用的 `economicSessionID` 仍取**生效值**（识别 + 合成兜底），与现状一致，不做行为变更。识别入口可切换到 `RecognizeSessionIDWithSource()` 取 effective 值，语义等价。

### 7.3 落库函数签名变更

`models.SaveAgentHttpTransaction()` 与 `proxy.logAIProxyTransaction()` 均新增参数 `agentToolSessionID string`（置于 `sessionID` 之后），内部赋值 `record.AgentToolSessionID = agentToolSessionID`。

---

## 8. ChatAnalysis 前端展示

### 8.1 列定义

`ClientWeb/src/pages/chat-analysis/index.jsx` 的 `columns` 数组中，`agent_tool_name` 列之后插入：

```jsx
{ key: 'agent_tool_session_id', title: t('chatAnalysis.agentSessionId'), render: (v, row) => {
    // 优先展示原生识别值；为空时降级展示生效 session_id（合成 ID 灰色斜体区分）
    let sid = v
    let isSynth = false
    if (!sid) {
      sid = row.session_id
      isSynth = typeof sid === 'string' && sid.startsWith('self_generate_')
    }
    if (!sid || sid === 'unknown_session_id') return '-'
    return <span style={isSynth ? { color: 'var(--muted)', fontStyle: 'italic' } : undefined} title={sid}>
      {sid.length > 24 ? sid.slice(0, 24) + '…' : sid}
    </span>
} },
```

展示规则：

| 数据状态 | 展示效果 |
|---------|---------|
| `agent_tool_session_id` 有值 | 正常色显示前 24 字符，title 悬浮完整值 |
| 无原生值、`session_id` 为 `self_generate_*` | 灰色斜体（服务端合成标识） |
| 无原生值、`session_id` 为 `unknown_session_id` 或空 | `-` |

### 8.2 i18n

| 语言 | key | 值 |
|------|-----|---|
| zh-CN | `chatAnalysis.agentSessionId` | `AgentSessionID` |
| en | `chatAnalysis.agentSessionId` | `Agent Session ID` |
| ja | `chatAnalysis.agentSessionId` | `AgentセッションID` |

### 8.3 双构建隔离

ChatAnalysis 组件为管理端/用户端共用（无 `__APP_ROLE__` 门控的管理专属代码），单次修改双端生效，不涉及双构建隔离红线复验新增项。

---

## 9. 实施步骤

| 阶段 | 内容 | 涉及文件 |
|------|------|---------|
| 1 | 设计文档（本文档） | `docs/项目迁移解决方案/AgentToolSessionID识别与合成增强及ChatAnalysis展示方案_20260831.md` |
| 2 | 配置项新增 | `ServerGo/config/config.go`、`LsmTokensServer.conf.example` |
| 3 | 数据模型新增字段 | `ServerGo/models/mysql_http_agent_model.go` |
| 4 | 合成 Session 增强 | `ServerGo/models/agent_algorithm_economic.go` |
| 5 | 识别器优化 | `ServerGo/recognizer/recognizer_session_id.go` |
| 6 | 分表 select/save 适配 | `ServerGo/models/subtable.go` |
| 7 | 代理层接入 | `ServerGo/proxy/server_http_ai_proxy.go`、`ServerGo/proxy/server_http_ai_proxy_utils.go` |
| 8 | 单元测试 | `ServerGo/models/agent_algorithm_economic_test.go`、`ServerGo/api/test_api_transactions_test.go`、`ServerGo/recognizer/recognizer_session_id_test.go` |
| 9 | 前端列 + i18n | `ClientWeb/src/pages/chat-analysis/index.jsx`、`ClientWeb/src/i18n/locales/{zh-CN,en,ja}.json` |
| 10 | 编译/测试/提交 | `./rebuild_restart_app.sh --build-only` → `go test ./...` → git commit |

---

## 10. 风险评估与回滚

| 风险 | 等级 | 缓解措施 |
|------|------|---------|
| GORM AutoMigrate 加列期间写请求阻塞 | 低 | MySQL 加列为 INSTANT/INPLACE DDL，128 字节 varchar 开销极小；与历史加列（session_id 等）同模式 |
| `SaveAgentHttpTransaction` 签名变更破坏调用方 | 中 | 全仓 grep 调用点逐一同步（proxy 2 处 + 测试若干）；编译期即可发现遗漏 |
| 合成 ID 加前缀后长度超列上限 | 无 | 38 字符 << 128 |
| 前缀变更影响旧数据区分 | 低 | 旧合成 ID（纯 hex）在页面上显示为正常色，属可接受的历史数据表现 |
| TTL 配置化后行为变化 | 低 | 默认值与原硬编码一致（15 分钟），不配置则零变化 |
| 路由行为回归 | 低 | `SessionID` 语义与赋值链路完全不变；经济算法仅换用等价的识别入口 |

**回滚方案**：功能均为增量（新字段、新前缀、新配置），回滚版本后旧代码忽略新列、忽略配置项即可正常运行；无需数据回滚。

---

## 11. 验收标准

1. `./rebuild_restart_app.sh --build-only` 后端 + 前端双构建通过
2. `go test ./...` 全绿（含新增测试）
3. 8 张分表均出现 `agent_tool_session_id` 列及索引
4. 带 `x-claude-code-session-id` 头的请求：`agent_tool_session_id` 与 `session_id` 同为真实值
5. eligible Agent 无 Session 请求：`session_id` = `self_generate_xxx`，`agent_tool_session_id` 为空
6. 非 eligible 请求：`session_id` = `unknown_session_id`，`agent_tool_session_id` 为空
7. ChatAnalysis 页面「Agent工具」列后出现「AgentSessionID」列，合成 ID 灰色斜体、超 24 字符截断 + 悬浮全量
8. 配置 `agentSessionIdleTimeoutMinutes` 生效：设为 1 分钟后空闲超 1 分钟的下次请求生成新 `self_generate_` ID
