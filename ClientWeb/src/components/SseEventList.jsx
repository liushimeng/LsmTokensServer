// ClientWeb/src/components/SseEventList.jsx
//
// SSE 事件列表组件：把 parseSSEEvents 的结果按 event 拆卡片，每张卡片可折叠。
// v2.0.7x 阶段AM：对话详情 Modal 的「SSE 解析」视图专用。
// v2.0.7x 阶段AP：增加事件类型颜色区分与统计摘要栏。
//
// 设计要点：
//   - 默认展开前 5 张卡片，超出折叠避免一次渲染过多 DOM；
//   - 每张卡片：序号 / event 名 / 可折叠 data（parsed 优先 JSON 展示，回退 raw）；
//   - 不同 event 类型使用不同左边框颜色便于快速识别；
//   - 顶部显示事件统计摘要。

import { useState } from 'react'

// 事件类型 → 颜色分类（阶段AP）
function sseColorClass(eventName) {
  const name = (eventName || '').toLowerCase()
  if (name.startsWith('message')) return 'sse-color-message'   // message_start/message_stop/message_delta
  if (name.startsWith('content_block')) return 'sse-color-content'  // content_block_start/stop/delta
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
  if (!events || !events.length) {
    return <div className="sse-empty">（未解析出 SSE 事件）</div>
  }

  // 统计各事件类型数量（阶段AP）
  const typeCounts = {}
  events.forEach(e => {
    const name = e.event || '(default)'
    typeCounts[name] = (typeCounts[name] || 0) + 1
  })

  return (
    <>
      {/* 事件统计摘要 */}
      <div className="sse-event-summary">
        <span className="ses-total">共 {events.length} 个事件</span>
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
          <SseEventCard key={`sse-${i}`} index={i + 1} event={e} />
        ))}
      </div>
    </>
  )
}

function SseEventCard({ index, event }) {
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
          aria-label={open ? '折叠' : '展开'}
        >
          {open ? '▾' : '▸'}
        </button>
      </div>
      {open ? (
        <div className="sse-event-card-body">
          {hasParsed ? (
            <pre className="log-box sse-event-data">{JSON.stringify(event.parsed, null, 2)}</pre>
          ) : (
            <pre className="log-box sse-event-data">{event.raw || '（空 data）'}</pre>
          )}
          <div className="sse-event-raw-toggle">
            <button type="button" className="btn btn-sm" onClick={() => setShowRaw((s) => !s)}>
              {showRaw ? '隐藏原始块' : '显示原始块'}
            </button>
          </div>
          {showRaw ? (
            <pre className="log-box sse-event-raw">{event.raw || '（空）'}</pre>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
