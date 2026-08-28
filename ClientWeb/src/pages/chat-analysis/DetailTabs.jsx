// 详情字段 Tab 导航组件（分组式：请求 / 响应）
// 2026-08-27 升级：仅展示 4 个核心字段，移除 src_protocol 原始协议分组。
// 2026-08-28 阶段AU：视图子 Tab 按字段语义裁剪（viewsForTab）——
//   request_body：原文 / JSON 美化（请求体不是 SSE 流）；
//   response_body：原文 / SSE 解析 / 聚合解析（流式原文不是单个 JSON）。
import { useI18n } from '../../i18n'
import { viewsForTab } from '../../shared/viewText'
import { DETAIL_FIELDS, viewLabels } from './constants'

export default function DetailTabs({ currentTab, currentView, onTabChange, onViewChange }) {
  const { t } = useI18n()
  const labels = viewLabels(t)
  const isBody = currentTab.includes('body')
  const allowedViews = viewsForTab(currentTab)

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

      {/* 视图子 Tab（仅 body 类字段，且按字段语义裁剪可用视图） */}
      {isBody ? (
        <nav className="detail-views">
          {allowedViews.map((v) => (
            <button key={v} className={`detail-view${currentView === v ? ' active' : ''}`}
                    onClick={() => onViewChange(v)}>{labels[v]}</button>
          ))}
        </nav>
      ) : null}
    </>
  )
}
