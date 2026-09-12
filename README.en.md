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

## 🔀 Open-Source AI Tokens Proxy · N-Model Load Balancing · Agent-Session-Preserving

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

> **🤖 100% AI-Agent-coded** — not a single line hand-written by a human.
> The entire project (Go backend, React frontend, protocol conversion layer, scheduling
> algorithms, crawler MCP, CI scripts) was written, tested, refactored and deployed
> autonomously by AI Agents. This repo is a full demonstration of "agent coding" on a
> **production-grade infrastructure project**.

> *One endpoint, N upstream model providers behind it.*
> *An Agent session stays sticky to one upstream for its whole task lifetime — load balancing never shreds it.*
> *The same proxy speaks both Anthropic and OpenAI protocols.*
> *If an upstream dies or runs out of balance (402), requests retry on another upstream — invisible to the agent above.*

---

## ✨ Key Features

- **🔀 N-model load balancing without splitting Agent sessions**: a single agent task easily spans hundreds of consecutive LLM calls. The session-recognition layer parses `session_id` from the request body and pins each session to one upstream, spreading load across providers safely.
- **🎛️ Four scheduling strategies**: `Pinned` / `Stable` / `Economic` implemented, `Smart` planned (see table below).
- **🔁 Automatic failover**: account-level upstream errors such as 402 (out of balance) trigger in-request retry on a different upstream — zero impact on the calling agent.
- **🔄 Bidirectional Anthropic ⇄ OpenAI protocol conversion**: full request/response conversion plus SSE streaming; Claude Code and OpenAI-style clients connect directly, upstream protocols can be freely mixed.
- **🖥️ Manager/User web dual-build isolation**: one codebase, two artifacts (`dist-manager` / `dist-user`); Rollup dead-code elimination guarantees zero admin code in the user artifact; separate manager and user JWT tracks.
- **🔒 Production-grade security**: bcrypt password hashing, centralized JWT secrets, blanket auth middleware on admin APIs, phone-number masking, `crypto/rand` key generation, trusted-proxy whitelist.
- **🕷️ Crawler CDP + MCP interface**: Chrome DevTools Protocol-driven web collection exposed to agents via MCP (port 29002).
- **📊 Real-time analytics**: token usage, latency/model distributions, agent tool-call reports over date ranges, streamed over WebSocket.

---

## 🎛️ Scheduling Strategies

| Strategy | Behavior | Best for |
|----------|----------|----------|
| **📌 Pinned** (implemented) | Always use the first configured upstream; no auto switch | A designated primary upstream |
| **🛡️ Stable** (implemented) | Use the head of the list; after 3 consecutive failures rotate it to the tail (order persisted) | Clear primary/backup setups |
| **💰 Economic** (implemented) | Deterministic FNV-1a session-hash stickiness + livePool consumption; reshuffled on restart; 402 triggers in-request upstream switch | Balancing quota packages / cost control |
| **🧠 Smart** (planned) | Multi-dimensional scoring on success rate, latency, price | Fully automatic optimal scheduling |

> Each AI route configures its own strategy and upstream list, effective immediately without restart.

---

## 🚀 Quick Start

### Prerequisites

- Go 1.22+, Node.js 18+, MySQL / MariaDB, Linux

### Install & Deploy

```bash
# 1. Clone
git clone <your-repo-url> LsmTokensServer
cd LsmTokensServer

# 2. Runtime config — auto-generated on first start (gitignored; contains secrets)
#    Template: cp LsmTokensServer.conf.example LsmTokensServer.conf
#    Set MySQL password, jwtSecret, managerUserName/managerPassword, upstream API keys

# 3. Frontend dependencies
cd ClientWeb && npm install && cd ..

# 4. Build frontend (dual build) + backend and start
./rebuild_restart_app.sh

# 5. Open in browser
# Manager Web  http://127.0.0.1:9101
# User Web     http://127.0.0.1:29001
```

### 🚀 First-Run Setup

Since v2.0.74, **zero-config first start**:

1. Just run `./rebuild_restart_app.sh` to start the service.
2. **No need to manually `cp .conf.example`** — when `LsmTokensServer.conf` is missing it is generated automatically.
3. The startup log and stdout print a `[FIRST-RUN]` summary block, including:
   - Auto-generated `managerUserName` (e.g. `adm-7kq3m9xp`);
   - Auto-generated `managerPassword` (16-char base62);
   - Auto-generated `jwtSecret` (32-byte base64).
4. Open `http://127.0.0.1:9101/ManagerLogin` and log in with the credentials above.
5. **Change the default password immediately after logging in.**

> ⚠️ The password is printed to stdout **only once** during this startup. It is **never** written to any `*.example` file, and never committed to git. Save it carefully. If lost, edit `LsmTokensServer.conf` (`security.managerUserName/managerPassword`) and restart.

### 🔒 Super-Admin Auto-Disable

When the service detects that MySQL **already has business users** (`TAgentHttpUserInfo` with `deleted_at IS NULL` and `count >= 1`), it automatically:

- Rewrites `security.managerUserName` / `managerPassword` to `disable`;
- Sets `managerWebAuthDisabled = true`;
- Rejects every manager-side business API (`/UserManageInterface`, `/AIRouteInterface`, …);
- Keeps `/ManagerLogin` reachable, but the login request itself is rejected with the message "Manager super-admin has been disabled".

This is **a one-way operation**: after being disabled it will not re-enable itself when the user table is later emptied. To re-enable, edit `LsmTokensServer.conf` to reset `managerUserName/managerPassword` to non-`disable` values and restart the service.

### ❓ FAQ

| Question | Answer |
|---|---|
| **Can't find the super-admin password?** | The startup stdout's `[FIRST-RUN]` block printed it. If lost, edit `LsmTokensServer.conf` → `security.managerPassword` and restart. |
| **Manager login says "disabled"?** | Database already has business users → service auto-disabled super-admin. To re-enable, edit conf to set `managerUserName/managerPassword` to non-`disable` values and restart. |
| **Want to customize MySQL credentials?** | Edit `LsmTokensServer.conf` → `DBMysql.User/Pwd` and restart; `validateAndFixConfig` validates automatically. |

### Ports

| Service | Port |
|---------|------|
| Manager Web (REST + admin SPA) | `9101` |
| AI proxy (HTTP) | `29000` |
| User Web (user SPA) | `29001` |
| MCP | `29002` |
| AI proxy (HTTPS) | `29003` |
| Crawler CDP | `9222` |

### Client example

```bash
# Claude Code / Anthropic-protocol clients
export ANTHROPIC_BASE_URL=http://127.0.0.1:29000
export ANTHROPIC_AUTH_TOKEN=<your-proxy-api-key>

# OpenAI-protocol clients
export OPENAI_BASE_URL=http://127.0.0.1:29000/v1
export OPENAI_API_KEY=<your-proxy-api-key>
```

---

## ⚙️ Tech Stack

| Module | Choice |
|--------|--------|
| 🚪 Backend | Go (module `github.com/lishimeng/LsmTokensServer`), Gin, GORM + MySQL/MariaDB, gorilla/websocket, JWT (HS256), bcrypt, in-house log rotation |
| 🪟 Frontend | React 18 + TypeScript, Vite (`__APP_ROLE__` build-time role constant, dual artifacts) |
| 📡 Proxy protocol | Anthropic Messages ⇄ OpenAI Chat Completions bidirectional conversion + SSE |
| 🧠 Scheduling | Session recognition + Pinned/Stable/Economic selectors (`ServerGo/models/agent_algorithm*.go`) |

---

## 📸 Screenshots

> The screenshots below are captured from a live production instance. Passwords, API keys, and full phone numbers have been redacted (`135****7302`, `sk-xxxx****`). All pages were collected by `go-web-debug-tool` — a Chrome DevTools Protocol automation harness — without any manual interaction.

### Manager Web (Super-admin view)

| Login | User Management |
|:------:|:--------:|
| ![Manager Login](docs/screenshots/manager/M01-login.png) | ![User Management](docs/screenshots/manager/M03-user-manage-full.png) |

| Route Management (top) | Route Management (all 23 routes) |
|:--------:|:----------:|
| ![Route Mgmt](docs/screenshots/manager/M04-route-manage.png) | ![Route Mgmt Full](docs/screenshots/manager/M04-route-manage-full.png) |

| Model stats | Agent stats |
|:--------:|:----------:|
| ![Model Info](docs/screenshots/manager/M06-model-info.png) | ![Agent Info](docs/screenshots/manager/M07-agent-info.png) |

| Model stats (trend + ranking) | Agent stats (trend + ranking) |
|:--------:|:----------:|
| ![Model Info Full](docs/screenshots/manager/M06-model-info-full.png) | ![Agent Info Full](docs/screenshots/manager/M07-agent-info-full.png) |

| Spider Sources | Cleanup Report |
|:--------:|:----------:|
| ![Spider Sources](docs/screenshots/manager/M09-spider-data-source.png) | ![Cleanup Report](docs/screenshots/manager/M11-cleanup-report.png) |

| Cleanup Report (trend + sub-table capacity) | Chat Details (row-expand) |
|:--------:|:----------:|
| ![Cleanup Full](docs/screenshots/manager/M11-cleanup-report-full.png) | ![Chat Dialog](docs/screenshots/manager/M16-chat-dialog.png) |

### User Web (business-user view)

| User Login (Model / User dual tabs) | User Home (21 model cards) |
|:--------:|:----------:|
| ![User Login](docs/screenshots/user/U01-login.png) | ![User Home](docs/screenshots/user/U02-home.png) |

| Chat page (System Prompt + API config) |
|:--------:|
| ![User Chat](docs/screenshots/user/U03-chat-dialog.png) |

---

## 📖 Manager Web Walkthrough

### Step 1 · Log in (super-admin or business user)

```text
1. Visit http://127.0.0.1:9101/ManagerLogin
2. Enter username + password + captcha
3. Click "Login"
4. (First login) Change the default password — since v2.0.74 the first startup auto-generates one to stdout
```

![Manager Login](docs/screenshots/manager/M01-login.png)

> **Security**: super-admin credentials live only in `LsmTokensServer.conf → security.managerUserName/managerPassword` and never touch the database. User passwords are stored as `bcrypt` hashes only. API responses blank the password field and mask phone numbers with `api.MaskPhone`. Full policy: [`docs/开发指南/SECURITY.md`](docs/开发指南/SECURITY.md).

### Step 2 · User Management (CRUD + enable/disable)

```text
1. Sidebar → Users & Routes → User Management
2. "+ Add User" to create a business user (name / password / phone / Anthropic / OpenAI toggles)
3. Inline "Disable / Edit / Delete / View Models"
4. "Refresh" to reload the list
```

![User Management](docs/screenshots/manager/M03-user-manage-full.png)

### Step 3 · Configure AI Route (protocol / algorithm / upstreams)

```text
1. Sidebar → Route Management
2. "+ Add Route": pick protocol (Anthropic / OpenAI), algorithm (Pinned / Stable / Economic), add upstreams
3. "Edit Route" to reorder upstreams, set API key, status
4. "Chat Analysis" jumps to per-route conversation drilldown
```

Four scheduling algorithms are supported:

| Algorithm | Use case |
|------|---------|
| 📌 **Pinned** | Force one specific upstream |
| 🛡️ **Stable** | Rotate after 3 consecutive failures |
| 💰 **Economic** | Multi-package cost balancing (session-hash stickiness) |
| 🧠 **Smart** (planned) | Multi-dimension scoring on success / latency / price |

![Route Management](docs/screenshots/manager/M04-route-manage-full.png)

### Step 4 · Monitoring & Analytics (Model · Agent · Cleanup)

```text
1. Sidebar → Models & Proxy → Model Info / Agent Info
2. Top filters: user, model, time range
3. Trend chart supports wheel / Shift+wheel / drag-to-brush / double-click reset
4. Lower half shows "Token usage ranking" and "Call count ranking" with share bars
```

| Model Info | Agent Info |
|:----------:|:----------:|
| ![Model Info](docs/screenshots/manager/M06-model-info-full.png) | ![Agent Info](docs/screenshots/manager/M07-agent-info-full.png) |

> 🧹 **Retention**: default 15-day conversation retention. The Cleanup Report page shows historical cleanup volumes, tokens recovered, and sub-table capacity monitoring.

---

## 🚀 User Web Walkthrough

### Step 1 · Log in (two ways)

```text
1. Visit https://127.0.0.1:29001/
2. Model Login tab: model name + API Key + captcha
   User  Login tab: username + password + phone + captcha
3. "Login" → redirect to Home
```

![User Login](docs/screenshots/user/U01-login.png)

### Step 2 · Home: model cards

```text
1. Top bar shows current user, current model, total model count
2. Each model card shows the masked API key (first 8 chars) + 6 quick links:
   Chat Details / Summary Statistics / Session Analysis / Task Analysis / Chat / Route Management
3. Click "Chat" to enter the conversation page
```

![User Home](docs/screenshots/user/U02-home.png)

### Step 3 · Start a conversation

```text
1. Click "Chat" on any model card → ChatDialog page
2. Top section shows Model / Protocol / API Key / Proxy URL (redacted)
3. Edit System Prompt and User Message, click "Send"
4. Stream the model response
```

![User Chat](docs/screenshots/user/U03-chat-dialog.png)

---

## 📁 Project Layout

```
ServerGo/                       Backend core (domain-packaged)
├── config/       config loading
├── logger/       log rotation
├── database/     DB foundation
├── models/       business models + scheduling algorithms
├── recognizer/   agent/session/tool recognition
├── protocol/     Anthropic⇄OpenAI conversion + SSE
├── proxy/        AI proxy forwarding + rate limiting
├── api/          REST APIs (user + manager)
├── spider/       crawler CDP + MCP interface
├── websocket/    WS push (streaming ChatTotal)
└── system/       system helpers
ClientWeb/                      Frontend (React + Vite, dist-manager / dist-user dual build)
docs/                           knowledge base, protocol analysis, dev guides
python-generate-image-tool/     [local private submodule, not in repo] AI image generation SDK
go-web-debug-tool/              [local private submodule, not in repo] Chrome CDP debugging
rebuild_restart_app.sh          one-shot build + deploy + restart
ProjectPic/                     project assets (donation QR codes, etc.)
```

> **Submodules**: `python-generate-image-tool/` and `go-web-debug-tool/` are not open-sourced
> (they contain API keys); the main project builds and runs without them.

---

## 🔒 Security Highlights

- No hardcoded secrets: JWT secret and manager credentials live only in the `security` section of `LsmTokensServer.conf`.
- All manager business APIs are protected by `ManagerAuthMiddleware`.
- User passwords stored as bcrypt hashes only; responses blank passwords and mask phone numbers.
- The frontend never persists API keys; chat history capped at 200 entries / 30 days in localStorage.

Full policy: [`docs/开发指南/SECURITY.md`](docs/开发指南/SECURITY.md).

---

## 🤖 Agent-Coded Engineering

Every line in this repo was written by AI Agents (Claude Code et al.):

- **Zero hand-coding**: no human wrote a single line of Go / TypeScript / CSS / SQL.
- **Self-testing & self-fixing**: agents run `go vet`, `go test ./...`, `npm run build` and fix what breaks.
- **Self-deploying**: `rebuild_restart_app.sh` was agent-written.
- **Continuous iteration**: rules and lessons accumulate in CLAUDE.md for later agents to load.

---

## 🤝 Follow & Support

Find me on:

| Platform | Account |
|----------|---------|
| Kuaishou | **封刀灌海** |
| Douyin | **封刀灌海** |
| Bilibili | **封刀灌海** |
| Xiaohongshu | **封刀灌海** |
| WeChat Channels | **封刀灌海** |

---

## ☕ Donate

Servers and LLM API calls cost real money. If this project helps you, consider a donation:

| WeChat | Alipay |
|:------:|:------:|
| ![WeChat QR](ProjectPic/wechat_qr.jpg) | ![Alipay QR](ProjectPic/alipay_qr.jpg) |

**Contact**:

- 📱 Phone: `13520647302`
- 💬 WeChat: `liushimeng109117198`

---

## 📜 License

Released under the **MIT License** — see [`LICENSE`](LICENSE).
All code is AI-Agent-written, human-reviewed before commit.

---

## 🌟 Star / Watch / Fork

If this project changes how you think about agent coding or AI tokens proxies:

- ⭐ **Star** this repo
- 👁️ **Watch** for updates (the `Smart` scheduler is on the roadmap)
- 🍴 **Fork** to build your own AI tokens relay

Hosted in parallel on GitHub / Gitee / GitCode — see links in [README.md](README.md).

> 💡 One ⭐ spreads this further than ten blog posts.

**No human wrote this code. It is the work of AI Agents coding around the clock.**

---

**Version**: v2.0.57  |  **Last updated**: 2026-08-25  |  **Build**: Agent-built
