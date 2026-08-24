import { useEffect, useState } from 'react'
import { get, post } from '../shared/api'

// 首页：登录用户信息 + 模型列表（UserModelListInterface，POST）
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
      <div className="card-grid">
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
          <h3>可用模型（{models.length}）</h3>
          {models.length
            ? <ul className="tag-list">{models.map((m, i) => <li key={i} className="tag">{typeof m === 'string' ? m : (m.model_name || m.name || JSON.stringify(m))}</li>)}</ul>
            : <div className="table-empty">暂无模型</div>}
        </div>
      </div>
    </div>
  )
}
