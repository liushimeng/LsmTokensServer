// ClientWeb/src/components/JsonTree.jsx
//
// JSON 美化视图组件 v2（v2.0.74 阶段AS 全面升级）。
//
// 核心策略：惰性渲染 —— 折叠的容器只渲染一行 "{ … } // N 项" 标记，
// 展开时才递归渲染子节点；DOM 数量只与"当前可见节点数"相关，与 JSON
// 总大小解耦，数据层永远持有完整解析结果，零丢失。
//
//   1) 标准 JSON 排版：{ } [ ] 引号/冒号/逗号/缩进对齐全保留，闭合括号独占一行；
//   2) 全量折叠：每个非空对象/数组节点都有独立折叠按钮（button + aria-expanded），
//      不存在"不可展开"的节点类型（废除阶段AR 的 OVERFLOW/truncated 机制）；
//   3) 大容器分页：展开容器默认渲染前 JSON_TREE_PAGE_SIZE 项，「显示更多 / 显示全部」
//      按需加载到末尾，剩余项永不丢失；
//   4) 超长字符串：超过 JSON_STRING_INLINE_LIMIT 默认截断 + 「显示全部」按需展开；
//   5) 渲染预算：单次渲染超过 JSON_RENDER_BUDGET 行即停止并提示，防级联卡死；
//   6) 工具栏：折叠全部 / 展开至 2、3、5 层 / 展开全部（阶段AU 新增）。
//
// 2026-08-28 阶段AU：
//   - 新增可选 query prop：key 名 / 字符串值 / 标量字面量经 SearchText 渲染，
//     在当前树布局内做查找高亮（计数与焦点滚动由 InlineDetailRow DOM 机制统一处理）；
//   - 新增可选 toolbar prop（默认 true）：嵌入 SSE 事件卡片等紧凑场景可隐藏工具栏。
//
// 旧版（阶段AM/AR）问题：配额制 buildJsonTreeNodes 在构建期截断数据，
// 导致"显示不全、只有第一个元素可折叠、Object{3} 标签式排版"。

import { useCallback, useMemo, useState } from 'react'
import { useI18n } from '../i18n'
import SearchText from './SearchText'
import {
  parseJsonSafely,
  escapeJsonString,
  entriesOf,
  childPathOf,
  collectContainerPaths,
  collectDefaultExpandedPaths,
  JSON_TREE_PAGE_SIZE,
  JSON_STRING_INLINE_LIMIT,
  JSON_RENDER_BUDGET,
} from '../shared/json'

/** 每层缩进像素（与 .jt-toggle 宽度 + 右距对齐：子级折叠按钮列对齐父级内容列） */
const JT_INDENT_PX = 20
/** 工具栏"展开至 N 层"档位 */
const JT_EXPAND_LEVELS = [2, 3, 5]

export default function JsonTree({ value, query, toolbar = true }) {
  const { t } = useI18n()
  const parsed = useMemo(() => parseJsonSafely(value), [value])

  if (!parsed.ok && parsed.reason === 'empty') {
    return <div className="muted">({t('chatAnalysis.emptyContent')})</div>
  }
  if (!parsed.ok) {
    // 非 JSON 文本（或解析失败）：原样兜底展示
    return <pre className="log-box">{String(value)}</pre>
  }

  return <JsonTreeInner data={parsed.data} query={query} toolbar={toolbar} />
}

/**
 * 受控惰性树主体（独立组件保证 hooks 稳定：JsonTree 的早退分支不触发 hook 顺序变化）。
 * 状态：
 *   expanded —— 展开的容器路径集合（$ / $["key"] / $[0]）
 *   limits   —— 各容器的分页上限（path → 可见子项数；缺省 JSON_TREE_PAGE_SIZE）
 */
function JsonTreeInner({ data, query, toolbar }) {
  const { t } = useI18n()
  const [expanded, setExpanded] = useState(() => collectDefaultExpandedPaths(data))
  const [limits, setLimits] = useState({})

  const toggle = useCallback((path) => {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(path)) next.delete(path)
      else next.add(path)
      return next
    })
  }, [])

  const showMore = useCallback((path, all) => {
    setLimits((prev) => ({
      ...prev,
      [path]: all ? Infinity : (prev[path] || JSON_TREE_PAGE_SIZE) + JSON_TREE_PAGE_SIZE,
    }))
  }, [])

  const collapseAll = () => setExpanded(new Set(['$']))
  const expandToLevel = (level) => setExpanded(collectContainerPaths(data, level))
  // 展开全部：收集全部容器路径（纯数据遍历，毫秒级）；DOM 防卡顿由渲染预算 +
  // 大数组分页兜底（阶段AU）
  const expandAll = () => setExpanded(collectContainerPaths(data, Number.POSITIVE_INFINITY))

  // 单次渲染通行的预算（每次 render 重建；渲染过程同步扣减）。
  // 扣减封装为 tryTake() 方法：语义即"尝试占用 1 个预算行，耗尽返回 false"。
  const budget = {
    left: JSON_RENDER_BUDGET,
    tryTake() {
      if (this.left <= 0) return false
      this.left--
      return true
    },
  }

  const ctx = {
    t,
    budget,
    query,
    isExpanded: (path) => expanded.has(path),
    toggle,
    getLimit: (path) => limits[path] || JSON_TREE_PAGE_SIZE,
    showMore,
  }

  return (
    <div className="json-tree">
      {toolbar ? (
        <div className="json-tree-toolbar">
          <button type="button" className="jt-tool-btn" onClick={collapseAll}>
            {t('chatAnalysis.jsonCollapseAll')}
          </button>
          {JT_EXPAND_LEVELS.map((level) => (
            <button
              key={level}
              type="button"
              className="jt-tool-btn"
              onClick={() => expandToLevel(level)}
            >
              {t('chatAnalysis.jsonExpandLevel', { n: level })}
            </button>
          ))}
          <button type="button" className="jt-tool-btn" onClick={expandAll}>
            {t('chatAnalysis.jsonExpandAll')}
          </button>
        </div>
      ) : null}
      <div className="json-tree-code">
        <JsonNode value={data} path="$" depth={0} name={undefined} hasKey={false} isLast ctx={ctx} />
      </div>
    </div>
  )
}

/**
 * 递归节点渲染。容器折叠时只输出一行标记；展开时输出 开括号行 / 子节点 / 闭括号行。
 * 每渲染一个节点扣减 1 个预算行，预算耗尽停止渲染后续兄弟节点并输出提示行。
 */
function JsonNode({ value, path, depth, name, hasKey, isLast, ctx }) {
  const isContainer = value !== null && typeof value === 'object'
  const comma = !isLast ? <span className="jt-punct">,</span> : null
  const keyPart = hasKey ? (
    <>
      <span className="jt-key">
        <SearchText query={ctx.query} text={`"${escapeJsonString(name)}"`} />
      </span>
      <span className="jt-punct">: </span>
    </>
  ) : null

  if (!isContainer) {
    if (!ctx.budget.tryTake()) return <BudgetHint depth={depth} ctx={ctx} />
    return (
      <Line depth={depth}>
        {keyPart}
        <PrimitiveValue value={value} query={ctx.query} />
        {comma}
      </Line>
    )
  }

  const isArr = Array.isArray(value)
  const entries = entriesOf(value)
  const openB = isArr ? '[' : '{'
  const closeB = isArr ? ']' : '}'

  // 空容器：单行 "{}" / "[]"，无折叠按钮（占位符保持列对齐）
  if (entries.length === 0) {
    if (!ctx.budget.tryTake()) return <BudgetHint depth={depth} ctx={ctx} />
    return (
      <Line depth={depth}>
        {keyPart}
        <span className="jt-brace">{openB}{closeB}</span>
        {comma}
      </Line>
    )
  }

  const open = ctx.isExpanded(path)
  const total = entries.length

  // 折叠态：一行式 "key": { … } // N 项 （保留原始符号 + 项数提示）
  if (!open) {
    if (!ctx.budget.tryTake()) return <BudgetHint depth={depth} ctx={ctx} />
    return (
      <Line depth={depth} toggle open={false} onToggle={() => ctx.toggle(path)}>
        {keyPart}
        <span className="jt-brace">
          {openB}<span className="jt-ellipsis"> … </span>{closeB}
        </span>
        <span className="jt-count">{ctx.t('chatAnalysis.jsonItemsSuffix', { n: total })}</span>
        {comma}
      </Line>
    )
  }

  // 展开态：开括号行 + 分页子节点 + 闭括号行
  if (!ctx.budget.tryTake()) return <BudgetHint depth={depth} ctx={ctx} />

  const limit = ctx.getLimit(path)
  const visible = entries.slice(0, limit)
  const remain = total - visible.length
  const rendered = []
  let exhausted = false
  for (let i = 0; i < visible.length; i++) {
    if (!ctx.budget.tryTake()) { exhausted = true; break }
    const [k, child] = visible[i]
    rendered.push(
      <JsonNode
        key={childPathOf(path, isArr, k)}
        value={child}
        path={childPathOf(path, isArr, k)}
        depth={depth + 1}
        name={k}
        hasKey={!isArr}
        isLast={i === total - 1}
        ctx={ctx}
      />,
    )
  }

  return (
    <>
      <Line depth={depth} toggle open onToggle={() => ctx.toggle(path)}>
        {keyPart}
        <span className="jt-brace">{openB}</span>
      </Line>
      {rendered}
      {exhausted ? <BudgetHint depth={depth + 1} ctx={ctx} /> : null}
      {remain > 0 ? (
        <div className="jt-line jt-more" style={{ paddingLeft: (depth + 1) * JT_INDENT_PX }}>
          <button
            type="button"
            className="jt-inline-btn"
            onClick={() => ctx.showMore(path, false)}
          >
            {ctx.t('chatAnalysis.jsonShowMore', { n: Math.min(JSON_TREE_PAGE_SIZE, remain) })}
          </button>
          <button
            type="button"
            className="jt-inline-btn"
            onClick={() => ctx.showMore(path, true)}
          >
            {ctx.t('chatAnalysis.jsonShowAll', { n: total })}
          </button>
        </div>
      ) : null}
      <Line depth={depth}>
        <span className="jt-brace">{closeB}</span>
        {comma}
      </Line>
    </>
  )
}

/** 单行容器：折叠按钮（或隐形占位符保持对齐）+ 内容区（可换行） */
function Line({ depth, toggle, open, onToggle, children }) {
  return (
    <div className="jt-line" style={{ paddingLeft: depth * JT_INDENT_PX }}>
      {toggle ? (
        <button
          type="button"
          className="jt-toggle"
          aria-expanded={open}
          onClick={onToggle}
        >
          {open ? '▾' : '▸'}
        </button>
      ) : (
        <span className="jt-toggle jt-toggle-none" aria-hidden="true">▸</span>
      )}
      <div className="jt-content">{children}</div>
    </div>
  )
}

/** 标量值渲染（字符串/数字/布尔/null）— 查找词非空时经 SearchText 高亮 */
function PrimitiveValue({ value, query }) {
  if (value === null) return <span className="jt-null"><SearchText query={query} text="null" /></span>
  if (typeof value === 'number') return <span className="jt-number"><SearchText query={query} text={String(value)} /></span>
  if (typeof value === 'boolean') return <span className="jt-boolean"><SearchText query={query} text={String(value)} /></span>
  if (typeof value === 'string') return <StringValue text={value} query={query} />
  return <span className="jt-string"><SearchText query={query} text={escapeJsonString(String(value))} /></span>
}

/** 字符串值：超长默认截断，「显示全部 / 收起」按需切换；截断态仅高亮可见部分 */
function StringValue({ text, query }) {
  const { t } = useI18n()
  const [showAll, setShowAll] = useState(false)

  if (text.length <= JSON_STRING_INLINE_LIMIT) {
    return <span className="jt-string"><SearchText query={query} text={`"${escapeJsonString(text)}"`} /></span>
  }
  const shown = showAll ? text : text.slice(0, JSON_STRING_INLINE_LIMIT)
  return (
    <>
      <span className="jt-string"><SearchText query={query} text={`"${escapeJsonString(shown)}${showAll ? '' : '…'}"`} /></span>
      <button type="button" className="jt-inline-btn" onClick={() => setShowAll((s) => !s)}>
        {showAll
          ? t('chatAnalysis.jsonStringCollapse')
          : t('chatAnalysis.jsonStringExpand', { n: text.length })}
      </button>
    </>
  )
}

/** 渲染预算耗尽提示行 */
function BudgetHint({ depth, ctx }) {
  return (
    <div className="jt-line jt-budget-hint" style={{ paddingLeft: (depth + 1) * JT_INDENT_PX }}>
      {ctx.t('chatAnalysis.jsonBudgetHint', { n: JSON_RENDER_BUDGET })}
    </div>
  )
}
