# LsmTokensServer

> 开源 AI Token 代理与管理服务 —— 由私有项目 LsmHttpAgent 迁移重构而来，前后端分离架构。
> 迁移设计与进度：[docs/项目迁移解决方案/00-总体迁移方案.md](docs/项目迁移解决方案/00-总体迁移方案.md)

## 架构

```
LsmTokensServer/
├── ServerGo/     后端（Go，按基础/业务模块分包）
├── ClientWeb/    前端（React + Vite）
├── docs/         知识库与迁移文档
├── scripts/      编译/部署/重启脚本
└── LsmTokensServer.conf.example  配置模板（实际配置 git 忽略）
```

## 快速开始

```bash
# 编译前后端（迁移期默认 build-only，不启动不占端口）
./scripts/rebuild_restart_app.sh --build-only

# 仅编译后端
./scripts/rebuild_restart_app.sh --build-only --skip-web

# 全量启动（端口与旧程序相同，切换前请先停止旧服务）
./scripts/rebuild_restart_app.sh
```

## 开发指引

- 迁移进度与阶段计划见 [docs/项目迁移解决方案/](docs/项目迁移解决方案/)
- 旧项目知识库见 [docs/开发指南/](docs/开发指南/)、[docs/协议分析/](docs/协议分析/)、[docs/mcp/](docs/mcp/)

## License

待补充（开源）
