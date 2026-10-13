// 数据表格列宽拖拽调整——通用工具模块（阶段CU：方案见 docs/项目迁移解决方案/数据表格列宽拖拽调整通用控件方案_20261013_164137.md）
//
// 设计要点：
//   1. 列宽以「占数据表可用宽度的百分比」存储（需求：整表等比例缩放）；
//   2. localStorage key = lsm:datatable:colwidth:{role}:{path}:{colsHash}，
//      colsHash 由列 key 序列哈希得出 —— 同页多表（ChatAnalysisTotal/Task 等）天然区分；
//   3. 折叠开关列（collapsible，40px）不占百分比，从可用宽度中扣除后数据列等比归一；
//   4. 纯函数部分（hash/归一/钳制/清洗）不依赖 DOM，node 直测（columnResize.test.js）。
//
// 运行测试：
//   cd ClientWeb && node src/shared/columnResize.test.js

import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

export const COLWIDTH_KEY_PREFIX = 'lsm:datatable:colwidth:'
export const COLWIDTH_MIN_PX = 48 // 单列最小像素宽（钳制下限时配对列吸收溢出量，总宽恒定）

// djb2 哈希 → base36：列 key 序列 → 稳定短哈希（同页多表区分用）
export function hashColumnKeys(keys) {
  let h = 5381
  const s = keys.join('|')
  for (let i = 0; i < s.length; i++) h = ((h << 5) + h + s.charCodeAt(i)) >>> 0
  return h.toString(36)
}

// 与 DataTable 排序持久化同一套 key 哲学：role + path + 列哈希
export function defaultColWidthStorageKey(role, path, keys) {
  const r = role || 'common'
  const p = path || 'global'
  return COLWIDTH_KEY_PREFIX + r + ':' + p + ':' + hashColumnKeys(keys)
}

// 像素宽数组 → 百分比数组（Σ=100，保留 2 位小数，尾差并入最后一列保证严格 100）
export function pxToPercents(pxWidths) {
  const sum = pxWidths.reduce((a, b) => a + b, 0)
  if (!sum || sum <= 0) {
    // 全零输入降级均分（尾差并入最后一列，Σ 严格 100）
    const per = Math.round((100 / pxWidths.length) * 100) / 100
    return pxWidths.map((_, i) => (i === pxWidths.length - 1 ? Math.round((100 - per * (pxWidths.length - 1)) * 100) / 100 : per))
  }
  const pcts = pxWidths.map((w) => Math.round((w / sum) * 10000) / 100)
  const rounded = pcts.reduce((a, b) => a + b, 0)
  pcts[pcts.length - 1] = Math.round((pcts[pcts.length - 1] + (100 - rounded)) * 100) / 100
  return pcts
}

// 拖拽增量：被拖列与其余列此消彼长，总量恒守恒（最小宽 minPx 钳制）。
// 让出顺序：右邻列优先（直觉上"向右拖挤压右边"），不足则按序溢出到更右侧列，最后才动用左侧列；
// 被拖列收缩时增量全部给第一优先列（保持两列联动的直观手感）。
// 溢出分摊的意义：14 列宽表里若紧邻列已接近最小宽，仅靠单列让位会让拖拽几乎无效。
export function applyDelta(pxWidths, index, delta, minPx = COLWIDTH_MIN_PX) {
  const next = pxWidths.slice()
  const n = next.length
  if (index < 0 || index >= n || n < 2) return next
  const order = []
  for (let i = index + 1; i < n; i++) order.push(i)
  for (let i = index - 1; i >= 0; i--) order.push(i)
  let d = Math.max(minPx - next[index], delta) // 被拖列自身不低于最小宽
  if (d > 0) {
    // 增长：需其他列让出 d 像素；让不满则缩减实际增量（总宽不变）
    let avail = 0
    for (const i of order) avail += Math.max(0, next[i] - minPx)
    d = Math.min(d, avail)
    let rest = d
    for (const i of order) {
      if (rest <= 0) break
      const take = Math.min(Math.max(0, next[i] - minPx), rest)
      next[i] -= take
      rest -= take
    }
  } else if (d < 0) {
    next[order[0]] -= d // 收缩：全部增量给第一优先列
  }
  next[index] += d
  return next
}

// 存储 value 清洗：版本、对象、key 集合与当前列完全一致，否则返回 null（丢弃旧结构数据）
export function sanitizeStored(raw, validKeys) {
  if (!raw) return null
  try {
    const obj = JSON.parse(raw)
    if (!obj || obj.v !== 1 || !obj.widths || typeof obj.widths !== 'object') return null
    const keys = Object.keys(obj.widths)
    if (keys.length !== validKeys.length) return null
    for (const k of validKeys) {
      const v = obj.widths[k]
      if (typeof v !== 'number' || !isFinite(v) || v <= 0 || v > 100) return null
    }
    return obj
  } catch { return null }
}

// 通用列宽拖拽 hook（DataTable 基类控件使用）。
// 参数：
//   columns     列定义数组（数据列，不含折叠开关列）
//   collapsible 是否有 40px 行首开关列
//   storageKey  localStorage key（传 null/'' 表示不持久化）
//   tableRef    <table> 元素 ref（用于实测像素宽）
// 返回：
//   enabled       是否启用（storageKey 为空时 false，调用方据此隐藏把手）
//   colPercents   百分比数组（与 columns 对齐；null = 未启用/无存储，走自然布局）
//   livePxWidths  拖拽中的像素宽数组（优先于 colPercents 渲染）
//   resizingIndex 正在拖拽的列下标（-1 无）
//   onHandleDown  把手 pointerdown 回调（列下标）
//   onHandleReset 把手双击回调（清除存储恢复自然布局）
export function useColumnResize({ columns, collapsible, storageKey, tableRef }) {
  const enabled = !!storageKey
  const keys = useMemo(() => columns.map((c) => c.key), [columns])
  const keysHash = useMemo(() => hashColumnKeys((collapsible ? ['#toggle'] : []).concat(keys)), [keys, collapsible])

  // 持久化百分比（{colKey: pct}），载入时清洗
  const [stored, setStored] = useState(() => {
    if (!enabled) return null
    try {
      const obj = sanitizeStored(window.localStorage.getItem(storageKey), keys)
      return obj ? obj.widths : null
    } catch { return null }
  })
  // 拖拽中实时像素宽
  const [livePxWidths, setLivePxWidths] = useState(null)
  const [resizingIndex, setResizingIndex] = useState(-1)
  // 拖拽过程同步记录（不依赖 React 提交时机，保证快速拖拽 move+up 同帧时也能正确收尾）
  const dragRef = useRef(null)

  // 列结构变化（keysHash 变化）时重载存储，避免旧数据错位
  useEffect(() => {
    if (!enabled) { setStored(null); return }
    try {
      const obj = sanitizeStored(window.localStorage.getItem(storageKey), keys)
      setStored(obj ? obj.widths : null)
    } catch { setStored(null) }
    setLivePxWidths(null)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [storageKey, keysHash])

  // 松手收尾：像素宽 → 百分比归一 → 持久化
  const finishDrag = useCallback((px) => {
    dragRef.current = null
    setResizingIndex(-1)
    setLivePxWidths(null)
    if (!px) return
    const pcts = pxToPercents(px)
    const widths = {}
    keys.forEach((k, i) => { widths[k] = pcts[i] })
    setStored(widths)
    if (enabled) {
      try { window.localStorage.setItem(storageKey, JSON.stringify({ v: 1, widths, ts: Date.now() })) } catch { /* 忽略 */ }
    }
  }, [enabled, storageKey, keys])

  const onHandleDown = useCallback((index) => (e) => {
    if (!enabled) return
    e.preventDefault()
    e.stopPropagation()
    const table = tableRef.current
    if (!table) return
    const tableW = table.getBoundingClientRect().width
    const toggleW = collapsible ? 40 : 0
    const availW = Math.max(tableW - toggleW, COLWIDTH_MIN_PX * keys.length)
    // 基准像素宽：已有存储 → 百分比换算；无存储 → 实测 th 宽（自然布局，视觉零跳变）
    let base
    if (stored) {
      base = keys.map((k) => (stored[k] / 100) * availW)
    } else {
      const ths = table.querySelectorAll('thead th')
      const offset = collapsible ? 1 : 0
      base = keys.map((_, i) => {
        const th = ths[i + offset]
        return th ? th.getBoundingClientRect().width : availW / keys.length
      })
    }
    const drag = { startX: e.clientX, index, baseWidths: base, latest: base, minPx: COLWIDTH_MIN_PX }
    dragRef.current = drag
    setResizingIndex(index)
    setLivePxWidths(base)
    // 命令式绑定 window 监听（组件卸载/新拖拽自动清理，避免 effect 依赖 dragRef 不触发的问题）
    const onMove = (ev) => {
      const d = ev.clientX - drag.startX
      const px = applyDelta(drag.baseWidths, drag.index, d, drag.minPx)
      drag.latest = px // 同步记录：同帧 move+up 也能拿到最新宽度
      setLivePxWidths(px)
    }
    const onUp = () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('pointercancel', onUp)
      // 优先用同步记录的拖拽结果；回退为基准宽（保证任何情况下都能正确收尾）
      finishDrag(dragRef.current ? dragRef.current.latest : drag.latest)
    }
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onUp)
  }, [enabled, stored, collapsible, keys, tableRef, finishDrag])

  const onHandleReset = useCallback((e) => {
    if (!enabled) return
    e.preventDefault()
    e.stopPropagation()
    setStored(null)
    setLivePxWidths(null)
    try { window.localStorage.removeItem(storageKey) } catch { /* 忽略 */ }
  }, [enabled, storageKey])

  // 渲染用百分比：拖拽中 livePxWidths 优先；否则用存储百分比
  const colPercents = useMemo(() => {
    if (livePxWidths) return null
    if (!stored) return null
    const pcts = keys.map((k) => stored[k])
    return pcts.every((v) => typeof v === 'number') ? pcts : null
  }, [stored, livePxWidths, keys])

  return { enabled, colPercents, livePxWidths, resizingIndex, onHandleDown, onHandleReset }
}
