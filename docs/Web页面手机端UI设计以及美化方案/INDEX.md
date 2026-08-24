# Web 页面手机端 UI 设计以及美化方案

> 目标：管理端 Web（9101）与用户端 Web（29001）同一套 React SPA 在手机端（≤600px）与平板（≤860px）下完整可用、触控友好、视觉统一。
> 总规范见 [`00-总体设计方案与移动端规范.md`](00-总体设计方案与移动端规范.md)，各页面专属方案如下。

## 文档清单

| 文档 | 覆盖页面 | 端口 |
|---|---|---|
| [00-总体设计方案与移动端规范](00-总体设计方案与移动端规范.md) | 全局断点 / 布局 / 弹窗 / 表格 / 表单 / 触控规范 | 双端 |
| 01-登录与首页 | Login、Home | 双端 |
| 02-用户管理 | UserManage | 管理端 |
| 03-端点管理 | DstEndPointManage | 双端 |
| 04-路由管理 | AIRouteManage | 双端 |
| 05-模型与Agent信息 | ModelInfo、AgentInfo | 双端 |
| 06-协议转换分析器 | ProtocolConvertAnalyzer | 双端 |
| 07-对话浏览与汇总 | ChatAnalysis、ChatAnalysisTotal | 双端 |
| 08-会话与任务分析 | ChatAnalysisSession、ChatAnalysisTask | 双端 |
| 09-对话查看 | ChatDialog | 双端 |
| 10-爬虫数据源与日报 | SpiderDataSource、SpiderDailyInfo | 双端 |
| 11-清理报告 | CleanupReport | 双端 |
| 12-顶部工具弹窗组 | 用户日志/Wiki/证书/Git/源码/系统信息/README/构建日志 | 双端 |
| 13-实现说明与验证清单 | 代码改动点与回归验证 | 双端 |
