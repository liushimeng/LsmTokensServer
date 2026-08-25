import { Fragment, useEffect, useState } from 'react'
import { get, post, download } from '../shared/api'
import Modal from './Modal'
import DataTable from './DataTable'

// 顶部工具栏弹窗组（迁移自旧 server_web_common_dialog_*.go / server_web_common_wiki.go）：
// 用户日志 / Wiki / 证书 / Git 信息 / 系统信息 / README / 构建日志

// HTML 转义
function esc(s) {
  return String(s || '').replace(/[&<>"']/g, (c) => (
    { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]
  ))
}

// 极简 Markdown → HTML（标题/粗体/斜体/行内代码/代码块/链接/列表/引用），不引第三方库
export function renderMarkdown(md) {
  const lines = String(md || '').split('\n')
  const out = []
  let inCode = false, listType = null // listType: 'ul' | 'ol' | null

  const closeList = () => { if (listType) { out.push(`</${listType}>`); listType = null } }
  const inline = (text) => esc(text)
    .replace(/`([^`]+)`/g, '<code>$1</code>')
    .replace(/\*\*([^*]+)\*\*/g, '<strong>$1</strong>')
    .replace(/(^|[^*])\*([^*\s][^*]*)\*/g, '$1<em>$2</em>')
    .replace(/\[([^\]]+)\]\(([^)\s]+)\)/g, '<a href="$2" target="_blank" rel="noreferrer">$1</a>')

  for (const line of lines) {
    // 代码块围栏
    if (/^\s*```/.test(line)) {
      closeList()
      out.push(inCode ? '</code></pre>' : '<pre><code>')
      inCode = !inCode
      continue
    }
    if (inCode) { out.push(esc(line)); continue }
    if (!line.trim()) { closeList(); continue }
    const h = line.match(/^(#{1,6})\s+(.*)$/)
    if (h) { closeList(); out.push(`<h${h[1].length}>${inline(h[2])}</h${h[1].length}>`); continue }
    const ul = line.match(/^\s*[-*+]\s+(.*)$/)
    if (ul) { if (listType !== 'ul') { closeList(); out.push('<ul>'); listType = 'ul' } out.push(`<li>${inline(ul[1])}</li>`); continue }
    const ol = line.match(/^\s*\d+[.)]\s+(.*)$/)
    if (ol) { if (listType !== 'ol') { closeList(); out.push('<ol>'); listType = 'ol' } out.push(`<li>${inline(ol[1])}</li>`); continue }
    const bq = line.match(/^>\s?(.*)$/)
    if (bq) { closeList(); out.push(`<blockquote>${inline(bq[1])}</blockquote>`); continue }
    if (/^\s*(-{3,}|\*{3,})\s*$/.test(line)) { closeList(); out.push('<hr/>'); continue }
    closeList()
    out.push(`<p>${inline(line)}</p>`)
  }
  closeList()
  if (inCode) out.push('</code></pre>')
  return out.join('\n')
}

// Markdown 内容展示组件
function MarkdownView({ md }) {
  return <div className="md-body" dangerouslySetInnerHTML={{ __html: renderMarkdown(md) }} />
}

// ===== 用户日志弹窗（数据库版，DataTable 结构化展示） =====
const ACTION_TYPES = [
  { value: '', label: '全部类型' },
  { value: 'LOGIN', label: 'LOGIN' },
  { value: 'MANAGER_LOGIN', label: 'MANAGER_LOGIN' },
  { value: 'MANAGER_LOGIN_FAIL', label: 'MANAGER_LOGIN_FAIL' },
  { value: 'ADD_MODEL', label: 'ADD_MODEL' },
  { value: 'UPDATE_MODEL', label: 'UPDATE_MODEL' },
  { value: 'DELETE_MODEL', label: 'DELETE_MODEL' },
  { value: 'UPDATE_MODEL_STATUS', label: 'UPDATE_MODEL_STATUS' },
  { value: 'ADD_ENDPOINT', label: 'ADD_ENDPOINT' },
  { value: 'UPDATE_ENDPOINT', label: 'UPDATE_ENDPOINT' },
  { value: 'TOGGLE_ENDPOINT', label: 'TOGGLE_ENDPOINT' },
  { value: 'DELETE_ENDPOINT', label: 'DELETE_ENDPOINT' },
  { value: 'ADD_USER', label: 'ADD_USER' },
  { value: 'UPDATE_USER', label: 'UPDATE_USER' },
  { value: 'DELETE_USER', label: 'DELETE_USER' },
  { value: 'UPDATE_USER_STATUS', label: 'UPDATE_USER_STATUS' },
]

// 操作类型颜色映射
function actionChipStyle(type) {
  const t = (type || '').toUpperCase()
  if (t.includes('FAIL')) return { bg: '#fecaca', color: '#991b1b' }       // 红色
  if (t.startsWith('LOGIN')) return { bg: '#bfdbfe', color: '#1e40af' }    // 蓝色
  if (t.startsWith('ADD')) return { bg: '#bbf7d0', color: '#166534' }      // 绿色
  if (t.startsWith('DELETE')) return { bg: '#fecaca', color: '#991b1b' }   // 红色
  if (t.startsWith('UPDATE') || t.startsWith('TOGGLE')) return { bg: '#fde68a', color: '#92400e' } // 橙色
  if (t.includes('ROUTE') || t.includes('AIRoute')) return { bg: '#ddd6fe', color: '#5b21b6' } // 紫色
  return { bg: '#e5e7eb', color: '#374151' }                               // 灰色
}

function UserLogDialog({ onClose }) {
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [keyword, setKeyword] = useState('')
  const [input, setInput] = useState('')
  const [actionType, setActionType] = useState('')
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    setErr('')
    post('UserInfoLogInterface', {
      page_num: page, page_size: pageSize,
      search_keyword: keyword, action_type: actionType,
    })
      .then(setData)
      .catch((e) => setErr(e.message || '加载失败'))
      .finally(() => setLoading(false))
  }, [page, pageSize, keyword, actionType])

  const totalPages = data ? (data.total_pages || 0) : 0
  const totalCount = data ? (data.total_count || 0) : 0

  const columns = [
    { key: 'created_at', title: '时间', width: 155, nowrap: true },
    { key: 'action_type', title: '操作类型', width: 150,
      render: (v) => {
        const s = actionChipStyle(v)
        return <span className="action-chip" style={{ background: s.bg, color: s.color }}>{v}</span>
      },
    },
    { key: 'user_name', title: '用户', width: 100 },
    { key: 'details', title: '详情' },
  ]

  const doSearch = () => { setKeyword(input); setActionType(input ? '' : actionType); setPage(1) }
  const doReset = () => { setKeyword(''); setInput(''); setActionType(''); setPage(1) }

  return (
    <Modal title="用户日志" onClose={onClose} width={960}>
      <div className="toolbar" style={{ flexWrap: 'wrap', gap: 8 }}>
        <input style={{ flex: 1, minWidth: 150 }} value={input} placeholder="关键词搜索…"
               onChange={(e) => setInput(e.target.value)}
               onKeyDown={(e) => { if (e.key === 'Enter') doSearch() }} />
        <select value={actionType} onChange={(e) => { setActionType(e.target.value); setKeyword(''); setInput(''); setPage(1) }}>
          {ACTION_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
        <button className="btn btn-sm btn-primary" onClick={doSearch}>查询</button>
        {(keyword || actionType) ? <button className="btn btn-sm" onClick={doReset}>重置</button> : null}
      </div>
      {err ? <div className="alert alert-error">{err}</div> : null}
      <DataTable columns={columns} rows={(data && data.records) || []} loading={loading} empty="暂无日志" rowKey="id" />
      <div className="pager">
        <span>共 {totalCount} 条 · 第 {page} / {totalPages || 1} 页</span>
        <select value={pageSize} onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1) }} style={{ fontSize: 12 }}>
          <option value={10}>10 条/页</option>
          <option value={20}>20 条/页</option>
          <option value={50}>50 条/页</option>
          <option value={100}>100 条/页</option>
        </select>
        <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
        <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>下一页</button>
      </div>
    </Modal>
  )
}

// ===== Wiki 弹窗（列表 + Markdown 内容查看）=====
function WikiDialog({ onClose }) {
  const [files, setFiles] = useState(null)
  const [file, setFile] = useState(null) // 当前查看的文件 {path, content}
  const [err, setErr] = useState('')

  useEffect(() => {
    post('WikiInterface', {})
      .then((d) => setFiles(d.files || []))
      .catch((e) => setErr(e.message || '加载失败'))
  }, [])

  const openFile = async (f) => {
    setErr('')
    setFile({ path: f.path, content: '加载中…' })
    try {
      const d = await post('WikiInterface', { action: 'get_content', file_path: f.path })
      if (d.error) { setErr(d.error); setFile(null); return }
      setFile({ path: d.path, content: d.content || '' })
    } catch (e) { setErr(e.message || '读取失败'); setFile(null) }
  }

  return (
    <Modal title={file ? `Wiki — ${file.path}` : 'Wiki 文档'} onClose={file ? () => setFile(null) : onClose} width={860}
           footer={file ? <button className="btn" onClick={() => setFile(null)}>返回列表</button> : null}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {!file && (
        files === null && !err ? <div className="table-loading">加载中…</div> :
        !files.length ? <div className="table-empty">暂无文档</div> :
        <div className="table-wrap"><table className="data-table">
          <thead><tr><th>路径</th><th>文件名</th><th style={{ width: 80 }}>操作</th></tr></thead>
          <tbody>{files.map((f) => (
            <tr key={f.path}>
              <td className="wrap"><code>{f.path}</code></td>
              <td>{f.name}</td>
              <td><button className="btn btn-sm" onClick={() => openFile(f)}>查看</button></td>
            </tr>
          ))}</tbody>
        </table></div>
      )}
      {file && <MarkdownView md={file.content} />}
    </Modal>
  )
}

// ===== 证书下载弹窗 =====
// 复制到剪贴板（clipboard API 优先，降级 execCommand）
async function copyText(text) {
  try {
    if (navigator.clipboard && window.isSecureContext) { await navigator.clipboard.writeText(text); return true }
  } catch { /* 降级 */ }
  try {
    const ta = document.createElement('textarea')
    ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0'
    document.body.appendChild(ta); ta.select()
    document.execCommand('copy'); document.body.removeChild(ta)
    return true
  } catch { return false }
}

function CertDialog({ onClose }) {
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')
  const [copied, setCopied] = useState('')

  useEffect(() => {
    get('CertDownloadInfoInterface').then((d) => {
      if (d.error) { setErr(d.error); return }
      setInfo(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  const doCopy = async (key, text) => {
    if (await copyText(text)) {
      setCopied(key)
      setTimeout(() => setCopied(''), 1500)
    }
  }

  // 完整 HTTPS 接入地址（对齐旧程序：Anthropic / OpenAI 完整 URL + 复制按钮）
  const base = info && info.https_enabled
    ? `https://${info.agent_host}${info.https_port ? `:${info.https_port}` : ''}` : ''
  const urls = [
    { key: 'anthropic', label: 'Anthropic 接入地址', url: base ? `${base}/${String(info.anthropic_path || 'Anthropic').replace(/^\/+/, '')}` : '' },
    { key: 'openai', label: 'OpenAI 接入地址', url: base ? `${base}/${String(info.openai_path || 'OpenAI').replace(/^\/+/, '')}` : '' },
  ]

  return (
    <Modal title="HTTPS 证书下载" onClose={onClose} width={620}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          {urls.map((u) => (
            <div key={u.key} className="toolbar" style={{ marginBottom: 6 }}>
              <span style={{ fontSize: 13, width: 130, flexShrink: 0 }}>{u.label}</span>
              <input readOnly value={u.url || 'HTTPS 未启用'} style={{ flex: 1 }}
                     disabled={!u.url} onFocus={(e) => e.target.select()} />
              <button className="btn btn-sm" disabled={!u.url}
                      onClick={() => doCopy(u.key, u.url)}>
                {copied === u.key ? '已复制' : '复制'}
              </button>
            </div>
          ))}
          <dl className="kv">
            <dt>代理地址</dt><dd>{info.agent_host}{info.https_port ? `:${info.https_port}` : ''}</dd>
            <dt>Anthropic 路径</dt><dd>{info.anthropic_path || '-'}</dd>
            <dt>OpenAI 路径</dt><dd>{info.openai_path || '-'}</dd>
            <dt>证书文件</dt><dd>{info.cert_file || '-'}</dd>
            <dt>证书状态</dt><dd>{info.cert_exists ? `存在（${info.cert_size} 字节）` : '不存在'}</dd>
            <dt>HTTPS 代理</dt><dd>{info.https_enabled ? '已启用' : '未启用'}</dd>
            <dt>Web HTTPS</dt><dd>{info.user_web_https_enabled ? '已启用' : '未启用'}</dd>
          </dl>
          <button className="btn btn-primary" disabled={!info.cert_exists}
                  onClick={() => download('CertDownloadInterface')}>下载证书</button>
          <div className="guide" style={{ marginTop: 14 }}>
            <p style={{ fontSize: 13, fontWeight: 600, margin: '0 0 8px' }}>证书安装指引（跨平台）</p>
            <details>
              <summary>Windows</summary>
              <ol style={{ fontSize: 13, paddingLeft: 18 }}>
                <li>下载证书文件 <code>LsmTokensServer.crt</code></li>
                <li>双击 → 「安装证书」→ 存储区域选「本地计算机」</li>
                <li>选择「将所有的证书都放入下列存储」→ 浏览 → 「受信任的根证书颁发机构」→ 完成</li>
                <li>打开 <code>cmd</code> 执行 <code>certutil -store Root</code> 确认已安装</li>
              </ol>
            </details>
            <details>
              <summary>macOS</summary>
              <ol style={{ fontSize: 13, paddingLeft: 18 }}>
                <li>下载证书文件后双击，加入「钥匙串访问」</li>
                <li>找到该证书 → 双击 → 「信任」→ 安全套接字层设为「始终信任」</li>
                <li>或命令行：<code>sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain LsmTokensServer.crt</code></li>
              </ol>
            </details>
            <details>
              <summary>Ubuntu / Debian</summary>
              <ol style={{ fontSize: 13, paddingLeft: 18 }}>
                <li><code>sudo cp LsmTokensServer.crt /usr/local/share/ca-certificates/</code></li>
                <li><code>sudo update-ca-certificates</code></li>
              </ol>
            </details>
            <details>
              <summary>CentOS / RHEL</summary>
              <ol style={{ fontSize: 13, paddingLeft: 18 }}>
                <li><code>sudo cp LsmTokensServer.crt /etc/pki/ca-trust/source/anchors/</code></li>
                <li><code>sudo update-ca-trust</code></li>
              </ol>
            </details>
          </div>
        </>
      )}
      {!info && !err && <div className="table-loading">加载中…</div>}
    </Modal>
  )
}

// ===== Git 信息弹窗（阶段AA：客户端分页 + 展开时惰性拉取文件变更，弹窗打开零 git show 子进程）=====
const GIT_PAGE_SIZE = 20
function GitDialog({ onClose }) {
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')
  const [page, setPage] = useState(1)
  const [openHash, setOpenHash] = useState(null) // 展开文件变更的 commit hash
  const [changes, setChanges] = useState({})     // hash → 变更列表缓存

  useEffect(() => {
    get('GitInfoInterface?limit=200').then((d) => {
      if (d.error) { setErr(d.error); return }
      setInfo(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  // 惰性拉取单提交文件变更（带本地缓存）
  const toggleCommit = async (hash) => {
    if (openHash === hash) { setOpenHash(null); return }
    setOpenHash(hash)
    if (!changes[hash]) {
      try {
        const d = await get(`GitInfoInterface?action=get_changes&hash=${hash}`)
        setChanges((prev) => ({ ...prev, [hash]: d.changes || [] }))
      } catch { /* 拉取失败保持“无文件变更信息”展示 */ }
    }
  }

  const commits = info ? (info.commits || []) : []
  const totalPages = Math.max(1, Math.ceil(commits.length / GIT_PAGE_SIZE))
  const cur = Math.min(page, totalPages)
  const pageCommits = commits.slice((cur - 1) * GIT_PAGE_SIZE, cur * GIT_PAGE_SIZE)

  return (
    <Modal title="Git 信息" onClose={onClose} width={760}
           footer={<>
             <button className="btn btn-sm" disabled={cur <= 1} onClick={() => setPage(cur - 1)}>上一页</button>
             <span style={{ fontSize: 12 }}>第 {cur} / {totalPages} 页</span>
             <button className="btn btn-sm" disabled={cur >= totalPages} onClick={() => setPage(cur + 1)}>下一页</button>
           </>}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          <p style={{ fontSize: 13 }}>分支：<strong>{info.branch || '-'}</strong>
            {info.remote ? <span style={{ color: 'var(--muted)' }}>（{info.remote}）</span> : null}
            ，共 {info.count || 0} 次提交，展示最近 {commits.length} 条<span style={{ color: 'var(--muted)' }}>（点击行展开文件变更）</span></p>
          <div className="table-wrap"><table className="data-table">
            <thead><tr><th>Hash</th><th>作者</th><th>日期</th><th>说明</th></tr></thead>
            <tbody>{pageCommits.map((c) => (
              <Fragment key={c.hash}>
                <tr className="row-click" onClick={() => toggleCommit(c.hash)}
                    style={openHash === c.hash ? { background: '#eef4ff' } : undefined}>
                  <td><code>{String(c.hash || '').slice(0, 7)}</code></td>
                  <td>{c.author}</td>
                  <td>{c.date}</td>
                  <td className="wrap">{c.message}</td>
                </tr>
                {openHash === c.hash && (
                  <tr><td colSpan={4}>
                    <div className="commit-files">
                      {changes[c.hash]
                        ? (changes[c.hash].length
                            ? changes[c.hash].map((f, i) => (
                                <div key={i}><span className={`chg chg-${f.status}`}>{f.status}</span><code>{f.path}</code></div>
                              ))
                            : '无文件变更信息')
                        : '加载变更中…'}
                    </div>
                  </td></tr>
                )}
              </Fragment>
            ))}</tbody>
          </table></div>
        </>
      )}
      {!info && !err && <div className="table-loading">加载中…</div>}
    </Modal>
  )
}

// ===== 系统信息弹窗（5 秒自动刷新，迁移自旧 setInterval 逻辑）=====
const SYS_REFRESH_MS = 5000
function SysDialog({ onClose }) {
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')
  const [auto, setAuto] = useState(true)

  useEffect(() => {
    let stop = false
    const load = () => get('SystemInfoInterface').then((d) => {
      if (stop) return
      if (d.error) { setErr(d.error); return }
      setErr(''); setInfo(d)
    }).catch((e) => { if (!stop) setErr(e.message || '加载失败') })
    load()
    if (!auto) return undefined
    const timer = setInterval(load, SYS_REFRESH_MS)
    return () => { stop = true; clearInterval(timer) }
  }, [auto])

  const Row = ({ k, v }) => <><dt>{k}</dt><dd>{v}</dd></>
  return (
    <Modal title="系统信息" onClose={onClose} width={720}
           footer={<label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer' }}>
             <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
             自动刷新（每 5 秒）
           </label>}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          <dl className="kv">
            <Row k="主机名" v={info.hostname} />
            <Row k="操作系统" v={`${info.os} / ${info.arch}`} />
            <Row k="Go 版本" v={info.go_version} />
            <Row k="CPU" v={`${info.num_cpu} 核 / 协程 ${info.num_goroutine}`} />
            <Row k="运行时长" v={info.uptime} />
            {(info.cpus || []).slice(0, 2).map((c, i) => (
              <Row key={i} k={`CPU${i}`} v={`${c.model_name}（${c.usage_pct}%）`} />
            ))}
            {info.memory && <Row k="内存" v={`${info.memory.used_human || '-'} / ${info.memory.total_human || '-'}（${info.memory.usage_pct}%）`} />}
            {info.load && <Row k="负载" v={`${info.load.load1} / ${info.load.load5} / ${info.load.load15}`} />}
          </dl>
          {(info.disk || []).filter((d) => d.mounted_on === '/' || d.usage_pct > 0).slice(0, 6).map((d, i) => (
            <p key={i} style={{ fontSize: 12, color: 'var(--muted)' }}>
              磁盘 {d.mounted_on}：{d.used_human || '-'} / {d.size_human || '-'}（{d.usage_pct}%）
            </p>
          ))}
          {info.disk_io && (
            <p style={{ fontSize: 12, color: 'var(--muted)' }}>
              磁盘 IO：读 {info.disk_io.read_mbps_human || '-'} / 写 {info.disk_io.write_mbps_human || '-'}，
              IOPS 读 {info.disk_io.read_ops_sec_human || '-'} / 写 {info.disk_io.write_ops_sec_human || '-'}，
              iowait {info.disk_io.io_wait_pct || 0}%
            </p>
          )}
          {info.process && (
            <p style={{ fontSize: 12, color: 'var(--muted)' }}>
              本进程：PID {info.process.pid}，线程 {info.process.num_threads}，FD {info.process.num_fd}，RSS {info.process.rss_human || '-'}
            </p>
          )}
          {info.network && (
            <p style={{ fontSize: 12, color: 'var(--muted)' }}>
              网络：TCP 连接 {info.network.connections || 0}，监听端口 {(info.network.listen_ports || []).slice(0, 8).join('、') || '-'}
            </p>
          )}
          {info.process_tops && (info.process_tops.cpu || []).length > 0 && (
            <>
              <p style={{ fontSize: 13, fontWeight: 600, margin: '10px 0 6px' }}>CPU Top 进程</p>
              <div className="table-wrap"><table className="data-table">
                <thead><tr><th style={{ width: 70 }}>PID</th><th>名称</th><th style={{ width: 100 }}>CPU</th></tr></thead>
                <tbody>{(info.process_tops.cpu || []).slice(0, 8).map((p, i) => (
                  <tr key={i}><td>{p.pid}</td><td>{p.name}</td><td>{p.usage_pct}%</td></tr>
                ))}</tbody>
              </table></div>
            </>
          )}
        </>
      )}
      {!info && !err && <div className="table-loading">加载中…</div>}
    </Modal>
  )
}

// ===== README 弹窗（Markdown 渲染）=====
function ReadmeDialog({ onClose }) {
  const [content, setContent] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    get('ReadmeInterface').then((d) => {
      if (d.error) { setErr(d.error); return }
      setContent(d.content || '')
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  return (
    <Modal title="README" onClose={onClose} width={860}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {content === null && !err ? <div className="table-loading">加载中…</div> : <MarkdownView md={content} />}
    </Modal>
  )
}

// ===== 构建日志弹窗 =====
function BuildLogDialog({ onClose }) {
  const [data, setData] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    get('BuildTimeLogInterface').then(setData).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  return (
    <Modal title="构建日志" onClose={onClose} width={760}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {data && data.lines && data.lines.length
        ? <div className="log-box">{data.lines.join('\n')}</div>
        : !err && <div className="table-empty">暂无构建日志</div>}
    </Modal>
  )
}

// ===== 源码统计弹窗（迁移自旧 server_web_common_dialog_sourcecode.go）=====
function SourceCodeDialog({ onClose }) {
  const [stats, setStats] = useState(null)
  const [file, setFile] = useState(null) // {name, content}
  const [err, setErr] = useState('')
  const [filter, setFilter] = useState('') // 文件名过滤（对齐旧程序过滤输入框）

  useEffect(() => {
    post('SourceCodeInterface', {}).then((d) => {
      if (d.error) { setErr(d.error); return }
      setStats(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  const filtered = stats && filter
    ? (stats.files || []).filter((f) => (f.name || '').toLowerCase().includes(filter.toLowerCase()))
    : (stats ? stats.files || [] : [])

  const openFile = async (f) => {
    setErr('')
    setFile({ name: f.name, content: '加载中…' })
    try {
      const d = await post('SourceCodeInterface', { action: 'get_content', file_path: f.name })
      if (d.error) { setErr(d.error); setFile(null); return }
      setFile({ name: f.name, content: d.content || '' })
    } catch (e) { setErr(e.message || '读取失败'); setFile(null) }
  }

  return (
    <Modal title={file ? `源码 — ${file.name}` : '源码统计'} onClose={file ? () => setFile(null) : onClose} width={860}
           footer={file ? <button className="btn" onClick={() => setFile(null)}>返回列表</button> : null}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {!file && stats && (
        <>
          <p style={{ fontSize: 13 }}>共 <strong>{stats.total_files}</strong> 个 .go 文件，
            <strong>{stats.total_lines}</strong> 行，总大小 {stats.size_human || '-'}</p>
          <div className="toolbar" style={{ marginBottom: 8 }}>
            <input style={{ flex: 1 }} value={filter} placeholder="文件名过滤…"
                   onChange={(e) => setFilter(e.target.value)} />
            {filter ? <span style={{ fontSize: 12, color: 'var(--muted)' }}>{filtered.length} / {stats.total_files}</span> : null}
          </div>
          <div className="table-wrap"><table className="data-table">
            <thead><tr><th>文件</th><th style={{ width: 100 }}>行数</th><th style={{ width: 100 }}>大小</th><th style={{ width: 80 }}>操作</th></tr></thead>
            <tbody>{filtered.map((f) => (
              <tr key={f.name}>
                <td className="wrap"><code>{f.name}</code></td>
                <td>{f.lines}</td>
                <td>{f.size_human}</td>
                <td><button className="btn btn-sm" onClick={() => openFile(f)}>查看</button></td>
              </tr>
            ))}</tbody>
          </table></div>
        </>
      )}
      {file && <div className="log-box">{file.content}</div>}
      {!stats && !file && !err ? <div className="table-loading">加载中…</div> : null}
    </Modal>
  )
}

// 工具按钮注册表
const DIALOGS = {
  userlog: { label: '用户日志', Comp: UserLogDialog },
  wiki: { label: 'Wiki', Comp: WikiDialog },
  cert: { label: '证书', Comp: CertDialog },
  git: { label: 'Git', Comp: GitDialog },
  sourcecode: { label: '源码', Comp: SourceCodeDialog },
  sysinfo: { label: '系统信息', Comp: SysDialog },
  readme: { label: 'README', Comp: ReadmeDialog },
  buildlog: { label: '构建日志', Comp: BuildLogDialog },
}

// 顶部工具栏：桌面端一排小按钮；≤860px 收进「⋯」下拉面板（见 00 文档 §3.1）
export default function ToolbarDialogs() {
  const [open, setOpen] = useState(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const Current = open && DIALOGS[open] ? DIALOGS[open].Comp : null

  // 打开弹窗或下拉变化时收起下拉
  useEffect(() => { if (open) setMenuOpen(false) }, [open])

  return (
    <div className="header-tools">
      <button className="tools-more" title="更多工具" aria-label="更多工具"
              onClick={() => setMenuOpen((v) => !v)}>⋯</button>
      {menuOpen && <div className="tools-close-mask" onClick={() => setMenuOpen(false)} />}
      <div className={'tools-list' + (menuOpen ? ' open' : '')}>
        {Object.entries(DIALOGS).map(([key, d]) => (
          <button key={key} className="btn btn-link btn-sm tool-btn"
                  onClick={() => setOpen(key)}>{d.label}</button>
        ))}
      </div>
      {Current ? <Current onClose={() => setOpen(null)} /> : null}
    </div>
  )
}
