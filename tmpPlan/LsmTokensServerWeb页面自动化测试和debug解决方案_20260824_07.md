# LsmTokensServer Web 页面自动化测试和 debug 解决方案

> 版本：v1.0（2026-08-24）
> 生成方式：通过 GoWebDebugTool 实际探测 + 源码静态分析

## 1. 已发现问题清单

### P0 - 用户端静态资源被 JWT 中间件拦截（严重：用户端完全不可用）

**现象**：用户端（29001）所有页面空白，React 不渲染。

**根因**：`userAuthMiddleware`（`ServerGo/api/server_api_user_login.go:518`）只放行 `/static/` 前缀，但 Vite 构建产物的静态资源在 `/assets/` 路径下。浏览器请求 `/assets/index-*.js` → JWT 校验失败 → 302 到 `/UserLogin` → `/UserLogin` 也是 SPA → 又请求 JS → 又 302 → 死循环。

**影响范围**：所有未登录用户无法看到登录页面。

**修复方案**：在 `userAuthMiddleware` 的放行列表中增加静态资源路径：
```go
// 放行 Vite 构建产物静态资源
if len(r.URL.Path) > 8 && (r.URL.Path[:8] == "/static/" || r.URL.Path[:8] == "/assets/") {
    next.ServeHTTP(w, r)
    return
}
// 放行根目录静态文件（favicon、图标等）
if strings.HasSuffix(r.URL.Path, ".svg") || strings.HasSuffix(r.URL.Path, ".png") ||
   strings.HasSuffix(r.URL.Path, ".ico") || strings.HasSuffix(r.URL.Path, ".js") ||
   strings.HasSuffix(r.URL.Path, ".css") {
    next.ServeHTTP(w, r)
    return
}
```

### P1 - 管理端 SPA 页面刷新后 hash 路由丢失

**现象**：管理端使用 hash 路由（`#/Home`、`#/UserManage`），直接访问 `http://localhost:9101/UserManage` 时返回 SPA `index.html`，但前端 hash 路由无法识别 `pathname` 中的路径。

**分析**：`App.jsx` 的 `currentRoute()` 只解析 `window.location.hash`，不解析 `pathname`。当浏览器直接访问 `/UserManage`（非 hash 路由）时，SPA 回落到 `index.html`，但 hash 为空，前端显示 Home 页而非 UserManage 页。

**修复方案**：在 `currentRoute()` 中增加 `pathname` 回退解析：
```javascript
function currentRoute() {
  let path = window.location.hash.replace(/^#\/?/, '')
  // 兼容直接通过 pathname 访问（如 /UserManage）
  if (!path) {
    path = window.location.pathname.replace(/^\//, '')
  }
  const [p, query] = path.split('?')
  return { path: PAGES[p] ? p : 'Home', query: new URLSearchParams(query || '') }
}
```

### P2 - 管理端首页"登录信息"卡片显示"加载中..."永不更新

**现象**：管理端首页（无登录态时）显示"加载中..."。

**分析**：`App.jsx` 中 `UserInfoInterface` 在管理端（9101）返回 SPA HTML（非 JSON），前端 `request()` 的 `res.json()` 解析失败，`catch` 分支未正确处理。且管理端本不需要登录，不应调用 `UserInfoInterface`。

**修复方案**：`App.jsx` 中管理端跳过 `UserInfoInterface` 调用，或管理端后端增加 `UserInfoInterface` 返回管理员信息。

### P3 - 管理端角色探测逻辑不可靠

**现象**：管理端通过 `fetch('UserManageInterface')` 返回是否 404 来判断角色，但管理端 `UserManageInterface` 是 POST 接口，GET 请求返回 200（SPA 回落），导致角色误判。

**分析**：`App.jsx` 第 50-52 行用 `fetch('UserManageInterface')`（GET 请求）探测，但管理端的 `UserManageInterface` 只注册了 POST handler，GET 请求落到 SPA 回落返回 `index.html`（200），导致所有用户都被识别为管理员。

**修复方案**：改为 POST 请求探测，或使用专用的角色接口：
```javascript
fetch('UserManageInterface', { method: 'POST', credentials: 'include', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({action:'list'}) })
  .then((r) => setUserInfo((u) => ({ ...u, isAdmin: r.status !== 404 && r.headers.get('content-type')?.includes('json') })))
  .catch(() => setUserInfo((u) => ({ ...u, isAdmin: false })))
```

### P4 - 用户端 `UserInfoInterface` 路径缺少前导 `/`

**现象**：`api.js` 中 `baseUrl()` 基于 `window.location.pathname` 计算，但 SPA 部署在根路径时 `baseUrl()` 返回 `/`，API 请求路径正确。但如果部署在子路径下（如 `/app/`），`baseUrl()` 返回 `/app/`，请求路径变成 `/app/UserInfoInterface`，而后端路由是 `/UserInfoInterface`。

**分析**：当前部署在根路径下没有问题，但如果未来部署在子路径下会出问题。

**修复方案**：`baseUrl()` 始终返回 `/` 或明确配置 baseURL。

## 2. 自动化测试方案

### 2.1 测试工具：GoWebDebugTool

已在本机运行（`http://127.0.0.1:28999`），提供 5 个 HTTP API：
- `POST /NewChromePage` — 创建页面
- `POST /ControlChromePage` — 页面操作（点击/输入/JS执行/截图等）
- `POST /LookChromePageInfo` — 页面信息（DOM/Console/Network/截图等）
- `POST /CloseChromePage` — 关闭页面
- `POST /ListChromePages` — 列出所有页面

### 2.2 测试覆盖矩阵

| 端口 | 页面 | 测试项 | 优先级 |
|------|------|--------|--------|
| 9101 | 管理端 Home | 页面渲染、登录信息、模型列表 | P0 |
| 9101 | 管理端 UserManage | 用户列表加载、CRUD 操作 | P1 |
| 9101 | 管理端 DstEndPointManage | 端点列表、连通性测试 | P1 |
| 9101 | 管理端 AIRouteManage | 路由列表、批量操作 | P1 |
| 9101 | 管理端 ModelInfo | 统计卡片、图表渲染 | P2 |
| 9101 | 管理端 AgentInfo | 统计卡片、图表渲染 | P2 |
| 9101 | 管理端 ProtocolConvertAnalyzer | 开关、测试、记录列表 | P1 |
| 9101 | 管理端 SpiderDataSource | 数据源列表、爬取 | P2 |
| 9101 | 管理端 SpiderDailyInfo | 日报列表、筛选 | P2 |
| 9101 | 管理端 CleanupReport | 报告列表、KPI | P2 |
| 9101 | 管理端 ChatAnalysis | 明细查询、分页、详情 | P1 |
| 9101 | 管理端 ChatAnalysisTotal | 汇总统计、WS 流式 | P1 |
| 9101 | 管理端 ChatAnalysisSession | 会话列表 | P2 |
| 9101 | 管理端 ChatAnalysisTask | 任务列表 | P2 |
| 9101 | 管理端 ChatDialog | 对话配置 | P2 |
| 29001 | 用户端 Login | 登录表单、验证码 | P0 |
| 29001 | 用户端 Home | 登录信息、模型列表 | P0 |
| 29001 | 用户端 ChatAnalysis | 分析查询 | P1 |
| 29001 | 用户端其他页面 | 同管理端对应页面 | P1 |

### 2.3 测试脚本设计

```bash
# 基础页面可访问性测试（curl）
for page in "" "UserManage" "DstEndPointManage" ...; do
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:9101/$page")
  [ "$code" != "200" ] && echo "FAIL: /$page → $code"
done

# 浏览器渲染测试（GoWebDebugTool）
# 1. 打开页面 → 截图 → 检查 DOM 非空 → 检查 Console 无错误
# 2. 点击导航按钮 → 验证路由切换 → 验证页面内容
# 3. 点击功能按钮 → 验证 API 调用 → 验证结果渲染
```

### 2.4 关键测试场景

**场景 1：管理端首页加载**
```
1. NewChromePage("http://127.0.0.1:9101/")
2. 等待 2 秒（React 渲染）
3. LookChromePageInfo(dom) → 验证 #root 有子元素
4. LookChromePageInfo(console) → 无 error 级日志
5. screenshot → 保存供人工审查
```

**场景 2：管理端导航遍历**
```
1. 打开管理端首页
2. 依次点击侧边栏每个导航项
3. 每次点击后：等待 1 秒 → 检查 DOM 更新 → 检查 Console 无错误
4. 截图记录每个页面
```

**场景 3：用户端登录流程**
```
1. NewChromePage("https://127.0.0.1:29001/")
2. 验证跳转到 /UserLogin
3. 等待 React 渲染 → 检查登录表单存在
4. 获取验证码（CaptchaGenerate）
5. 输入凭据 + 验证码 → 点击登录
6. 验证跳转到首页
```

**场景 4：API 接口可用性**
```
对所有 Interface 接口发送 POST 请求，验证返回 JSON 格式正确
```

## 3. 修复优先级

| 优先级 | 问题 | 影响 | 预计工作量 |
|--------|------|------|-----------|
| P0 | 用户端静态资源 302 | 用户端完全不可用 | 10 分钟 |
| P1 | 管理端角色探测误判 | 管理/用户导航混乱 | 10 分钟 |
| P1 | 管理端首页加载信息卡住 | 首页信息不完整 | 10 分钟 |
| P2 | hash 路由 pathname 兼容 | 直接 URL 访问不正确 | 15 分钟 |
| P2 | baseUrl 子路径兼容 | 未来部署问题 | 5 分钟 |

## 4. 验证流程

1. 修复代码 → `./rebuild_restart_app.sh --build-only` 编译验证
2. `go test ./...` 单元测试全绿
3. `./rebuild_restart_app.sh` 重启服务
4. GoWebDebugTool 自动化遍历所有页面
5. 每页面截图 + Console 检查 + DOM 验证
6. 人工确认 → git 中文提交

## 5. 长期建议

1. **增加 E2E 测试**：基于 GoWebDebugTool 的 HTTP API，编写自动化测试脚本，纳入 CI
2. **增加路由守卫**：前端路由增加登录态检查，未登录自动跳转
3. **增加错误边界**：React ErrorBoundary 捕获渲染错误，避免白屏
4. **统一角色判断**：后端增加 `/UserRoleInterface`，前端通过 API 获取角色而非探测
5. **增加健康检查面板**：在管理端增加服务状态监控页（端口、DB、Chrome 状态）
