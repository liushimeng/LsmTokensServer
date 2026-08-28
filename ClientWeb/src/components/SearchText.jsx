// ClientWeb/src/components/SearchText.jsx
//
// 查找高亮哑组件（阶段AU）：把一段文本按关键词（大小写不敏感）切分，
// 匹配段渲染为 <mark className="sm-mark">。
//
// 设计要点：
//   - 只负责渲染，不做计数 / 不做当前项标记 / 不滚动 —— 这些统一由
//     InlineDetailRow 的 DOM 机制（querySelectorAll('mark.sm-mark')）完成，
//     天然兼容 JsonTree 节点折叠、SSE 卡片折叠等嵌套局部状态变化；
//   - query 为空时原样输出，零开销；
//   - 与 HighlightText 的区别：本组件用于"视图布局内的文本片段"（JSON 树的
//     key/值、SSE 事件名、聚合标签等），HighlightText 用于整段纯文本视图。

/**
 * @param {object} props
 * @param {string} props.text       要渲染的文本片段
 * @param {string} [props.query]    查找关键词（空串时不高亮）
 * @param {string} [props.className] 包裹 span 的附加类名
 */
export default function SearchText({ text, query, className }) {
  const src = text == null ? '' : String(text)
  const q = (query || '').toLowerCase()
  if (!q) return <span className={className}>{src}</span>

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
    <span className={className}>
      {parts.map((p, idx) =>
        p.match ? <mark key={idx} className="sm-mark">{p.str}</mark> : <span key={idx}>{p.str}</span>,
      )}
    </span>
  )
}
