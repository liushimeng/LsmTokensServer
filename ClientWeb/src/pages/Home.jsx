import { useEffect, useRef, useState } from 'react'
import { get, post } from '../shared/api'
import { useI18n } from '../i18n'

// 首页：登录用户信息 + 我的模型列表卡片（对齐旧版用户端首页）
// 每个模型卡片展示 API Key 前 8 位打码，并提供 6 个快捷跳转
const maskKey = (k) => {
  const s = String(k || '')
  return s ? s.substring(0, 8) + '****' : '（无 Key）'
}

export default function Home() {
  const { t } = useI18n()
  const [info, setInfo] = useState(null)
  const [models, setModels] = useState([])
  const [error, setError] = useState('')
  const [infoLoaded, setInfoLoaded] = useState(false)
  const aliveRef = useRef(true)

  useEffect(() => {
    aliveRef.current = true
    get('UserInfoInterface')
      .then((d) => {
        if (!aliveRef.current) return
        const info = (d && d.data) || d
        if (!info || !info.user_name) {
          // 兜底：接口返回空数据（历史 302 重定向残留场景）→ 视为登录失效，自动跳登录页
          setError(t('errors.unauthorized'))
          setTimeout(() => { window.location.hash = '#/Login'; window.location.reload() }, 800)
          return
        }
        setInfo(info)
      })
      .catch((e) => {
        if (aliveRef.current) {
          // 区分超时/网络错误，给出更友好的提示
          const msg = e.message || ''
          if (/超时|重启/.test(msg)) {
            setError(t('common.timeout'))
          } else if (/网络错误/.test(msg)) {
            setError(t('common.networkError'))
          } else {
            setError(msg)
          }
        }
      })
      .finally(() => { if (aliveRef.current) setInfoLoaded(true) })
    post('UserModelListInterface', {})
      .then((d) => { if (aliveRef.current) setModels((d && (d.data || d.models)) || []) })
      // 阶段AY：失败时给出提示而非静默吞错（用户模型列表为空但 UI 完全无反馈）
      .catch((e) => { if (aliveRef.current) setError(e.message || t('home.loadModelsFailed')) })
    return () => { aliveRef.current = false }
  }, [])

  return (
    <div className="page">
      <h2 className="page-title">{t('nav.home')}</h2>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <div className="card-grid kpi-grid">
        <div className="card">
          <h3>{t('home.loginInfo')}</h3>
          {info ? (
            <dl className="kv">
              <dt>{t('common.user')}</dt><dd>{info.user_name}</dd>
              <dt>{t('home.currentModel')}</dt><dd>{info.model_name || '-'}</dd>
              <dt>{t('common.type')}</dt><dd>{info.login_type || '-'}</dd>
            </dl>
          ) : infoLoaded ? (
            <div className="table-empty">{t('errors.fetchFailed')}{error ? `：${error}` : ''}</div>
          ) : (
            <div className="table-loading">{t('common.loading')}</div>
          )}
        </div>
        <div className="card">
          <h3>{t('home.recentModels')}（{models.length}）</h3>
          {models.length
            ? <ul className="tag-list">{models.map((m, i) => <li key={i} className="tag">{m.model_name || m.name || ''}</li>)}</ul>
            : <div className="table-empty">{t('home.noData')}</div>}
        </div>
      </div>

      {models.length ? (
        <div className="card-grid kpi-grid" style={{ marginTop: 16 }}>
          {models.map((m, i) => {
            const mn = m.model_name || m.name || ''
            return (
              <div className="card" key={i}>
                <h3 title={mn}>{mn || t('home.noData')}</h3>
                <div style={{ fontFamily: 'monospace', fontSize: 13, color: '#64748b', margin: '6px 0 10px' }}>
                  Key: {maskKey(m.api_key || m.key)}
                </div>
                <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6 }}>
                  <a className="btn btn-sm" href={`#/ChatAnalysis?model_name=${encodeURIComponent(mn)}`}>{t('nav.chatAnalysis')}</a>
                  <a className="btn btn-sm" href={`#/ChatAnalysisTotal?model_name=${encodeURIComponent(mn)}`}>{t('nav.chatAnalysisTotal')}</a>
                  <a className="btn btn-sm" href={`#/ChatAnalysisSession?model_name=${encodeURIComponent(mn)}`}>{t('nav.chatAnalysisSession')}</a>
                  <a className="btn btn-sm" href={`#/ChatAnalysisTask?model_name=${encodeURIComponent(mn)}`}>{t('nav.chatAnalysisTask')}</a>
                  <a className="btn btn-sm" href={`#/ChatDialog?model_name=${encodeURIComponent(mn)}`}>{t('nav.chatDialog')}</a>
                  <a className="btn btn-sm" href="#/AIRouteManage">{t('nav.aiRouteManage')}</a>
                </div>
              </div>
            )
          })}
        </div>
      ) : null}
    </div>
  )
}
