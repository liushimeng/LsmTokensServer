import { useEffect, useState } from 'react'
import { get, post } from '../shared/api'

// 首页：登录用户信息 + 我的模型列表卡片（对齐旧版用户端首页）
// 每个模型卡片展示 API Key 前 8 位打码，并提供 6 个快捷跳转
const maskKey = (k) => {
  const s = String(k || '')
  return s ? s.substring(0, 8) + '****' : '（无 Key）'
}

export default function Home() {
  const [info, setInfo] = useState(null)
  const [models, setModels] = useState([])
  const [error, setError] = useState('')

  useEffect(() => {
    get('UserInfoInterface')
      .then((d) => setInfo((d && d.data) || d))
      .catch((e) => setError(e.message))
    post('UserModelListInterface', {})
      .then((d) => setModels((d && (d.data || d.models)) || []))
      .catch(() => {})
  }, [])

  return (
    <div className="page">
      <h2 className="page-title">首页</h2>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card-grid kpi-grid">
        <div className="card">
          <h3>登录信息</h3>
          {info ? (
            <dl className="kv">
              <dt>用户</dt><dd>{info.user_name}</dd>
              <dt>模型</dt><dd>{info.model_name || '-'}</dd>
              <dt>登录方式</dt><dd>{info.login_type || '-'}</dd>
            </dl>
          ) : <div className="table-loading">加载中…</div>}
        </div>
        <div className="card">
          <h3>我的模型（{models.length}）</h3>
          {models.length
            ? <ul className="tag-list">{models.map((m, i) => <li key={i} className="tag">{m.model_name || m.name || ''}</li>)}</ul>
            : <div className="table-empty">暂无模型</div>}
        </div>
      </div>

      {models.length ? (
        <div className="card-grid kpi-grid" style={{ marginTop: 16 }}>
          {models.map((m, i) => {
            const mn = m.model_name || m.name || ''
            return (
              <div className="card" key={i}>
                <h3 title={mn}>{mn || '未命名模型'}</h3>
                <div style={{ fontFamily: 'monospace', fontSize: 13, color: '#64748b', margin: '6px 0 10px' }}>
                  Key: {maskKey(m.api_key || m.key)}
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  <a className="btn btn-sm" href={`#/ChatAnalysis?model_name=${encodeURIComponent(mn)}`}>浏览记录</a>
                  <a className="btn btn-sm" href={`#/ChatAnalysisTotal?model_name=${encodeURIComponent(mn)}`}>统计</a>
                  <a className="btn btn-sm" href={`#/ChatAnalysisSession?model_name=${encodeURIComponent(mn)}`}>Session</a>
                  <a className="btn btn-sm" href={`#/ChatAnalysisTask?model_name=${encodeURIComponent(mn)}`}>Task</a>
                  <a className="btn btn-sm" href={`#/ChatDialog?model_name=${encodeURIComponent(mn)}`}>对话</a>
                  <a className="btn btn-sm" href="#/AIRouteManage">智能路由</a>
                </div>
              </div>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}
