import { useEffect, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'
import { pickRouteQuery } from '../shared/format'

// 对话查看页（/ChatDialogInterface）
// 两步交互：action=models 拉取用户的模型列表 → action=config 拉取选中模型的对话配置
// （代理地址 / API Key / 协议类型 / 源站列表），供在对话客户端中直接拼接 API 地址使用。
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

  // 拉取用户的模型列表
  const loadModels = async (u = userName) => {
    if (!u.trim()) { setError('请先填写用户名'); return }
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
      setConfig(d.data || {})
    } catch (e) {
      setError(e.message || '获取对话配置失败')
    } finally { setLoadingConfig(false) }
  }

  // 路由带 user_name 时自动拉取模型列表
  useEffect(() => {
    if (init.userName) loadModels(init.userName)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const proxyFullUrl = config
    ? `${config.agent_base_url || ''}${config.protocol_type === 2 ? config.openai_proxy_path : config.anthropic_proxy_path}`
    : ''

  return (
    <div className="page">
      <h2 className="page-title">对话查看</h2>

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
          {loadingConfig ? '加载中…' : '查看对话配置'}
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
              <dt>协议类型</dt><dd>{config.protocol_type === 1 ? 'Anthropic' : config.protocol_type === 2 ? 'OpenAI' : '未知'}</dd>
              <dt>API Key</dt>
              <dd>
                <span className="field-inline">
                  <code>{showKey ? config.api_key : '••••••••••••'}</code>
                  <button className="btn btn-link" onClick={() => setShowKey(!showKey)}>{showKey ? '隐藏' : '显示'}</button>
                </span>
              </dd>
              <dt>代理地址</dt><dd>{config.agent_addr || '-'}:{config.agent_port}</dd>
              <dt>代理路径</dt><dd>{config.proxy_path || '-'}</dd>
              <dt>完整 API 地址</dt><dd>{proxyFullUrl}</dd>
            </dl>
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
