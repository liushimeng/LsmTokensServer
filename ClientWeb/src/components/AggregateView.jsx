// ClientWeb/src/components/AggregateView.jsx
//
// SSE 聚合视图组件：分块展示 usage / event_types / tool_calls / 文本。
// v2.0.7x 阶段AM：对话详情 Modal 的「聚合解析」视图专用。
// v2.0.7x 阶段AP：Tokens 用量增加比例条可视化；事件类型标签增加颜色区分。
// v2.0.7x 阶段AR：全面增强 ——
//   1) 非流式完整响应（isComplete=true）显示紫色徽标，避免"暂无数据"误导；
//   2) 对无 usage / 无 text 的纯错误响应（status 非 2xx），给出明确提示；
//   3) 支持 cache_creation_input_tokens / cache_read_input_tokens 展示（Anthropic）。
// v2.0.75 阶段AU：新增可选 query prop —— 聚合文本块 / 工具名 / 事件类型标签
// 在面板布局内查找高亮（计数与焦点滚动由 InlineDetailRow DOM 机制统一处理）。
// v2.0.76 阶段AV：渲染顺序调整 ——
//   1) 聚合文本（Text）前置为主信息，紧接 banner 之后；多事件流的 Events 标签列
//      不再遮挡用户最想看到的"聚合出的对话文本"；
//   2) Tokens / Events / Tools 三块合并到一个 <details> 中默认折叠，避免一打开
//      聚合解析就铺满统计标签列；保留所有数据可见性（用户主动展开查看）；
//   3) 新增 .agg-summary-row 汇总行（紧凑 meta：一行展示 tokens / 事件数 / 工具数），
//      作为元信息补充，避免聚合文本孤立无背景。
// v2.0.77 阶段AW：聚合文本片段级 JSON 识别 ——
//   1) 用 splitAggregateTextParts 按"独立片段是否为合法 JSON object/array"切分；
//   2) JSON 片段改用 JsonTree 渲染（toolbar=false），与 JSON 美化按钮行为一致：
//      标准排版 / 折叠 / 语法高亮 / 超长字符串保护 / 查找高亮 / 渲染预算；
//   3) 普通片段保持 <pre> + SearchText 渲染；片段之间细线分隔；
//   4) buildViewText / aggregateToText 契约不变（复制文本仍为完整拼接）。

import { useI18n } from '../i18n'
import SearchText from './SearchText'
import JsonTree from './JsonTree'
import { splitAggregateTextParts } from '../shared/sse'

// 单片段渲染器（阶段AW）：按片段类型选择渲染器
function FragmentBlock({ index, frag, query }) {
  if (frag.kind === 'json') {
    return (
      <div className="agg-frag agg-frag-json">
        <div className="agg-frag-label">📦 JSON 块 #{index + 1}</div>
        <div className="agg-frag-json-tree">
          <JsonTree value={frag.value} query={query} toolbar={false} />
        </div>
      </div>
    )
  }
  // 普通文本片段
  return (
    <div className="agg-frag agg-frag-text">
      <pre className="log-box agg-text-content"><SearchText query={query} text={frag.value} /></pre>
    </div>
  )
}

// 事件类型颜色映射（阶段AP）
const EVENT_TYPE_COLORS = {
  'message_start': { bg: '#dbeafe', color: '#1e40af' },
  'message_stop': { bg: '#dbeafe', color: '#1e40af' },
  'message_delta': { bg: '#fef3c7', color: '#92400e' },
  'content_block_start': { bg: '#dcfce7', color: '#166534' },
  'content_block_stop': { bg: '#dcfce7', color: '#166534' },
  'content_block_delta': { bg: '#dcfce7', color: '#166534' },
  'complete': { bg: '#ede9fe', color: '#5b21b6' },
}
const DEFAULT_EVENT_COLOR = { bg: '#e0e7ff', color: '#3730a3' }

export default function AggregateView({ result, query }) {
  const { t } = useI18n()
  if (!result) return <div className="agg-empty">({t('common.noData')})</div>
  const usage = result.usage || {}
  const eventTypes = result.eventTypes || {}
  const toolCalls = result.toolCalls || []
  const textParts = result.textParts || []
  const isComplete = !!result.isComplete

  const inFinal = usage.input_tokens_final ?? usage.input_tokens ?? 0
  const outFinal = usage.output_tokens_final ?? usage.output_tokens ?? 0
  const totalTokens = inFinal + outFinal
  const inPct = totalTokens > 0 ? Math.round((inFinal / totalTokens) * 100) : 0
  const outPct = totalTokens > 0 ? 100 - inPct : 0

  // 阶段AR：四块全空 → 给出"无聚合数据"提示，避免"暂无数据"误导
  const isAllEmpty =
    totalTokens === 0 && toolCalls.length === 0 && textParts.length === 0 &&
    Object.keys(eventTypes).length === 0

  // 阶段AY：有 SSE 事件但无文本内容（如纯工具调用流、错误响应、message_stop 终止）
  // → 给出明确解释，避免"Text (0) (无)"误导用户以为解析失败
  const hasEventsButNoText =
    textParts.length === 0 && Object.keys(eventTypes).length > 0 &&
    (totalTokens > 0 || toolCalls.length > 0)

  // 阶段AV：汇总行 meta（tokens / 事件数 / 工具数 / 文本片段数）
  const eventTotalCount = Object.values(eventTypes).reduce((s, n) => s + n, 0)
  const hasStats = totalTokens > 0 || eventTotalCount > 0 || toolCalls.length > 0

  // 阶段AW：片段级 JSON 识别
  const fragments = splitAggregateTextParts(textParts)
  const jsonFragmentCount = fragments.filter((f) => f.kind === 'json').length

  return (
    <div className="aggregate-view">
      {isComplete ? (
        <div className="agg-complete-banner">
          🟣 {t('chatAnalysis.sseCompleteResponseHint')}
        </div>
      ) : null}

      {isAllEmpty ? (
        <div className="agg-empty-detail">
          ({t('chatAnalysis.aggNoSummary')})
        </div>
      ) : null}

      {/* 阶段AY：有 SSE 事件但聚合出 0 文本（如纯工具调用流、错误响应、终止流）→
          给出明确解释，避免"Text (0) (无)"误导；下方仍展示 Text 块与统计信息。 */}
      {hasEventsButNoText ? (
        <div className="agg-no-text-hint">
          💡 {toolCalls.length > 0
            ? t('chatAnalysis.aggNoTextWithTools', { toolCount: toolCalls.length })
            : t('chatAnalysis.aggNoTextHint')}
        </div>
      ) : null}

      {/* 阶段AV：聚合文本前置为主信息（用户最想看的内容） */}
      <div className="agg-block agg-text">
        <div className="agg-block-title">
          📝 Text ({textParts.length}
          {jsonFragmentCount > 0 ? ` · ${jsonFragmentCount} JSON` : ''})
        </div>
        <div className="agg-block-body">
          {fragments.length === 0 ? (
            <span className="muted">({t('common.none')})</span>
          ) : (
            <div className="agg-text-fragments">
              {fragments.map((frag, i) => (
                <FragmentBlock key={`agg-frag-${i}`} index={i} frag={frag} query={query} />
              ))}
            </div>
          )}
        </div>
      </div>

      {/* 阶段AV：汇总信息行 —— 一行紧凑展示 tokens / 事件 / 工具元数据。
          提供快速上下文，避免聚合文本"孤立无背景"。 */}
      {hasStats ? (
        <div className="agg-summary-row">
          {totalTokens > 0 ? (
            <span className="agg-summary-item">
              <span className="agg-summary-label">Tokens</span>
              <span className="agg-summary-val">
                in {inFinal.toLocaleString()} / out {outFinal.toLocaleString()}
                {' '}<span className="agg-summary-sub">({inPct}% / {outPct}%)</span>
              </span>
            </span>
          ) : null}
          {eventTotalCount > 0 ? (
            <span className="agg-summary-item">
              <span className="agg-summary-label">Events</span>
              <span className="agg-summary-val">{eventTotalCount}</span>
            </span>
          ) : null}
          {toolCalls.length > 0 ? (
            <span className="agg-summary-item">
              <span className="agg-summary-label">Tools</span>
              <span className="agg-summary-val">{toolCalls.length}</span>
            </span>
          ) : null}
          {(usage.cache_creation_input_tokens || usage.cache_read_input_tokens) ? (
            <span className="agg-summary-item">
              <span className="agg-summary-label">Cache</span>
              <span className="agg-summary-val">
                {usage.cache_creation_input_tokens ? `create ${usage.cache_creation_input_tokens.toLocaleString()}` : ''}
                {usage.cache_creation_input_tokens && usage.cache_read_input_tokens ? ' / ' : ''}
                {usage.cache_read_input_tokens ? `read ${usage.cache_read_input_tokens.toLocaleString()}` : ''}
              </span>
            </span>
          ) : null}
        </div>
      ) : null}

      {/* 阶段AV：Tokens / Events / Tools 三块合并为默认折叠的 details，
          既保留完整统计可见性，又避免一打开就铺满标签列。
          展开后内容与既有阶段AP/AR 行为一致（含 cache 标签）。 */}
      {hasStats ? (
        <details className="agg-details">
          <summary className="agg-details-summary">📊 详细统计（Tokens / Events / Tools）</summary>
          <div className="agg-details-body">
            {totalTokens > 0 ? (
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
                  {(usage.cache_creation_input_tokens || usage.cache_read_input_tokens) ? (
                    <div className="agg-cache-info">
                      {usage.cache_creation_input_tokens ? (
                        <span className="agg-cache-tag">cache_creation: {usage.cache_creation_input_tokens.toLocaleString()}</span>
                      ) : null}
                      {usage.cache_read_input_tokens ? (
                        <span className="agg-cache-tag">cache_read: {usage.cache_read_input_tokens.toLocaleString()}</span>
                      ) : null}
                    </div>
                  ) : null}
                  <div className="agg-usage-bar">
                    <div className="bar-input" style={{ width: `${inPct}%` }} title={`Input ${inPct}%`} />
                    <div className="bar-output" style={{ width: `${outPct}%` }} title={`Output ${outPct}%`} />
                  </div>
                </div>
              </div>
            ) : null}

            {Object.keys(eventTypes).length > 0 ? (
              <div className="agg-block agg-event-types">
                <div className="agg-block-title">📋 Events</div>
                <div className="agg-block-body">
                  <ul className="agg-tag-list">
                    {Object.entries(eventTypes).map(([k, v]) => {
                      const c = EVENT_TYPE_COLORS[k] || DEFAULT_EVENT_COLOR
                      return (
                        <li key={k} className="agg-tag" style={{ background: c.bg, color: c.color }}>
                          <span className="agg-tag-name"><SearchText query={query} text={k || '(default)'} /></span>
                          <span className="agg-tag-count">× {v}</span>
                        </li>
                      )
                    })}
                  </ul>
                </div>
              </div>
            ) : null}

            {toolCalls.length > 0 ? (
              <div className="agg-block agg-tool-calls">
                <div className="agg-block-title">🔧 Tools</div>
                <div className="agg-block-body">
                  <ul className="agg-tag-list">
                    {toolCalls.map((tl, i) => (
                      <li key={`${tl}-${i}`} className="agg-tag agg-tag-tool">
                        <span className="agg-tag-name"><SearchText query={query} text={tl || '(unnamed)'} /></span>
                      </li>
                    ))}
                  </ul>
                </div>
              </div>
            ) : null}
          </div>
        </details>
      ) : null}
    </div>
  )
}