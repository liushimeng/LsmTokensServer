// src/shared/listOrder.js
//
// 列表「顺序调整」纯函数集。弹窗内有序列表（当前唯一使用者：智能路由管理
// 「目标源站列表」，管理端 9101 与用户端 29001 共享同一组件）共用这一套语义。
//
// 业务约定：列表下标即优先级，下标 0 = 首位 = 最高优先级（保存时同时作为主源站
// dst_endpoint_id；稳定型/经济型算法失败切换时把第 0 个滚动到末尾）。
//
// 设计约束：
//   1. 全部返回新数组，绝不修改入参（React state 安全）；
//   2. `from` 越界（陈旧下标、异步列表已变短）时不做任何搬移，直接返回副本，
//      避免「点了旧按钮却把别的记录搬走」；`to` 越界则钳制到端点（置顶/置底天然幂等）；
//   3. 纯函数、无 React/DOM 依赖，可被 `node` 直接跑自检（见 listOrder.test.js）。

const clamp = (v, lo, hi) => Math.min(Math.max(v, lo), hi)

// 下标是否可搬移：整数、在 [0, len) 内、且列表至少 2 个元素
const movableIndex = (list, index) => (
  Array.isArray(list)
  && list.length > 1
  && Number.isInteger(index)
  && index >= 0
  && index < list.length
)

/**
 * 唯一搬移原语：把 index 为 from 的元素移动到 index 为 to 的位置（先摘出再插入，
 * 其余元素相对顺序保持不变）。
 * @param {Array} list 源列表（不被修改）
 * @param {number} from 当前下标
 * @param {number} to 目标下标（越界钳制）
 * @returns {Array} 新列表
 */
export function moveItem(list, from, to) {
  const copy = Array.isArray(list) ? list.slice() : []
  if (!movableIndex(copy, from)) return copy
  const dst = clamp(Number.isFinite(to) ? Math.trunc(to) : 0, 0, copy.length - 1)
  if (dst === from) return copy
  const [item] = copy.splice(from, 1)
  copy.splice(dst, 0, item)
  return copy
}

/** 置顶：移到列表首位（优先级最高 / 成为主源站）。idx===0 时幂等。 */
export const pinToTop = (list, from) => moveItem(list, from, 0)

/** 置底：移到列表末位（优先级最低）。idx===末位时幂等。 */
export const pinToBottom = (list, from) => moveItem(list, from, Array.isArray(list) ? list.length - 1 : 0)

/** 上移一位。已在首位时幂等。 */
export const moveUp = (list, from) => moveItem(list, from, from - 1)

/** 下移一位。已在末位时幂等。 */
export const moveDown = (list, from) => moveItem(list, from, from + 1)

/** 能否「上移 / 置顶」：非首位才可 */
export const canMoveUp = (list, index) => movableIndex(list, index) && index > 0

/** 能否「下移 / 置底」：非末位才可 */
export const canMoveDown = (list, index) => movableIndex(list, index) && index < (Array.isArray(list) ? list.length : 0) - 1

/** 是否首位（主源站标记用） */
export const isFirst = (list, index) => Array.isArray(list) && list.length > 0 && index === 0

/** 整个顺序操作区是否可用（少于 2 条无顺序可调） */
export const canReorder = (list) => Array.isArray(list) && list.length > 1
