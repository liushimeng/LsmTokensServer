// components/LongQueryProgress.jsx —— 长查询进度条（阶段BZ）
//
// 用于 SSE / WS 流式接口（ChatAnalysisTotalWS / ChatAnalysisTotalRangeInterface?stream=1）。
// 显示三态：
//   - scanning：扫描中（onProgress 每秒推一行）
//   - statizing：统计中（合并阶段）
//   - done：完成（自动 fade-out）
//
// 用法：
//   const { progress, cancel } = useLongQueryProgress()
//   <LongQueryProgress progress={progress} onCancel={cancel} />
//
// 与 AbortController 联动：调用方传入 controller.signal，按"取消"立即中断流。

import { useI18n } from '../i18n'

export default function LongQueryProgress({ progress, onCancel }) {
  const { t } = useI18n()
  if (!progress) return null
  const { phase, rows = 0, approx = 0, percent = 0, message } = progress
  if (phase === 'done') return null
  const isStat = phase === 'statizing'
  return (
    <div className="long-query-progress" role="status" aria-live="polite">
      <div className="long-query-progress-row">
        <span className="long-query-progress-phase">
          {isStat ? t('common.statizing') : t('common.scanning')}
          {rows ? ` · ${t('common.longQueryHint', { rows, approx, pct: Math.floor(percent) })}` : ''}
        </span>
        {onCancel ? (
          <button type="button" className="btn btn-sm" onClick={onCancel}>
            {t('common.cancel')}
          </button>
        ) : null}
      </div>
      <div className="long-query-progress-bar">
        <div
          className="long-query-progress-bar-fill"
          style={{ width: `${Math.min(100, Math.max(0, percent))}%` }}
        />
      </div>
      {message ? <div className="long-query-progress-msg">{message}</div> : null}
    </div>
  )
}