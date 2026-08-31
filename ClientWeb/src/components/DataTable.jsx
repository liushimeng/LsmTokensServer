// 通用数据表格：columns = [{key,title,render?,width?,sortable?,sortValue?,nowrap?}]
// cardMode（默认 true）：≤600px 时行转卡片，依赖 td 的 data-label 显示列名；
// 传 false 退回横向滚动（docs/Web页面手机端UI设计以及优化方案/00 §3.5）
// sortable: true 时表头可点击排序（升序→降序→取消 三态循环，纯前端实现）；
// sortValue(row) 可选，提供排序取值（默认取 row[key]）；nowrap: true 时该列单元格不换行（cell-nowrap）；
// rowClass(row) 用于状态行高亮。td 默认可换行（长内容多行展示、无横向滚动）。
// collapsible: true 启用折叠行；collapsedIds: Set 折叠的 rowKey 集合；onToggleCollapse(rowKey)：切换回调。
// renderCollapsedRow(row, onToggle)：折叠时自定义跨行摘要（优先级最高）。
// collapsedHiddenColumns: [key1, key2]：折叠时隐藏指定列，其余列正常显示但单行紧凑（与 renderCollapsedRow 二选一）。
import { useMemo, useState } from 'react'
import { useI18n } from '../i18n'

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
  collapsible, collapsedIds, onToggleCollapse, renderCollapsedRow, collapsedHiddenColumns = [] }) {
  const { t } = useI18n()
  const [sort, setSort] = useState(null) // {key, dir: 1|-1}

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
            const collapsed = collapsible && collapsedIds && collapsedIds.has(k)
            const cls = [rowClass ? rowClass(r) : '', collapsed ? 'row-collapsed' : ''].filter(Boolean).join(' ') || undefined
            if (collapsed && renderCollapsedRow) {
              // 自定义折叠摘要行（跨所有列）
              return (
                <tr key={k} className={cls}>
                  <td colSpan={columns.length + 1} className="cell-collapsed-row">
                    {renderCollapsedRow(r, () => onToggleCollapse && onToggleCollapse(k))}
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
            ].filter(Boolean).join(' ') || undefined
            return (
              <tr key={k} className={rowCls}>
                {collapsible ? (
                  <td className="cell-collapse-toggle">
                    <button type="button" className="collapse-btn" onClick={() => onToggleCollapse && onToggleCollapse(k)}
                      title={collapsed ? t('common.expand') : t('common.collapse')} aria-label={collapsed ? t('common.expand') : t('common.collapse')}>
                      {collapsed ? '▶' : '▼'}
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
          })}
        </tbody>
      </table>
    </div>
  )
}
