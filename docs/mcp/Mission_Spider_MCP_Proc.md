# Mission Spider MCP Proc - AI Agent 爬虫任务执行指南

**面向**: Claude Code、Kilo Code、OpenCode、pi、OpenClaw 等 AI Agent  
**服务地址**: `http://localhost:29002`  
**版本**: v2.0.17 patch

## 概述

MCP 爬虫服务基于 Chrome DevTools Protocol (chromedp) + headless Chrome：
- 支持 JS 渲染 / SPA / 表单交互 / 截屏
- **28 个交互 action**（v2.0.0 14 + v2.0.7 13 调试/观察 + v2.0.14 `restart_browser` 自愈）
- 每个 session 一个独立 Chrome tab，TTL=10 分钟
- **多 Tab 支持**（v2.0.7）：`new_tab` / `switch_tab` / `close_tab` / `list_tabs`
- **调试观察能力**（v2.0.7）：console / network / DOM 详情 / JS 求值 / 增强元素抽取
- **存储能力**（v2.0.7）：localStorage / sessionStorage / cookies 读写
- **鼠标 / 键盘全功能**（v2.0.7）：右键 / 中键 / 双击 / 坐标点击 / 滚轮 / 组合键 / 连续键入
- **SPA 水合探测**（v2.0.13+）：`wait_for_hydration` 等 React/Next/San/Vue 框架接管信号；v2.0.17 patch 新增 ES Module 加载可见性
- **MCP 自愈**（v2.0.14）：连续 `context canceled` 时调用 `restart_browser` action
- **主动反反爬**（v2.0.8）：per-session UA 轮换、动态请求头、代理池、指纹轮换、自动重试
- HTTP 回退路径已移除，Chrome 不可用时直接 `success=false`
- **调试 / 观察型数据不写入数据库**（v2.0.7）：console_logs / network_log / dom / storage / cookies / eval_result / tabs / extract_list 八个字段只在 `/SpiderWebData` 响应中返回
- **Agent 必须显式调用 `/InputSpiderDailyInfo` 保存数据**

## Agent 8 步标准流程

```
AI Agent
   ↓
GET /  健康检查
   ↓
POST /GetSpiderDataSource { user_id, is_admin, ...过滤 }
   跳过 status=0，解析 description
   ↓
POST /SpiderWebData { data_source_id / url }
   返回 HTML + content + session_id + elements
   ↓
   ├── 多轮：scroll / click / fill_form / extract / screenshot
   ↓
Agent 自行处理数据（清洗/提取/截断/翻译）
   ↓
POST /GetSpiderDailyInfo { data_source_id }
   按 data_source_id 查询最近已保存记录（默认 crawl_time DESC），用于去重
   ↓
Agent 去重：URL 完全相同 或 内容完全相同 → 跳过
   ↓
POST /InputSpiderDailyInfo  仅保存不重复的记录
```

### 步骤 1: 服务健康检查

```
GET http://localhost:29002/
```

期望：
```json
{
  "service": "LSM Spider MCP Service",
  "version": "2.0.0",
  "endpoints": ["/SpiderWebData", "/GetSpiderDataSource", "/InputSpiderDailyInfo", "/GetSpiderDailyInfo"]
}
```

### 步骤 2: 获取数据源列表（支持多维度过滤查询）

```json
POST /GetSpiderDataSource
{ "user_id": 0, "is_admin": true }
```

- `status=1` 启用，`status=0` 禁用（**必须跳过**）
- `description` 是 Agent 处理规则（翻译/字数/时间窗口/多轮）
- 权限：`is_admin=true` → 全部；`is_admin=false` → 自己 + 公共（`user_id=0`）

**过滤示例**（AND 关系）：

```json
{ "user_id": 0, "is_admin": true, "id": 6 }                                       // 精确 ID
{ "user_id": 0, "is_admin": true, "platform_name": "Tech" }                       // 模糊匹配
{ "user_id": 0, "is_admin": true, "status": 1 }                                   // 仅启用
{ "user_id": 0, "is_admin": true, "platform_name": "Tech", "status": 1 }          // 组合
```

### 步骤 3: 首次爬取

```json
POST /SpiderWebData
{ "data_source_id": 6 }
// 或 { "url": "https://techcrunch.com" }
```

响应字段：`content`（初步提取）、`raw_html`（完整 HTML）、`session_id`（多轮交互）、`language`（zh/en/unknown）、`elements`（links/headings/paragraphs）、`hydration_state`（v2.0.13+）

可选参数：`max_content_len`、`return_state`（返回 `page_state` 含链接列表）、`session_id`、`action`、`wait_for_hydration` / `wait_for_hydration_ms`

### 步骤 3.5: 元素清单分类（v2.0.1，Agent 自主判断）

`data.elements` 提供 3 类元素，Agent 决策场景：

| 元素 | 含义 |
|------|------|
| `elements.links` | `<a href>` 元素，去重+完整 URL；带 `scope` 标注（nav/article/list/body） |
| `elements.headings` | h1/h2/h3 标题；内嵌 `<a>` 时附 URL |
| `elements.paragraphs` | 候选长段落（中文>60字或英文>25词），带 `word_count` |

| 场景 | 特征 | 处理 |
|------|------|------|
| **单篇文章** | headings 1 个 H1 或 1-2 个 H2；paragraphs ≥3 word_count > 200；links.scope=article | 组合 `title + url + headings[0] + paragraphs[].text` |
| **文章列表** | headings ≥5 带 url；paragraphs 短（snippet 是摘要）；scope=list/body | 按页面顺序对齐 headings[] 和 paragraphs[] |
| **导航/入口页** | headings ≤2 或空；paragraphs 空；scope=nav | links[] 中挑关键词匹配入口，session_id + click 进入子页面递归 |

### 步骤 4: Agent 处理数据

Agent **必须**自己完成：
1. 清洗内容：去广告 / 导航 / Footer
2. 提取文章：从 `raw_html` 或 `content` 提取文章列表
3. 时间窗口：按 `description` 过滤
4. 字数截断：按 `description` 限制
5. 翻译：Agent 自己实现

### 步骤 5: 多轮交互（28 个 action）

**v2.0.0 已有 14 个**：navigate / click / scroll / scroll_to / fill_form / extract / screenshot / get_state / wait / hover / select / keypress / switch_frame / drag_and_drop

**v2.0.7 新增 13 个（鼠标 / 键盘 / Tab / 调试 / 存储）**：right_click / double_click / middle_click / click_at（坐标 + 偏移）/ mouse_move / wheel / press_key（组合键）/ type_text / new_tab / switch_tab / close_tab / list_tabs / console_logs / network_log / elements / dom / eval / local_storage / session_storage / cookies / upload_file / element_screenshot

**v2.0.14 新增 1 个（自愈）**：

| 类型 | 说明 | 必填参数 |
|------|------|----------|
| `restart_browser` | 强制重启 Chrome + 主动释放所有 session 的 cdpCtx/cdpTarget（防 v2.0.14 级联 context canceled） | - |

**选择器语法**：`.class` / `#id` / `text:xxx` / `xpath:expr` / `tag`

**多轮示例**：

```json
// 滚动加载
{ "session_id": "spider_xxx", "action": { "type": "scroll" } }
// 点击翻页
{ "session_id": "spider_xxx", "action": { "type": "click", "selector": ".next-page" } }
// 提取内容
{ "session_id": "spider_xxx", "action": { "type": "extract", "params": { "selector": ".article-title", "limit": 50 } } }
// 截屏
{ "session_id": "spider_xxx", "action": { "type": "screenshot" } }
// 组合键 Ctrl+A / Ctrl+C
{ "session_id": "spider_xxx", "action": { "type": "press_key", "params": { "key": "a", "modifiers": ["ctrl"] } } }
// JS 求值
{ "session_id": "spider_xxx", "action": { "type": "eval", "params": { "expression": "Array.from(document.querySelectorAll('a')).map(a => a.href)", "await_promise": false } } }
// v2.0.14 自愈：连续 context canceled 时调用
{ "session_id": "spider_xxx", "action": { "type": "restart_browser" } }
```

> **不写入数据库**（v2.0.7 约定）：`console_logs` / `network_log` / `dom` / `storage` / `cookies` / `eval_result` / `tabs` / `extract_list` 八个字段是**调试 / 观察型数据**，**不写入数据库**。Agent 自行决定是否调用 `/InputSpiderDailyInfo` 保存。

Session TTL=10 分钟；过期需重新创建。

### 步骤 6: 查询已有记录并去重

保存前先查询该数据源最近已保存的记录，对本次爬取结果去重：

```json
POST /GetSpiderDailyInfo
{ "data_source_id": 6 }
```

- 默认按 `crawl_time DESC` 排序，首页返回最近 20 条，通常足够覆盖本轮去重
- 若本轮爬取文章较多，可调大 `page_size`（上限 100）

**去重规则**（命中任一即跳过，不保存）：
1. **URL 完全匹配**：新记录的 `url` 与已有记录的 `url` 完全相同
2. **内容完全匹配**：新记录的 `content` 与已有记录的 `content` 完全相同

### 步骤 7: 保存数据（必须调用，仅保存不重复的记录）

```json
POST /InputSpiderDailyInfo
{
  "data_source_id": 6,
  "platform_name":  "TechCrunch",
  "title":          "AI Breakthrough",
  "content":        "Agent 处理后的内容...",
  "raw_data":       "<!DOCTYPE html>...",
  "crawl_time":     "2026-06-17T14:30:00Z",
  "url":            "https://techcrunch.com"
}
```

> 多篇文章/列表数据逐条保存，每条独立去重后调用。

### 步骤 8: 验证写入（可选）

```json
POST /GetSpiderDailyInfo
{ "data_source_id": 6, "page": 1, "page_size": 5 }
```

- 三种模式：单条（`id`）、批量（`ids`）、分页（`page` + `page_size` + 过滤条件）
- 默认不返回 `raw_data`，需 `include_raw_data: true`

## SPA 兼容性 Agent 决策树（v2.0.11 → v2.0.17 patch）

复杂受控 SPA（典型场景：chat.baidu.com 文心一言 React 18+）下，**应首选 `eval` 单 roundtrip** —— 同一 `await_promise=true` 表达式里完成 `set value` + `dispatchEvent InputEvent('beforeinput'/'input')` + `click` + 流式轮询，不要拆 `fill_form` + 多次 `click` roundtrip（chromedp 在 React 高频 re-render 下深度死锁，watchdog 救不回来）。

| 信号 | 场景 | 行动 |
|------|------|------|
| `click_effect_verification.effect_verified=true` + warning 含 `auto-recovered by ancestor` | 第 1 类（v2.0.12 自动祖先补救成功） | 继续，下轮直接用祖先 selector |
| `click_effect_verification.effect_verified=false` + warning 含 `spa_no_effect` + `ancestor probe: no clickable ancestor found` | 第 1 类（祖先补救失败） | 改用 eval 单 roundtrip |
| `fill_form_result.fields[].diagnostics.framework_consumed=false` | 第 2/3 类（受控组件未消费） | 改用 eval 单 roundtrip |
| `network_log` 中无提交 API | 几乎肯定是受控 SPA 场景 | eval 单 roundtrip |
| **`hydration_state.state=timeout` + `detected_framework=static` + `console_lines=0`** | **页面未水合（v2.0.13）** | **直接换 API 直连，不要 retry fill_form/click/eval** |
| **`hydration_state.module_loads_failed>0` 或 `module_zero_transfer>0`** | **客户端 bundle 被 CSP/CDN 拦截（v2.0.17 patch）** | **直接换 API 直连** |
| 连续 `context canceled` 错误 | v2.0.14 级联故障 | 先 `action: "restart_browser"` 100% 自愈；还失败则 API 直连 |

### chat.baidu.com eval 单 roundtrip 首选模板

```js
// 1) set 原生 setter 触发 React state + 增派 beforeinput
Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value')
  .set.call(el, text);
el.dispatchEvent(new InputEvent('beforeinput', { bubbles: true, inputType: 'insertText', data: text }));
el.dispatchEvent(new InputEvent('input', { bubbles: true, inputType: 'insertText', data: text }));
// 2) 等待 React state 提交
await new Promise(r => setTimeout(r, 800));
// 3) submit
submitBtn.click();
// 4) 流式阶段必须边轮询边保留 maxLength 版本（不能等"稳定"再读）
await new Promise(r => setTimeout(r, 1500));
let fullText = '';
for (let i = 0; i < 60; i++) {
  const t = document.body.innerText;
  if (t.length > fullText.length) { fullText = t; }
  await new Promise(r => setTimeout(r, 500));
}
// 同时记录 rawText 和 fullText 的最大长度
```

实测：chat.baidu.com 11 分钟内 100% 拿到完整 AI 回复；fill_form + click 多 roundtrip 0% 完成。

## 错误处理与重试

| 阶段 | error_type | 处理建议 |
|------|------------|----------|
| 健康检查 | 连接失败 | 检查 MCP 服务 / 端口 29002 / Chrome 进程 |
| 获取数据源 | 数据库错误 | 检查 MySQL 连接；记录错误继续 |
| 爬取网页 | `timeout` | 缩短操作链、减少页面加载、稍后重试 |
| 爬取网页 | `timeout_hard` | 页面加载过慢，考虑跳过或换数据源 |
| 爬取网页 | `region_block` | 403/451/UA 黑名单，记录失败继续下一个 |
| 爬取网页 | `captcha` | 验证码，当前无法处理，记录失败继续 |
| 爬取网页 | `anti_bot` | 反爬特征命中，记录失败继续 |
| 爬取网页 | `rate_limit` | 429 限流，降低频率稍后重试 |
| 爬取网页 | `spa_no_effect` | v2.0.11 新增；改用 eval 单 roundtrip |
| 爬取网页 | 级联 `context canceled` | v2.0.14：先 `restart_browser` 自愈 |
| **水合诊断** | **`hydration_state.state=timeout` + bundle 拦截** | **直接换 API 直连**（v2.0.17 patch） |
| 查询去重 | 接口失败 | 失败时仍可保存（保守：宁可重复不丢数据） |
| 保存数据 | 数据库错误 | 检查 MySQL / `data_source_id` 是否存在 |

**重试**：1s → 2s → 4s → 8s 指数退避，最多 3-5 次。  
**超时**：单次 handler 硬超时 180s（上限 300s），软超时 +60s 缓冲。**Agent 应在 4m30s 前进入保存阶段**。

## 最佳实践 Checklist

执行前：
- [ ] MCP 服务健康检查通过
- [ ] 获取数据源列表，支持过滤参数（`status=1`、`platform_name=xxx`）
- [ ] 跳过 `status=0`
- [ ] 仔细解析 `description`，确定处理策略

执行中：
- [ ] 首次爬取获取原始数据
- [ ] 根据 `description` 决定多轮动作
- [ ] 复杂受控 SPA 首选 `eval` 单 roundtrip
- [ ] **`hydration_state` 提示未水合或 bundle 拦截时，直接换 API 直连**
- [ ] 连续 `context canceled` 时调用 `restart_browser` 自愈
- [ ] Agent 自己处理数据（清洗/提取/截断/翻译）
- [ ] 保存前调用 `/GetSpiderDailyInfo` 查询已有记录，按 URL + 内容去重
- [ ] **必须调用** `/InputSpiderDailyInfo` 保存不重复的记录
- [ ] 频率控制避免触发目标站限流
- [ ] **时间控制**：总任务建议 4m30s 前进入保存阶段

执行后：
- [ ] 统计每个数据源状态：enabled / saved / failed
- [ ] 失败的数据源记录 `error_type` 和错误信息继续下一个
- [ ] 输出总报告

## 相关文档

- `MCP_SpiderWebData_def.md` - 爬取接口详细定义（v2.0.17 patch）
- `MCP_GetSpiderDataSource_def.md` - 数据源接口详细定义（v2.0.4）
- `MCP_InputSpiderDailyInfo_def.md` - 保存接口详细定义（v2.0.0）
- `MCP_GetSpiderDailyInfo_def.md` - 查询接口详细定义（v2.0.5）
- `CLAUDE.md` / `AGENT.md` - AI Agent 工具规范
