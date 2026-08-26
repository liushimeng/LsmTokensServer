import { useEffect, useState } from 'react'
import { get, post, baseUrl } from '../shared/api'
import { useI18n } from '../i18n'

// 管理端登录页（v2.0.56 安全加固）：管理员账号 + 密码 + 验证码
// 凭证在服务端 LsmTokensServer.conf 的 security 段配置，前端不保存任何管理员敏感信息
export default function ManagerLogin() {
  const { t } = useI18n()
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
    if (!userName || !password || !captchaCode) { setError(t('managerLogin.emptyUsername')); return }
    setBusy(true)
    try {
      const d = await post('ManagerLoginInterface', {
        user_name: userName, password,
        captcha_id: captchaId, captcha_code: captchaCode,
      })
      if (d.success) {
        window.location.href = baseUrl() + 'Home'
        window.location.reload()
      } else {
        setError(d.message || t('managerLogin.loginFailed'))
        refreshCaptcha(); setCaptchaCode('')
      }
    } catch (err) {
      setError(err.message || t('managerLogin.loginFailed'))
      refreshCaptcha(); setCaptchaCode('')
    } finally { setBusy(false) }
  }

  return (
    <div className="login-page">
      <form className="login-card" onSubmit={submit}>
        <h1 className="login-title">{t('common.appName')} {t('common.role.admin')}</h1>
        <p className="login-sub">{t('managerLogin.title')}</p>
        {error ? <div className="login-error">{error}</div> : null}
        <label className="field">
          <span>{t('managerLogin.username')}</span>
          <input value={userName} onChange={(e) => setUserName(e.target.value)}
                 autoComplete="username" placeholder={t('managerLogin.emptyUsername')} />
        </label>
        <label className="field">
          <span>{t('managerLogin.password')}</span>
          <span className="field-inline">
            <input type={showPwd ? 'text' : 'password'} value={password}
                   onChange={(e) => setPassword(e.target.value)}
                   autoComplete="current-password" placeholder={t('managerLogin.emptyPassword')} />
            <button type="button" className="btn btn-link" onClick={() => setShowPwd(!showPwd)}>
              {showPwd ? t('common.hide') : t('common.show')}
            </button>
          </span>
        </label>
        <label className="field">
          <span>{t('managerLogin.captcha')}</span>
          <span className="field-inline">
            <input value={captchaCode} onChange={(e) => setCaptchaCode(e.target.value)}
                   maxLength={4} placeholder={t('login.captchaPlaceholder')} />
            {captchaUrl
              ? <img className="captcha-img" src={captchaUrl} onClick={refreshCaptcha}
                     title={t('login.captchaRefresh')} alt={t('managerLogin.captcha')} />
              : <button type="button" className="btn btn-link" onClick={refreshCaptcha}>{t('common.refresh')}</button>}
          </span>
        </label>
        <button className="btn btn-primary login-submit" type="submit" disabled={busy}>
          {busy ? t('common.processing') : t('managerLogin.submit')}
        </button>
      </form>
    </div>
  )
}
