// ClientWeb/src/components/HighlightText.jsx
//
// 只读高亮文本组件：把文本按关键词（大小写不敏感）切分，匹配段渲染为 <mark>。
// 2026-08-27 对话分析记录查看器升级：查找遍历模式专用渲染器。
// 2026-08-28 阶段AU：匹配计数 / 当前项标记 / 焦点滚动统一收敛到 InlineDetailRow
// 的 DOM 机制（querySelectorAll('mark.sm-mark')，兼容嵌套局部状态变化），
// 本组件退化为纯渲染：mark 统一使用 sm-mark 类名，不再内部计数与滚动。
//
// 特性：
//   - 只读：<pre> 渲染，可选择、可复制，不可编辑；
//   - 与 SearchText 共用 sm-mark 类名体系，供上层 DOM 机制统一发现。

/**
 * @param {object} props
 * @param {string} props.text  全文文本
 * @param {string} props.query 查找关键词（空串时不高亮）
 */
export default function HighlightText({ text, query }) {
  const src = text || ''
  const q = (query || '').toLowerCase()
  if (!q) {
    return <pre className="log-box detail-content highlight-text">{src}</pre>
  }

  const lower = src.toLowerCase()
  const parts = []
  let i = 0
  while (i <= src.length) {
    const hit = lower.indexOf(q, i)
    if (hit === -1) {
      parts.push({ match: false, str: src.slice(i) })
      break
    }
    if (hit > i) parts.push({ match: false, str: src.slice(i, hit) })
    parts.push({ match: true, str: src.slice(hit, hit + q.length) })
    i = hit + q.length
  }

  return (
    <pre className="log-box detail-content highlight-text">
      {parts.map((p, idx) =>
        p.match ? <mark key={idx} className="sm-mark">{p.str}</mark> : <span key={idx}>{p.str}</span>,
      )}
    </pre>
  )
}
