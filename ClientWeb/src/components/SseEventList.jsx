// ClientWeb/src/components/SseEventList.jsx
//
// SSE 事件列表组件：把 parseSSEEvents 的结果按 event 拆卡片，每张卡片可折叠。
// v2.0.7x 阶段AM：对话详情 Modal 的「SSE 解析」视图专用。
//
// 设计要点：
//   - 默认展开前 5 张卡片，超出折叠避免一次渲染过多 DOM；
//   - 每张卡片：序号 / event 名 / 可折叠 data（parsed 优先 JSON 展示，回退 raw）；
//   - 原始块默认折叠，仅 debug 时展开。

import { useState } from 'react'

export default function SseEventList({ events }) {
  if (!events || !events.length) {
    return <div className="sse-empty">（未解析出 SSE 事件）</div>
  }
  return (
    <div className="sse-event-list">
      {events.map((e, i) => (
        <SseEventCard key={`sse-${i}`} index={i + 1} event={e} />
      ))}
    </div>
  )
}

function SseEventCard({ index, event }) {
  const [open, setOpen] = useState(index <= 5)
  const [showRaw, setShowRaw] = useState(false)
  const hasParsed = event.parsed !== null && event.parsed !== undefined
  return (
    <div className="sse-event-card">
      <div className="sse-event-card-head" onClick={() => setOpen((o) => !o)}>
        <span className="sse-event-index">#{index}</span>
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