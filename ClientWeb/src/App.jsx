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

// 页面注册表：key = hash 路由名
const PAGES = {
  Login, Home, UserManage, DstEndPointManage, AIRouteManage, ModelInfo, AgentInfo,
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
    if (route.path === 'Login') return
    get('UserInfoInterface')
      .then((d) => setUserInfo({ ...(d && d.data) || d, loaded: true }))
      .catch(() => { window.location.hash = '#/Login' })
    // 角色探测：管理端 mux 独有 UserManageInterface POST 接口，用户端 404 → user 角色
    fetch('UserManageInterface', {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action: 'list' }),
    })
      .then((r) => {
        const ct = r.headers.get('content-type') || ''
        setUserInfo((u) => ({ ...u, isAdmin: r.status !== 404 && ct.includes('json') }))
      })
      .catch(() => setUserInfo((u) => ({ ...u, isAdmin: false })))
  }, [route.path])

  if (route.path === 'Login') return <Login />

  const Page = PAGES[route.path] || Home
  return (
    <Layout route={route.path} userInfo={userInfo}>
      <Page route={route} />
    </Layout>
  )
}
