# MCP SpiderWebData 接口定义

**版本**: v2.0.17 patch  
**接口路径**: `/SpiderWebData`  
**请求方法**: POST  
**服务地址**: `http://localhost:29002`

## 概述

通过 Chrome DevTools Protocol (chromedp) 抓取网页内容，支持 JS 渲染 / SPA / 表单交互 / 多轮会话。

核心能力：
- 真实浏览器渲染（headless Chrome，会话 TTL=10 分钟）
- **28 种交互动作**（v2.0.0 14 + v2.0.7 新增 13 调试/观察型 + v2.0.14 新增 `restart_browser` 自愈）
- 结构化元素抽取 `elements`（links / headings / paragraphs），Agent 自主判断页面类型
- 调试观察：`console_logs` / `network_log` / `dom` / JS 求值 `eval`
- 存储观察/操作：`localStorage` / `sessionStorage` / `cookies`
- 多 Tab 管理：`new_tab` / `switch_tab` / `close_tab` / `list_tabs`
- **主动反反爬**（v2.0.8）：per-session UA 轮换、动态请求头、代理池、行为抖动、指纹轮换、反爬自动重试
- **客户端水合探测**（v2.0.13+）：`wait_for_hydration` 等待 SPA 框架接管；v2.0.17 patch 新增 ES Module 加载可见性
- **MCP 浏览器自愈**（v2.0.14）：级联 `context canceled` 时用 `restart_browser` 主动恢复
- 原始数据返回给 Agent，**不写入数据库**（Agent 自行决定调用 `/InputSpiderDailyInfo`）

## 请求参数

```json
{
  "url":                   { "type": "string",  "description": "首次请求必填" },
  "timeout":               { "type": "number",  "description": "超时（秒），默认 30" },
  "data_source_id":        { "type": "integer", "description": "数据源 ID（用于获取 URL）" },
  "max_content_len":       { "type": "integer", "description": "初步提取最大长度，默认 10000" },
  "session_id":            { "type": "string",  "description": "会话 ID，多轮对话使用" },
  "action":                { "type": "object",  "description": "交互动作" },
  "return_state":          { "type": "boolean", "description": "是否返回 page_state，默认 false" },
  "wait_for_hydration":    { "type": "boolean", "description": "v2.0.13：navigate 后等待 SPA 水合，默认 false" },
  "wait_for_hydration_ms": { "type": "integer", "description": "等待水合毫秒，默认 2000，上限 5000" }
}
```

### action 字段

```json
{
  "type":     "navigate/click/scroll/...（见表）",
  "url":      "navigate 目标 URL",
  "selector": "元素选择器（.class / #id / text:xxx / tag）",
  "xpath":    "XPath 备选",
  "params":   "其他动作参数"
}
```

### 选择器语法

| 语法 | 示例 |
|------|------|
| `.classname` | `.next-page` |
| `#id` | `#submit` |
| `text:keyword` | `text:登录` |
| `xpath:expr` | `xpath://div[@id]` |
| `tag` | `button` |

## 交互动作（28 个）

### 14 个 v2.0.0 基础

| 类型 | 说明 | 必填参数 |
|------|------|----------|
| `navigate` | 导航到新 URL | `url` |
| `click` | 点击元素 | `selector` 或 `xpath` |
| `scroll` | 滚动页面 | - |
| `scroll_to` | 滚动到位置 | `params.y` |
| `fill_form` | 填写表单 | `params.fields: [{selector, value}, ...]` |
| `extract` | 提取内容 | `params.selector`（非空）、`params.limit?` |
| `screenshot` | 截屏 | `params.quality?` `params.full_page?` |
| `get_state` | 获取页面状态 | - |
| `wait` | 等待元素出现 | `params.selector` `params.timeout`（默认 10s，上限 300s） |
| `hover` | 鼠标悬停 | `params.selector` |
| `select` | 下拉框选择 | `params.selector` + `params.value`/`text` |
| `keypress` | 键盘按键 | `params.key` `params.modifiers?`（v2.0.12 起有 1500ms watchdog） |
| `switch_frame` | 切换 iframe | `params.selector`/`index`/`reset` |
| `drag_and_drop` | 拖拽元素 | `params.source` `params.target` |

### 14 个调试/鼠标/键盘/Tab/存储/自愈（v2.0.7 + v2.0.14）

| 类型 | 说明 | 必填参数 |
|------|------|----------|
| `right_click` | 右键点击 | `selector` |
| `double_click` | 双击 | `selector` |
| `middle_click` | 中键点击 | `selector` |
| `click_at` | 坐标点击 / 选择器+偏移 | `params.x,y` 或 `selector` + `offset_x/offset_y` |
| `mouse_move` | 移动鼠标 | `params.x,y` 或 `selector` |
| `wheel` | 滚轮滚动 | `params.delta_y?` `params.delta_x?` |
| `press_key` | 组合键 | `params.key` + `params.modifiers[]`（ctrl/alt/shift/meta） |
| `type_text` | 连续键入文本 | `params.text` `selector?` |
| `new_tab` | 打开新 Tab | `params.url?` `params.alias?` |
| `switch_tab` | 切换 tab | `params.alias?` 或 `params.index?` |
| `close_tab` | 关闭 tab | `params.alias?` 或 `params.index?` |
| `list_tabs` | 列出所有 tab | - |
| `console_logs` | 读 console 输出 | `params.wait_ms?` `params.clear?` |
| `network_log` | 读 fetch/xhr | `params.wait_ms?` `params.filter?` `params.limit?` `params.clear?` |
| `elements` | 增强元素抽取 | `params.selector` `params.scope?` `params.attributes?` `params.limit?` |
| `dom` | 单 DOM 节点详情 | `params.selector` `params.include_computed_style?` `params.computed_keys?` |
| `eval` | 在页面上下文执行 JS | `params.expression` `params.await_promise?` |
| `local_storage` | localStorage 操作 | `params.op`（get/set/remove/clear/keys） |
| `session_storage` | sessionStorage 操作 | 同上 |
| `cookies` | cookies 操作 | `params.op`（get/set/delete/clear）|
| `upload_file` | input[type=file] | `params.selector` `params.files[]` |
| `element_screenshot` | 元素级截图 | `params.selector` `params.quality?` |
| **`restart_browser`** | **v2.0.14 自愈：强制重启 Chrome，清理所有 session** | **-（无需参数）** |

## 响应格式

### 业务字段

```json
{
  "success": true,
  "message": "Crawl completed",
  "data": {
    "url":          "https://...",
    "title":        "...",
    "content":      "初步提取内容",
    "raw_html":     "<!DOCTYPE html>...",
    "crawl_time":   "2026-06-17T14:30:00Z",
    "language":     "zh/en/unknown",
    "data_source_id": 8,
    "session_id":   "spider_1234567890123",
    "page_state":   { "url": "...", "title": "...", "links": [...], "forms": [...], "scroll_y": 0, "content_type": "..." },
    "has_more":     true,
    "screenshot":   "base64 PNG（screenshot/element_screenshot 时）",
    "elements": {
      "links":      [{ "text": "...", "href": "...", "url": "...", "scope": "nav/article/list/body" }],
      "headings":   [{ "level": 2, "text": "...", "url": "..." }],
      "paragraphs": [{ "text": "...", "snippet": "...", "word_count": 12 }]
    },
    "click_effect_verification": {  // v2.0.11
      "effect_verified": false,
      "has_element_change": false,
      "has_network_change": false,
      "warning": "spa_no_effect ..."
    },
    "fill_form_result": {           // v2.0.11
      "fields": [{ "selector": "...", "verified_ok": false, "diagnostics": { "framework_consumed": false, "react_tracker_value": "...", "has_value_tracker": true } }]
    },
    "hydration_state": {            // v2.0.13+
      "state": "hydrated/none/timeout",
      "wait_ms": 2000,
      "fiber_roots_count": 0,
      "has_next": false,
      "has_san":  false,
      "has_vue":  false,
      "detected_framework": "static",
      "warning": "client bundle never executed",
      "module_loads_total":  12,     // v2.0.17 patch
      "module_loads_failed": 4,      // v2.0.17 patch（duration=0 视为未启动）
      "module_zero_transfer": 2,     // v2.0.17 patch（duration>0 + transferSize=0，CDN 拦截嫌疑）
      "module_failed_urls":  ["not_started:https://...", "zero_transfer:https://..."]
    }
  }
}
```

### 调试/观察型字段（v2.0.7+，**不写入数据库**）

`extract_list` / `console_logs` / `network_log` / `dom` / `storage` / `cookies` / `eval_result` / `tabs`

### 失败响应

```json
{
  "success": false,
  "message": "Crawl failed: timeout",
  "data": {
    "error_type": "timeout",
    "signals":    ["..."],
    "partial_result": { ... }
  }
}
```

## 业务错误类型（data.error_type）

| error_type | 含义 | Agent 建议 |
|------------|------|------------|
| `timeout` | 超过软超时（180s+60s） | 缩短操作链，减少页面加载，稍后重试 |
| `timeout_hard` | 超过硬超时（180s） | 页面加载过慢，考虑跳过或换数据源 |
| `rate_limit` | 429 Too Many Requests | 降低频率、增加延迟、稍后重试 |
| `region_block` | 403/451 UA 黑名单 | 记录失败继续下一个 |
| `captcha` | 验证码/挑战页 | 当前无法处理，记录失败继续下一个 |
| `anti_bot` | 反爬特征命中 | 记录失败继续下一个 |
| `interaction_failed` | CDP 交互失败 | 改用 eval 兜底或换坐标点击 |
| `spa_no_effect` | v2.0.11 click/click_at 已派发但业务侧无副作用 | 改用 eval 单 roundtrip |

## Agent 决策参考（elements 三场景）

| 场景 | 特征 | 处理 |
|------|------|------|
| **单篇文章** | headings 只有 1 个 H1 或 1-2 个 H2；paragraphs ≥3 且 word_count > 200；links.scope=article 占多数 | 组合 title + url + headings[0] + paragraphs[].text |
| **文章列表** | headings ≥5 且每条带 url；paragraphs 短（snippet 即摘要）；links.scope=list/body | 按页面顺序对齐 headings[] 和 paragraphs[]，组成 `[{ title, snippet, url }]` |
| **导航/入口页** | headings ≤2 或空；paragraphs 为空；links.scope=nav 占多数 | 在 links[] 中挑关键词匹配入口，`session_id` + `action.click` 进入子页面，递归直到命中场景 1 或 2 |

`links[].url` 为 resolveURL 后的完整 URL；`scope` 标注 nav/article/list/body；`paragraphs[].word_count` 中文字符数用于快速判定。

## 已知不稳定场景（v2.0.11 → v2.0.17 patch）

Agent 应**首选 `eval` 单 roundtrip**（同一 `await_promise=true` 表达式里完成 `set value` + `dispatchEvent InputEvent` + `click` + 流式轮询），不要拆 `fill_form` + 多次 `click` roundtrip（chromedp 在 React 高频 re-render 下深度死锁）：

| 场景 | 表现 | 处置 |
|------|------|------|
| React 18+ SPA 自定义 onClick 提交（如 chat.baidu.com 文心一言） | click/click_at/keypress Enter/eval MouseEvent 全部不触发；右侧始终空 | v2.0.12 「最近的可点击祖先」自动补救（向上 6 层找 A/BUTTON/INPUT/role=button/link/data-submit/cursor:pointer 未 disabled 祖先并 click）；补救成功时 `effect_verified=true` + warning 含 `auto-recovered by ancestor`；失败时改 eval 单 roundtrip（**首选模板**：(1) `Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype,'value').set.call(el, text)` + `dispatchEvent(new InputEvent('beforeinput',{bubbles:true,inputType:'insertText',data:text}))` + `dispatchEvent(new InputEvent('input',{bubbles:true,inputType:'insertText',data:text}))`；(2) `setTimeout(800)` 等 React state 提交；(3) `submitBtn.click()`；(4) 轮询 `document.body.innerText` 同时保留 `maxLength` 版本，**不能等稳定再读**） |
| 复杂受控组件每次 render 复位 value | fill_form 返回 `framework_consumed=false` | v2.0.12 受控输入 JS 增派 `beforeinput`（React 18+/San 要求）；同上 |
| Vue 3 + 自定义受控指令的复杂表单 | DOM value OK 但 Vue state 未更新 | eval 中读 `el.__vueParentComponent.ctx` 校验 |
| chromedp.WaitReady("body") 高频重渲染挂死 | click/double_click 10s+ 后 deadline exceeded | v2.0.11 per-action watchdog（1500ms）；v2.0.12 keypress 也复用；仍超时改 eval |
| 受 contenteditable 控制富文本编辑器（quill/tiptap/ProseMirror） | SendKeys / runControlledInputJS 都不触发内部 model | 暂不支持；评估改 WebSocket / 协议直接发请求 |
| **级联 `context canceled`** | 一连串 `attachCDPContext navigate failed: context canceled` / `CDP fetch failed: context canceled` 导致所有 action 失败 | v2.0.14 主动调用 `action: "restart_browser"` 100% 自愈（响应 < 1s）；还失败则评估 API 直连 |

### Agent 自检 checklist（按顺序判断）

1. `click_effect_verification.effect_verified=true` + warning 含 `auto-recovered by ancestor` → 第 1 行已被 MCP 救活，无需改 eval
2. `click_effect_verification.effect_verified=false` + warning 含 `spa_no_effect` + `ancestor probe: no clickable ancestor found` → 第 1 行，需改 eval 单 roundtrip
3. `fill_form_result.fields[].diagnostics.framework_consumed=false` + warning 含"framework 未消费" → 第 2/3 行
4. `network_log` 中无目标站点的提交 API → 几乎肯定是上述场景之一
5. v2.0.13：`wait_for_hydration=true` 后 `hydration_state.state=timeout` + `detected_framework=static` + console_lines=0 → 页面未水合（所有 CDP 事件无回调），**直接换 API 直连**
6. v2.0.14：连续出现 `context canceled` → 先 `action: "restart_browser"` 自愈；还失败则换 API 直连
7. v2.0.17 patch：**`hydration_state.module_loads_failed>0` 或 `module_zero_transfer>0`** → 客户端 bundle 被 CSP/CDN 拦截，后续 fill_form/click/eval 都不会工作，直接换 API 直连（**不要 retry**）
8. **chat.baidu.com 实测结论**：(a) navigate **不要**传 `wait_for_hydration=true`（v2.0.14 经验：触发级联 context canceled）；(b) `restart_browser` 是 100% 可靠恢复手段；(c) `framework_consumed=true` 是**误导信号**，DOM value 仍会被 React 重置；(d) 流式阶段必须边轮询边保留 `maxLength` 版本

### v2.0.17 patch：客户端 bundle 被拦截警告

`MCPSpiderWebDataHandler` 在原有「三连击」(state=timeout + framework=static + console_lines=0) 警告基础上，**新增**「客户端 bundle 被拦截」警告：
- 触发条件：`state=timeout` + `framework=static` + (`ModuleLoadsFailed>0` 或 `ModuleZeroTransfer>0`)
- 提示 Agent 评估改走 API 直连
- `HydrationDiagnostics` 新字段 `omitempty`：0/nil/"" 时自动抑制

## 配置项

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `spiderCDPPort` | Chrome DevTools 端口 | 9222 |
| `spiderChromePath` | Chrome 可执行路径 | `google-chrome-stable` |
| `spiderChromeUserDataDir` | Chrome 用户数据目录 | `/tmp/lsm-spider-chrome` |
| `spiderCDPHealthCheckSec` | 健康检查间隔（秒） | 30 |
| `spiderCDPStartTimeoutSec` | Chrome 启动超时（秒） | 30 |
| `spiderHandlerTimeoutSec` | Handler 硬超时（秒） | 180（上限 300） |
| `spiderMaxConcurrency` | 最大并发 tab 数 | 8（上限 64） |
| `spiderChromeCustomArgs` | 额外 Chrome 启动参数 | `[]` |
| `spiderUserAgent` | 全局自定义 UA | `""`（探测 Chrome 版本拼装 Linux UA） |
| `spiderUserAgentPerSource` | 按数据源配置 UA | `{}`（v2.0.8 真正生效） |
| `spiderProxy` | 代理服务器 | `""`（如 `http://host:port`） |
| `spiderActionWaitSec` | wait action 超时上限 | 60（上限 300） |

### v2.0.8 反反爬（默认与 v2.0.7 行为一致）

| 配置项 | 说明 | 默认值 |
|--------|------|--------|
| `spiderEnableUAFlip` | per-session UA 轮换（内置 12 UA 池） | `false` |
| `spiderUAFlipPool` | 自定义 UA 池（覆盖内置池） | `[]` |
| `spiderProxyPool` | 代理池（`spiderProxy` 为空时启用） | `[]` |
| `spiderPerSourceProxy` | per-data_source 代理覆盖 | `{}` |
| `spiderRequestHeaders` | 额外请求头（Accept-Language / Referer 等） | `{}` |
| `spiderMinNavDelayMs` / `spiderMaxNavDelayMs` | pre-nav 抖动（ms，0=关闭） | `0` / `0` |
| `spiderFingerprintPerSession` | per-session 指纹轮换（viewport/hardware/...） | `false` |
| `spiderAntiBotAutoRetry` | 反爬自动重试次数（0-5，仅 retry `anti_bot`/`captcha`） | 2 |
| `spiderStealthScript` | 用户注入 stealth JS 前缀（最大 16KB） | `""` |

**UA 选择优先级**（per session 首次 navigate 决定）：
1. `spiderUserAgentPerSource[data_source_id]` — v2.0.8 起真正生效（修复历史 bug）
2. `spiderUAFlipPool` 轮询（若非空）
3. 内置 12 UA 池轮询（`spiderEnableUAFlip=true`）
4. `spiderUserAgent`（v2.0.3 全局）
5. 引擎默认探测链（v2.0.3 行为）

## 错误码

| HTTP | 说明 |
|------|------|
| 200 | 始终 200，业务结果看 `success` 字段 |
| 405 | 仅支持 POST |
