import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vite'

// 阶段T 双构建隔离（docs/项目迁移解决方案/管理员与用户Web服务双构建隔离升级方案_20260825.md）：
// 一套源码，两套产物 —— `--mode manager` → dist-manager（全量，含管理页）；
// `--mode user` → dist-user（用户端，管理页 chunk 经死代码消除不产出）。
// dev 模式（mode=development）默认 manager，便于本地全功能调试。
// __APP_ROLE__ 在代码中经 define 静态替换，禁止运行时嗅探端口判断角色。

const here = dirname(fileURLToPath(import.meta.url))

// 20260826-05 工具栏版本显示：前端版本号单一事实来源为后端 config.APP_VERSION
// （前后端同仓库一版一库），构建期直接从 ServerGo/config/config.go 提取注入；
// 读不到（异常场景）回落 package.json version，绝不 fail build。
function resolveAppVersion() {
  try {
    const go = readFileSync(resolve(here, '../ServerGo/config/config.go'), 'utf8')
    const m = go.match(/APP_VERSION\s*=\s*"([^"]+)"/)
    if (m) return m[1]
  } catch { /* 回落 */ }
  try {
    return JSON.parse(readFileSync(resolve(here, 'package.json'), 'utf8')).version || '0.0.0'
  } catch {
    return '0.0.0'
  }
}

// 前端编译时间，格式与后端 ldflags 注入的 buildTime 统一（YYYY-MM-DD_HH:mm:ss）
function buildTimestamp() {
  const d = new Date()
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}_${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

export default defineConfig(({ mode }) => {
  const role = mode === 'user' ? 'user' : 'manager'
  return {
    plugins: [react()],
    // 相对基路径（v2.0.58 网关代理支持）：产物资源引用 ./assets/...，
    // 支持网关按任意子路径代理托管（如 https://host:8080/ChatAnalysis/）。
    base: './',
    define: {
      __APP_ROLE__: JSON.stringify(role),
      __APP_VERSION__: JSON.stringify(resolveAppVersion()),
      __APP_BUILD_TIME__: JSON.stringify(buildTimestamp()),
    },
    build: { outDir: `dist-${role}` },
  }
})
