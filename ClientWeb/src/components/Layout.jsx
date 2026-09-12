import { useEffect, useState } from 'react'
import { managerLogout, logout } from '../shared/auth'
import { baseUrl } from '../shared/api'
import { loadSidebarCollapsed, saveSidebarCollapsed, loadShortcutsEnabled, saveShortcutsEnabled } from '../shared/navConfig'
import { useKeyboardShortcut } from '../shared/useKeyboardShortcut'
import ToolbarDialogs from './ToolbarDialogs'
import SideNav from './SideNav'
import VersionInfo from './VersionInfo'
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
  // 阶段CA：顶栏构建时间是否隐藏（中屏收起）
  const [timesHidden, setTimesHidden] = useState(() => {
    try { return window.localStorage.getItem('lsm.header.timesHidden') === '1' } catch { return false }
  })
  // 阶段CA：快捷键开关
  const [shortcutsOn, setShortcutsOn] = useState(loadShortcutsEnabled)

  const onToggle = () => {
    if (isMobile) { setMenuOpen(!menuOpen); return }
    setNavCollapsed((v) => { saveSidebarCollapsed(!v); return !v })
  }

  // 阶段CA：快捷键 [ 切换整侧栏折叠，] 全部折叠/展开一级分组，
  // \ 切换顶栏工具下拉（输入框聚焦时已由 hook 屏蔽）。
  useKeyboardShortcut('[', () => {
    if (isMobile) return
    setNavCollapsed((v) => { saveSidebarCollapsed(!v); return !v })
  }, { enabled: !isMobile })

  // ] 切换全部一级分组（事件总线由 SideNav 监听）
  useKeyboardShortcut(']', () => {
    window.dispatchEvent(new CustomEvent('lsm:nav:toggleAllGroups'))
  })

  // \ 切换顶栏工具下拉（事件总线由 ToolbarDialogs 监听）
  useKeyboardShortcut('\\', () => {
    window.dispatchEvent(new CustomEvent('lsm:toolbar:toggleMore'))
  })

  // 移动端抽屉打开时锁定 body 滚动，关闭后恢复
  useEffect(() => {
    if (isMobile && menuOpen) document.body.style.overflow = 'hidden'
    else document.body.style.overflow = ''
    return () => { document.body.style.overflow = '' }
  }, [isMobile, menuOpen])

  // 阶段CA：写入 localStorage（持久化用户偏好）
  useEffect(() => {
    try { window.localStorage.setItem('lsm.header.timesHidden', timesHidden ? '1' : '0') } catch { /* ignore */ }
  }, [timesHidden])

  // 暴露快捷键开关切换器（供顶栏"快捷键"按钮调用——ToolbarDialogs 内可读取）
  // 这里通过事件机制实现，避免循环依赖
  useEffect(() => { saveShortcutsEnabled(shortcutsOn) }, [shortcutsOn])

  return (
    <div className={'layout' + (navCollapsed && !isMobile ? ' sidebar-collapsed' : '')}>
      <header className="layout-header">
        <div className="header-left">
          <button
            className="menu-toggle"
            onClick={onToggle}
            title={isMobile ? '打开菜单' : (navCollapsed ? '展开侧栏' : '折叠侧栏')}
            aria-label={isMobile ? '打开菜单' : (navCollapsed ? '展开侧栏' : '折叠侧栏')}
          >{isMobile ? '☰' : (navCollapsed ? '»' : '«')}</button>
          <img className="app-logo" src={baseUrl() + 'logo-48.png'} alt="logo" />
          <span className="app-title">LsmTokensServer</span>
          <VersionInfo timesHidden={timesHidden} onToggleTimes={() => setTimesHidden((v) => !v)} />
          <span className="app-role">{isAdmin ? t('common.role.admin') : t('common.role.user')}</span>
        </div>
        <div className="header-right">
          <LanguageSwitcher />
          <ToolbarDialogs onToggleTimes={timesHidden ? null : () => setTimesHidden(true)} onToggleShortcuts={() => setShortcutsOn((v) => !v)} shortcutsOn={shortcutsOn} />
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
