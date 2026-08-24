# LsmTokensServer Web 页面自动化测试和 debug 解决方案

> 版本：v1.0（2026-08-24）
> 生成方式：GoWebDebugTool 实际驱动 Headless Chrome 遍历点击 + 源码静态分析
> 对应上一轮：`LsmTokensServerWeb页面自动化测试和debug解决方案_20260824_07.md`（阶段J 已修复 P0-P4）

## 1. 测试环境

| 项 | 值 |
|----|----|
| 被测服务 | `./LsmTokensServer`（管理端 9101 / 用户端 29001，均已运行） |
| 驱动工具 | `go-web-debug-tool`（本地私有子模块，HTTP 监听 `127.0.0.1:28999`） |
| 浏览器 | Headless Chrome 147（`anti_detect=true`、`chrome_headless=true`，自动忽略自签名证书） |
| 遍历方式 | `NewChromePage` 打开 → `ControlChromePage(eval_js)` 走 hash 路由 → `LookChromePageInfo(console/dom)` 断言 |

## 2. 遍历结果总览

### 2.1 管理端（9101，HTTP，无需登录）

遍历 15 个页面（`Home` / `UserManage` / `DstEndPointManage` / `AIRouteManage` /
`ModelInfo` / `AgentInfo` / `ProtocolConvertAnalyzer` / `SpiderDataSource` /
`SpiderDailyInfo` / `CleanupReport` / `ChatAnalysis` / `ChatAnalysisTotal` /
`ChatAnalysisSession` / `ChatAnalysisTask` / `ChatDialog`），**全部渲染正常、Console 0 error**。

交互控件点击验证（非破坏性）：
- 顶部工具栏 7 个弹窗（用户日志 / Wiki / 证书 / Git / 系统信息 / README / 构建日志）全部可打开。
- `UserManage` 的「查看模型」展开正常，模型列表 + API Key 脱敏显示正常。
- `ProtocolConvertAnalyzer` 的「执行转换」空输入时正确返回空输出（无异常）。
- `DstEndPointManage` / `AIRouteManage` / `UserManage` 列表加载、下拉、批量选择、分页正常。

### 2.2 用户端（29001，HTTPS 自签名证书）

- 根路径自动 302 → `/UserLogin`，登录页渲染正常、验证码（base64 data URL）加载正常、Console 0 error。

## 3. 本轮发现问题与修复（阶段K）

### P0-1 管理端角色误判（导航在“管理端/用户端”间随机跳动）—— 已修复

- **现象**：同一管理端首页，多次刷新 / 切换页面后，顶部角色徽标在“管理端 / 用户端”之间**随机翻转**，
  侧边栏在 ADMIN_NAV（10 项）与 USER_NAV（14 项）之间漂移。
- **根因**：`ClientWeb/src/App.jsx` 中两个 `setUserInfo` 存在**竞态**：
  - `get('UserInfoInterface').then(d => setUserInfo({ ...d.data, loaded:true }))` 为**整体替换式** setState，不含 `isAdmin`；
  - `fetch('UserManageInterface',{POST}).then(r => setUserInfo(u => ({ ...u, isAdmin:... })))` 为函数式 setState。
  - 若 `UserManageInterface` 先返回，`UserInfoInterface` 后返回时会把 `isAdmin` 整体覆盖掉，反之则保留，导致结果不确定。
- **修复**：用 `Promise.all` 并发拉取“登录信息 + 角色探测”，合并后**只调用一次** `setUserInfo({ ...info, isAdmin })`，并在卸载时用 `alive` 标志防竞态。

### P0-2 运行时产物路径解析基准错误（日志/证书/README 全部指向 ServerGo/ 而非工程根目录）—— 已修复

- **现象**：
  1. `LsmTokensServer.log`、`LsmTokensServerUsersInfo.log` 被写入 `ServerGo/`（而非工程根目录）；
  2. 「构建日志」弹窗显示“暂无构建日志”；
  3. 「证书」弹窗显示“证书状态：不存在”；
  4. 「README」弹窗显示“读取 README.md 失败: …/ServerGo/README.md: no such file or directory”。
- **根因**：新工程前后端分离，可执行文件位于 `ServerGo/` 子目录，而配置、证书、README、日志等运行时产物统一位于工程根目录。但
  `config.ResolvePath` / `system.getProjectDir` 以 `os.Executable()` 所在目录（= `ServerGo/`）为基准解析相对路径，导致全部错位。
- **修复**：
  - `ServerGo/config/pathutil.go`：新增 `configDir` + `GetConfigDir()`，`ResolvePath` 改为**以配置文件所在目录为基准**解析相对路径（配置未加载前回退可执行文件目录）。
  - `ServerGo/config/config.go` `LoadConfig`：加载成功时记录 `configDir = filepath.Dir(绝对配置路径)`。
  - `ServerGo/api/server_api_common_info.go` `readmeInterfaceHandle`：改读 `config.GetConfigDir()/README.md`。
  - 连带修复：`loadConfigSafe` 对 `LogFileURL / BuildDateTimeLogURL / UserInfoLogURL` 的解析、`certDownloadInfoInterfaceHandle` 的证书解析，均自动回到工程根目录。

### P1-1 证书弹窗 “Web HTTPS” 字段名前后端不一致 —— 已修复

- **现象**：后端实际下发 `user_web_https_enabled`，前端却读 `info.user_web_enabled`，导致配置 `userWebUseHTTPS=true` 时仍显示“未启用”。
- **修复**：`ClientWeb/src/components/ToolbarDialogs.jsx` 改为读 `info.user_web_https_enabled`（与后端 `CertDownloadInfoResponse` JSON tag 对齐）。

### P2-1 协议转换分析器独立角色探测不可靠 —— 已修复

- **现象**：`ProtocolConvertAnalyzer.jsx` 用 `fetch('ProtocolConvertAnalyzerToggle').then(r => setIsAdmin(r.status !== 404))` 探测角色。
  用户端无该接口会回落到 SPA 首页（200），`status !== 404` 恒为 true，导致登录用户被误判为管理员，看到无效的“启用/禁用”开关。
- **修复**：管理端该接口为 POST-only，GET 返回 **405**；用户端回落到 SPA 首页返回 200。故改为 `setIsAdmin(r.status === 405)` 精确区分。

## 4. 自动化测试方案

### 4.1 基础可访问性（curl）

```bash
# 管理端（HTTP）
for p in "" UserManage DstEndPointManage AIRouteManage ModelInfo AgentInfo \
         ProtocolConvertAnalyzer SpiderDataSource SpiderDailyInfo CleanupReport \
         ChatAnalysis ChatAnalysisTotal ChatAnalysisSession ChatAnalysisTask ChatDialog; do
  code=$(curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:9101/$p")
  [ "$code" = "200" ] || echo "FAIL /$p -> $code"
done
```

### 4.2 浏览器遍历断言（GoWebDebugTool）

对每个页面：`eval_js` 设置 `location.hash` → 等待渲染 → 校验：
1. `LookChromePageInfo(console, level=error)` 返回条数为 0；
2. `document.body.innerText` 包含该页 `page-title`；
3. 关键控件（按钮 / 下拉 / 弹窗）存在且可点击。

顶部工具栏弹窗循环：按按钮文本定位 → `click` → 断言 `.modal-box` 出现且无 error → 关闭 `.modal-close`。

## 5. 修复优先级与验证

| 级别 | 问题 | 影响 | 修复 |
|------|------|------|------|
| P0 | 角色误判（导航随机漂移） | 管理端功能入口错乱 | App.jsx Promise.all 合并 setState |
| P0 | 运行时产物路径基准错误 | 日志/证书/README 全部失效 | config 以配置文件目录为基准 |
| P1 | 证书 Web HTTPS 字段不一致 | 证书信息显示错误 | 前端字段名对齐 |
| P2 | 分析器独立角色探测 | 用户端误显示管理开关 | 405 精确探测 |

验证流程：`go vet ./...` 0 告警 → `go test ./...` 全绿 → `npm run build` 成功 →
`./rebuild_restart_app.sh` 重启 → GoWebDebugTool 复测角色徽标稳定为“管理端”、证书/README/构建日志三弹窗恢复正常。

## 6. 长期建议

1. 统一角色判定：后端新增 `/UserRoleInterface`，前端单一数据源，消除多处重复“探测式”判断（App.jsx 与 ProtocolConvertAnalyzer 各有一套）。
2. 路径基准收敛：所有相对运行时路径统一经 `config.ResolvePath`（配置文件目录）解析，禁止再直接 `os.Executable()`。
3. E2E 固化：将第 4 节的 GoWebDebugTool 遍历脚本抽成独立测试程序，纳入 CI（无 Chrome 环境时跳过）。
4. 补回「源码」对话框：后端 `SourceCodeInterface` 已挂载但前端 ToolbarDialogs 未暴露，后续补上即可对齐旧版。
