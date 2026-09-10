// components/EmptyState.jsx —— 空态组件（阶段BZ）
//
// 替换原来 "table-empty: 暂无数据" 一行文字 + 用户必须手动刷新的"反人类" UX：
//   - icon：左上图标（emoji 或自定义）
//   - title：主标题（如 "暂无路由"）
//   - hint：副提示（如 "可能后端正在处理大量数据，或当前过滤条件下确实没有数据"）
//   - onRetry：主操作按钮（"重试" / "重新加载"）
//   - onChangeFilter：可选的次要按钮（"切换统计档位" / "调整筛选条件"）
//   - retryLabel / changeFilterLabel：自定义按钮文案（默认走 i18n common.retry / changeFilter）
//
// 触发场景：
//   - 首次 load 失败（timeout/network/http_5xx） → 渲染 EmptyState + 重试按钮
//   - 列表为空（API 返回 totalCount=0）→ 渲染 EmptyState，onRetry 可选
//   - 长查询返回 partial（partial=true + warning）→ 渲染 EmptyState + hint 展示 warning

import { useI18n } from '../i18n'

export default function EmptyState(props) {
  const {
    icon = '📭',
    title,
    hint,
    warning,
    onRetry,
    onChangeFilter,
    retryLabel,
    changeFilterLabel,
    retryDisabled,
    style,
  } = props
  const { t } = useI18n()
  return (
    <div className="empty-state" style={style}>
      {icon ? <div className="empty-state-icon" aria-hidden="true">{icon}</div> : null}
      {title ? <div className="empty-state-title">{title}</div> : null}
      {hint ? <div className="empty-state-hint">{hint}</div> : null}
      {warning ? <div className="empty-state-warning">{warning}</div> : null}
      <div className="empty-state-actions">
        {onRetry ? (
          <button
            type="button"
            className="btn btn-primary btn-sm"
            disabled={retryDisabled}
            onClick={onRetry}
          >
            {retryLabel || t('common.retry')}
          </button>
        ) : null}
        {onChangeFilter ? (
          <button
            type="button"
            className="btn btn-sm"
            onClick={onChangeFilter}
          >
            {changeFilterLabel || t('common.changeFilter')}
          </button>
        ) : null}
      </div>
    </div>
  )
}