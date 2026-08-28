// 详情主体渲染组件：支持 raw / JSON美化 / SSE解析 / 聚合解析 4种视图
// 2026-08-27 升级：接入查找高亮模式。
// 2026-08-28 阶段AU：查找模式不再整体替换为纯文本长文 —— 除 raw/headers 外，
// 当前视图布局（JSON 折叠树 / SSE 卡片 / 聚合面板）保持原样，把 query 传入
// 视图组件在布局内做查找高亮（计数 / 当前项 / 焦点滚动由 InlineDetailRow
// 的 DOM 机制统一处理）。
import SseEventList from '../../components/SseEventList'
import AggregateView from '../../components/AggregateView'
import JsonTree from '../../components/JsonTree'
import HighlightText from '../../components/HighlightText'
import { parseSSEEvents, aggregateSSE } from '../../shared/sse'
import { useI18n } from '../../i18n'
import { VIEW_RAW, VIEW_JSON, VIEW_SSE, VIEW_AGG } from './constants'

export default function DetailBody({ tab, view, value, loading: isLoading, search }) {
  const { t } = useI18n()

  if (isLoading) return <div className="table-loading">{t('chatAnalysis.fieldLoading')}</div>

  const isBody = tab.includes('body')
  const query = (search && search.query) || ''

  // 非 body 字段（headers）只显示原始文本（查找时高亮）
  if (!isBody) {
    if (query) {
      return <HighlightText text={value || t('chatAnalysis.emptyContent')} query={query} />
    }
    return <pre className="log-box detail-content">{value || t('chatAnalysis.emptyContent')}</pre>
  }

  // body 字段按视图模式渲染；查找词非空时在当前视图布局内高亮
  switch (view) {
    case VIEW_JSON:
      return <div className="detail-content"><JsonTree value={value} query={query} /></div>
    case VIEW_SSE:
      return <div className="detail-content"><SseEventList events={parseSSEEvents(value)} query={query} /></div>
    case VIEW_AGG:
      return <div className="detail-content"><AggregateView result={aggregateSSE(value)} query={query} /></div>
    case VIEW_RAW:
    default:
      if (query) {
        return <HighlightText text={value || t('chatAnalysis.emptyContent')} query={query} />
      }
      return <pre className="log-box detail-content">{value || t('chatAnalysis.emptyContent')}</pre>
  }
}
