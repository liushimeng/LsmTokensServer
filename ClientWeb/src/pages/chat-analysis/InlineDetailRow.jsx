// 内联展开详情面板：替代 Modal 弹窗，在表格行下方展开显示
// 支持多条记录同时展开，按需加载 + 内存缓存
// 2026-08-27 升级：工具栏新增 查找 / 复制 / 全屏；Esc 退出全屏；复制所见即所得。
// 2026-08-28 阶段AU：
//   - 切换字段 Tab 时若当前视图不在新字段的可用视图列表（viewsForTab）中，
//     自动回落 raw（request: raw/json；response: raw/sse/agg）；
//   - 查找高亮改为 DOM 驱动：渲染后的 DOM 是唯一事实来源（WYSIWYG）——
//     匹配计数 / 当前项标记 / 焦点滚动统一基于 querySelectorAll('mark.sm-mark')，
//     天然兼容 JsonTree 节点折叠、SSE 卡片折叠、超长字符串展开等嵌套局部
//     状态变化（这些变化不触发本组件重渲染，纯 React 状态方案必然计数失真）。
// v2.0.7x 阶段CN（需求 3：真全屏）：
//   - 「全屏」改为 Element Fullscreen API（见 shared/useFullscreen.js）：详情面板进入 top layer
//     铺满整个屏幕，真正跳出浏览器页面框架（地址栏/标签栏/书签栏全部隐藏）；
//     JS 无法触发浏览器自身的 F11 开关，元素级 top layer 全屏是唯一正确手段。
//   - 状态以 document.fullscreenElement 为唯一事实来源（订阅 fullscreenchange），原生全屏下
//     **不注册** Esc 按键处理（交给浏览器退出，避免抢事件造成状态错乱 —— 修复 B6）；
//   - iOS Safari 等不支持元素全屏、或 requestFullscreen 被 reject（非用户手势 / iframe 缺
//     allow="fullscreen"）→ 自动降级为原 position:fixed 页面内最大化（fallback 态），
//     仅降级态才锁背景滚动 + 监听 Esc 退出，并在按钮 title 说明已降级（修复 B5）；
//   - 收起详情行 / 翻页 / 路由切换导致本组件卸载时，若全屏元素正是自己则主动退出全屏，
//     且只认自己这个 DOM，多条记录同时展开互不误伤（修复 B7）。
// 阶段CN（需求 1：自动展开）：挂载后自动 focus + scrollIntoView，配合 DataTable 新增的
//   「展开行」语义（expandedIds + renderExpandedRow）—— 原数据行保留，详情在行下方整行展开。
import { useEffect, useRef, useState } from 'react'
import { buildViewText, viewsForTab, VIEW_RAW } from '../../shared/viewText'
import useFullscreen from '../../shared/useFullscreen'
import { copyToClipboard } from '../../shared/clipboard'
import { useI18n } from '../../i18n'
import { protocolBadgeText } from './constants'
import SearchBar from '../../components/SearchBar'
import DetailHeader from './DetailHeader'
import DetailTabs from './DetailTabs'
import DetailBody from './DetailBody'
import DetailFooter from './DetailFooter'

export default function InlineDetailRow({
  row,
  detailState,      // { tab, view, value, loading, cache }
  onTabChange,       // (field) => void — 加载指定字段
  onViewChange,      // (view) => void — 切换视图模式
  onCopy,            // () => void — 复制当前视图内容
  copyOk,
  onClose,           // () => void — 收起详情
}) {
  const { t } = useI18n()

  // 查找状态
  const [searchOpen, setSearchOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [matchCount, setMatchCount] = useState(0)
  const rootRef = useRef(null)

  // 全屏状态机（阶段CN）：native = 元素已进入 top layer 真全屏；fallback = 页面内最大化降级
  const {
    isFullscreen, isFallback, nativeUnavailable, toggle: toggleFullscreen, exit: exitFullscreen,
  } = useFullscreen(rootRef)

  // 查找 DOM 机制的最新闭包与状态（MutationObserver 回调需要稳定引用）
  const applySearchRef = useRef(() => {})
  const matchCountRef = useRef(0)
  const lastActiveElRef = useRef(null)

  // 查找高亮 / 计数 / 焦点移动（DOM 机制）：每次 render 重建最新闭包。
  // - 计数：root 内全部 mark.sm-mark 元素数（上报 matchCount 供 SearchBar 显示 i/N）；
  // - 当前项：marks[activeIndex] 切换 sm-mark-active 类（DOM 类操作，React 不
  //   diff 未受控类名，二者互不干扰）；
  // - 焦点移动：当前项元素变化时 scrollIntoView 居中。
  applySearchRef.current = () => {
    const root = rootRef.current
    if (!root || !query) return
    const marks = root.querySelectorAll('mark.sm-mark')
    if (marks.length !== matchCountRef.current) {
      matchCountRef.current = marks.length
      setMatchCount(marks.length)
      setActiveIndex((i) => (marks.length === 0 ? 0 : Math.min(i, marks.length - 1)))
    }
    let activeEl = null
    marks.forEach((m, i) => {
      const on = i === activeIndex
      m.classList.toggle('sm-mark-active', on)
      if (on) activeEl = m
    })
    if (activeEl && activeEl !== lastActiveElRef.current) {
      lastActiveElRef.current = activeEl
      activeEl.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }
    if (!activeEl) lastActiveElRef.current = null
  }

  // 首次展开时自动加载默认字段
  // 阶段AY：通过 onTabChangeRef 跟踪最新回调，避免空依赖造成 stale closure
  // （父组件更新 onTabChange 引用时，本组件不会重新订阅）
  const onTabChangeRef = useRef(onTabChange)
  useEffect(() => { onTabChangeRef.current = onTabChange }, [onTabChange])
  useEffect(() => {
    if (detailState && !detailState.value && !detailState.loading && !detailState.cache['request_body']) {
      onTabChangeRef.current('request_body')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 展开即定位并聚焦（阶段CN 需求 1）：点了「详情」就要给出明确反馈 ——
  // 面板滚动进入视口 + 根节点可聚焦（tabIndex=-1，不显示焦点环），
  // 使 Esc / 方向键 / 全屏按钮立即可用。原生全屏下无需滚动（面板已铺满屏幕）。
  useEffect(() => {
    const el = rootRef.current
    if (!el) return
    try { el.focus({ preventScroll: true }) } catch { /* 老浏览器不识别选项对象 */ }
    if (typeof el.scrollIntoView === 'function') el.scrollIntoView({ block: 'nearest', behavior: 'smooth' })
  }, [])

  // 仅降级态（页面内 CSS 最大化）需要：锁定背景滚动 + Esc 退出。
  // 原生全屏由浏览器管理 Esc 与背景，抢按键会导致状态错乱（修复 B6）。
  // Esc 用冒泡阶段监听，SearchBar 内的 Esc 已 stopPropagation，先关查找栏再退全屏。
  useEffect(() => {
    if (!isFallback) return undefined
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        exitFullscreen()
      }
    }
    window.addEventListener('keydown', onKey)
    return () => {
      document.body.style.overflow = prev
      window.removeEventListener('keydown', onKey)
    }
  }, [isFallback, exitFullscreen])

  // 每次 render 后应用查找高亮 / 计数 / 焦点（query / activeIndex / tab / view /
  // value 变化均触发）。注意：必须在早退 return 之前调用，保证 hook 顺序稳定。
  useEffect(() => { applySearchRef.current() })

  // MutationObserver 兜底：嵌套局部状态变化（JsonTree 节点折叠、SSE 卡片展开、
  // 超长字符串"显示全部"等）不触发本组件重渲染，靠 DOM 变更回调重新计数。
  useEffect(() => {
    const root = rootRef.current
    if (!root || typeof MutationObserver === 'undefined') return undefined
    const mo = new MutationObserver(() => applySearchRef.current())
    mo.observe(root, { childList: true, subtree: true, characterData: true })
    return () => mo.disconnect()
    // eslint-disable-next-line react-hooks/exhaustive-deps -- 仅挂载时订阅一次
  }, [])

  if (!detailState) return null

  const { tab, view, value, loading: isLoading } = detailState

  // 当前视图的"实际显示文本"（查找与复制共用，保证所见即所得）
  const getShownContent = () => buildViewText(tab, view, value)

  const handleTabChange = (field) => {
    setActiveIndex(0)
    // 视图按字段语义裁剪（阶段AU）：新字段不支持当前视图时回落 raw
    if (!viewsForTab(field).includes(view)) {
      onViewChange(VIEW_RAW)
    }
    onTabChange(field)
  }

  const handleViewChange = (v) => {
    setActiveIndex(0)
    onViewChange(v)
  }

  const handleCopy = () => {
    copyToClipboard(getShownContent() || '').then((ok) => {
      if (ok) onCopy()
    })
  }

  // ===== 查找交互 =====
  const handleQueryChange = (q) => { setQuery(q); setActiveIndex(0) }
  const handlePrev = () => { if (matchCount > 0) setActiveIndex((i) => (i - 1 + matchCount) % matchCount) }
  const handleNext = () => { if (matchCount > 0) setActiveIndex((i) => (i + 1) % matchCount) }
  const handleSearchToggle = () => {
    if (searchOpen) {
      setSearchOpen(false); setQuery(''); setActiveIndex(0); setMatchCount(0)
      matchCountRef.current = 0
      lastActiveElRef.current = null
    } else { setSearchOpen(true) }
  }

  const search = { query }

  // 全屏样式类：降级态用页面内 fixed 最大化，原生态用 top layer 尺寸/滚动适配（两类互斥）
  const fullscreenClass = isFallback
    ? 'inline-detail-fullscreen'
    : (isFullscreen ? 'inline-detail-native-fullscreen' : '')
  const fsLabel = isFullscreen ? t('chatAnalysis.exitFullscreen') : t('chatAnalysis.fullscreen')
  // 降级态 / 已知原生全屏不可用时，hover 文案说明实际行为（避免用户以为「全屏没生效」）
  const fsTitle = (isFallback || nativeUnavailable)
    ? `${fsLabel} · ${t('chatAnalysis.fullscreenUnsupported')}`
    : fsLabel

  return (
    <div ref={rootRef} tabIndex={-1} className={`inline-detail-row${fullscreenClass ? ' ' + fullscreenClass : ''}`}>
      {/* 详情头部：标题 + 工具栏（查找/复制/全屏/关闭） */}
      <div className="inline-detail-title">
        <span className="inline-detail-title-text">
          {t('chatAnalysis.conversationDetail')} #{row.id} · {protocolBadgeText(row.dst_endpoint_algorithm_type, t)}
        </span>
        <div className="inline-detail-actions">
          <button className={`btn btn-sm${searchOpen ? ' btn-active' : ''}`} onClick={handleSearchToggle}
                  title={t('chatAnalysis.search')}>
            🔍 {t('chatAnalysis.search')}
          </button>
          <button className="btn btn-sm" onClick={handleCopy} title={t('chatAnalysis.copyView')}>
            {copyOk ? '📋 ' + t('common.copied') + ' ✓' : '📋 ' + t('chatAnalysis.copyView')}
          </button>
          <button className={`btn btn-sm${isFullscreen ? ' btn-active' : ''}`} onClick={toggleFullscreen}
                  title={fsTitle} aria-pressed={isFullscreen}>
            {isFullscreen ? '🗗 ' + fsLabel : '⛶ ' + fsLabel}
          </button>
          <button className="btn btn-sm inline-detail-close" onClick={onClose} title={t('common.collapse')}>
            ✕ {t('common.collapse')}
          </button>
        </div>
      </div>

      {/* 协议流向 + KPI 卡片 + 请求信息 */}
      <DetailHeader row={row} />

      {/* 字段 Tab + 视图切换 */}
      <DetailTabs
        currentTab={tab}
        currentView={view}
        onTabChange={handleTabChange}
        onViewChange={handleViewChange}
      />

      {/* 查找栏 */}
      {searchOpen ? (
        <SearchBar
          query={query}
          onQueryChange={handleQueryChange}
          activeIndex={activeIndex}
          matchCount={matchCount}
          onPrev={handlePrev}
          onNext={handleNext}
          onClose={handleSearchToggle}
        />
      ) : null}

      {/* 详情主体 */}
      <main className="detail-body">
        <DetailBody tab={tab} view={view} value={value} loading={isLoading} search={search} />
      </main>

      {/* 底部状态栏 */}
      <DetailFooter
        tab={tab}
        view={view}
        value={value}
      />
    </div>
  )
}
