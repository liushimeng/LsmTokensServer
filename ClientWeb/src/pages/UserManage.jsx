import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'

// 用户管理：UserManageInterface + UserModelManageInterface（均 POST JSON {action:...}）
// 分析页跳转：#/ChatAnalysis?user_name=..&model_name=.. 等（对照旧版 nav）

const ANALYSIS_PAGES = [
  { page: 'ChatAnalysis', label: '浏览记录' },
  { page: 'ChatAnalysisTotal', label: '统计' },
  { page: 'ChatAnalysisSession', label: 'Session' },
  { page: 'ChatAnalysisTask', label: 'Task' },
  { page: 'ChatDialog', label: '对话' },
]

function analysisHref(page, userName, modelName) {
  return `#/${page}?user_name=${encodeURIComponent(userName)}&model_name=${encodeURIComponent(modelName)}`
}

const emptyUserForm = { id: 0, user_name: '', password: '', phone: '', anthropic_enabled: true, openai_enabled: true }
const emptyModelForm = { id: 0, user_id: 0, user_name: '', model_name: '' }

export default function UserManage() {
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [expanded, setExpanded] = useState(null) // 展开模型的用户 id
  const [models, setModels] = useState([])
  const [modelsLoading, setModelsLoading] = useState(false)
  const [userForm, setUserForm] = useState(null)
  const [modelForm, setModelForm] = useState(null)
  const [saving, setSaving] = useState(false)

  const loadUsers = useCallback(() => {
    setLoading(true)
    setError('')
    post('UserManageInterface', { action: 'list' })
      .then((d) => setUsers((d && d.data) || []))
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [])

  useEffect(() => { loadUsers() }, [loadUsers])

  // 展开某用户的模型列表（UserModelManageInterface list）
  const loadModels = useCallback((userId) => {
    setModelsLoading(true)
    setModels([])
    post('UserModelManageInterface', { action: 'list', user_id: userId })
      .then((d) => setModels((d && d.data) || []))
      .catch((e) => setError(e.message))
      .finally(() => setModelsLoading(false))
  }, [])

  const toggleExpand = (userId) => {
    if (expanded === userId) { setExpanded(null); return }
    setExpanded(userId)
    loadModels(userId)
  }

  const saveUser = async () => {
    setSaving(true)
    try {
      await post('UserManageInterface', {
        action: userForm.id ? 'update' : 'add',
        id: userForm.id || 0,
        user_name: userForm.user_name,
        password: userForm.password,
        phone: userForm.phone,
        anthropic_enabled: !!userForm.anthropic_enabled,
        openai_enabled: !!userForm.openai_enabled,
      })
      setUserForm(null)
      loadUsers()
    } catch (e) { alert(e.message) } finally { setSaving(false) }
  }

  const toggleUserStatus = async (u) => {
    const status = u.status === 2 ? 1 : 2
    const label = status === 2 ? '禁用' : '启用'
    if (!confirm(`确定${label}该用户？已登录的用户将会被强制退出。`)) return
    try {
      await post('UserManageInterface', { action: 'update_status', id: u.id, status })
      loadUsers()
    } catch (e) { alert(e.message) }
  }

  const deleteUser = async (u) => {
    if (!confirm('确定删除该用户？其所有模型也会被删除。')) return
    try {
      await post('UserManageInterface', { action: 'delete', id: u.id })
      loadUsers()
    } catch (e) { alert(e.message) }
  }

  const saveModel = async () => {
    setSaving(true)
    try {
      await post('UserModelManageInterface', {
        action: modelForm.id ? 'update' : 'add',
        id: modelForm.id || 0,
        user_id: modelForm.user_id,
        model_name: modelForm.model_name,
      })
      setModelForm(null)
      if (expanded === modelForm.user_id) loadModels(modelForm.user_id)
    } catch (e) { alert(e.message) } finally { setSaving(false) }
  }

  const toggleModelStatus = async (m, userId) => {
    try {
      await post('UserModelManageInterface', { action: 'update_status', id: m.id, status: m.status === 2 ? 1 : 2 })
      loadModels(userId)
    } catch (e) { alert(e.message) }
  }

  const deleteModel = async (m, userId) => {
    if (!confirm('确定删除该模型？')) return
    try {
      await post('UserModelManageInterface', { action: 'delete', id: m.id })
      loadModels(userId)
    } catch (e) { alert(e.message) }
  }

  const columns = [
    { key: 'id', title: 'ID', width: 60 },
    { key: 'user_name', title: '用户名' },
    { key: 'phone', title: '手机号', render: (v) => v || '-' },
    {
      key: 'status', title: '用户状态',
      render: (v) => (
        <span>
          <span className={`status-dot ${v === 2 ? 'status-off' : 'status-on'}`} />
          {v === 2 ? '禁用' : '启用'}
        </span>
      ),
    },
    {
      key: 'anthropic_enabled', title: 'Anthropic',
      render: (v) => <span><span className={`status-dot ${v ? 'status-on' : 'status-off'}`} />{v ? '启用' : '禁用'}</span>,
    },
    {
      key: 'openai_enabled', title: 'OpenAI',
      render: (v) => <span><span className={`status-dot ${v ? 'status-on' : 'status-off'}`} />{v ? '启用' : '禁用'}</span>,
    },
    {
      key: 'actions', title: '操作', render: (_, u) => (
        <span>
          <button className="btn btn-sm" onClick={() => toggleUserStatus(u)}>{u.status === 2 ? '启用用户' : '禁用用户'}</button>{' '}
          <button className="btn btn-sm btn-primary" onClick={() => setUserForm({ ...u, password: '' })}>编辑</button>{' '}
          <button className="btn btn-sm btn-danger" onClick={() => deleteUser(u)}>删除</button>{' '}
          <button className="btn btn-link" onClick={() => toggleExpand(u.id)}>
            查看模型 {expanded === u.id ? '▲' : '▼'}
          </button>
        </span>
      ),
    },
  ]

  return (
    <div className="page">
      <h2 className="page-title">用户管理</h2>
      <div className="toolbar">
        <button className="btn" onClick={loadUsers}>刷新</button>
        <button className="btn btn-primary" onClick={() => setUserForm({ ...emptyUserForm })}>+ 添加用户</button>
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={users} loading={loading} empty="暂无用户" rowKey="id" />
        {expanded != null && users.find((u) => u.id === expanded) ? (
          <div style={{ marginTop: 12 }}>
            <div className="toolbar" style={{ marginBottom: 8 }}>
              <strong>模型列表（{users.find((u) => u.id === expanded).user_name}）</strong>
              <button
                className="btn btn-primary btn-sm"
                onClick={() => setModelForm({ ...emptyModelForm, user_id: expanded, user_name: users.find((u) => u.id === expanded).user_name })}
              >+ 添加模型</button>
            </div>
            <DataTable
              loading={modelsLoading}
              empty="暂无模型"
              rowKey="id"
              rows={models}
              columns={[
                { key: 'id', title: 'ID', width: 60 },
                { key: 'model_name', title: '模型名称' },
                { key: 'api_key', title: 'API Key', render: (v) => (v ? v.substring(0, 8) + '****' : '-') },
                { key: 'status', title: '状态', render: (v) => <span><span className={`status-dot ${v === 2 ? 'status-off' : 'status-on'}`} />{v === 2 ? '禁用' : '启用'}</span> },
                {
                  key: 'analysis', title: '分析',
                  render: (_, m) => {
                    const un = users.find((u) => u.id === expanded)?.user_name || ''
                    return (
                      <span>
                        {ANALYSIS_PAGES.map((p) => (
                          <a key={p.page} className="btn btn-link" style={{ padding: '2px 4px' }} href={analysisHref(p.page, un, m.model_name)}>{p.label}</a>
                        ))}
                      </span>
                    )
                  },
                },
                {
                  key: 'actions', title: '操作',
                  render: (_, m) => (
                    <span>
                      <button className="btn btn-sm" onClick={() => toggleModelStatus(m, expanded)}>{m.status === 2 ? '启用' : '禁用'}</button>{' '}
                      <button className="btn btn-sm btn-primary" onClick={() => setModelForm({ id: m.id, user_id: expanded, model_name: m.model_name })}>编辑</button>{' '}
                      <button className="btn btn-sm btn-danger" onClick={() => deleteModel(m, expanded)}>删除</button>
                    </span>
                  ),
                },
              ]}
            />
          </div>
        ) : null}
      </div>

      {userForm ? (
        <Modal
          title={userForm.id ? '编辑用户' : '添加用户'}
          width={480}
          onClose={() => setUserForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setUserForm(null)}>取消</button>
              <button className="btn btn-primary" disabled={saving} onClick={saveUser}>保存</button>
            </>
          }
        >
          <label className="field"><span>用户名</span>
            <input value={userForm.user_name} placeholder="3-50位" onChange={(e) => setUserForm({ ...userForm, user_name: e.target.value })} />
          </label>
          <label className="field"><span>密码</span>
            <input type="password" value={userForm.password} placeholder="至少6位，编辑时留空表示不修改" onChange={(e) => setUserForm({ ...userForm, password: e.target.value })} />
          </label>
          <label className="field"><span>手机号</span>
            <input value={userForm.phone} placeholder="7-20位数字" onChange={(e) => setUserForm({ ...userForm, phone: e.target.value })} />
          </label>
          <label className="field-check">
            <input type="checkbox" checked={!!userForm.anthropic_enabled} onChange={(e) => setUserForm({ ...userForm, anthropic_enabled: e.target.checked })} />
            启用 Anthropic 协议
          </label>
          <label className="field-check">
            <input type="checkbox" checked={!!userForm.openai_enabled} onChange={(e) => setUserForm({ ...userForm, openai_enabled: e.target.checked })} />
            启用 OpenAI 协议
          </label>
        </Modal>
      ) : null}

      {modelForm ? (
        <Modal
          title={modelForm.id ? '编辑模型' : '添加模型'}
          width={420}
          onClose={() => setModelForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setModelForm(null)}>取消</button>
              <button className="btn btn-primary" disabled={saving} onClick={saveModel}>保存</button>
            </>
          }
        >
          <label className="field"><span>所属用户</span><input value={modelForm.user_name} disabled /></label>
          <label className="field"><span>模型名称</span>
            <input value={modelForm.model_name} placeholder="8-64位" onChange={(e) => setModelForm({ ...modelForm, model_name: e.target.value })} />
          </label>
        </Modal>
      ) : null}
    </div>
  )
}
