import { useCallback, useEffect, useMemo, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { useI18n } from '../i18n'

// 折叠/分页 localStorage 工具（带容错）
const safeGet = (k) => { try { return window.localStorage.getItem(k) } catch { return null } }
const safeSet = (k, v) => { try { window.localStorage.setItem(k, v) } catch { /* 忽略 */ } }
const PAGE_SIZES = [10, 20, 30, 50, 100]
const DEFAULT_PAGE_SIZE = 50

// 智能路由管理（管理端）：AIRouteManageInterface（POST JSON {action:...}）
// action: list / list_models / list_endpoints / add / update / delete / batch_delete / batch_update / batch_stats
// 用户端（29001）：UserAIRouteInterface（action: list / list_endpoints / count_record_by_protocol / update），
// 仅限本人路由的受限编辑，无新增/删除/批量。

const DAYS_OPTIONS = [-1, -2, -4, -6, -12, 1, 3, 5, 7, 14, 30, 60, 90, 0]

const protocolName = (t) => (parseInt(t, 10) === 1 ? 'Anthropic' : 'OpenAI')
const protocolSlug = (t) => (parseInt(t, 10) === 1 ? 'anthropic' : 'openai')
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
  const { t } = useI18n()
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

  // 算法名称/描述（需 t() 内联，因为含变量）
  const ALGO_NAMES = { 1: t('aiRouteManage.algoDesignated'), 2: t('aiRouteManage.algoStable'), 3: t('aiRouteManage.algoEconomic') }
  const ALGO_DESC = {
    1: t('aiRouteManage.algoDirectDesc'),
    2: t('aiRouteManage.algoStableDesc'),
    3: t('aiRouteManage.algoEconomicDesc'),
  }

  // 天数标签（需 t() 内联，因为含变量）
  const daysLabel = (d) => (d < 0 ? t('aiRouteManage.daysHour', { hours: -d }) : d === 0 ? t('aiRouteManage.daysAll') : t('aiRouteManage.daysDay', { days: d }))

  // 折叠/展开状态（按角色隔离 localStorage）
  const collapseKey = `lsm:airoute:collapsed:${isAdmin ? 'manager' : 'user'}`
  const [collapsedIds, setCollapsedIds] = useState(() => {
    try { const raw = safeGet(collapseKey); return new Set(raw ? JSON.parse(raw) : []) } catch { return new Set() }
  })
  const toggleCollapse = (id) => {
    setCollapsedIds((prev) => {
      const next = new Set(prev)
      next.has(id) ? next.delete(id) : next.add(id)
      safeSet(collapseKey, JSON.stringify([...next]))
      return next
    })
  }

  // 分页状态（按角色隔离 localStorage）
  const pageKey = `lsm:airoute:page:${isAdmin ? 'manager' : 'user'}`
  const [page, setPage] = useState(() => {
    try { const raw = safeGet(pageKey); if (raw) { const s = JSON.parse(raw); if (s && s.page > 0) return s.page } } catch { /* 忽略 */ }
    return 1
  })
  const [pageSize, setPageSize] = useState(() => {
    try { const raw = safeGet(pageKey); if (raw) { const s = JSON.parse(raw); if (s && PAGE_SIZES.includes(s.pageSize)) return s.pageSize } } catch { /* 忽略 */ }
    return DEFAULT_PAGE_SIZE
  })
  useEffect(() => { safeSet(pageKey, JSON.stringify({ page, pageSize })) }, [page, pageSize, pageKey])

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
    if (!epId) { alert(t('aiRouteManage.pleaseSelectEndpointFirst')); return }
    if (form.endpoints.some((x) => x.id === epId)) { alert(t('aiRouteManage.endpointAlreadyAdded')); return }
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
      alert(algo === 1 ? t('aiRouteManage.directProtocolMismatch') : t('aiRouteManage.converterProtocolMismatch'))
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
    if (!isAdmin && (!editing || !editing.user_model_id || !editing.protocol_type)) { setFormError(t('aiRouteManage.routeInfoIncomplete')); return }
    if (!userId || !modelId || !protocolType) { setFormError(t('aiRouteManage.pleaseSelectUserModelProtocol')); return }
    if (!form.endpoints.length) { setFormError(t('aiRouteManage.pleaseSelectAtLeastOneEndpoint')); return }
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
    if (!confirm(t('aiRouteManage.deleteRouteConfirm', { userName: route.user_name || '', modelName: route.model_name || '' }))) return
    try {
      await post('AIRouteManageInterface', { action: 'delete', id: route.id })
      loadRoutes()
    } catch (e) { alert(e.message) }
  }

  const batchDelete = async () => {
    const ids = [...selected]
    if (!ids.length) { alert(t('aiRouteManage.pleaseSelectRouteToDelete')); return }
    if (!confirm(t('aiRouteManage.deleteSelectedConfirm', { count: ids.length }))) return
    try {
      await post('AIRouteManageInterface', { action: 'batch_delete', ids })
      setSelected(new Set())
      loadRoutes()
    } catch (e) { alert(e.message) }
  }

  const batchUpdateAlgo = async () => {
    const ids = [...selected]
    if (!ids.length) { alert(t('aiRouteManage.pleaseSelectRouteToEdit')); return }
    const type = prompt(
      t('aiRouteManage.batchSetAlgoPrompt', {
        designated: t('aiRouteManage.algoDesignated'),
        stable: t('aiRouteManage.algoStable'),
        economic: t('aiRouteManage.algoEconomic'),
      }),
      '2'
    )
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
    if (failed) return <span style={{ color: '#c0392b', fontWeight: 600 }}>{t('aiRouteManage.queryFailed')}</span>
    if (!has) return <span style={{ color: '#999', fontStyle: 'italic' }}>{kind === 'success' ? t('aiRouteManage.noSuccessRecord') : t('aiRouteManage.noFailureRecord')}</span>
    const status = (kind === 'success' ? route.last_success_status : route.last_failure_status) || t('aiRouteManage.transferError')
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
        <input type="checkbox" title={t('aiRouteManage.selectAll')}
          checked={routes.length > 0 && selected.size >= routes.length}
          onChange={(e) => setSelected(e.target.checked ? new Set(routes.map((r) => r.id)) : new Set())} />
      ), width: 36,
      render: (_, r) => <input type="checkbox" checked={selected.has(r.id)} onChange={() => toggleSelect(r.id)} />,
    }] : []),
    { key: 'id', title: t('aiRouteManage.id'), width: 60 },
    ...(isAdmin ? [{ key: 'user_name', title: t('aiRouteManage.belongUser') }] : []),
    { key: 'model_name', title: t('aiRouteManage.modelName') },
    { key: 'endpoints', title: t('aiRouteManage.targetList'), render: (_, r) => renderEpList(r) },
    { key: 'protocol_type', title: t('aiRouteManage.protocol'), render: (v) => (
      <span className={`protocol-badge protocol-${protocolSlug(v)}`}>{protocolName(v)}</span>
    ) },
    { key: 'algorithm_name', title: t('aiRouteManage.algoStrategy'), render: (v, r) => v || ALGO_NAMES[r.algorithm_strategy_type] || '-' },
    {
      key: 'stats', title: (
        <span>
          {t('aiRouteManage.summaryStats')}{' '}
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
    { key: 'last_success', title: t('aiRouteManage.lastSuccessRecord'), render: (_, r) => renderLastRecord(r, 'success') },
    { key: 'last_failure', title: t('aiRouteManage.lastFailureRecord'), render: (_, r) => renderLastRecord(r, 'failure') },
    {
      key: 'actions', title: t('aiRouteManage.operation'),
      render: (_, r) => (
        <span className="op-btns">
          <button className="btn btn-sm btn-primary" onClick={() => openEdit(r)}>{t('aiRouteManage.editRoute')}</button>
          <a className="btn btn-link" href={`#/ChatDialog?user_name=${encodeURIComponent(r.user_name || '')}&model_name=${encodeURIComponent(r.model_name || '')}`}>{t('aiRouteManage.dialog')}</a>
          <a className="btn btn-link" href={`#/ChatAnalysis?user_name=${encodeURIComponent(r.user_name || '')}&model_name=${encodeURIComponent(r.model_name || '')}`}>{t('aiRouteManage.dialogAnalysis')}</a>
          <a className="btn btn-link" href={`#/ChatAnalysisTotal?user_name=${encodeURIComponent(r.user_name || '')}&model_name=${encodeURIComponent(r.model_name || '')}${days >= 0 ? '&days=' + days : ''}`}>{t('aiRouteManage.summaryStats')}</a>
          {isAdmin ? <button className="btn btn-sm btn-danger" onClick={() => deleteItem(r)}>{t('aiRouteManage.deleteRoute')}</button> : null}
        </span>
      ),
    },
  ]

  // 分页计算
  const totalPages = Math.max(1, Math.ceil(routes.length / pageSize))
  const safePage = Math.min(page, totalPages)
  const pagedRoutes = useMemo(() => {
    const start = (safePage - 1) * pageSize
    return routes.slice(start, start + pageSize)
  }, [routes, safePage, pageSize])

  return (
    <div className="page">
      <h2 className="page-title">{t('aiRouteManage.title')}</h2>
      <div className="toolbar">
        <button className="btn" onClick={loadRoutes}>{t('aiRouteManage.refresh')}</button>
        {isAdmin ? <button className="btn btn-primary" onClick={openAdd}>+ {t('aiRouteManage.addRoute')}</button> : null}
        {!isAdmin ? <span style={{ color: '#888', fontSize: 13 }}>{t('aiRouteManage.userMode')}</span> : null}
        {selected.size > 0 ? (
          <>
            <span>{t('aiRouteManage.selected', { count: selected.size })}</span>
            <button className="btn btn-sm" onClick={batchUpdateAlgo}>{t('aiRouteManage.batchEditAlgo')}</button>
            <button className="btn btn-sm btn-danger" onClick={batchDelete}>{t('aiRouteManage.batchDelete')}</button>
            <button className="btn btn-sm" onClick={() => setSelected(new Set())}>{t('aiRouteManage.cancelSelection')}</button>
          </>
        ) : null}
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card">
        <DataTable columns={columns} rows={pagedRoutes} loading={loading} empty={t('aiRouteManage.noRoutesConfig')} rowKey="id"
          rowClass={(r) => 'row-protocol-' + protocolSlug(r.protocol_type)}
          collapsible collapsedIds={collapsedIds} onToggleCollapse={toggleCollapse}
          renderCollapsedRow={(r, onToggle) => (
            <div className="collapsed-summary">
              <button type="button" className="collapse-btn" onClick={onToggle} title={t('aiRouteManage.expand')} aria-label={t('aiRouteManage.expand')}>▶</button>
              <span className="collapsed-id">#{r.id}</span>
              <span className={`protocol-badge protocol-${protocolSlug(r.protocol_type)}`}>{protocolName(r.protocol_type)}</span>
              <span className="collapsed-model">{r.model_name || '-'}</span>
              <span className="collapsed-hint">{r.algorithm_name || ALGO_NAMES[r.algorithm_strategy_type] || ''} · {t('aiRouteManage.sourcesCount', { count: (r.endpoint_list || []).length })}</span>
            </div>
          )} />
        <div className="pager">
          <span>{t('aiRouteManage.totalPages', { total: routes.length, page: safePage, pages: totalPages })}</span>
          <select value={pageSize} onChange={(e) => { setPageSize(parseInt(e.target.value, 10)); setPage(1) }}>
            {PAGE_SIZES.map((n) => <option key={n} value={n}>{n}</option>)}
          </select>
          <button className="btn btn-sm" disabled={safePage <= 1} onClick={() => setPage((p) => Math.max(1, p - 1))}>{t('aiRouteManage.prevPage')}</button>
          <button className="btn btn-sm" disabled={safePage >= totalPages} onClick={() => setPage((p) => Math.min(totalPages, p + 1))}>{t('aiRouteManage.nextPage')}</button>
        </div>
      </div>

      {form ? (
        <Modal
          title={form.id ? t('aiRouteManage.editRoute') : t('aiRouteManage.addRoute')}
          width={760}
          onClose={() => setForm(null)}
          footer={
            <>
              <button className="btn" onClick={() => setForm(null)}>{t('aiRouteManage.cancel')}</button>
              <button className="btn btn-primary" disabled={saving} onClick={save}>{t('aiRouteManage.save')}</button>
            </>
          }
        >
          {formError ? <div className="alert alert-error">{formError}</div> : null}
          {!isAdmin ? (
            <dl className="kv" style={{ marginBottom: 10 }}>
              <dt>{t('aiRouteManage.model')}</dt><dd><b>{form.id ? (routes.find((r) => r.id === form.id) || {}).model_name : ''}</b></dd>
              <dt>{t('aiRouteManage.protocol')}</dt><dd>{protocolName(form.id ? (routes.find((r) => r.id === form.id) || {}).protocol_type : 0)}</dd>
            </dl>
          ) : null}
          {isAdmin ? (
          <>
          <label className="field"><span>{t('aiRouteManage.selectUser')}</span>
            <select value={form.user_id} disabled={!!form.id} onChange={(e) => onUserChange(e.target.value)}>
              <option value="">{t('aiRouteManage.pleaseSelectUser')}</option>
              {users.map((u) => (
                <option key={u.id} value={u.id}>{u.user_name}</option>
              ))}
            </select>
          </label>
          <label className="field"><span>{t('aiRouteManage.modelList')}</span>
            <select value={form.user_model_id} disabled={!!form.id} onChange={(e) => setForm({ ...form, user_model_id: parseInt(e.target.value, 10), protocol_type: '', endpoints: [] })}>
              <option value="">{form.user_id ? t('aiRouteManage.pleaseSelectModel') : t('aiRouteManage.pleaseSelectUserFirst')}</option>
              {formModels.map((m) => (
                <option key={m.id} value={m.id}>
                  {m.model_name} ({(m.api_key || '').substring(0, 8)}****)
                </option>
              ))}
            </select>
          </label>
          <label className="field"><span>{t('aiRouteManage.protocolType')}</span>
            <select value={form.protocol_type} disabled={!!form.id} onChange={(e) => setForm({ ...form, protocol_type: parseInt(e.target.value, 10), endpoints: [] })}>
              <option value="">{form.user_model_id ? t('aiRouteManage.pleaseSelectProtocol') : t('aiRouteManage.pleaseSelectModelFirst')}</option>
              {protocolOptions().map((p) => <option key={p} value={p}>{protocolName(p)}</option>)}
            </select>
          </label>
          </>
          ) : null}
          <div className="field"><span>{t('aiRouteManage.targetEndpoints')}</span>
            <div style={{ display: 'flex', gap: 8, marginBottom: 8 }}>
              <select style={{ flex: 1 }} value="" onChange={(e) => { if (e.target.value) addEndpoint(parseInt(e.target.value, 10)) }}>
                <option value="">{form.protocol_type ? t('aiRouteManage.pleaseSelectEndpoint') : t('aiRouteManage.pleaseSelectProtocolFirst')}</option>
                {form.protocol_type
                  ? formEndpoints
                      .filter((ep) => ep.status == 1 && !form.endpoints.some((x) => x.id === ep.id)) // eslint-disable-line eqeqeq
                      .sort((a, b) => ((a.platform_name || '') + (a.model_name || '')).localeCompare((b.platform_name || '') + (b.model_name || ''), 'zh-Hans-CN'))
                      .map((ep) => (
                        <option key={ep.id} value={ep.id}>
                          {ep.platform_name} / {ep.model_name} [{protocolName(ep.protocol_type)} · {epAlgoForProtocol(ep, form.protocol_type) === 1 ? t('aiRouteManage.protocolDirect') : t('aiRouteManage.protocolConverter')}]
                        </option>
                      ))
                  : null}
              </select>
            </div>
            <div style={{ border: '1px solid #ddd', borderRadius: 4, padding: 8, minHeight: 40, background: '#fafafa' }}>
              {form.endpoints.length === 0 ? <span style={{ color: '#999', fontSize: 13 }}>{t('aiRouteManage.noSelectedEndpoints')}</span> : form.endpoints.map((sel, i) => {
                const ep = formEndpoints.find((e) => e.id == sel.id) // eslint-disable-line eqeqeq
                return (
                  <div key={sel.id} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 10, padding: '6px 8px', border: '1px solid #e0e0e0', borderRadius: 6, marginBottom: 6, fontSize: 13, background: '#fff' }}>
                    <span>{i + 1}. {ep ? `${ep.platform_name} / ${ep.model_name} [${protocolName(ep.protocol_type)}]` : 'ID: ' + sel.id}</span>
                    <span style={{ display: 'flex', gap: 6, alignItems: 'center' }}>
                      <label style={{ fontSize: 12 }}><input type="radio" checked={sel.algorithm_type === 1} onChange={() => setEpAlgo(i, 1)} disabled={ep && !epAlgoValid(ep, form.protocol_type, 1)} />{t('aiRouteManage.directConnect')}</label>
                      <label style={{ fontSize: 12 }}><input type="radio" checked={sel.algorithm_type === 2} onChange={() => setEpAlgo(i, 2)} disabled={ep && !epAlgoValid(ep, form.protocol_type, 2)} />{t('aiRouteManage.converter')}</label>
                      <button className="btn btn-sm" onClick={() => toggleEpStatus(i)}>{sel.in_route_status === 0 ? t('aiRouteManage.disabledStatus') : t('aiRouteManage.enabledStatus')}</button>
                      <button className="btn btn-sm" disabled={i === 0} onClick={() => moveEndpoint(i, -1)}>↑</button>
                      <button className="btn btn-sm" disabled={i === form.endpoints.length - 1} onClick={() => moveEndpoint(i, 1)}>↓</button>
                      <button className="btn btn-sm btn-danger" onClick={() => removeEndpoint(sel.id)}>{t('aiRouteManage.removeOp')}</button>
                    </span>
                  </div>
                )
              })}
            </div>
          </div>
          <label className="field"><span>{t('aiRouteManage.algoStrategy')}</span>
            <select value={form.algorithm_strategy_type} onChange={(e) => setForm({ ...form, algorithm_strategy_type: parseInt(e.target.value, 10) })}>
              <option value={1}>{t('aiRouteManage.algoDesignated')}</option>
              <option value={2}>{t('aiRouteManage.algoStable')}</option>
              <option value={3}>{t('aiRouteManage.algoEconomic')}</option>
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
