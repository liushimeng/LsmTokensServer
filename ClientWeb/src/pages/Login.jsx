import { useEffect, useRef, useState } from 'react'
import { get, post } from '../shared/api'
import { saveCredentials, loadCredentials, clearCredentials } from '../shared/auth'
import { useI18n } from '../i18n'

// 登录页：双登录方式（模型名登录 + 用户名登录）+ 验证码
// 阶段AQ（20260831）：新增用户名+密码+手机号登录，通过 Tab 切换
// 阶段BO（20260902）：用户名登录成功后手机号自动保存 localStorage 并自动回填；
// 手机号输入框与密码一致，默认掩码隐藏、支持显示/隐藏切换。
// 阶段BP（20260903）：双 Tab 自动填充隔离（autoComplete 差异化）+ 手机号加密存储。
// 阶段BQ（20260903）：双 Tab 记忆完全独立化——rememberModel/rememberUser 独立 state；
// 模型 Tab 记住模型名+API Key，用户 Tab 记住用户名+密码+手机号（全部 AES-GCM 加密）；
// submit 按 Tab 独立清除（clearCredentials(type)）；验证码错误不触发锁定（后端修复）。
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
  // 阶段BQ：双 Tab 独立"记住我"状态（不再共享单一 remember）
  const [rememberModel, setRememberModel] = useState(false)
  const [rememberUser, setRememberUser] = useState(false)
  const [error, setError] = useState('')
  const [showKey, setShowKey] = useState(false)
  const [showPassword, setShowPassword] = useState(false)
  const [showPhone, setShowPhone] = useState(false) // 阶段BO：手机号默认隐藏（敏感信息遮蔽）
  const [busy, setBusy] = useState(false)
  // 阶段AO：组件卸载后 setState 静默丢弃
  const aliveRef = useRef(true)

  const refreshCaptcha = async () => {
    try {
      const d = await get('CaptchaGenerate')
      if (!aliveRef.current) return
      if (d.success) { setCaptchaId(d.captcha_id); setCaptchaUrl(d.image_url) }
      else { setError(t('login.captchaRefreshFailed') || '验证码刷新失败') }
    } catch { setError(t('login.captchaRefreshFailed') || '验证码刷新失败，请检查网络') }
  }

  // 阶段AS：双份记忆——同时恢复两种登录方式的已保存名称，
  // 切换 Tab 时保存当前 Tab 的凭据后恢复另一 Tab 的已存名称。
  const savedCredsRef = useRef({ modelData: null, userData: null })

  useEffect(() => {
    aliveRef.current = true
    refreshCaptcha()
    // v7：加载保存的登录凭据（async：手机号/密码/API Key 需解密），恢复两种登录方式各自记忆的值
    ;(async () => {
      const creds = await loadCredentials()
      if (!aliveRef.current) return
      if (creds) {
        savedCredsRef.current = { modelData: creds.modelData, userData: creds.userData }
        const activeType = creds.loginType || 'model'
        setLoginType(activeType)
        // 恢复模型名 Tab 的已保存值（含 API Key）
        if (creds.modelData) {
          if (creds.modelData.mn) setModelName(creds.modelData.mn)
          if (creds.modelData.ak) setApiKey(creds.modelData.ak)
        }
        // 恢复用户名 Tab 的已保存值（含密码 + 手机号）
        if (creds.userData) {
          if (creds.userData.mn) setUserName(creds.userData.mn)
          if (creds.userData.pw) setPassword(creds.userData.pw)
          if (creds.userData.ph) setPhone(creds.userData.ph)
        }
        // 阶段BQ：双 Tab 独立恢复"记住我"状态
        setRememberModel(!!(creds.modelData && creds.modelData.mn))
        setRememberUser(!!(creds.userData && creds.userData.mn))
      }
    })()
    return () => { aliveRef.current = false }
  }, [])

  // 切换 Tab：保存当前 Tab 凭据后刷新验证码，并恢复目标 Tab 的已存名称与记住状态
  const switchTab = async (type) => {
    if (type === loginType) return
    // 保存当前 Tab 的凭据（勾选了"记住我"时），保持存储新鲜
    if (loginType === 'model') {
      if (rememberModel && modelName) {
        await saveCredentials('model', modelName, { apiKey })
      }
    } else {
      if (rememberUser && userName) {
        await saveCredentials('user', userName, { password, phone })
      }
    }
    setLoginType(type)
    setError('')
    setCaptchaCode('')
    refreshCaptcha()
    // 恢复目标 Tab 的已保存值和"记住我"状态
    if (type === 'user') {
      const ud = savedCredsRef.current.userData
      if (ud) {
        if (ud.mn && !userName) setUserName(ud.mn)
        if (ud.pw && !password) setPassword(ud.pw)
        if (ud.ph && !phone) setPhone(ud.ph)
      }
    } else {
      const md = savedCredsRef.current.modelData
      if (md) {
        if (md.mn && !modelName) setModelName(md.mn)
        if (md.ak && !apiKey) setApiKey(md.ak)
      }
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
        // 阶段BQ：按 Tab 独立保存/清除凭据（不再整体 clearCredentials）
        if (loginType === 'model') {
          if (rememberModel) {
            await saveCredentials('model', modelName, { apiKey })
          } else {
            clearCredentials('model') // 仅清除模型 Tab 条目
          }
        } else {
          if (rememberUser) {
            await saveCredentials('user', userName, { password, phone })
          } else {
            clearCredentials('user') // 仅清除用户 Tab 条目
          }
        }
        window.location.hash = '#/Home'
        window.location.reload()
      } else {
        setError(d.message || t('login.loginFailed'))
        // 阶段BQ：await refreshCaptcha 消除竞态（避免旧图/旧 id 残留）
        setCaptchaCode('')
        await refreshCaptcha()
      }
    } catch (err) {
      setError(err.message || t('login.loginFailed'))
      setCaptchaCode('')
      await refreshCaptcha()
    } finally { setBusy(false) }
  }

  // 当前 Tab 的"记住我"状态与 setter
  const remember = loginType === 'model' ? rememberModel : rememberUser
  const setRemember = loginType === 'model' ? setRememberModel : setRememberUser

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
              {/* 阶段BP：autoComplete="off" 隔离浏览器自动填充，避免跨 Tab 错乱 */}
              <input value={modelName} onChange={(e) => setModelName(e.target.value)}
                     autoComplete="off" placeholder={t('login.emptyModelName')} />
            </label>
            <label className="field">
              <span>{t('login.apiKey')}</span>
              <span className="field-inline">
                {/* 阶段BP：autoComplete="off" 隔离浏览器自动填充 */}
                <input type={showKey ? 'text' : 'password'} value={apiKey}
                       onChange={(e) => setApiKey(e.target.value)}
                       autoComplete="off" placeholder={t('login.emptyApiKey')} />
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
                {/* 阶段BP：autoComplete="new-password" 阻止浏览器填充已存密码 */}
                <input type={showPassword ? 'text' : 'password'} value={password}
                       onChange={(e) => setPassword(e.target.value)}
                       autoComplete="new-password" placeholder={t('login.emptyPassword')} />
                <button type="button" className="btn btn-link" onClick={() => setShowPassword(!showPassword)}>
                  {showPassword ? t('common.hide') : t('common.show')}
                </button>
              </span>
            </label>
            <label className="field">
              <span>{t('login.phone')}</span>
              {/* 阶段BO：手机号与密码一致——默认掩码隐藏，支持显示/隐藏切换 */}
              <span className="field-inline">
                <input type={showPhone ? 'text' : 'password'} value={phone}
                       onChange={(e) => setPhone(e.target.value)}
                       autoComplete="off" inputMode="tel" placeholder={t('login.emptyPhone')} />
                <button type="button" className="btn btn-link" onClick={() => setShowPhone(!showPhone)}>
                  {showPhone ? t('common.hide') : t('common.show')}
                </button>
              </span>
              {/* 阶段AR：手机号可选，未填写时按 DB 中实际手机号（可能为空）校验 */}
              <span className="field-hint">{t('login.phoneOptionalHint')}</span>
              {/* 阶段BO/BQ：告知用户勾选记住我后手机号自动保存在本机浏览器 */}
              <span className="field-hint">{t('login.phoneSaveHint')}</span>
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
          {/* 阶段BQ：双 Tab 独立文案 */}
          <span>{loginType === 'model' ? t('login.rememberModelName') : t('login.rememberUserName')}</span>
        </label>
        <button className="btn btn-primary login-submit" type="submit" disabled={busy}>
          {busy ? t('common.processing') : t('login.submit')}
        </button>
      </form>
    </div>
  )
}
