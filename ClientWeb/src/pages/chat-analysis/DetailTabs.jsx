// 详情字段 Tab 导航组件（分组式：请求 / 响应）
// 2026-08-27 升级：仅展示 4 个核心字段，移除 src_protocol 原始协议分组。
import { useI18n } from '../../i18n'
import { DETAIL_FIELDS, viewLabels } from './constants'

export default function DetailTabs({ currentTab, currentView, onTabChange, onViewChange }) {
  const { t } = useI18n()
  const labels = viewLabels(t)
  const isBody = currentTab.includes('body')

  const renderGroup = (prefix, labelKey) => (
    <div className="detail-tab-group">
      <span className="detail-tab-group-label">{t(labelKey)}</span>
      {DETAIL_FIELDS.filter((f) => f.key.startsWith(prefix)).map((f) => (
        <button key={f.key} className={`detail-tab${currentTab === f.key ? ' active' : ''}`}
                onClick={() => onTabChange(f.key)}>{t(f.titleKey)}</button>
      ))}
    </div>
  )

  return (
    <>
      {/* 主 Tab：请求组 / 响应组，各含 headers + body */}
      <nav className="detail-tabs">
        {renderGroup('request_', 'chatAnalysis.request')}
        {renderGroup('response_', 'chatAnalysis.response')}
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
