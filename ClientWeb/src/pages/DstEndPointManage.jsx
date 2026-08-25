import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'

// 源站管理（管理端）：DstEndPointManageInterface（POST JSON {action:...}）
// action: list / add / update / toggle_status / delete / batch_enable / batch_disable / batch_delete / test / list_platforms / list_models
// 用户端（29001）同名接口仅支持 list / test（只读 + 连通性测试），增删改按钮不展示。

const emptyForm = {
  id: 0, user_id: 0, platform_name: '', model_name: '',
  protocol_type: 1, auth_type: 0, url_address: '', api_key: '',
}

// 按协议+认证方式拼出保存时实际发出的 Request Header（纯前端预览）
function headerPreview(protocolType, authType, apiKey, isEdit) {
  const proto = parseInt(protocolType, 10) || 1
  const auth = parseInt(authType, 10) || 0
  if (!apiKey.trim()) return isEdit ? '（保持原值不变）' : '（请填写 API Key）'
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
    return u ? u.user_name : '用户 ' + uid
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
    if (!confirm(`确认${status === 1 ? '启用' : '禁用'}以下源站？\n\n${ep.platform_name} / ${ep.model_name}`)) return
    try {
      await post('DstEndPointManageInterface', { action: 'toggle_status', id: ep.id, status })
      loadData()
    } catch (e) { alert(e.message) }
  }

  const deleteItem = async (ep) => {
    if (!confirm(`确认删除以下源站？\n\n${ep.platform_name} / ${ep.model_name}\n\n将同步清理所有智能路由中对该源站的引用（仅剩该源站的路由会被级联删除），此操作不可恢复！`)) return
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
      setTestResult({ success: false, message: '请求异常: ' + e.message, data: {} })
    }
  }

  const runBatch = async (actionName) => {
    const ids = [...selected]
    if (ids.length === 0) { alert('请先选择要操作的源站'); return }
    if (ids.length > 500) { alert('单次最多操作 500 条，当前已选 ' + ids.length + ' 条'); return }
    const label = { batch_enable: '启用', batch_disable: '禁用', batch_delete: '删除' }[actionName]
    if (!confirm(`确认${label}选中的 ${ids.length} 条源站？` + (actionName === 'batch_delete' ? '此操作不可恢复！' : ''))) return
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
        <input type="checkbox" title="全选"
          checked={endpoints.length > 0 && selected.size >= endpoints.length}
          onChange={(e) => setSelected(e.target.checked ? new Set(endpoints.map((x) => x.id)) : new Set())} />
      ), width: 36,
      render: (_, ep) => <input type="checkbox" checked={selected.has(ep.id)} onChange={() => toggleSelect(ep.id)} />,
    }] : []),
    { key: 'id', title: 'ID', width: 60 },
    ...(isAdmin ? [{ key: 'user_id', title: '所属用户', render: (v) => userName(v) }] : []),
    { key: 'platform_name', title: '平台' },
    { key: 'model_name', title: '模型' },
    { key: 'protocol_type', title: '协议', render: (v) => (v == 1 ? 'Anthropic' : 'OpenAI') },
    { key: 'url_address', title: 'URL', render: (v) => <span style={{ wordBreak: 'break-all', whiteSpace: 'normal' }}>{v}</span> },
    { key: 'status', title: '状态', render: (v) => <span><span className={`status-dot ${v == 1 ? 'status-on' : 'status-off'}`} />{v == 1 ? '启用' : '禁用'}</span> },
    {
      key: 'actions', title: '操作',
      render: (_, ep) => (
        <span>
          {isAdmin ? <button className="btn btn-sm" onClick={() => toggleStatus(ep)}>{ep.status == 1 ? '禁用' : '启用'}</button> : null}{' '}
          {isAdmin ? <button className="btn btn-sm btn-primary" onClick={() => setForm({ ...emptyForm, ...ep, api_key: '' })}>编辑</button> : null}{' '}
          <button className="btn btn-sm" onClick={() => testItem(ep)}>测试</button>
          {isAdmin ? <>{' '}<button className="btn btn-sm btn-danger" onClick={() => deleteItem(ep)}>删除</button></> : null}
        </span>
      ),
    },
  ]

  return (
    <div className="page">
      <h2 className="page-title">源站管理</h2>
      <div className="toolbar">
        <button className="btn" onClick={loadData}>刷新</button>
        {isAdmin ? <button className="btn btn-primary" onClick={() => setForm({ ...emptyForm, user_id: users[0]?.id || 0 })}>+ 添加源站</button>
          : <span style={{ color: '#888', fontSize: 13 }}>用户模式：只读列表 + 连通性测试</span>}
        {selected.size > 0 ? (
          <>
            <span>已选择 {selected.size} 条（单次最多 500 条）</span>
            <button className="btn btn-sm" onClick={() => runBatch('batch_enable')}>批量启用</button>
            <button className="btn btn-sm" onClick={() => runBatch('batch_disable')}>批量禁用</button>
            <button className="btn btn-sm btn-danger" onClick={() => runBatch('batch_delete')}>批量删除</button>
            <button className="btn btn-sm" onClick={() => setSelected(new Set())}>取消选择</button>
          </>
        ) : null}
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={endpoints} loading={loading} empty="暂无源站" rowKey="id" />
      </div>

      {form ? (
        <Modal
          title={form.id ? '编辑源站' : '添加源站'}
          onClose={() => setForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setForm(null)}>取消</button>
              <button className="btn btn-primary" disabled={saving} onClick={save}>保存</button>
            </>
          }
        >
          {formError ? <div className="alert alert-error">{formError}</div> : null}
          <label className="field"><span>所属用户</span>
            <select value={form.user_id} onChange={(e) => setForm({ ...form, user_id: parseInt(e.target.value, 10) })}>
              {users.map((u) => <option key={u.id} value={u.id}>{u.user_name}</option>)}
            </select>
          </label>
          <label className="field"><span>平台名称</span>
            <input value={form.platform_name} placeholder="如: Anthropic, OpenAI" onChange={(e) => setForm({ ...form, platform_name: e.target.value })} />
          </label>
          <label className="field"><span>模型名</span>
            <input value={form.model_name} placeholder="如: claude-3-5-sonnet" onChange={(e) => setForm({ ...form, model_name: e.target.value })} />
          </label>
          <label className="field"><span>协议类型</span>
            <select value={form.protocol_type} onChange={(e) => setForm({ ...form, protocol_type: parseInt(e.target.value, 10) })}>
              <option value={1}>Anthropic</option>
              <option value={2}>OpenAI</option>
            </select>
          </label>
          <label className="field"><span>认证方式</span>
            <select value={form.auth_type} onChange={(e) => setForm({ ...form, auth_type: parseInt(e.target.value, 10) })}>
              <option value={0}>协议默认（Anthropic→X-Api-Key，OpenAI→Authorization Bearer）</option>
              <option value={1}>强制 X-Api-Key</option>
              <option value={2}>强制 Authorization Bearer</option>
            </select>
          </label>
          <label className="field"><span>URL 地址</span>
            <input value={form.url_address} placeholder="https://api.xxx.com/v1" onChange={(e) => setForm({ ...form, url_address: e.target.value })} />
          </label>
          <label className="field"><span>API Key</span>
            <input type={form.id ? 'password' : 'text'} value={form.api_key}
              placeholder={form.id ? '留空则保持原值不变' : '源站 API Key'}
              onChange={(e) => setForm({ ...form, api_key: e.target.value })} />
          </label>
          <div className="field"><span>Request Header（保存时实际发出）</span>
            <pre style={{ fontSize: 12, background: '#1e1e1e', color: '#d4d4d4', padding: 10, borderRadius: 4, whiteSpace: 'pre-wrap', margin: 0 }}>
              {headerPreview(form.protocol_type, form.auth_type, form.api_key, !!form.id)}
            </pre>
          </div>
        </Modal>
      ) : null}

      {testResult ? (
        <Modal title="源站连通性测试" width={720} onClose={() => setTestResult(null)}
          footer={<button className="btn" onClick={() => setTestResult(null)}>关闭</button>}>
          {testResult.loading ? <div className="table-loading">测试中，请稍候…</div> : (
            <div>
              <div className={'alert ' + (testResult.success ? 'alert-ok' : 'alert-error')} style={{ fontWeight: 600 }}>
                {testResult.success ? '测试成功' : '测试失败'}
                {testResult.data?.status_code ? ' | HTTP ' + testResult.data.status_code : ''}
                {testResult.data?.elapsed_ms ? ' | 耗时 ' + testResult.data.elapsed_ms + 'ms' : ''}
                {testResult.message && testResult.message !== '测试成功' ? ' | ' + testResult.message : ''}
              </div>
              <div className="field"><span>请求 URL</span>
                <div style={{ fontFamily: 'monospace', fontSize: 12, wordBreak: 'break-all', background: '#f8f9fa', padding: 8, borderRadius: 4 }}>{testResult.data.request_url}</div>
              </div>
              <div className="field"><span>请求头</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4 }}>{testResult.data.request_headers}</pre>
              </div>
              <div className="field"><span>请求体</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4 }}>{formatJSON(testResult.data.request_body)}</pre>
              </div>
              <div className="field"><span>响应头</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4, maxHeight: 160, overflow: 'auto' }}>{testResult.data.response_headers}</pre>
              </div>
              <div className="field"><span>响应体</span>
                <pre style={{ fontSize: 12, whiteSpace: 'pre-wrap', margin: 0, background: '#f8f9fa', padding: 8, borderRadius: 4, maxHeight: 160, overflow: 'auto' }}>{formatJSON(testResult.data.response_body)}</pre>
              </div>
            </div>
          )}
        </Modal>
      ) : null}
    </div>
  )
}
