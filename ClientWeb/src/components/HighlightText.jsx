// ClientWeb/src/components/HighlightText.jsx
//
// 只读高亮文本组件：把文本按关键词（大小写不敏感）切分，匹配段渲染为 <mark>。
// 2026-08-27 对话分析记录查看器升级：查找遍历模式专用渲染器。
//
// 特性：
//   - 当前匹配项加 .mark-active 样式并自动滚动到可视区中央；
//   - 通过 onCount(n) 上报匹配总数（供 SearchBar 显示 i/N）；
//   - 只读：<pre> 渲染，可选择、可复制，不可编辑。

import { useEffect, useMemo, useRef } from 'react'

/**
 * @param {object} props
 * @param {string} props.text        全文文本
 * @param {string} props.query       查找关键词（空串时调用方不应使用本组件）
 * @param {number} props.activeIndex 当前匹配序号（0 基）
 * @param {(n:number)=>void} [props.onCount] 匹配总数上报
 */
export default function HighlightText({ text, query, activeIndex, onCount }) {
  const activeRef = useRef(null)

  // 切分文本为 普通段/匹配段 序列
  const parts = useMemo(() => {
    const src = text || ''
    const q = (query || '').toLowerCase()
    if (!q) return [{ match: false, str: src }]
    const out = []
    let i = 0
    const lower = src.toLowerCase()
    while (i < src.length) {
      const hit = lower.indexOf(q, i)
      if (hit === -1) { out.push({ match: false, str: src.slice(i) }); break }
      if (hit > i) out.push({ match: false, str: src.slice(i, hit) })
      out.push({ match: true, str: src.slice(hit, hit + q.length) })
      i = hit + q.length
    }
    return out
  }, [text, query])

  const matchCount = useMemo(() => parts.filter((p) => p.match).length, [parts])

  // 上报匹配总数
  useEffect(() => { if (onCount) onCount(matchCount) }, [matchCount, onCount])

  // 当前匹配滚动到可视区
  useEffect(() => {
    if (activeRef.current) {
      activeRef.current.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
  }, [activeIndex, matchCount, query])

  let matchSeq = -1
  return (
    <pre className="log-box detail-content highlight-text">
      {parts.map((p, idx) => {
        if (!p.match) return <span key={idx}>{p.str}</span>
        matchSeq++
        const isActive = matchSeq === activeIndex
        return (
          <mark key={idx} ref={isActive ? activeRef : null} className={isActive ? 'mark-active' : ''}>
            {p.str}
          </mark>
        )
      })}
    </pre>
  )
}
