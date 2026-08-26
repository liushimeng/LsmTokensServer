import { Fragment, useEffect, useState } from 'react'
import { get, post, download } from '../shared/api'
import Modal from './Modal'
import DataTable from './DataTable'
import { useI18n } from '../i18n'

// 顶部工具栏弹窗组（迁移自旧 server_web_common_dialog_*.go / server_web_common_wiki.go）：
// 用户日志 / Wiki / 证书 / Git 信息 / 系统信息

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
  const { t } = useI18n()
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
    <Modal title={t('toolbar.userLog')} onClose={onClose} width={960}>
      <div className="toolbar" style={{ flexWrap: 'wrap', gap: 8 }}>
        <input style={{ flex: 1, minWidth: 150 }} value={input} placeholder={t('datatable.searchPlaceholder')}
               onChange={(e) => setInput(e.target.value)}
               onKeyDown={(e) => { if (e.key === 'Enter') doSearch() }} />
        <select value={actionType} onChange={(e) => { setActionType(e.target.value); setKeyword(''); setInput(''); setPage(1) }}>
          {ACTION_TYPES.map((t) => <option key={t.value} value={t.value}>{t.label}</option>)}
        </select>
        <button className="btn btn-sm btn-primary" onClick={doSearch}>{t('common.search')}</button>
        {(keyword || actionType) ? <button className="btn btn-sm" onClick={doReset}>{t('common.reset')}</button> : null}
      </div>
      {err ? <div className="alert alert-error">{err}</div> : null}
      <DataTable columns={columns} rows={(data && data.records) || []} loading={loading} empty={t('toolbar.noData')} rowKey="id" />
      <div className="pager">
        <span>{t('toolbar.total', { count: totalCount })}</span>
        <select value={pageSize} onChange={(e) => { setPageSize(Number(e.target.value)); setPage(1) }} style={{ fontSize: 12 }}>
          <option value={10}>10</option>
          <option value={20}>20</option>
          <option value={50}>50</option>
          <option value={100}>100</option>
        </select>
        <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('datatable.previous')}</button>
        <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>{t('datatable.next')}</button>
      </div>
    </Modal>
  )
}

// ===== Wiki 弹窗（阶段AG：树形目录 + 搜索 + 惰性分块加载文件内容）=====
const WIKI_CHUNK_LINES = 400 // 与后端 wikiContentDefaultLimit 对齐

// 友好大小：1024 进位，保留 1 位小数
function formatWikiSize(bytes) {
  if (!bytes || bytes < 0) return '-'
  if (bytes < 1024) return `${bytes} B`
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`
  return `${(bytes / 1024 / 1024).toFixed(2)} MB`
}

// 友好时间：YYYY-MM-DD HH:mm
function formatWikiTime(iso) {
  if (!iso) return '-'
  try {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return '-'
    const pad = (n) => String(n).padStart(2, '0')
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  } catch { return '-' }
}

// 命中节点及其所有祖先路径（用于搜索时自动展开）
function collectMatchedPaths(node, keyword) {
  if (!keyword || !node) return null
  const lc = keyword.toLowerCase()
  const matched = new Set()
  const walk = (n, ancestors) => {
    const selfMatch = (n.name || '').toLowerCase().includes(lc) || (n.path || '').toLowerCase().includes(lc)
    let childMatch = false
    if (n.children) {
      for (const c of n.children) {
        if (walk(c, [...ancestors, n.path])) childMatch = true
      }
    }
    if (selfMatch || childMatch) {
      matched.add(n.path)
      ancestors.forEach((a) => matched.add(a))
    }
    return selfMatch || childMatch
  }
  walk(node, [])
  return matched
}

// 树节点组件
function WikiTreeNode({ node, depth, expanded, selectedPath, matchedPaths, onToggle, onSelect }) {
  const isDir = node.type === 'dir'
  const isOpen = expanded.has(node.path)
  const isSelected = selectedPath === node.path
  const isMatched = matchedPaths && matchedPaths.has(node.path)
  // 搜索时未命中且无子项命中的非目录节点隐藏
  if (matchedPaths && !isMatched && !isDir) return null
  if (matchedPaths && !isMatched && isDir && !isOpen) {
    const hasMatchInSubtree = (n) => {
      if (matchedPaths.has(n.path)) return true
      return (n.children || []).some(hasMatchInSubtree)
    }
    if (!hasMatchInSubtree(node)) return null
  }

  const indent = { paddingLeft: 8 + depth * 16 }
  const label = isDir ? (isOpen ? '📂' : '📁') : (node.type === 'other' ? '📄' : '📑')
  const sizeStr = isDir ? `${node.child_count} 项` : formatWikiSize(node.size)
  const isLarge = !isDir && node.size > 50 * 1024 // 50KB

  return (
    <>
      <div
        className={'wiki-node' + (isSelected ? ' wiki-node-selected' : '') + (isMatched ? ' wiki-node-match' : '')}
        style={indent}
        onClick={() => (isDir ? onToggle(node.path) : onSelect(node))}
        title={node.path}
      >
        {isDir ? <span className="wiki-caret">{isOpen ? '▾' : '▸'}</span> : <span className="wiki-caret wiki-caret-empty">·</span>}
        <span className="wiki-icon">{label}</span>
        <span className="wiki-name">{node.name}</span>
        <span className="wiki-meta">
          {!isDir && isLarge ? <span className="wiki-chip wiki-chip-warn">大文件（{formatWikiSize(node.size)}）</span> : null}
          <span className="wiki-size">{sizeStr}</span>
          {node.modified_time ? <span className="wiki-time">{formatWikiTime(node.modified_time)}</span> : null}
        </span>
      </div>
      {isDir && isOpen && node.children && node.children.map((c) => (
        <WikiTreeNode
          key={c.path}
          node={c}
          depth={depth + 1}
          expanded={expanded}
          selectedPath={selectedPath}
          matchedPaths={matchedPaths}
          onToggle={onToggle}
          onSelect={onSelect}
        />
      ))}
    </>
  )
}

function WikiDialog({ onClose }) {
  const { t } = useI18n()
  const [tree, setTree] = useState(null)
  const [treeErr, setTreeErr] = useState('')
  const [expanded, setExpanded] = useState(new Set()) // 已展开目录 path 集合
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState(null) // 当前选中文件节点
  const [content, setContent] = useState('')       // 已加载文本（拼接）
  const [loadedLines, setLoadedLines] = useState(0) // 已加载行数
  const [totalLines, setTotalLines] = useState(0)
  const [hasMore, setHasMore] = useState(false)
  const [loading, setLoading] = useState(false)
  const [fileErr, setFileErr] = useState('')
  const [fileMeta, setFileMeta] = useState(null) // {size, modified_time}

  // 初始加载树
  useEffect(() => {
    post('WikiInterface', {})
      .then((d) => {
        if (d.error) { setTreeErr(d.error); return }
        setTree(d.tree)
        // 默认展开根与所有顶层目录
        const init = new Set([''])
        if (d.tree && d.tree.children) {
          for (const c of d.tree.children) {
            if (c.type === 'dir') init.add(c.path)
          }
        }
        setExpanded(init)
      })
      .catch((e) => setTreeErr(e.message || '加载失败'))
  }, [])

  // 展开/折叠
  const toggle = (path) => setExpanded((prev) => {
    const next = new Set(prev)
    if (next.has(path)) next.delete(path); else next.add(path)
    return next
  })
  const expandAll = () => {
    if (!tree) return
    const all = new Set()
    const walk = (n) => { all.add(n.path); (n.children || []).forEach(walk) }
    walk(tree)
    setExpanded(all)
  }
  const collapseAll = () => setExpanded(new Set(['']))

  // 搜索
  const matchedPaths = search ? collectMatchedPaths(tree, search) : null
  // 搜索时：自动把所有命中的祖先目录加入 expanded
  useEffect(() => {
    if (!matchedPaths) return
    setExpanded((prev) => {
      let changed = false
      const next = new Set(prev)
      for (const p of matchedPaths) {
        if (!next.has(p)) { next.add(p); changed = true }
      }
      return changed ? next : prev
    })
  }, [search]) // eslint-disable-line react-hooks/exhaustive-deps

  // 选中文件
  const selectFile = async (node) => {
    if (node.type === 'other') return // 非 .md 不可查看
    setSelected(node)
    setContent('')
    setLoadedLines(0)
    setTotalLines(0)
    setHasMore(false)
    setFileErr('')
    setFileMeta(null)
    setLoading(true)
    try {
      const d = await post('WikiInterface', { action: 'get_content', file_path: node.path, offset: 0, limit: WIKI_CHUNK_LINES })
      if (d.error) { setFileErr(d.error); setLoading(false); return }
      setContent(d.content || '')
      setLoadedLines((d.offset || 0) + (d.limit || 0))
      setTotalLines(d.total_lines || 0)
      setHasMore(!!d.has_more)
      setFileMeta({ size: d.size, modified_time: d.modified_time })
    } catch (e) { setFileErr(e.message || '读取失败') }
    setLoading(false)
  }

  // 加载更多
  const loadMore = async () => {
    if (!selected || loading || !hasMore) return
    setLoading(true)
    setFileErr('')
    try {
      const d = await post('WikiInterface', { action: 'get_content', file_path: selected.path, offset: loadedLines, limit: WIKI_CHUNK_LINES })
      if (d.error) { setFileErr(d.error); setLoading(false); return }
      setContent((prev) => prev + '\n' + (d.content || ''))
      setLoadedLines((d.offset || 0) + (d.limit || 0))
      setHasMore(!!d.has_more)
    } catch (e) { setFileErr(e.message || '加载更多失败') }
    setLoading(false)
  }

  // 面包屑
  const crumbs = selected ? selected.path.split('/').filter(Boolean) : []
  const totalFiles = tree ? (() => {
    let n = 0
    const walk = (x) => { if (x.type === 'file') n++; (x.children || []).forEach(walk) }
    walk(tree)
    return n
  })() : 0

  return (
    <Modal title={t('toolbar.wiki')} onClose={onClose} width={1000}>
      <div className="wiki-toolbar">
        <input
          className="wiki-search"
          placeholder="搜索文件名或路径…"
          value={search}
          onChange={(e) => setSearch(e.target.value)}
        />
        <button className="btn btn-sm" onClick={expandAll}>{t('common.expand')}</button>
        <button className="btn btn-sm" onClick={collapseAll}>{t('common.collapse')}</button>
        <span className="wiki-stats">{t('common.total', { count: totalFiles })} .md</span>
      </div>
      {treeErr ? <div className="alert alert-error">{treeErr}</div> : null}
      <div className="wiki-layout">
        <div className="wiki-tree-pane">
          {!tree && !treeErr ? <div className="table-loading">加载中…</div> :
            tree ? (
              <div className="wiki-tree">
                <WikiTreeNode
                  node={tree}
                  depth={0}
                  expanded={expanded}
                  selectedPath={selected ? selected.path : null}
                  matchedPaths={matchedPaths}
                  onToggle={toggle}
                  onSelect={selectFile}
                />
              </div>
            ) : null
          }
        </div>
        <div className="wiki-content-pane">
          {selected ? (
            <>
              <div className="wiki-breadcrumb">
                {crumbs.map((seg, i) => (
                  <span key={i}>
                    {i > 0 ? <span className="wiki-crumb-sep">/</span> : null}
                    <span className="wiki-crumb">{seg}</span>
                  </span>
                ))}
                {fileMeta ? <span className="wiki-content-meta"> · {formatWikiSize(fileMeta.size)} · {formatWikiTime(fileMeta.modified_time)}</span> : null}
              </div>
              <div className="wiki-content">
                {loading && !content ? (
                  <div className="wiki-loading-indicator">
                    <span className="wiki-loading-spinner" /> 加载中…
                  </div>
                ) : fileErr ? (
                  <div className="alert alert-error">{fileErr}</div>
                ) : content ? (
                  <MarkdownView md={content} />
                ) : (
                  <div className="wiki-welcome">
                    <p>文件内容为空</p>
                  </div>
                )}
                {hasMore && content ? (
                  <div className="wiki-loadmore">
                    <button className="btn" onClick={loadMore} disabled={loading}>
                      {loading ? '加载中…' : `加载更多（已加载 ${loadedLines} / ${totalLines} 行）`}
                    </button>
                  </div>
                ) : loadedLines > 0 && content ? <div className="wiki-content-end">— 已加载全部 {loadedLines} 行 —</div> : null}
              </div>
            </>
          ) : (
            <div className="wiki-welcome">
              <p>👈 请从左侧树选择一个 Markdown 文件查看内容</p>
              <p className="wiki-welcome-hint">支持全局搜索（按文件名/路径），目录可展开/折叠。</p>
            </div>
          )}
        </div>
      </div>
    </Modal>
  )
}

// ===== 证书安装指南弹窗（阶段AI：完整接入地址 + 证书元信息 + 跨平台安装指引）=====
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

// 跨平台安装指引（每段 4 步：下载 → 安装 → 验证 → 客户端配置）
const CERT_INSTALL_GUIDES = [
  {
    icon: '🍎', os: 'macOS',
    steps: [
      {
        title: '下载证书',
        body: () => <>点击上方「下载证书」按钮，保存到 <code>~/Downloads/LsmTokensServer.crt</code>。</>,
      },
      {
        title: '导入到钥匙串',
        body: () => (
          <>
            <p style={{ margin: '4px 0' }}>双击下载的 <code>.crt</code> 文件，默认会导入到「登录」钥匙串。</p>
            <p style={{ margin: '4px 0', color: 'var(--muted)', fontSize: 12 }}>命令行等价步骤（需管理员权限）：</p>
            <pre className="cert-code-block" data-copy="mac-import"><code>{`open ~/Downloads/LsmTokensServer.crt\n# 或：钥匙串访问 → 系统 → 文件 → 导入项目`}</code></pre>
          </>
        ),
      },
      {
        title: '设为始终信任',
        body: () => (
          <>
            <p style={{ margin: '4px 0' }}>在「钥匙串访问」中找到该证书 → 双击 → 展开「信任」→ 「安全套接字层」设为「始终信任」。</p>
            <pre className="cert-code-block" data-copy="mac-trust"><code>{`sudo security add-trusted-cert -d -r trustRoot \\\n  -k /Library/Keychains/System.keychain \\\n  ~/Downloads/LsmTokensServer.crt`}</code></pre>
          </>
        ),
      },
      {
        title: '验证 & 客户端配置',
        body: (info) => (
          <>
            <pre className="cert-code-block" data-copy="mac-verify"><code>{`curl -vI ${info?.public_anthropic_url || 'https://your-host:29003/Anthropic'}\n# 应输出：SSL certificate verify ok`}</code></pre>
            <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>
              客户端（Claude Desktop / Cursor / Continue 等）把 HTTPS 接入地址填入 <code>base_url</code> 字段即可。
            </p>
          </>
        ),
      },
    ],
  },
  {
    icon: '🪟', os: 'Windows',
    steps: [
      { title: '下载证书', body: () => <>点击上方「下载证书」按钮，保存为 <code>LsmTokensServer.crt</code>。</> },
      {
        title: '导入到受信任的根证书颁发机构',
        body: () => (
          <>
            <p style={{ margin: '4px 0' }}>GUI：双击 <code>.crt</code> → 「安装证书」→ 存储区域选「本地计算机」→ 「将所有的证书都放入下列存储」→ 「受信任的根证书颁发机构」。</p>
            <p style={{ margin: '4px 0', color: 'var(--muted)', fontSize: 12 }}>PowerShell（管理员）一行命令等价：</p>
            <pre className="cert-code-block" data-copy="win-import"><code>{`Import-Certificate -FilePath "$env:USERPROFILE\\Downloads\\LsmTokensServer.crt" \\\n  -CertStoreLocation Cert:\\LocalMachine\\Root`}</code></pre>
          </>
        ),
      },
      {
        title: '验证',
        body: () => (
          <pre className="cert-code-block" data-copy="win-verify"><code>{`certutil -store Root | findstr /I "LsmTokensServer"\n# 出现指纹行即表示已安装`}</code></pre>
        ),
      },
      {
        title: '客户端配置',
        body: () => <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>把 HTTPS 接入地址填入客户端 <code>base_url</code> 字段（Claude Desktop / Cursor / Continue 等）。</p>,
      },
    ],
  },
  {
    icon: '🐧', os: 'Ubuntu / Debian',
    steps: [
      { title: '下载证书', body: () => <>点击上方「下载证书」按钮，保存到 <code>~/Downloads/LsmTokensServer.crt</code>。</> },
      {
        title: '安装到系统信任池',
        body: () => (
          <pre className="cert-code-block" data-copy="ubuntu-install"><code>{`sudo cp ~/Downloads/LsmTokensServer.crt /usr/local/share/ca-certificates/\nsudo update-ca-certificates\n# 末尾输出：1 added, 0 removed; done.`}</code></pre>
        ),
      },
      {
        title: '验证',
        body: () => (
          <pre className="cert-code-block" data-copy="ubuntu-verify"><code>{`awk '/BEGIN/,/END/' /etc/ssl/certs/ca-certificates.crt | grep -c "BEGIN"\n# 比更新前 +1 表示新证书已并入系统信任池`}</code></pre>
        ),
      },
      { title: '客户端配置', body: () => <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>curl / wget / git / Python requests / Node.js 18+ 都会跟随系统 CA；客户端 <code>base_url</code> 直接使用 HTTPS 接入地址。</p> },
    ],
  },
  {
    icon: '🎩', os: 'CentOS / RHEL / Rocky / Alma',
    steps: [
      { title: '下载证书', body: () => <>点击上方「下载证书」按钮，假设保存到 <code>~/LsmTokensServer.crt</code>。</> },
      {
        title: '安装到系统信任池',
        body: () => (
          <pre className="cert-code-block" data-copy="centos-install"><code>{`sudo cp ~/LsmTokensServer.crt /etc/pki/ca-trust/source/anchors/\nsudo update-ca-trust`}</code></pre>
        ),
      },
      {
        title: '验证',
        body: () => (
          <>
            <pre className="cert-code-block" data-copy="centos-verify"><code>{`trust list | grep -i LsmTokensServer\n# CentOS 7：update-ca-certificates --fresh`}</code></pre>
            <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>CentOS 8+ / RHEL 8+ / Rocky / Alma 用 <code>update-ca-trust</code>；CentOS 7 用 <code>update-ca-certificates --fresh</code>。</p>
          </>
        ),
      },
      { title: '客户端配置', body: () => <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>把 HTTPS 接入地址填入客户端 <code>base_url</code> 字段。</p> },
    ],
  },
  {
    icon: '🌿', os: 'Fedora',
    steps: [
      { title: '下载证书', body: () => <>点击上方「下载证书」按钮，假设保存到 <code>~/LsmTokensServer.crt</code>。</> },
      {
        title: '安装到系统信任池',
        body: () => (
          <pre className="cert-code-block" data-copy="fedora-install"><code>{`sudo cp ~/LsmTokensServer.crt /etc/pki/ca-trust/source/anchors/\nsudo update-ca-trust extract`}</code></pre>
        ),
      },
      {
        title: '验证',
        body: () => <pre className="cert-code-block" data-copy="fedora-verify"><code>{`trust list | grep -i LsmTokensServer`}</code></pre>,
      },
      { title: '客户端配置', body: () => <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>把 HTTPS 接入地址填入客户端 <code>base_url</code> 字段。</p> },
    ],
  },
  {
    icon: '🏛️', os: 'Arch / Manjaro',
    steps: [
      { title: '下载证书', body: () => <>点击上方「下载证书」按钮，假设保存到 <code>~/LsmTokensServer.crt</code>。</> },
      {
        title: '使用 trust 工具',
        body: () => (
          <pre className="cert-code-block" data-copy="arch-install"><code>{`sudo trust anchor --store ~/LsmTokensServer.crt`}</code></pre>
        ),
      },
      {
        title: '验证',
        body: () => <pre className="cert-code-block" data-copy="arch-verify"><code>{`trust list | grep -i LsmTokensServer`}</code></pre>,
      },
      { title: '客户端配置', body: () => <p style={{ margin: '4px 0', fontSize: 12, color: 'var(--muted)' }}>把 HTTPS 接入地址填入客户端 <code>base_url</code> 字段。</p> },
    ],
  },
  {
    icon: '🐧', os: '通用 Linux（snap / flatpak / Docker）',
    steps: [
      {
        title: '系统层证书',
        body: () => (
          <p style={{ margin: '4px 0' }}>
            系统层证书已通过上述任一发行版命令安装。但 <code>snap</code> / <code>flatpak</code> / Docker 容器通常自带独立证书栈：
          </p>
        ),
      },
      {
        title: '常见客户端',
        body: () => (
          <ul style={{ margin: '4px 0', paddingLeft: 20, fontSize: 12.5 }}>
            <li><strong>curl / wget / git</strong>：跟随系统 <code>/etc/ssl/certs</code>，无需额外操作</li>
            <li><strong>Python requests</strong>：跟随系统；如使用 <code>certifi</code> 自带证书库，需手工合并</li>
            <li><strong>Node.js 18+</strong>：使用内置 CA，跟随系统</li>
            <li><strong>Docker</strong>：构建镜像时 <code>COPY LsmTokensServer.crt /usr/local/share/ca-certificates/ &amp;&amp; update-ca-certificates</code></li>
            <li><strong>snap 应用</strong>：snap 不读取系统 CA，需在 snap 应用内单独配置</li>
          </ul>
        ),
      },
    ],
  },
]

// 单条接入地址行（含复制按钮）
function CertUrlRow({ label, url, copiedKey, onCopy }) {
  const enabled = !!url
  return (
    <div className="toolbar cert-url-row" style={{ marginBottom: 6 }}>
      <span style={{ fontSize: 13, width: 130, flexShrink: 0 }}>{label}</span>
      <input readOnly value={url || (enabled ? '-' : '未启用')} style={{ flex: 1 }}
             disabled={!enabled} onFocus={(e) => e.target.select()} />
      <button className="btn btn-sm" disabled={!enabled} onClick={() => onCopy(url)}>
        {copiedKey === label ? '已复制' : '复制'}
      </button>
    </div>
  )
}

// 命令代码块（含右上角复制按钮）
function CertCodeBlock({ children, dataCopy }) {
  const [copied, setCopied] = useState(false)
  const handleCopy = async () => {
    // 从 <code> 文本提取纯文本
    const text = String(children).replace(/\s+/g, ' ').trim()
    if (await copyText(text)) {
      setCopied(true)
      setTimeout(() => setCopied(false), 1500)
    }
  }
  return (
    <div style={{ position: 'relative' }}>
      <pre className="cert-code-block" data-copy={dataCopy}>
        <button className="btn btn-sm cert-code-copy" onClick={handleCopy}
                title="复制全部命令">{copied ? '已复制' : '📋 复制'}</button>
        <code>{children}</code>
      </pre>
    </div>
  )
}

function CertDialog({ onClose }) {
  const { t } = useI18n()
  const [info, setInfo] = useState(null)
  const [err, setErr] = useState('')
  const [copied, setCopied] = useState('')

  useEffect(() => {
    get('CertDownloadInfoInterface').then((d) => {
      if (d.error) { setErr(d.error); return }
      setInfo(d)
    }).catch((e) => setErr(e.message || '加载失败'))
  }, [])

  const doCopy = async (label, text) => {
    if (await copyText(text)) {
      setCopied(label)
      setTimeout(() => setCopied(''), 1500)
    }
  }

  // 构造接入地址（优先使用后端新字段 public_*_url / http_*_url；旧字段缺失时前端兜底拼装）
  const httpsUrls = info ? [
    { key: 'https-anthropic', label: 'Anthropic', url: info.public_anthropic_url || (info.https_enabled && info.agent_host ? `${info.https_port ? 'https' : 'https'}://${info.agent_host}${info.https_port ? `:${info.https_port}` : ''}/${(info.anthropic_path || 'Anthropic').replace(/^\/+/, '')}` : '') },
    { key: 'https-openai', label: 'OpenAI', url: info.public_openai_url || (info.https_enabled && info.agent_host ? `${info.https_port ? 'https' : 'https'}://${info.agent_host}${info.https_port ? `:${info.https_port}` : ''}/${(info.openai_path || 'OpenAI').replace(/^\/+/, '')}` : '') },
  ].map((u) => ({ ...u, url: u.url || '' })) : []

  const httpUrls = info ? [
    { key: 'http-anthropic', label: 'Anthropic', url: info.http_anthropic_url || (info.http_port > 0 && info.public_host ? `http://${info.public_host}:${info.http_port}/${(info.anthropic_path || 'Anthropic').replace(/^\/+/, '')}` : '') },
    { key: 'http-openai', label: 'OpenAI', url: info.http_openai_url || (info.http_port > 0 && info.public_host ? `http://${info.public_host}:${info.http_port}/${(info.openai_path || 'OpenAI').replace(/^\/+/, '')}` : '') },
  ].map((u) => ({ ...u, url: u.url || '' })) : []

  const httpEnabled = !!info && info.http_port > 0 && !!info.public_host && (httpUrls.some((u) => u.url))

  return (
    <Modal title={t('toolbar.cert')} onClose={onClose} width={760}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          {/* ① HTTPS 接入地址 */}
          <section className="cert-section">
            <h3 className="cert-section-title">🔒 HTTPS 接入地址（推荐）</h3>
            <p className="cert-msg">
              客户端（Claude Desktop / Cursor / Continue 等）应优先使用 HTTPS 接入地址，
              在系统信任自签证书后即消除「不安全」告警。
            </p>
            {httpsUrls.map((u) => (
              <CertUrlRow key={u.key} label={u.label} url={u.url}
                          copiedKey={copied} onCopy={() => doCopy(u.label, u.url)} />
            ))}
            {!info.https_enabled && (
              <p className="cert-msg" style={{ color: 'var(--danger)' }}>
                ⚠ 当前未启用 HTTPS 代理（agentHttpsListenPort = 0），请先在 <code>LsmTokensServer.conf</code> 启用。
              </p>
            )}
          </section>

          {/* ② HTTP 接入地址 */}
          {httpEnabled && (
            <section className="cert-section">
              <h3 className="cert-section-title">🌐 HTTP 接入地址（明文，仅供内网或调试）</h3>
              <p className="cert-msg">
                明文传输，仅在受信任的内网或调试时使用；公网部署请使用 HTTPS。
              </p>
              {httpUrls.map((u) => (
                <CertUrlRow key={u.key} label={u.label} url={u.url}
                            copiedKey={copied} onCopy={() => doCopy('HTTP-' + u.label, u.url)} />
              ))}
            </section>
          )}

          {/* ③ 证书元信息 */}
          <section className="cert-section">
            <h3 className="cert-section-title">📄 证书信息</h3>
            <dl className="kv">
              <dt>文件路径</dt><dd>{info.cert_file || '-'}</dd>
              <dt>大小</dt>
              <dd>{info.cert_exists ? `${info.cert_size} 字节` : <span style={{ color: 'var(--danger)' }}>不存在</span>}</dd>
              <dt>主题 (Subject)</dt><dd>{info.cert_subject || '-'}</dd>
              <dt>颁发者 (Issuer)</dt><dd>{info.cert_issuer || '-'}</dd>
              <dt>有效期</dt>
              <dd>
                {info.cert_not_before || '-'} ~ {info.cert_not_after || '-'}
                {info.cert_not_after && (
                  <span style={{ marginLeft: 8 }}>
                    {info.cert_expired
                      ? <><span className="status-dot status-off" /><span style={{ color: 'var(--danger)' }}>已过期</span></>
                      : <><span className="status-dot status-on" /><span style={{ color: 'var(--ok)' }}>有效</span></>}
                  </span>
                )}
              </dd>
              <dt>SHA-256 指纹</dt>
              <dd>
                {info.cert_sha256
                  ? (
                    <>
                      <code className="cert-fingerprint">{info.cert_sha256}</code>
                      <button className="btn btn-sm" style={{ marginLeft: 6 }}
                              onClick={() => doCopy('sha256', info.cert_sha256)}>
                        {copied === 'sha256' ? '已复制' : '复制'}
                      </button>
                    </>
                  )
                  : <span style={{ color: 'var(--muted)' }}>无法解析（文件非 PEM 或已损坏）</span>}
              </dd>
              <dt>序列号</dt>
              <dd><code>{info.cert_serial || '-'}</code></dd>
              <dt>HTTPS 代理</dt><dd>{info.https_enabled ? `已启用 :${info.https_port}` : '未启用'}</dd>
              <dt>Web HTTPS</dt><dd>{info.user_web_https_enabled ? '已启用' : '未启用'}</dd>
            </dl>

            <button className="btn btn-primary cert-download-btn" disabled={!info.cert_exists}
                    onClick={() => download('CertDownloadInterface')}>
              ⬇ 下载证书（{info.cert_file || 'server.crt'}）
            </button>
          </section>

          {/* ④ 跨平台安装指引 */}
          <section className="cert-section">
            <h3 className="cert-section-title">📚 跨平台安装指引</h3>
            <p className="cert-msg">
              下载证书后，按系统选择以下步骤。安装完成后浏览器 / curl 调用 HTTPS 接入地址将不再提示「不安全」。
            </p>
            <div className="cert-guide-list">
              {CERT_INSTALL_GUIDES.map((g, gi) => (
                <details key={gi} open={gi === 0 /* macOS 默认展开 */}>
                  <summary>
                    <span className="cert-sys-icon">{g.icon}</span>
                    {g.os}
                  </summary>
                  <div style={{ marginTop: 8 }}>
                    {g.steps.map((step, si) => (
                      <div key={si} className="cert-step">
                        <span className="cert-step-num">{si + 1}</span>
                        <div style={{ flex: 1, minWidth: 0 }}>
                          <div className="cert-step-title">{step.title}</div>
                          <div className="cert-step-body">
                            <StepBody step={step} info={info} />
                          </div>
                        </div>
                      </div>
                    ))}
                  </div>
                </details>
              ))}
            </div>
          </section>
        </>
      )}
      {!info && !err && <div className="table-loading">加载中…</div>}
    </Modal>
  )
}

// StepBody 渲染步骤正文（识别 body 为函数并传入 info；其余按字符串/<> 原样输出）
function StepBody({ step, info }) {
  if (typeof step.body === 'function') {
    const node = step.body(info)
    // 函数返回值如果是数组，包装一层；否则直接返回
    if (Array.isArray(node)) {
      return <>{node.map((n, i) => <span key={i}>{n}</span>)}</>
    }
    return node
  }
  return step.body
}

// ===== Git 信息弹窗（阶段AA：客户端分页 + 展开时惰性拉取文件变更，弹窗打开零 git show 子进程）=====
const GIT_PAGE_SIZE = 20
function GitDialog({ onClose }) {
  const { t } = useI18n()
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
    <Modal title={t('toolbar.git')} onClose={onClose} width={760}
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
                            : t('common.noData'))
                        : t('common.loading')}
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
  const { t } = useI18n()
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
    <Modal title={t('toolbar.sysInfo')} onClose={onClose} width={720}
           footer={<label style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 6, cursor: 'pointer' }}>
             <input type="checkbox" checked={auto} onChange={(e) => setAuto(e.target.checked)} />
             自动刷新（每 5 秒）
           </label>}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {info && (
        <>
          <dl className="kv">
            <Row k="Hostname" v={info.hostname} />
            <Row k="OS" v={`${info.os} / ${info.arch}`} />
            <Row k="Go" v={info.go_version} />
            <Row k="CPU" v={`${info.num_cpu} cores / ${info.num_goroutine} goroutines`} />
            <Row k="Uptime" v={info.uptime} />
            {(info.cpus || []).slice(0, 2).map((c, i) => (
              <Row key={i} k={`CPU${i}`} v={`${c.model_name}（${c.usage_pct}%）`} />
            ))}
            {info.memory && <Row k="Memory" v={`${info.memory.used_human || '-'} / ${info.memory.total_human || '-'} (${info.memory.usage_pct}%)`} />}
            {info.load && <Row k="Load" v={`${info.load.load1} / ${info.load.load5} / ${info.load.load15}`} />}
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

// ===== 构建日志弹窗（阶段AK：结构化构建记录 + 服务端分页） =====
function BuildLogDialog({ onClose }) {
  const { t } = useI18n()
  const [page, setPage] = useState(1)
  const [pageSize, setPageSize] = useState(20)
  const [data, setData] = useState(null)
  const [loading, setLoading] = useState(false)
  const [err, setErr] = useState('')

  useEffect(() => {
    setLoading(true)
    setErr('')
    get(`BuildLogInterface?page_num=${page}&page_size=${pageSize}`)
      .then(setData)
      .catch((e) => setErr(e.message || '加载失败'))
      .finally(() => setLoading(false))
  }, [page, pageSize])

  const totalPages = data ? (data.totalPages || 0) : 0
  const totalCount = data ? (data.totalCount || 0) : 0
  const records = data ? (data.records || []) : []

  // 构建模式标签颜色
  const modeStyle = (mode) => {
    switch (mode) {
      case 'restart':   return { bg: '#dbeafe', color: '#1d4ed8' }
      case 'build-only': return { bg: '#f3f4f6', color: '#6b7280' }
      case 'skip-web':  return { bg: '#fef3c7', color: '#b45309' }
      default:          return { bg: '#f3f4f6', color: '#6b7280' }
    }
  }
  const modeLabel = (mode) => {
    switch (mode) {
      case 'restart':    return t('common.restart')
      case 'build-only': return t('common.buildOnly')
      case 'skip-web':   return t('common.skipWeb')
      default:           return mode || '-'
    }
  }

  // 状态指示
  const statusBadge = (val, label) => {
    if (val === null || val === undefined) return <span className="buildlog-badge buildlog-skip" title={label}>- 跳过</span>
    if (val === true)  return <span className="buildlog-badge buildlog-ok" title={label}>✓ {label}</span>
    return <span className="buildlog-badge buildlog-fail" title={label}>✗ {label}</span>
  }

  // 卡片左边框色条颜色
  const cardBorder = (entry) => {
    if (entry.backend_ok === false) return '#ef4444'
    if (entry.web_ok === false) return '#f59e0b'
    if (entry.backend_ok === true) return '#22c55e'
    return '#94a3b8'
  }

  return (
    <Modal title={t('toolbar.buildLog')} onClose={onClose} width={800}>
      {err ? <div className="alert alert-error">{err}</div> : null}
      {loading && !data ? <div className="table-loading">加载中…</div> : null}
      {!loading && !err && records.length === 0 ? <div className="table-empty">{t('toolbar.noData')}</div> : null}
      {records.length > 0 && (
        <div className="buildlog-list">
          {records.map((entry, i) => {
            const ms = modeStyle(entry.mode)
            return (
              <div className="buildlog-card" key={i} style={{ borderLeftColor: cardBorder(entry) }}>
                <div className="buildlog-head">
                  <span className="buildlog-time">{entry.time || '-'}</span>
                  <div className="buildlog-badges">
                    {entry.mode ? <span className="buildlog-badge" style={{ background: ms.bg, color: ms.color }}>{modeLabel(entry.mode)}</span> : null}
                    {statusBadge(entry.web_ok, '前端')}
                    {statusBadge(entry.backend_ok, '后端')}
                  </div>
                </div>
                {(entry.git_hash || entry.git_msg) && (
                  <div className="buildlog-git">
                    {entry.git_hash ? <span className="buildlog-hash">{entry.git_hash}</span> : null}
                    {entry.git_msg ? <span className="buildlog-msg" title={entry.git_msg}>{entry.git_msg}</span> : null}
                  </div>
                )}
              </div>
            )
          })}
        </div>
      )}
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

// 工具按钮注册表
const DIALOGS = {
  userlog: { labelKey: 'toolbar.userLog', Comp: UserLogDialog },
  wiki: { labelKey: 'toolbar.wiki', Comp: WikiDialog },
  cert: { labelKey: 'toolbar.cert', Comp: CertDialog },
  git: { labelKey: 'toolbar.git', Comp: GitDialog },
  sysinfo: { labelKey: 'toolbar.sysInfo', Comp: SysDialog },
  buildlog: { labelKey: 'toolbar.buildLog', Comp: BuildLogDialog },
}

// 顶部工具栏：桌面端一排小按钮；≤860px 收进「⋯」下拉面板（见 00 文档 §3.1）
export default function ToolbarDialogs() {
  const [open, setOpen] = useState(null)
  const [menuOpen, setMenuOpen] = useState(false)
  const { t } = useI18n()
  const Current = open && DIALOGS[open] ? DIALOGS[open].Comp : null

  // 打开弹窗或下拉变化时收起下拉
  useEffect(() => { if (open) setMenuOpen(false) }, [open])

  return (
    <div className="header-tools">
      <button className="tools-more" title={t('common.more')} aria-label={t('common.more')}
              onClick={() => setMenuOpen((v) => !v)}>⋯</button>
      {menuOpen && <div className="tools-close-mask" onClick={() => setMenuOpen(false)} />}
      <div className={'tools-list' + (menuOpen ? ' open' : '')}>
        {Object.entries(DIALOGS).map(([key, d]) => (
          <button key={key} className="btn btn-link btn-sm tool-btn"
                  onClick={() => setOpen(key)}>{t(d.labelKey)}</button>
        ))}
      </div>
      {Current ? <Current onClose={() => setOpen(null)} /> : null}
    </div>
  )
}
