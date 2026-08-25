import { useEffect, useState } from 'react'
import { post } from '../shared/api'
import { isAdminRole, fetchMyModels } from '../shared/auth'
import { useUserModelOptions, modelNamesOf, allModelNames } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import { fmtTime, fmtNum, fmtBytes, fmtMs, pickRouteQuery } from '../shared/format'

// 任务/工具调用分析页（/ChatAnalysisTaskInterface）
// 按 Task 维度（is_parsed 预解析特征）聚合：任务数 / 模型分布 / 流式占比等 + 任务明细表
const DAYS_OPTIONS = [0, 1, 3, 5, 7, 14, 30, 60, 90]
const PAGE_SIZE = 20

export default function ChatAnalysisTask({ route }) {
  const init = pickRouteQuery(route && route.query)
  const isAdmin = isAdminRole() // 用户端：服务端强制 claims.UserName
  const [userName, setUserName] = useState(isAdmin ? init.userName : '')
  const [modelName, setModelName] = useState(init.modelName)
  const [days, setDays] = useState(3)
  const [data, setData] = useState(null) // TaskAnalysisResult
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1)
  const [detail, setDetail] = useState(null) // 任务详情弹窗
  // 用户名/模型名级联下拉：管理端用 UserModelOptionsInterface（页面生命周期内缓存一次），用户端用本人模型列表
  const { users: userOptions } = useUserModelOptions()
  const [myModels, setMyModels] = useState([])
  const modelOptions = isAdmin
    ? (userName.trim() ? modelNamesOf(userOptions, userName.trim()) : allModelNames(userOptions))
    : myModels.map((m) => m.model_name).filter(Boolean)

  const hasKey = (isAdmin ? userName.trim() !== '' : true) && modelName.trim() !== ''

  const doQuery = async (modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (isAdmin && userName.trim() === '') { setError('请先填写用户名'); return }
    if (!mn) { setError('请先填写模型名'); return }
    setLoading(true); setError(''); setPage(1)
    try {
      const d = await post('ChatAnalysisTaskInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn, days,
      })
      setData(d.data || {})
    } catch (e) {
      setError(e.message || '分析失败')
    } finally { setLoading(false) }
  }

  useEffect(() => {
    if (init.userName && init.modelName) { doQuery(); return }
    // 用户端进入页面：自动取本人第一个模型并查询（对齐旧版重定向逻辑）
    if (!isAdmin) {
      fetchMyModels()
        .then((ms) => {
          setMyModels(ms || [])
          const first = ms && ms[0]
          if (!first) return
          setModelName(first.model_name || '')
          doQuery(first.model_name || '')
        })
        .catch(() => {})
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [])

  const tasks = (data && data.tasks) || []
  const totalPages = Math.max(1, Math.ceil(tasks.length / PAGE_SIZE))
  const pageRows = tasks.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)

  const columns = [
    { key: 'id', title: 'ID', width: 90 },
    { key: 'created_at', title: '时间', render: (v) => fmtTime(v) },
    { key: 'model', title: '任务模型', render: (v) => v || '-' },
    { key: 'request_method', title: '方法', width: 70 },
    { key: 'request_url', title: 'URL', render: (v) => <span title={v}>{String(v || '').length > 50 ? String(v).slice(0, 50) + '…' : v}</span> },
    { key: 'response_status', title: '状态', render: (v) => {
      const ok = String(v).startsWith('2')
      return <span style={{ color: ok ? 'var(--ok)' : 'var(--danger)' }}>{v || '-'}</span>
    } },
    { key: 'message_count', title: '消息数', render: fmtNum },
    { key: 'user_message_count', title: '用户消息', render: fmtNum },
    { key: 'req_size', title: '请求大小', render: fmtBytes },
    { key: 'resp_size', title: '响应大小', render: fmtBytes },
    { key: 'elapsed_ms', title: '耗时', render: fmtMs },
    { key: 'stream', title: '流式', render: (v) => (v ? '是' : '否') },
    { key: 'has_tool_call', title: '工具调用', render: (v) => (v ? '有' : '无') },
    { key: 'session_id', title: '所属会话', render: (v) => v || '-' },
    { key: 'actions', title: '操作', render: (_, r) => (
      <button className="btn btn-sm" onClick={() => setDetail(r)}>详情</button>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">任务 / 工具调用分析</h2>

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
            {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{d === 0 ? '全部时间' : `最近 ${d} 天`}</option>)}
          </select>
        </label>
        <button className="btn btn-primary" onClick={doQuery} disabled={loading}>分析</button>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      {data ? (
        <div className="card-grid kpi-grid">
          <div className="card"><h3>总任务数</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_tasks)}</div></div>
          <div className="card"><h3>流式 / 非流式</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.stream_count)} / {fmtNum(data.non_stream_count)}</div></div>
          <div className="card"><h3>含系统提示词</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.has_system_prompt)}</div></div>
          <div className="card"><h3>含工具调用</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.has_tool_call)}</div></div>
          <div className="card"><h3>平均耗时</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtMs(data.avg_elapsed_ms)}</div></div>
          <div className="card"><h3>平均消息数</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{Number(data.avg_messages || 0).toFixed(2)}</div></div>
        </div>
      ) : null}

      {data ? (
        <div className="card">
          <h3>任务模型分布</h3>
          <DataTable
            columns={[
              { key: 'k', title: '模型' },
              { key: 'v', title: '任务数', render: fmtNum },
            ]}
            rows={Object.entries(data.model_stats || {}).map(([k, v]) => ({ k, v }))}
            empty="暂无数据" />
        </div>
      ) : null}

      <DataTable columns={columns} rows={pageRows} loading={loading} rowKey="id"
                 empty="暂无任务数据（需填写用户名 + 模型名后分析）" />

      {tasks.length > 0 ? (
        <div className="pager">
          <span>共 {fmtNum(tasks.length)} 个任务 / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>上一页</button>
          <span>第 {page} / {totalPages} 页</span>
          <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>下一页</button>
        </div>
      ) : null}

      {detail ? (
        <Modal title={`任务详情 #${detail.id}`} onClose={() => setDetail(null)}>
          <dl className="kv">
            <dt>时间</dt><dd>{fmtTime(detail.created_at)}</dd>
            <dt>任务模型</dt><dd>{detail.model || '-'}</dd>
            <dt>请求</dt><dd>{detail.request_method} {detail.request_url}</dd>
            <dt>状态</dt><dd>{detail.response_status || '-'}（耗时 {fmtMs(detail.elapsed_ms)}）</dd>
            <dt>消息数</dt><dd>总 {fmtNum(detail.message_count)} / 用户 {fmtNum(detail.user_message_count)}</dd>
            <dt>大小</dt><dd>请求 {fmtBytes(detail.req_size)} / 响应 {fmtBytes(detail.resp_size)}</dd>
            <dt>特征</dt><dd>{detail.has_system_prompt ? '含系统提示词 ' : ''}{detail.has_tool_call ? '含工具调用 ' : ''}{detail.stream ? '流式' : '非流式'}</dd>
            <dt>来源地址</dt><dd>{detail.remote_addr || '-'}</dd>
            <dt>所属会话</dt><dd>{detail.session_id || '-'}（#{detail.session_first_record_id} ~ #{detail.session_last_record_id}，共 {fmtNum(detail.session_record_count)} 条）</dd>
          </dl>
        </Modal>
      ) : null}
    </div>
  )
}
