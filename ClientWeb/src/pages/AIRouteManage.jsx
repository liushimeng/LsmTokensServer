import { useCallback, useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'

// 智能路由管理（管理端）：AIRouteManageInterface（POST JSON {action:...}）
// action: list / list_models / list_endpoints / add / update / delete / batch_delete / batch_update / batch_stats
// 用户端（29001）：UserAIRouteInterface（action: list / list_endpoints / count_record_by_protocol / update），
// 仅限本人路由的受限编辑，无新增/删除/批量。

const ALGO_NAMES = { 1: '指定型', 2: '稳定型', 3: '经济型' }
const ALGO_DESC = {
  1: '始终使用目标源站列表中的第一个。您可以通过调整列表顺序来控制优先级。',
  2: '遇到服务端错误（429/500/502/503/504）或连接超时，自动切换到下一个源站。',
  3: 'Session 级别负载均衡：根据 session_id 将不同会话轮询分配到各源站。仅支持 Anthropic 协议，OpenAI 协议自动降级为稳定型。',
}
const DAYS_OPTIONS = [-1, -2, -4, -6, -12, 1, 3, 5, 7, 14, 30, 60, 90, 0]
const daysLabel = (d) => (d < 0 ? '最近' + -d + '小时' : d === 0 ? '全部时间' : '最近' + d + '天')

const protocolName = (t) => (parseInt(t, 10) === 1 ? 'Anthropic' : 'OpenAI')
// 源站算法：1=协议直连（同协议）2=协议转换器（异协议）
const epAlgoForProtocol = (ep, routeProtocol) => (parseInt(ep.protocol_type, 10) === parseInt(routeProtocol, 10) ? 1 : 2)
const epAlgoValid = (ep, routeProtocol, algo) => {
  if (!ep || !routeProtocol) return true
  const same = parseInt(ep.protocol_type, 10) === parseInt(routeProtocol, 10)
  return algo === 1 ? same : !same
}

function emptyForm() {
  return {
    id: 0, user_id: '', user_model_id: '', protocol_type: '',
    algorithm_strategy_type: 1,
    endpoints: [], // [{id, algorithm_type, in_route_status}]
  }
}

export default function AIRouteManage() {
  const isAdmin = __APP_ROLE__ === 'manager' ? isAdminRole() : false // 用户端走 UserAIRouteInterface（本人路由 + 受限编辑，构建期裁剪管理分支）
  const [routes, setRoutes] = useState([])
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [selected, setSelected] = useState(new Set())
  const [form, setForm] = useState(null) // 编辑/新增表单
  const [formModels, setFormModels] = useState([])
  const [formEndpoints, setFormEndpoints] = useState([])
  const [userRoutes, setUserRoutes] = useState([])
  const [saving, setSaving] = useState(false)
  const [formError, setFormError] = useState('')
  const [days, setDays] = useState(3)
  const [stats, setStats] = useState({}) // route_id -> {anthropic_count, openai_count}

  const loadRoutes = useCallback(() => {
    setLoading(true)
    setError('')
    // 管理端：AIRouteManageInterface 全量；用户端：UserAIRouteInterface 本人路由
    post(isAdmin ? 'AIRouteManageInterface' : 'UserAIRouteInterface', { action: 'list' })
      .then((d) => { setRoutes((d && d.data) || []) })
      .catch((e) => setError(e.message))
      .finally(() => setLoading(false))
  }, [isAdmin])

  useEffect(() => { loadRoutes() }, [loadRoutes])

  // 用户下拉数据（UserManageInterface list，仅管理端 mux 存在）
  useEffect(() => {
    if (!isAdmin) return
    post('UserManageInterface', { action: 'list' })
      .then((d) => setUsers((d && d.data) || []))
      .catch(() => {})
  }, [isAdmin])

  // 时间跨度统计：管理端 batch_stats 批量聚合；用户端 count_record_by_protocol 按模型逐条
  useEffect(() => {
    if (!routes.length) { setStats({}); return }
    if (isAdmin) {
      const items = routes
        .filter((r) => r.user_name && r.model_name)
        .map((r) => ({
          route_id: r.id,
          protocol_type: r.protocol_type || 0,
          days,
          key: { user_name: r.user_name, model_name: r.model_name, protocol_type: r.protocol_type || 0 },
        }))
      if (!items.length) return
      post('AIRouteManageInterface', { action: 'batch_stats', batch_items: items })
        .then((d) => setStats((d && d.data) || {}))
        .catch(() => setStats({}))
    } else {
      // 用户端按 (model_name, days) 查询，同模型两条路由共享同一条统计
      const seen = new Set()
      routes.forEach((r) => {
        if (!r.model_name || seen.has(r.model_name)) return
        seen.add(r.model_name)
        post('UserAIRouteInterface', { action: 'count_record_by_protocol', model_name: r.model_name, days })
          .then((d) => {
            const s = (d && d.data) || {}
            setStats((prev) => ({ ...prev, [r.id]: { anthropic_count: s.anthropic || 0, openai_count: s.openai || 0 } }))
          })
          .catch(() => {})
      })
    }
  }, [routes, days, isAdmin])

  // 选择用户后联动加载：模型列表 / 源站列表 / 该用户已有路由（用于协议占用判断）
  const onUserChange = async (userId) => {
    setForm((f) => ({ ...f, user_id: userId, user_model_id: '', protocol_type: '', endpoints: [] }))
    if (!userId) { setFormModels([]); setFormEndpoints([]); setUserRoutes([]); return }
    try {
      const [m, e, r] = await Promise.all([
        post('AIRouteManageInterface', { action: 'list_models', user_id: parseInt(userId, 10) }),
        post('AIRouteManageInterface', { action: 'list_endpoints', user_id: parseInt(userId, 10) }),
        post('AIRouteManageInterface', { action: 'list', user_id: parseInt(userId, 10) }),
      ])
      setFormModels((m && m.data) || [])
      setFormEndpoints((e && e.data) || [])
      setUserRoutes((r && r.data) || [])
    } catch (err) { setError(err.message) }
  }

  // 某模型下可选协议（已配满则只保留编辑中的协议）
  const protocolOptions = () => {
    if (!form.user_model_id) return []
    const rs = userRoutes.filter((r) => r.user_model_id == form.user_model_id) // eslint-disable-line eqeqeq
    const hasA = rs.some((r) => r.protocol_type == 1) // eslint-disable-line eqeqeq
    const hasO = rs.some((r) => r.protocol_type == 2) // eslint-disable-line eqeqeq
    const editing = form.id ? routes.find((r) => r.id === form.id) : null
    const opts = []
    if (!hasA || (editing && editing.protocol_type == 1)) opts.push(1) // eslint-disable-line eqeqeq
    if (!hasO || (editing && editing.protocol_type == 2)) opts.push(2) // eslint-disable-line eqeqeq
    return opts
  }

  const openAdd = () => {
    setFormModels([]); setFormEndpoints([]); setUserRoutes([])
    setFormError('')
    setForm(emptyForm())
  }

  const openEdit = async (route) => {
    setFormError('')
    const ids = (route.dst_endpoint_id_list || '').split(',').map((s) => parseInt(s.trim(), 10)).filter((n) => !isNaN(n))
    const algos = (route.dst_endpoint_algorithm_type_list || '').split(',').map((s) => parseInt(s.trim(), 10)).filter((n) => n === 1 || n === 2)
    const statusLookup = {}
    ;(route.endpoint_list || []).forEach((e) => { statusLookup[e.id] = e.in_route_status })
    if (isAdmin) {
      await onUserChange(route.user_id)
    } else {
      // 用户端：源站下拉走 UserAIRouteInterface list_endpoints（本人源站）
      try {
        const e = await post('UserAIRouteInterface', { action: 'list_endpoints' })
        setFormEndpoints((e && e.data) || [])
      } catch (err) { setError(err.message); return }
    }
    setForm({
      id: route.id,
      user_id: route.user_id,
      user_model_id: route.user_model_id,
      protocol_type: route.protocol_type,
      algorithm_strategy_type: route.algorithm_strategy_type || 1,
      endpoints: ids.map((id, i) => ({
        id,
        algorithm_type: algos[i] || 1,
        in_route_status: statusLookup[id] === 0 ? 0 : 1,
      })),
    })
  }

  const addEndpoint = (epId) => {
    if (!epId) { alert('请先选择一个源站'); return }
    if (form.endpoints.some((x) => x.id === epId)) { alert('该源站已添加'); return }
    const ep = formEndpoints.find((e) => e.id == epId) // eslint-disable-line eqeqeq
    setForm({ ...form, endpoints: [...form.endpoints, { id: epId, algorithm_type: epAlgoForProtocol(ep, form.protocol_type), in_route_status: 1 }] })
  }
  const removeEndpoint = (epId) => setForm({ ...form, endpoints: form.endpoints.filter((x) => x.id !== epId) })
  const moveEndpoint = (idx, dir) => {
    const next = form.endpoints.slice()
    const j = idx + dir
    if (j < 0 || j >= next.length) return
    ;[next[idx], next[j]] = [next[j], next[idx]]
    setForm({ ...form, endpoints: next })
  }
  const setEpAlgo = (idx, algo) => {
    const ep = formEndpoints.find((e) => e.id == form.endpoints[idx].id) // eslint-disable-line eqeqeq
    if (!epAlgoValid(ep, form.protocol_type, algo)) {
      alert(algo === 1 ? '协议直连要求源站协议与路由协议一致' : '协议转换器要求源站协议与路由协议相反')
      return
    }
    const next = form.endpoints.slice()
    next[idx] = { ...next[idx], algorithm_type: algo }
    setForm({ ...form, endpoints: next })
  }
  const toggleEpStatus = (idx) => {
    const next = form.endpoints.slice()
    next[idx] = { ...next[idx], in_route_status: next[idx].in_route_status === 0 ? 1 : 0 }
    setForm({ ...form, endpoints: next })
  }

  const save = async () => {
    const editing = form.id ? routes.find((r) => r.id === form.id) : null
    const userId = editing ? editing.user_id : parseInt(form.user_id, 10) || 0
    const modelId = editing ? editing.user_model_id : parseInt(form.user_model_id, 10) || 0
    const protocolType = editing ? editing.protocol_type : parseInt(form.protocol_type, 10) || 0
    if (!isAdmin && (!editing || !editing.user_model_id || !editing.protocol_type)) { setFormError('路由信息不完整，请刷新后重试'); return }
    if (!userId || !modelId || !protocolType) { setFormError('请选择用户、模型和协议'); return }
    if (!form.endpoints.length) { setFormError('请至少选择一个目标源站'); return }
    setSaving(true)
    setFormError('')
    try {
      if (!isAdmin) {
        // 用户端受限更新：仅允许调整源站列表（顺序/直连转换/启用禁用）与算法策略
        await post('UserAIRouteInterface', {
          action: 'update',
          id: form.id,
          dst_endpoint_id: form.endpoints[0].id,
          dst_endpoint_id_list: form.endpoints.map((x) => x.id).join(','),
          dst_endpoint_id_status_list: form.endpoints.map((x) => x.in_route_status).join(','),
          dst_endpoint_algorithm_type_list: form.endpoints.map((x) => x.algorithm_type).join(','),
          algorithm_strategy_type: parseInt(form.algorithm_strategy_type, 10) || 1,
        })
        setForm(null)
        loadRoutes()
        return
      }
      await post('AIRouteManageInterface', {
        action: form.id ? 'update' : 'add',
        id: form.id || 0,
        user_id: userId,
        user_model_id: modelId,
        protocol_type: protocolType,
        dst_endpoint_id: form.endpoints[0].id,
        dst_endpoint_id_list: form.endpoints.map((x) => x.id).join(','),
        dst_endpoint_id_status_list: form.endpoints.map((x) => x.in_route_status).join(','),
        dst_endpoint_algorithm_type_list: form.endpoints.map((x) => x.algorithm_type).join(','),
        algorithm_strategy_type: parseInt(form.algorithm_strategy_type, 10) || 1,
      })
      setForm(null)
      loadRoutes()
    } catch (e) { setFormError(e.message) } finally { setSaving(false) }
  }

  const deleteItem = async (route) => {
    if (!confirm(`确认删除以下路由？\n\n${route.user_name || ''} / ${route.model_name || ''}\n\n此操作不可恢复！`)) return
    try {
      await post('AIRouteManageInterface', { action: 'delete', id: route.id })
      loadRoutes()
    } catch (e) { alert(e.message) }
  }

  const batchDelete = async () => {
    const ids = [...selected]
    if (!ids.length) { alert('请先选择要删除的路由'); return }
    if (!confirm('确认删除选中的 ' + ids.length + ' 条路由？此操作不可恢复！')) return
    try {
      await post('AIRouteManageInterface', { action: 'batch_delete', ids })
      setSelected(new Set())
      loadRoutes()
    } catch (e) { alert(e.message) }
  }

  const batchUpdateAlgo = async () => {
    const ids = [...selected]
    if (!ids.length) { alert('请先选择要编辑的路由'); return }
    const type = prompt('批量设置算法策略：1=指定型 2=稳定型 3=经济型（输入数字）', '2')
    const algo = parseInt(type, 10)
    if (![1, 2, 3].includes(algo)) return
    try {
      await post('AIRouteManageInterface', { action: 'batch_update', ids, algorithm_strategy_type: algo })
      setSelected(new Set())
      loadRoutes()
    } catch (e) { alert(e.message) }
  }

  const toggleSelect = (id) => {
    const next = new Set(selected)
    if (next.has(id)) next.delete(id); else next.add(id)
    setSelected(next)
  }

  const renderEpList = (route) => {
    const list = route.endpoint_list || []
    if (!list.length) return `${route.platform_name || ''} / ${route.endpoint_model_name || ''}`
    return (
      <div className="chip-list">
        {list.map((ep, i) => (
          <span key={ep.id + '-' + i} className={'ep-chip' + (ep.in_route_status === 0 ? ' ep-chip-off' : '')}>
            {i + 1}. {ep.platform_name} / {ep.model_name}{ep.algorithm_name ? ' · ' + ep.algorithm_name : ''}
          </span>
        ))}
      </div>
    )
  }

  const renderLastRecord = (route, kind) => {
    const failed = kind === 'success' ? route.last_success_failed : route.last_failure_failed
    const has = kind === 'success' ? route.last_success_has_record : route.last_failure_has_record
    if (failed) return <span style={{ color: '#c0392b', fontWeight: 600 }}>查询失败</span>
    if (!has) return <span style={{ color: '#999', fontStyle: 'italic' }}>{kind === 'success' ? '暂无成功记录' : '暂无失败记录'}</span>
    const status = (kind === 'success' ? route.last_success_status : route.last_failure_status) || '传输错误'
    const time = kind === 'success' ? route.last_success_at_text : route.last_failure_at_text
    const model = kind === 'success' ? route.last_success_dst_model_name : route.last_failure_dst_model_name
    return (
      <span style={{ display: 'inline-flex', flexDirection: 'column', fontSize: 12 }}>
        <b style={{ color: kind === 'success' ? '#155724' : '#721c24' }}>{status}</b>
        <span>{time}</span>
        {model ? <span style={{ color: '#0c4a8f' }}>{model}</span> : null}
      </span>
    )
  }

  const columns = [
    ...(isAdmin ? [{
      key: 'checkbox', title: (
        <input type="checkbox" title="全选"
          checked={routes.length > 0 && selected.size >= routes.length}
          onChange={(e) => setSelected(e.target.checked ? new Set(routes.map((r) => r.id)) : new Set())} />
      ), width: 36,
      render: (_, r) => <input type="checkbox" checked={selected.has(r.id)} onChange={() => toggleSelect(r.id)} />,
    }] : []),
    { key: 'id', title: 'ID', width: 60 },
    ...(isAdmin ? [{ key: 'user_name', title: '所属用户' }] : []),
    { key: 'model_name', title: '模型名' },
    { key: 'endpoints', title: '目标源站列表', render: (_, r) => renderEpList(r) },
    { key: 'protocol_type', title: '协议', render: (v) => protocolName(v) },
    { key: 'algorithm_name', title: '算法策略', render: (v, r) => v || ALGO_NAMES[r.algorithm_strategy_type] || '-' },
    {
      key: 'stats', title: (
        <span>
          汇总统计{' '}
          <select value={days} onChange={(e) => setDays(normalizeDays(e.target.value))} style={{ fontSize: 12 }}>
            {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{daysLabel(d)}</option>)}
          </select>
        </span>
      ),
      render: (_, r) => {
        const s = stats[r.id]
        if (!s) return <span style={{ color: '#999' }}>-</span>
        const a = s.anthropic_count || 0
        const o = s.openai_count || 0
        if (!a && !o) return <span style={{ color: '#999' }}>-</span>
        return (
          <span>
            {a > 0 ? <b style={{ color: '#6f42c1' }}>A:{a}</b> : null}
            {a > 0 && o > 0 ? <span style={{ color: '#c0c4cc' }}> / </span> : null}
            {o > 0 ? <b style={{ color: '#28a745' }}>O:{o}</b> : null}
          </span>
        )
      },
    },
    { key: 'last_success', title: '最后成功记录', render: (_, r) => renderLastRecord(r, 'success') },
    { key: 'last_failure', title: '最后失败记录', render: (_, r) => renderLastRecord(r, 'failure') },
    {
      key: 'actions', title: '操作',
      render: (_, r) => (
        <span className="op-btns">
          <button className="btn btn-sm btn-primary" onClick={() => openEdit(r)}>编辑</button>
          <a className="btn btn-link" href={`#/ChatDialog?user_name=${encodeURIComponent(r.user_name || '')}&model_name=${encodeURIComponent(r.model_name || '')}`}>对话</a>
          <a className="btn btn-link" href={`#/ChatAnalysis?user_name=${encodeURIComponent(r.user_name || '')}&model_name=${encodeURIComponent(r.model_name || '')}`}>对话明细分析</a>
          <a className="btn btn-link" href={`#/ChatAnalysisTotal?user_name=${encodeURIComponent(r.user_name || '')}&model_name=${encodeURIComponent(r.model_name || '')}${days >= 0 ? '&days=' + days : ''}`}>汇总统计</a>
          {isAdmin ? <button className="btn btn-sm btn-danger" onClick={() => deleteItem(r)}>删除</button> : null}
        </span>
      ),
    },
  ]

  return (
    <div className="page">
      <h2 className="page-title">智能路由管理</h2>
      <div className="toolbar">
        <button className="btn" onClick={loadRoutes}>刷新</button>
        {isAdmin ? <button className="btn btn-primary" onClick={openAdd}>+ 添加路由</button> : null}
        {!isAdmin ? <span style={{ color: '#888', fontSize: 13 }}>用户模式：仅显示并允许编辑本人模型的路由</span> : null}
        {selected.size > 0 ? (
          <>
            <span>已选择 {selected.size} 条</span>
            <button className="btn btn-sm" onClick={batchUpdateAlgo}>批量编辑算法</button>
            <button className="btn btn-sm btn-danger" onClick={batchDelete}>批量删除</button>
            <button className="btn btn-sm" onClick={() => setSelected(new Set())}>取消选择</button>
          </>
        ) : null}
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={routes} loading={loading} empty="暂无路由配置" rowKey="id" />
      </div>

      {form ? (
        <Modal
          title={form.id ? '编辑路由' : '添加路由'}
          width={760}
          onClose={() => setForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setForm(null)}>取消</button>
              <button className="btn btn-primary" disabled={saving} onClick={save}>保存</button>
            </>
          }
        >
          {formError ? <div className="alert alert-error">{formError}</div> : null}
          {!isAdmin ? (
            <dl className="kv" style={{ marginBottom: 10 }}>
              <dt>模型</dt><dd><b>{form.id ? (routes.find((r) => r.id === form.id) || {}).model_name : ''}</b></dd>
              <dt>协议</dt><dd>{protocolName(form.id ? (routes.find((r) => r.id === form.id) || {}).protocol_type : 0)}</dd>
            </dl>
          ) : null}
          {isAdmin ? (
          <>
          <label className="field"><span>选择用户</span>
            <select value={form.user_id} disabled={!!form.id} onChange={(e) => onUserChange(e.target.value)}>
              <option value="">请选择用户</option>
              {users.map((u) => (
                <option key={u.id} value={u.id}>{u.user_name}</option>
              ))}
            </select>
          </label>
          <label className="field"><span>模型列表</span>
            <select value={form.user_model_id} disabled={!!form.id} onChange={(e) => setForm({ ...form, user_model_id: parseInt(e.target.value, 10), protocol_type: '', endpoints: [] })}>
              <option value="">{form.user_id ? '请选择模型' : '请先选择用户'}</option>
              {formModels.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.model_name} ({(m.api_key || '').substring(0, 8)}****)
                </option>
              ))}
            </select>
          </label>
          <label className="field"><span>协议类型</span>
            <select value={form.protocol_type} disabled={!!form.id} onChange={(e) => setForm({ ...form, protocol_type: parseInt(e.target.value, 10), endpoints: [] })}>
              <option value="">{form.user_model_id ? '请选择协议' : '请先选择模型'}</option>
              {protocolOptions().map((p) => <option key={p} value={p}>{protocolName(p)}</option>)}
            </select>
          </label>
          </>
          ) : null}
          <div className="field"><span>目标源站</span>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <select style={{ flex: 1 }} value="" onChange={(e) => { if (e.target.value) addEndpoint(parseInt(e.target.value, 10)) }}>
                <option value="">{form.protocol_type ? '请选择源站' : '请先选择协议'}</option>
                {form.protocol_type
                  ? formEndpoints
                      .filter((ep) => ep.status == 1 && !form.endpoints.some((x) => x.id === ep.id)) // eslint-disable-line eqeqeq
                      .sort((a, b) => ((a.platform_name || '') + (a.model_name || '')).localeCompare((b.platform_name || '') + (b.model_name || ''), 'zh-Hans-CN'))
                      .map((ep) => (
                        <option key={ep.id} value={ep.id}>
                          {ep.platform_name} / {ep.model_name} [{protocolName(ep.protocol_type)} · {epAlgoForProtocol(ep, form.protocol_type) === 1 ? '协议直连' : '协议转换器'}]
                        </option>
                      ))
                  : null}
              </select>
            </div>
            <div style={{ border: '1px solid #ddd', borderRadius: 4, padding: 8, minHeight: 40, background: '#fafafa' }}>
              {form.endpoints.length === 0 ? <span style={{ color: '#999', fontSize: 13 }}>暂无已选源站</span> : form.endpoints.map((sel, i) => {
                const ep = formEndpoints.find((e) => e.id == sel.id) // eslint-disable-line eqeqeq
                return (
                  <div key={sel.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, padding: '6px 8px', border: '1px solid #e0e0e0', borderRadius: 6, marginBottom: 6, fontSize: 13, background: '#fff' }}>
                    <span>{i + 1}. {ep ? `${ep.platform_name} / ${ep.model_name} [${protocolName(ep.protocol_type)}]` : 'ID: ' + sel.id}</span>
                    <span style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                      <label style={{ fontSize: 12 }}><input type="radio" checked={sel.algorithm_type === 1} onChange={() => setEpAlgo(i, 1)} disabled={ep && !epAlgoValid(ep, form.protocol_type, 1)} />直连</label>
                      <label style={{ fontSize: 12 }}><input type="radio" checked={sel.algorithm_type === 2} onChange={() => setEpAlgo(i, 2)} disabled={ep && !epAlgoValid(ep, form.protocol_type, 2)} />转换器</label>
                      <button className="btn btn-sm" onClick={() => toggleEpStatus(i)}>{sel.in_route_status === 0 ? '已禁用' : '已启用'}</button>
                      <button className="btn btn-sm" disabled={i === 0} onClick={() => moveEndpoint(i, -1)}>↑</button>
                      <button className="btn btn-sm" disabled={i === form.endpoints.length - 1} onClick={() => moveEndpoint(i, 1)}>↓</button>
                      <button className="btn btn-sm btn-danger" onClick={() => removeEndpoint(sel.id)}>移除</button>
                    </span>
                  </div>
                )
              })}
            </div>
          </div>
          <label className="field"><span>算法策略</span>
            <select value={form.algorithm_strategy_type} onChange={(e) => setForm({ ...form, algorithm_strategy_type: parseInt(e.target.value, 10) })}>
              <option value={1}>指定型</option>
              <option value={2}>稳定型</option>
              <option value={3}>经济型</option>
            </select>
            <div style={{ marginTop: 6, padding: 10, background: '#f0f7ff', borderRadius: 6, fontSize: 12, color: '#0066cc' }}>
              {ALGO_DESC[form.algorithm_strategy_type] || ''}
            </div>
          </label>
        </Modal>
      ) : null}
    </div>
  )
}

function normalizeDays(v) {
  const n = parseInt(v, 10)
  return DAYS_OPTIONS.includes(n) ? n : 3
}
