# Developer_SOP - LsmTokensServer 开发标准操作规程（开源版）

> 完整 SOP 见 [`docs/开发指南/Developer_SOP.md`](docs/开发指南/Developer_SOP.md)。
> 本文件为根目录快速入口，覆盖新工程最常用的操作。

## 环境要求

- Go 1.26+
- Node 20+ / npm 10+
- MySQL 5.7+（可选，纯代理可关闭）
- Chrome / Chromium（爬虫模块需要）

## 日常命令

```bash
# 编译（迁移期默认）
./scripts/rebuild_restart_app.sh --build-only

# 仅后端
./scripts/rebuild_restart_app.sh --build-only --skip-web

# 运行后端测试
cd ServerGo && go test ./... && go vet ./...

# 前端开发
cd ClientWeb && npm run dev      # 开发服务器
npm run build                    # 生产构建

# 数据库迁移 / 表初始化（有 MySQL 时）
# 首次启动会自动建表（见 ServerGo/main.go）
```

## 配置

1. 复制模板：`cp LsmTokensServer.conf.example LsmTokensServer.conf`
2. 填写实际 MySQL 账号、端口、openClaw key 等
3. **切勿提交到 git**（已在 `.gitignore`）

## 目录结构

```
ServerGo/    后端（config/logger/database/models/proxy/protocol/api/spider/websocket/system/webserver）
ClientWeb/   前端（React + Vite）
docs/        文档库
scripts/     编译部署脚本
tools/       本地私有工具（git 忽略）
```

## 提交规范

- 中文 commit message
- 每阶段一提交，保持可回滚
- 禁止提交敏感信息（配置、证书、日志、二进制）

## 详细文档

- 迁移方案：`docs/项目迁移解决方案/`
- 开发指南：`docs/开发指南/`
- AI Agent 入口：`AGENTS.md` / `CLAUDE.md`
- 知识库索引：`docs/INDEX.md`
