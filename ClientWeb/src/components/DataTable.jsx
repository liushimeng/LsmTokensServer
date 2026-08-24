// 通用数据表格：columns = [{key,title,render?,width?}]
// cardMode（默认 true）：≤600px 时行转卡片，依赖 td 的 data-label 显示列名；
// 传 false 退回横向滚动（docs/Web页面手机端UI设计以及美化方案/00 §3.5）
export default function DataTable({ columns, rows, loading, empty = '暂无数据', rowKey, cardMode = true }) {
  if (loading) return <div className="table-loading">加载中…</div>
  if (!rows || !rows.length) return <div className="table-empty">{empty}</div>
  const keyOf = (r, i) => (rowKey ? r[rowKey] : i)
  return (
    <div className={'table-wrap' + (cardMode ? ' card-wrap' : '')}>
      <table className={'data-table' + (cardMode ? ' card-mode' : '')}>
        <thead>
          <tr>{columns.map((c) => <th key={c.key} style={c.width ? { width: c.width } : undefined}>{c.title}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={keyOf(r, i)}>
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
