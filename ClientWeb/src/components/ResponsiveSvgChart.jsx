// ResponsiveSvgChart：通用 SVG 图表自适应宽度容器
// 工程惯例：不引入第三方图表库，纯 SVG 实现。
//
// 设计目标：
//   1. 将「宽度自适应」逻辑从具体图表组件中抽取为通用容器
//   2. 通过 render props 模式向子组件注入实测宽度
//   3. 首帧同步测量 + ResizeObserver 实时跟随
//   4. 任何需要宽度自适应的 SVG 图表都可复用
//
// props:
//   minWidth?: 最小宽度保护（默认 160）
//   height?: 容器高度提示（仅用于样式，默认 280）
//   children: render props 函数 (measuredWidth) => ReactNode
//   onWidthChange?: 宽度变化回调
//   className / style: 透传到容器 div
import { useEffect, useRef, useState } from 'react'

const DEFAULT_WIDTH = 720  // 默认宽度，首帧同步测量前兜底
const DEFAULT_HEIGHT = 280
const DEFAULT_MIN_WIDTH = 160

export default function ResponsiveSvgChart({
  minWidth = DEFAULT_MIN_WIDTH,
  height = DEFAULT_HEIGHT,
  children,
  onWidthChange,
  className,
  style,
}) {
  const containerRef = useRef(null)
  const [width, setWidth] = useState(DEFAULT_WIDTH)
  const onWidthChangeRef = useRef(onWidthChange)
  useEffect(() => { onWidthChangeRef.current = onWidthChange }, [onWidthChange])

  useEffect(() => {
    const el = containerRef.current
    if (!el) return

    // 立即同步一次初始宽度，避免首帧用 DEFAULT_WIDTH 兜底
    const initW = el.clientWidth
    if (initW > 0) {
      const clampedW = Math.max(minWidth, initW)
      setWidth(clampedW)
      onWidthChangeRef.current?.(clampedW)
    }

    const ro = new ResizeObserver((entries) => {
      const w = entries[0].contentRect.width
      if (w > 0) {
        const clampedW = Math.max(minWidth, w)
        setWidth(clampedW)
        onWidthChangeRef.current?.(clampedW)
      }
    })
    ro.observe(el)
    return () => ro.disconnect()
  }, [minWidth])

  return (
    <div
      ref={containerRef}
      className={className}
      style={{ position: 'relative', width: '100%', height, ...style }}
    >
      {typeof children === 'function' ? children(width) : children}
    </div>
  )
}
