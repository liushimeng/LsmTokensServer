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
// v2.0.75 阶段AU：SSE 事件 data 本质是 JSON ——
//   1) parsed JSON 改用 JsonTree 渲染（无卡片内工具栏），自动获得标准排版、
//      语法高亮、逐节点折叠、超长字符串保护等 JSON 树能力；
//   2) 新增可选 query prop：事件名 / raw 原文 / parsed JSON 在卡片布局内
//      查找高亮（计数与焦点滚动由 InlineDetailRow DOM 机制统一处理）。
// v2.0.77 阶段AW：SSE 事件 parsed 是字符串时二次 JSON 解析 ——
//   兼容 data: "{\"a\":1}"（合法 JSON 字符串作为 SSE data 行）这类场景，
//   二次解析后若为 object/array 则走 JsonTree 渲染，与 JSON 美化按钮行为一致。
// v2.0.78 阶段BH：超长流渲染上限 ——
//   长对话（千级 delta 事件）一次性全量渲染卡片会挂出过多 DOM 节点导致滚动/
//   展开卡顿；改为分批渲染（首屏 RENDER_BATCH=200 张，超出部分「加载更多」
//   每次追加 200）。顶部 summary 统计栏始终基于全量 events，不随截断变化。

import { useState } from 'react'
import { useI18n } from '../i18n'
import JsonTree from './JsonTree'
import SearchText from './SearchText'
import { tryParseJsonObject } from '../shared/sse'

// 阶段BH：单批渲染卡片数（首屏 & 每次加载更多的增量）
const RENDER_BATCH = 200

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

export default function SseEventList({ events, query }) {
  const { t } = useI18n()
  // 阶段BH：分批渲染游标（视图切换 / 行切换时组件重挂载自动复位）
  const [visible, setVisible] = useState(RENDER_BATCH)
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

  // 阶段BH：仅渲染前 visible 张卡片（统计栏始终基于全量 events）
  const shown = Math.min(visible, events.length)

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
            <span><SearchText query={query} text={name} /></span>
            <span style={{ color: '#6366f1', fontSize: 11, background: 'rgba(99,102,241,.08)', borderRadius: 6, padding: '0 5px' }}>×{count}</span>
          </span>
        ))}
      </div>

      <div className="sse-event-list">
        {events.slice(0, shown).map((e, i) => (
          <SseEventCard key={`sse-${i}`} index={i + 1} event={e} t={t} query={query} defaultOpen={i < 5} isComplete={isCompleteSingle} />
        ))}
        {events.length > shown ? (
          <button
            type="button"
            className="btn btn-sm sse-load-more"
            onClick={() => setVisible((v) => v + RENDER_BATCH)}
          >
            ⬇ {t('chatAnalysis.sseLoadMore', { shown, total: events.length })}
          </button>
        ) : null}
      </div>
    </>
  )
}

function SseEventCard({ index, event, t, query, defaultOpen, isComplete }) {
  const [open, setOpen] = useState(defaultOpen)
  const [showRaw, setShowRaw] = useState(false)
  const hasParsed = event.parsed !== null && event.parsed !== undefined
  const colorClass = sseColorClass(event.event)

  // 阶段AW：parsed 是字符串时（如 data: "{\"a\":1}" 合法 JSON 字符串场景）
  // 二次解析若为 object/array 则走 JsonTree 渲染，与 JSON 美化按钮行为一致。
  let renderValue = null
  let useJsonTree = false
  if (hasParsed) {
    if (typeof event.parsed === 'object' && event.parsed !== null) {
      renderValue = event.parsed
      useJsonTree = true
    } else if (typeof event.parsed === 'string') {
      const r = tryParseJsonObject(event.parsed)
      if (r.ok) {
        renderValue = r.data
        useJsonTree = true
      } else {
        renderValue = event.parsed // 按普通文本渲染
      }
    } else {
      renderValue = event.parsed // 标量
    }
  }

  const title = isComplete
    ? t('chatAnalysis.sseCompleteResponse')
    : `event: ${event.event || '(default)'}`

  return (
    <div className={`sse-event-card ${colorClass}${isComplete ? ' sse-complete-card' : ''}`}>
      <div className="sse-event-card-head" onClick={() => setOpen((o) => !o)}>
        {!isComplete ? <span className="sse-event-index">#{index}</span> : null}
        <span className="sse-event-type-dot" style={{ background: sseDotColor(event.event) }} />
        <span className="sse-event-name">
          {isComplete ? title : (
            <>event: <SearchText query={query} text={event.event || '(default)'} /></>
          )}
        </span>
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
          {useJsonTree ? (
            <div className="sse-event-json">
              <JsonTree value={renderValue} query={query} toolbar={false} />
            </div>
          ) : hasParsed ? (
            // 标量值（数字/布尔/null）—— 直接展示
            <pre className="log-box sse-event-data"><SearchText query={query} text={String(renderValue)} /></pre>
          ) : (
            // parsed=null —— 展示 raw（可能非 JSON 文本）
            <pre className="log-box sse-event-data"><SearchText query={query} text={event.raw || `(${t('common.noData')})`} /></pre>
          )}
          <div className="sse-event-raw-toggle">
            <button type="button" className="btn btn-sm" onClick={() => setShowRaw((s) => !s)}>
              {showRaw ? t('common.hide') : t('common.show')}
            </button>
          </div>
          {showRaw ? (
            <pre className="log-box sse-event-raw"><SearchText query={query} text={event.raw || `(${t('common.none')})`} /></pre>
          ) : null}
        </div>
      ) : null}
    </div>
  )
}
