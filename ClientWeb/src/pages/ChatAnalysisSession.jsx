import { useEffect, useState } from 'react'
import { useI18n } from '../i18n'
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
  const { t } = useI18n()
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
    if (isAdmin && userName.trim() === '') { setError(t('chatAnalysisSession.selectUserFirst')); return }
    if (!mn) { setError(t('chatAnalysisSession.selectModelFirst')); return }
    setLoading(true); setError(''); setPage(1)
    try {
      const d = await post('ChatAnalysisSessionInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn, days,
      })
      setData(d.data || {})
    } catch (e) {
      setError(e.message || t('chatAnalysisSession.queryFailed'))
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
    { key: 'session_id', title: t('chatAnalysisSession.sessionId'), render: (v) => v || '-' },
    { key: 'start_time', title: t('chatAnalysisSession.startTime'), render: fmtTime },
    { key: 'end_time', title: t('chatAnalysisSession.endTime'), render: fmtTime },
    { key: 'duration_min', title: t('chatAnalysisSession.durationMin'), render: (v) => Number(v || 0).toFixed(1) },
    { key: 'request_count', title: t('chatAnalysisSession.requestCount'), render: fmtNum },
    { key: 'task_count', title: t('chatAnalysisSession.taskCount'), render: fmtNum },
    { key: 'tokens_input_size', title: t('chatAnalysis.inputTokens'), render: fmtNum },
    { key: 'tokens_output_size', title: t('chatAnalysis.outputTokens'), render: fmtNum },
    { key: 'tokens_all_size', title: t('chatAnalysis.totalTokens'), render: fmtNum },
    { key: 'models', title: t('chatAnalysis.model'), render: (v) => (v || []).join('、') || '-' },
    { key: 'is_stream', title: t('chatAnalysis.streaming'), render: (v) => (v ? t('common.yes') : t('common.no')) },
    { key: 'has_tool_call', title: t('chatAnalysis.toolCall'), render: (v) => (v ? t('common.yes') : t('common.no')) },
    { key: 'remote_addr', title: t('chatAnalysisSession.sourceAddr'), render: (v) => v || '-' },
    { key: 'actions', title: t('common.action'), render: (_, r) => (
      <button className="btn btn-sm" onClick={() => setDetail(r)}>{t('chatAnalysis.detail')}</button>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('chatAnalysisSession.title')}</h2>

      <div className="toolbar">
        {isAdmin ? <label>{t('userManage.username')}
          <select value={userName} onChange={(e) => { setUserName(e.target.value); setModelName('') }} style={{ width: 150 }}>
            <option value="">{t('userManage.selectUser')}</option>
            {userOptions.map((u) => <option key={u.user_name} value={u.user_name}>{u.user_name}</option>)}
          </select>
        </label> : null}
        <label>{t('userManage.modelName')}
          <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
            <option value="">{t('chatDialog.selectModel')}</option>
            {modelOptions.map((m) => <option key={m} value={m}>{m}</option>)}
          </select>
        </label>
        <label>{t('chatAnalysisSession.timeRange')}
          <select value={days} onChange={(e) => setDays(Number(e.target.value))}>
            {DAYS_OPTIONS.map((d) => <option key={d} value={d}>{d === 0 ? t('chatAnalysisSession.allTime') : t('chatAnalysisSession.lastNDays', { days: d })}</option>)}
          </select>
        </label>
        <button className="btn btn-primary" onClick={doQuery} disabled={loading}>{t('common.search')}</button>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      {data ? (
        <div className="card-grid kpi-grid">
          <div className="card"><h3>{t('chatAnalysisSession.totalSessions')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_sessions)}</div></div>
          <div className="card"><h3>{t('chatAnalysisSession.totalTasks')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_tasks)}</div></div>
          <div className="card"><h3>{t('chatAnalysisSession.totalRequests')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_requests)}</div></div>
          <div className="card"><h3>{t('chatAnalysisSession.avgSessionDuration')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{Number(data.avg_duration_min || 0).toFixed(1)} {t('chatAnalysisSession.minutes')}</div></div>
          <div className="card"><h3>{t('chatAnalysisSession.avgTasksPerSession')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{Number(data.avg_tasks_per_session || 0).toFixed(2)}</div></div>
        </div>
      ) : null}

      <DataTable columns={columns} rows={pageRows} loading={loading} rowKey="session_id"
                 empty={t('chatAnalysisSession.noData')} />

      {sessions.length > 0 ? (
        <div className="pager">
          <span>{t('toolbar.pagination', { count: fmtNum(sessions.length), page, totalPages })}</span>
          <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('datatable.previous')}</button>
          <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>{t('datatable.next')}</button>
        </div>
      ) : null}

      {detail ? (
        <Modal title={`${t('chatAnalysisSession.sessionDetail')} ${detail.session_id || ''}`} onClose={() => setDetail(null)}>
          <dl className="kv">
            <dt>{t('chatAnalysisSession.sessionId')}</dt><dd>{detail.session_id || '-'}</dd>
            <dt>{t('chatAnalysisSession.timeRange')}</dt><dd>{fmtTime(detail.start_time)} ~ {fmtTime(detail.end_time)}（{Number(detail.duration_min || 0).toFixed(1)} {t('chatAnalysisSession.minutes')}）</dd>
            <dt>{t('chatAnalysisSession.requestsAndTasks')}</dt><dd>{fmtNum(detail.request_count)} / {fmtNum(detail.task_count)}</dd>
            <dt>{t('chatAnalysisSession.avgDuration')}</dt><dd>{fmtMs(detail.avg_elapsed_ms)}（{t('common.total')} {fmtMs(detail.total_elapsed_ms)}）</dd>
            <dt>{t('chatAnalysisSession.traffic')}</dt><dd>{t('chatAnalysis.request')} {fmtBytes(detail.total_req_size)} / {t('chatAnalysis.response')} {fmtBytes(detail.total_resp_size)}</dd>
            <dt>{t('chatAnalysis.tokens')}</dt><dd>{t('chatAnalysis.inputTokens')} {fmtNum(detail.tokens_input_size)} / {t('chatAnalysis.outputTokens')} {fmtNum(detail.tokens_output_size)} / {t('chatAnalysis.totalTokens')} {fmtNum(detail.tokens_all_size)}</dd>
            <dt>{t('chatAnalysisSession.usedModels')}</dt><dd>{(detail.models || []).join('、') || '-'}</dd>
            <dt>{t('chatAnalysisSession.features')}</dt><dd>{detail.has_system_prompt ? t('chatAnalysisSession.hasSystemPrompt') + ' ' : ''}{detail.has_tool_call ? t('chatAnalysisSession.hasToolCall') + ' ' : ''}{detail.is_stream ? t('chatAnalysis.streaming') : t('chatAnalysisSession.nonStreaming')}</dd>
            <dt>{t('chatAnalysisSession.sourceAddr')}</dt><dd>{detail.remote_addr || '-'}</dd>
            <dt>{t('chatAnalysisSession.firstUrl')}</dt><dd>{detail.first_url || '-'}</dd>
            <dt>{t('chatAnalysisSession.lastUrl')}</dt><dd>{detail.last_url || '-'}</dd>
            <dt>{t('chatAnalysisSession.recordRange')}</dt><dd>#{detail.first_record_id} ~ #{detail.last_record_id}</dd>
          </dl>
        </Modal>
      ) : null}
    </div>
  )
}
