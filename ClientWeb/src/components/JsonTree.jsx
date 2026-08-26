// ClientWeb/src/components/JsonTree.jsx
//
// JSON 美化视图组件：递归渲染可折叠 JSON 树。
// v2.0.7x 阶段AM：对话详情 Modal 的「JSON 美化」视图专用。

import { useI18n } from '../i18n'
import {
  buildJsonTreeNodes,
  escapeJsonString,
  NODE_KIND_PRIMITIVE,
  NODE_KIND_CONTAINER,
  NODE_KIND_OVERFLOW,
  NODE_KIND_EMPTY,
} from '../shared/json'

export default function JsonTree({ value }) {
  const { t } = useI18n()
  if (value === null || value === undefined) {
    return <div className="muted">({t('common.none')})</div>
  }
  let parsed = value
  if (typeof value === 'string') {
    try {
      parsed = JSON.parse(value)
    } catch {
      return <pre className="log-box">{value}</pre>
    }
  }
  const nodes = buildJsonTreeNodes(parsed)
  return <div className="json-tree">{nodes.map((n, i) => renderNode(n, i))}</div>

  function renderNode(node, idx) {
    if (node.kind === NODE_KIND_PRIMITIVE) {
      let valueSpan = null
      if (node.type === 'string') {
        valueSpan = <span className="string">"{escapeJsonString(node.value)}"</span>
      } else if (node.type === 'number') {
        valueSpan = <span className="number">{String(node.value)}</span>
      } else if (node.type === 'boolean') {
        valueSpan = <span className="boolean">{String(node.value)}</span>
      } else if (node.type === 'null') {
        valueSpan = <span className="null">null</span>
      }
      return (
        <div className="json-tree-row" key={`p-${idx}`}>
          {node.key ? <span className="key">{node.key}</span> : null}
          {valueSpan}
        </div>
      )
    }
    if (node.kind === NODE_KIND_CONTAINER) {
      const summaryText = node.key ? `${node.key}: ${node.typeLabel}` : node.typeLabel
      return (
        <details key={`c-${idx}`} className="json-tree-container" open={node.open}>
          <summary>{summaryText}</summary>
          <div className="json-tree-body">
            {node.children.map((c, ci) => renderChild(c, ci))}
          </div>
        </details>
      )
    }
    if (node.kind === NODE_KIND_EMPTY) {
      return (
        <div className="json-tree-row" key={`e-${idx}`}>
          {node.key ? <span className="key">{node.key}</span> : null}
          <span className="muted">{node.emptyText}</span>
        </div>
      )
    }
    if (node.kind === NODE_KIND_OVERFLOW) {
      return (
        <div className="json-tree-overflow" key={`o-${idx}`}>
          {node.key ? <span className="key">{node.key}</span> : null}
          {node.typeLabel ? <span>: {node.typeLabel}</span> : null}
          {' '}{node.note}
        </div>
      )
    }
    return null
  }

  function renderChild(child, ci) {
    return (
      <div className="json-tree-row" key={`r-${ci}`}>
        {child.nodes.map((n, ni) => renderNode(n, `${ci}-${ni}`))}
      </div>
    )
  }
}
