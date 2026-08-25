import { useEffect, useState } from 'react'
import { get, post } from '../shared/api'

// 管理端登录页（v2.0.56 安全加固）：管理员账号 + 密码 + 验证码
// 凭证在服务端 LsmTokensServer.conf 的 security 段配置，前端不保存任何管理员敏感信息
export default function ManagerLogin() {
  const [captchaId, setCaptchaId] = useState('')
  const [captchaUrl, setCaptchaUrl] = useState('')
  const [userName, setUserName] = useState('')
  const [password, setPassword] = useState('')
  const [captchaCode, setCaptchaCode] = useState('')
  const [error, setError] = useState('')
  const [showPwd, setShowPwd] = useState(false)
  const [busy, setBusy] = useState(false)

  const refreshCaptcha = async () => {
    try {
      const d = await get('CaptchaGenerate')
      if (d.success) { setCaptchaId(d.captcha_id); setCaptchaUrl(d.image_url) }
    } catch { /* 忽略 */ }
  }

  useEffect(() => { refreshCaptcha() }, [])

  const submit = async (e) => {
    e.preventDefault()
    setError('')
    if (!userName || !password || !captchaCode) { setError('请填写完整登录信息'); return }
    setBusy(true)
    try {
      const d = await post('ManagerLoginInterface', {
        user_name: userName, password,
        captcha_id: captchaId, captcha_code: captchaCode,
      })
      if (d.success) {
        window.location.href = '/Home'
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
        <h1 className="login-title">LsmTokensServer 管理端</h1>
        <p className="login-sub">管理员登录</p>
        {error ? <div className="login-error">{error}</div> : null}
        <label className="field">
          <span>管理员账号</span>
          <input value={userName} onChange={(e) => setUserName(e.target.value)}
                 autoComplete="username" placeholder="请输入管理员账号" />
        </label>
        <label className="field">
          <span>密码</span>
          <span className="field-inline">
            <input type={showPwd ? 'text' : 'password'} value={password}
                   onChange={(e) => setPassword(e.target.value)}
                   autoComplete="current-password" placeholder="请输入密码" />
            <button type="button" className="btn btn-link" onClick={() => setShowPwd(!showPwd)}>
              {showPwd ? '隐藏' : '显示'}
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
        <button className="btn btn-primary login-submit" type="submit" disabled={busy}>
          {busy ? '登录中…' : '登录'}
        </button>
      </form>
    </div>
  )
}
