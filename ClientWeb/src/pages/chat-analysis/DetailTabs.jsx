// 详情字段 Tab 导航组件（分组式：转发Body / 原始Body / Headers）
import { useI18n } from '../../i18n'
import { DETAIL_FIELDS, viewLabels, VIEW_RAW, VIEW_JSON, VIEW_SSE, VIEW_AGG } from './constants'

export default function DetailTabs({ currentTab, currentView, onTabChange, onViewChange }) {
  const { t } = useI18n()
  const labels = viewLabels(t)
  const isBody = currentTab.includes('body')

  return (
    <>
      {/* 主 Tab：分组式 6 个字段 */}
      <nav className="detail-tabs">
        <div className="detail-tab-group">
          <span className="detail-tab-group-label">{t('chatAnalysis.forwardBody')}</span>
          {DETAIL_FIELDS.filter(f => f.key === 'request_body' || f.key === 'response_body').map((f) => (
            <button key={f.key} className={`detail-tab${currentTab === f.key ? ' active' : ''}`}
                    onClick={() => onTabChange(f.key)}>{t(f.titleKey)}</button>
          ))}
        </div>
        <div className="detail-tab-group">
          <span className="detail-tab-group-label">{t('chatAnalysis.rawBody')}</span>
          {DETAIL_FIELDS.filter(f => f.key.includes('src_protocol')).map((f) => (
            <button key={f.key} className={`detail-tab${currentTab === f.key ? ' active' : ''}`}
                    onClick={() => onTabChange(f.key)}>{t(f.titleKey)}</button>
          ))}
        </div>
        <div className="detail-tab-group">
          <span className="detail-tab-group-label">{t('chatAnalysis.headers')}</span>
          {DETAIL_FIELDS.filter(f => f.key.includes('headers')).map((f) => (
            <button key={f.key} className={`detail-tab${currentTab === f.key ? ' active' : ''}`}
                    onClick={() => onTabChange(f.key)}>{t(f.titleKey)}</button>
          ))}
        </div>
      </nav>

      {/* 视图子 Tab（仅 body 类字段） */}
      {isBody ? (
        <nav className="detail-views">
          {Object.entries(labels).map(([v, label]) => (
            <button key={v} className={`detail-view${currentView === v ? ' active' : ''}`}
                    onClick={() => onViewChange(v)}>{label}</button>
          ))}
        </nav>
      ) : null}
    </>
  )
}
