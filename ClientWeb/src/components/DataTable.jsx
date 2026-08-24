// 通用数据表格：columns = [{key,title,render?,width?}]
export default function DataTable({ columns, rows, loading, empty = '暂无数据', rowKey }) {
  if (loading) return <div className="table-loading">加载中…</div>
  if (!rows || !rows.length) return <div className="table-empty">{empty}</div>
  const keyOf = (r, i) => (rowKey ? r[rowKey] : i)
  return (
    <div className="table-wrap">
      <table className="data-table">
        <thead>
          <tr>{columns.map((c) => <th key={c.key} style={c.width ? { width: c.width } : undefined}>{c.title}</th>)}</tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={keyOf(r, i)}>
              {columns.map((c) => (
                <td key={c.key}>{c.render ? c.render(r[c.key], r) : (r[c.key] ?? '')}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
