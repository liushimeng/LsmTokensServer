import { useEffect, useState } from 'react'
import { useI18n } from '../i18n'
import { post } from '../shared/api'
import { isAdminRole } from '../shared/auth'
import { useUserModelOptions, useMyModelNames, modelNamesOf, allModelNames } from '../shared/userModelOptions'
import DataTable from '../components/DataTable'
import Modal from '../components/Modal'
import TimeRangeSelector from '../components/TimeRangeSelector'
import { useTimeSpanLevels } from '../shared/useTimeSpanLevels'
import { nearestSpan } from '../shared/timeSpan'
import { fmtTime, fmtNum, fmtBytes, fmtMs, pickRouteQuery } from '../shared/format'

// 任务/工具调用分析页（/ChatAnalysisTaskInterface）
// 按 Task 维度（is_parsed 预解析特征）聚合：任务数 / 模型分布 / 流式占比等 + 任务明细表
// 20260826：时间跨度为动态档位（1 小时 ~ transactionRetentionDays+1 天，统一 span 编码）
const PAGE_SIZE = 20

export default function ChatAnalysisTask({ route }) {
  const { t } = useI18n()
  const init = pickRouteQuery(route && route.query)
  const isAdmin = isAdminRole() // 用户端：服务端强制 claims.UserName
  const [userName, setUserName] = useState(isAdmin ? init.userName : '')
  const [modelName, setModelName] = useState(init.modelName)
  const { levels, loading: levelsLoading } = useTimeSpanLevels()
  const [days, setDays] = useState(null) // 档位加载后初始化（默认就近 3 天档）
  const [data, setData] = useState(null) // TaskAnalysisResult
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [page, setPage] = useState(1)
  const [detail, setDetail] = useState(null) // 任务详情弹窗
  // 用户名/模型名级联下拉：管理端用 UserModelOptionsInterface（页面生命周期内缓存一次），用户端用本人模型列表
  const { users: userOptions } = useUserModelOptions()
  const { modelNames: myModelNames } = useMyModelNames()
  const modelOptions = isAdmin
    ? (userName.trim() ? modelNamesOf(userOptions, userName.trim()) : allModelNames(userOptions))
    : myModelNames

  const hasKey = (isAdmin ? userName.trim() !== '' : true) && modelName.trim() !== ''

  const doQuery = async (modelOverride) => {
    const mn = (modelOverride !== undefined ? modelOverride : modelName).trim()
    if (isAdmin && userName.trim() === '') { setError(t('chatAnalysisTask.selectUserFirst')); return }
    if (!mn) { setError(t('chatAnalysisTask.selectModelFirst')); return }
    setLoading(true); setError(''); setPage(1)
    try {
      const d = await post('ChatAnalysisTaskInterface', {
        user_name: isAdmin ? userName.trim() : '', model_name: mn, days: days ?? 3,
      })
      setData(d.data || {})
    } catch (e) {
      setError(e.message || t('chatAnalysisTask.queryFailed'))
    } finally { setLoading(false) }
  }

  // 动态档位到达后初始化 span（默认 3 天就近档）
  useEffect(() => {
    if (!levels.length || days !== null) return
    setDays(nearestSpan(levels, 3))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [levels])

  // 路由带参进入时自动查询（等待档位就绪）
  useEffect(() => {
    if (days === null) return
    if (init.userName && init.modelName) { doQuery(); return }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [days])

  // 用户端进入页面：本人模型列表（缓存一次）到达后自动选第一个并查询
  useEffect(() => {
    if (days === null) return
    if (isAdmin || !myModelNames.length || modelName) return
    setModelName(myModelNames[0])
    doQuery(myModelNames[0])
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [myModelNames, days])

  const tasks = (data && data.tasks) || []
  const totalPages = Math.max(1, Math.ceil(tasks.length / PAGE_SIZE))
  const pageRows = tasks.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE)

  const columns = [
    { key: 'id', title: t('chatAnalysisTask.id'), width: 90 },
    { key: 'created_at', title: t('chatAnalysisTask.time'), render: (v) => fmtTime(v) },
    { key: 'model', title: t('chatAnalysisTask.taskModel'), render: (v) => v || '-' },
    { key: 'request_method', title: t('chatAnalysisTask.method'), width: 70 },
    { key: 'request_url', title: t('chatAnalysisTask.url'), render: (v) => <span title={v}>{String(v || '').length > 50 ? String(v).slice(0, 50) + '…' : v}</span> },
    { key: 'response_status', title: t('common.status'), render: (v) => {
      const ok = String(v).startsWith('2')
      return <span style={{ color: ok ? 'var(--ok)' : 'var(--danger)' }}>{v || '-'}</span>
    } },
    { key: 'message_count', title: t('chatAnalysisTask.messageCount'), render: fmtNum },
    { key: 'user_message_count', title: t('chatAnalysisTask.userMessages'), render: fmtNum },
    { key: 'req_size', title: t('chatAnalysisTask.reqSize'), render: fmtBytes },
    { key: 'resp_size', title: t('chatAnalysisTask.respSize'), render: fmtBytes },
    { key: 'elapsed_ms', title: t('chatAnalysisTask.duration'), render: fmtMs },
    { key: 'stream', title: t('chatAnalysis.streaming'), render: (v) => (v ? t('common.yes') : t('common.no')) },
    { key: 'has_tool_call', title: t('chatAnalysis.toolCall'), render: (v) => (v ? t('common.yes') : t('common.no')) },
    { key: 'session_id', title: t('chatAnalysisTask.belongedSession'), render: (v) => v || '-' },
    { key: 'actions', title: t('common.action'), render: (_, r) => (
      <button className="btn btn-sm" onClick={() => setDetail(r)}>{t('chatAnalysis.detail')}</button>
    ) },
  ]

  return (
    <div className="page">
      <h2 className="page-title">{t('chatAnalysisTask.title')}</h2>

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
        <label>{t('chatAnalysisTask.timeRange')}
          <TimeRangeSelector span={days ?? 3} onChange={setDays} levels={levels} loading={levelsLoading} />
        </label>
        <button className="btn btn-primary" onClick={doQuery} disabled={loading}>{t('common.search')}</button>
      </div>

      {error ? <div className="alert alert-error">{error}</div> : null}

      {data ? (
        <div className="card-grid kpi-grid">
          <div className="card"><h3>{t('chatAnalysisTask.totalTasks')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.total_tasks)}</div></div>
          <div className="card"><h3>{t('chatAnalysisTask.streamAndNonStream')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.stream_count)} / {fmtNum(data.non_stream_count)}</div></div>
          <div className="card"><h3>{t('chatAnalysisTask.hasSystemPrompt')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.has_system_prompt)}</div></div>
          <div className="card"><h3>{t('chatAnalysisTask.hasToolCall')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtNum(data.has_tool_call)}</div></div>
          <div className="card"><h3>{t('chatAnalysisTask.avgDuration')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{fmtMs(data.avg_elapsed_ms)}</div></div>
          <div className="card"><h3>{t('chatAnalysisTask.avgMessageCount')}</h3><div style={{ fontSize: 24, fontWeight: 700 }}>{Number(data.avg_messages || 0).toFixed(2)}</div></div>
        </div>
      ) : null}

      {data ? (
        <div className="card">
          <h3>{t('chatAnalysisTask.taskModelDistribution')}</h3>
          <DataTable
            columns={[
              { key: 'k', title: t('chatAnalysis.model') },
              { key: 'v', title: t('chatAnalysisTask.tasks'), render: fmtNum },
            ]}
            rows={Object.entries(data.model_stats || {}).map(([k, v]) => ({ k, v }))}
            empty={t('datatable.noData')} />
        </div>
      ) : null}

      <DataTable columns={columns} rows={pageRows} loading={loading} rowKey="id"
                 empty={t('chatAnalysisTask.noData')} />

      {tasks.length > 0 ? (
        <div className="pager">
          <span>{t('toolbar.pagination', { count: fmtNum(tasks.length), page, totalPages })}</span>
          <button className="btn btn-sm" disabled={page <= 1} onClick={() => setPage(page - 1)}>{t('datatable.previous')}</button>
          <button className="btn btn-sm" disabled={page >= totalPages} onClick={() => setPage(page + 1)}>{t('datatable.next')}</button>
        </div>
      ) : null}

      {detail ? (
        <Modal title={`${t('chatAnalysisTask.taskDetail')} #${detail.id}`} onClose={() => setDetail(null)}>
          <dl className="kv">
            <dt>{t('chatAnalysisTask.time')}</dt><dd>{fmtTime(detail.created_at)}</dd>
            <dt>{t('chatAnalysisTask.taskModel')}</dt><dd>{detail.model || '-'}</dd>
            <dt>{t('chatAnalysisTask.request')}</dt><dd>{detail.request_method} {detail.request_url}</dd>
            <dt>{t('common.status')}</dt><dd>{detail.response_status || '-'}（{t('chatAnalysisTask.duration')} {fmtMs(detail.elapsed_ms)}）</dd>
            <dt>{t('chatAnalysisTask.messageCount')}</dt><dd>{t('common.total')} {fmtNum(detail.message_count)} / {t('chatAnalysisTask.user')} {fmtNum(detail.user_message_count)}</dd>
            <dt>{t('chatAnalysisTask.size')}</dt><dd>{t('chatAnalysisTask.request')} {fmtBytes(detail.req_size)} / {t('chatAnalysis.response')} {fmtBytes(detail.resp_size)}</dd>
            <dt>{t('chatAnalysisTask.features')}</dt><dd>{detail.has_system_prompt ? t('chatAnalysisTask.hasSystemPrompt') + ' ' : ''}{detail.has_tool_call ? t('chatAnalysisTask.hasToolCall') + ' ' : ''}{detail.stream ? t('chatAnalysisTask.streaming') : t('chatAnalysisTask.nonStreaming')}</dd>
            <dt>{t('chatAnalysisTask.sourceAddr')}</dt><dd>{detail.remote_addr || '-'}</dd>
            <dt>{t('chatAnalysisTask.belongedSession')}</dt><dd>{detail.session_id || '-'}（#{detail.session_first_record_id} ~ #{detail.session_last_record_id}，{t('common.total')} {fmtNum(detail.session_record_count)} {t('chatAnalysisTask.records')}）</dd>
          </dl>
        </Modal>
      ) : null}
    </div>
  )
}
