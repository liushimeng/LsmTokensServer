// ClientWeb/src/components/SseEventList.jsx
//
// SSE 事件列表组件：把 parseSSEEvents 的结果按 event 拆卡片，每张卡片可折叠。
// v2.0.7x 阶段AM：对话详情 Modal 的「SSE 解析」视图专用。
// v2.0.7x 阶段AP：增加事件类型颜色区分与统计摘要栏。
// v2.0.7x 阶段AR：全面增强 ——
//   1) 非流式完整响应（synthetic complete 事件）以"完整响应"卡片展示，
//      避免"暂无数据"误导；
//   2) 大响应体时卡片默认折叠，避免一次性渲染过多内容；
//   3) 所有卡片均支持点击展开/折叠，内容可读可复制。

import { useState } from 'react'
import { useI18n } from '../i18n'

// 事件类型 → 颜色分类（阶段AP）
function sseColorClass(eventName) {
  const name = (eventName || '').toLowerCase()
  if (name.startsWith('message')) return 'sse-color-message'
  if (name.startsWith('content_block')) return 'sse-color-content'
  if (name.includes('delta')) return 'sse-color-delta'
  if (name.includes('error')) return 'sse-color-error'
  if (name === 'complete') return 'sse-color-complete'
  return 'sse-color-default'
}

// 事件类型 → 左侧圆点颜色
function sseDotColor(eventName) {
  const name = (eventName || '').toLowerCase()
  if (name.startsWith('message')) return '#2563eb'
  if (name.startsWith('content_block')) return '#16a34a'
  if (name.includes('delta')) return '#ea580c'
  if (name.includes('error')) return '#dc2626'
  if (name === 'complete') return '#7c3aed'
  return '#94a3b8'
}

export default function SseEventList({ events }) {
  const { t } = useI18n()
  if (!events || !events.length) {
    return <div className="sse-empty">({t('common.noData')})</div>
  }

  const typeCounts = {}
  events.forEach(e => {
    const name = e.event || '(default)'
    typeCounts[name] = (typeCounts[name] || 0) + 1
  })

  // 阶段AR：单条 synthetic complete 事件 → 显示"完整响应"说明
  const isCompleteSingle = events.length === 1 && events[0].synthetic && events[0].event === 'complete'

  return (
    <>
      <div className="sse-event-summary">
        <span className="ses-total">
          {isCompleteSingle
            ? t('chatAnalysis.sseCompleteResponse')
            : t('common.total', { count: events.length })}
        </span>
        <span className="ses-divider" />
        {Object.entries(typeCounts).map(([name, count]) => (
          <span key={name} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span className="sse-event-type-dot" style={{ background: sseDotColor(name) }} />
            <span>{name}</span>
            <span style={{ color: '#6366f1', fontSize: 11, background: 'rgba(99,102,241,.08)', borderRadius: 6, padding: '0 5px' }}>×{count}</span>
          </span>
        ))}
      </div>

      <div className="sse-event-list">
        {events.map((e, i) => (
          <SseEventCard key={`sse-${i}`} index={i + 1} event={e} t={t} defaultOpen={i < 5} isComplete={isCompleteSingle} />
        ))}
      </div>
    </>
  )
}

function SseEventCard({ index, event, t, defaultOpen, isComplete }) {
  const [open, setOpen] = useState(defaultOpen)
  const [showRaw, setShowRaw] = useState(false)
  const hasParsed = event.parsed !== null && event.parsed !== undefined
  const colorClass = sseColorClass(event.event)

  const title = isComplete
    ? t('chatAnalysis.sseCompleteResponse')
    : `event: ${event.event || '(default)'}`

  return (
    <div className={`sse-event-card ${colorClass}${isComplete ? ' sse-complete-card' : ''}`}>
      <div className="sse-event-card-head" onClick={() => setOpen((o) => !o)}>
        {!isComplete ? <span className="sse-event-index">#{index}</span> : null}
        <span className="sse-event-type-dot" style={{ background: sseDotColor(event.event) }} />
        <span className="sse-event-name">{title}</span>
        <button
          type="button"
          className="btn btn-sm sse-event-toggle"
          onClick={(e) => { e.stopPropagation(); setOpen((o) => !o) }}
          aria-label={open ? t('common.collapse') : t('common.expand')}
        >
          {open ? '▾' : '▸'}
        </button>
      </div>
      {open ? (
        <div className="sse-event-card-body">
          {hasParsed ? (
            <pre className="log-box sse-event-data">{JSON.stringify(event.parsed, null, 2)}</pre>
          ) : (
            <pre className="log-box sse-event-data">{event.raw || `(${t('common.noData')})`}</pre>
          )}
          <div className="sse-event-raw-toggle">
            <button type="button" className="btn btn-sm" onClick={() => setShowRaw((s) => !s)}>
              {showRaw ? t('common.hide') : t('common.show')}
            </button>
          </div>
          {showRaw ? (
            <pre className="log-box sse-event-raw">{event.raw || `(${t('common.none')})`}</pre>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
