// ClientWeb/src/components/JsonTree.jsx
//
// JSON 美化视图组件：递归渲染可折叠 JSON 树。
// v2.0.7x 阶段AM：对话详情 Modal 的「JSON 美化」视图专用。
//
// 设计要点：
//   - 委托给 shared/json.js 的 buildJsonTreeNodes 构造节点；
//   - 本组件专注于 JSX 渲染，保持 shared 层无 React 依赖；
//   - 顶层默认展开，更深层级默认折叠；
//   - 大对象（>500 节点）自动折叠 + 提示，避免卡顿。

import {
  buildJsonTreeNodes,
  escapeJsonString,
  NODE_KIND_PRIMITIVE,
  NODE_KIND_CONTAINER,
  NODE_KIND_OVERFLOW,
  NODE_KIND_EMPTY,
} from '../shared/json'

export default function JsonTree({ value }) {
  if (value === null || value === undefined) {
    return <div className="muted">（空）</div>
  }
  let parsed = value
  if (typeof value === 'string') {
    try {
      parsed = JSON.parse(value)
    } catch {
      // 解析失败时回退为原始字符串展示
      return <pre className="log-box">{value}</pre>
    }
  }
  const nodes = buildJsonTreeNodes(parsed)
  return <div className="json-tree">{nodes.map((n, i) => renderNode(n, i))}</div>
}

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