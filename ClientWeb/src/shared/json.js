// ClientWeb/src/shared/json.js
//
// JSON 美化与可折叠 JSON 树数据结构构造工具。
// v2.0.7x 阶段AM：从 ChatAnalysis.jsx 原样抽离 prettyJSON；新增 buildJsonTreeNodes。
// v2.0.7x 阶段AR：节点上限策略调整 ——
//   大对象/数组不再"截断为 OVERFLOW"导致无可展开按钮，
//   而是返回正常 CONTAINER 并标记 truncated：默认折叠，summary 显示
//   "对象过大（已截断显示前 N 项）" + 展开按钮，用户可点开逐项浏览。
//
// 设计要点：
//   - 本文件不包含 JSX（项目 vite 未为 .js 启用 JSX 解析）。
//   - buildJsonTreeNodes 返回纯 JSON 结构（树形对象数组），由 components/JsonTree.jsx 负责 JSX 渲染。
//   - 这样保持工具层无 React 依赖，组件层专注 UI。
//   - 配额制（remainingBudget）：进入 buildNodes 时扣除 1 个配额给自身；
//     进入 children 递归前若配额已耗尽，则当前容器 truncated，children
//     按 truncatedRender 截断构建。

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
 *   - 'container'：对象或数组，可折叠（truncated=true 时默认折叠+截断渲染）
 *   - 'overflow'  ：嵌套层级过深（独立字段，无 children）
 *   - 'empty'     ：空对象/空数组
 */
export const NODE_KIND_PRIMITIVE = 'primitive'
export const NODE_KIND_CONTAINER = 'container'
export const NODE_KIND_OVERFLOW = 'overflow'
export const NODE_KIND_EMPTY = 'empty'

/**
 * 默认节点数上限：单棵子树递归总节点数超过此值时，进入"截断模式"，
 * 容器节点会被标记 truncated，默认折叠，summary 给出截断提示，
 * 渲染时仅展示前 truncatedRender 项，避免一次性渲染数万节点造成卡顿。
 *
 * 用户主动点击折叠按钮展开后，可继续浏览（最多渲染 truncatedRender 个子项，
 * 超出部分显示"剩余 N 项未渲染"，防止浏览器无响应）。
 */
export const DEFAULT_MAX_NODES = 500
export const DEFAULT_TRUNCATED_RENDER = 200

/**
 * 构造 JSON 树节点数组（自上而下递归）。
 *
 * 节点结构：
 *   {
 *     kind: 'primitive' | 'container' | 'overflow' | 'empty',
 *     key: string,        // 当前节点名（数组项为 "[idx]"）
 *     typeLabel: string,  // 类型提示文本，如 Object{3} / Array(2) / string
 *     open: boolean,      // 顶层默认展开，深层默认折叠
 *     truncated?: boolean,// container 超过节点上限时为 true（默认折叠 + 截断渲染）
 *     truncatedRender?: number, // truncated 时最多渲染的子项数
 *     truncatedRemain?: number, // truncated 时剩余未渲染的子项数
 *     value?: any,        // primitive 时附带原始值
 *     isString?: boolean, // primitive 时是否字符串（用于加引号 + 转义）
 *     children?: JsonNode[] // container 时附带子节点
 *     note?: string,      // overflow / empty 时附带提示文本
 *   }
 *
 * @param {any} value
 * @param {object} [opts]
 * @param {number} [opts.maxDepth=6]
 * @param {number} [opts.maxNodes=500]   单棵子树节点数上限；超过后该容器 truncated
 * @param {number} [opts.truncatedRender=200] truncated 时最多渲染子项数
 * @returns {JsonNode[]}
 */
export function buildJsonTreeNodes(value, opts = {}) {
  const maxDepth = opts.maxDepth || 6
  const maxNodes = opts.maxNodes || DEFAULT_MAX_NODES
  const truncatedRender = opts.truncatedRender || DEFAULT_TRUNCATED_RENDER
  // 根级扣除 1 个配额给自身
  return buildNodes(value, '', false, 0, maxDepth, maxNodes, truncatedRender, maxNodes - 1)
}

function buildNodes(value, keyName, isArrayItem, depth, maxDepth, maxNodes, truncatedRender, budget) {
  // budget：当前节点及其子树可用的剩余节点配额
  if (budget < 0) {
    // 配额已耗尽 —— 直接视为 truncated 的空容器（用 OVERFLOW 表示"已折叠"）
    return [{ kind: NODE_KIND_OVERFLOW, key: keyName, note: '（对象过大，已折叠；超过节点上限）' }]
  }
  const t = typeOf(value)
  if (t === 'string' || t === 'number' || t === 'boolean' || t === 'null') {
    return [{
      kind: NODE_KIND_PRIMITIVE,
      key: keyName,
      type: t,
      value,
      isString: t === 'string',
    }]
  }
  // object / array —— 自身占 1 个配额
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

  // 配额预算：递归每个子项都需要至少 1 个配额（其自身）；
  // 当剩余配额不足以覆盖所有子项时，本节点 truncated，children 按 truncatedRender 截断构建。
  const remaining = budget - 1 // 扣除本节点
  if (remaining < entries.length) {
    // 当前配额不足以遍历全部子项 —— 标 truncated
    const visibleCount = Math.min(entries.length, truncatedRender)
    const children = entries.slice(0, visibleCount).map(([k, v]) => ({
      childKey: isArr ? `[${k}]` : String(k),
      isArrayItem: isArr,
      nodes: buildNodes(v, isArr ? `[${k}]` : String(k), isArr, depth + 1, maxDepth, maxNodes, truncatedRender, remaining),
    }))
    return [{
      kind: NODE_KIND_CONTAINER,
      key: keyName,
      typeLabel,
      open: false,
      truncated: true,
      truncatedRender: visibleCount,
      truncatedRemain: Math.max(0, entries.length - visibleCount),
      children,
    }]
  }

  // 配额充足：正常递归
  const open = depth < 1
  const children = entries.map(([k, v]) => ({
    childKey: isArr ? `[${k}]` : String(k),
    isArrayItem: isArr,
    nodes: buildNodes(v, isArr ? `[${k}]` : String(k), isArr, depth + 1, maxDepth, maxNodes, truncatedRender, remaining),
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