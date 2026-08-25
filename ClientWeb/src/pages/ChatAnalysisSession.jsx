import { useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import { useUserModelOptions, useMyModelNames, modelNamesOf, allModelNames } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { fmtTime, fmtNum, fmtBytes, fmtMs, pickRouteQuery } from '../shared/format'

// 会话分析页（/ChatAnalysisSessionInterface）
// 按 session 维度聚合：会话数 / 任务数 / 请求数 / 平均时长等 + 会话明细表
const DAYS_OPTIONS = [0, 1, 3, 5, 7, 14, 30, 60, 90]
const PAGE_SIZE = 20

export default function ChatAnalysisSession({ route }) {
  const init = pickRouteQuery(route && route.query)
  const isAdmin = isAdminRole() // 用户端：服务端强制 claims.UserName
  const [userName, setUserName] = useState(isAdmin ? init.userName : '')
  const [modelName, setModelName] = useState(init.modelName)
  const [days, setDays] = useState(3)
  const [data, setData] = useState(null) // SessionAnalysisResult
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1)
  const [detail, setDetail] = useState(null) // 会话详情弹窗
  // 用户名/模型名级联下拉：管理端用 UserModelOptionsInterface（页面生命周期内缓存一次），用户端用本人模型列表
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()
  const modelOptions = isAdmin
    ? (userName.trim() ? modelNamesOf(userOptions, userName.trim()) : allModelNames(userOptions))
    : myModelNames

  const hasKey = (isAdmin ? userName.trim() !== '' : true) && modelName.trim() !== ''

  const doQuery = async (modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (isAdmin && userName.trim() === '') { setError('请先选择用户'); return }
    if (!mn) { setError('请先选择模型'); return }
    setLoading(true); setError(''); setPage(1)
    try {
      const d = await post('ChatAnalysisSessionInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn, days,
      })
      setData(d.data || {})
    } catch (e) {
      setError(e.message || '查询失败')
    } finally { setLoading(false) }
  }

  // 路由带参进入时自动查询
  useEffect(() => {
    if (init.userName && init.modelName) { doQuery(); return }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  // 用户端进入页面：本人模型列表（缓存一次）到达后自动选第一个并查询
  useEffect(() => {
    if (isAdmin || !myModelNames.length || modelName) return
    setModelName(myModelNames[0])
    doQuery(myModelNames[0])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [myModelNames])

  const sessions = (data && data.sessions) || []
  const totalPages = Math.max(1, Math.ceil(sessions.length / PAGE_SIZE))
  const pageRows = sessions.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)

  const columns = [
    { key: 'session_id', title: '会话 ID', render: (v) => v || '-' },
    { key: 'start_time', title: '开始时间', render: fmtTime },
    { key: 'end_time', title: '结束时间', render: fmtTime },
    { key: 'duration_min', title: '时长(分)', render: (v) => Number(v || 0).toFixed(1) },
    { key: 'request_count', title: '请求数', render: fmtNum },
    { key: 'task_count', title: '任务数', render: fmtNum },
    { key: 'tokens_input_size', title: '输入 Tokens', render: fmtNum },
    { key: 'tokens_output_size', title: '输出 Tokens', render: fmtNum },
    { key: 'tokens_all_size', title: '总 Tokens', render: fmtNum },
    { key: 'models', title: '模型', render: (v) => (v || []).join('、') || '-' },
    { key: 'is_stream', title: '流式', render: (v) => (v ? '是' : '否') },
    { key: 'has_tool_call', title: '工具调用', render: (v) => (v ? '有' : '无') },
    { key: 'remote_addr', title: '来源地址', render: (v) => v || '-' },
    { key: 'actions', title: '操作', render: (_, r) => (
      <button className="btn btn-sm" onClick={() => setDetail(r)}>详情</button>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">会话分析</h2>

      <div className="toolbar">
        {isAdmin ? <label>用户名
          <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 150 }}>
            <option value="">请选择用户</option>
            {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
          </select>
        </label> : null}
        <label>模型名
          <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
            <option value="">请选择模型</option>
            {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>时间跨度
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{d === 0 ? '全部时间' : `最近${d}天`}</option>)}
          </select>
        </label>
        <button className="btn btn-primary" onClick={doQuery} disabled={loading}>查询</button>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      {data ? (
        <div className="card-grid kpi-grid">
          <div className="card"><h3>总会话数</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_sessions)}</div></div>
          <div className="card"><h3>总任务数</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_tasks)}</div></div>
          <div className="card"><h3>总请求数</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_requests)}</div></div>
          <div className="card"><h3>平均会话时长</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{Number(data.avg_duration_min || 0).toFixed(1)} 分钟</div></div>
          <div className="card"><h3>平均任务数/会话</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{Number(data.avg_tasks_per_session || 0).toFixed(2)}</div></div>
        </div>
      ) : null}

      <DataTable columns={columns} rows={pageRows} loading={loading} rowKey="session_id"
                 empty="暂无数据（需选择用户名 + 模型名后查询）" />

      {sessions.length > 0 ? (
        <div className="pager">
          <span>共 {fmtNum(sessions.length)} 条 · 第 {page} / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
          <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>下一页</button>
        </div>
      ) : null}

      {detail ? (
        <Modal title={`会话详情 ${detail.session_id || ''}`} onClose={() => setDetail(null)}>
          <dl className="kv">
            <dt>会话 ID</dt><dd>{detail.session_id || '-'}</dd>
            <dt>起止时间</dt><dd>{fmtTime(detail.start_time)} ~ {fmtTime(detail.end_time)}（{Number(detail.duration_min || 0).toFixed(1)} 分钟）</dd>
            <dt>请求数 / 任务数</dt><dd>{fmtNum(detail.request_count)} / {fmtNum(detail.task_count)}</dd>
            <dt>平均耗时</dt><dd>{fmtMs(detail.avg_elapsed_ms)}（总 {fmtMs(detail.total_elapsed_ms)}）</dd>
            <dt>流量</dt><dd>请求 {fmtBytes(detail.total_req_size)} / 响应 {fmtBytes(detail.total_resp_size)}</dd>
            <dt>Tokens</dt><dd>输入 Tokens {fmtNum(detail.tokens_input_size)} / 输出 Tokens {fmtNum(detail.tokens_output_size)} / 总 Tokens {fmtNum(detail.tokens_all_size)}</dd>
            <dt>使用模型</dt><dd>{(detail.models || []).join('、') || '-'}</dd>
            <dt>特征</dt><dd>{detail.has_system_prompt ? '含系统提示词 ' : ''}{detail.has_tool_call ? '含工具调用 ' : ''}{detail.is_stream ? '流式' : '非流式'}</dd>
            <dt>来源地址</dt><dd>{detail.remote_addr || '-'}</dd>
            <dt>首条 URL</dt><dd>{detail.first_url || '-'}</dd>
            <dt>末条 URL</dt><dd>{detail.last_url || '-'}</dd>
            <dt>记录范围</dt><dd>#{detail.first_record_id} ~ #{detail.last_record_id}</dd>
          </dl>
        </Modal>
      ) : null}
    </div>
  )
}
