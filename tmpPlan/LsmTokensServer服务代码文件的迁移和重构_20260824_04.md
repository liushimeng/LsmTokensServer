# LsmTokensServer 服务代码文件的迁移和重构 — 全面排查与优化方案（2026-08-24 第 04 轮）

> 排查方法：延续 03 轮基线（后端全部 Go handler 已 1:1 迁移、`go build`/`go test ./...` 全绿），
> 本轮聚焦 03 轮遗留的唯一高严重度缺口 —— **阶段 F：ClientWeb SPA 前端全量实现**。
> 信息源：旧工程 57 个 `server_web_*.go` 文件（约 2.5 万行内嵌 HTML/CSS/JS）逐一提取页面结构、
> 导航、API 调用契约（请求参数/动作/响应字段）、登录加密存储逻辑；
> 对照新工程 `ServerGo/api/routes.go`（43+ 路由）与 `ServerGo/webserver/webserver.go`（SPA 托管 + fallback）。

## 0. 本轮结论总览

| # | 缺口 | 严重度 | 本轮状态 |
|---|---|---|---|
| 1 | ClientWeb 仍为占位页（App.jsx 单页），登录/全部业务页面未实现 | 高 | ✅ 本轮实现 |
| 2 | 登录页（模型名+APIKey+验证码、记住凭据 XOR+Base64 本地存储）未迁移 | 高 | ✅ 本轮实现 |
| 3 | 管理端 16 个页面 + 用户端 14 个页面未实现 | 高 | ✅ 本轮实现 |
| 4 | 通用工具栏弹窗（用户日志/Wiki/证书/Git/系统信息/README/源码）未实现 | 中 | ✅ 本轮实现 |
| 5 | 协议转换分析器页面（1070 行模板 + 12 条 API 交互）未实现 | 中 | ✅ 本轮实现 |
| 6 | 存量回归测试（websocket/chat_total/stats gobucket 等）未迁移 | 低 | ⏳ 下轮（阶段 G） |

## 1. 页面清单与 SPA 架构

### 1.1 旧导航提取结果

- **管理端**（49101）：Home、UserManage、DstEndPointManage、AIRouteManage、ModelInfo、AgentInfo、
  ProtocolConvertAnalyzer、SpiderDataSource、SpiderDailyInfo、CleanupReport
  （+ 从用户管理页跳转 ChatAnalysis/ChatAnalysisTotal/ChatAnalysisSession/ChatAnalysisTask/ChatDialog，带 user_name/model_name 查询参数）。
- **用户端**（42901）：Home、ChatAnalysis、ChatAnalysisTotal、ChatAnalysisSession、ChatAnalysisTask、ChatDialog、
  AIRouteManage、ModelInfo、AgentInfo、ProtocolConvertAnalyzer、DstEndPointManage、SpiderDataSource、SpiderDailyInfo、CleanupReport。
- **登录**：`/UserLogin` 表单 → `POST /UserLoginInterface`（model_name、api_key、captcha_id、captcha_code、remember），
  验证码 `GET /CaptchaGenerate`；记住凭据使用 XOR+Base64 + hostname/UA salt 存储 localStorage（key `lsm_agent_creds`）。

### 1.2 新 SPA 架构（ClientWeb）

```
ClientWeb/src/
  main.jsx / App.jsx            # hash 路由（#/Login、#/UserManage…），无第三方路由依赖
  shared/api.js                 # fetch 封装（credentials include、JSON、错误处理、SSE/WS helper）
  shared/auth.js                # 登录态检测、凭据本地加密存储（与旧版算法一致）
  components/
    Layout.jsx                  # 页头 + 导航（admin/user 两套）+ 通用工具栏
    DataTable.jsx / Modal.jsx   # 通用表格/弹窗/表单组件
    dialogs/                    # UserLog / Wiki / Cert / Git / SysInfo / Readme / SourceCode 弹窗
  pages/
    Login.jsx Home.jsx
    UserManage.jsx DstEndPointManage.jsx AIRouteManage.jsx ModelInfo.jsx AgentInfo.jsx
    ProtocolConvertAnalyzer.jsx SpiderDataSource.jsx SpiderDailyInfo.jsx CleanupReport.jsx
    ChatAnalysis.jsx ChatAnalysisTotal.jsx ChatAnalysisSession.jsx ChatAnalysisTask.jsx ChatDialog.jsx
```

- 角色判定：同一 SPA 产物同时托管在 49101（管理）与 42901（用户）端口，登录后按 `/UserInfoInterface`
  返回的角色渲染对应导航与页面（后端在两个端口分别挂管理/用户 handler，同名 API 天然分流）。
- API 契约：所有页面严格按旧 `server_web_*_page*.go` 中的 JS `fetch` 参数/动作实现，路径 1:1。
- 特殊链路：ChatAnalysisTotal 使用 WebSocket（`/ChatAnalysisTotalWS`）流式刷新；
  SpiderDataSource 爬取使用 `EventSource`（`/SpiderDataSourceCrawl` SSE）；
  证书下载使用原始文件名下载头；Wiki/README 返回原文由前端渲染（补充轻量 Markdown 渲染）。

## 2. 实施步骤

1. 编写本方案文档（本文件）。
2. 实现 SPA 基础设施（api.js/auth.js/路由/Layout/DataTable/Modal/弹窗/登录页）。
3. 并行实现四组页面（CRUD 组 / Chat 组 / 分析器+爬虫组 / Home+剩余），
   每组对照旧 Go 模板逐文件核对字段与动作。
4. `npm run build` + `oxlint` 通过；`go build ./...`、`go test ./...` 保持全绿。
5. `./rebuild_restart_app.sh` 完整重启，浏览器/接口冒烟验证（登录、导航、各页面数据加载、SSE/WS）。
6. 中文 commit 提交。

## 3. 有意废弃项（延续前几轮）
- 旧服务端内嵌 HTML 生成代码（已废弃，SPA 替代）。
- 旧服务端 `markdownToHTML`：前端渲染 Markdown。
- 旧分析器服务端四段拼装：前端分四次调 Test 接口。

## 4. 剩余待办（下轮）
- 阶段 G：迁移存量回归测试（websocket v2055/v2060、chat_total、stats gobucket 系列）。
- 统一 `config.DEFAULT_OPENCLAW_SYSTEM_PROMPT` 与 `system.DefaultOpenClawSystemPrompt` 两处默认值。
- 核对 v2.0.56 task 特征异步回填 goroutine 接线。

## 5. 验证方式与结果

1. `npx vite build` 通过（39 modules，JS 327KB/gzip 92KB）；`npx oxlint` 仅剩与既有页面同类的
   `react/set-state-in-effect` 风格 warning（数据加载 effect 同步 setState，React 19 兼容写法），无错误。
2. `go build ./...` 通过；`go vet` 5 处告警为旧工程既有问题平移；`go test ./...` 全绿（未改动后端）。
3. `./rebuild_restart_app.sh` 完整重启后：
   - 管理端 9101：SPA 首页 200（zh-CN、新构建产物），`/UserManageInterface` 管理路由可达，
     SPA fallback（/UserLogin → 200 index.html）正常；
   - 用户端 29001（HTTPS）：`/` 未登录 302 → /UserLogin（SPA 兜底渲染登录页），
     `/CaptchaGenerate` 返回 captcha_id + base64 图片；
   - 旧服务 LsmHttpAgent（PID 2912429）仍在运行，未受影响。
4. 页面契约均由子代理逐文件对照旧 `server_web_*_page*.go` 的 JS 提取（action 值、分页白名单、
   筛选字段 1:1），并交叉核对新工程 `ServerGo/api/`、`ServerGo/websocket/` 实际 handler。

### 实现说明（与旧版差异）
- ChatDialog 按新工程实际契约实现（models/config 两步接口），与旧版一致。
- AIRouteManage 批量"追加/删除源站"的旧前端逐条 update 循环未复刻（保留算法批量 batch_update 与 batch_delete）。
- ChatAnalysisTotal 优先 WS 流式（7 stage 渐进渲染 + cancel/过期帧丢弃），WS 失败自动回退 HTTP full_http。
- 图表以纯 CSS 柱状/进度条替代（坚持无第三方依赖）。

## 6. 提交
中文 commit：`阶段F：ClientWeb SPA 前端全量实现（登录/管理端/用户端/分析器/爬虫SSE/Chat流式）（迁移排查04轮）`
