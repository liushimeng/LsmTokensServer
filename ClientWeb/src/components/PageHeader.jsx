// 通用页面级标题组件（docs/项目迁移解决方案/管理员与用户Web侧边菜单与顶部工具栏通用组件折叠展开方案_20260912.md §2.4）
//
// 替换各页面散落的 <h2 className="page-title"> 模式，统一为：
//   ┌──────────────────────────────────────────────────────────────────┐
//   │ 🏠  对话分析                                       [刷新] [导出] │
//   │      ChatAnalysis · /ChatAnalysis                                │
//   │      共 1,234 条记录 · 覆盖 30 天                                │
//   └──────────────────────────────────────────────────────────────────┘
//
// API：
//   <PageHeader
//     icon="💬"                       // 一级分组图标（与 NAV_TREES 一致）
//     title="对话分析"                 // 主标题（已翻译文本）
//     breadcrumb={['分析', '对话']}    // 可选面包屑
//     info={[<KPI 文本 />, ...]}       // 可选副信息（记录数、时间跨度、KPI）
//     actions={<button>刷新</button>}  // 可选页面级操作按钮
//   />
//
// 响应式：
// - 宽屏（≥860px）：三栏（icon+title | info | actions）平铺
// - 窄屏（≤860px）：actions 自动收进「⋯」下拉

import { useEffect, useRef, useState } from 'react'

// 与 Layout.jsx 保持一致的窄屏断点
const MOBILE_MQ = '(max-width: 860px)'
function useIsMobile() {
  const [m, setM] = useState(() => typeof window !== 'undefined' && window.matchMedia(MOBILE_MQ).matches)
  useEffect(() => {
    const mq = window.matchMedia(MOBILE_MQ)
    const onChange = (e) => setM(e.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return m
}

export default function PageHeader({ icon, title, breadcrumb, info, actions }) {
  const isMobile = useIsMobile()
  const [moreOpen, setMoreOpen] = useState(false)
  const moreRef = useRef(null)

  // 外部点击关闭「⋯」
  useEffect(() => {
    if (!moreOpen) return undefined
    const onDoc = (e) => {
      if (moreRef.current && !moreRef.current.contains(e.target)) setMoreOpen(false)
    }
    document.addEventListener('mousedown', onDoc)
    return () => document.removeEventListener('mousedown', onDoc)
  }, [moreOpen])

  const hasActions = !!actions
  const hasBreadcrumb = Array.isArray(breadcrumb) && breadcrumb.length > 0
  const hasInfo = Array.isArray(info) && info.length > 0

  return (
    <header className="page-header">
      <div className="page-header-main">
        {icon ? <span className="page-header-icon" aria-hidden="true">{icon}</span> : null}
        <div className="page-header-titles">
          <h2 className="page-title">{title}</h2>
          {hasBreadcrumb && (
            <nav className="page-header-crumb" aria-label="breadcrumb">
              {breadcrumb.map((seg, i) => (
                <span key={i}>
                  {i > 0 ? <span className="page-header-crumb-sep">/</span> : null}
                  <span className="page-header-crumb-seg">{seg}</span>
                </span>
              ))}
            </nav>
          )}
        </div>
      </div>

      {(hasInfo || hasActions) && (
        <div className="page-header-right">
          {hasInfo && (
            <div className="page-header-info">
              {info.map((seg, i) => (
                <span key={i} className="page-header-info-seg">{seg}</span>
              ))}
            </div>
          )}
          {hasActions && (
            isMobile ? (
              <div className="page-header-actions-more" ref={moreRef}>
                <button
                  type="button"
                  className="btn btn-sm"
                  aria-label="更多操作"
                  title="更多操作"
                  onClick={() => setMoreOpen((v) => !v)}
                >⋯</button>
                {moreOpen && (
                  <div className="page-header-actions-dropdown" role="menu">
                    {actions}
                  </div>
                )}
              </div>
            ) : (
              <div className="page-header-actions">
                {actions}
              </div>
            )
          )}
        </div>
      )}
    </header>
  )
}
