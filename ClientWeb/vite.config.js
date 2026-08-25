import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// 阶段T 双构建隔离（docs/项目迁移解决方案/管理员与用户Web服务双构建隔离升级方案_20260825.md）：
// 一套源码，两套产物 —— `--mode manager` → dist-manager（全量，含管理页）；
// `--mode user` → dist-user（用户端，管理页 chunk 经死代码消除不产出）。
// dev 模式（mode=development）默认 manager，便于本地全功能调试。
// __APP_ROLE__ 在代码中经 define 静态替换，禁止运行时嗅探端口判断角色。
export default defineConfig(({ mode }) => {
  const role = mode === 'user' ? 'user' : 'manager'
  return {
    plugins: [react()],
    // 相对基路径（v2.0.58 网关代理支持）：产物资源引用 ./assets/...，
    // 支持网关按任意子路径代理托管（如 https://host:8080/ChatAnalysis/）。
    base: './',
    define: { __APP_ROLE__: JSON.stringify(role) },
    build: { outDir: `dist-${role}` },
  }
})
