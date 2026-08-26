import { useEffect, useRef, useState } from 'react'
import { get, post } from '../shared/api'
import { saveCredentials, loadCredentials, clearCredentials } from '../shared/auth'

// 登录页：模型登录（model_name + api_key）+ 验证码，与旧 /UserLogin 表单等价
export default function Login() {
  const [captchaId, setCaptchaId] = useState('')
  const [captchaUrl, setCaptchaUrl] = useState('')
  const [modelName, setModelName] = useState('')
  const [apiKey, setApiKey] = useState('')
  const [captchaCode, setCaptchaCode] = useState('')
  const [remember, setRemember] = useState(false)
  const [error, setError] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [busy, setBusy] = useState(false)
  // 阶段AO：组件卸载后 setState 静默丢弃，避免 React StrictMode 双调用或路由切换时的
  // "Can't perform a React state update on an unmounted component" warning。
  const aliveRef = useRef(true)

  const refreshCaptcha = async () => {
    try {
      const d = await get('CaptchaGenerate')
      if (!aliveRef.current) return
      if (d.success) { setCaptchaId(d.captcha_id); setCaptchaUrl(d.image_url) }
    } catch { /* 忽略 */ }
  }

  useEffect(() => {
    aliveRef.current = true
    refreshCaptcha()
    // v2 安全加固：记住我只回填模型名称，API Key 不再本地存储
    const creds = loadCredentials()
    if (creds && creds.modelName) {
      setModelName(creds.modelName); setRemember(true)
    }
    return () => { aliveRef.current = false }
  }, [])

  const submit = async (e) => {
    e.preventDefault()
    setError('')
    if (!modelName || !apiKey || !captchaCode) { setError('请填写完整登录信息'); return }
    setBusy(true)
    try {
      const d = await post('UserLoginInterface', {
        login_type: 'model', model_name: modelName, api_key: apiKey,
        captcha_id: captchaId, captcha_code: captchaCode,
      })
      if (d.success) {
        if (remember) saveCredentials(modelName); else clearCredentials()
        window.location.hash = '#/Home'
        window.location.reload()
      } else {
        setError(d.message || '登录失败')
        refreshCaptcha(); setCaptchaCode('')
      }
    } catch (err) {
      setError(err.message || '登录失败')
      refreshCaptcha(); setCaptchaCode('')
    } finally { setBusy(false) }
  }

  return (
    <div className="login-page">
      <form className="login-card" onSubmit={submit}>
        <h1 className="login-title">LsmTokensServer</h1>
        <p className="login-sub">AI Tokens 代理与管理服务</p>
        {error ? <div className="login-error">{error}</div> : null}
        <label className="field">
          <span>模型名称</span>
          <input value={modelName} onChange={(e) => setModelName(e.target.value)}
                 autoComplete="username" placeholder="请输入模型名称" />
        </label>
        <label className="field">
          <span>API Key</span>
          <span className="field-inline">
            <input type={showKey ? 'text' : 'password'} value={apiKey}
                   onChange={(e) => setApiKey(e.target.value)}
                   autoComplete="current-password" placeholder="请输入 API Key" />
            <button type="button" className="btn btn-link" onClick={() => setShowKey(!showKey)}>
              {showKey ? '隐藏' : '显示'}
            </button>
          </span>
        </label>
        <label className="field">
          <span>验证码</span>
          <span className="field-inline">
            <input value={captchaCode} onChange={(e) => setCaptchaCode(e.target.value)}
                   maxLength={4} placeholder="请输入验证码" />
            {captchaUrl
              ? <img className="captcha-img" src={captchaUrl} onClick={refreshCaptcha}
                     title="点击刷新" alt="验证码" />
              : <button type="button" className="btn btn-link" onClick={refreshCaptcha}>刷新</button>}
          </span>
        </label>
        <label className="field-check">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          <span>记住模型名称（API Key 不在本地保存）</span>
        </label>
        <button className="btn btn-primary login-submit" type="submit" disabled={busy}>
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
