// 内联展开详情面板：替代 Modal 弹窗，在表格行下方展开显示
// 支持多条记录同时展开，按需加载 + 内存缓存
// 2026-08-27 升级：工具栏新增 查找 / 复制 / 全屏；Esc 退出全屏；复制所见即所得。
import { useEffect, useRef, useState } from 'react'
import { buildViewText } from '../../shared/viewText'
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

  // 全屏状态
  const [fullscreen, setFullscreen] = useState(false)
  // 查找状态
  const [searchOpen, setSearchOpen] = useState(false)
  const [query, setQuery] = useState('')
  const [activeIndex, setActiveIndex] = useState(0)
  const [matchCount, setMatchCount] = useState(0)
  const rootRef = useRef(null)

  // 首次展开时自动加载默认字段
  useEffect(() => {
    if (detailState && !detailState.value && !detailState.loading && !detailState.cache['request_body']) {
      onTabChange('request_body')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 全屏时锁定背景滚动；卸载/退出全屏时复位
  useEffect(() => {
    if (!fullscreen) return undefined
    const prev = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    return () => { document.body.style.overflow = prev }
  }, [fullscreen])

  // Esc 退出全屏（SearchBar 内的 Esc 已 stopPropagation，互不干扰）
  useEffect(() => {
    if (!fullscreen) return undefined
    const onKey = (e) => {
      if (e.key === 'Escape') {
        e.preventDefault()
        setFullscreen(false)
      }
    }
    window.addEventListener('keydown', onKey, true)
    return () => window.removeEventListener('keydown', onKey, true)
  }, [fullscreen])

  if (!detailState) return null

  const { tab, view, value, loading: isLoading } = detailState

  // 当前视图的"实际显示文本"（查找与复制共用，保证所见即所得）
  const getShownContent = () => buildViewText(tab, view, value)

  const handleTabChange = (field) => {
    setActiveIndex(0)
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

  // 查找交互
  const handleQueryChange = (q) => { setQuery(q); setActiveIndex(0) }
  const handlePrev = () => { if (matchCount > 0) setActiveIndex((i) => (i - 1 + matchCount) % matchCount) }
  const handleNext = () => { if (matchCount > 0) setActiveIndex((i) => (i + 1) % matchCount) }
  const handleCount = (n) => {
    setMatchCount(n)
    setActiveIndex((i) => (n === 0 ? 0 : Math.min(i, n - 1)))
  }
  const handleSearchToggle = () => {
    if (searchOpen) { setSearchOpen(false); setQuery(''); setActiveIndex(0); setMatchCount(0) } else { setSearchOpen(true) }
  }

  const search = { query, activeIndex, onCount: handleCount }

  return (
    <div ref={rootRef} className={`inline-detail-row${fullscreen ? ' inline-detail-fullscreen' : ''}`}>
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
          <button className="btn btn-sm" onClick={() => setFullscreen((f) => !f)}
                  title={fullscreen ? t('chatAnalysis.exitFullscreen') : t('chatAnalysis.fullscreen')}>
            {fullscreen ? '🗗 ' + t('chatAnalysis.exitFullscreen') : '⛶ ' + t('chatAnalysis.fullscreen')}
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
