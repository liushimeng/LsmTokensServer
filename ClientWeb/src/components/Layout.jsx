import { useState } from 'react'
import { logout } from '../shared/auth'
import ToolbarDialogs from './ToolbarDialogs'

// 导航定义（与旧 server_web_common_nav_admin.go / nav_user.go 一致）
const ADMIN_NAV = [
  ['Home', '首页'],
  ['UserManage', '用户管理'],
  ['DstEndPointManage', '端点管理'],
  ['AIRouteManage', '路由管理'],
  ['ModelInfo', '模型信息'],
  ['AgentInfo', 'Agent信息'],
  ['ProtocolConvertAnalyzer', '协议分析器'],
  ['SpiderDataSource', '爬虫数据源'],
  ['SpiderDailyInfo', '爬虫日报'],
  ['CleanupReport', '清理报告'],
]
const USER_NAV = [
  ['Home', '首页'],
  ['ChatAnalysis', '对话分析'],
  ['ChatAnalysisTotal', '汇总统计'],
  ['ChatAnalysisSession', '会话分析'],
  ['ChatAnalysisTask', '任务分析'],
  ['ChatDialog', '对话查看'],
  ['AIRouteManage', '路由管理'],
  ['ModelInfo', '模型信息'],
  ['AgentInfo', 'Agent信息'],
  ['ProtocolConvertAnalyzer', '协议分析器'],
  ['DstEndPointManage', '端点管理'],
  ['SpiderDataSource', '爬虫数据源'],
  ['SpiderDailyInfo', '爬虫日报'],
  ['CleanupReport', '清理报告'],
]

export default function Layout({ route, userInfo, children }) {
  const isAdmin = !!(userInfo && userInfo.isAdmin)
  const nav = isAdmin ? ADMIN_NAV : USER_NAV
  const [menuOpen, setMenuOpen] = useState(false)
  return (
    <div className="layout">
      <header className="layout-header">
        <div className="header-left">
          <button className="menu-toggle" onClick={() => setMenuOpen(!menuOpen)}>☰</button>
          <span className="app-title">LsmTokensServer</span>
          <span className="app-role">{isAdmin ? '管理端' : '用户端'}</span>
        </div>
        <div className="header-right">
          <ToolbarDialogs />
          {userInfo && userInfo.user_name ? (
            <span className="user-chip">
              {userInfo.user_name}{userInfo.model_name ? ` / ${userInfo.model_name}` : ''}
            </span>
          ) : null}
          <button className="btn btn-link" onClick={logout}>退出</button>
        </div>
      </header>
      <div className="layout-body">
        <nav className={'layout-nav' + (menuOpen ? ' open' : '')}>
          {nav.map(([key, label]) => (
            <a key={key} href={`#/${key}`}
               className={'nav-item' + (route === key ? ' active' : '')}
               onClick={() => setMenuOpen(false)}>{label}</a>
          ))}
        </nav>
        <main className="layout-main">{children}</main>
      </div>
    </div>
  )
}
