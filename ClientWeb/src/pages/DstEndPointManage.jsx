import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import PageHeader from '../components/PageHeader'
import { useI18n } from '../i18n'
import { useConfirm } from '../components/ConfirmModal'

// 源站管理（管理端）：DstEndPointManageInterface（POST JSON {action:...}）
// action: list / add / update / toggle_status / delete / batch_enable / batch_disable / batch_delete / test / list_platforms / list_models
// 用户端（29001）同名接口仅支持 list / test（只读 + 连通性测试），增删改按钮不展示。

const emptyPeriod = () => ({ start: '09:00:00', end: '18:00:00' })
// 新增源站默认全天工作（0-24）
const allDayPeriod = () => ({ start: '00:00:00', end: '24:00:00' })

const emptyForm = {
  id: 0, user_id: 0, platform_name: '', model_name: '',
  protocol_type: 1, auth_type: 0, url_address: '', api_key: '',
  work_periods: '', // 后端返回 JSON 字符串；编辑态下由 periods 数组驱动
  work_enabled: 1,  // 工作时间控制开关：1=启用（按时间段），0=禁用（全天可用）
}

// ========== 工作时间段工具函数 ==========

// 解析 work_periods JSON → [{start, end}]（失败返回全天默认）
function parsePeriods(jsonStr) {
  if (!jsonStr) return [allDayPeriod()]
  try {
    const arr = JSON.parse(jsonStr)
    if (Array.isArray(arr) && arr.length > 0) {
      return arr.map((p) => ({
        start: (p.start || '').toString(),
        end: (p.end || '').toString(),
      }))
    }
  } catch { /* 解析失败兜底 */ }
  return [allDayPeriod()]
}

// [{start, end}] → JSON 字符串
function formatPeriods(periods) {
  return JSON.stringify(periods.map((p) => ({ start: p.start.trim(), end: p.end.trim() })))
}

// 判断是否全天 00:00:00-24:00:00
function isAllDay(periods) {
  if (periods.length !== 1) return false
  return periods[0].start.trim() === '00:00:00' && periods[0].end.trim() === '24:00:00'
}

// 校验单段时间格式 HH:MM:SS（允许 24:00:00）
function validateTimeStr(s) {
  const m = /^(\d{1,2}):(\d{1,2}):(\d{1,2})$/.exec(s.trim())
  if (!m) return false
  const h = parseInt(m[1], 10), mi = parseInt(m[2], 10), se = parseInt(m[3], 10)
  if (mi > 59 || se > 59) return false
  if (h === 24) return mi === 0 && se === 0
  return h >= 0 && h <= 23
}

// 时间字符串转秒数（用于排序/比较）
function toSeconds(s) {
  const m = /^(\d{1,2}):(\d{1,2}):(\d{1,2})$/.exec(s.trim())
  if (!m) return -1
  return parseInt(m[1], 10) * 3600 + parseInt(m[2], 10) * 60 + parseInt(m[3], 10)
}

// 校验时间段列表：格式、start < end、不重叠
function validatePeriods(periods) {
  for (const p of periods) {
    if (!validateTimeStr(p.start)) return { ok: false, key: 'dstEndPoint.workPeriodsFormatError', which: 'start' }
    if (!validateTimeStr(p.end)) return { ok: false, key: 'dstEndPoint.workPeriodsFormatError', which: 'end' }
    if (toSeconds(p.start) >= toSeconds(p.end)) return { ok: false, key: 'dstEndPoint.workPeriodsOrderError' }
  }
  // 检查重叠（按开始时间排序后相邻比较）
  const sorted = [...periods].sort((a, b) => toSeconds(a.start) - toSeconds(b.start))
  for (let i = 1; i < sorted.length; i++) {
    if (toSeconds(sorted[i].start) < toSeconds(sorted[i - 1].end)) {
      return { ok: false, key: 'dstEndPoint.workPeriodsOverlapError' }
    }
  }
  return { ok: true }
}

// 友好展示：全天 → "全天"；多段 → 首段 + "..." 悬停显示全部
function formatPeriodsDisplay(periods, t) {
  if (isAllDay(periods)) {
    return { short: t('dstEndPoint.workPeriodsAllDay'), full: '00:00:00 - 24:00:00', isAllDay: true }
  }
  const fullList = periods.map((p) => `${p.start} - ${p.end}`).join('\n')
  const first = `${periods[0].start} - ${periods[0].end}`
  const short = periods.length > 1 ? `${first} ...` : first
  return { short, full: fullList, isAllDay: false }
}

// ========== 按协议+认证方式拼出保存时实际发出的 Request Header（纯前端预览） ==========
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
  const sysConfirm = useConfirm()
  const isAdmin = __APP_ROLE__ === 'manager' ? isAdminRole() : false // 用户端：只读列表 + 连通性测试（构建期裁剪管理分支）
  const [users, setUsers] = useState([])
  const [endpoints, setEndpoints] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [form, setForm] = useState(null)
  const [periods, setPeriods] = useState([emptyPeriod()]) // 弹窗内时间段编辑态
  const [periodsError, setPeriodsError] = useState('')
  const [formError, setFormError] = useState('')
  const [saving, setSaving] = useState(false)
  const [selected, setSelected] = useState(new Set())
  const [testResult, setTestResult] = useState(null) // {success,message,data}
  const [platformOptions, setPlatformOptions] = useState([]) // list_platforms 去重平台名（可编辑弹窗 datalist）
  const [modelOptions, setModelOptions] = useState([])       // list_models 去重模型名
  // 阶段CK：表格当前列排序状态；用于显示「恢复默认排序」按钮，确保弹窗提交/刷新后顺序不变
  const [tableSort, setTableSort] = useState(null)

  // 弹窗打开 / 切换所属用户时刷新平台名与模型名候选（仅管理端，供 datalist 下拉选择）
  const loadNameOptions = useCallback((userId) => {
    post('DstEndPointManageInterface', { action: 'list_platforms', user_id: userId })
      .then((d) => setPlatformOptions((d && d.data) || []))
      .catch(() => setPlatformOptions([]))
    post('DstEndPointManageInterface', { action: 'list_models', user_id: userId })
      .then((d) => setModelOptions((d && d.data) || []))
      .catch(() => setModelOptions([]))
  }, [])

  // 阶段CK：用 ref 缓存当前 endpoints，避免 loadData 因 endpoints 变化导致 effect 循环
  const endpointsRef = useRef([])
  useEffect(() => { endpointsRef.current = endpoints }, [endpoints])

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
        // 阶段CK：保存当前行 id 集合，确保重新加载后保持既有记录的相对顺序：
        // 服务端按 id ASC 已是稳定顺序，但若用户曾点击列排序，DataTable 会按用户列排序展示；
        // 此处仅做"稳定去抖"——若服务端返回顺序与当前顺序完全一致（除新增/删除），则保留当前顺序，
        // 这样视觉上原数据位置不变，新增数据按服务端顺序追加到末尾。
        const prev = endpointsRef.current
        const prevIds = prev.map((ep) => ep.id)
        const next = (e && e.data) || []
        if (prevIds.length > 0) {
          const prevSet = new Set(prevIds)
          const kept = prevIds.filter((id) => next.some((x) => x.id === id)).map((id) => next.find((x) => x.id === id))
          const added = next.filter((x) => !prevSet.has(x.id))
          setEndpoints([...kept, ...added])
        } else {
          setEndpoints(next)
        }
      })
      .catch((e2) => setError(e2.message))
      .finally(() => setLoading(false))
  }, [isAdmin])

  useEffect(() => { loadData() }, [loadData])

  const userName = (uid) => {
    const u = users.find((x) => x.id == uid) // eslint-disable-line eqeqeq
    return u ? u.user_name : t('dstEndPoint.userName', { id: uid })
  }

  // 打开弹窗时解析 work_periods（新增默认全天 0-24，编辑回显已有配置）
  const openForm = (ep = null) => {
    setPeriodsError('')
    setFormError('')
    if (ep) {
      setForm({ ...emptyForm, ...ep, api_key: '' })
      setPeriods(parsePeriods(ep.work_periods))
    } else {
      setForm({ ...emptyForm })
      setPeriods([allDayPeriod()])
    }
  }

  const closeForm = () => {
    setForm(null)
    setPeriodsError('')
    setFormError('')
  }

  const save = async () => {
    setSaving(true)
    setFormError('')
    setPeriodsError('')
    const workEnabled = parseInt(form.work_enabled, 10) === 0 ? 0 : 1
    // 校验时间段（仅启用工作时间控制时校验；禁用=全天可用，时段仅保留配置）
    if (workEnabled === 1) {
      const v = validatePeriods(periods)
      if (!v.ok) {
        setPeriodsError(t(v.key))
        setSaving(false)
        return
      }
    }
    const body = {
      action: form.id ? 'update' : 'add',
      id: form.id || 0,
      user_id: parseInt(form.user_id, 10) || 0,
      platform_name: form.platform_name,
      model_name: form.model_name,
      protocol_type: parseInt(form.protocol_type, 10) || 1,
      auth_type: parseInt(form.auth_type, 10) || 0,
      url_address: form.url_address,
      work_enabled: workEnabled,
      work_periods: formatPeriods(periods),
    }
    if (!form.id || form.api_key) body.api_key = form.api_key
    try {
      await post('DstEndPointManageInterface', body, { timeout: 60000 })
      closeForm()
      loadData()
    } catch (e) {
      // 保存失败且带连通性详情（data 为对象）时弹测试结果窗，否则表单内联报错
      if (e && e.data && typeof e.data === 'object') setTestResult({ success: false, message: e.message, data: e.data })
      else setFormError(e.message)
    } finally { setSaving(false) }
  }

  const toggleStatus = async (ep) => {
    const status = ep.status == 1 ? 0 : 1 // eslint-disable-line eqeqeq
    if (!(await sysConfirm(t('dstEndPoint.confirmToggle', { action: status === 1 ? t('dstEndPoint.enableAction') : t('dstEndPoint.disableAction'), platform: ep.platform_name, model: ep.model_name })))) return
    try {
      await post('DstEndPointManageInterface', { action: 'toggle_status', id: ep.id, status })
      loadData()
    } catch (e) { alert(e.message) }
  }

  const deleteItem = async (ep) => {
    if (!(await sysConfirm(t('dstEndPoint.confirmDeleteEndpoint', { platform: ep.platform_name, model: ep.model_name })))) return
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
    if (!(await sysConfirm(t(`dstEndPoint.${batchConfirmKey}`, { count: ids.length })))) return
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

  // 阶段CK：恢复默认排序——清空持久化的列排序状态并强制 DataTable 重挂载（key 变化）回到服务端默认顺序
  const [tableResetNonce, setTableResetNonce] = useState(0)
  const resetTableSort = () => {
    try {
      const role = (typeof __APP_ROLE__ !== 'undefined' && __APP_ROLE__) || 'common'
      window.localStorage.removeItem(`lsm:datatable:sort:${role}:${window.location.pathname}`)
    } catch { /* 忽略 */ }
    setTableSort(null)
    setTableResetNonce((n) => n + 1)
  }
  // 阶段CK：DataTable 排序变化感知；同步到 tableSort 用于显示按钮
  const onTableSortChange = (sort) => {
    setTableSort(sort)
  }
  const hasUserSort = tableSort && tableSort.key && tableSort.dir

  // 列表工作时间列：截断显示友好文本，悬停通过 data-tooltip 显示完整时间段 + 状态
  // work_enabled=0（禁用工作时间控制）→ 显示"全天可用"+ 已停用徽标，状态点恒绿
  const workPeriodsColumn = useMemo(() => ({
    key: 'work_periods',
    title: t('dstEndPoint.workPeriods'),
    sortable: false,
    className: 'cell-nowrap',
    render: (_, ep) => {
      const workEnabled = ep.work_enabled != 0 // eslint-disable-line eqeqeq
      const p = parsePeriods(ep.work_periods)
      const display = formatPeriodsDisplay(p, t)
      // 完整悬停信息：控制开关状态 + 所有时间段逐行 + 当前工作时间状态
      const statusLine = ep.work_status == 1 ? t('dstEndPoint.workStatusInTime') : t('dstEndPoint.workStatusOffTime') // eslint-disable-line eqeqeq
      const enableLine = workEnabled ? t('dstEndPoint.workEnabledOn') : t('dstEndPoint.workEnabledOff')
      const fullTooltip = (workEnabled ? '' : '[' + t('dstEndPoint.workDisabledTip') + ']\n') + display.full + '\n[' + statusLine + ']'
      const inTime = workEnabled ? ep.work_status == 1 : true // eslint-disable-line eqeqeq
      const tagClass = inTime ? 'status-dot status-on' : 'status-dot status-off'
      if (!workEnabled) {
        // 禁用工作时间控制：全天可用 + "已停用"徽标，配置时段仅在悬停中展示
        return (
          <span data-tooltip={fullTooltip} style={{ display: 'inline-flex', alignItems: 'center', gap: 6, verticalAlign: 'middle' }}>
            <span className={tagClass} style={{ flexShrink: 0 }} />
            <span className="truncate" style={{ maxWidth: 150 }}>{display.isAllDay ? display.short : t('dstEndPoint.workPeriodsAllDay')}</span>
            <span className="badge" style={{ fontSize: 11, padding: '1px 5px', flexShrink: 0 }} data-tooltip={enableLine}>{t('dstEndPoint.workDisabledTag')}</span>
          </span>
        )
      }
      return (
        <span
          data-tooltip={fullTooltip}
          style={{ display: 'inline-flex', alignItems: 'center', gap: 6, verticalAlign: 'middle' }}
        >
          <span className={tagClass} style={{ flexShrink: 0 }} />
          <span className="truncate" style={{ maxWidth: 150 }}>
            {display.short}
          </span>
          {display.isAllDay ? <span className="badge badge-green" style={{ fontSize: 11, padding: '1px 5px', flexShrink: 0 }}>24h</span> : null}
        </span>
      )
    },
  }), [t])

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
    { key: 'protocol_type', title: t('dstEndPoint.protocolLabel'), sortable: true, render: (v) => (v == 1 ? t('dstEndPoint.anthropic') : t('dstEndPoint.openai')) }, // eslint-disable-line eqeqeq
    { key: 'url_address', title: t('dstEndPoint.urlLabel'), sortable: true, render: (v) => <span style={{ wordBreak: 'break-all', whiteSpace: 'normal' }}>{v}</span> },
    workPeriodsColumn,
    { key: 'status', title: t('dstEndPoint.statusLabel'), sortable: true, render: (v) => <span><span className={`status-dot ${v == 1 ? 'status-on' : 'status-off'}`} />{v == 1 ? t('dstEndPoint.enableAction') : t('dstEndPoint.disableAction')}</span> }, // eslint-disable-line eqeqeq
    {
      key: 'actions', title: t('common.action'),
      render: (_, ep) => (
        <span>
          {isAdmin ? <button className="btn btn-sm" onClick={() => toggleStatus(ep)}>{ep.status == 1 ? t('dstEndPoint.disableAction') : t('dstEndPoint.enableAction')}</button> : null}{' '}
          {isAdmin ? <button className="btn btn-sm btn-primary" onClick={() => openForm(ep)}>{t('common.edit')}</button> : null}{' '}
          <button className="btn btn-sm" onClick={() => testItem(ep)}>{t('dstEndPoint.testConnection')}</button>
          {isAdmin ? <>{' '}<button className="btn btn-sm btn-danger" onClick={() => deleteItem(ep)}>{t('common.delete')}</button></> : null}
        </span>
      ),
    },
  ]

  // 时间段编辑辅助
  const updatePeriod = (idx, field, value) => {
    setPeriods((prev) => prev.map((p, i) => (i === idx ? { ...p, [field]: value } : p)))
  }
  const addPeriod = () => {
    setPeriods((prev) => [...prev, emptyPeriod()])
  }
  const removePeriod = (idx) => {
    setPeriods((prev) => (prev.length <= 1 ? prev : prev.filter((_, i) => i !== idx)))
  }
  const setAllDay = () => {
    setPeriods([allDayPeriod()])
    setPeriodsError('')
  }

  return (
    <div className="page">
      <PageHeader icon="🌐" title={t('dstEndPoint.title')}
        breadcrumb={[t('nav.userRoute'), t('nav.dstEndPointManage')]}
        info={[
          <span key="total">{t('dstEndPoint.totalCount', { count: endpoints.length }) || `共 ${endpoints.length} 条`}</span>,
        ]}
        actions={<>
          <button className="btn" onClick={loadData}>{t('common.refresh')}</button>
          {isAdmin ? <button className="btn btn-primary" onClick={() => { const uid = users[0]?.id || 0; openForm(); setForm((f) => f ? { ...f, user_id: uid } : f); loadNameOptions(uid) }}>+ {t('dstEndPoint.addEndPoint')}</button>
            : null}
        </>}
      />
      {!isAdmin ? <div style={{ color: '#888', fontSize: 13, marginBottom: 12 }}>{t('dstEndPoint.userMode')}</div> : null}
      {selected.size > 0 ? (
        <div className="toolbar">
          <span>{t('dstEndPoint.selectedCountMax', { count: selected.size })}</span>
          <button className="btn btn-sm" onClick={() => runBatch('batch_enable')}>{t('dstEndPoint.batchEnable')}</button>
          <button className="btn btn-sm" onClick={() => runBatch('batch_disable')}>{t('dstEndPoint.batchDisable')}</button>
          <button className="btn btn-sm btn-danger" onClick={() => runBatch('batch_delete')}>{t('dstEndPoint.batchDelete')}</button>
          <button className="btn btn-sm" onClick={() => setSelected(new Set())}>{t('dstEndPoint.cancelSelection')}</button>
        </div>
      ) : null}
      {error ? <div className="alert alert-error">{error}</div> : null}
      {hasUserSort ? (
        <div className="toolbar" style={{ marginBottom: 8 }}>
          <span>{t('dstEndPoint.sortHint')}</span>
          <button className="btn btn-sm" onClick={resetTableSort}>{t('dstEndPoint.resetDefaultSort')}</button>
        </div>
      ) : null}
      <div className="card">
        <DataTable key={`dst-endpoint-${tableResetNonce}`} columns={columns} rows={endpoints} loading={loading} empty={t('dstEndPoint.noData')} rowKey="id"
          rowClass={(ep) => (ep.status == 1 ? 'row-enabled' : 'row-disabled')}
          onSortChange={onTableSortChange} /> {/* eslint-disable-line eqeqeq */}
      </div>

      {form ? (
        <Modal
          title={form.id ? t('dstEndPoint.editEndPoint') : t('dstEndPoint.addEndPoint')}
          onClose={closeForm}
          closeOnOverlayClick={false}
          footer={<>
            <button className="btn" onClick={closeForm}>{t('common.cancel')}</button>
            <button className="btn btn-primary" disabled={saving} onClick={save}>{t('common.save')}</button>
          </>}
        >
          {formError ? <div className="alert alert-error">{formError}</div> : null}
          <label className="field"><span>{t('dstEndPoint.userLabel')}</span>
            <select value={form.user_id} onChange={(e) => { const uid = parseInt(e.target.value, 10); setForm({ ...form, user_id: uid }); loadNameOptions(uid) }}>
              {users.map((u) => <option key={u.id} value={u.id}>{u.user_name}</option>)}
            </select>
          </label>
          <label className="field"><span>{t('dstEndPoint.platformNameLabel')}</span>
            <input value={form.platform_name} list="dst-endpoint-platform-names" placeholder={t('dstEndPoint.platformNamePlaceholder')} onChange={(e) => setForm({ ...form, platform_name: e.target.value })} />
            <datalist id="dst-endpoint-platform-names">
              {platformOptions.map((n) => <option key={n} value={n} />)}
            </datalist>
          </label>
          <label className="field"><span>{t('dstEndPoint.modelLabel')}</span>
            <input value={form.model_name} list="dst-endpoint-model-names" placeholder={t('dstEndPoint.modelNamePlaceholder')} onChange={(e) => setForm({ ...form, model_name: e.target.value })} />
            <datalist id="dst-endpoint-model-names">
              {modelOptions.map((n) => <option key={n} value={n} />)}
            </datalist>
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

          {/* ===== 工作时间控制（启用/禁用 + 时间段配置） ===== */}
          <div className="field"><span>{t('dstEndPoint.workEnabled')}</span>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 6, marginTop: 4 }}>
              {/* 启用/禁用开关：禁用 = 工作时间功能不生效，0-24 全天可用 */}
              <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                <label style={{ display: 'inline-flex', alignItems: 'center', gap: 5, fontSize: 13, margin: 0, cursor: 'pointer' }}>
                  <input
                    type="checkbox"
                    checked={parseInt(form.work_enabled, 10) !== 0}
                    onChange={(e) => setForm({ ...form, work_enabled: e.target.checked ? 1 : 0 })}
                  />
                  {parseInt(form.work_enabled, 10) !== 0 ? t('dstEndPoint.workEnabledOn') : t('dstEndPoint.workEnabledOff')}
                </label>
              </div>
              {/* 禁用提示：时间段配置保留但置灰，便于重新启用时恢复 */}
              {parseInt(form.work_enabled, 10) === 0 ? (
                <div className="alert" style={{ fontSize: 12, padding: '6px 10px', margin: 0, background: '#f8f9fa', color: '#666', border: '1px dashed #ddd' }}>
                  {t('dstEndPoint.workDisabledTip')}
                </div>
              ) : null}
              {/* 时间段编辑区：禁用时整体置灰只读 */}
              <div style={{
                display: 'flex', flexDirection: 'column', gap: 6,
                opacity: parseInt(form.work_enabled, 10) === 0 ? 0.5 : 1,
                pointerEvents: parseInt(form.work_enabled, 10) === 0 ? 'none' : 'auto',
              }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
                  <span style={{ fontSize: 12, color: '#888' }}>{t('dstEndPoint.workPeriodsHint')}</span>
                  <button type="button" className="btn btn-sm" onClick={setAllDay}>{t('dstEndPoint.workPeriodsAllDay')}</button>
                </div>
                {/* 阶段CO：行布局复用通用 .sortable-* 类，与智能路由「目标源站列表」同一视觉语言
                    （同一序号缩进、同一操作簇右对齐基线）。此处顺序不承载业务语义（时间段按 start 校验
                    重叠、按编辑序序列化），故有意不提供置顶/上移/下移/置底按钮。 */}
                {periods.map((p, idx) => (
                  <div key={idx} className="sortable-row">
                    <span className="sortable-row-main">
                      <span className="sortable-row-index">{idx + 1}.</span>
                      <input
                        style={{ width: 130 }}
                        placeholder={t('dstEndPoint.workPeriodsPlaceholder')}
                        value={p.start}
                        onChange={(e) => updatePeriod(idx, 'start', e.target.value)}
                      />
                      <span>→</span>
                      <input
                        style={{ width: 130 }}
                        placeholder={t('dstEndPoint.workPeriodsPlaceholder')}
                        value={p.end}
                        onChange={(e) => updatePeriod(idx, 'end', e.target.value)}
                      />
                    </span>
                    <span className="sortable-row-actions">
                      <button type="button" className="btn btn-sm btn-danger" onClick={() => removePeriod(idx)} disabled={periods.length <= 1}>
                        {t('dstEndPoint.workPeriodsRemove')}
                      </button>
                    </span>
                  </div>
                ))}
                <div>
                  <button type="button" className="btn btn-sm" onClick={addPeriod}>+ {t('dstEndPoint.workPeriodsAdd')}</button>
                </div>
                {periodsError ? <div style={{ color: '#e55', fontSize: 12 }}>{periodsError}</div> : null}
              </div>
            </div>
          </div>

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
