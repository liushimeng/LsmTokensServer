// 全局键盘快捷键 hook（docs/项目迁移解决方案/管理员与用户Web侧边菜单与顶部工具栏通用组件折叠展开方案_20260912.md §2.6）
//
// 用法：
//   useKeyboardShortcut('[', () => toggleSidebar(), { enabled: !isMobile })
//   useKeyboardShortcut('\\', () => toggleToolbarMore())
//
// 行为约束：
// - 输入框、可编辑元素（input/textarea/[contenteditable]）聚焦时**自动禁用**，避免误触。
// - 按住修饰键（Ctrl/Cmd/Alt）时不响应（防止与浏览器/系统快捷键冲突）。
// - 重复按键（auto-repeat）只触发一次，依赖浏览器原生 keydown single-fire 行为。
// - 卸载时自动 removeEventListener，无内存泄漏。

import { useEffect } from 'react'

// 输入元素检测：原生 input/textarea/select + contenteditable
function isTypingTarget(el) {
  if (!el) return false
  const tag = (el.tagName || '').toUpperCase()
  if (tag === 'INPUT' || tag === 'TEXTAREA' || tag === 'SELECT') return true
  if (el.isContentEditable) return true
  return false
}

// 全局 enabled 开关（用户可在 localStorage 关闭，详见 navConfig.shortcutsEnabled）
function readShortcutsEnabled() {
  try {
    if (window.localStorage.getItem('lsm.layout.shortcutsEnabled') === '0') return false
  } catch { /* 隐私模式 */ }
  return true
}

/**
 * 注册一个全局键盘快捷键。
 * @param {string} key - 目标键（如 '['、']'、'\\'、'?'），区分大小写
 * @param {(e: KeyboardEvent) => void} handler - 触发回调
 * @param {{ enabled?: boolean, allowInInputs?: boolean }} [opts]
 */
export function useKeyboardShortcut(key, handler, opts = {}) {
  const { enabled = true, allowInInputs = false } = opts

  useEffect(() => {
    if (!enabled || typeof handler !== 'function') return undefined

    const onKeyDown = (e) => {
      // 修饰键（Ctrl/Cmd/Alt）按下时不响应，避免与系统/扩展冲突
      if (e.ctrlKey || e.metaKey || e.altKey) return
      // 全局开关关闭
      if (!readShortcutsEnabled()) return
      // 输入框聚焦时禁用（除非显式 allowInInputs）
      if (!allowInInputs && isTypingTarget(e.target)) return
      // 键名匹配（key 区分大小写，需传入大写或与 KeyboardEvent.key 一致的字面量）
      if (e.key !== key) return
      // 阻止默认行为（[、]、\ 等在某些场景有系统语义）
      e.preventDefault()
      handler(e)
    }

    window.addEventListener('keydown', onKeyDown)
    return () => window.removeEventListener('keydown', onKeyDown)
  }, [key, handler, enabled, allowInInputs])
}
