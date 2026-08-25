// ClientWeb/src/shared/json.js
//
// JSON 美化与可折叠 JSON 树数据结构构造工具。
// v2.0.7x 阶段AM：从 ChatAnalysis.jsx 原样抽离 prettyJSON；新增 buildJsonTreeNodes。
//
// 设计要点：
//   - 本文件不包含 JSX（项目 vite 未为 .js 启用 JSX 解析）。
//   - buildJsonTreeNodes 返回纯 JSON 结构（树形对象数组），由 components/JsonTree.jsx 负责 JSX 渲染。
//   - 这样保持工具层无 React 依赖，组件层专注 UI。

/**
 * 美化 JSON 字符串。失败时返回原字符串（不抛异常，便于兜底展示）。
 *
 * @param {string} s
 * @returns {string}
 */
export function prettyJSON(s) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

/**
 * 节点类型：
 *   - 'primitive'：基本类型（含字符串/数字/布尔/null）
 *   - 'container'：对象或数组，可折叠
 *   - 'overflow'  ：嵌套过深或节点过多，提示已折叠
 *   - 'empty'     ：空对象/空数组
 */
export const NODE_KIND_PRIMITIVE = 'primitive'
export const NODE_KIND_CONTAINER = 'container'
export const NODE_KIND_OVERFLOW = 'overflow'
export const NODE_KIND_EMPTY = 'empty'

/**
 * 构造 JSON 树节点数组（自上而下递归）。
 *
 * 节点结构：
 *   {
 *     kind: 'primitive' | 'container' | 'overflow' | 'empty',
 *     key: string,        // 当前节点名（数组项为 "[idx]"）
 *     typeLabel: string,  // 类型提示文本，如 Object{3} / Array(2) / string
 *     open: boolean,      // 顶层默认展开，深层默认折叠
 *     value?: any,        // primitive 时附带原始值
 *     isString?: boolean, // primitive 时是否字符串（用于加引号 + 转义）
 *     children?: JsonNode[] // container 时附带子节点
 *     note?: string,      // overflow / empty 时附带提示文本
 *   }
 *
 * @param {any} value
 * @param {object} [opts]
 * @param {number} [opts.maxDepth=6]
 * @param {number} [opts.maxNodes=500]
 * @returns {JsonNode[]}
 */
export function buildJsonTreeNodes(value, opts = {}) {
  const maxDepth = opts.maxDepth || 6
  const maxNodes = opts.maxNodes || 500
  const counter = { count: 0 }
  return buildNodes(value, '', false, 0, maxDepth, maxNodes, counter)
}

function buildNodes(value, keyName, isArrayItem, depth, maxDepth, maxNodes, counter) {
  if (counter.count > maxNodes) {
    return [{ kind: NODE_KIND_OVERFLOW, key: keyName, note: `（对象过大，已折叠；超过 ${maxNodes} 节点）` }]
  }
  const t = typeOf(value)
  if (t === 'string' || t === 'number' || t === 'boolean' || t === 'null') {
    counter.count++
    return [{
      kind: NODE_KIND_PRIMITIVE,
      key: keyName,
      type: t,
      value,
      isString: t === 'string',
    }]
  }
  // object / array
  counter.count++
  const isArr = Array.isArray(value)
  const entries = isArr
    ? value.map((v, i) => [i, v])
    : Object.entries(value || {})
  const typeLabel = isArr ? `Array(${entries.length})` : `Object{${entries.length}}`
  if (entries.length === 0) {
    return [{
      kind: NODE_KIND_EMPTY,
      key: keyName,
      typeLabel,
      emptyText: isArr ? '[]' : '{}',
    }]
  }
  if (depth > maxDepth) {
    return [{
      kind: NODE_KIND_OVERFLOW,
      key: keyName,
      typeLabel,
      note: `（嵌套层级超过 ${maxDepth}，已折叠）`,
    }]
  }
  const open = depth < 1
  const children = entries.map(([k, v]) => ({
    childKey: isArr ? `[${k}]` : String(k),
    isArrayItem: isArr,
    nodes: buildNodes(v, isArr ? `[${k}]` : String(k), isArr, depth + 1, maxDepth, maxNodes, counter),
  }))
  return [{
    kind: NODE_KIND_CONTAINER,
    key: keyName,
    typeLabel,
    open,
    children,
  }]
}

function typeOf(v) {
  if (v === null) return 'null'
  if (Array.isArray(v)) return 'array'
  return typeof v
}

export function escapeJsonString(s) {
  return String(s).replace(/"/g, '\\"').replace(/\n/g, '\\n').replace(/\r/g, '\\r').replace(/\t/g, '\\t')
}