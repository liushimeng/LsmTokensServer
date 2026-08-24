# Go Web 调试工具链 (go-web-debug-tool)

> 本文档是 `go-web-debug-tool` 子模块的统一入口；详细规范请参阅子模块内的文档。
>
> 历史说明：早期版本曾使用 Playwright + Python 实现的 `python-web-debug-tool`，已于 2026-07-03
> 替换为基于 Go + chromedp 的 `go-web-debug-tool` HTTP MCP 调试服务。所有原 Python 子模块命令
> 已废弃，新接入请直接阅读下面表格与子模块文档。

---

## 快速导航

| 文档 | 位置 | 用途 |
|------|------|------|
| Agent 接入主文档 | [`go-web-debug-tool/MCP_Proc_Def.md`](go-web-debug-tool/MCP_Proc_Def.md) | 服务概览、CLI、认证、错误码、五个接口、典型流程 |
| 开发者 SOP | [`go-web-debug-tool/CLAUDE.md`](go-web-debug-tool/CLAUDE.md) | Claude Code 在子工程内的强制开发规范 |
| Kilo 入口指针 | [`go-web-debug-tool/KILO.md`](go-web-debug-tool/KILO.md) | Kilo Agent 进入子工程的最短路径 |
| SubAgent 拆分策略 | [`go-web-debug-tool/AGENT.md`](go-web-debug-tool/AGENT.md) | 主 Agent / SubAgent 协作、上下文压缩、异常重连 |
| 子模块 README | [`go-web-debug-tool/README.md`](go-web-debug-tool/README.md) | 子模块功能概述与构建说明 |
| 单接口详细定义 | `go-web-debug-tool/MCP_NewChromePage_Def.md` / `MCP_ControlChromePage_Def.md` / `MCP_LookChromePageInfo_Def.md` / `MCP_CloseChromePage_Def.md` / `MCP_ListChromePages_Def.md` | 五个 RESTful 接口的字段规范 |
| 主项目集成 | [`CLAUDE.md`](CLAUDE.md) / [`KILO.md`](KILO.md) / [`AGENT.md`](AGENT.md) | 各 AI Agent 工具的集成说明 |

---

## 首次使用前

### 1. 确认子模块存在

```bash
ls -la go-web-debug-tool/
```

> 如果子目录为空，先在父项目根目录执行 `git submodule update --init --recursive` 拉取。

### 2. 编译二进制

```bash
cd go-web-debug-tool
go build -o GoWebDebugTool .
```

### 3. 启动服务

```bash
./GoWebDebugTool -d            # 守护进程模式（首次启动会生成 GoWebDebugTool.conf）
```

启动日志会写入 `GoWebDebugTool.log`（路径可配置），PID 文件位于 `GoWebDebugTool.pid`。

### 4. 阅读完整规范

入口文档：[`go-web-debug-tool/MCP_Proc_Def.md`](go-web-debug-tool/MCP_Proc_Def.md)。

---

## 服务概览

- **本地监听**：`http://localhost:28999`（可在 `GoWebDebugTool.conf` 中修改）
- **协议**：所有接口均为 `POST + application/json`，统一信封 `{code, message, data}`
- **认证**：若配置 `auth_token` 非空，则每个请求必须携带 `Authorization: Bearer <token>` 或 `X-Auth-Token: <token>`
- **错误码**：`1000` 系列为请求错误，`2000` 系列为页面/CDP 错误，`3000` 系列为服务端内部错误

| Code | 含义 | 推荐处理 |
|------|------|----------|
| 0 | Success | 正常处理 `data` |
| 1000 | Invalid JSON body | 检查请求体语法，不要重试 |
| 1001 | Missing/invalid parameter | 校对参数表 |
| 1002 | Unauthorized | 检查 `auth_token` 与请求头 |
| 2000 | page_id not found | 先调 `/ListChromePages` 恢复索引 |
| 2001 | CDP disconnected | 等 1-3s 后重试；3 次失败后重新 `/NewChromePage` |
| 2002 | Action execution failed | 检查 selector / 参数；React 受控组件可试 `input_text use_js=true` |
| 2003 | Page crashed | `/CloseChromePage` 清理，重新开页 |
| 3000 | Internal server error | 查看服务日志 |

---

## 五个接口一览

| 接口 | 用途 |
|------|------|
| `POST /NewChromePage` | 打开一个新的 Chrome 页面 / 标签 |
| `POST /ControlChromePage` | 在指定 page 上执行 click / input_text / eval_js / scroll / 等 action |
| `POST /LookChromePageInfo` | 读取 Console / Network / DOM / screenshot 等 |
| `POST /CloseChromePage` | 关闭页面并释放 CDP 上下文 |
| `POST /ListChromePages` | 枚举当前所有受管页面 |

---

## 常用命令速查

### 服务管理

| 任务 | 命令 |
|------|------|
| 前台运行（开发） | `cd go-web-debug-tool && ./GoWebDebugTool` |
| 守护进程模式启动 | `cd go-web-debug-tool && ./GoWebDebugTool -d` |
| 优雅停止 | `cd go-web-debug-tool && ./GoWebDebugTool -u` |
| 指定配置文件 | `./GoWebDebugTool -c /path/to/conf` |
| 一键重建并守护重启 | `cd go-web-debug-tool && ./rebuild_restart_app.sh` |

### Agent 典型调用序列

```bash
# 1. 启动服务（如未启动）
cd go-web-debug-tool && ./GoWebDebugTool -d

# 2. 列出当前受管页面（启动 / 失联恢复时调用）
curl -sS -X POST http://localhost:28999/ListChromePages -H 'Content-Type: application/json' -d '{}'

# 3. 新建一个 Chrome 页面
curl -sS -X POST http://localhost:28999/NewChromePage -H 'Content-Type: application/json' \
  -d '{"url":"https://example.com"}'

# 4. 在指定 page 上点击 / 填表
curl -sS -X POST http://localhost:28999/ControlChromePage -H 'Content-Type: application/json' \
  -d '{"page_id":"p_xxxxxxxx","action":"click","params":{"selector":"#submit"}}'

# 5. 读页面状态 (console / network / screenshot / dom / 等)
curl -sS -X POST http://localhost:28999/LookChromePageInfo -H 'Content-Type: application/json' \
  -d '{"page_id":"p_xxxxxxxx","info":"screenshot","params":{"full_page":true}}'

# 6. 关闭页面
curl -sS -X POST http://localhost:28999/CloseChromePage -H 'Content-Type: application/json' \
  -d '{"page_id":"p_xxxxxxxx"}'
```

> 实际接入建议由 Claude Code / Kilo / OpenCode 等 Agent 通过 MCP 风格调用，而非直接 curl；详见
> [`go-web-debug-tool/MCP_Proc_Def.md`](go-web-debug-tool/MCP_Proc_Def.md) 第 6 节「典型调用流程」。

---

## 核心特性

- **统一信封**：`{code, message, data}` 所有接口格式一致，便于 Agent 解析。
- **新 Tab 自动追踪**：`target.Created` 事件被服务端监听，新页面的 `page_id` 通过 `spawned_page_id` 字段回传。
- **Console / Network 环形缓冲**：各 500 条，避免 Agent 直接消费原始数组造成上下文爆炸。
- **反自动化检测**：`anti_detect=true` 默认开启，注入 `--enable-automation=false` + `navigator.webdriver` shim，
  绕过常见 WAF 对 `navigator.webdriver === true` 的判定。
- **三层 Chrome 进程生命周期兜底**：janitor 后台扫描 + close_tab 同步清理 + 启动时孤儿 Chrome 清理。
- **PID + 守护进程**：`-d` 自动 SIGTERM 旧进程并守护重启；`-u` 优雅停止并删除 PID 文件。
- **per-action watchdog**：单 action 同时受软超时（`page_timeout_seconds`）与硬超时（`×2`）两层保护，
  即使 React 受控组件死循环也不会无限期挂死。

---

## 反自动化检测 (anti_detect) 详解

为绕过常见 WAF / 反爬对 `navigator.webdriver === true` 的判定，默认 `anti_detect=true` 启用三层防护：

1. **启动 flag**：`--enable-automation=false`，覆盖 chromedp 默认值，禁止 Chrome 自动设置 `navigator.webdriver`。
2. **启动 flag**：`--disable-blink-features=AutomationControlled`，关闭 Blink 引擎的 AutomationControlled 特性。
3. **页面级 shim**：`Page.addScriptToEvaluateOnNewDocument` 在每个新文档创建前注入
   `Object.defineProperty(navigator, 'webdriver', {get: () => false, configurable: true})`。

三层互为冗余，任何一层回归不会单独暴露指纹。需要原始 chromedp 行为时改为 `anti_detect=false`。

---

## 详细文档

完整规范和详细用法请参阅：[`go-web-debug-tool/MCP_Proc_Def.md`](go-web-debug-tool/MCP_Proc_Def.md)。

子工程目录结构和命名规则见 [`go-web-debug-tool/CLAUDE.md`](go-web-debug-tool/CLAUDE.md)。
