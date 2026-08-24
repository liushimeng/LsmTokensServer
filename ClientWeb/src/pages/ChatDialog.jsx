import { useEffect, useRef, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'
import { pickRouteQuery } from '../shared/format'

// 对话页（/ChatDialogInterface + 同源 AI 代理）
// 两步配置：action=models 拉取模型列表 → action=config 拉取选中模型对话配置
// 交互式对话（对齐旧版 server_web_chat_dialog.go）：
//   发消息 / SSE 流式输出 / 系统提示词 / 协议切换（Anthropic/OpenAI）/
//   消息编辑删除 / 历史与偏好 localStorage 持久化 / 停止生成
// 请求一律走同源相对路径（管理端 9101 / 用户端 29001 mux 均已挂载
// {AgentAnthropicListenURL} 与 {AgentOpenAIListenURL} 代理，无 CORS 问题）。
export default function ChatDialog({ route }) {
  const init = pickRouteQuery(route && route.query)
  const [userName, setUserName] = useState(init.userName)
  const [models, setModels] = useState([]) // [{id, model_name, api_key_masked}]
  const [modelName, setModelName] = useState(init.modelName)
  const [config, setConfig] = useState(null)
  const [loadingModels, setLoadingModels] = useState(false)
  const [loadingConfig, setLoadingConfig] = useState(false)
  const [error, setError] = useState('')
  const [showKey, setShowKey] = useState(false)

  // 对话状态
  const [messages, setMessages] = useState([]) // [{role:'user'|'assistant'|'system', content}]
  const [systemPrompt, setSystemPrompt] = useState('')
  const [chatInput, setChatInput] = useState('')
  const [useStream, setUseStream] = useState(true)
  const [protocolType, setProtocolType] = useState(0) // 1=Anthropic 2=OpenAI
  const [sending, setSending] = useState(false)
  const [editingIndex, setEditingIndex] = useState(-1)
  const [editText, setEditText] = useState('')
  const abortRef = useRef(null)
  const historyRef = useRef(null)

  // 当前模式（管理端可指定用户名；用户端仅本人）与历史存储键
  const [userMode, setUserMode] = useState(false)
  const storageKey = config
    ? (userMode
        ? `lsm_chat_history_user_${config.model_name}`
        : `lsm_chat_history_${config.user_name || userName}_${config.model_name}`)
    : ''

  // 拉取用户的模型列表
  const loadModels = async (u = userName) => {
    if (!u.trim() && !userMode) { setError('请先填写用户名'); return }
    setLoadingModels(true); setError('')
    try {
      const d = await post('ChatDialogInterface', { action: 'models', user_name: u.trim() })
      setModels(d.data || [])
      // 默认选中路由带入的模型
      if (init.modelName && (d.data || []).some((m) => m.model_name === init.modelName)) {
        setModelName(init.modelName)
        loadConfig(u.trim(), init.modelName)
      }
    } catch (e) {
      setError(e.message || '获取模型列表失败')
      setModels([])
    } finally { setLoadingModels(false) }
  }

  // 拉取选中模型的对话配置
  const loadConfig = async (u = userName, m = modelName) => {
    if (!u.trim() || !m) { setError('请先选择模型'); return }
    setLoadingConfig(true); setError(''); setConfig(null); setShowKey(false)
    try {
      const d = await post('ChatDialogInterface', { action: 'config', user_name: u.trim(), model_name: m })
      const cfg = d.data || {}
      setConfig(cfg)
      // 恢复协议偏好（仅覆盖协议类型）
      let pt = cfg.protocol_type
      try {
        const saved = localStorage.getItem(`lsm_chat_protocol_${cfg.user_name || u}_${cfg.model_name}`)
        if (saved === '1' || saved === '2') pt = parseInt(saved, 10)
      } catch { /* 忽略 */ }
      setProtocolType(pt)
      loadHistory(cfg, pt)
    } catch (e) {
      setError(e.message || '获取对话配置失败')
    } finally { setLoadingConfig(false) }
  }

  // ========== 历史（localStorage，键名对齐旧版） ==========
  const loadHistory = (cfg, pt) => {
    const key = userMode
      ? `lsm_chat_history_user_${cfg.model_name}`
      : `lsm_chat_history_${cfg.user_name || userName}_${cfg.model_name}`
    try {
      const raw = localStorage.getItem(key)
      if (raw) {
        const parsed = JSON.parse(raw)
        setMessages(parsed.messages || [])
        setSystemPrompt(parsed.systemPrompt || '')
      } else {
        setMessages([]); setSystemPrompt('')
      }
    } catch { setMessages([]) }
    // 流式偏好（全局）
    try {
      if (localStorage.getItem('lsm_chat_stream_pref') === '0') setUseStream(false)
    } catch { /* 忽略 */ }
    void pt
  }

  const saveHistory = (msgs, sys) => {
    if (!storageKey) return
    try {
      localStorage.setItem(storageKey, JSON.stringify({ systemPrompt: sys, messages: msgs }))
    } catch { /* 忽略 */ }
  }

  const saveStreamPref = (v) => {
    try { localStorage.setItem('lsm_chat_stream_pref', v ? '1' : '0') } catch { /* 忽略 */ }
  }

  const saveProtocolPref = (v) => {
    if (!config) return
    try { localStorage.setItem(`lsm_chat_protocol_${config.user_name || userName}_${config.model_name}`, String(v)) } catch { /* 忽略 */ }
  }

  // 初始化：探测模式（管理端 login_type=manager）；路由带 user_name 时自动拉模型
  useEffect(() => {
    post('UserInfoInterface').then((d) => {
      const isUser = d && d.data && d.data.login_type && d.data.login_type !== 'manager'
      setUserMode(isUser)
      if (isUser) loadModels('')
    }).catch(() => { /* 探测失败按管理端处理 */ })
    // 路由带 user_name 时自动拉取模型列表
    if (init.userName) loadModels(init.userName)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 消息变化时持久化 + 滚到底部
  useEffect(() => {
    saveHistory(messages, systemPrompt)
    if (historyRef.current) historyRef.current.scrollTop = historyRef.current.scrollHeight
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [messages])

  const proxyPath = config
    ? (protocolType === 1 ? (config.anthropic_proxy_path || 'Anthropic') : (config.openai_proxy_path || 'OpenAI'))
    : ''

  const proxyFullUrl = config
    ? `${config.agent_base_url || ''}${proxyPath}`
    : ''

  // ========== 编辑 / 删除 ==========
  const startEdit = (i) => { setEditingIndex(i); setEditText(messages[i].content) }
  const saveEdit = () => {
    if (!editText.trim()) return
    const next = messages.slice()
    next[editingIndex] = { ...next[editingIndex], content: editText.trim() }
    setMessages(next); setEditingIndex(-1); setEditText('')
  }
  const deleteMessage = (i) => {
    if (!window.confirm('确定删除这条消息？')) return
    const next = messages.slice()
    // user 消息连同紧随其后的 assistant 回复成对删除
    if (next[i].role === 'user' && i + 1 < next.length && next[i + 1].role === 'assistant') next.splice(i, 2)
    else next.splice(i, 1)
    setMessages(next)
  }
  const clearHistory = () => {
    if (!window.confirm('确定清空所有对话历史？（仅清除本地浏览器存储）')) return
    setMessages([]); setSystemPrompt('')
  }

  // ========== 发送 ==========
  const buildRequestBody = (sys, stream, msgs) => {
    // msgs 传入发送时最新数组（含刚追加的 user 消息），避免 setState 异步读到旧值
    const reqMsgs = (msgs || messages).filter((m) => m.role === 'user' || m.role === 'assistant')
      .map((m) => ({ role: m.role, content: m.content }))
    const body = { model: config.model_name, max_tokens: 4096, stream }
    if (protocolType === 1) {
      body.messages = reqMsgs
      if (sys) body.system = sys
    } else {
      body.messages = sys ? [{ role: 'system', content: sys }, ...reqMsgs] : reqMsgs
    }
    return body
  }

  const appendChunk = (idx, chunk) => {
    setMessages((prev) => {
      const next = prev.slice()
      next[idx] = { ...next[idx], content: (next[idx].content || '') + chunk }
      return next
    })
  }

  const send = async () => {
    if (!config || sending) return
    const content = chatInput.trim()
    if (!content) return
    setChatInput('')
    const next = [...messages, { role: 'user', content }, { role: 'assistant', content: '' }]
    setMessages(next)
    setSending(true)
    const msgIndex = next.length - 1
    const sys = systemPrompt.trim()
    try {
      const isAnthropic = protocolType === 1
      const apiPath = isAnthropic ? `${proxyPath}/v1/messages` : `${proxyPath}/chat/completions`
      const headers = {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + config.api_key,
      }
      if (isAnthropic) headers['anthropic-version'] = '2023-06-01'
      abortRef.current = new AbortController()
      const resp = await fetch(apiPath, {
        method: 'POST', headers, credentials: 'include',
        body: JSON.stringify(buildRequestBody(sys, useStream, next)),
        signal: abortRef.current.signal,
      })
      if (!resp.ok) throw new Error('HTTP ' + resp.status + ': ' + (await resp.text()).slice(0, 300))
      if (!useStream) {
        const data = await resp.json()
        const text = isAnthropic
          ? (data.content && data.content[0] && data.content[0].text) || ''
          : (data.choices && data.choices[0] && data.choices[0].message && data.choices[0].message.content) || ''
        setMessages((prev) => {
          const cp = prev.slice()
          cp[msgIndex] = { ...cp[msgIndex], content: text || '(空响应)' }
          return cp
        })
      } else {
        const reader = resp.body.getReader()
        const decoder = new TextDecoder('utf-8')
        let buffer = '', acc = ''
        for (;;) {
          const { done, value } = await reader.read()
          if (done) break
          buffer += decoder.decode(value, { stream: true })
          const lines = buffer.split('\n')
          buffer = lines.pop()
          for (const raw of lines) {
            const line = raw.trim()
            if (!line.startsWith('data: ')) continue
            const payload = line.slice(6)
            if (payload === '[DONE]') continue
            try {
              const parsed = JSON.parse(payload)
              let chunk = ''
              if (isAnthropic) {
                if (parsed.type === 'content_block_delta' && parsed.delta) chunk = parsed.delta.text || ''
              } else if (parsed.choices && parsed.choices[0] && parsed.choices[0].delta) {
                chunk = parsed.choices[0].delta.content || ''
              }
              if (chunk) { acc += chunk; appendChunk(msgIndex, chunk) }
            } catch { /* 忽略解析错误 */ }
          }
        }
        if (!acc) appendChunk(msgIndex, '(空响应)')
      }
    } catch (e) {
      if (e.name === 'AbortError') {
        appendChunk(msgIndex, '\n（已停止生成）')
      } else {
        const errMsg = (e.message === 'Failed to fetch')
          ? '请求失败：无法连接到 Web 服务（服务未运行或被浏览器拦截）'
          : '请求失败: ' + e.message
        setMessages((prev) => {
          const cp = prev.slice()
          cp[msgIndex] = { ...cp[msgIndex], content: errMsg }
          return cp
        })
      }
    } finally {
      setSending(false)
      abortRef.current = null
    }
  }

  const stopSend = () => { if (abortRef.current) abortRef.current.abort() }

  const onInputKeydown = (e) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); send() }
  }

  return (
    <div className="page">
      <h2 className="page-title">对话</h2>

      <div className="toolbar">
        <label>用户名 <input value={userName} onChange={(e) => setUserName(e.target.value)} placeholder="user_name" style={{ width: 140 }} /></label>
        <button className="btn btn-primary" onClick={() => loadModels()} disabled={loadingModels}>
          {loadingModels ? '加载中…' : '加载模型列表'}
        </button>
        <label>模型
          <select value={modelName} onChange={(e) => setModelName(e.target.value)}>
            <option value="">请选择模型</option>
            {models.map((m) => <option key={m.id} value={m.model_name}>{m.model_name}（{m.api_key_masked}）</option>)}
          </select>
        </label>
        <button className="btn btn-primary" onClick={() => loadConfig()} disabled={!modelName || loadingConfig}>
          {loadingConfig ? '加载中…' : '加载对话配置'}
        </button>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      {models.length > 0 && !config ? (
        <div className="card">
          <h3>模型列表</h3>
          <DataTable
            columns={[
              { key: 'id', title: 'ID' },
              { key: 'model_name', title: '模型名称' },
              { key: 'api_key_masked', title: 'API Key' },
              { key: 'actions', title: '操作', render: (_, r) => (
                <button className="btn btn-sm" onClick={() => { setModelName(r.model_name); loadConfig(userName, r.model_name) }}>查看配置</button>
              ) },
            ]}
            rows={models} rowKey="id" empty="该用户暂无模型" />
        </div>
      ) : null}

      {loadingConfig ? <div className="table-loading">对话配置加载中…</div> : null}

      {config ? (
        <>
          <div className="card">
            <h3>对话配置（{config.model_name}）</h3>
            <dl className="kv">
              <dt>用户名</dt><dd>{config.user_name || '-'}</dd>
              <dt>模型 ID</dt><dd>{config.model_id}</dd>
              <dt>协议类型</dt>
              <dd>
                <select
                  value={protocolType}
                  onChange={(e) => {
                    const v = parseInt(e.target.value, 10)
                    setProtocolType(v); saveProtocolPref(v)
                  }}
                >
                  <option value={1}>Anthropic</option>
                  <option value={2}>OpenAI</option>
                  {protocolType !== 1 && protocolType !== 2 ? <option value={protocolType}>未知（{protocolType}）</option> : null}
                </select>
              </dd>
              <dt>API Key</dt>
              <dd>
                <span className="field-inline">
                  <code>{showKey ? config.api_key : '••••••••••••'}</code>
                  <button className="btn btn-link" onClick={() => setShowKey(!showKey)}>{showKey ? '隐藏' : '显示'}</button>
                </span>
              </dd>
              <dt>代理地址</dt><dd>{config.agent_addr || '-'}:{config.agent_port}</dd>
              <dt>代理路径</dt><dd>{proxyPath || '-'}</dd>
              <dt>完整 API 地址</dt><dd>{proxyFullUrl}</dd>
            </dl>
          </div>

          <div className="card">
            <h3>对话</h3>
            <div style={{ marginBottom: 8 }}>
              <label>系统提示词</label>
              <textarea
                rows={2}
                style={{ width: '100%', padding: 8, border: '1px solid #ddd', borderRadius: 6, fontSize: 13, resize: 'vertical', boxSizing: 'border-box' }}
                placeholder="例如：你是一个专业的编程助手，擅长Go语言..."
                value={systemPrompt}
                onChange={(e) => setSystemPrompt(e.target.value)}
              />
            </div>
            <div
              ref={historyRef}
              style={{ height: 340, overflowY: 'auto', border: '1px solid #eee', borderRadius: 6, padding: 8, background: 'var(--bg-2, #fafafa)' }}
            >
              {messages.length === 0 ? (
                <div style={{ color: '#999', textAlign: 'center', padding: 24 }}>暂无对话记录</div>
              ) : messages.map((m, i) => (
                <div key={i} style={{ marginBottom: 10, display: 'flex', flexDirection: 'column', alignItems: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
                  <div style={{ fontSize: 12, color: '#999', marginBottom: 2 }}>
                    {m.role === 'user' ? '👤 我' : m.role === 'system' ? '⚠️ 系统' : '🤖 助手'}
                  </div>
                  {editingIndex === i ? (
                    <div style={{ width: '80%' }}>
                      <textarea
                        rows={3}
                        style={{ width: '100%', padding: 8, border: '1px solid #007aff', borderRadius: 6, fontSize: 13, boxSizing: 'border-box' }}
                        value={editText}
                        onChange={(e) => setEditText(e.target.value)}
                      />
                      <div style={{ marginTop: 4 }}>
                        <button className="btn btn-sm btn-primary" onClick={saveEdit}>保存</button>
                        {' '}<button className="btn btn-sm" onClick={() => { setEditingIndex(-1); setEditText('') }}>取消</button>
                      </div>
                    </div>
                  ) : (
                    <>
                      <div
                        style={{
                          maxWidth: '80%', padding: '8px 12px', borderRadius: 10, whiteSpace: 'pre-wrap', wordBreak: 'break-word', fontSize: 13,
                          background: m.role === 'user' ? '#007aff' : m.role === 'system' ? '#fff3cd' : '#f1f1f1',
                          color: m.role === 'user' ? '#fff' : 'inherit',
                        }}
                      >{m.content || (sending && i === messages.length - 1 ? '…' : '')}</div>
                      {m.role !== 'system' ? (
                        <div style={{ marginTop: 2 }}>
                          <button className="btn btn-sm" onClick={() => startEdit(i)}>编辑</button>
                          {' '}<button className="btn btn-sm" onClick={() => deleteMessage(i)}>删除</button>
                        </div>
                      ) : null}
                    </>
                  )}
                </div>
              ))}
            </div>
            <div style={{ display: 'flex', gap: 8, alignItems: 'flex-end', marginTop: 8 }}>
              <textarea
                rows={2}
                style={{ flex: 1, padding: 8, border: '1px solid #ddd', borderRadius: 6, fontSize: 13, resize: 'vertical' }}
                placeholder="输入消息，Shift+回车换行，回车发送..."
                value={chatInput}
                onChange={(e) => setChatInput(e.target.value)}
                onKeyDown={onInputKeydown}
              />
              {sending
                ? <button className="btn" onClick={stopSend}>停止</button>
                : <button className="btn btn-primary" onClick={send} disabled={!config}>发送</button>}
              <label style={{ whiteSpace: 'nowrap', fontSize: 13 }}>
                <input type="checkbox" checked={useStream} onChange={(e) => { setUseStream(e.target.checked); saveStreamPref(e.target.checked) }} /> 流式输出
              </label>
              <button className="btn" onClick={clearHistory}>清空历史</button>
            </div>
          </div>

          <div className="card">
            <h3>目标源站列表</h3>
            <DataTable
              columns={[
                { key: 'id', title: 'ID' },
                { key: 'platform_name', title: '平台' },
                { key: 'model_name', title: '源站模型' },
                { key: 'protocol_type', title: '协议', render: (v) => (v === 1 ? 'Anthropic' : v === 2 ? 'OpenAI' : v) },
                { key: 'status', title: '状态', render: (v) => (
                  <span><span className={`status-dot ${v === 1 ? 'status-on' : 'status-off'}`} />{v === 1 ? '启用' : '停用'}</span>
                ) },
              ]}
              rows={config.endpoints || []} rowKey="id" empty="该模型暂无可用源站（或未配置路由）" />
          </div>
        </>
      ) : null}
    </div>
  )
}
