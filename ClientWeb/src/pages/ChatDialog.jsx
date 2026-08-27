import { useEffect, useRef, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import { useUserModelOptions } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import { pickRouteQuery } from '../shared/format'
import { useI18n } from '../i18n'

// 对话页（/ChatDialogInterface + 同源 AI 代理）
// 两步配置：action=models 拉取模型列表 → action=config 拉取选中模型对话配置
// 交互式对话（对齐旧版 server_web_chat_dialog.go）：
//   发消息 / SSE 流式输出 / 系统提示词 / 协议切换（Anthropic/OpenAI）/
//   消息编辑删除 / 历史与偏好 localStorage 持久化 / 停止生成
// 请求一律走同源相对路径（管理端 9101 / 用户端 29001 mux 均已挂载
// {AgentAnthropicListenURL} 与 {AgentOpenAIListenURL} 代理，无 CORS 问题）。
export default function ChatDialog({ route }) {
  const { t } = useI18n()
  const init = pickRouteQuery(route && route.query)
  const isAdmin = isAdminRole() // 管理端：用户名下拉选择（页面生命周期内缓存一次）；用户端登录态即身份，无用户名控件
  const { users: userOptions } = useUserModelOptions()
  const [userName, setUserName] = useState(init.userName)
  const [models, setModels] = useState([]) // [{id, model_name, api_key_masked}]
  const [modelName, setModelName] = useState(init.modelName)
  const [config, setConfig] = useState(null)
  const [loadingModels, setLoadingModels] = useState(false)
  const [loadingConfig, setLoadingConfig] = useState(false)
  const [error, setError] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [fullKey, setFullKey] = useState('') // 完整 Key 经 reveal_key 按需获取，仅存页面内存（不落 localStorage）

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

  // 拉取用户的模型列表（管理端按选中用户名查询；用户端登录态即身份，不传 user_name）
  const loadModels = async (u = userName) => {
    if (!u.trim() && !userMode) { setError(t('chatDialog.selectUserFirst')); return }
    setLoadingModels(true); setError('')
    try {
      const body = { action: 'models' }
      if (!userMode) body.user_name = u.trim()
      const d = await post('ChatDialogInterface', body)
      setModels(d.data || [])
      // 默认选中路由带入的模型
      if (init.modelName && (d.data || []).some((m) => m.model_name === init.modelName)) {
        setModelName(init.modelName)
        loadConfig(u.trim(), init.modelName)
      }
    } catch (e) {
      setError(e.message || t('chatDialog.getModelsFailed'))
      setModels([])
    } finally { setLoadingModels(false) }
  }

  // 拉取选中模型的对话配置（用户端不传 user_name，后端按登录态鉴权）
  const loadConfig = async (u = userName, m = modelName) => {
    if ((!u.trim() && !userMode) || !m) { setError(t('chatDialog.selectModelFirst')); return }
    setLoadingConfig(true); setError(''); setConfig(null); setShowKey(false); setFullKey('')
    try {
      const cfgBody = { action: 'config', model_name: m }
      if (!userMode) cfgBody.user_name = u.trim()
      const d = await post('ChatDialogInterface', cfgBody)
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
      setError(e.message || t('chatDialog.getConfigFailed'))
    } finally { setLoadingConfig(false) }
  }

  // ========== 历史（localStorage，键名对齐旧版） ==========
  // v2 安全加固：单 key 消息上限 200 条（超出裁掉最旧）、30 天未更新自动过期清理
  const HISTORY_MAX_MESSAGES = 200
  const HISTORY_EXPIRE_MS = 30 * 24 * 60 * 60 * 1000

  const loadHistory = (cfg, pt) => {
    const key = userMode
      ? `lsm_chat_history_user_${cfg.model_name}`
      : `lsm_chat_history_${cfg.user_name || userName}_${cfg.model_name}`
    try {
      const raw = localStorage.getItem(key)
      if (raw) {
        const parsed = JSON.parse(raw)
        // 30 天未更新 → 过期清理
        if (parsed.savedAt && Date.now() - parsed.savedAt > HISTORY_EXPIRE_MS) {
          localStorage.removeItem(key)
          setMessages([]); setSystemPrompt('')
        } else {
          setMessages((parsed.messages || []).slice(-HISTORY_MAX_MESSAGES))
          setSystemPrompt(parsed.systemPrompt || '')
        }
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
      localStorage.setItem(storageKey, JSON.stringify({
        savedAt: Date.now(),
        systemPrompt: sys,
        messages: (msgs || []).slice(-HISTORY_MAX_MESSAGES),
      }))
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

  // 显式获取完整 API Key（config 默认脱敏；「显示」与发送对话时按需调用，结果仅存页面内存）
  const revealKey = async () => {
    if (!config) return ''
    if (fullKey) return fullKey
    const body = { action: 'reveal_key', model_name: config.model_name }
    if (!userMode) body.user_name = (config.user_name || userName || '').trim()
    const d = await post('ChatDialogInterface', body)
    const k = (d && d.data && d.data.api_key) || ''
    setFullKey(k)
    return k
  }

  // 「显示 / 隐藏」完整 Key：首次显示先走 reveal_key 二次获取
  const toggleShowKey = async () => {
    if (!showKey && !fullKey) {
      try { await revealKey() } catch (e) {
        setError((e && e.message) || t('chatDialog.getConfigFailed'))
        return
      }
    }
    setShowKey(!showKey)
  }

  const proxyPath = config
    ? (protocolType === 1 ? (config.anthropic_proxy_path || t('chatDialog.anthropic')) : (config.openai_proxy_path || t('chatDialog.openai')))
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
    if (!window.confirm(t('chatDialog.confirmDeleteMessage'))) return
    const next = messages.slice()
    // user 消息连同紧随其后的 assistant 回复成对删除
    if (next[i].role === 'user' && i + 1 < next.length && next[i + 1].role === 'assistant') next.splice(i, 2)
    else next.splice(i, 1)
    setMessages(next)
  }
  const clearHistory = () => {
    if (!window.confirm(t('chatDialog.confirmClearHistory'))) return
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
      // 完整 API Key 按需揭示（config 响应默认脱敏，仅在此刻获取并暂存页面内存）
      let key = fullKey
      if (!key) key = await revealKey()
      if (!key) throw new Error(t('chatDialog.getConfigFailed'))
      const isAnthropic = protocolType === 1
      const apiPath = isAnthropic ? `${proxyPath}/v1/messages` : `${proxyPath}/chat/completions`
      const headers = {
        'Content-Type': 'application/json',
        'Authorization': 'Bearer ' + key,
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
          cp[msgIndex] = { ...cp[msgIndex], content: text || t('chatDialog.emptyResponse') }
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
        if (!acc) appendChunk(msgIndex, t('chatDialog.emptyResponse'))
      }
    } catch (e) {
      if (e.name === 'AbortError') {
        appendChunk(msgIndex, '\n' + t('chatDialog.generationStopped'))
      } else {
        const errMsg = (e.message === 'Failed to fetch')
          ? t('chatDialog.requestFailedConnection')
          : t('chatDialog.requestFailed', { message: e.message })
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
      <h2 className="page-title">{t('chatDialog.title')}</h2>

      <div className="toolbar">
        {isAdmin ? <>
          <label>{t('chatDialog.selectUser')}
            <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName(''); loadModels(e.target.value) }} style={{ width: 150 }}>
              <option value="">{t('chatDialog.selectUser')}</option>
              {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
            </select>
          </label>
          <button className="btn btn-primary" onClick={() => loadModels()} disabled={loadingModels}>
            {loadingModels ? t('common.loading') : t('chatDialog.loadModels')}
          </button>
        </> : null}
        <label>{t('chatDialog.model')}
          <select value={modelName} onChange={(e) => setModelName(e.target.value)}>
            <option value="">{t('chatDialog.selectModel')}</option>
            {models.map((m) => <option key={m.id} value={m.model_name}>{m.model_name}（{m.api_key_masked}）</option>)}
          </select>
        </label>
        <button className="btn btn-primary" onClick={() => loadConfig()} disabled={!modelName || loadingConfig}>
          {loadingConfig ? t('common.loading') : t('chatDialog.loadConfig')}
        </button>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      {models.length > 0 && !config ? (
        <div className="card">
          <h3>{t('chatDialog.modelList')}</h3>
          <DataTable
            columns={[
              { key: 'id', title: t('chatDialog.id') },
              { key: 'model_name', title: t('chatDialog.model') },
              { key: 'api_key_masked', title: t('chatDialog.apiKey') },
              { key: 'actions', title: t('common.action'), render: (_, r) => (
                <button className="btn btn-sm" onClick={() => { setModelName(r.model_name); loadConfig(userName, r.model_name) }}>{t('chatDialog.viewConfig')}</button>
              ) },
            ]}
            rows={models} rowKey="id" empty={t('chatDialog.noModelsForUser')} />
        </div>
      ) : null}

      {loadingConfig ? <div className="table-loading">{t('chatDialog.configLoading')}</div> : null}

      {config ? (
        <>
          <div className="card">
            <h3>{t('chatDialog.chatConfig', { model: config.model_name })}</h3>
            <dl className="kv">
              <dt>{t('chatDialog.selectUser')}</dt><dd>{config.user_name || '-'}</dd>
              <dt>{t('chatDialog.modelId')}</dt><dd>{config.model_id}</dd>
              <dt>{t('chatDialog.protocolType')}</dt>
              <dd>
                <select
                  value={protocolType}
                  onChange={(e) => {
                    const v = parseInt(e.target.value, 10)
                    setProtocolType(v); saveProtocolPref(v)
                  }}
                >
                  <option value={1}>{t('chatDialog.anthropic')}</option>
                  <option value={2}>{t('chatDialog.openai')}</option>
                  {protocolType !== 1 && protocolType !== 2 ? <option value={protocolType}>{t('chatDialog.unknown', { type: protocolType })}</option> : null}
                </select>
              </dd>
              <dt>{t('chatDialog.apiKey')}</dt>
              <dd>
                <span className="field-inline">
                  <code>{showKey ? fullKey : (config.api_key_masked || config.api_key || '••••••••••••')}</code>
                  <button className="btn btn-link" onClick={toggleShowKey}>{showKey ? t('chatDialog.hide') : t('chatDialog.show')}</button>
                </span>
              </dd>
              <dt>{t('chatDialog.proxyAddr')}</dt><dd>{config.agent_addr || '-'}:{config.agent_port}</dd>
              <dt>{t('chatDialog.proxyPath')}</dt><dd>{proxyPath || '-'}</dd>
              <dt>{t('chatDialog.fullApiUrl')}</dt><dd>{proxyFullUrl}</dd>
            </dl>
          </div>

          <div className="card">
            <h3>{t('chatDialog.title')}</h3>
            <div style={{ marginBottom: 8 }}>
              <label>{t('chatDialog.systemPrompt')}</label>
              <textarea
                rows={2}
                style={{ width: '100%', padding: 8, border: '1px solid #ddd', borderRadius: 6, fontSize: 13, resize: 'vertical', boxSizing: 'border-box' }}
                placeholder={t('chatDialog.systemPromptPlaceholder')}
                value={systemPrompt}
                onChange={(e) => setSystemPrompt(e.target.value)}
              />
            </div>
            <div
              ref={historyRef}
              style={{ height: 340, overflowY: 'auto', border: '1px solid #eee', borderRadius: 6, padding: 8, background: 'var(--bg-2, #fafafa)' }}
            >
              {messages.length === 0 ? (
                <div style={{ color: '#999', textAlign: 'center', padding: 24 }}>{t('chatDialog.noChatHistory')}</div>
              ) : messages.map((m, i) => (
                <div key={i} style={{ marginBottom: 10, display: 'flex', flexDirection: 'column', alignItems: m.role === 'user' ? 'flex-end' : 'flex-start' }}>
                  <div style={{ fontSize: 12, color: '#999', marginBottom: 2 }}>
                    {m.role === 'user' ? t('chatDialog.me') : m.role === 'system' ? t('chatDialog.system') : t('chatDialog.assistant')}
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
                        <button className="btn btn-sm btn-primary" onClick={saveEdit}>{t('common.save')}</button>
                        {' '}<button className="btn btn-sm" onClick={() => { setEditingIndex(-1); setEditText('') }}>{t('common.cancel')}</button>
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
                          <button className="btn btn-sm" onClick={() => startEdit(i)}>{t('common.edit')}</button>
                          {' '}<button className="btn btn-sm" onClick={() => deleteMessage(i)}>{t('common.delete')}</button>
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
                placeholder={t('chatDialog.inputPlaceholderFull')}
                value={chatInput}
                onChange={(e) => setChatInput(e.target.value)}
                onKeyDown={onInputKeydown}
              />
              {sending
                ? <button className="btn" onClick={stopSend}>{t('chatDialog.stop')}</button>
                : <button className="btn btn-primary" onClick={send} disabled={!config}>{t('chatDialog.send')}</button>}
              <label style={{ whiteSpace: 'nowrap', fontSize: 13 }}>
                <input type="checkbox" checked={useStream} onChange={(e) => { setUseStream(e.target.checked); saveStreamPref(e.target.checked) }} /> {t('chatDialog.streaming')}
              </label>
              <button className="btn" onClick={clearHistory}>{t('chatDialog.clearHistory')}</button>
            </div>
          </div>

          <div className="card">
            <h3>{t('chatDialog.targetEndpoints')}</h3>
            <DataTable
              columns={[
                { key: 'id', title: t('chatDialog.id') },
                { key: 'platform_name', title: t('chatDialog.platform') },
                { key: 'model_name', title: t('chatDialog.sourceModel') },
                { key: 'protocol_type', title: t('chatDialog.protocol'), render: (v) => (v === 1 ? t('chatDialog.anthropic') : v === 2 ? t('chatDialog.openai') : v) },
                { key: 'status', title: t('common.status'), render: (v) => (
                  <span><span className={`status-dot ${v === 1 ? 'status-on' : 'status-off'}`} />{v === 1 ? t('common.enabled') : t('common.disabled')}</span>
                ) },
              ]}
              rows={config.endpoints || []} rowKey="id" empty={t('chatDialog.noEndpoints')} />
          </div>
        </>
      ) : null}
    </div>
  )
}
