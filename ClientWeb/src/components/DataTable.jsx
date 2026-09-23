// 通用数据表格：columns = [{key,title,render?,width?,sortable?,sortValue?,nowrap?}]
// cardMode（默认 true）：≤600px 时行转卡片，依赖 td 的 data-label 显示列名；
// 传 false 退回横向滚动（docs/Web页面手机端UI设计以及优化方案/00 §3.5）
// sortable: true 时表头可点击排序（升序→降序→取消 三态循环，纯前端实现）；
// sortValue(row) 可选，提供排序取值（默认取 row[key]）；nowrap: true 时该列单元格不换行（cell-nowrap）；
// rowClass(row) 用于状态行高亮。td 默认可换行（长内容多行展示、无横向滚动）。
// collapsible: true 启用折叠行；collapsedIds: Set 折叠的 rowKey 集合；onToggleCollapse(rowKey)：切换回调。
// renderCollapsedRow(row, onToggle)：折叠时自定义跨行摘要（优先级最高）。
// collapsedHiddenColumns: [key1, key2]：折叠时隐藏指定列，其余列正常显示但单行紧凑（与 renderCollapsedRow 二选一）。
// v2.0.7x 阶段CN 新增「展开行」语义（与上面「折叠行」语义并存、互不干扰）：
//   expandedIds: Set 展开的 rowKey 集合；renderExpandedRow(row, onToggle)：展开时在数据行**下方**追加一行渲染的内容。
//   区别 —— 折叠语义：collapsedIds 命中后用摘要行**替换**整行（原行内容消失，AIRouteManage 用法）；
//          展开语义：expandedIds 命中后**原数据行照常保留**，详情追加在下一整行（ChatAnalysis 对话详情用法）。
//   未传 expandedIds 时行渲染与历史行为完全一致（零回归）。
// sortStorageKey: 传入后排序状态持久化到 localStorage（{key, dir}），刷新后恢复；key 失效或列不可排序时自动忽略。
// enableSortPersist（默认 true）：未传 sortStorageKey 时，按路径+角色自动生成 localStorage key，
//   让"刷新后保持用户当前列排序"对所有页面默认生效（提交/刷新后顺序不会跳）。
// onSortChange(sort|null)：排序变化时回调，父组件可用此暴露"恢复默认排序"按钮。
//   v2.0.79 阶段CK：弹出式窗口提交后表格顺序稳定性全面优化新增。
import { Fragment, useEffect, useMemo, useRef, useState } from 'react'
import { useI18n } from '../i18n'

// 根据当前路径+角色生成稳定的 sortStorageKey（避免不同页面相互覆盖）
function defaultSortStorageKey() {
  if (typeof window === 'undefined') return ''
  const role = (typeof __APP_ROLE__ !== 'undefined' && __APP_ROLE__) || 'common'
  const p = window.location && window.location.pathname ? window.location.pathname : 'global'
  return `lsm:datatable:sort:${role}:${p}`
}

// 数值优先数值比较，其余中文 localeCompare；null/undefined 恒排末尾
function compare(a, b) {
  const empty = (v) => v == null || v === ''
  if (empty(a) && empty(b)) return 0
  if (empty(a)) return 1
  if (empty(b)) return -1
  if (typeof a === 'number' && typeof b === 'number') return a - b
  const na = Number(a), nb = Number(b)
  if (!isNaN(na) && !isNaN(nb) && String(a).trim() !== '' && String(b).trim() !== '') return na - nb
  return String(a).localeCompare(String(b), 'zh-CN')
}

export default function DataTable({ columns, rows, loading, empty, rowKey, cardMode = true, rowClass,
  collapsible, collapsedIds, onToggleCollapse, renderCollapsedRow, collapsedHiddenColumns = [], sortStorageKey,
  enableSortPersist = true, onSortChange, expandedIds, renderExpandedRow }) {
  const { t } = useI18n()
  // 排序状态：{key, dir: 1|-1}；
  // 1) sortStorageKey 显式传入时按其持久化；
  // 2) enableSortPersist（默认 true）且未显式传入 sortStorageKey 时，按路径+角色自动生成 localStorage key 持久化。
  const effectiveStorageKey = useMemo(() => {
    if (sortStorageKey) return sortStorageKey
    if (enableSortPersist) return defaultSortStorageKey()
    return ''
  }, [sortStorageKey, enableSortPersist])
  // 排序状态：{key, dir: 1|-1}；effectiveStorageKey 存在时从 localStorage 恢复并持久化
  const [sort, setSort] = useState(() => {
    if (!effectiveStorageKey) return null
    try {
      const raw = window.localStorage.getItem(effectiveStorageKey)
      if (!raw) return null
      const s = JSON.parse(raw)
      // 校验：列仍存在且可排序、方向合法，否则丢弃记忆
      const col = columns.find((c) => c.key === s.key)
      if (!col || !col.sortable || ![1, -1].includes(s.dir)) return null
      return { key: s.key, dir: s.dir }
    } catch { return null }
  })
  useEffect(() => {
    if (!effectiveStorageKey) return
    try {
      if (sort) window.localStorage.setItem(effectiveStorageKey, JSON.stringify(sort))
      else window.localStorage.removeItem(effectiveStorageKey)
    } catch { /* 忽略 */ }
  }, [sort, effectiveStorageKey])
  // 排序变化回调（用于父组件显示"恢复默认排序"按钮等）
  const lastReportedSort = useRef(null)
  useEffect(() => {
    if (typeof onSortChange !== 'function') return
    if (lastReportedSort.current === sort) return
    lastReportedSort.current = sort
    onSortChange(sort)
  }, [sort, onSortChange])

  if (!empty) empty = t('datatable.noData')

  const hiddenSet = useMemo(() => new Set(collapsedHiddenColumns), [collapsedHiddenColumns])

  const sorted = useMemo(() => {
    if (!sort || !rows) return rows
    const col = columns.find((c) => c.key === sort.key)
    if (!col) return rows
    const val = col.sortValue ? (r) => col.sortValue(r) : (r) => r[col.key]
    return [...rows].sort((x, y) => compare(val(x), val(y)) * sort.dir)
  }, [rows, columns, sort])

  const clickSort = (c) => {
    if (!c.sortable) return
    setSort((s) => (s && s.key === c.key ? (s.dir === 1 ? { key: c.key, dir: -1 } : null) : { key: c.key, dir: 1 }))
  }

  if (loading) return <div className="table-loading">{t('common.loading')}</div>
  if (!rows || !rows.length) return <div className="table-empty">{empty}</div>
  const keyOf = (r, i) => (rowKey ? r[rowKey] : i)
  return (
    <div className={'table-wrap' + (cardMode ? ' card-wrap' : '')}>
      <table className={'data-table' + (cardMode ? ' card-mode' : '')}>
        <thead>
          <tr>
            {collapsible ? <th style={{ width: 40 }} title={t('common.expand') + '/' + t('common.collapse')}></th> : null}
            {columns.map((c) => (
              <th key={c.key} style={c.width ? { width: c.width } : undefined}
                className={c.sortable ? 'sortable' : undefined}
                onClick={() => clickSort(c)}
                title={c.sortable ? t('datatable.sortAsc') + ' / ' + t('datatable.sortDesc') : undefined}>
                {c.title}{c.sortable && sort && sort.key === c.key ? (sort.dir === 1 ? ' ▲' : ' ▼') : ''}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {sorted.map((r, i) => {
            const k = keyOf(r, i)
            const toggle = () => onToggleCollapse && onToggleCollapse(k)
            // 展开语义（阶段CN）：传了 expandedIds 即启用，数据行保留 + 下方追加详情整行
            const expandMode = !!expandedIds
            const expanded = collapsible && expandMode && expandedIds.has(k)
            // 折叠语义（历史行为，未启用展开语义时生效）：摘要行替换整行
            const collapsed = collapsible && !expandMode && collapsedIds && collapsedIds.has(k)
            if (collapsed && renderCollapsedRow) {
              // 自定义折叠摘要行（跨所有列）
              return (
                <tr key={k} className={[rowClass ? rowClass(r) : '', 'row-collapsed'].filter(Boolean).join(' ') || undefined}>
                  <td colSpan={columns.length + 1} className="cell-collapsed-row">
                    {renderCollapsedRow(r, toggle)}
                  </td>
                </tr>
              )
            }
            // 折叠且配置了隐藏列：仍逐列渲染但隐藏指定列，内容单行紧凑
            const compactMode = collapsed && collapsedHiddenColumns.length > 0
            const rowCls = [
              rowClass ? rowClass(r) : '',
              collapsed ? 'row-collapsed' : '',
              compactMode ? 'row-collapsed-compact' : '',
              expanded ? 'row-expanded' : '',
            ].filter(Boolean).join(' ') || undefined
            // 行首指示：展开态/完整行 ▼（点击收起），折叠态/未展开 ▶（点击展开）
            const open = expandMode ? expanded : !collapsed
            const toggleTitle = open ? t('common.collapse') : t('common.expand')
            const mainRow = (
              <tr key={k} className={rowCls}>
                {collapsible ? (
                  <td className="cell-collapse-toggle">
                    <button type="button" className="collapse-btn" onClick={toggle}
                      title={toggleTitle} aria-label={toggleTitle}>
                      {open ? '▼' : '▶'}
                    </button>
                  </td>
                ) : null}
                {columns.map((c) => {
                  const hidden = compactMode && hiddenSet.has(c.key)
                  return (
                    <td key={c.key}
                      className={[
                        c.nowrap ? 'cell-nowrap' : undefined,
                        hidden ? 'cell-collapsed-hidden' : undefined,
                      ].filter(Boolean).join(' ') || undefined}
                      data-label={typeof c.title === 'string' ? c.title : undefined}>
                      {hidden ? null : (c.render ? c.render(r[c.key], r) : (r[c.key] ?? ''))}
                    </td>
                  )
                })}
              </tr>
            )
            if (!expanded || !renderExpandedRow) return mainRow
            // 数据行下方追加详情整行（跨所有列），原数据行不消失
            return (
              <Fragment key={k}>
                {mainRow}
                <tr className="row-expanded-detail">
                  <td colSpan={columns.length + 1} className="cell-expanded-row">
                    {renderExpandedRow(r, toggle)}
                  </td>
                </tr>
              </Fragment>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}
