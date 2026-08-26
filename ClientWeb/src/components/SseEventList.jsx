// ClientWeb/src/components/SseEventList.jsx
//
// SSE 事件列表组件：把 parseSSEEvents 的结果按 event 拆卡片，每张卡片可折叠。
// v2.0.7x 阶段AM：对话详情 Modal 的「SSE 解析」视图专用。
// v2.0.7x 阶段AP：增加事件类型颜色区分与统计摘要栏。

import { useState } from 'react'
import { useI18n } from '../i18n'

// 事件类型 → 颜色分类（阶段AP）
function sseColorClass(eventName) {
  const name = (eventName || '').toLowerCase()
  if (name.startsWith('message')) return 'sse-color-message'
  if (name.startsWith('content_block')) return 'sse-color-content'
  if (name.includes('delta')) return 'sse-color-delta'
  if (name.includes('error')) return 'sse-color-error'
  return 'sse-color-default'
}

// 事件类型 → 左侧圆点颜色
function sseDotColor(eventName) {
  const name = (eventName || '').toLowerCase()
  if (name.startsWith('message')) return '#2563eb'
  if (name.startsWith('content_block')) return '#16a34a'
  if (name.includes('delta')) return '#ea580c'
  if (name.includes('error')) return '#dc2626'
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

  return (
    <>
      <div className="sse-event-summary">
        <span className="ses-total">{t('common.total', { count: events.length })}</span>
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
          <SseEventCard key={`sse-${i}`} index={i + 1} event={e} t={t} />
        ))}
      </div>
    </>
  )
}

function SseEventCard({ index, event, t }) {
  const [open, setOpen] = useState(index <= 5)
  const [showRaw, setShowRaw] = useState(false)
  const hasParsed = event.parsed !== null && event.parsed !== undefined
  const colorClass = sseColorClass(event.event)
  return (
    <div className={`sse-event-card ${colorClass}`}>
      <div className="sse-event-card-head" onClick={() => setOpen((o) => !o)}>
        <span className="sse-event-index">#{index}</span>
        <span className="sse-event-type-dot" style={{ background: sseDotColor(event.event) }} />
        <span className="sse-event-name">event: {event.event || '(default)'}</span>
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
