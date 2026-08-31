# ChatAnalysis 详情增加 AgentToolName / AgentToolInfo / AgentToolSessionID 字段显示方案

> 日期：2026-08-31
> 范围：管理员 Web 服务（9101）与用户 Web 服务（29001）共用的 `/ChatAnalysis` 对话分析页面
> 版本：v2.0.7x（阶段 BD 之后）

---

## 一、背景与目标

### 1.1 背景

`TAgentHttpTransactionDataItem` 数据表已经具备三个 AI Agent 工具相关的字段（v2.0.74 阶段后陆续落库）：

| 字段 | 类型 | 索引 | 含义 |
|---|---|---|---|
| `agent_tool_name` | varchar(64) | 索引 | Agent 工具名称，如 `claude-cli` / `opencode` 等 |
| `agent_tool_info` | varchar(512) | — | Agent 工具扩展信息，含版本、运行时等 |
| `agent_tool_session_id` | varchar(128) | 索引 | Agent 工具原生识别出的 Session ID（识别失败为空字符串） |

v2.0.76 阶段 BD 已在前端 `/ChatAnalysis` 数据列表中新增了 `AgentTool`（列基于 `agent_tool_name`）与 `AgentSessionID`（列基于 `agent_tool_session_id`）两列；但：

1. **`agent_tool_info` 字段尚未在前端任何位置展示**——这是数据表中扩展信息（版本号、运行时等）的唯一来源，缺失会导致排查 Agent 客户端差异时必须回到 DB 手动查询。
2. **数据列表「对话详情」模块**（即内联展开的 `InlineDetailRow`，阶段 AR 重构后的版本）目前只展示协议流向 / KPI 卡片 / 请求信息行 / 字段 Tab，并未把 `agent_tool_name` / `agent_tool_info` / `agent_tool_session_id` 作为「请求元信息」展示——只能从列表列瞥一眼。

### 1.2 目标

| 维度 | 目标 | 验收标准 |
|---|---|---|
| 数据列表列 | `agent_tool_info` 列新增 | 空值降级 `-`，title 展示完整内容；与 `agent_tool_name` 列相邻 |
| 对话详情模块 | `InlineDetailRow` 详情头部新增「Agent 工具」信息块 | 展示 `agent_tool_name` / `agent_tool_info` / `agent_tool_session_id` 三项；空值降级；与 `agent_tool_session_id` 现有「合成 ID 灰色斜体」规则一致 |
| 国际化 | 三语言 `zh-CN` / `en` / `ja` 同步新增 `chatAnalysis.agentToolInfo` 列名 + 详情块标签 | key 名一致，文案自然 |
| 测试 | 前端单测 / 列渲染 / 详情头部渲染 / i18n key 完整性 | 全绿 |
| 构建 | `npm run build` 双构建产出 `dist-manager` / `dist-user` | 成功 |

---

## 二、现状分析

### 2.1 已具备的能力（复用）

| 能力 | 位置 |
|---|---|
| 数据表三字段落库 | `ServerGo/models/mysql_http_agent_model.go:133-140` |
| 数据列表 `agent_tool_name` 列 | `ClientWeb/src/pages/chat-analysis/index.jsx:144` |
| 数据列表 `agent_tool_session_id` 列（含合成 ID 灰色斜体规则） | `ClientWeb/src/pages/chat-analysis/index.jsx:147-158` |
| 详情头部组件 | `ClientWeb/src/pages/chat-analysis/DetailHeader.jsx` |
| 详情头部字段渲染模式（`KPI 卡片` + `请求信息行`） | `DetailHeader.jsx:34-60` |
| 三语 i18n key | `ClientWeb/src/i18n/locales/zh-CN.json`、`en.json`、`ja.json` |
| API 自动序列化 | `ServerGo/api/server_api_user_chat_analysis.go:80-86` 调用 `modelsdb.QueryAgentHttpTransactions` 返回 GORM 模型，自动 JSON 序列化 `agent_tool_name` / `agent_tool_info` / `agent_tool_session_id` 三个字段 |

### 2.2 需要改造的点

1. 数据列表 `index.jsx` —— 在 `agent_tool_name` 列后新增 `agent_tool_info` 列。
2. 详情头部 `DetailHeader.jsx` —— 新增「Agent 工具」信息块（独立分组，标题为「🤖 Agent 工具」）。
3. i18n 三语言 —— 同步新增 `chatAnalysis.agentToolInfo`（列表列名）+ `chatAnalysis.agentToolBlock`（详情块标题）。
4. 前端单测 —— `ClientWeb/src/pages/chat-analysis/` 下新增 `DetailHeader.test.jsx` 与 `index.test.jsx`，覆盖 AgentTool 三字段渲染与 i18n key 完整性。

### 2.3 数据流（无需修改后端）

```
DB TAgentHttpTransactionDataItem.agent_tool_*
   ↓ GORM 自动序列化（json tag）
ServerGo models 切片
   ↓ modelsdb.QueryAgentHttpTransactions 返回
ChatAnalysisInterfaceResponse{Data: { records }}
   ↓ post('ChatAnalysisInterface')
useChatAnalysisData.setRows(records)
   ↓
DataTable rows → 列渲染 + InlineDetailRow.row 透传
   ↓
DetailHeader({ row }) → 渲染 Agent 工具信息块
```

后端 API 层零修改：JSON tag 已经在 GORM 模型定义中（`agent_tool_name` / `agent_tool_info` / `agent_tool_session_id`），记录列表自动包含三字段。

---

## 三、方案设计

### 3.1 数据列表新增 `agent_tool_info` 列（`index.jsx`）

紧邻现有 `agent_tool_name` 列之后插入新列：

```jsx
{ key: 'agent_tool_name', title: t('chatAnalysis.agentTool'), render: (v) => v || '-' },
// v2.0.7x 阶段BG：agent_tool_info 列——扩展信息（版本、运行时等），
// 空值降级 '-'；title 展示完整内容，便于悬停查看。
{ key: 'agent_tool_info', title: t('chatAnalysis.agentToolInfo'), render: (v) =>
  v ? <span title={v}>{v.length > 60 ? v.slice(0, 60) + '…' : v}</span> : '-'
},
{ key: 'agent_tool_session_id', title: t('chatAnalysis.agentSessionId'), ... }
```

- 字段空值降级：与 `agent_tool_name` 一致展示 `-`。
- 长内容截断：超过 60 字符截断显示 + `title` 属性展示完整内容（与 `request_url` 列的模式一致，见 `index.jsx:132`）。
- 不需要新增 `width`，让 DataTable 自适应（参考相邻列无 width 的实现）。

### 3.2 详情头部新增「Agent 工具」信息块（`DetailHeader.jsx`）

在「请求信息行」之后新增一个独立分组「Agent 工具信息」：

```jsx
{/* v2.0.7x 阶段BG：Agent 工具信息块 —— 展示三字段，便于排查 Agent 客户端差异 */}
{(row.agent_tool_name || row.agent_tool_info || row.agent_tool_session_id) ? (
  <div className="detail-head-agent">
    <span className="dha-title">🤖 {t('chatAnalysis.agentToolBlock')}</span>
    {row.agent_tool_name ? (
      <span className="dha-item" title={row.agent_tool_name}>
        <span className="dha-label">{t('chatAnalysis.agentTool')}：</span>
        <span className="dha-value">{row.agent_tool_name}</span>
      </span>
    ) : null}
    {row.agent_tool_info ? (
      <span className="dha-item" title={row.agent_tool_info}>
        <span className="dha-label">{t('chatAnalysis.agentToolInfo')}：</span>
        <span className="dha-value">{row.agent_tool_info}</span>
      </span>
    ) : null}
    {row.agent_tool_session_id ? (
      <span className="dha-item" title={row.agent_tool_session_id}>
        <span className="dha-label">{t('chatAnalysis.agentSessionId')}：</span>
        <span className="dha-value">{row.agent_tool_session_id}</span>
      </span>
    ) : null}
  </div>
) : null}
```

- **整体条件渲染**：三字段全为空时不展示该块，避免「无意义空块」干扰阅读（与阶段 BF「ChatAnalysis 聚合解析」对空 Text 块的处理思路一致）。
- **单字段条件渲染**：每个字段独立判断，空值则跳过该项标签（与 KPI 卡片的「按字段存在性展示」模式一致）。
- **`agent_tool_session_id` 不在此处做合成 ID 灰色斜体处理**：列表列已做灰色斜体区分，详情头部仅展示原始 `agent_tool_session_id` 值（即「Agent 工具原生识别值」）；合成 ID 的归一化处理交给列表列。如果未来详情需要展示归一化值，再行扩展。
- **样式约定**：复用 `detail-head` / `dhreq-*` 已有的 CSS 命名空间，新前缀 `dha-*`（detail-head-agent），与现有 KPI 卡片样式视觉对齐。

### 3.3 i18n 三语言同步

新增以下 key（三语文件名 / 文件名约定见 `docs/开发指南/AGENT.md` 与 `ClientWeb/src/i18n/locales/`）：

| key | zh-CN | en | ja |
|---|---|---|---|
| `chatAnalysis.agentToolInfo` | Agent工具信息 | Agent Tool Info | Agentツール情報 |
| `chatAnalysis.agentToolBlock` | Agent工具信息块 | Agent Tool Info Block | Agentツール情報ブロック |

说明：
- `chatAnalysis.agentTool`、`chatAnalysis.agentSessionId` 已存在（阶段 BD 引入），不重复新增。
- 三语同步确保 `key: {zh, en, ja}` 一一对应，构建期由 `useI18n` 单 key 查找，无回退；缺失会在前端控制台 warning。

### 3.4 CSS 样式约定（`ClientWeb/src/pages/chat-analysis/` 同级或全局 CSS）

`detail-head-agent` 容器采用 flex wrap，间距与现有 `detail-head-request` 对齐：

```css
.detail-head-agent {
  display: flex;
  flex-wrap: wrap;
  gap: 12px;
  align-items: baseline;
  margin-top: 8px;
  padding: 8px 12px;
  border-radius: 6px;
  background: var(--surface-2, #f6f7f9);
}
.dha-title { font-weight: 600; color: var(--fg, #222); }
.dha-item { display: inline-flex; gap: 4px; align-items: baseline; min-width: 0; }
.dha-label { color: var(--muted, #888); font-size: 12px; }
.dha-value {
  font-family: ui-monospace, SFMono-Regular, Menlo, monospace;
  font-size: 13px;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 360px;
}
```

CSS 命名风格延续 `detail-head-*` / `dhreq-*` / `dhc-*` 既有约定；若现有 CSS 已包含 `detail-head-agent`（可能性低），本次以「最小差异」叠加为准。

### 3.5 数据流与边界

| 边界场景 | 处理 |
|---|---|
| `agent_tool_name` 为空但 `agent_tool_info` 非空 | 仅展示 `agent_tool_info` 项（单字段条件渲染生效） |
| `agent_tool_session_id` 非空但与 `session_id` 不同 | 详情头部仅展示原生识别值；列表列保留「优先 Agent 工具识别值 + 降级 `session_id`」逻辑 |
| 三字段全空 | 整个信息块不渲染（顶层条件渲染） |
| `agent_tool_info` 超长（如 256 字符版本字符串） | `title` 展示完整内容，`<span>` 截断显示 + ellipsis |
| 后端字段缺失（如旧版本编译产物） | 字段为 `undefined`，模板字符串与三元判断均安全降级 |

### 3.6 测试

#### 前端单测（新增）

`ClientWeb/src/pages/chat-analysis/index.test.jsx`（vitest + @testing-library/react）：

1. `agent_tool_info` 列渲染：传入 row 含 `agent_tool_info='opencode v1.2.3'`，断言列渲染该字符串；空值渲染 `-`；超长字符串截断 + title 属性完整。
2. 列 i18n key 完整性：`en.json`、`zh-CN.json`、`ja.json` 均含 `chatAnalysis.agentToolInfo` 且文案非空。

`ClientWeb/src/pages/chat-analysis/DetailHeader.test.jsx`：

1. 三字段全空时不渲染 `.detail-head-agent`。
2. 仅 `agent_tool_name` 非空时，块渲染且仅展示该项。
3. 三字段均有值时，三个 `.dha-item` 全部渲染，文案匹配输入。
4. `i18n.t('chatAnalysis.agentToolBlock')` 调用出现（mock useI18n）。

> 测试运行命令：`npx vitest run src/pages/chat-analysis/`（参考 `ClientWeb/src/shared/clipboard.test.js` 既有模式；如果项目当前使用其他测试运行器，按 `package.json` 与 README 实际命令执行）。

---

## 四、实施步骤

1. **i18n 三语同步新增 key**：编辑 `zh-CN.json` / `en.json` / `ja.json` 在 `chatAnalysis.agentSessionId` 附近插入 `chatAnalysis.agentToolInfo` 与 `chatAnalysis.agentToolBlock`。
2. **数据列表新增列**：编辑 `ClientWeb/src/pages/chat-analysis/index.jsx`，在 `agent_tool_name` 列后插入 `agent_tool_info` 列。
3. **详情头部新增信息块**：编辑 `ClientWeb/src/pages/chat-analysis/DetailHeader.jsx`，在请求信息行之后插入「Agent 工具信息」块。
4. **CSS 样式**：在 `ClientWeb/src/pages/chat-analysis/` 同级 CSS（或全局）新增 `.detail-head-agent` / `.dha-*` 规则；若现有 CSS 中已含同名类，叠加差异属性即可。
5. **单测**：新增 `index.test.jsx` 与 `DetailHeader.test.jsx`。
6. **构建验证**：`npm run build` 双构建产物生成。
7. **提交**：中文 commit message —— `阶段BG：ChatAnalysis 详情增加 AgentToolName/Info/SessionID 三字段显示 + i18n 三语同步 + 单测覆盖`。

---

## 五、风险与兼容性

| 风险 | 影响 | 缓解 |
|---|---|---|
| 旧版本构建产物字段缺失 | row 上字段为 `undefined`，UI 显示降级 | 模板与三元判断均容错，无需版本校验 |
| 后端未升级到阶段 BD 的版本 | `agent_tool_session_id` 列可能为空 | 已是阶段 BD 落库字段；详情块顶层条件渲染保证整块消失 |
| 用户端产物含管理端字样（违反 CLAUDE.md 2.5） | 构建后 grep 复验 | 本次改动均为 `pages/chat-analysis/` 公共页面，存在于 `__APP_ROLE__` 双角色，无管理端专属代码注入 |
| API Key 泄露 | 无 | 本次不涉及登录态与凭证 |
| 长字符串性能 | `agent_tool_info` 长度 512 字符上限，title 完整展示无渲染开销 | `white-space: nowrap + ellipsis` 限制显示宽度 |

---

## 六、版本与变更记录

| 版本 | 日期 | 变更 |
|---|---|---|
| v1 | 2026-08-31 | 初版方案设计 |