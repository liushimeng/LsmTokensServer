// 内联展开详情面板：替代 Modal 弹窗，在表格行下方展开显示
// 支持多条记录同时展开，按需加载 + 内存缓存
import { useEffect } from 'react'
import { parseSSEEvents, aggregateSSE, aggregateToText, sseEventsToText } from '../../shared/sse'
import { prettyJSON } from '../../shared/json'
import { useI18n } from '../../i18n'
import { VIEW_RAW, VIEW_JSON, VIEW_SSE, VIEW_AGG, DETAIL_FIELDS, viewLabels, protocolBadgeText } from './constants'
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

  // 首次展开时自动加载默认字段
  useEffect(() => {
    if (detailState && !detailState.value && !detailState.loading && !detailState.cache['request_body']) {
      onTabChange('request_body')
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  if (!detailState) return null

  const { tab, view, value, loading: isLoading, cache } = detailState

  // 获取当前视图的"实际显示文本"（用于复制）
  const getShownContent = () => {
    const v = value || ''
    if (!tab.includes('body')) return v
    if (view === VIEW_RAW) return v
    if (view === VIEW_JSON) return prettyJSON(v)
    if (view === VIEW_SSE) return sseEventsToText(parseSSEEvents(v))
    if (view === VIEW_AGG) return aggregateToText(aggregateSSE(v))
    return v
  }

  const handleTabChange = (field) => {
    // 切换字段时，如果缓存中有数据则直接使用，否则加载
    onTabChange(field)
  }

  const handleViewChange = (v) => {
    onViewChange(v)
  }

  const handleCopy = () => {
    navigator.clipboard.writeText(getShownContent() || '').then(() => {
      onCopy()
    }).catch(() => {})
  }

  return (
    <div className="inline-detail-row">
      {/* 详情头部：标题 + 关闭按钮 */}
      <div className="inline-detail-title">
        <span className="inline-detail-title-text">
          {t('chatAnalysis.conversationDetail')} #{row.id} · {protocolBadgeText(row.dst_endpoint_algorithm_type, t)}
        </span>
        <button className="btn btn-sm inline-detail-close" onClick={onClose} title={t('common.collapse')}>
          ✕ {t('common.collapse')}
        </button>
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

      {/* 详情主体 */}
      <main className="detail-body">
        <DetailBody tab={tab} view={view} value={value} loading={isLoading} />
      </main>

      {/* 底部状态栏 */}
      <DetailFooter
        tab={tab}
        view={view}
        value={value}
        copyOk={copyOk}
        onCopy={handleCopy}
      />
    </div>
  )
}
