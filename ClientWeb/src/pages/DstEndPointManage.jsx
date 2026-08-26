import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { useI18n } from '../i18n'

// 源站管理（管理端）：DstEndPointManageInterface（POST JSON {action:...}）
// action: list / add / update / toggle_status / delete / batch_enable / batch_disable / batch_delete / test / list_platforms / list_models
// 用户端（29001）同名接口仅支持 list / test（只读 + 连通性测试），增删改按钮不展示。

const emptyForm = {
  id: 0, user_id: 0, platform_name: '', model_name: '',
  protocol_type: 1, auth_type: 0, url_address: '', api_key: '',
}

// 按协议+认证方式拼出保存时实际发出的 Request Header（纯前端预览）
function headerPreview(protocolType, authType, apiKey, isEdit, t) {
  const proto = parseInt(protocolType, 10) || 1
  const auth = parseInt(authType, 10) || 0
  if (!apiKey.trim()) return isEdit ? t('dstEndPoint.headerKeepUnchanged') : t('dstEndPoint.headerEnterKey')
  let authLine
  if (auth === 1) authLine = 'X-Api-Key: **API-KEY**'
  else if (auth === 2) authLine = 'Authorization: Bearer **API-KEY**'
  else authLine = proto === 1 ? 'X-Api-Key: **API-KEY**' : 'Authorization: Bearer **API-KEY**'
  return proto === 1
    ? ['Anthropic-Version: 2023-06-01', 'Content-Type: application/json', authLine].join('\n')
    : ['Content-Type: application/json', authLine].join('\n')
}

function formatJSON(s) {
  if (!s) return ''
  try { return JSON.stringify(JSON.parse(s), null, 2) } catch { return s }
}

export default function DstEndPointManage() {
  const { t } = useI18n()
  const isAdmin = __APP_ROLE__ === 'manager' ? isAdminRole() : false // 用户端：只读列表 + 连通性测试（构建期裁剪管理分支）
  const [users, setUsers] = useState([])
  const [endpoints, setEndpoints] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [form, setForm] = useState(null)
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const [selected, setSelected] = useState(new Set())
  const [testResult, setTestResult] = useState(null) // {success,message,data}

  const loadData = useCallback(() => {
    setLoading(true)
    setError('')
    Promise.all([
      // 用户下拉仅管理端 mux 提供；用户端 Promise.all 会整体失败导致列表不渲染，须跳过
      isAdmin ? post('UserManageInterface', { action: 'list' }) : Promise.resolve(null),
      post('DstEndPointManageInterface', { action: 'list' }),
    ])
      .then(([u, e]) => {
        setUsers((u && u.data) || [])
        setEndpoints((e && e.data) || [])
      })
      .catch((e2) => setError(e2.message))
      .finally(() => setLoading(false))
  }, [isAdmin])

  useEffect(() => { loadData() }, [loadData])

  const userName = (uid) => {
    const u = users.find((x) => x.id == uid) // eslint-disable-line eqeqeq
    return u ? u.user_name : t('dstEndPoint.userName', { id: uid })
  }

  const save = async () => {
    setSaving(true)
    setFormError('')
    const body = {
      action: form.id ? 'update' : 'add',
      id: form.id || 0,
      user_id: parseInt(form.user_id, 10) || 0,
      platform_name: form.platform_name,
      model_name: form.model_name,
      protocol_type: parseInt(form.protocol_type, 10) || 1,
      auth_type: parseInt(form.auth_type, 10) || 0,
      url_address: form.url_address,
    }
    if (!form.id || form.api_key) body.api_key = form.api_key
    try {
      await post('DstEndPointManageInterface', body)
      setForm(null)
      loadData()
    } catch (e) {
      // 保存失败且带连通性详情（data 为对象）时弹测试结果窗，否则表单内联报错
      if (e && e.data && typeof e.data === 'object') setTestResult({ success: false, message: e.message, data: e.data })
      else setFormError(e.message)
    } finally { setSaving(false) }
  }

  const toggleStatus = async (ep) => {
    const status = ep.status == 1 ? 0 : 1 // eslint-disable-line eqeqeq
    if (!confirm(t('dstEndPoint.confirmToggle', { action: status === 1 ? t('dstEndPoint.enableAction') : t('dstEndPoint.disableAction'), platform: ep.platform_name, model: ep.model_name }))) return
    try {
      await post('DstEndPointManageInterface', { action: 'toggle_status', id: ep.id, status })
      loadData()
    } catch (e) { alert(e.message) }
  }

  const deleteItem = async (ep) => {
    if (!confirm(t('dstEndPoint.confirmDeleteEndpoint', { platform: ep.platform_name, model: ep.model_name }))) return
    try {
      await post('DstEndPointManageInterface', { action: 'delete', id: ep.id })
      loadData()
    } catch (e) { alert(e.message) }
  }

  const testItem = async (ep) => {
    setTestResult({ loading: true })
    try {
      const d = await post('DstEndPointManageInterface', { action: 'test', id: ep.id })
      setTestResult({ success: d.success, message: d.message, data: d.data || {} })
    } catch (e) {
      setTestResult({ success: false, message: t('dstEndPoint.requestError') + e.message, data: {} })
    }
  }

  const runBatch = async (actionName) => {
    const ids = [...selected]
    if (ids.length === 0) { alert(t('dstEndPoint.selectEndpoints')); return }
    if (ids.length > 500) { alert(t('dstEndPoint.max500', { count: ids.length })); return }
    const label = { batch_enable: t('dstEndPoint.batchEnable'), batch_disable: t('dstEndPoint.batchDisable'), batch_delete: t('dstEndPoint.batchDelete') }[actionName]
    const batchConfirmKey = { batch_enable: 'confirmBatchEnable', batch_disable: 'confirmBatchDisable', batch_delete: 'confirmBatchDelete' }[actionName]
    if (!confirm(t(`dstEndPoint.${batchConfirmKey}`, { count: ids.length }))) return
    try {
      await post('DstEndPointManageInterface', { action: actionName, ids })
      setSelected(new Set())
      loadData()
    } catch (e) { alert(e.message) }
  }

  const toggleSelect = (id) => {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id); else next.add(id)
    setSelected(next)
  }

  const columns = [
    ...(isAdmin ? [{
      key: 'checkbox', title: (
        <input type="checkbox" title={t('dstEndPoint.selectAll')}
          checked={endpoints.length > 0 && selected.size >= endpoints.length}
          onChange={(e) => setSelected(e.target.checked ? new Set(endpoints.map((x) => x.id)) : new Set())} />
      ), width: 36,
      render: (_, ep) => <input type="checkbox" checked={selected.has(ep.id)} onChange={() => toggleSelect(ep.id)} />,
    }] : []),
    { key: 'id', title: t('dstEndPoint.id'), width: 60, sortable: true },
    ...(isAdmin ? [{ key: 'user_id', title: t('dstEndPoint.userLabel'), sortable: true, sortValue: (r) => userName(r.user_id), render: (v) => userName(v) }] : []),
    { key: 'platform_name', title: t('dstEndPoint.platformLabel'), sortable: true },
    { key: 'model_name', title: t('dstEndPoint.modelLabel'), sortable: true },
    { key: 'protocol_type', title: t('dstEndPoint.protocolLabel'), sortable: true, render: (v) => (v == 1 ? t('dstEndPoint.anthropic') : t('dstEndPoint.openai')) },
    { key: 'url_address', title: t('dstEndPoint.urlLabel'), sortable: true, render: (v) => <span style={{ wordBreak: 'break-all', whiteSpace: 'normal' }}>{v}</span> },
    { key: 'status', title: t('dstEndPoint.statusLabel'), sortable: true, render: (v) => <span><span className={`status-dot ${v == 1 ? 'status-on' : 'status-off'}`} />{v == 1 ? t('dstEndPoint.enableAction') : t('dstEndPoint.disableAction')}</span> },
    {
      key: 'actions', title: t('common.action'),
      render: (_, ep) => (
        <span>
          {isAdmin ? <button className="btn btn-sm" onClick={() => toggleStatus(ep)}>{ep.status == 1 ? t('dstEndPoint.disableAction') : t('dstEndPoint.enableAction')}</button> : null}{' '}
          {isAdmin ? <button className="btn btn-sm btn-primary" onClick={() => setForm({ ...emptyForm, ...ep, api_key: '' })}>{t('common.edit')}</button> : null}{' '}
          <button className="btn btn-sm" onClick={() => testItem(ep)}>{t('dstEndPoint.testConnection')}</button>
          {isAdmin ? <>{' '}<button className="btn btn-sm btn-danger" onClick={() => deleteItem(ep)}>{t('common.delete')}</button></> : null}
        </span>
      ),
    },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('dstEndPoint.title')}</h2>
      <div className="toolbar">
        <button className="btn" onClick={loadData}>{t('common.refresh')}</button>
        {isAdmin ? <button className="btn btn-primary" onClick={() => setForm({ ...emptyForm, user_id: users[0]?.id || 0 })}>+ {t('dstEndPoint.addEndPoint')}</button>
          : <span style={{ color: '#888', fontSize: 13 }}>{t('dstEndPoint.userMode')}</span>}
        {selected.size > 0 ? (
          <>
            <span>{t('dstEndPoint.selectedCountMax', { count: selected.size })}</span>
            <button className="btn btn-sm" onClick={() => runBatch('batch_enable')}>{t('dstEndPoint.batchEnable')}</button>
            <button className="btn btn-sm" onClick={() => runBatch('batch_disable')}>{t('dstEndPoint.batchDisable')}</button>
            <button className="btn btn-sm btn-danger" onClick={() => runBatch('batch_delete')}>{t('dstEndPoint.batchDelete')}</button>
            <button className="btn btn-sm" onClick={() => setSelected(new Set())}>{t('dstEndPoint.cancelSelection')}</button>
          </>
        ) : null}
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={endpoints} loading={loading} empty={t('dstEndPoint.noData')} rowKey="id"
          rowClass={(ep) => (ep.status == 1 ? 'row-enabled' : 'row-disabled')} />
      </div>

      {form ? (
        <Modal
          title={form.id ? t('dstEndPoint.editEndPoint') : t('dstEndPoint.addEndPoint')}
          onClose={() => setForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setForm(null)}>{t('common.cancel')}</button>
              <button className="btn btn-primary" disabled={saving} onClick={save}>{t('common.save')}</button>
            </>
          }
        >
          {formError ? <div className="alert alert-error">{formError}</div> : null}
          <label className="field"><span>{t('dstEndPoint.userLabel')}</span>
            <select value={form.user_id} onChange={(e) => setForm({ ...form, user_id: parseInt(e.target.value, 10) })}>
              {users.map((u) => <option key={u.id} value={u.id}>{u.user_name}</option>)}
            </select>
          </label>
          <label className="field"><span>{t('dstEndPoint.platformNameLabel')}</span>
            <input value={form.platform_name} placeholder={t('dstEndPoint.platformNamePlaceholder')} onChange={(e) => setForm({ ...form, platform_name: e.target.value })} />
          </label>
          <label className="field"><span>{t('dstEndPoint.modelLabel')}</span>
            <input value={form.model_name} placeholder={t('dstEndPoint.modelNamePlaceholder')} onChange={(e) => setForm({ ...form, model_name: e.target.value })} />
          </label>
          <label className="field"><span>{t('dstEndPoint.protocolType')}</span>
            <select value={form.protocol_type} onChange={(e) => setForm({ ...form, protocol_type: parseInt(e.target.value, 10) })}>
              <option value={1}>{t('dstEndPoint.anthropic')}</option>
              <option value={2}>{t('dstEndPoint.openai')}</option>
            </select>
          </label>
          <label className="field"><span>{t('dstEndPoint.authType')}</span>
            <select value={form.auth_type} onChange={(e) => setForm({ ...form, auth_type: parseInt(e.target.value, 10) })}>
              <option value={0}>{t('dstEndPoint.authDefault')}</option>
              <option value={1}>{t('dstEndPoint.authForceXApiKey')}</option>
              <option value={2}>{t('dstEndPoint.authForceBearer')}</option>
            </select>
          </label>
          <label className="field"><span>{t('dstEndPoint.urlAddress')}</span>
            <input value={form.url_address} placeholder={t('dstEndPoint.urlPlaceholder')} onChange={(e) => setForm({ ...form, url_address: e.target.value })} />
          </label>
          <label className="field"><span>{t('dstEndPoint.apiKey')}</span>
            <input type={form.id ? 'password' : 'text'} value={form.api_key}
              placeholder={form.id ? t('dstEndPoint.apiKeyKeepUnchanged') : t('dstEndPoint.apiKeyPlaceholder')}
              onChange={(e) => setForm({ ...form, api_key: e.target.value })} />
          </label>
          <div className="field"><span>{t('dstEndPoint.headerPreview')}</span>
            <pre style={{ fontSize: 12, background: '#1e1e1e', color: '#d4d4d4', padding: 10, borderRadius: 4, whiteSpace: 'pre-wrap', margin: 0 }}>
              {headerPreview(form.protocol_type, form.auth_type, form.api_key, !!form.id, t)}
            </pre>
          </div>
        </Modal>
      ) : null}

      {testResult ? (
        <Modal title={t('dstEndPoint.connectivityTest')} width={720} onClose={() => setTestResult(null)}
          footer={<button className="btn" onClick={() => setTestResult(null)}>{t('common.close')}</button>}>
          {testResult.loading ? <div className="table-loading">{t('dstEndPoint.testing')}</div> : (
            <div>
              <div className={'alert ' + (testResult.success ? 'alert-ok' : 'alert-error')} style={{ fontWeight: 600 }}>
                {testResult.success ? t('dstEndPoint.testSuccess') : t('dstEndPoint.testFailed')}
                {testResult.data?.status_code ? t('dstEndPoint.httpStatus') + testResult.data.status_code : ''}
                {testResult.data?.elapsed_ms ? t('dstEndPoint.elapsed', { ms: testResult.data.elapsed_ms }) : ''}
                {testResult.message && testResult.message !== t('dstEndPoint.testSuccess') ? ' | ' + testResult.message : ''}
              </div>
              <div className="field"><span>{t('dstEndPoint.requestUrl')}</span>
                <div style={{ fontFamily: 'monospace', fontSize: 12, wordBreak: 'break-all', background: '#f8f9fa', padding: 8, borderRadius: 4 }}>{testResult.data.request_url}</div>
              </div>
              <div className="field"><span>{t('dstEndPoint.requestHeaders')}</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4 }}>{testResult.data.request_headers}</pre>
              </div>
              <div className="field"><span>{t('dstEndPoint.requestBody')}</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4 }}>{formatJSON(testResult.data.request_body)}</pre>
              </div>
              <div className="field"><span>{t('dstEndPoint.responseHeaders')}</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4, maxHeight: 160, overflow: 'auto' }}>{testResult.data.response_headers}</pre>
              </div>
              <div className="field"><span>{t('dstEndPoint.responseBody')}</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4, maxHeight: 160, overflow: 'auto' }}>{formatJSON(testResult.data.response_body)}</pre>
              </div>
            </div>
          )}
        </Modal>
      ) : null}
    </div>
  )
}
