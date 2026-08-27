## 自动化 Web 测试任务：LsmTokensServer 管理员端 + 用户端全功能专项（go-web-debug-tool 版）

> 本项目按 [MIT License](LICENSE) 开源。测试任务面向维护者，参与前请阅读 [CLAUDE.md](CLAUDE.md) 与 [AutoDebugTestReport.md](AutoDebugTestReport.md)。
> 本文件为 LsmTokensServer 统一测试入口，**产出** `TestReport/自动化测试报告_YYYYMMDD_HHMMSS.md` 前缀的报告。
> **测试工具**：所有浏览器交互必须通过 `go-web-debug-tool`（默认端口 28999）完成，严格模拟人类操作行为。

- **工作目录**: `/usr/local/LsmTokensServer/LsmTokensServer`
- **入口脚本**: `AutoTestAndSaveReport.sh`(随机选编程 Agent CLI → 跑测试 → 报告落盘 → shell 层确定性接力 `AutoDebugTestReport.sh` 自动修复)
- **测试工具**: `go-web-debug-tool`（CDP 控制 Chrome，端口 28999）
  - 工具文档: `go-web-debug-tool/MCP_Proc_Def.md`
  - 新建页面: `POST /NewChromePage`
  - 控制页面: `POST /ControlChromePage`
  - 查看信息: `POST /LookChromePageInfo`
  - 关闭页面: `POST /CloseChromePage`
  - 列出页面: `POST /ListChromePages`
- **产物路径**:
  - 测试报告: `TestReport/自动化测试报告_YYYYMMDD_HHMMSS.md`
  - 协议抓包分析报告: `TestReport/协议抓包分析报告_YYYYMMDD_HHMMSS.md`
  - **自动化测试进度文件(跨轮去重基线,强制生成+读取)**: `AutoTestProgress/自动化测试进度_YYYYMMDD_HHMMSS.md`
  - 子工程工具报告: `go-web-debug-tool/UseReport/测试工具使用报告_YYYYMMDD_HHMMSS.md`
  - 文件名时间戳统一 `YYYYMMDD_HHMMSS`,精度到秒
- **测试产物处理**: `TestReport/*` 与 `AutoTestProgress/*` 由脚本层 git add + commit + 接力 debug；**测试 Agent 禁止**自行执行 `git add` / `git commit` / `git push`(避免与 shell 接力双重提交)。**测试 Agent 严禁删除或归档报告文件**(删除/归档由接力脚本在代码修复提交后统一执行,Agent 提前删除将导致接力链断裂)。
- **执行模式**: 主 Agent 跑核心流程;遇代码读取/协议抓包/数据查询可按需委派 SubAgent,不阻塞主流程。
- **执行策略**: 通过 `go-web-debug-tool` **严格模拟真实人类操作**——拟人点击、逐字输入、滚轮滚动、等待页面加载；串行单浏览器会话;以最近 10 次非缺陷修复提交划定测试范围,新增/优化项优先。
- **核心目标**: 验证管理员 Web(9101) 与用户 Web(29001) 全部页面的可用性、所有 REST API 的功能正确性、双构建隔离合规性、安全红线(鉴权/脱敏/密码哈希)。
- **代码变更限制**: 业务代码只读;若发现缺陷,**只出报告与建议**,严禁自动改业务代码。**严禁删除或归档 `TestReport/`、`AutoTestProgress/` 下的任何文件**(接力脚本依赖文件存在性检测,Agent 提前删除将导致接力链断裂)。

### 1. go-web-debug-tool 拟人化测试规范（强制）

> **核心原则**：所有浏览器交互必须通过 `go-web-debug-tool` 完成，**严格模拟真实人类操作行为**，禁止使用非拟人化的快速操作（如直接 JS 注入点击、瞬间填充表单等），确保测试结果符合人类使用预期。

#### 1.1 拟人化操作映射表

| 人类行为 | go-web-debug-tool action | 说明 |
|----------|--------------------------|------|
| 点击按钮/链接 | `human_click` | 贝塞尔曲线移动鼠标 + 随机延迟 + 抖动，模拟真实点击 |
| 输入文本 | `human_input` | 逐字符输入，每字符 50-200ms 随机延迟，每 5-10 字符额外停顿 200-500ms |
| 滚动页面 | `scroll` + `use_wheel=true` | 触发真实 WheelEvent，模拟滚轮滚动 |
| 滚动到元素可见 | `scroll_to` | 等价 `el.scrollIntoView`，带平滑滚动 |
| 悬停/展开下拉菜单 | `hover` → 等待菜单展开 → `human_click` 选择项 | 先悬停触发下拉，再点击选项 |
| 选择下拉选项 | `select_option` | 选中 `<select>` 里的 option |
| 按键操作 | `key_press` / `press_sequence` | 支持 Enter、ArrowDown、Tab 等 |
| 等待页面加载 | `LookChromePageInfo` `info=page_meta` 检查 `ready_state=complete` | 每步操作后必须等待页面就绪 |
| 截图验证 | `LookChromePageInfo` `info=screenshot` | 关键步骤截图留证 |
| 查看网络请求 | `LookChromePageInfo` `info=network` | 验证 API 调用与响应 |
| 查看控制台 | `LookChromePageInfo` `info=console` | 检查错误日志 |

#### 1.2 拟人化操作流程模板

每个页面测试必须遵循以下流程：

```
1. 打开页面：POST /NewChromePage { "url": "...", "wait_until": "networkidle" }
2. 等待加载：POST /LookChromePageInfo { "page_id": "...", "info": "page_meta" } 直到 ready_state=complete
3. 截图留证：POST /LookChromePageInfo { "page_id": "...", "info": "screenshot" }
4. 执行操作（按需选择）：
   a. 点击：POST /ControlChromePage { "action": "human_click", "params": { "selector": "..." } }
   b. 输入：POST /ControlChromePage { "action": "human_input", "params": { "selector": "...", "text": "..." } }
   c. 滚动：POST /ControlChromePage { "action": "scroll", "params": { "use_wheel": true, "delta_y": 300 } }
   d. 下拉：POST /ControlChromePage { "action": "hover", "params": { "selector": "..." } } → 等待 → human_click 选项
5. 操作后等待：再次检查 page_meta 确认页面稳定
6. 验证结果：截图 + network + console 三维验证
7. 记录结果：写入测试报告
```

#### 1.3 操作节奏控制（强制）

- **每步操作后必须等待**：点击/输入后至少等待 500-1000ms，SPA 页面需等待 `ready_state=complete`
- **页面跳转后必须等待**：导航到新页面后等待 `networkidle` 或至少 2000ms
- **下拉菜单展开后必须等待**：hover 触发下拉后等待 300-500ms 再 click 选项
- **表单提交后必须等待**：提交后等待响应完成，检查 network 确认 API 调用成功
- **禁止连续快速操作**：两次操作之间至少间隔 300ms

### 2. 测试范围

#### 2.1 管理员 Web(managerWebListenPort=9101, HTTP)

| 分组 | 页面 | 主 API |
|------|------|--------|
| 首页 | Home | `/UserInfoInterface`(manager)、`/UserModelListInterface` |
| 用户/路由管理 | UserManage | `/UserManageInterface`、`/UserModelManageInterface` |
| | DstEndPointManage | `/DstEndPointManageInterface` |
| | AIRouteManage | `/AIRouteManageInterface` |
| 模型/代理 | ModelInfo | `/ModelInfoInterface`、`/ModelInfoManageInterface` |
| | AgentInfo | `/AgentInfoInterface` |
| | ProtocolConvertAnalyzer | `/ProtocolConvertAnalyzer*`(7 条) |
| 对话分析 | ChatAnalysis | `/ChatAnalysisInterface`、`/ChatAnalysisDstModelsInterface`、`/ChatAnalysisAgentToolsInterface`、`/ChatAnalysisDetailInterface`、`/ChatAnalysisBatchDeleteInterface` |
| | ChatAnalysisTotal | `/ChatAnalysisTotalInterface`、`/ChatAnalysisTotalRangeInterface`、`/ChatAnalysisTotalWS` |
| | ChatAnalysisSession | `/ChatAnalysisSessionInterface` |
| | ChatAnalysisTask | `/ChatAnalysisTaskInterface` |
| | ChatDialog | `/ChatDialogInterface` |
| 爬虫 | SpiderDataSource | `/SpiderDataSourceInterface`、`/SpiderDataSourceCrawl` |
| | SpiderDailyInfo | `/SpiderDailyInfoInterface` |
| | CleanupReport | `/CleanupReportInterface` |
| 系统 | GitInfo / SystemInfo / Wiki / UserInfoLog / BuildLog | `/GitInfoInterface`、`/SystemInfoInterface`、`/WikiInterface`、`/UserInfoLogInterface`、`/BuildLogInterface`、`/TimeSpanConfigInterface`、`/CertDownloadInfoInterface`、`/CertDownloadInterface` |

#### 2.2 用户 Web(userWebListenPort=29001, HTTPS)

| 分组 | 页面 | 主 API |
|------|------|--------|
| 登录 | Login | `/CaptchaGenerate`、`/UserLoginInterface`(login_type=model) |
| 首页 | Home | `/UserInfoInterface`(user)、`/UserModelListInterface`、`/UserLogoutInterface` |
| 对话分析 | ChatAnalysis | `/ChatAnalysisInterface`、`/ChatAnalysisDstModelsInterface`、`/ChatAnalysisAgentToolsInterface`、`/ChatAnalysisDetailInterface` |
| | ChatAnalysisTotal | `/ChatAnalysisTotalInterface`、`/ChatAnalysisTotalRangeInterface`、`/ChatAnalysisTotalWS` |
| | ChatAnalysisSession | `/ChatAnalysisSessionInterface` |
| | ChatAnalysisTask | `/ChatAnalysisTaskInterface` |
| | ChatDialog | `/ChatDialogInterface` |
| 路由/模型 | AIRouteManage | `/UserAIRouteInterface` |
| | ModelInfo | `/ModelInfoInterface` |
| | AgentInfo | `/AgentInfoInterface` |
| | ProtocolConvertAnalyzer | `/ProtocolConvertAnalyzer*`(5 条,无 Toggle/Users) |
| | DstEndPointManage | `/DstEndPointManageInterface` |
| 爬虫 | SpiderDataSource | `/SpiderDataSourceInterface`、`/SpiderDataSourceCrawl` |
| | SpiderDailyInfo | `/SpiderDailyInfoInterface` |
| | CleanupReport | `/CleanupReportInterface` |
| 系统 | 同上 | `/GitInfoInterface`、`/SystemInfoInterface`、`/WikiInterface`、`/UserInfoLogInterface`、`/BuildLogInterface`、`/TimeSpanConfigInterface`、`/CertDownload*` |

#### 2.3 双构建隔离合规性(强制)

- 用户端(`__APP_ROLE__='user'`)产物 `ClientWeb/dist-user/` 经 Rollup 死代码消除后**不得包含**任何管理端字样:
  - `UserManage`、`ManagerLogin`、`UserManageInterface`、`ManagerLoginInterface`、`ManagerLogoutInterface`、`ChatAnalysisBatchDeleteInterface`、`ProtocolConvertAnalyzerToggle`、`ProtocolConvertAnalyzerUsers`。
- 构建后 grep 复验:`grep -rE "UserManage|ManagerLogin|UserManageInterface|ManagerLoginInterface" ClientWeb/dist-user/` 必须为空。
- 角色由构建期常量 `__APP_ROLE__` 决定,禁止运行时嗅探端口/localStorage。

### 3. 登录流程（拟人化）

#### 3.1 管理员登录(ManagerLogin, 9101)

**拟人化登录流程**（通过 go-web-debug-tool）：

1. **打开登录页**：`POST /NewChromePage { "url": "http://localhost:9101/ManagerLogin", "wait_until": "networkidle" }`
2. **等待页面加载**：`LookChromePageInfo info=page_meta` 确认 `ready_state=complete`
3. **截图确认**：`LookChromePageInfo info=screenshot` 确认登录表单可见
4. **输入用户名**：`ControlChromePage { "action": "human_input", "params": { "selector": "input[name=user_name]", "text": "<managerUserName>" } }`
5. **输入密码**：`ControlChromePage { "action": "human_input", "params": { "selector": "input[name=password]", "text": "<managerPassword>" } }`
6. **处理验证码**（如有）：
   - 读取 `LsmTokensServer.conf` 的 `security.managerUserName` / `managerPassword`(该文件已加入 `.gitignore`,仅本地存在)
   - 若 `managerWebAuthDisabled=true` 可跳过验证码
   - 否则需通过验证码输入框 `human_input` 输入（测试环境可读取 captcha_id 对应的验证码值）
7. **点击登录按钮**：`ControlChromePage { "action": "human_click", "params": { "selector": "button[type=submit]" } }`
8. **等待跳转**：等待 `page_meta` 确认跳转到管理端首页
9. **验证登录成功**：截图 + 检查 Cookie `manager_token` 已设置

#### 3.2 用户登录(Login, 29001)

**拟人化登录流程**（通过 go-web-debug-tool）：

1. **读取凭证**：读取 `user_model_info.json`(与本文件同目录)获取 **模型名** 与 **API Key**:
   ```json
   { "模型名": "xxx", "API Key": "yyy" }
   ```
   **该文件已加入 `.gitignore`,含敏感信息,禁止提交、禁止明文写入报告**。
2. **打开登录页**：`POST /NewChromePage { "url": "https://localhost:29001/Login", "wait_until": "networkidle" }`
3. **等待页面加载**：确认 `ready_state=complete`
4. **截图确认**：确认登录表单可见
5. **选择登录方式**（如需要）：`human_click` 选择 "模型登录" 选项
6. **输入模型名**：`human_input` 到模型名输入框
7. **输入 API Key**：`human_input` 到 API Key 输入框
8. **处理验证码**（如有）
9. **点击登录按钮**：`human_click` 提交按钮
10. **等待跳转**：确认跳转到用户端首页
11. **验证登录成功**：截图 + 检查 Cookie `user_token` 已设置

> **重要**:`user_model_info.json` 不存在或格式错误时,测试 Agent **必须**在报告中标注「用户端登录跳过:凭证文件缺失/格式错误」,不得伪造/硬编码凭证。

### 4. 终止条件（阻塞即停策略）

> **核心策略**：本测试是 loop 循环架构的一个环节，**主要关注测试本身**。不要求一次发现所有问题，遇到主流程卡住、阻塞等问题时，**立即停止测试并生成问题报告**，由接力脚本修复后下一轮继续。

1. **主流程卡住/阻塞**（页面无响应、操作超时、元素不可交互）→ **立即停止当前测试**，生成问题报告，标注阻塞位置与已采集的诊断数据。
2. **页面崩溃**（`page_meta` 返回 `ready_state=crashed` 或 CDP 断开）→ **立即停止**，记录崩溃前的操作与截图。
3. **安全红线被突破**(接口未鉴权、明文密码泄漏、用户端包含管理代码) → **立即停止**,标记为严重缺陷,生成报告。
4. **核心 API 连续 3 次调用失败**(鉴权失败/协议不兼容/服务端持续故障)→ **立即停止**,记录失败详情。
5. **双构建隔离违规**(用户端产物含管理字样) → **立即停止**,标记为严重缺陷。
6. **规则自相矛盾** → 终止当前用例,记录后判断是否继续。
7. 无论正常结束或异常终止,均须按规范出报告;中断的进度必须标「中断未完成」。

### 5. 启动前置

1. 读最近 10 次**非缺陷修复类**提交(`git log --oneline -10 -- ':!*fix' ':!*BUG'` 等过滤),梳理新增/优化项作为本轮重点。
2. 确认服务已启动:`curl -s -o /dev/null -w "%{http_code}" http://localhost:9101/UserInfoInterface` 与 `curl -sk -o /dev/null -w "%{http_code}" https://localhost:29001/UserInfoInterface` 任一可达即开始。
3. 读 `LsmTokensServer.conf` 获取管理员凭证(如有)、读 `user_model_info.json` 获取用户端模型凭证。
4. 确认前端双构建产物存在:`ClientWeb/dist-manager/` 与 `ClientWeb/dist-user/` 目录存在。
5. **确认 `go-web-debug-tool` 已启动**（默认端口 28999）：`curl -s http://localhost:28999/ListChromePages` 可达。
6. 读取 `AutoTestProgress/` 目录下最新的进度文件,解析已覆盖页面/接口清单,本轮跳过已覆盖项。

### 6. 测试执行流程（go-web-debug-tool 驱动）

#### 6.1 去重预检(强制)

- 读取最新进度文件的 `已覆盖页面/接口清单`,提取状态为 `通过` 的条目。
- 本轮仅测试:新增/优化项 + 上次 `失败` 或 `跳过` 的项 + 安全红线强制项。
- 若进度文件解析失败,打印警告后全量测试。

#### 6.2 双构建隔离复验(强制,首轮)

- `grep -rE "UserManage|ManagerLogin|UserManageInterface|ManagerLoginInterface" ClientWeb/dist-user/` 必须为空。
- 违规 → 整轮终止,标记严重缺陷,写入报告。

#### 6.3 管理员端测试(9101)（拟人化流程）

按 §2.1 页面顺序串行执行,每页面遵循 §1.2 拟人化操作流程：

1. **打开页面**：`POST /NewChromePage { "url": "http://localhost:9101/<页面路径>", "wait_until": "networkidle" }`
2. **等待加载**：`LookChromePageInfo info=page_meta` 确认 `ready_state=complete`
3. **截图留证**：`LookChromePageInfo info=screenshot`
4. **执行核心操作**（按需）：
   - 点击按钮：`human_click`
   - 填写表单：`human_input`
   - 下拉选择：`hover` 展开 → `human_click` 选项
   - 滚动浏览：`scroll use_wheel=true`
   - 切换 Tab/菜单：`human_click`
5. **验证交互响应**：
   - `LookChromePageInfo info=network` 抓取 API 调用,验证请求/响应状态码
   - `LookChromePageInfo info=console` 检查 error 级别日志
   - `LookChromePageInfo info=screenshot` 确认 UI 变化
6. **记录结果**：通过/缺陷/跳过，附带截图证据

#### 6.4 用户端测试(29001)（拟人化流程）

同 §6.3,按 §2.2 页面顺序执行，所有操作通过 go-web-debug-tool 完成。

#### 6.5 安全红线验证(强制)

- 鉴权:未携带 Cookie 访问业务接口应返回 401。
- 脱敏:API 响应中手机号应被 `MaskPhone` 处理,密码字段置空。
- 密码哈希:数据库中密码为 bcrypt 哈希,无明文存储。

#### 6.6 协议抓包分析(按需)

- 通过 `LookChromePageInfo info=network` 抓取完整 API 调用链。
- 分析请求/响应格式、状态码、错误信息。
- 输出独立的协议抓包分析报告。

### 7. 缺陷分级

| 等级 | 定义 | 示例 |
|------|------|------|
| P0-严重 | 安全红线突破 / 服务不可用 / 数据泄漏 / **主流程阻塞** | 接口未鉴权、明文密码泄漏、页面完全无响应 |
| P1-主要 | 核心功能不可用 / 页面崩溃 / **操作阻塞** | 登录失败、页面白屏、按钮点击无响应 |
| P2-次要 | 功能部分异常 / 体验问题 | 下拉菜单展开异常、滚动失效 |
| P3-轻微 | 显示问题 / 非核心功能异常 | 样式错乱、文案错误 |

### 8. 阻塞问题报告模板（立即生成）

当测试因阻塞停止时，**必须立即生成报告**，使用以下模板：

```markdown
# 自动化测试报告（阻塞中断）

## 阻塞信息
- **阻塞位置**：[端点/页面/操作步骤]
- **阻塞类型**：[页面无响应 / 操作超时 / 元素不可交互 / API 连续失败 / 页面崩溃]
- **阻塞时间**：YYYY-MM-DD HH:MM:SS

## 阻塞前操作序列
| 步骤 | 操作 | 结果 | 证据 |
|------|------|------|------|
| 1 | ... | 成功/失败 | 截图/日志 |

## 已采集的诊断数据
### 最后截图
[截图 Base64 或引用]

### 网络请求
[关键 API 调用与响应]

### 控制台错误
[Console error 日志]

## 已覆盖页面/接口清单
| # | 端 | 页面/接口 | 状态 | 结果摘要 |
|---|-----|----------|------|---------|

## 结论
测试在 [位置] 阻塞，已完成 [X/Y] 项测试，建议修复后下一轮继续。
```

### 9. 证据与记录规范

1. 每个缺陷必须附带:截图(screenshot)、网络请求(network)、控制台日志(console)至少一项。
2. 所有证据基于 go-web-debug-tool 的 `LookChromePageInfo` 输出,杜绝幻觉与臆断。
3. 截图必须包含关键操作步骤的前后对比。
4. 网络请求需记录:URL、Method、Status Code、响应体摘要(脱敏)。

### 10. 报告与后处理

1. 测试结束首先生成测试报告,完整保存。报告必须包含:
   - `测试范围与变更基线`
   - `管理员 Web(9101) 页面遍历结果`(逐页面:通过/缺陷/跳过)
   - `用户 Web(29001) 页面遍历结果`(逐页面:通过/缺陷/跳过)
   - `安全红线验证结果`
   - `双构建隔离合规性(grep 复验)`
   - `缺陷列表`(BugID / 严重等级 / 复现步骤 / 期望 vs 实际 / 证据)
   - `诊断数据与证据链`(API 响应/日志/抓包)
   - `优化建议`(定位到根因:提示词/工具定义/业务逻辑/前端/协议适配/安全)
   - **阻塞信息**(如有):阻塞位置、类型、阻塞前操作序列、诊断数据
2. **强制生成进度文件**(跨轮去重基线,**非可选**;正常结束、异常终止、整轮中断均必须落盘):
   - 路径: `AutoTestProgress/自动化测试进度_YYYYMMDD_HHMMSS.md`(与测试报告时间戳一致,便于按时间排序取最新)。
   - 目录不存在则自动 `mkdir -p`。**测试 Agent 不允许**自行执行 `git add` / `git commit`,该文件由 shell 接力脚本统一提交(见本文件顶部「测试产物处理」一节,纳入 `AutoTestProgress/*` 路径范围;若接力脚本暂未覆盖该路径,本轮进度文件至少应**保留在工作区**让下轮 Agent 能读到,以"git status 含未跟踪文件"形式提示)。
   - 必备字段(全部使用 Markdown 标题 + 表格,**机器可读**,下轮启动时 Agent 必须能据此解析):
     - `本轮基线(Git HEAD)`: `git rev-parse HEAD` 的完整短哈希 + 对应 commit message 第一行,作为下轮去重失效判定基准。
     - `本轮测试时间`: `YYYY-MM-DD HH:MM CST`,便于人工对照 `TestReport/`。
     - `本轮测试重点`: 与 `git log -10` 变更项对齐,描述本轮覆盖的新增/优化功能点。
     - `已覆盖页面/接口清单`: 表格,列 = `# | 端 | 页面/接口 | 分组 | 状态 | 本轮结果摘要 | 跳过上轮的进度文件(若有)`。`状态` 取值:`通过` / `失败` / `跳过(凭证缺失)` / `跳过(降级路径)` / `跳过(主流程阻塞)`。`本轮结果摘要` 用一句话说清结论(通过则写关键证据 / 失败则写原因 / 跳过则写原因)。**该表格是 §6.1 去重预检的输入,必须完整无缺**。
     - `缺陷清单与状态`: 表格,列 = `BugID | 严重等级 | 描述 | 引入版本(commit) | 状态`。`状态` 取值:`待修复` / `修复中` / `已修复验证` / `挂起`。只有 `已修复验证` 的 Bug 在下轮可跳过(且需 `已修复验证` 的 commit HEAD 与本轮 HEAD 一致)。
     - `下一轮建议重点`: 给下轮 Agent 一段话,描述本轮跳过的项、未覆盖的变更项、需要持续监控的指标。
   - 中断时(整轮终止):进度文件**也必须落盘**,并在标题处加 `> **本轮状态: 中断未完成 (YYYY-MM-DD HH:MM)**` 显式标识;中断时 `已覆盖页面/接口清单` 记录到中断点为止,未覆盖项标 `跳过(中断未完成)`。
   - 中断的进度必须标「中断未完成」(沿用 §4)。
   - **强约束**: 进度文件必须**与本轮测试报告同时间戳**生成,若时间戳不同步视为报告流程未完成,接力 debug 不应启动(由 shell 层校验文件存在性后才会启动 `AutoDebugTestReport.sh`)。
3. 报告落盘即可——**接力(报告生成后自动 debug)**: 报告落盘 → Agent 退出 → 入口脚本 `AutoTestAndSaveReport.sh` 在 shell 层**确定性接力**启动 `AutoDebugTestReport.sh` 自动修复(含待处理报告预检 + flock 防重入;不再依赖 Agent 自觉,避免「声明了却从不接线」式断链)。Agent **无需也禁止**自行执行 `AutoDebugTestReport.sh`(与 shell 接力双重启动)。修复流程会提交/推送代码修复并删除或归档已处理报告,报告内容必须自包含、结论写全。
   - **强约束 — 测试 Agent 严禁删除或归档报告文件**: `TestReport/自动化测试报告_*.md` 与 `AutoTestProgress/自动化测试进度_*.md` 必须**原样保留在工作区**,作为接力脚本 `AutoDebugTestReport.sh` 的检测入口(`any_pending_report()` 通过文件存在性判断)。Agent 自行删除将导致接力链断裂(脚本检测不到待处理报告 → 静默跳过 → 缺陷无人修复)。**仅当接力脚本完成代码修复并 git commit 后**,才由接力流程删除或 `_无问题.md` 后缀归档。即使用户直接要求删除,Agent 也**必须拒绝并解释接力依赖**,建议改为等接力脚本自动处理。
   - 接力启动的前置条件(`AutoTestAndSaveReport.sh` `any_pending_report` 校验):
     - 必备: `TestReport/自动化测试报告_<同时间戳>.md` 存在(且未被 Agent 提前删除)
     - 必备: `AutoTestProgress/自动化测试进度_<同时间戳>.md` 存在(本轮新增强制项)
     - 任一缺失 → 接力不启动,运行索引日志写 `event=handoff_skipped_missing_progress`(对应修复:测试 Agent 必须先生成两份产物再退出,且不得删除)。
4. 触发协议层诊断时,**额外**写协议抓包分析报告(API Key/Token/Cookie/密码脱敏),并在测试报告里引用。
5. **中断未完成也要写报告**,标 `中断未完成`,避免被后续轮次误判为已覆盖。
6. **阻塞停止后必须立即生成报告**（见 §8 模板），包含阻塞位置、诊断数据、已覆盖清单。

### 11. 注意事项

- **go-web-debug-tool 是唯一的浏览器交互工具**：所有页面操作必须通过 go-web-debug-tool 完成，禁止直接 curl 页面 HTML 或绕过工具执行 JS。
- **拟人化操作是强制要求**：必须使用 `human_click`、`human_input`、`scroll use_wheel=true` 等拟人化 action，禁止直接使用 `click`、`input_text` 等非拟人化 action（除非拟人化 action 失败后的降级）。
- **操作后必须等待**：每次操作后必须等待页面稳定（`ready_state=complete` 或至少 500ms）再执行下一步。
- **报告文件保留(接力链不断裂)**: `TestReport/`、`AutoTestProgress/` 下的报告与进度文件必须**原样保留在工作区**,严禁测试 Agent 删除、移动、重命名或 `_无问题.md` 归档。接力脚本 `AutoDebugTestReport.sh` 通过 `any_pending_report()` 检测文件存在性来决定是否启动修复;Agent 提前删除将导致接力链断裂(缺陷无人修复)。即使用户直接要求删除,Agent 也**必须拒绝并解释接力依赖**,建议等接力脚本在代码修复提交后自动处理。
- **多 Agent 并发与 Git**: 测试期间留意其他开发 Agent;**禁止** `git add` / `git commit` / `git push` 等一切写操作(由 shell 接力脚本统一提交)。
- **数据安全**: 抓包数据 / 测试报告严禁明文输出 API Key / Token / Cookie / 密码,必须脱敏;`user_model_info.json` 内容不得写入报告。
- **服务可用性**: 任一端口(9101/29001)不可达时,先在报告中标注,再决定是否继续(可跳过该端口测试)。
- **前端双构建**: 测试前确认 `npm run build` 已产出 `dist-manager` + `dist-user`,且 grep 复验通过。
- **优化建议**: 报告须给具体可执行建议(系统提示词迭代 / 前端修复 / 协议重试策略 / 安全加固),定位到根因。
- **端口规范**: 测试仅针对 Web 服务(9101/29001),不涉及 AI 代理(29000/29003)、MCP(29002)、爬虫 CDP(9222)的内部测试。

### 12. 主流程阻塞时的降级测试路径

> 当主流程被阻塞（服务不可达 / 登录失败 / 页面渲染异常）时，**不要立即整轮终止**，按以下降级路径继续采集可观测数据。
> **注意**：降级路径采集数据后，**仍需立即生成问题报告**（遵循 §4 阻塞即停策略）。

#### 12.1 第一层：REST API 快照（不依赖前端渲染）

通过 HTTP 直接验证服务端状态：

- 管理端 `GET http://localhost:9101/UserInfoInterface` — 应返回 401(未登录)或管理员信息
- 用户端 `GET https://localhost:29001/UserInfoInterface` — 应返回 401(未登录)或用户信息
- `GET http://localhost:9101/GitInfoInterface` / `/SystemInfoInterface` — 系统信息
- `GET http://localhost:9101/TimeSpanConfigInterface` — 时间跨度配置

**判定**：若 REST 端点均无响应 → 服务未启动;若返回 5xx → 服务端异常;若 401 正常返回 → 鉴权中间件工作正常。

#### 12.2 第二层：服务端日志诊断（不依赖前端渲染）

通过日志文件直接观察：

- `LsmTokensServer.log` 中 `Listen` / `started` 相关日志(确认端口监听)
- 鉴权失败日志(`401` / `unauthorized`)
- 业务处理错误日志(panic / error)

**判定**：若日志显示端口监听正常 + 无业务错误,但前端无响应 → 前端/构建产物问题;若日志有 panic → 服务端缺陷。

#### 12.3 第三层：构建产物验证（不依赖服务）

当服务与日志均不可用时,验证构建产物：

- `ls ClientWeb/dist-manager/` 与 `ClientWeb/dist-user/` 是否存在
- `grep -rE "UserManage|ManagerLogin" ClientWeb/dist-user/` 双构建隔离复验
- `go build ./...` 编译是否通过

#### 12.4 降级路径产出要求

即使主流程完全阻塞,降级路径采集的数据也必须写入报告：

- **REST 快照** → 填入报告「诊断数据与证据链」节
- **日志统计** → 填入报告「服务端日志统计」节
- **构建产物** → 填入报告「构建产物验证」节
- **结论**：明确「主流程阻塞,但通过降级路径验证了 X/Y/Z」或「降级路径也失败,问题在更底层」

**禁止**: 主流程阻塞后直接终止、报告只写「全部未覆盖」而无诊断数据。降级完成后仍需按 §4 生成阻塞问题报告。

### 13. go-web-debug-tool 操作速查表

| 场景 | 请求示例 |
|------|----------|
| 打开页面 | `POST /NewChromePage {"url":"http://localhost:9101/Home","wait_until":"networkidle"}` |
| 拟人点击 | `POST /ControlChromePage {"page_id":"p_xxx","action":"human_click","params":{"selector":"button.submit"}}` |
| 拟人输入 | `POST /ControlChromePage {"page_id":"p_xxx","action":"human_input","params":{"selector":"input[name=username]","text":"admin"}}` |
| 滚轮滚动 | `POST /ControlChromePage {"page_id":"p_xxx","action":"scroll","params":{"use_wheel":true,"delta_y":300}}` |
| 滚动到元素 | `POST /ControlChromePage {"page_id":"p_xxx","action":"scroll_to","params":{"selector":".footer","block":"center"}}` |
| 悬停展开 | `POST /ControlChromePage {"page_id":"p_xxx","action":"hover","params":{"selector":".dropdown-toggle"}}` |
| 选择下拉 | `POST /ControlChromePage {"page_id":"p_xxx","action":"select_option","params":{"selector":"#role","value":"admin"}}` |
| 按键 | `POST /ControlChromePage {"page_id":"p_xxx","action":"key_press","params":{"key":"Enter"}}` |
| 查看元信息 | `POST /LookChromePageInfo {"page_id":"p_xxx","info":"page_meta"}` |
| 截图 | `POST /LookChromePageInfo {"page_id":"p_xxx","info":"screenshot","params":{"full_page":true}}` |
| 网络请求 | `POST /LookChromePageInfo {"page_id":"p_xxx","info":"network","params":{"limit":100}}` |
| 控制台日志 | `POST /LookChromePageInfo {"page_id":"p_xxx","info":"console","params":{"level":["error"],"limit":50}}` |
| 元素详情 | `POST /LookChromePageInfo {"page_id":"p_xxx","info":"elements","params":{"selector":"#login-form"}}` |
| 关闭页面 | `POST /CloseChromePage {"page_id":"p_xxx"}` |
| 列出页面 | `POST /ListChromePages {}` |

### 14. 一轮测试的生命周期（loop 视角）

```
  Loop 入口：AutoTestAndSaveReport.sh 启动本轮测试
  ─────────────────────────────────────────────────────────
  1. 读取进度文件，确定本轮测试范围（§6.1）
  2. 启动 go-web-debug-tool 浏览器会话
  3. 拟人化执行测试（§6.3-§6.6）
  4. 遇到阻塞？→ 立即停止，生成阻塞报告（§8）
  5. 正常完成？→ 生成完整测试报告（§10）
  6. 生成进度文件（§10.2）
  7. Agent 退出
  ─────────────────────────────────────────────────────────
  Shell 接力：AutoDebugTestReport.sh 自动修复
  → 修复代码 → git commit → 归档报告 → 下一轮 Loop 继续
```

> **核心理念**：每一轮测试都是 loop 中的一个迭代，目标是**发现当前最突出的问题**而非一次性覆盖所有场景。阻塞即停、快速报告、接力修复、下一轮继续——这才是高效的自动化测试循环。
