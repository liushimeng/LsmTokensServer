import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { clearUserModelOptionsCache } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { useI18n } from '../i18n'

// 用户管理：UserManageInterface + UserModelManageInterface（均 POST JSON {action:...}）
// 分析页跳转：#/ChatAnalysis?user_name=..&model_name=.. 等（对照旧版 nav）

const ANALYSIS_PAGES = [
  { page: 'ChatAnalysis', label: 'chatAnalysis' },
  { page: 'ChatAnalysisTotal', label: 'chatAnalysisTotal' },
  { page: 'ChatAnalysisSession', label: 'chatAnalysisSession' },
  { page: 'ChatAnalysisTask', label: 'chatAnalysisTask' },
  { page: 'ChatDialog', label: 'chatDialog' },
]

function analysisHref(page, userName, modelName) {
  return `#/${page}?user_name=${encodeURIComponent(userName)}&model_name=${encodeURIComponent(modelName)}`
}

const emptyUserForm = { id: 0, user_name: '', password: '', phone: '', anthropic_enabled: true, openai_enabled: true }
const emptyModelForm = { id: 0, user_id: 0, user_name: '', model_name: '' }

export default function UserManage() {
  const { t } = useI18n()
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
      clearUserModelOptionsCache() // 用户增改后失效下拉选项缓存
      loadUsers()
    } catch (e) { alert(e.message) } finally { setSaving(false) }
  }

  const toggleUserStatus = async (u) => {
    const status = u.status === 2 ? 1 : 2
    const actionLabel = status === 2 ? t('userManage.disable') : t('userManage.enable')
    if (!confirm(t('userManage.toggleStatusConfirm', { action: actionLabel }))) return
    try {
      await post('UserManageInterface', { action: 'update_status', id: u.id, status })
      loadUsers()
    } catch (e) { alert(e.message) }
  }

  const deleteUser = async (u) => {
    if (!confirm(t('userManage.deleteUserConfirm'))) return
    try {
      await post('UserManageInterface', { action: 'delete', id: u.id })
      clearUserModelOptionsCache()
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
      clearUserModelOptionsCache() // 模型增改后失效下拉选项缓存
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
    if (!confirm(t('userManage.deleteModelConfirm'))) return
    try {
      await post('UserModelManageInterface', { action: 'delete', id: m.id })
      clearUserModelOptionsCache()
      loadModels(userId)
    } catch (e) { alert(e.message) }
  }

  const columns = [
    { key: 'id', title: t('userManage.id'), width: 60, sortable: true },
    { key: 'user_name', title: t('userManage.username'), sortable: true },
    { key: 'phone', title: t('userManage.phone'), render: (v) => v || '-' },
    {
      key: 'status', title: t('userManage.userStatus'), sortable: true,
      render: (v) => (
        <span>
          <span className={`status-dot ${v === 2 ? 'status-off' : 'status-on'}`} />
          {v === 2 ? t('userManage.disable') : t('userManage.enable')}
        </span>
      ),
    },
    {
      key: 'anthropic_enabled', title: t('chatAnalysis.anthropic'),
      render: (v) => <span><span className={`status-dot ${v ? 'status-on' : 'status-off'}`} />{v ? t('userManage.enable') : t('userManage.disable')}</span>,
    },
    {
      key: 'openai_enabled', title: t('chatAnalysis.openai'),
      render: (v) => <span><span className={`status-dot ${v ? 'status-on' : 'status-off'}`} />{v ? t('userManage.enable') : t('userManage.disable')}</span>,
    },
    {
      key: 'actions', title: t('common.action'), render: (_, u) => (
        <span>
          <button className="btn btn-sm" onClick={() => toggleUserStatus(u)}>{u.status === 2 ? t('userManage.enableUser') : t('userManage.disableUser')}</button>{' '}
          <button className="btn btn-sm btn-primary" onClick={() => setUserForm({ ...u, password: '' })}>{t('common.edit')}</button>{' '}
          <button className="btn btn-sm btn-danger" onClick={() => deleteUser(u)}>{t('common.delete')}</button>{' '}
          <button className="btn btn-link" onClick={() => toggleExpand(u.id)}>
            {t('userManage.viewModels')} {expanded === u.id ? '▲' : '▼'}
          </button>
        </span>
      ),
    },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('userManage.title')}</h2>
      <div className="toolbar">
        <button className="btn" onClick={loadUsers}>{t('common.refresh')}</button>
        <button className="btn btn-primary" onClick={() => setUserForm({ ...emptyUserForm })}>+ {t('userManage.addUser')}</button>
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={users} loading={loading} empty={t('userManage.noUsers')} rowKey="id"
          rowClass={(u) => (u.status === 2 ? 'row-disabled' : '')} />
        {expanded != null && users.find((u) => u.id === expanded) ? (
          <div style={{ marginTop: 12 }}>
            <div className="toolbar" style={{ marginBottom: 8 }}>
              <strong>{t('userManage.modelListWithUser', { userName: users.find((u) => u.id === expanded).user_name })}</strong>
              <button
                className="btn btn-primary btn-sm"
                onClick={() => setModelForm({ ...emptyModelForm, user_id: expanded, user_name: users.find((u) => u.id === expanded).user_name })}
              >+ {t('userManage.addModel')}</button>
            </div>
            <DataTable
              loading={modelsLoading}
              empty={t('userManage.noModels')}
              rowKey="id"
              rows={models}
              rowClass={(m) => (m.status === 2 ? 'row-disabled' : '')}
              columns={[
                { key: 'id', title: t('userManage.id'), width: 60, sortable: true },
                { key: 'model_name', title: t('userManage.modelName'), sortable: true },
                { key: 'api_key', title: t('userManage.apiKey'), render: (v) => (v ? v.substring(0, 8) + '****' : '-') },
                { key: 'status', title: t('common.status'), sortable: true, render: (v) => <span><span className={`status-dot ${v === 2 ? 'status-off' : 'status-on'}`} />{v === 2 ? t('userManage.disable') : t('userManage.enable')}</span> },
                {
                  key: 'analysis', title: t('userManage.analysis'),
                  render: (_, m) => {
                    const un = users.find((u) => u.id === expanded)?.user_name || ''
                    return (
                      <span>
                        {ANALYSIS_PAGES.map((p) => (
                          <a key={p.page} className="btn btn-link" style={{ padding: '2px 4px' }} href={analysisHref(p.page, un, m.model_name)}>{t(`nav.${p.label}`)}</a>
                        ))}
                      </span>
                    )
                  },
                },
                {
                  key: 'actions', title: t('common.action'),
                  render: (_, m) => (
                    <span>
                      <button className="btn btn-sm" onClick={() => toggleModelStatus(m, expanded)}>{m.status === 2 ? t('userManage.enable') : t('userManage.disable')}</button>{' '}
                      <button className="btn btn-sm btn-primary" onClick={() => setModelForm({ id: m.id, user_id: expanded, model_name: m.model_name })}>{t('common.edit')}</button>{' '}
                      <button className="btn btn-sm btn-danger" onClick={() => deleteModel(m, expanded)}>{t('common.delete')}</button>
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
          title={userForm.id ? t('userManage.editUser') : t('userManage.addUser')}
          width={480}
          onClose={() => setUserForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setUserForm(null)}>{t('common.cancel')}</button>
              <button className="btn btn-primary" disabled={saving} onClick={saveUser}>{t('common.save')}</button>
            </>
          }
        >
          <label className="field"><span>{t('userManage.username')}</span>
            <input value={userForm.user_name} placeholder={t('userManage.usernamePlaceholder')} onChange={(e) => setUserForm({ ...userForm, user_name: e.target.value })} />
          </label>
          <label className="field"><span>{t('userManage.password')}</span>
            <input type="password" value={userForm.password} placeholder={t('userManage.passwordPlaceholder')} onChange={(e) => setUserForm({ ...userForm, password: e.target.value })} />
          </label>
          <label className="field"><span>{t('userManage.phone')}</span>
            <input value={userForm.phone} placeholder={t('userManage.phonePlaceholder')} onChange={(e) => setUserForm({ ...userForm, phone: e.target.value })} />
          </label>
          <label className="field-check">
            <input type="checkbox" checked={!!userForm.anthropic_enabled} onChange={(e) => setUserForm({ ...userForm, anthropic_enabled: e.target.checked })} />
            {t('userManage.enableAnthropicProtocol')}
          </label>
          <label className="field-check">
            <input type="checkbox" checked={!!userForm.openai_enabled} onChange={(e) => setUserForm({ ...userForm, openai_enabled: e.target.checked })} />
            {t('userManage.enableOpenaiProtocol')}
          </label>
        </Modal>
      ) : null}

      {modelForm ? (
        <Modal
          title={modelForm.id ? t('userManage.editModel') : t('userManage.addModel')}
          width={420}
          onClose={() => setModelForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setModelForm(null)}>{t('common.cancel')}</button>
              <button className="btn btn-primary" disabled={saving} onClick={saveModel}>{t('common.save')}</button>
            </>
          }
        >
          <label className="field"><span>{t('userManage.belongingUser')}</span><input value={modelForm.user_name} disabled /></label>
          <label className="field"><span>{t('userManage.modelName')}</span>
            <input value={modelForm.model_name} placeholder={t('userManage.modelNamePlaceholder')} onChange={(e) => setModelForm({ ...modelForm, model_name: e.target.value })} />
          </label>
        </Modal>
      ) : null}
    </div>
  )
}
