// 详情主体渲染组件：支持 raw / JSON美化 / SSE解析 / 聚合解析 4种视图
// 2026-08-27 升级：接入查找高亮模式 —— 查找词非空时按当前视图的文本表示做
// 全文高亮渲染（保证 JSON 折叠树/SSE 卡片/聚合面板中的内容都可检索遍历）。
import SseEventList from '../../components/SseEventList'
import AggregateView from '../../components/AggregateView'
import JsonTree from '../../components/JsonTree'
import HighlightText from '../../components/HighlightText'
import { parseSSEEvents, aggregateSSE } from '../../shared/sse'
import { buildViewText } from '../../shared/viewText'
import { useI18n } from '../../i18n'
import { VIEW_RAW, VIEW_JSON, VIEW_SSE, VIEW_AGG } from './constants'

export default function DetailBody({ tab, view, value, loading: isLoading, search }) {
  const { t } = useI18n()

  if (isLoading) return <div className="table-loading">{t('chatAnalysis.fieldLoading')}</div>

  const isBody = tab.includes('body')
  const query = (search && search.query) || ''

  // 查找模式：对当前视图文本表示做高亮渲染（所见即所查）
  if (query) {
    const shownText = isBody ? buildViewText(tab, view, value) : (value || '')
    return (
      <HighlightText
        text={shownText || t('chatAnalysis.emptyContent')}
        query={query}
        activeIndex={search.activeIndex}
        onCount={search.onCount}
      />
    )
  }

  // 非 body 字段（headers）只显示原始文本
  if (!isBody) {
    return <pre className="log-box detail-content">{value || t('chatAnalysis.emptyContent')}</pre>
  }

  // body 字段按视图模式渲染
  switch (view) {
    case VIEW_JSON:
      return <div className="detail-content"><JsonTree value={value} /></div>
    case VIEW_SSE:
      return <div className="detail-content"><SseEventList events={parseSSEEvents(value)} /></div>
    case VIEW_AGG:
      return <div className="detail-content"><AggregateView result={aggregateSSE(value)} /></div>
    case VIEW_RAW:
    default:
      return <pre className="log-box detail-content">{value || t('chatAnalysis.emptyContent')}</pre>
  }
}
