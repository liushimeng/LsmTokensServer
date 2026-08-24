import { useEffect, useState } from 'react'
import { get, post, download } from '../shared/api'
import Modal from './Modal'

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

// ===== 用户日志弹窗 =====
function UserLogDialog({ onClose }) {
  const [page, setPage] = useState(1)
  const [keyword, setKeyword] = useState('')
  const [input, setInput] = useState('')
  const [data, setData] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    setErr('')
    post('UserInfoLogInterface', { page_num: page, page_size: 100, search_keyword: keyword })
      .then(setData)
      .catch((e) => setErr(e.message || '加载失败'))
  }, [page, keyword])

  const totalPages = data ? (data.total_pages || 0) : 0
  return (
    <Modal title="用户日志" onClose={onClose} width={860}
           footer={<>
             <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
             <span style={{ fontSize: 12 }}>第 {data ? data.page_num : page} / {totalPages} 页</span>
             <button className="btn btn-sm" disabled={!data || !data.has_more} onClick={() => setPage(page + 1)}>下一页</button>
           </>}>
      <div className="toolbar">
        <input style={{ flex: 1 }} value={input} placeholder="关键词搜索…"
               onChange={(e) => setInput(e.target.value)}
               onKeyDown={(e) => { if (e.key === 'Enter') { setKeyword(input); setPage(1) } }} />
        <button className="btn btn-sm btn-primary" onClick={() => { setKeyword(input); setPage(1) }}>搜索</button>
        {keyword ? <button className="btn btn-sm" onClick={() => { setKeyword(''); setInput(''); setPage(1) }}>清除</button> : null}
      </div>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {data && data.is_search && (
        <p style={{ fontSize: 12, color: 'var(--muted)' }}>搜索「{data.search_keyword}」匹配 {data.match_count} 条（总 {data.count} 条）</p>
      )}
      {data && data.lines && data.lines.length ? (
        <div className="log-box">{data.lines.join('\n')}</div>
      ) : (
        !err && <div className="table-empty">{data ? (data.is_search ? '无匹配记录' : '暂无日志') : '加载中…'}</div>
      )}
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
function CertDialog({ onClose }) {
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    get('CertDownloadInfoInterface').then((d) => {
      if (d.error) { setErr(d.error); return }
      setInfo(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  return (
    <Modal title="HTTPS 证书下载" onClose={onClose} width={620}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          <dl className="kv">
            <dt>代理地址</dt><dd>{info.agent_host}{info.https_port ? `:${info.https_port}` : ''}</dd>
            <dt>Anthropic 路径</dt><dd>{info.anthropic_path || '-'}</dd>
            <dt>OpenAI 路径</dt><dd>{info.openai_path || '-'}</dd>
            <dt>证书文件</dt><dd>{info.cert_file || '-'}</dd>
            <dt>证书状态</dt><dd>{info.cert_exists ? `存在（${info.cert_size} 字节）` : '不存在'}</dd>
            <dt>HTTPS 代理</dt><dd>{info.https_enabled ? '已启用' : '未启用'}</dd>
            <dt>Web HTTPS</dt><dd>{info.user_web_enabled ? '已启用' : '未启用'}</dd>
          </dl>
          <button className="btn btn-primary" disabled={!info.cert_exists}
                  onClick={() => download('CertDownloadInterface')}>下载证书</button>
        </>
      )}
      {!info && !err && <div className="table-loading">加载中…</div>}
    </Modal>
  )
}

// ===== Git 信息弹窗 =====
function GitDialog({ onClose }) {
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    get('GitInfoInterface').then((d) => {
      if (d.error) { setErr(d.error); return }
      setInfo(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  return (
    <Modal title="Git 信息" onClose={onClose} width={760}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          <p style={{ fontSize: 13 }}>分支：<strong>{info.branch || '-'}</strong>
            {info.remote ? <span style={{ color: 'var(--muted)' }}>（{info.remote}）</span> : null}
            ，共 {info.count || 0} 次提交</p>
          <div className="table-wrap"><table className="data-table">
            <thead><tr><th>Hash</th><th>作者</th><th>日期</th><th>说明</th></tr></thead>
            <tbody>{(info.commits || []).map((c) => (
              <tr key={c.hash}>
                <td><code>{String(c.hash || '').slice(0, 7)}</code></td>
                <td>{c.author}</td>
                <td>{c.date}</td>
                <td className="wrap">{c.message}</td>
              </tr>
            ))}</tbody>
          </table></div>
        </>
      )}
      {!info && !err && <div className="table-loading">加载中…</div>}
    </Modal>
  )
}

// ===== 系统信息弹窗 =====
function SysDialog({ onClose }) {
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')

  useEffect(() => {
    get('SystemInfoInterface').then((d) => {
      if (d.error) { setErr(d.error); return }
      setInfo(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  const Row = ({ k, v }) => <><dt>{k}</dt><dd>{v}</dd></>
  return (
    <Modal title="系统信息" onClose={onClose} width={720}>
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

// 工具按钮注册表
const DIALOGS = {
  userlog: { label: '用户日志', Comp: UserLogDialog },
  wiki: { label: 'Wiki', Comp: WikiDialog },
  cert: { label: '证书', Comp: CertDialog },
  git: { label: 'Git', Comp: GitDialog },
  sysinfo: { label: '系统信息', Comp: SysDialog },
  readme: { label: 'README', Comp: ReadmeDialog },
  buildlog: { label: '构建日志', Comp: BuildLogDialog },
}

// 顶部工具栏：一排小按钮 + 对应 Modal
export default function ToolbarDialogs() {
  const [open, setOpen] = useState(null)
  const Current = open && DIALOGS[open] ? DIALOGS[open].Comp : null
  return (
    <div className="header-right" style={{ gap: 4 }}>
      {Object.entries(DIALOGS).map(([key, d]) => (
        <button key={key} className="btn btn-link btn-sm" style={{ color: '#d1d5db' }}
                onClick={() => setOpen(key)}>{d.label}</button>
      ))}
      {Current ? <Current onClose={() => setOpen(null)} /> : null}
    </div>
  )
}
