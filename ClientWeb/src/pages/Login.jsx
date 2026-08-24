import { useEffect, useState } from 'react'
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

  const refreshCaptcha = async () => {
    try {
      const d = await get('CaptchaGenerate')
      if (d.success) { setCaptchaId(d.captcha_id); setCaptchaUrl(d.image_url) }
    } catch { /* 忽略 */ }
  }

  useEffect(() => {
    refreshCaptcha()
    const creds = loadCredentials()
    if (creds && creds.modelName && creds.apiKey) {
      setModelName(creds.modelName); setApiKey(creds.apiKey); setRemember(true)
    }
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
        if (remember) saveCredentials(modelName, apiKey); else clearCredentials()
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
          <span>记住模型名称和 API Key（本地加密存储）</span>
        </label>
        <button className="btn btn-primary login-submit" type="submit" disabled={busy}>
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
