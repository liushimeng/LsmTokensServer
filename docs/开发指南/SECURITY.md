# LsmTokensServer 安全规范（v2.0.56）

> 适用范围：后端 `ServerGo/`、前端 `ClientWeb/`、Python 分析脚本、文档。
> AI Agent（Claude Code / Codex / OpenCode / pi / Hermes / OpenClaw 等）修改涉及安全面时必须遵守本规范。

## 1. 鉴权架构

| 端口 | 端 | 鉴权方式 | 登录入口 |
|---|---|---|---|
| 9101 | 管理端 | `api.ManagerAuthMiddleware`（manager JWT，HttpOnly Cookie `lsm_manager_token`） | `/ManagerLoginInterface` |
| 29001 | 用户端 | `api.UserAuthMiddleware`（user JWT，Cookie `lsm_user_token`） | `/UserLoginInterface`（用户名+密码+手机号 / 模型名+API Key，均带验证码与防爆破锁定） |
| 29000/29003 | AI 代理 | 请求自带 API Key（`TAgentHttpUserModelInfo.APIKey`） | 无 |

- JWT 签名密钥：`LsmTokensServer.conf` → `security.jwtSecret`；未配置时进程启动随机生成（重启后登录态全部失效）。
- 管理端凭证：`security.managerUserName` / `managerPassword`；**未配置时管理端业务接口全部拒绝**（不存在默认账号）。
- 中间件放行清单（新增公开路由需评审）：登录/验证码/静态资源/`/healthz`/AI 代理路径。

## 2. 密码与敏感数据处理

- 用户密码：bcrypt（`api.HashPassword` / `api.VerifyPassword`）。旧明文在用户下一次登录成功时自动升级为哈希，无需批量迁移脚本。
- 管理端"编辑用户"提交空密码 / `$2` 前缀哈希 / 含 `****` 的掩码手机号 → 服务端视为"未修改"，保留原值。
- 响应脱敏：密码字段一律置空；手机号 `api.MaskPhone`（`138****5678`）；API Key 列表只给 `api_key_masked`。
- 前端：localStorage 禁止保存 API Key（"记住我"仅存模型名）；对话历史上限 200 条 + 30 天过期。

## 3. 网络与传输

- `X-Forwarded-For` / `X-Real-IP` 仅在 `security.trustProxyHeaders=true` 时信任（部署于可信反代后）。
- Cookie `Secure` 标志随 HTTPS 自动启用（`userWebUseHTTPS`）。
- 生产 IP、密钥、密码禁止出现在源码 / 注释 / Markdown / 抓包样本中（含 `<REDACTED>` 占位）。

## 4. 随机数

- API Key / JWT 密钥：只用 `crypto/rand`；失败返回错误，**禁止降级为时间戳等可预测值**。

## 5. 已知遗留（人工跟进）

- 历史 git 提交（`0f3dd8f`、`1dd3a1a`）中残留旧 MySQL 密码字符串：需轮换数据库密码，必要时 `git filter-repo` 改写历史。
- `ChatDialogInterface` config 仍返回完整 API Key（前端同源代理调用依赖；已置于登录态之后，属已评估的保留项）。
