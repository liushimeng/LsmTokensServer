<!-- markdownlint-disable -->
<div align="center">

[🇨🇳 中文](README.md) · [🇬🇧 English](README.en.md) · [🇯🇵 日本語](README.ja.md)

---

```
╔═════════════════════════════════════════════════════════════════════╗
║  _     _                        _____           _                   ║
║ | |   (_)_ __   ___  ___  _   |_   _|__   ___ | |   _   _  ___     ║
║ | |   | | '_ \ / _ \/ __|| | | || |/ _ \ / _ \| |  | | | |/ _ \    ║
║ | |___| | | | |  __/\__ \| |_| || | (_) | (_) | |__| |_| |  __/    ║
║ |_____|_|_| |_|\___||___/ \__, ||_| \___/ \___/|_____\__, |\___|    ║
║                           |___/                      |___/         ║
║      AI Tokens Proxy · Load Balancing · Anthropic ⇄ OpenAI          ║
╚═════════════════════════════════════════════════════════════════════╝
```

## 🔀 开源 AI Tokens 中转服务 · N 模型负载均衡 · 不拆分 Agent Session

</div>
<!-- markdownlint-restore -->

<div align="center">

[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![PRs Welcome](https://img.shields.io/badge/PRs-welcome-brightgreen.svg)](CONTRIBUTING.md)
[![AI Agent Coded](https://img.shields.io/badge/AI--Agent-100%25-ff6b6b)](CLAUDE.md)
[![Go](https://img.shields.io/badge/Go-1.22+-00ADD8)](ServerGo)
[![React](https://img.shields.io/badge/React-18-61dafb)](ClientWeb)
[![Chinese](https://img.shields.io/badge/lang-中文-red)](README.md) [![English](https://img.shields.io/badge/lang-English-blue)](README.en.md) [![日本語](https://img.shields.io/badge/lang-日本語-green)](README.ja.md)

</div>

---

> **🤖 100% AI Agent 自动编程** —— 无人工手写一行代码，无古法编程。
> 整个项目（后端 Go、前端 React、协议转换层、调度算法、爬虫 MCP、CI 脚本）全部由
> AI Agent 自主编写、测试、重构、部署。本仓库是「Agent 编程」能力在**生产级基础设施工件**
> 上的一次完整展示。

> *一条入口，背后是 N 个模型源站。*
> *Agent 的 Session 在整个任务周期内保持粘性，不会被负载均衡拆得四分五裂；*
> *同一套代理入口同时说 Anthropic 与 OpenAI 两种协议；*
> *源站挂了、余额不足了，请求自动换站重试，上层 Agent 无感知。*

---

## ✨ 核心特性

- **🔀 N 模型负载均衡，不拆分 Agent Session**：一次 Agent 任务动辄几十上百次连续 LLM 调用，
  LsmTokensServer 通过 Session 识别层从请求体解析 `session_id`，把同一会话粘到同一源站，
  在不拆分 Agent Session 任务的前提下实现多源站均衡分摊。
- **🎛️ 四种分配调度机制**：`指定型` / `稳定型` / `经济型` 已实装，`智能型` 规划中（详见下表）。
- **🔁 故障自动转移**：源站 402（余额不足）等账号级错误触发换站重试，请求内排除已失败源站，
  上层 Agent 零感知。
- **🔄 Anthropic ⇄ OpenAI 双向协议转换**：完整请求/响应互转 + SSE 流式转换，
  Claude Code、OpenAI 系客户端均可直连，源站协议自由混配。
- **🖥️ 管理员 / 用户 Web 双构建隔离**：一套源码两套产物（`dist-manager` / `dist-user`），
  Rollup 死代码消除保证用户端产物零管理代码；管理员 JWT 与用户 JWT 双轨鉴权。
- **🔒 生产级安全加固**：bcrypt 密码哈希、JWT 集中密钥管理、管理接口全量中间件保护、
  手机号脱敏、API Key / JWT 密钥 `crypto/rand` 生成、信任代理头白名单。
- **🕷️ 爬虫 CDP + MCP 接口**：Chrome DevTools Protocol 驱动的站点采集能力，通过 MCP（29002）暴露给 Agent。
- **📊 实时统计**：Token 用量、延迟分布、模型分布、Agent 工具调用等多维区间报告，WebSocket 流式推送。

---

## 🎛️ 四种分配调度机制

| 机制 | 策略 | 适用场景 |
|------|------|---------|
| **📌 指定型**（已实装） | 始终使用路由配置的第 1 个源站，失败不自动切换 | 明确指定主力源站 |
| **🛡️ 稳定型**（已实装） | 永远使用队首源站；连续失败 3 次触发滚动切换（队首移到队尾），顺序持久化 | 主备分明、少切换 |
| **💰 经济型**（已实装） | Session 确定性哈希粘性分配（FNV-1a + 启动时间戳），livePool 消费式均衡；服务重启后重新打散；402 触发请求内换源 | 多套餐包均衡摊量、控成本 |
| **🧠 智能型**（规划中） | 根据历史成功率、延迟、价格等多维度评分动态选择最优源站 | 全自动最优调度 |

> 每条 AI 路由可独立配置分配机制与源站列表，实时生效，无需重启。

---

## 🚀 快速启动

### 前置要求

- Go 1.22+、Node.js 18+、MySQL / MariaDB、Linux

### 安装与部署

```bash
# 1. 克隆仓库
git clone <your-repo-url> LsmTokensServer
cd LsmTokensServer

# 2. 运行时配置 —— 首次启动自动生成 LsmTokensServer.conf（含敏感信息，已 gitignore）
#    手动模板：cp LsmTokensServer.conf.example LsmTokensServer.conf
#    设置 MySQL 密码、jwtSecret、managerUserName/managerPassword、模型源站 API Key

# 3. 安装前端依赖
cd ClientWeb && npm install && cd ..

# 4. 一键编译前端（双构建）+ 后端并启动服务
./rebuild_restart_app.sh

# 5. 打开浏览器
# 管理员 Web  http://127.0.0.1:9101
# 用户 Web    http://127.0.0.1:29001
```

### 服务端口规范

| 服务 | 端口 |
|------|------|
| 管理员 Web（REST + 管理端 SPA） | `9101` |
| AI 代理（HTTP） | `29000` |
| 用户 Web（用户端 SPA） | `29001` |
| MCP | `29002` |
| AI 代理（HTTPS） | `29003` |
| 爬虫 CDP | `9222` |

### 客户端接入示例

```bash
# Claude Code / Anthropic 协议客户端
export ANTHROPIC_BASE_URL=http://127.0.0.1:29000
export ANTHROPIC_AUTH_TOKEN=<你的代理 API Key>

# OpenAI 协议客户端
export OPENAI_BASE_URL=http://127.0.0.1:29000/v1
export OPENAI_API_KEY=<你的代理 API Key>
```

---

## ⚙️ 技术栈

| 模块 | 选型 |
|------|------|
| 🚪 后端 | Go（模块名 `github.com/lishimeng/LsmTokensServer`）、Gin、GORM + MySQL/MariaDB、gorilla/websocket、JWT (HS256)、bcrypt、自研日志轮转 |
| 🪟 前端 | React 18 + TypeScript、Vite（`__APP_ROLE__` 构建期角色常量，双产物隔离） |
| 📡 代理协议 | Anthropic Messages ⇄ OpenAI Chat Completions 双向转换 + SSE 流式 |
| 🧠 调度算法 | Session 识别 + 指定型/稳定型/经济型选择器（`ServerGo/models/agent_algorithm*.go`） |

---

## 📁 项目结构

```
ServerGo/                       后端核心（按业务域分包）
├── config/       配置加载
├── logger/       日志轮转
├── database/     DB 基础
├── models/       业务模型 + 路由调度算法（指定/稳定/经济）
├── recognizer/   agent/session/tool 识别
├── protocol/     Anthropic⇄OpenAI 协议转换 + SSE
├── proxy/        AI 代理转发 + 安全限流
├── api/          REST 接口（用户端 + 管理端）
├── spider/       爬虫 CDP + MCP 接口
├── websocket/    WS 推送（ChatTotal 流式）
└── system/       系统辅助
ClientWeb/                      前端（React + Vite，dist-manager / dist-user 双构建）
docs/                           知识库、协议分析、开发指南
python-generate-image-tool/     [本地私有子模块，不入库] AI 图像生成 SDK
go-web-debug-tool/              [本地私有子模块，不入库] Chrome CDP 自动化调试
rebuild_restart_app.sh          一键编译前后端 + 部署 + 运行
ProjectPic/                     项目资源（收款码等）
```

> **关于子模块**：`python-generate-image-tool/` 与 `go-web-debug-tool/` 因涉及 API Key 等
> 敏感信息未开源，仅为本机 Agent 开发辅助工具；不拉取不影响主工程编译运行。

---

## 🔒 安全设计摘要

- 严禁硬编码密钥：JWT 密钥与管理端凭证只放 `LsmTokensServer.conf` 的 `security` 段。
- 管理端所有业务接口经 `ManagerAuthMiddleware` 统一保护。
- 用户密码只存 bcrypt 哈希，接口响应密码置空、手机号脱敏。
- 前端不持久化 API Key；对话历史 localStorage 上限 200 条 + 30 天过期。

完整规范见 [`docs/开发指南/SECURITY.md`](docs/开发指南/SECURITY.md)。

---

## 🤖 Agent 自动编程

本仓库的所有代码均由 AI Agent（Claude Code 等）自动编写：

- **无手工编码**：没有人类程序员手写任何一行 Go / TypeScript / CSS / SQL。
- **自测自修**：Agent 自动运行 `go vet`、`go test ./...`、`npm run build`，发现 Bug 自动定位、修复、回归。
- **自部署**：`rebuild_restart_app.sh` 由 Agent 编写，一键编译 + 部署 + 重启。
- **持续迭代**：CLAUDE.md 中沉淀各阶段规则与教训，后续 Agent 自动加载避免重犯。

**这一切，没有一个人工手写字符。**

---

## 🤝 关注与支持

如果你觉得这个项目有趣，欢迎关注我在各平台的账号：

| 平台 | 搜索账号 |
|------|---------|
| 快手 | **封刀灌海** |
| 抖音 | **封刀灌海** |
| B站 | **封刀灌海** |
| 小红书 | **封刀灌海** |
| 微信视频号 | **封刀灌海** |

---

## ☕ 打赏支持

项目的服务器、LLM API 调用等均有持续成本。如果这个项目对你有帮助或你觉得有趣，欢迎打赏支持：

| 微信打赏 | 支付宝打赏 |
|:--------:|:----------:|
| ![微信收款码](ProjectPic/wechat_qr.jpg) | ![支付宝收款码](ProjectPic/alipay_qr.jpg) |

**联系方式**：

- 📱 手机：`13520647302`
- 💬 微信：`liushimeng109117198`

---

## 📜 协议

本项目以 **MIT License** 开放源代码 —— 详见 [`LICENSE`](LICENSE) 文件。
所有代码由 AI Agent 自动编写，人工 review 后入库。

---

## 🌟 Star / Watch / Fork

如果这个项目让你对「Agent 编程」或「AI Tokens 中转架构」有了新的认识，请：

- ⭐ **Star** 本仓库 —— 让更多人看到 Agent 编程的力量
- 👁️ **Watch** —— 跟进后续迭代（`智能型` 调度已在路上）
- 🍴 **Fork** —— 搭建一套专属你的 AI Tokens 中转服务

本仓库在三平台同步托管：

| 平台 | 链接 |
|------|------|
| GitHub | `https://github.com/<your-org>/LsmTokensServer` |
| Gitee | `https://gitee.com/<your-org>/LsmTokensServer` |
| GitCode | `https://gitcode.com/<your-org>/LsmTokensServer` |

> 💡 一个 ⭐ 比十篇博客更能推动这件事被更多人看见。

**这不是一个人写的代码。这是一群 AI Agent 24 小时不间断编程的作品。**

---

**版本**：v2.0.57  |  **最后更新**：2026-08-25  |  **构建**：Agent 自动构建
