// ClientWeb/src/components/AggregateView.jsx
//
// SSE 聚合视图组件：分块展示 usage / event_types / tool_calls / 文本。
// v2.0.7x 阶段AM：对话详情 Modal 的「聚合解析」视图专用。
// v2.0.7x 阶段AP：Tokens 用量增加比例条可视化；事件类型标签增加颜色区分。

import { useI18n } from '../i18n'

// 事件类型颜色映射（阶段AP）
const EVENT_TYPE_COLORS = {
  'message_start': { bg: '#dbeafe', color: '#1e40af' },
  'message_stop': { bg: '#dbeafe', color: '#1e40af' },
  'message_delta': { bg: '#fef3c7', color: '#92400e' },
  'content_block_start': { bg: '#dcfce7', color: '#166534' },
  'content_block_stop': { bg: '#dcfce7', color: '#166534' },
  'content_block_delta': { bg: '#dcfce7', color: '#166534' },
}
const DEFAULT_EVENT_COLOR = { bg: '#e0e7ff', color: '#3730a3' }

export default function AggregateView({ result }) {
  const { t } = useI18n()
  if (!result) return <div className="agg-empty">({t('common.noData')})</div>
  const usage = result.usage || {}
  const eventTypes = result.eventTypes || {}
  const toolCalls = result.toolCalls || []
  const textParts = result.textParts || []

  const inFinal = usage.input_tokens_final ?? usage.input_tokens ?? 0
  const outFinal = usage.output_tokens_final ?? usage.output_tokens ?? 0
  const inSum = usage.input_tokens || 0
  const outSum = usage.output_tokens || 0
  const totalTokens = inFinal + outFinal
  const inPct = totalTokens > 0 ? Math.round((inFinal / totalTokens) * 100) : 0
  const outPct = totalTokens > 0 ? 100 - inPct : 0

  return (
    <div className="aggregate-view">
      <div className="agg-block agg-usage">
        <div className="agg-block-title">📊 Tokens</div>
        <div className="agg-block-body">
          <div className="agg-usage-grid">
            <div className="agg-usage-item">
              <span className="agg-usage-label">Input</span>
              <span className="agg-usage-val input">{inFinal.toLocaleString()}</span>
            </div>
            <div className="agg-usage-item">
              <span className="agg-usage-label">Output</span>
              <span className="agg-usage-val output">{outFinal.toLocaleString()}</span>
            </div>
          </div>
          {totalTokens > 0 ? (
            <div className="agg-usage-bar">
              <div className="bar-input" style={{ width: `${inPct}%` }} title={`Input ${inPct}%`} />
              <div className="bar-output" style={{ width: `${outPct}%` }} title={`Output ${outPct}%`} />
            </div>
          ) : null}
        </div>
      </div>

      <div className="agg-block agg-event-types">
        <div className="agg-block-title">📋 Events</div>
        <div className="agg-block-body">
          {Object.keys(eventTypes).length === 0 ? (
            <span className="muted">({t('common.none')})</span>
          ) : (
            <ul className="agg-tag-list">
              {Object.entries(eventTypes).map(([k, v]) => {
                const c = EVENT_TYPE_COLORS[k] || DEFAULT_EVENT_COLOR
                return (
                  <li key={k} className="agg-tag" style={{ background: c.bg, color: c.color }}>
                    <span className="agg-tag-name">{k || '(default)'}</span>
                    <span className="agg-tag-count">× {v}</span>
                  </li>
                )
              })}
            </ul>
          )}
        </div>
      </div>

      <div className="agg-block agg-tool-calls">
        <div className="agg-block-title">🔧 Tools</div>
        <div className="agg-block-body">
          {toolCalls.length === 0 ? (
            <span className="muted">({t('common.none')})</span>
          ) : (
            <ul className="agg-tag-list">
              {toolCalls.map((tl, i) => (
                <li key={`${tl}-${i}`} className="agg-tag agg-tag-tool">
                  <span className="agg-tag-name">{tl || '(unnamed)'}</span>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>

      <div className="agg-block agg-text">
        <div className="agg-block-title">📝 Text ({textParts.length})</div>
        <div className="agg-block-body">
          {textParts.length === 0 ? (
            <span className="muted">({t('common.none')})</span>
          ) : (
            <pre className="log-box agg-text-content">{textParts.join('')}</pre>
          )}
        </div>
      </div>
    </div>
  )
}
