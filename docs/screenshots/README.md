# Screenshots · 文档级截图资源

> 本目录为 LsmTokensServer 项目 README / Wiki / 博客文章所用的核心功能截图。所有图片通过
> [`go-web-debug-tool`](../../go-web-debug-tool)（Chrome DevTools Protocol 自动化驱动）
> 自动采集，**未经过任何手动操作**。
>
> 采集脚本与采集 SOP 详见 [`tmpPlan/README功能截图与文档优化方案_20260912.md`](../../tmpPlan/README功能截图与文档优化方案_20260912.md)。

## 1. 命名规范

| 前缀 | 含义 |
|------|------|
| `M` | Manager Web（管理员，端口 9101）截图 |
| `U` | User Web（用户端，端口 29001）截图 |
| 数字 | 顺序编号（01 = 登录，03 = 用户管理，…） |
| `-full` | 全屏截图（full_page=true），高度 = 整页内容 |

## 2. 文件清单

### Manager Web（管理员）

| 文件 | 路由 | 说明 |
|------|------|------|
| `M01-login.png` | `/ManagerLogin` | 管理员登录页（用户名/密码/验证码） |
| `M03-user-manage.png` | `/UserManage` | 用户管理局部（首屏） |
| `M03-user-manage-full.png` | `/UserManage` | 用户管理全屏 |
| `M04-route-manage.png` | `/AIRouteManage` | 路由管理局部（首屏） |
| `M04-route-manage-full.png` | `/AIRouteManage` | 路由管理全屏（23 条路由全展示） |
| `M06-model-info.png` | `/ModelInfo` | 模型统计局部 |
| `M06-model-info-full.png` | `/ModelInfo` | 模型统计全屏（含趋势图 + 排行） |
| `M07-agent-info.png` | `/AgentInfo` | Agent 统计局部 |
| `M07-agent-info-full.png` | `/AgentInfo` | Agent 统计全屏 |
| `M09-spider-data-source.png` | `/SpiderDataSource` | 爬虫数据源 |
| `M10-spider-daily-info.png` | `/SpiderDailyInfo` | 爬虫日报 |
| `M11-cleanup-report.png` | `/CleanupReport` | 清理报告局部 |
| `M11-cleanup-report-full.png` | `/CleanupReport` | 清理报告全屏（含子表容量监控） |
| `M16-chat-dialog.png` | `/ChatDialog?rows=...` | 对话详情（按行展开视图） |

### User Web（用户端）

| 文件 | 路由 | 说明 |
|------|------|------|
| `U01-login.png` | `/#/Login` | 用户登录页（Model / User 双 Tab） |
| `U02-home.png` | `/#/Home` | 用户首页（21 个模型卡片） |
| `U03-chat-dialog.png` | `/#/ChatDialog?model_name=liusm191-ai-model` | 对话页（System Prompt + API 配置） |

## 3. 敏感信息脱敏规则

> ⚠️ **重要**：截图采集脚本必须严格遵守以下脱敏 SOP。

| 字段 | 脱敏方式 | 工具 |
|------|----------|------|
| 密码 | `input[type=password]` 的 `value` 置空 + 派发 `input` 事件 | `eval_js` (`expression_b64`) |
| 手机号 | `^1\d{10}$` 替换为 `^(\d{3})\d{4}(\d{4})$` → `$1****$2` | `eval_js` |
| API Key | 模板内置 `maskKey(k)` → `前 8 位 + ****` | 前端组件已实现 |
| JWT / conf 密钥 | 服务端永不返回，仅配置中维护 | 后端 |

截图采集前先执行：

```js
// 1) 清空所有密码字段
document.querySelectorAll('input').forEach(i => {
  if (i.type === 'password') { i.value = ''; i.dispatchEvent(new Event('input', {bubbles:true})); }
  // 2) 掩码手机号
  if (/^1\d{10}$/.test(i.value)) i.value = i.value.replace(/^(\d{3})\d{4}(\d{4})$/, '$1****$2');
});
```

## 4. 自动采集流程

```bash
# 1. 启动 LsmTokensServer 与 go-web-debug-tool
./rebuild_restart_app.sh          # 启动主服务（含 9101/29001）
cd go-web-debug-tool && ./GoWebDebugTool -d  # 启动 CDP harness

# 2. 通过 /NewChromePage 打开每个页面 → /LookChromePageInfo info=screenshot
#    截图保存在 docs/screenshots/{manager,user}/
# 3. eval_js 注入脱敏脚本（见 §3）

# 4. 完成后人工 / git 自动 commit
git add docs/screenshots/ README*.md
git commit -m "阶段XX：核心功能截图采集与三语 README 操作步骤补充"
```

## 5. 历史变更

| 日期 | 变更 |
|------|------|
| 2026-09-12 | 初版：管理员 Web 14 张 + 用户 Web 3 张共 17 张截图 |