# ChatAnalysis「对话详情」ResponseBody 修复与视图交互优化方案（20260828-02）

> 适用范围：管理员 Web（9101）与用户 Web（29001）`/ChatAnalysis` 对话分析页 —— 记录详情内联面板。
> 版本：v2.0.74 → v2.0.75（阶段AU）。
> 前序：`tmpPlan/ChatAnalysis-JSON美化视图全面升级方案-20260828-01.md`（阶段AS，JsonTree v2 惰性渲染）。

---

## 1. 问题清单（用户反馈定位）

### 1.1 ResponseBody 显示 Base64 乱码，SSE 解析 / 聚合解析不可用（Bug，后端）

- **现象**：`ResponseBody` 直接显示 Base64 文本（乱码）；`SSE 解析`、`聚合解析` 均显示"暂无数据"。
- **根因**：落库与读取不对称 ——
  - 写入：`ServerGo/models/subtable.go:530` `SaveAgentHttpTransaction` 对 `respBody` 统一做
    `base64.StdEncoding.EncodeToString` 后存入 `response_body`（v2.0.5x 起为规避 MySQL utf8
    Error 1366 引入）；
  - 读取：`GetAgentHttpTransactionFieldByID`（同文件 :927）只对 `request_body` /
    `request_src_protocol_body` 做 base64 解码，**`response_body` 漏了解码**。
- **影响面**：`ChatAnalysisDetailInterface` 管理端与用户端共用该模型函数，两端同坏；
  前端拿到 Base64 后 `isSSEFormat()` 判否 → `JSON.parse` 失败 → SSE/聚合双双"暂无数据"，
  JSON 美化/复制同样输出乱码。

### 1.2 视图按钮与字段语义不匹配（体验）

- `RequestBody` 提供 `SSE 解析`、`聚合解析` 按钮 —— 请求体不是 SSE 流，点了必然"暂无数据"；
- `ResponseBody` 提供 `JSON 美化` 按钮 —— 流式响应原文不是单个 JSON，解析必失败
  （SSE/聚合视图才是响应体的"JSON 化"入口，见 1.6）。

### 1.3 查找不在当前视图布局内进行（体验）

现状：`DetailBody` 在查找词非空时把**整个视图替换**为 `HighlightText` 纯文本长文
（`buildViewText` 的文本化表示），视图布局（JSON 折叠树 / SSE 卡片 / 聚合面板）全部消失。
期望：在**当前显示状态**下查找 —— 例如 `RequestBody` 的 `原文` 或 `JSON 美化` 布局内
直接高亮、焦点移动；`ResponseBody` 的 `原文` / `SSE 解析` / `聚合解析` 同理。

### 1.4 复制要求所见即所得（体验，随 1.1/1.3 联动）

复制内容需与"当前视图显示内容"一致（视图级文本表示），后端解码修复 + 视图裁剪后即自然对齐。

### 1.5 JsonTree 缺「展开全部」（体验）

工具栏现有 `折叠全部 / 展开至 2 层 / 3 层 / 5 层`，缺一个与"折叠全部"对称的 `展开全部`。

### 1.6 SSE / 聚合解析后的数据是 JSON，未享受 JSON 树能力（体验）

`SSE 解析` 卡片内 parsed JSON 目前是 `JSON.stringify(..., 2)` 的裸 `<pre>`：
无折叠、无超长字符串保护、无语法高亮、无查找高亮 —— 与阶段AS JsonTree v2 能力不齐。

## 2. 解决方案

### 2.1 后端：`response_body` 读取时 base64 解码（修 1.1）

`ServerGo/models/subtable.go`：

- 新增纯函数 `tryDecodeBase64Text(value string) string`：仅当 value 非空、`len%4==0`、
  `base64.StdEncoding.DecodeString` 成功 **且解码结果为合法 UTF-8** 时才替换原值
  （防误伤"恰好是 base64 字符集的明文"；`{`、`:`、`"`、换行等均不在 base64 字母表内，
  JSON/SSE 明文实际不可能通过校验）。
- `GetAgentHttpTransactionFieldByID` 中解码分支由 `request_body`/`request_src_protocol_body`
  扩展为同时覆盖 **`response_body`**；三个字段统一走 `tryDecodeBase64Text`（比原裸 DecodeString
  多一层 UTF-8 校验，更安全）。
- 不动 `SaveAgentHttpTransaction` 写入侧（base64 落库仍是 Error 1366 容错的关键）。

验证：新增 sqlite 内存库往返测试 —— `SaveAgentHttpTransaction` 存入明文响应体（函数内部
自动编码），`GetAgentHttpTransactionFieldByID(..., "response_body")` 取回明文；明文/非 base64/
非法 UTF-8 场景的 `tryDecodeBase64Text` 单测。

### 2.2 视图按钮按字段裁剪（修 1.2）

`ClientWeb/src/shared/viewText.js` 新增纯函数：

```js
viewsForTab(tab):
  request_body     → [raw, json]
  response_body    → [raw, sse, agg]
  其他（headers）  → [raw]
```

- `DetailTabs.jsx`：视图子 Tab 只渲染 `viewsForTab(currentTab)` 列出的按钮；
- `InlineDetailRow.jsx`：`handleTabChange` 时若当前 view 不在新 Tab 的可用列表中，
  自动 `onViewChange(VIEW_RAW)` 回落（例如 request/json → response 时）。

### 2.3 查找：当前视图布局内高亮 + 焦点移动（修 1.3）

**架构原则：渲染后的 DOM 是唯一事实来源（WYSIWYG）** —— 匹配计数、当前项高亮、
焦点滚动全部基于 `mark.sm-mark` DOM 查询，天然兼容 JsonTree 节点折叠、SSE 卡片折叠等
**嵌套局部状态变化**（这些变化不会触发父级重渲染，纯 React 状态方案必然计数失真）。

新增 `ClientWeb/src/components/SearchText.jsx`（哑组件）：

- 输入 `{ text, query, className }`，按 query（大小写不敏感）切分为 普通段/`<mark className="sm-mark">`
  匹配段；query 为空时原样输出。不做任何计数/滚动 —— 全部交由上层 DOM 机制。

视图接入（`DetailBody` 查找模式下**不再替换视图**，改为把 `query` 传入当前视图组件）：

| 视图 | 实现 |
|---|---|
| `原文`（body）/ headers | `HighlightText`（内部 mark 改用 `sm-mark` 类名，删除自带计数/滚动，统一交给 DOM 机制） |
| `JSON 美化` | `JsonTree` 新增可选 `query` prop：key 名、字符串值、数字/布尔/null 字面量经 `SearchText` 渲染；工具栏/分页按钮/`// N 项` 徽标等 UI chrome 不参与匹配 |
| `SSE 解析` | `SseEventList` 新增 `query`：事件名、raw 文本、parsed JSON（经 JsonTree，见 2.5）参与匹配 |
| `聚合解析` | `AggregateView` 新增 `query`：聚合文本块、工具名标签、事件类型标签参与匹配 |

`InlineDetailRow` 集中实现（配合既有 SearchBar 的 上一个/下一个/计数）：

- 每次 render 后 + `MutationObserver(childList/characterData, subtree)` 监听详情容器：
  1. `marks = root.querySelectorAll('mark.sm-mark')` → 匹配总数上报 `matchCount`；
  2. `marks[activeIndex]` 追加 `sm-mark-active` 类（DOM 类操作，React 不 diff 未受控类名），
     其余移除；当前项变化时 `scrollIntoView({ block: 'center' })` 焦点移动。
- 匹配语义与浏览器 Ctrl+F 一致：**只命中当前渲染可见的文本**（折叠的节点/卡片内未渲染
  的内容不参与），展开后自动重新计数。

### 2.4 复制所见即所得（修 1.4，随 2.1/2.2 自然达成）

`buildViewText(tab, view, value)` 契约不变（视图级完整文本）：

- `原文` → 原始文本；`JSON 美化` → 完整标准美化 JSON；
- `SSE 解析` → `sseEventsToText`（事件标签 + 美化 JSON，与卡片布局一致）；
- `聚合解析` → `aggregateToText`（事件分布/工具/usage/聚合文本，与面板布局一致）。

response_body 解码修复后，四类视图的复制内容与显示内容一致。

### 2.5 JsonTree「展开全部」+ SSE 卡片 JSON 树化（修 1.5 / 1.6）

- `JsonTree` 工具栏新增 `展开全部`：`setExpanded(collectContainerPaths(data, Number.POSITIVE_INFINITY))`
  —— 纯数据遍历收集全部容器路径（毫秒级）；DOM 防卡顿由既有渲染预算（4000 行）+
  大数组分页（每页 100 + 显示更多/显示全部）兜底，数据零丢失。
- `SseEventList` 事件卡片：`parsed` 非 null 时改用 `<JsonTree value={event.parsed} query={...} toolbar={false} />`
  渲染（对象直通解析；`toolbar={false}` 隐藏卡片内工具栏避免每卡一排按钮）——
  自动获得：标准 JSON 排版、语法高亮、逐节点折叠、超长字符串截断保护、查找高亮；
  `raw` 原文仍保留 `<pre>` + 查找高亮。

### 2.6 i18n 与样式

- i18n 三语言新增 `chatAnalysis.jsonExpandAll`（展开全部 / Expand all / すべて展開）。
- `index.css` 新增全局查找高亮样式：`mark.sm-mark`（黄底）与 `mark.sm-mark-active`
  （橙底 + 描边），沿用 `HighlightText` 既有配色。

## 3. 文件改动清单

| 文件 | 改动 |
|---|---|
| `ServerGo/models/subtable.go` | 新增 `tryDecodeBase64Text`；`GetAgentHttpTransactionFieldByID` 解码分支扩展覆盖 `response_body` |
| `ServerGo/api/test_api_transactions_test.go` | 新增 response_body 编码往返 + tryDecodeBase64Text 边界用例 |
| `ClientWeb/src/shared/viewText.js` | 新增 `viewsForTab` |
| `ClientWeb/src/shared/viewText.test.js` | 新增 `viewsForTab` 用例 |
| `ClientWeb/src/components/SearchText.jsx` | 新增：查找高亮哑组件 |
| `ClientWeb/src/components/HighlightText.jsx` | mark 改用 `sm-mark`；删除内部计数/滚动（交给 DOM 机制） |
| `ClientWeb/src/components/JsonTree.jsx` | 新增 `query`/`toolbar` props；文本节点接入 SearchText；工具栏新增「展开全部」 |
| `ClientWeb/src/components/SseEventList.jsx` | 新增 `query`；事件名/raw 接入 SearchText；parsed 改 JsonTree 渲染 |
| `ClientWeb/src/components/AggregateView.jsx` | 新增 `query`；文本块/标签接入 SearchText |
| `ClientWeb/src/pages/chat-analysis/DetailBody.jsx` | 查找模式保留原视图，传 `query`；视图裁剪 |
| `ClientWeb/src/pages/chat-analysis/DetailTabs.jsx` | 视图按钮按 `viewsForTab` 过滤 |
| `ClientWeb/src/pages/chat-analysis/InlineDetailRow.jsx` | 切 Tab 视图回落；DOM 查找计数/高亮/焦点机制 |
| `ClientWeb/src/index.css` | `sm-mark` 系列样式 |
| `ClientWeb/src/i18n/locales/{zh-CN,en,ja}.json` | `jsonExpandAll` |
| `ServerGo/config/config.go` | `APP_VERSION` v2.0.74 → v2.0.75 |

## 4. 验证计划

1. Go：`go test ./ServerGo/models/... ./ServerGo/api/...`（新增用例全绿）、`go vet` 通过。
2. 前端自检：`node src/shared/json.test.js`、`jsonTree.test.js`、`viewText.test.js`、
   `sse.test.js`、`clipboard.test.js`、`timeSpan.test.js` 全绿；`npx oxlint` 通过。
3. `./rebuild_restart_app.sh` 双构建 + 重启；按 CLAUDE.md 2.5 grep 复验 `dist-user` 无管理端字样。
4. 端到端（9101 / 29001）：打开 /ChatAnalysis → 展开记录：
   - `ResponseBody` 原文为明文 JSON/SSE 文本（非 Base64）；SSE 解析出事件卡片、聚合解析出面板；
   - `RequestBody` 无 SSE/聚合按钮；`ResponseBody` 无 JSON 美化按钮；跨 Tab 切换视图自动回落；
   - 查找在 JSON 树 / SSE 卡片 / 聚合面板布局内高亮，↑↓ 焦点移动、折叠节点后计数刷新；
   - 复制与当前视图显示一致；JsonTree 有「展开全部」；SSE 卡片内 JSON 可折叠/查找。
5. 中文 commit：`阶段AU：ChatAnalysis 对话详情修复与优化 — ResponseBody解码/视图裁剪/视图内查找/展开全部`。

## 5. 风险与边界

- `response_body` 解码采用"严格 base64 + UTF-8 合法才替换"策略，明文响应体（历史数据或未来
  写入策略变更）不受影响；`[truncated]` 截断体在编码前产生，解码后标记保留可读。
- DOM 查找机制只统计可见文本（浏览器 Ctrl+F 语义）；折叠容器内的匹配需展开后可见 —— 预期行为。
- `展开全部` 在超大 JSON 下受渲染预算（4000 行）约束会分段提示，属既有防卡顿设计。
