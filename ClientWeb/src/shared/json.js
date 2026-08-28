// ClientWeb/src/shared/json.js
//
// JSON 美化与可折叠 JSON 树的纯数据工具层。
// v2.0.7x 阶段AM：从 ChatAnalysis.jsx 原样抽离 prettyJSON；新增 buildJsonTreeNodes。
// v2.0.7x 阶段AR：节点上限策略（配额制 + truncated 截断）。
// v2.0.74 阶段AS：配额制整体废弃 —— 数据层截断导致"显示不全、折叠按钮缺失"。
//   改为惰性渲染配套的纯工具集：
//   - parseJsonSafely            安全解析（对象直通 / 字符串解析 / 失败原因）
//   - countJsonNodes             子树节点计数（带 cap 早退，用于默认展开判定）
//   - entriesOf / childPathOf    统一对象/数组子项枚举与节点路径构造（保证唯一）
//   - collectContainerPaths      收集 depth < maxLevel 的全部容器路径（"展开至 N 层"）
//   - collectDefaultExpandedPaths 默认展开集（根 + 子树节点数 ≤ 60 的小容器）
//   防卡顿边界从数据层移到渲染层：组件只渲染可见节点（分页 + 渲染预算），
//   解析数据始终完整，零丢失。
//
// 设计要点：
//   - 本文件不包含 JSX（项目 vite 未为 .js 启用 JSX 解析）。
//   - 全部为纯函数，无 React/DOM 依赖，便于 node 自检脚本直接验证。

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

// ===== 惰性渲染树常量 =====

/** 展开的容器默认渲染的子项数（分页页大小；「显示更多」每次追加一页） */
export const JSON_TREE_PAGE_SIZE = 100
/** 超过此长度的字符串值默认截断显示，按需「显示全部」 */
export const JSON_STRING_INLINE_LIMIT = 500
/** 子树总节点数 ≤ 此值的容器默认自动展开（小 JSON 全开，大 JSON 浅开） */
export const JSON_SMALL_TREE_NODES = 60
/** 单次渲染通行预算（行数）：防「展开至 5 层」级联渲染数万 DOM 行卡死 */
export const JSON_RENDER_BUDGET = 4000

/**
 * 安全解析 JSON。
 * - null/undefined → { ok:false, reason:'empty' }
 * - 非字符串（已解析对象）→ 直通 { ok:true, data }
 * - 字符串 → JSON.parse，失败 → { ok:false, reason:'parse-error' }
 *
 * @param {any} value
 * @returns {{ok: boolean, data?: any, reason?: string}}
 */
export function parseJsonSafely(value) {
  if (value === null || value === undefined) return { ok: false, reason: 'empty' }
  if (typeof value === 'object') return { ok: true, data: value }
  if (typeof value !== 'string') return { ok: true, data: value }
  const s = value.trim()
  try {
    return { ok: true, data: JSON.parse(s) }
  } catch {
    return { ok: false, reason: 'parse-error' }
  }
}

/**
 * 统一子项枚举：数组返回 [index, item]，对象返回 [key, value]。
 * @param {object|any[]} v
 * @returns {[string|number, any][]}
 */
export function entriesOf(v) {
  if (Array.isArray(v)) return v.map((item, i) => [i, item])
  return Object.entries(v || {})
}

/**
 * 构造子节点路径（状态 key / React key 双用，保证唯一）。
 * 数组项：`$[0]`；对象键统一 JSON.stringify 引号包裹：`$["a.b"]` ——
 * 含点/引号等特殊字符的键与嵌套路径不会冲突。
 *
 * @param {string} parentPath
 * @param {boolean} parentIsArray
 * @param {string|number} key
 * @returns {string}
 */
export function childPathOf(parentPath, parentIsArray, key) {
  return parentIsArray ? `${parentPath}[${key}]` : `${parentPath}[${JSON.stringify(key)}]`
}

/**
 * 统计子树总节点数（容器自身 + 全部后代；标量计 1）。
 * cap 用于早退：计数超过 cap 即可停止深入（只需判断"是否超限"时传入）。
 *
 * @param {any} v
 * @param {number} [cap=Infinity]
 * @returns {number}
 */
export function countJsonNodes(v, cap = Infinity) {
  let n = 0
  let over = false
  const walk = (x) => {
    if (over) return
    n++
    if (n > cap) { over = true; return }
    if (x !== null && typeof x === 'object') {
      for (const [, child] of entriesOf(x)) {
        walk(child)
        if (over) return
      }
    }
  }
  walk(v)
  return n
}

/**
 * 收集 depth < maxLevel 的全部容器路径（含根 depth=0）。
 * 用于工具栏「展开至 N 层」：纯数据遍历，不触碰 DOM。
 *
 * @param {any} data
 * @param {number} maxLevel 层数上限（2 = 根 + 第一层子容器展开）
 * @returns {Set<string>}
 */
export function collectContainerPaths(data, maxLevel) {
  const out = new Set()
  const walk = (v, path, depth) => {
    if (v === null || typeof v !== 'object') return
    out.add(path)
    if (depth + 1 >= maxLevel) return
    const isArr = Array.isArray(v)
    for (const [k, child] of entriesOf(v)) {
      walk(child, childPathOf(path, isArr, k), depth + 1)
    }
  }
  walk(data, '$', 0)
  return out
}

/**
 * 默认展开路径集（打开详情即有内容，同时大 JSON 不卡）：
 * - 根容器始终展开；
 * - 其余容器当子树总节点数 ≤ JSON_SMALL_TREE_NODES 时自动展开（递归判定）。
 *
 * @param {any} data
 * @returns {Set<string>}
 */
export function collectDefaultExpandedPaths(data) {
  const out = new Set()
  const walk = (v, path) => {
    if (v === null || typeof v !== 'object') return
    out.add(path)
    const isArr = Array.isArray(v)
    for (const [k, child] of entriesOf(v)) {
      if (child === null || typeof child !== 'object') continue
      if (countJsonNodes(child, JSON_SMALL_TREE_NODES) > JSON_SMALL_TREE_NODES) continue
      walk(child, childPathOf(path, isArr, k))
    }
  }
  walk(data, '$')
  return out
}

/**
 * JSON 字符串值转义（用于展示层还原带引号的字符串字面量）。
 * 阶段AS 增强：补 `\\` 反斜杠、`\b`、`\f` 与 `\u0000-\u001f` 其余控制字符，
 * 与 JSON.stringify 的转义规则对齐（旧版漏掉反斜杠会导致 `\n` 歧义显示）。
 *
 * @param {string} s
 * @returns {string}
 */
export function escapeJsonString(s) {
  return String(s)
    .replace(/\\/g, '\\\\')
    .replace(/"/g, '\\"')
    // eslint-disable-next-line no-control-regex -- 匹配控制字符即为该转义器的目的
    .replace(/[\u0000-\u001f]/g, (c) => {
      switch (c) {
        case '\n': return '\\n'
        case '\r': return '\\r'
        case '\t': return '\\t'
        case '\b': return '\\b'
        case '\f': return '\\f'
        default: return '\\u' + c.charCodeAt(0).toString(16).padStart(4, '0')
      }
    })
}
