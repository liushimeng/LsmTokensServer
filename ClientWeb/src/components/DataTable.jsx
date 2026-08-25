// 通用数据表格：columns = [{key,title,render?,width?,sortable?,sortValue?}]
// cardMode（默认 true）：≤600px 时行转卡片，依赖 td 的 data-label 显示列名；
// 传 false 退回横向滚动（docs/Web页面手机端UI设计以及美化方案/00 §3.5）
// sortable: true 时表头可点击排序（升序→降序→取消 三态循环，纯前端实现）；
// sortValue(row) 可选，提供排序取值（默认取 row[key]）；rowClass(row) 用于状态行高亮。
import { useMemo, useState } from 'react'

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

export default function DataTable({ columns, rows, loading, empty = '暂无数据', rowKey, cardMode = true, rowClass }) {
  const [sort, setSort] = useState(null) // {key, dir: 1|-1}

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

  if (loading) return <div className="table-loading">加载中…</div>
  if (!rows || !rows.length) return <div className="table-empty">{empty}</div>
  const keyOf = (r, i) => (rowKey ? r[rowKey] : i)
  return (
    <div className={'table-wrap' + (cardMode ? ' card-wrap' : '')}>
      <table className={'data-table' + (cardMode ? ' card-mode' : '')}>
        <thead>
          <tr>{columns.map((c) => (
            <th key={c.key} style={c.width ? { width: c.width } : undefined}
              className={c.sortable ? 'sortable' : undefined}
              onClick={() => clickSort(c)}
              title={c.sortable ? '点击排序' : undefined}>
              {c.title}{c.sortable && sort && sort.key === c.key ? (sort.dir === 1 ? ' ▲' : ' ▼') : ''}
            </th>
          ))}</tr>
        </thead>
        <tbody>
          {sorted.map((r, i) => (
            <tr key={keyOf(r, i)} className={rowClass ? rowClass(r) : undefined}>
              {columns.map((c) => (
                <td key={c.key} data-label={typeof c.title === 'string' ? c.title : undefined}>
                  {c.render ? c.render(r[c.key], r) : (r[c.key] ?? '')}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
