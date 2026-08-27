// ClientWeb/src/shared/clipboard.js
//
// 跨场景文本复制工具：
//   - 优先使用 navigator.clipboard.writeText（异步、剪贴板写入权限受用户代理控制）；
//   - 失败兜底：动态创建隐藏 <textarea> + document.execCommand('copy')，
//     兼容非安全上下文（http、iframe、权限被拒等场景）；
//   - 大文本（数十万字符）也能稳定复制。
//
// 2026-08-27 阶段AR：新增；用于支持 Request Body 等超大字段的所见即所得复制。

/**
 * 复制文本到剪贴板；返回 Promise<boolean> 表示是否成功。
 *
 * @param {string} text
 * @returns {Promise<boolean>}
 */
export async function copyToClipboard(text) {
  const safe = text == null ? '' : String(text)
  // 1) 优先 navigator.clipboard
  if (typeof navigator !== 'undefined' && navigator.clipboard && typeof navigator.clipboard.writeText === 'function') {
    try {
      await navigator.clipboard.writeText(safe)
      return true
    } catch {
      // 权限被拒或非安全上下文 → 走兜底
    }
  }
  // 2) 兜底：textarea + execCommand
  try {
    const ta = document.createElement('textarea')
    ta.value = safe
    ta.setAttribute('readonly', '')
    ta.style.position = 'fixed'
    ta.style.top = '0'
    ta.style.left = '0'
    ta.style.opacity = '0'
    ta.style.pointerEvents = 'none'
    document.body.appendChild(ta)
    // 选中文本
    const prevActive = document.activeElement
    ta.focus()
    ta.select()
    ta.setSelectionRange(0, ta.value.length)
    let ok = false
    try {
      ok = document.execCommand && document.execCommand('copy')
    } catch {
      ok = false
    }
    document.body.removeChild(ta)
    if (prevActive && typeof prevActive.focus === 'function') {
      try { prevActive.focus() } catch { /* ignore */ }
    }
    return ok
  } catch {
    return false
  }
}