// ClientWeb/src/components/JsonTree.jsx
//
// JSON 美化视图组件：递归渲染可折叠 JSON 树。
// v2.0.7x 阶段AM：对话详情 Modal 的「JSON 美化」视图专用。
// v2.0.7x 阶段AR：JSON 树折叠/展开修复 ——
//   1) 根级容器不再用 <details> 包裹（顶层无折叠意义且会遮蔽所有子级折叠按钮）；
//   2) 每个对象/数组节点都用独立的 <details> 渲染，子树可独立折叠/展开；
//   3) 递归结构保持 indent + 虚线引导线，深层节点的折叠按钮始终可见可点。

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
  // 根级只有一个 NODE_KIND_CONTAINER 节点（来自 buildJsonTreeNodes），
  // 这里直接展开 children —— 顶层不渲染 <details>，保证每个深层节点都
  // 有自己独立的折叠按钮。
  if (nodes.length === 1 && nodes[0].kind === NODE_KIND_CONTAINER) {
    const root = nodes[0]
    return (
      <div className="json-tree">
        {root.children.map((c, ci) => renderChild(c, `r-${ci}`))}
      </div>
    )
  }
  return <div className="json-tree">{nodes.map((n, i) => renderNode(n, `n-${i}`))}</div>

  function renderNode(node, key) {
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
        <div className="json-tree-row" key={key}>
          {node.key ? <span className="key">{node.key}</span> : null}
          {valueSpan}
        </div>
      )
    }
    if (node.kind === NODE_KIND_CONTAINER) {
      const baseLabel = node.key ? `${node.key}: ${node.typeLabel}` : node.typeLabel
      // 截断模式 summary 加上"对象过大"提示，summary 仍可点击展开。
      const summaryText = node.truncated
          ? (node.key ? `${node.key}: ${node.typeLabel} · 对象过大（已截断）` : `${node.typeLabel} · 对象过大（已截断）`)
          : baseLabel
      return (
        <details key={key} className={`json-tree-container${node.truncated ? ' json-tree-truncated' : ''}`} open={node.open}>
          <summary>{summaryText}</summary>
          <div className="json-tree-body">
            {node.children.map((c, ci) => renderChild(c, `${key}-${ci}`))}
            {node.truncated && node.truncatedRemain > 0 ? (
              <div className="json-tree-truncated-more">
                … 剩余 {node.truncatedRemain} 项未渲染（防止浏览器卡顿）
              </div>
            ) : null}
          </div>
        </details>
      )
    }
    if (node.kind === NODE_KIND_EMPTY) {
      return (
        <div className="json-tree-row" key={key}>
          {node.key ? <span className="key">{node.key}</span> : null}
          <span className="muted">{node.emptyText}</span>
        </div>
      )
    }
    if (node.kind === NODE_KIND_OVERFLOW) {
      return (
        <div className="json-tree-overflow" key={key}>
          {node.key ? <span className="key">{node.key}</span> : null}
          {node.typeLabel ? <span>: {node.typeLabel}</span> : null}
          {' '}{node.note}
        </div>
      )
    }
    return null
  }

  // renderChild：每个 child 包含 1 个 node（来自 buildNodes 返回的单元素数组）。
  // 直接渲染该 node 即可 —— 不再包一层额外的 .json-tree-row 防止破坏 children 嵌套层级。
  function renderChild(child, key) {
    if (!child || !child.nodes || child.nodes.length === 0) return null
    return <div className="json-tree-row" key={key}>{child.nodes.map((n, ni) => renderNode(n, `${key}-n${ni}`))}</div>
  }
}
