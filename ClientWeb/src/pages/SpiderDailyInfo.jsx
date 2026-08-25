import { useCallback, useEffect, useState } from 'react'
import { get, post } from '../shared/api'
import DataTable from '../components/DataTable'

// 每日 MCP 信息：SpiderDailyInfoInterface
// 列表：GET ?page&page_size&platform&start_date&end_date（返回 {data:{infos,platforms}, total}）
// 动作：POST JSON {action:'get_content'|'delete'|'batch_delete'}

const PAGE_SIZES = [1, 3, 5, 10, 15, 20, 50, 100]
const URL_REGEX = /https?:\/\/[^\s<>"']+[^\s<>"'.,;:!?)\]]/gi

function fmtTime(v) {
  if (!v) return ''
  let d = new Date(String(v).replace(' ', 'T'))
  if (isNaN(d.getTime())) return v
  const p = (x) => String(x).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// 内容中的 URL 渲染为可点击链接（noopener 防反向操控）
function renderContent(text) {
  if (!text) return null
  const parts = String(text).split(URL_REGEX)
  const urls = String(text).match(URL_REGEX) || []
  return (
    <span style={{ whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
      {parts.map((p, i) => (
        <span key={i}>
          {p}
          {urls[i] ? <a href={urls[i]} target="_blank" rel="noopener noreferrer" style={{ color: 'var(--primary)', wordBreak: 'break-all' }}>{urls[i]}</a> : null}
        </span>
      ))}
    </span>
  )
}

export default function SpiderDailyInfo(props) {
  const q = props?.route?.query
  const [page, setPage] = useState(() => Math.max(1, parseInt(q?.get('page') || '1', 10) || 1))
  const [pageSize, setPageSize] = useState(() => {
    const n = parseInt(q?.get('page_size') || '20', 10)
    return PAGE_SIZES.includes(n) ? n : 20
  })
  const [platform, setPlatform] = useState(q?.get('platform') || '')
  const [startDate, setStartDate] = useState(q?.get('start_date') || '')
  const [endDate, setEndDate] = useState(q?.get('end_date') || '')
  const [infos, setInfos] = useState([])
  const [platforms, setPlatforms] = useState([])
  const [total, setTotal] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(new Set())
  const [expanded, setExpanded] = useState(new Set()) // 已展开的 id
  const [contentCache, setContentCache] = useState({}) // id -> {state:'loading'|'loaded'|'error', content, message}

  const load = useCallback((p, ps, pf, sd, ed) => {
    setLoading(true)
    setError('')
    const params = new URLSearchParams({ page: p, page_size: ps })
    if (pf) params.set('platform', pf)
    if (sd) params.set('start_date', sd)
    if (ed) params.set('end_date', ed)
    get('SpiderDailyInfoInterface?' + params.toString())
      .then((res) => {
        setInfos((res?.data?.infos) || [])
        setPlatforms((res?.data?.platforms) || [])
        setTotal(res?.total || 0)
      })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { load(page, pageSize, platform, startDate, endDate) }, [load, page, pageSize, platform, startDate, endDate])

  // 按需加载内容（get_content，缓存到内存）
  const loadContent = async (id) => {
    setContentCache((c) => ({ ...c, [id]: { state: 'loading' } }))
    try {
      const res = await post('SpiderDailyInfoInterface', { action: 'get_content', id })
      setContentCache((c) => ({ ...c, [id]: { state: 'loaded', content: (res?.data?.content) || '' } }))
      setExpanded((s) => new Set(s).add(id))
    } catch (e) {
      setContentCache((c) => ({ ...c, [id]: { state: 'error', message: e.message } }))
    }
  }

  const toggleContent = async (id) => {
    const c = contentCache[id]
    if (!c || c.state === 'error') { loadContent(id); return }
    if (c.state === 'loading') return
    setExpanded((s) => {
      const next = new Set(s)
      if (next.has(id)) next.delete(id); else next.add(id)
      return next
    })
  }

  const deleteInfo = async (id) => {
    if (!confirm('确认删除这条记录？此操作不可恢复！')) return
    try {
      await post('SpiderDailyInfoInterface', { action: 'delete', id })
      setSelected((s) => { const n = new Set(s); n.delete(id); return n })
      load(page, pageSize, platform, startDate, endDate)
    } catch (e) { alert(e.message) }
  }

  const batchDelete = async () => {
    if (!selected.size) { alert('请先选择要删除的记录'); return }
    if (!confirm('确认删除选中的 ' + selected.size + ' 条记录？此操作不可恢复！')) return
    try {
      await post('SpiderDailyInfoInterface', { action: 'batch_delete', items: [...selected].map((id) => ({ id })) })
      setSelected(new Set())
      setContentCache({})
      setExpanded(new Set())
      load(page, pageSize, platform, startDate, endDate)
    } catch (e) { alert(e.message) }
  }

  const totalPages = Math.ceil(total / pageSize) || 1
  const allSelected = infos.length > 0 && infos.every((i) => selected.has(i.id))
  const toggleSelect = (id) => {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id); else next.add(id)
    setSelected(next)
  }

  const columns = [
    {
      key: 'checkbox', title: (
        <input type="checkbox" checked={allSelected}
          onChange={(e) => setSelected(e.target.checked ? new Set(infos.map((i) => i.id)) : new Set())} />
      ), width: 36,
      render: (_, info) => <input type="checkbox" checked={selected.has(info.id)} onChange={() => toggleSelect(info.id)} />,
    },
    { key: 'id', title: 'ID', width: 70 },
    { key: 'platform_name', title: '平台' },
    { key: 'title', title: '标题', render: (v, info) => info.url ? <a href={info.url} target="_blank" rel="noopener noreferrer" style={{ color: 'inherit' }}>{v}</a> : v },
    { key: 'crawl_time', title: '爬取时间', render: (v) => fmtTime(v) || '-' },
    {
      key: 'content', title: '内容', render: (_, info) => {
        const c = contentCache[info.id]
        const isOpen = expanded.has(info.id)
        if (!c) return <span style={{ color: '#999' }}>（未加载）</span>
        if (c.state === 'loading') return <span>加载中…</span>
        if (c.state === 'error') return (
          <span style={{ color: '#dc3545' }}>
            加载失败：{c.message}
            <button className="btn btn-link" onClick={() => loadContent(info.id)}>点击重试</button>
          </span>
        )
        return isOpen ? <span className="wrap">{renderContent(c.content)}</span> : <span style={{ color: '#999' }}>（已缓存，点击展开）</span>
      },
    },
    {
      key: 'actions', title: '操作',
      render: (_, info) => {
        const c = contentCache[info.id]
        const label = !c || c.state === 'error' ? (c?.state === 'error' ? '重试' : '展开内容') : (expanded.has(info.id) ? '收起' : '展开')
        return (
          <span>
            <button className="btn btn-sm" disabled={c?.state === 'loading'} onClick={() => toggleContent(info.id)}>{label}</button>{' '}
            <button className="btn btn-sm btn-danger" onClick={() => deleteInfo(info.id)}>删除</button>
          </span>
        )
      },
    },
  ]

  return (
    <div className="page">
      <h2 className="page-title">每日 MCP 信息</h2>
      <div className="toolbar">
        <span>平台</span>
        <select value={platform} onChange={(e) => { setPlatform(e.target.value); setPage(1) }}>
          <option value="">全部</option>
          {platforms.map((p) => <option key={p} value={p}>{p}</option>)}
        </select>
        <span>开始时间</span>
        <input type="datetime-local" step={1} value={startDate} onChange={(e) => { setStartDate(e.target.value); setPage(1) }} />
        <span>结束时间</span>
        <input type="datetime-local" step={1} value={endDate} onChange={(e) => { setEndDate(e.target.value); setPage(1) }} />
        <button className="btn" onClick={() => { setPage(1); setPlatform(''); setStartDate(''); setEndDate('') }}>重置</button>
        <button className="btn btn-primary" onClick={() => load(page, pageSize, platform, startDate, endDate)}>刷新</button>
        {infos.length > 0 ? (
          <>
            <span>已选择 {selected.size} 条</span>
            <button className="btn btn-sm btn-danger" disabled={!selected.size} onClick={batchDelete}>批量删除</button>
          </>
        ) : null}
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={infos} loading={loading} empty="暂无数据" rowKey="id" />
        <div className="pager">
          <span>总计 {total} 条 · 第 {page} / {totalPages} 页 · 每页</span>
          <select value={pageSize} onChange={(e) => { setPageSize(parseInt(e.target.value, 10)); setPage(1) }}>
            {PAGE_SIZES.map((s) => <option key={s} value={s}>{s}</option>)}
          </select>
          <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
          <span>{page}</span>
          <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>下一页</button>
        </div>
      </div>
    </div>
  )
}
