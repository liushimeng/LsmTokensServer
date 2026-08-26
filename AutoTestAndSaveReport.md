## 自动化 Web 测试任务：LsmTokensServer 管理员端 + 用户端全功能专项

> 本项目按 [MIT License](LICENSE) 开源。测试任务面向维护者，参与前请阅读 [CLAUDE.md](CLAUDE.md) 与 [AutoDebugTestReport.md](AutoDebugTestReport.md)。
> 本文件为 LsmTokensServer 统一测试入口，**产出** `TestReport/自动化测试报告_YYYYMMDD_HHMMSS.md` 前缀的报告。

- **工作目录**: `/usr/local/LsmTokensServer/LsmTokensServer`
- **入口脚本**: `AutoTestAndSaveReport.sh`(随机选编程 Agent CLI → 跑测试 → 报告落盘 → shell 层确定性接力 `AutoDebugTestReport.sh` 自动修复)
- **产物路径**:
  - 测试报告: `TestReport/自动化测试报告_YYYYMMDD_HHMMSS.md`
  - 协议抓包分析报告: `TestReport/协议抓包分析报告_YYYYMMDD_HHMMSS.md`
  - **自动化测试进度文件(跨轮去重基线,强制生成+读取)**: `AutoTestProgress/自动化测试进度_YYYYMMDD_HHMMSS.md`
  - 子工程工具报告: `go-web-debug-tool/UseReport/测试工具使用报告_YYYYMMDD_HHMMSS.md`
  - 文件名时间戳统一 `YYYYMMDD_HHMMSS`,精度到秒
- **测试产物处理**: `TestReport/*` 与 `AutoTestProgress/*` 由脚本层 git add + commit + 接力 debug；**测试 Agent 禁止**自行执行 `git add` / `git commit` / `git push`(避免与 shell 接力双重提交)。
- **执行模式**: 主 Agent 跑核心流程;遇代码读取/协议抓包/数据查询可按需委派 SubAgent,不阻塞主流程。
- **执行策略**: 模仿真实人类管理员/用户,串行单浏览器,严守「每完成一步等待页面完全加载再走下一步」的拟人节奏;以最近 10 次非缺陷修复提交划定测试范围,新增/优化项优先。
- **核心目标**: 验证管理员 Web(9101) 与用户 Web(29001) 全部页面的可用性、所有 REST API 的功能正确性、双构建隔离合规性、安全红线(鉴权/脱敏/密码哈希)。
- **代码变更限制**: 业务代码只读;若发现缺陷,**只出报告与建议**,严禁自动改业务代码。

### 1. 测试范围

#### 1.1 管理员 Web(managerWebListenPort=9101, HTTP)

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

#### 1.2 用户 Web(userWebListenPort=29001, HTTPS)

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

#### 1.3 双构建隔离合规性(强制)

- 用户端(`__APP_ROLE__='user'`)产物 `ClientWeb/dist-user/` 经 Rollup 死代码消除后**不得包含**任何管理端字样:
  - `UserManage`、`ManagerLogin`、`UserManageInterface`、`ManagerLoginInterface`、`ManagerLogoutInterface`、`ChatAnalysisBatchDeleteInterface`、`ProtocolConvertAnalyzerToggle`、`ProtocolConvertAnalyzerUsers`。
- 构建后 grep 复验:`grep -rE "UserManage|ManagerLogin|UserManageInterface|ManagerLoginInterface" ClientWeb/dist-user/` 必须为空。
- 角色由构建期常量 `__APP_ROLE__` 决定,禁止运行时嗅探端口/localStorage。

### 2. 登录流程

#### 2.1 管理员登录(ManagerLogin, 9101)

1. `GET http://localhost:9101/CaptchaGenerate` → 返回 `{success, captcha_id, image_url}`。
2. 由于验证码图片需要人工识别,**自动化测试采用以下方式之一**:
   - **方式 A(推荐)**:直接调 API 时传入 `captcha_id` + `captcha_code`,服务端 `captcha.VerifyString` 校验;测试环境若配置了 `managerWebAuthDisabled=true` 可跳过验证码。
   - **方式 B**:读取 `LsmTokensServer.conf` 的 `security.managerUserName` / `managerPassword`,结合 OCR/人工输入验证码。
3. `POST http://localhost:9101/ManagerLoginInterface` body: `{user_name, password, captcha_id, captcha_code}` → 成功返回 `{success:true}` + 设置 `manager_token` Cookie。
4. 后续请求携带 Cookie 访问管理端业务接口。

> **注意**:管理员凭证在 `LsmTokensServer.conf` 的 `security` 段,测试 Agent 需读取该文件获取 `managerUserName` / `managerPassword`(该文件已加入 `.gitignore`,仅本地存在)。

#### 2.2 用户登录(Login, 29001)

1. 读取 `user_model_info.json`(与本文件同目录)获取 **模型名** 与 **API Key**:
   ```json
   { "模型名": "xxx", "API Key": "yyy" }
   ```
   **该文件已加入 `.gitignore`,含敏感信息,禁止提交、禁止明文写入报告**。
2. `GET https://localhost:29001/CaptchaGenerate` → 返回 `{success, captcha_id, image_url}`。
3. `POST https://localhost:29001/UserLoginInterface` body: `{login_type:"model", model_name, api_key, captcha_id, captcha_code}` → 成功返回 `{success:true}` + 设置 `user_token` Cookie。
4. 后续请求携带 Cookie 访问用户端业务接口。

> **重要**:`user_model_info.json` 不存在或格式错误时,测试 Agent **必须**在报告中标注「用户端登录跳过:凭证文件缺失/格式错误」,不得伪造/硬编码凭证。

### 3. 终止条件

1. 阶段卡死 / 页面崩溃 / 规则自相矛盾 → 终止**当前用例**,记录后判断是否继续。
2. 安全红线被突破(接口未鉴权、明文密码泄漏、用户端包含管理代码) → 整轮终止,标记为严重缺陷。
3. 核心 API 连续 5 次调用失败(鉴权失败/协议不兼容/服务端持续故障)→ 整轮终止。
4. 双构建隔离违规(用户端产物含管理字样) → 整轮终止,标记为严重缺陷。
5. 无论正常结束或异常终止,均须按规范出报告;中断的进度必须标「中断未完成」。

### 4. 启动前置

1. 读最近 10 次**非缺陷修复类**提交(`git log --oneline -10 -- ':!*fix' ':!*BUG'` 等过滤),梳理新增/优化项作为本轮重点。
2. 确认服务已启动:`curl -s -o /dev/null -w "%{http_code}" http://localhost:9101/UserInfoInterface` 与 `curl -sk -o /dev/null -w "%{http_code}" https://localhost:29001/UserInfoInterface` 任一可达即开始。
3. 读 `LsmTokensServer.conf` 获取管理员凭证(如有)、读 `user_model_info.json` 获取用户端模型凭证。
4. 确认前端双构建产物存在:`ClientWeb/dist-manager/` 与 `ClientWeb/dist-user/`。
5. **加载上一轮自动化测试进度文件作为去重基线(强制)**:
   - 路径: `AutoTestProgress/自动化测试进度_*.md`(取**文件名时间戳最新**的一份)。
   - 若目录不存在或无任何进度文件 → 视为**首次启动**,全部页面/接口全量测试,跳过本节。
   - 加载后必须解析其中三个机器可读区块(详见 §8.2 进度文件必备字段):
     - `本轮基线(Git HEAD)` → 与本轮 `git rev-parse HEAD` 比较;**若 HEAD 变化**,则基线失效,全部页面/接口重测,**忽略进度文件中的「已通过」清单**。
     - `已覆盖页面/接口清单` → 仅在 HEAD 未变时作为去重依据:**已通过**项跳过,**失败/跳过/失败后修复/Bug** 项必须重测。
     - `缺陷清单与状态` → 表格中**所有非「已修复验证」状态**的 Bug 必须重测验证。
   - **凡被跳过的项**,本轮测试报告必须显式注明「延续自历史进度文件 XXXXXXX_XXXXXX.md,未重测(HEAD 未变)」。
   - **凡被重测的项**,无论结果与上次是否一致,**都视为本轮新数据写入本轮报告与进度文件**(不允许简单复制历史结论)。
6. 已充分覆盖的模块可跳过;变更项必须覆盖。(在第 5 步去重逻辑之上叠加 — HEAD 未变时进度文件清单优先,HEAD 变了则以 git 变更项优先。)

### 5. 大模型 API 异常与协议层诊断

- **触发**: 任一 AI 代理调用 LLM 失败 ≥ 2 次(超时/报错/空响应/tool_use 解析失败等)即启动协议诊断。
- **方式**: 调 `/ProtocolConvertAnalyzer*` 接口或读取 `ServerGo/protocol/` 相关代码,结合 `LsmTokensServer.log` 抓包分析。
- **重点排查**:
  - 请求侧: URL / 鉴权头 / `model` / `messages` 角色顺序 / `tools` `tool_use` `tool_result` 配对 / `max_tokens` 与上下文长度。
  - 响应侧: HTTP 状态码(400/401/429/5xx)/ `error.type|message` / `stop_reason` / SSE 是否截断。
  - 链路侧: TLS 握手 / 超时与连接重置 / 代理网关改写 / Anthropic↔OpenAI 协议适配差异。
- **结论落地**: 错误归因写入协议抓包分析报告,并在测试报告里引用;若属可修复缺陷,出具体优化建议(**不自动改业务代码**)。

### 6. 测试步骤

#### 6.1 准备
- 读取凭证(管理员 conf + 用户 user_model_info.json)。
- 验证码走 API 获取,测试环境若 `managerWebAuthDisabled` 可旁路。
- 记录本轮测试范围 + 重点变更项。
- **执行去重预检(基于 §4.5 的进度文件)**:
  - 输出本轮测试清单(下表),`状态` 列三选一:`重测`、`跳过(基线已通过)`、`跳过(基线无此历史项)`。
  - 表格写入本轮测试报告与进度文件,作为审计依据。

  | # | 页面 / 接口 | 分组 | 上一轮状态 | 本轮判定 | 备注 |
  |---|------------|------|------------|----------|------|
  | 1 | 管理端 #/Home | 首页 | 通过 | 跳过(基线已通过) | 延续自进度文件 xxx.md |
  | 2 | 管理端 #/UserManage | 用户管理 | 通过 | 跳过(基线已通过) | — |
  | 3 | … | … | … | … | … |
  | … | … | … | … | … | … |

  - 仅有 `重测` 项进入 6.2~6.7 实际执行;`跳过(基线已通过)` 项在 6.2~6.7 的对应章节中**直接引用进度文件结论**即可,不再展开细节。
  - 当进度文件 HEAD 与本轮 HEAD 不一致时,本步输出「基线失效,全部页面/接口重测」一句话,跳过该表格。

#### 6.2 管理员 Web 页面遍历(9101)
- 模拟人类管理员遍历所有可交互元素(按钮/链接/开关/输入框/下拉/分页)。
- 结合最近 10 次提交判断是否有新/优功能:无则跑基础可用性,有则重点验证。
- 每次点击后**等待页面完全加载**(推荐 800~1500ms)再走下一步,符合真实人类节奏。
- 未达预期项详细记录,写入最终报告。

#### 6.3 管理员端核心功能验证
- **用户管理**:UserManage 列表/增/改/删、UserModelManage 模型分配。
- **路由管理**:AIRouteManage 路由规则增/改/删、DstEndPointManage 端点管理。
- **模型/代理**:ModelInfo 列表/统计、AgentInfo 列表、ProtocolConvertAnalyzer 启停/测试/记录/映射。
- **对话分析**:ChatAnalysis 列表/详情/批量删、ChatAnalysisTotal 图表/范围报告/WS 流式、Session/Task 列表、ChatDialog 对话。
- **爬虫**:SpiderDataSource 数据源/爬取、SpiderDailyInfo 日报、CleanupReport 清理报告。
- **系统**:GitInfo/SystemInfo/Wiki/UserInfoLog/BuildLog/CertDownload。

#### 6.4 用户 Web 登录与页面遍历(29001)
- 用 `user_model_info.json` 凭证完成模型登录。
- 遍历用户端所有页面(同 6.2 节奏)。
- 验证用户端**无**管理端功能入口(菜单/路由/API 调用均应 401 或不存在)。

#### 6.5 用户端核心功能验证
- 对话分析(与管理员同名接口,数据按用户/模型维度过滤)。
- 路由/模型/端点(用户维度,仅本人数据)。
- 爬虫(用户维度)。

#### 6.6 安全红线验证
- 管理端所有业务接口未携带 JWT → 401(参考 `e2e_web_test.go::TestManagerRoutesRegisteredAndProtected`)。
- 用户端未登录访问业务接口 → 401 或跳登录。
- 接口响应中密码字段置空、手机号脱敏(`api.MaskPhone`)。
- 用户端产物零管理代码(grep 复验)。

#### 6.7 协议转换分析器(独立用例)
- 管理端 7 条 / 用户端 5 条接口功能验证。
- 实际发起一次 Anthropic↔OpenAI 协议转换,抓包分析请求/响应合规性。

### 7. 操作与覆盖要求

- **拟人操作**: 每步操作后**等待页面完全加载**(建议 800~1500ms)再走下一步,符合真实用户节奏;严禁「点击 + 立刻下一步」的机械节奏。
- **覆盖度**: 全面覆盖管理员 + 用户双端所有可交互元素,重点验证按钮/表格/表单/下拉/分页/弹窗;双构建隔离与安全红线必须覆盖。
- **客观性**: 严格基于页面截图 / 日志 / 协议抓包 / API 响应等可观测事实,杜绝幻觉与臆断。

### 8. 报告与后处理

1. 测试结束首先生成测试报告,完整保存。报告必须包含:
   - `测试范围与变更基线`
   - `管理员 Web(9101) 页面遍历结果`(逐页面:通过/缺陷/跳过)
   - `用户 Web(29001) 页面遍历结果`(逐页面:通过/缺陷/跳过)
   - `安全红线验证结果`
   - `双构建隔离合规性(grep 复验)`
   - `缺陷列表`(BugID / 严重等级 / 复现步骤 / 期望 vs 实际 / 证据)
   - `诊断数据与证据链`(API 响应/日志/抓包)
   - `优化建议`(定位到根因:提示词/工具定义/业务逻辑/前端/协议适配/安全)
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
   - 中断的进度必须标「中断未完成」(沿用 §3.5)。
   - **强约束**: 进度文件必须**与本轮测试报告同时间戳**生成,若时间戳不同步视为报告流程未完成,接力 debug 不应启动(由 shell 层校验文件存在性后才会启动 `AutoDebugTestReport.sh`)。
3. 报告落盘即可——**接力(报告生成后自动 debug)**: 报告落盘 → Agent 退出 → 入口脚本 `AutoTestAndSaveReport.sh` 在 shell 层**确定性接力**启动 `AutoDebugTestReport.sh` 自动修复(含待处理报告预检 + flock 防重入;不再依赖 Agent 自觉,避免「声明了却从不接线」式断链)。Agent **无需也禁止**自行执行 `AutoDebugTestReport.sh`(与 shell 接力双重启动)。修复流程会提交/推送代码修复并删除或归档已处理报告,报告内容必须自包含、结论写全。
   - 接力启动的前置条件(`AutoTestAndSaveReport.sh` `any_pending_report` 校验):
     - 必备: `TestReport/自动化测试报告_<同时间戳>.md` 存在
     - 必备: `AutoTestProgress/自动化测试进度_<同时间戳>.md` 存在(本轮新增强制项)
     - 任一缺失 → 接力不启动,运行索引日志写 `event=handoff_skipped_missing_progress`(对应修复:测试 Agent 必须先生成两份产物再退出)。
4. 触发协议层诊断时,**额外**写协议抓包分析报告(API Key/Token/Cookie/密码脱敏),并在测试报告里引用。
5. 中断未完成也要写报告,标 `中断未完成`,避免被后续轮次误判为已覆盖。

### 9. 注意事项

- **多 Agent 并发与 Git**: 测试期间留意其他开发 Agent;**禁止** `git add` / `git commit` / `git push` 等一切写操作(由 shell 接力脚本统一提交)。
- **数据安全**: 抓包数据 / 测试报告严禁明文输出 API Key / Token / Cookie / 密码,必须脱敏;`user_model_info.json` 内容不得写入报告。
- **服务可用性**: 任一端口(9101/29001)不可达时,先在报告中标注,再决定是否继续(可跳过该端口测试)。
- **前端双构建**: 测试前确认 `npm run build` 已产出 `dist-manager` + `dist-user`,且 grep 复验通过。
- **优化建议**: 报告须给具体可执行建议(系统提示词迭代 / 前端修复 / 协议重试策略 / 安全加固),定位到根因。
- **端口规范**: 测试仅针对 Web 服务(9101/29001),不涉及 AI 代理(29000/29003)、MCP(29002)、爬虫 CDP(9222)的内部测试。

### 10. 主流程阻塞时的降级测试路径

> 当主流程被阻塞（服务不可达 / 登录失败 / 页面渲染异常）时，**不要立即整轮终止**，按以下降级路径继续采集可观测数据。

#### 10.1 第一层：REST API 快照（不依赖前端渲染）

通过 HTTP 直接验证服务端状态：

- 管理端 `GET http://localhost:9101/UserInfoInterface` — 应返回 401(未登录)或管理员信息
- 用户端 `GET https://localhost:29001/UserInfoInterface` — 应返回 401(未登录)或用户信息
- `GET http://localhost:9101/GitInfoInterface` / `/SystemInfoInterface` — 系统信息
- `GET http://localhost:9101/TimeSpanConfigInterface` — 时间跨度配置

**判定**：若 REST 端点均无响应 → 服务未启动;若返回 5xx → 服务端异常;若 401 正常返回 → 鉴权中间件工作正常。

#### 10.2 第二层：服务端日志诊断（不依赖前端渲染）

通过日志文件直接观察：

- `LsmTokensServer.log` 中 `Listen` / `started` 相关日志(确认端口监听)
- 鉴权失败日志(`401` / `unauthorized`)
- 业务处理错误日志(panic / error)

**判定**：若日志显示端口监听正常 + 无业务错误,但前端无响应 → 前端/构建产物问题;若日志有 panic → 服务端缺陷。

#### 10.3 第三层：构建产物验证（不依赖服务）

当服务与日志均不可用时,验证构建产物：

- `ls ClientWeb/dist-manager/` 与 `ClientWeb/dist-user/` 是否存在
- `grep -rE "UserManage|ManagerLogin" ClientWeb/dist-user/` 双构建隔离复验
- `go build ./...` 编译是否通过

#### 10.4 降级路径产出要求

即使主流程完全阻塞,降级路径采集的数据也必须写入报告：

- **REST 快照** → 填入报告「诊断数据与证据链」节
- **日志统计** → 填入报告「服务端日志统计」节
- **构建产物** → 填入报告「构建产物验证」节
- **结论**：明确「主流程阻塞,但通过降级路径验证了 X/Y/Z」或「降级路径也失败,问题在更底层」

**禁止**: 主流程阻塞后直接终止、报告只写「全部未覆盖」而无诊断数据。
