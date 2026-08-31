import { useEffect, useRef, useState } from 'react'
import { get, post } from '../shared/api'
import { saveCredentials, loadCredentials, clearCredentials } from '../shared/auth'
import { useI18n } from '../i18n'

// 登录页：双登录方式（模型名登录 + 用户名登录）+ 验证码
// 阶段AQ（20260831）：新增用户名+密码+手机号登录，通过 Tab 切换
export default function Login() {
  const { t } = useI18n()
  const [loginType, setLoginType] = useState('model') // 'model' 或 'user'
  const [captchaId, setCaptchaId] = useState('')
  const [captchaUrl, setCaptchaUrl] = useState('')
  // 模型登录字段
  const [modelName, setModelName] = useState('')
  const [apiKey, setApiKey] = useState('')
  // 用户登录字段
  const [userName, setUserName] = useState('')
  const [password, setPassword] = useState('')
  const [phone, setPhone] = useState('')
  // 通用字段
  const [captchaCode, setCaptchaCode] = useState('')
  const [remember, setRemember] = useState(false)
  const [error, setError] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
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

  // 阶段AS：v4 双份记忆——同时恢复两种登录方式的已保存名称，
  // 切换 Tab 时保存当前 Tab 的凭据后恢复另一 Tab 的已存名称。
  // 每个 Tab 的"记住我"状态独立维护。
  const savedCredsRef = useRef({ modelData: null, userData: null })

  useEffect(() => {
    aliveRef.current = true
    refreshCaptcha()
    // v4：加载保存的登录凭据，同时恢复两种登录方式各自记忆的名称
    const creds = loadCredentials()
    if (creds) {
      savedCredsRef.current = { modelData: creds.modelData, userData: creds.userData }
      const activeType = creds.loginType || 'model'
      setLoginType(activeType)
      // 恢复模型名 Tab 的已保存值
      if (creds.modelData && creds.modelData.mn) {
        setModelName(creds.modelData.mn)
      }
      // 恢复用户名 Tab 的已保存值
      if (creds.userData && creds.userData.mn) {
        setUserName(creds.userData.mn)
      }
      // 记住我状态跟随当前 active Tab
      if (activeType === 'user') {
        setRemember(!!(creds.userData && creds.userData.mn))
      } else {
        setRemember(!!(creds.modelData && creds.modelData.mn))
      }
    }
    return () => { aliveRef.current = false }
  }, [])

  // 切换 Tab：保存当前 Tab 凭据后刷新验证码，并恢复目标 Tab 的已存名称与记住状态
  const switchTab = (type) => {
    if (type === loginType) return
    // 保存当前 Tab 的凭据（勾选了"记住我"时）
    if (remember) {
      const savedName = loginType === 'model' ? modelName : userName
      if (savedName) saveCredentials(loginType, savedName)
    }
    setLoginType(type)
    setError('')
    setCaptchaCode('')
    refreshCaptcha()
    // 恢复目标 Tab 的已保存值和"记住我"状态
    if (type === 'user') {
      const ud = savedCredsRef.current.userData
      if (ud && ud.mn && !userName) setUserName(ud.mn)
      setRemember(!!(ud && ud.mn))
    } else {
      const md = savedCredsRef.current.modelData
      if (md && md.mn && !modelName) setModelName(md.mn)
      setRemember(!!(md && md.mn))
    }
  }

  const submit = async (e) => {
    e.preventDefault()
    setError('')

    // 根据登录类型校验字段
    if (loginType === 'model') {
      if (!modelName || !apiKey || !captchaCode) { setError(t('login.emptyModelName')); return }
    } else {
      // 阶段AR：手机号改为选填（管理员可清空用户手机号）。
      if (!userName || !password || !captchaCode) { setError(t('login.emptyUserName')); return }
    }

    setBusy(true)
    try {
      const reqBody = {
        login_type: loginType,
        captcha_id: captchaId,
        captcha_code: captchaCode,
      }
      if (loginType === 'model') {
        reqBody.model_name = modelName
        reqBody.api_key = apiKey
      } else {
        reqBody.user_name = userName
        reqBody.password = password
        reqBody.phone = phone
      }

      const d = await post('UserLoginInterface', reqBody)
      if (d.success) {
        // 记住我：保存登录类型和名称
        const savedName = loginType === 'model' ? modelName : userName
        if (remember) saveCredentials(loginType, savedName); else clearCredentials()
        window.location.hash = '#/Home'
        window.location.reload()
      } else {
        setError(d.message || t('login.loginFailed'))
        refreshCaptcha(); setCaptchaCode('')
      }
    } catch (err) {
      setError(err.message || t('login.loginFailed'))
      refreshCaptcha(); setCaptchaCode('')
    } finally { setBusy(false) }
  }

  return (
    <div className="login-page">
      <form className="login-card" onSubmit={submit}>
        <h1 className="login-title">{t('common.appName')}</h1>
        <p className="login-sub">AI Tokens {t('login.title')}</p>

        {/* Tab 选项卡 */}
        <div className="login-tabs">
          <div className={`login-tab ${loginType === 'model' ? 'active' : ''}`}
               onClick={() => switchTab('model')}>
            {t('login.tabModel')}
          </div>
          <div className={`login-tab ${loginType === 'user' ? 'active' : ''}`}
               onClick={() => switchTab('user')}>
            {t('login.tabUser')}
          </div>
        </div>

        {error ? <div className="login-error">{error}</div> : null}

        {/* 模型名登录表单 */}
        {loginType === 'model' && (
          <>
            <label className="field">
              <span>{t('login.modelName')}</span>
              <input value={modelName} onChange={(e) => setModelName(e.target.value)}
                     autoComplete="username" placeholder={t('login.emptyModelName')} />
            </label>
            <label className="field">
              <span>{t('login.apiKey')}</span>
              <span className="field-inline">
                <input type={showKey ? 'text' : 'password'} value={apiKey}
                       onChange={(e) => setApiKey(e.target.value)}
                       autoComplete="current-password" placeholder={t('login.emptyApiKey')} />
                <button type="button" className="btn btn-link" onClick={() => setShowKey(!showKey)}>
                  {showKey ? t('common.hide') : t('common.show')}
                </button>
              </span>
            </label>
          </>
        )}

        {/* 用户名登录表单 */}
        {loginType === 'user' && (
          <>
            <label className="field">
              <span>{t('login.userName')}</span>
              <input value={userName} onChange={(e) => setUserName(e.target.value)}
                     autoComplete="username" placeholder={t('login.emptyUserName')} />
            </label>
            <label className="field">
              <span>{t('login.password')}</span>
              <span className="field-inline">
                <input type={showPassword ? 'text' : 'password'} value={password}
                       onChange={(e) => setPassword(e.target.value)}
                       autoComplete="current-password" placeholder={t('login.emptyPassword')} />
                <button type="button" className="btn btn-link" onClick={() => setShowPassword(!showPassword)}>
                  {showPassword ? t('common.hide') : t('common.show')}
                </button>
              </span>
            </label>
            <label className="field">
              <span>{t('login.phone')}</span>
              <input value={phone} onChange={(e) => setPhone(e.target.value)}
                     autoComplete="tel" placeholder={t('login.emptyPhone')} />
              {/* 阶段AR：手机号可选，未填写时按 DB 中实际手机号（可能为空）校验 */}
              <span className="field-hint">{t('login.phoneOptionalHint')}</span>
            </label>
          </>
        )}

        {/* 验证码（两种登录方式共用） */}
        <label className="field">
          <span>{t('login.captcha')}</span>
          <span className="field-inline">
            <input value={captchaCode} onChange={(e) => setCaptchaCode(e.target.value)}
                   maxLength={4} placeholder={t('login.captchaPlaceholder')} />
            {captchaUrl
              ? <img className="captcha-img" src={captchaUrl} onClick={refreshCaptcha}
                     title={t('login.captchaRefresh')} alt={t('login.captcha')} />
              : <button type="button" className="btn btn-link" onClick={refreshCaptcha}>{t('common.refresh')}</button>}
          </span>
        </label>

        <label className="field-check">
          <input type="checkbox" checked={remember} onChange={(e) => setRemember(e.target.checked)} />
          <span>{loginType === 'model' ? t('login.rememberModelName') : t('login.rememberUserName')}</span>
        </label>
        <button className="btn btn-primary login-submit" type="submit" disabled={busy}>
          {busy ? t('common.processing') : t('login.submit')}
        </button>
      </form>
    </div>
  )
}
