import { useEffect, useState } from 'react'
import { get } from './shared/api'
import Layout from './components/Layout'
import Login from './pages/Login'
import Home from './pages/Home'
import UserManage from './pages/UserManage'
import DstEndPointManage from './pages/DstEndPointManage'
import AIRouteManage from './pages/AIRouteManage'
import ModelInfo from './pages/ModelInfo'
import AgentInfo from './pages/AgentInfo'
import ProtocolConvertAnalyzer from './pages/ProtocolConvertAnalyzer'
import SpiderDataSource from './pages/SpiderDataSource'
import SpiderDailyInfo from './pages/SpiderDailyInfo'
import CleanupReport from './pages/CleanupReport'
import ChatAnalysis from './pages/ChatAnalysis'
import ChatAnalysisTotal from './pages/ChatAnalysisTotal'
import ChatAnalysisSession from './pages/ChatAnalysisSession'
import ChatAnalysisTask from './pages/ChatAnalysisTask'
import ChatDialog from './pages/ChatDialog'
import ManagerLogin from './pages/ManagerLogin'

// 页面注册表：key = hash 路由名
const PAGES = {
  Login, ManagerLogin, Home, UserManage, DstEndPointManage, AIRouteManage, ModelInfo, AgentInfo,
  ProtocolConvertAnalyzer, SpiderDataSource, SpiderDailyInfo, CleanupReport,
  ChatAnalysis, ChatAnalysisTotal, ChatAnalysisSession, ChatAnalysisTask, ChatDialog,
}

// 路径别名映射：兼容服务端 redirect（如 /UserLogin → Login）
const PATH_ALIASES = {
  UserLogin: 'Login',
  ManagerHome: 'Home',
}

function currentRoute() {
  let h = window.location.hash.replace(/^#\/?/, '')
  // 兼容直接通过 pathname 访问（如 /UserManage → 视为对应页面）
  if (!h) {
    h = window.location.pathname.replace(/^\//, '')
  }
  const [path, query] = h.split('?')
  // 路径别名归一化
  const normalized = PATH_ALIASES[path] || path
  return { path: PAGES[normalized] ? normalized : 'Home', query: new URLSearchParams(query || '') }
}

export default function App() {
  const [route, setRoute] = useState(currentRoute())
  const [userInfo, setUserInfo] = useState(null)

  useEffect(() => {
    const onHash = () => setRoute(currentRoute())
    window.addEventListener('hashchange', onHash)
    return () => window.removeEventListener('hashchange', onHash)
  }, [])

  useEffect(() => {
    if (route.path === 'Login' || route.path === 'ManagerLogin') return
    let alive = true
    // 登录信息与角色探测并发获取后合并为一次 setState，避免两个 setState 竞态
    // 导致 isAdmin 被后到的 UserInfoInterface 结果覆盖（管理端误显示为“用户端”）。
    const infoP = get('UserInfoInterface')
      .then((d) => ({ ...((d && d.data) || d), loaded: true }))
      .catch(() => null)
    // 角色探测：管理端 mux 独有 UserManageInterface POST 接口；
    // 用户端 302/404 → user 角色；管理端未登录返回 401 JSON → manager 端口但需跳管理端登录页
    const roleP = fetch('UserManageInterface', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'list' }),
    })
      .then((r) => {
        const ct = r.headers.get('content-type') || ''
        if (r.status === 401 && ct.includes('json')) return 'manager-unauth'
        return r.ok && ct.includes('json') ? 'manager' : 'user'
      })
      .catch(() => 'user')
    Promise.all([infoP, roleP]).then(([info, role]) => {
      if (!alive) return
      // 记录端口角色，供 api.js 401 时选择正确的登录页
      try { localStorage.setItem('lsm.role', role === 'user' ? 'user' : 'manager') } catch { /* 忽略 */ }
      if (!info) {
        if (role === 'manager-unauth') { window.location.href = '/ManagerLogin'; return }
        window.location.hash = '#/Login'; return
      }
      setUserInfo({ ...info, isAdmin: role === 'manager' })
    })
    return () => { alive = false }
  }, [route.path])

  if (route.path === 'Login') return <Login />
  if (route.path === 'ManagerLogin') return <ManagerLogin />

  const Page = PAGES[route.path] || Home
  return (
    <Layout route={route.path} userInfo={userInfo}>
      <Page route={route} />
    </Layout>
  )
}
