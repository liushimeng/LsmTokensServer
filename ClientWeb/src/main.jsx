import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import './index.css'
import App from './App.jsx'

// ── 全局错误监听：捕获 chunk 加载失败等未处理异常 ──────────────────────
// 服务重启后旧构建产物 hash 变化，浏览器缓存的旧 chunk 文件 404，
// 导致动态 import() 失败。通过全局监听检测并自动刷新页面恢复。
;(function installGlobalErrorGuards() {
  // 防止死循环：sessionStorage 标记，刷新后仅自动 reload 一次
  const RELOAD_KEY = 'lsm_chunk_reload_done'
  const isChunkLoadError = (message) => {
    if (!message) return false
    return /Loading chunk|Failed to fetch|error loading dynamically imported module|Importing a module script failed|chunk.*404/i.test(message)
  }
  const tryAutoReload = (reason) => {
    if (sessionStorage.getItem(RELOAD_KEY)) return // 已自动刷新过一次，不再重复
    sessionStorage.setItem(RELOAD_KEY, '1')
    console.warn('[全局错误] 检测到页面资源加载失败，自动刷新:', reason)
    window.location.reload()
  }

  // 未处理的 Promise 拒绝（含动态 import 失败）
  window.addEventListener('unhandledrejection', (event) => {
    const reason = event.reason
    const msg = reason && reason.message ? reason.message : String(reason || '')
    if (isChunkLoadError(msg)) {
      event.preventDefault() // 防止控制台报错（可选）
      tryAutoReload(msg)
    }
  })

  // 运行时错误（脚本加载失败、语法错误等）
  window.addEventListener('error', (event) => {
    const msg = event.message || ''
    if (isChunkLoadError(msg)) {
      event.preventDefault()
      tryAutoReload(msg)
    }
  }, true)

  // 页面卸载时清除标记（正常导航后下次可再次自动刷新）
  window.addEventListener('beforeunload', () => {
    // 不立即清除：reload 后 sessionStorage 仍在，用于防止死循环
    // 用户正常关闭页面后下次打开是新的 sessionStorage
  })
})()

createRoot(document.getElementById('root')).render(
  <StrictMode>
    <App />
  </StrictMode>,
)
