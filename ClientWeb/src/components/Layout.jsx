import { useEffect, useState } from 'react'
import { logout, managerLogout } from '../shared/auth'
import { baseUrl } from '../shared/api'
import { loadSidebarCollapsed, saveSidebarCollapsed } from '../shared/navConfig'
import ToolbarDialogs from './ToolbarDialogs'
import SideNav from './SideNav'
import { useI18n, LanguageSwitcher } from '../i18n'

const MOBILE_MQ = '(max-width: 860px)'

function useIsMobile() {
  const [mobile, setMobile] = useState(() => window.matchMedia(MOBILE_MQ).matches)
  useEffect(() => {
    const mq = window.matchMedia(MOBILE_MQ)
    const onChange = (e) => setMobile(e.matches)
    mq.addEventListener('change', onChange)
    return () => mq.removeEventListener('change', onChange)
  }, [])
  return mobile
}

export default function Layout({ route, userInfo, children }) {
  const isAdmin = !!(userInfo && userInfo.isAdmin)
  const role = isAdmin ? 'admin' : 'user'
  const { t } = useI18n()
  const isMobile = useIsMobile()
  // 移动端：抽屉开关；桌面端：整侧栏折叠（localStorage 记忆）
  const [menuOpen, setMenuOpen] = useState(false)
  const [navCollapsed, setNavCollapsed] = useState(loadSidebarCollapsed)

  const onToggle = () => {
    if (isMobile) { setMenuOpen(!menuOpen); return }
    setNavCollapsed((v) => { saveSidebarCollapsed(!v); return !v })
  }

  // 移动端抽屉打开时锁定 body 滚动，关闭后恢复
  useEffect(() => {
    if (isMobile && menuOpen) document.body.style.overflow = 'hidden'
    else document.body.style.overflow = ''
    return () => { document.body.style.overflow = '' }
  }, [isMobile, menuOpen])

  return (
    <div className="layout">
      <header className="layout-header">
        <div className="header-left">
          <button className="menu-toggle" onClick={onToggle}>☰</button>
          <img className="app-logo" src={baseUrl() + "logo-48.png"} alt="logo" />
          <span className="app-title">LsmTokensServer</span>
          <span className="app-role">{isAdmin ? t('common.role.admin') : t('common.role.user')}</span>
        </div>
        <div className="header-right">
          <LanguageSwitcher />
          <ToolbarDialogs />
          {userInfo && userInfo.user_name ? (
            <span className="user-chip">
              {userInfo.user_name}{userInfo.model_name ? ` / ${userInfo.model_name}` : ''}
            </span>
          ) : null}
          <button className="btn btn-link" onClick={isAdmin ? managerLogout : logout}>{t('common.logout')}</button>
        </div>
      </header>
      <div className="layout-body">
        <SideNav
          role={role}
          route={route}
          collapsed={!isMobile && navCollapsed}
          open={menuOpen}
          onClose={() => setMenuOpen(false)}
          onExpand={() => { saveSidebarCollapsed(false); setNavCollapsed(false) }}
        />
        {isMobile && menuOpen && <div className="nav-mask" onClick={() => setMenuOpen(false)} />}
        <main className="layout-main">{children}</main>
      </div>
    </div>
  )
}
