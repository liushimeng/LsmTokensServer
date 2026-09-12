// 自适应工具按钮组（docs/项目迁移解决方案/管理员与用户Web侧边菜单与顶部工具栏通用组件折叠展开方案_20260912.md §2.3）
//
// 替代 ToolbarDialogs.jsx 中散落的桌面/移动两套布局：
// - 宽屏（≥1280px）：所有按钮平铺
// - 中屏（860~1279px）：前 primaryCount 个平铺，剩余收进「⋯」下拉
// - 窄屏（≤860px）：全部收进「⋯」下拉
//
// 与 Modal/SideNav 等已有组件一致使用遮罩关闭下拉，避免菜单穿透到下层。
//
// 用法：
//   <CollapsibleToolbar items={DIALOG_REGISTRY} primaryCount={2}
//     active={openKey} onPick={setOpenKey} />

import { Fragment, useEffect, useRef, useState } from 'react'

const WIDE_MQ = '(min-width: 1280px)'
const NARROW_MQ = '(max-width: 860px)'

function useToolbarLayout() {
  const [isWide, setIsWide] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia(WIDE_MQ).matches
  )
  const [isNarrow, setIsNarrow] = useState(() =>
    typeof window !== 'undefined' && window.matchMedia(NARROW_MQ).matches
  )
  useEffect(() => {
    const w = window.matchMedia(WIDE_MQ)
    const n = window.matchMedia(NARROW_MQ)
    const onWide = (e) => setIsWide(e.matches)
    const onNarrow = (e) => setIsNarrow(e.matches)
    w.addEventListener('change', onWide)
    n.addEventListener('change', onNarrow)
    return () => {
      w.removeEventListener('change', onWide)
      n.removeEventListener('change', onNarrow)
    }
  }, [])
  return { isWide, isNarrow }
}

/**
 * 自适应工具按钮组。
 * @param {{
 *   items: { key: string, label: string }[],
 *   primaryCount?: number,   // 中屏平铺数量，默认 2
 *   active: string|null,     // 当前打开的弹窗 key
 *   onPick: (k: string) => void,
 *   buttonClassName?: string,
 *   ariaLabel?: string,
 * }} props
 */
export default function CollapsibleToolbar({
  items, primaryCount = 2, active, onPick,
  buttonClassName = 'btn btn-link btn-sm tool-btn', ariaLabel = '工具',
}) {
  const { isWide, isNarrow } = useToolbarLayout()
  const [moreOpen, setMoreOpen] = useState(false)
  const wrapRef = useRef(null)

  // 打开弹窗或下拉变化时收起下拉
  useEffect(() => { if (active) setMoreOpen(false) }, [active])

  // 外部点击关闭「⋯」
  useEffect(() => {
    if (!moreOpen) return undefined
    const onDoc = (e) => {
      if (wrapRef.current && !wrapRef.current.contains(e.target)) setMoreOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [moreOpen])

  if (!items || items.length === 0) return null

  // 宽屏全部平铺
  if (isWide) {
    return (
      <div className="header-tools" role="toolbar" aria-label={ariaLabel}>
        <div className="tools-list open">
          {items.map((it) => (
            <button key={it.key} type="button"
                    className={buttonClassName}
                    aria-pressed={active === it.key}
                    onClick={() => onPick(it.key)}>{it.label}</button>
          ))}
        </div>
      </div>
    )
  }

  // 中屏：前 N 个平铺，剩余 ⋯
  // 窄屏：全部 ⋯
  const flat = !isNarrow ? items.slice(0, primaryCount) : []
  const more = isNarrow ? items : items.slice(primaryCount)

  return (
    <div className="header-tools" ref={wrapRef} role="toolbar" aria-label={ariaLabel}>
      {flat.map((it) => (
        <Fragment key={it.key}>
          <button type="button"
                  className={buttonClassName + (isNarrow ? ' tools-flat-hidden' : '')}
                  aria-pressed={active === it.key}
                  onClick={() => onPick(it.key)}>{it.label}</button>
        </Fragment>
      ))}
      {more.length > 0 && (
        <>
          <button type="button" className="tools-more" title="更多工具"
                  aria-label="更多工具" aria-expanded={moreOpen}
                  onClick={() => setMoreOpen((v) => !v)}>⋯</button>
          {moreOpen && <div className="tools-close-mask" onClick={() => setMoreOpen(false)} />}
          <div className={'tools-list' + (moreOpen ? ' open' : '')}>
            {more.map((it) => (
              <button key={it.key} type="button" className={buttonClassName}
                      aria-pressed={active === it.key}
                      onClick={() => onPick(it.key)}>{it.label}</button>
            ))}
          </div>
        </>
      )}
    </div>
  )
}
