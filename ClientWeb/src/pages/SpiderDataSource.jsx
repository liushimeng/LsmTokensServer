import { useEffect, useRef, useState } from 'react'
import { post, openSse } from '../shared/api'
import { fmtTime } from '../shared/format'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { useI18n } from '../i18n'
import { useConfirm } from '../components/ConfirmModal'

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
  const { t } = useI18n()
  const sysConfirm = useConfirm()

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
      setErr(t('spider.loadFailed') + e.message)
    }
  }

  useEffect(() => {
    // 阶段T：角色由构建期常量决定，用户端构建不再探测管理端接口
    setIsAdmin(__APP_ROLE__ === 'manager')
    load()
    return () => { if (esRef.current) esRef.current.close() }
  }, [])

  // 保存（新增 / 修改）
  const save = async () => {
    const f = editing
    if (!f.platform_name || !f.url_address) { setErr(t('spider.platformNameRequired')); return }
    setSaving(true); setErr(''); setMsg('')
    try {
      const action = f.id ? 'update' : 'add'
      const d = await post('SpiderDataSourceInterface', { action, ...f })
      setMsg(d.message || t('spider.saveSuccess'))
      setEditing(null)
      load()
    } catch (e) { setErr(e.message || t('errors.saveFailed')) } finally { setSaving(false) }
  }

  // 删除（仅管理端）
  const remove = async (rec) => {
    if (!(await sysConfirm(t('spider.confirmDeleteSource', { name: rec.platform_name, id: rec.id })))) return
    setErr(''); setMsg('')
    try {
      const d = await post('SpiderDataSourceInterface', { action: 'delete', id: rec.id })
      setMsg(d.message || t('spider.deleteSuccess'))
      load()
    } catch (e) { setErr(e.message || t('errors.deleteFailed')) }
  }

  // 启用/禁用切换
  const toggleStatus = async (rec) => {
    setErr(''); setMsg('')
    try {
      const d = await post('SpiderDataSourceInterface', {
        action: 'toggle_status', id: rec.id, status: rec.status === 1 ? 0 : 1,
      })
      setMsg(d.message || t('spider.statusUpdated'))
      load()
    } catch (e) { setErr(e.message || t('errors.operationFailed')) }
  }

  // 打开爬取弹窗：默认提示词带 {{.DataSourceID}} 模板说明
  // 阶段AY：用 `__DATA_SOURCE_ID__` 一次性占位符替换，避免 rec.id 数字串
  // 与模板内其他位置（如默认值 "1"、"true" 等）误匹配。
  const openCrawl = (rec) => {
    setCrawling(rec)
    const tmpl = defaultPromptTemplate(rec.id)
    const idStr = String(rec.id)
    const replaced = tmpl.indexOf(idStr) >= 0
      ? tmpl.replace(idStr, '{{.DataSourceID}}')
      : tmpl
    setPrompt(replaced + `\n- 当前数据源 ID：${rec.id}（提示词会被原样发送，后端将替换 {{.DataSourceID}}）`)
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
          setLogText((s) => s + '\n' + t('spider.errorPrefix') + ' ' + (e && e.message ? e.message : t('common.networkError')))
        },
        onDone: () => {
          setLogText((s) => s + '\n' + t('spider.donePrefix'))
          setCrawlBusy(false)
          esRef.current = null
        },
      })
  }

  const columns = [
    { key: 'id', title: 'ID', width: 60, sortable: true },
    { key: 'platform_name', title: t('spider.platform'), sortable: true },
    { key: 'url_address', title: 'URL', render: (v) => (
      <a href={v} target="_blank" rel="noreferrer" className="wrap">{v}</a>
    ) },
    { key: 'description', title: t('spider.sourceDescription'), render: (v) => <span className="wrap">{v || '-'}</span> },
    { key: 'remark', title: t('spider.remark') },
    { key: 'status', title: t('common.status'), sortable: true, render: (v) => (
      <span><span className={'status-dot ' + (v === 1 ? 'status-on' : 'status-off')} />{v === 1 ? t('common.enabled') : t('common.disabled')}</span>
    ) },
    { key: 'updated_at', title: t('userManage.updatedAt'), render: (v) => fmtTime(v) },
    { key: 'op', title: t('common.action'), render: (_, rec) => (
      <span className="op-btns" style={{ display: 'flex', gap: 4 }}>
        <button className="btn btn-sm btn-primary" onClick={() => openCrawl(rec)}
                disabled={rec.status !== 1} title={rec.status !== 1 ? t('spider.dataSourceDisabled') : ''}>{t('spider.startCrawl')}</button>
        <button className="btn btn-sm" onClick={() => setEditing({
          id: rec.id, platform_name: rec.platform_name, url_address: rec.url_address,
          description: rec.description || '', remark: rec.remark || '',
        })}>{t('common.edit')}</button>
        <button className="btn btn-sm" onClick={() => toggleStatus(rec)}>{rec.status === 1 ? t('common.disable') : t('common.enable')}</button>
        {isAdmin && <button className="btn btn-sm btn-danger" onClick={() => remove(rec)}>{t('common.delete')}</button>}
      </span>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('spider.dataSourceManagement')}</h2>
      <div className="toolbar">
        <button className="btn btn-primary" onClick={() => setEditing({ ...EMPTY_FORM })}>+ {t('spider.addDataSource')}</button>
        <button className="btn" onClick={load}>{t('common.refresh')}</button>
        <span style={{ color: 'var(--muted)', fontSize: 12 }}>
          {t('spider.crawlThroughOpenClaw')}
        </span>
      </div>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {msg ? <div className="alert alert-ok">{msg}</div> : null}
      <DataTable columns={columns} rows={rows || []} loading={!rows} empty={t('spider.noData')} rowKey="id"
        rowClass={(rec) => (rec.status === 1 ? 'row-enabled' : 'row-disabled')} />

      {/* 新增/编辑弹窗 */}
      {editing && (
        <Modal title={editing.id ? `${t('spider.editDataSourceTitle')} #${editing.id}` : t('spider.addDataSource')}
               onClose={() => setEditing(null)}
               closeOnOverlayClick={false}
               footer={<>
                 <button className="btn" onClick={() => setEditing(null)}>{t('common.cancel')}</button>
                 <button className="btn btn-primary" onClick={save} disabled={saving}>
                   {saving ? t('spider.saving') : t('common.save')}
                 </button>
               </>}>
          <label className="field"><span>{t('spider.platformNameLabel')}</span>
            <input value={editing.platform_name}
                   onChange={(e) => setEditing({ ...editing, platform_name: e.target.value })} />
          </label>
          <label className="field"><span>{t('spider.urlAddressLabel')}</span>
            <input value={editing.url_address} placeholder="https://…"
                   onChange={(e) => setEditing({ ...editing, url_address: e.target.value })} />
          </label>
          <label className="field"><span>{t('spider.sourceDescription')}</span>
            <textarea rows={4} value={editing.description}
                      onChange={(e) => setEditing({ ...editing, description: e.target.value })} />
          </label>
          <label className="field"><span>{t('spider.remark')}</span>
            <input value={editing.remark}
                   onChange={(e) => setEditing({ ...editing, remark: e.target.value })} />
          </label>
        </Modal>
      )}

      {/* 爬取弹窗：SSE 流式输出到 .log-box */}
      {crawling && (
        <Modal title={t('spider.aiCrawl', { name: crawling.platform_name, id: crawling.id })} onClose={closeCrawl} width={860} closeOnOverlayClick={false}
               footer={<>
                 <button className="btn" onClick={closeCrawl} disabled={crawlBusy}>{t('toolbar.close')}</button>
                 <button className="btn btn-primary" onClick={startCrawl} disabled={crawlBusy}>
                   {crawlBusy ? t('spider.crawling') : t('spider.startCrawl')}
                 </button>
               </>}>
          <label className="field"><span>{t('spider.promptLabel')}</span>
            <textarea className="crawl-prompt" rows={8} value={prompt} onChange={(e) => setPrompt(e.target.value)} />
          </label>
          <div style={{ fontSize: 12, color: 'var(--muted)', marginBottom: 6 }}>{t('spider.crawlOutput')}</div>
          <div className="log-box crawl-log" ref={(el) => { if (el) el.scrollTop = el.scrollHeight }}>{logText || t('spider.notStarted')}</div>
        </Modal>
      )}
    </div>
  )
}
