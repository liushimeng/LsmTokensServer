// 通用可折叠列表组件：用于「目标源站列表」等长标签序列
// mode='single': 折叠态，仅显示首条 + 数量，鼠标悬停 tooltip 显示全部
// mode='multi' : 展开态，多行完整展示（maxLines 限高滚动）
import { useState, useRef } from 'react'

export default function CollapsibleList({ items, mode = 'multi', maxLines = 6, showIndex = true }) {
  const [tooltip, setTooltip] = useState(false)
  const ref = useRef(null)
  if (!items || !items.length) return <span style={{ color: '#999', fontStyle: 'italic' }}>-</span>

  if (mode === 'single') {
    const first = items[0]
    const restCount = items.length - 1
    // 原生 title tooltip（换行拼接全部）
    const allText = items.map((it, i) => `${i + 1}. ${it.text}`).join('\n')
    return (
      <span className="cl-single" title={allText}
        ref={ref}
        onMouseEnter={() => setTooltip(true)}
        onMouseLeave={() => setTooltip(false)}>
        <span className={'ep-chip' + (first.off ? ' ep-chip-off' : '')}>
          {showIndex ? `1. ` : ''}{first.text}
        </span>
        {restCount > 0 ? <span className="cl-more">+{restCount}</span> : null}
        {tooltip ? (
          <span className="cl-tooltip" style={tooltipStyle(ref)}>
            {items.map((it, i) => (
              <span key={it.id + '-' + i} className={'cl-tooltip-item' + (it.off ? ' cl-tooltip-item-off' : '')}>
                {i + 1}. {it.text}
              </span>
            ))}
          </span>
        ) : null}
      </span>
    )
  }

  // mode === 'multi'
  return (
    <div className="cl-multi" style={{ maxLines }}>
      <div className="chip-list">
        {items.map((it, i) => (
          <span key={it.id + '-' + i} className={'ep-chip' + (it.off ? ' ep-chip-off' : '')}>
            {showIndex ? `${i + 1}. ` : ''}{it.text}
          </span>
        ))}
      </div>
    </div>
  )
}

// tooltip 定位：相对父级（cl-single）上方居中
function tooltipStyle(ref) {
  // 默认样式；显示位置依赖父级 position:relative（CSS 已设置）
  return {}
}
