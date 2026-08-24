import { useEffect, useRef, useState } from 'react'
import { post, openSse } from '../shared/api'
import { fmtTime } from '../shared/format'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'

// 爬虫数据源管理（迁移自旧 server_web_spider_data_source.go / server_web_spider_crawl.go）
// 数据源 CRUD（/SpiderDataSourceInterface，action=list/add/update/delete/toggle_status）
// + 每行「爬取」按钮：Modal 输入提示词 → EventSource 连 /SpiderDataSourceCrawl 流式输出

// 默认提示词模板（{{.DataSourceID}} 占位符由前端替换展示默认值，实际替换在后端）
const defaultPromptTemplate = (id) =>
  '【强制语言规范】你的所有输出（包括分析、推理、日志、总结）必须全部使用中文，严禁输出英文正文。代码、API 路径、技术术语可保留英文，但所有描述和解释必须为中文。\n\n' +
  `## OpenClaw 自动化爬虫任务执行规范 - 单个数据源处理\n\n### 目标数据源\n- **数据源 ID**：${id}\n` +
  '- 本次任务仅处理此 ID 对应的单条数据源记录，不得操作其他数据源。\n\n' +
  '（完整规范见后端 defaultSpiderCrawlUserPromptTemplate；此处为可编辑提示词，' +
  '使用 {{.DataSourceID}} 占位符会被替换为数据源 ID。）'

const EMPTY_FORM = { id: 0, platform_name: '', url_address: '', description: '', remark: '' }

export default function SpiderDataSource() {
  const [rows, setRows] = useState(null) // null = 加载中
  const [err, setErr] = useState('')
  const [msg, setMsg] = useState('')
  const [isAdmin, setIsAdmin] = useState(false)

  // 编辑弹窗（新增/修改共用）
  const [editing, setEditing] = useState(null) // null=关闭，表单对象
  const [saving, setSaving] = useState(false)

  // 爬取弹窗
  const [crawling, setCrawling] = useState(null) // 目标数据源对象
  const [prompt, setPrompt] = useState('')
  const [logText, setLogText] = useState('')
  const [crawlBusy, setCrawlBusy] = useState(false)
  const esRef = useRef(null)

  const load = async () => {
    setRows(null)
    try {
      const d = await post('SpiderDataSourceInterface', { action: 'list' })
      setRows(d.data || [])
    } catch (e) {
      setRows([])
      setErr('加载失败: ' + e.message)
    }
  }

  useEffect(() => {
    // 管理端 mux 独有 delete 权限，探测一次（用户端接口同样存在，此处用 UserManageInterface 探测）
    fetch('UserManageInterface', { credentials: 'include' })
      .then((r) => setIsAdmin(r.status !== 404))
      .catch(() => setIsAdmin(false))
    load()
    return () => { if (esRef.current) esRef.current.close() }
  }, [])

  // 保存（新增 / 修改）
  const save = async () => {
    const f = editing
    if (!f.platform_name || !f.url_address) { setErr('平台名称和 URL 为必填项'); return }
    setSaving(true); setErr(''); setMsg('')
    try {
      const action = f.id ? 'update' : 'add'
      const d = await post('SpiderDataSourceInterface', { action, ...f })
      setMsg(d.message || '保存成功')
      setEditing(null)
      load()
    } catch (e) { setErr(e.message || '保存失败') } finally { setSaving(false) }
  }

  // 删除（仅管理端）
  const remove = async (rec) => {
    if (!window.confirm(`确认删除数据源「${rec.platform_name}」(#${rec.id})？`)) return
    setErr(''); setMsg('')
    try {
      const d = await post('SpiderDataSourceInterface', { action: 'delete', id: rec.id })
      setMsg(d.message || '删除成功')
      load()
    } catch (e) { setErr(e.message || '删除失败') }
  }

  // 启用/禁用切换
  const toggleStatus = async (rec) => {
    setErr(''); setMsg('')
    try {
      const d = await post('SpiderDataSourceInterface', {
        action: 'toggle_status', id: rec.id, status: rec.status === 1 ? 0 : 1,
      })
      setMsg(d.message || '状态已更新')
      load()
    } catch (e) { setErr(e.message || '操作失败') }
  }

  // 打开爬取弹窗：默认提示词带 {{.DataSourceID}} 模板说明
  const openCrawl = (rec) => {
    setCrawling(rec)
    setPrompt(defaultPromptTemplate(rec.id).replace(String(rec.id), '{{.DataSourceID}}')
      + `\n- 当前数据源 ID：${rec.id}（提示词会被原样发送，后端将替换 {{.DataSourceID}}）`)
    setLogText('')
    setCrawlBusy(false)
  }

  // 关闭爬取弹窗：断开 SSE
  const closeCrawl = () => {
    if (esRef.current) { esRef.current.close(); esRef.current = null }
    setCrawling(null)
  }

  // 开始爬取：EventSource 流式输出（复用 shared/api.js 的 openSse）
  const startCrawl = () => {
    if (!crawling) return
    if (esRef.current) esRef.current.close()
    setLogText('')
    setCrawlBusy(true)
    esRef.current = openSse('SpiderDataSourceCrawl',
      { data_source_id: crawling.id, prompt },
      {
        onEvent: (obj) => {
          // 事件结构 {type, content}：content=正文增量，reasoning=推理增量
          const text = obj && (obj.content || obj.raw || '')
          if (text) setLogText((s) => s + text)
        },
        onError: (e) => {
          setLogText((s) => s + '\n[错误] ' + (e && e.message ? e.message : '连接中断'))
        },
        onDone: () => {
          setLogText((s) => s + '\n[完成] 爬取任务已结束')
          setCrawlBusy(false)
          esRef.current = null
        },
      })
  }

  const columns = [
    { key: 'id', title: 'ID', width: 60 },
    { key: 'platform_name', title: '平台名称' },
    { key: 'url_address', title: 'URL', render: (v) => (
      <a href={v} target="_blank" rel="noreferrer" className="wrap">{v}</a>
    ) },
    { key: 'description', title: '源信息描述', render: (v) => <span className="wrap">{v || '-'}</span> },
    { key: 'remark', title: '备注' },
    { key: 'status', title: '状态', render: (v) => (
      <span><span className={'status-dot ' + (v === 1 ? 'status-on' : 'status-off')} />{v === 1 ? '启用' : '禁用'}</span>
    ) },
    { key: 'updated_at', title: '更新时间', render: (v) => fmtTime(v) },
    { key: 'op', title: '操作', render: (_, rec) => (
      <span style={{ display: 'flex', gap: 4 }}>
        <button className="btn btn-sm btn-primary" onClick={() => openCrawl(rec)}
                disabled={rec.status !== 1} title={rec.status !== 1 ? '数据源已禁用' : ''}>爬取</button>
        <button className="btn btn-sm" onClick={() => setEditing({
          id: rec.id, platform_name: rec.platform_name, url_address: rec.url_address,
          description: rec.description || '', remark: rec.remark || '',
        })}>编辑</button>
        <button className="btn btn-sm" onClick={() => toggleStatus(rec)}>{rec.status === 1 ? '禁用' : '启用'}</button>
        {isAdmin && <button className="btn btn-sm btn-danger" onClick={() => remove(rec)}>删除</button>}
      </span>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">爬虫数据源管理</h2>
      <div className="toolbar">
        <button className="btn btn-primary" onClick={() => setEditing({ ...EMPTY_FORM })}>新增数据源</button>
        <button className="btn" onClick={load}>刷新</button>
        <span style={{ color: 'var(--muted)', fontSize: 12 }}>
          爬取通过 OpenClaw AI 多轮交互完成，最长 15 分钟；同一用户同时仅允许一个任务。
        </span>
      </div>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {msg ? <div className="alert alert-ok">{msg}</div> : null}
      <DataTable columns={columns} rows={rows || []} loading={!rows} empty="暂无数据源" rowKey="id" />

      {/* 新增/编辑弹窗 */}
      {editing && (
        <Modal title={editing.id ? `编辑数据源 #${editing.id}` : '新增数据源'}
               onClose={() => setEditing(null)}
               footer={<>
                 <button className="btn" onClick={() => setEditing(null)}>取消</button>
                 <button className="btn btn-primary" onClick={save} disabled={saving}>
                   {saving ? '保存中…' : '保存'}
                 </button>
               </>}>
          <label className="field"><span>平台名称 *</span>
            <input value={editing.platform_name}
                   onChange={(e) => setEditing({ ...editing, platform_name: e.target.value })} />
          </label>
          <label className="field"><span>URL 地址 *</span>
            <input value={editing.url_address} placeholder="https://…"
                   onChange={(e) => setEditing({ ...editing, url_address: e.target.value })} />
          </label>
          <label className="field"><span>源信息描述（爬虫任务指令）</span>
            <textarea rows={4} value={editing.description}
                      onChange={(e) => setEditing({ ...editing, description: e.target.value })} />
          </label>
          <label className="field"><span>备注</span>
            <input value={editing.remark}
                   onChange={(e) => setEditing({ ...editing, remark: e.target.value })} />
          </label>
        </Modal>
      )}

      {/* 爬取弹窗：SSE 流式输出到 .log-box */}
      {crawling && (
        <Modal title={`AI 爬取 — ${crawling.platform_name} (#${crawling.id})`} onClose={closeCrawl} width={860}
               footer={<>
                 <button className="btn" onClick={closeCrawl} disabled={crawlBusy}>关闭</button>
                 <button className="btn btn-primary" onClick={startCrawl} disabled={crawlBusy}>
                   {crawlBusy ? '爬取中…' : '开始爬取'}
                 </button>
               </>}>
          <label className="field"><span>提示词（支持 {'{{.DataSourceID}}'} 占位符）</span>
            <textarea rows={8} value={prompt} onChange={(e) => setPrompt(e.target.value)} />
          </label>
          <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 6 }}>爬取输出：</div>
          <div className="log-box">{logText || '（尚未开始）'}</div>
        </Modal>
      )}
    </div>
  )
}
