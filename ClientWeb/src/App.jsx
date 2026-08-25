import { lazy, Suspense, useEffect, useState } from 'react'
import { get, baseUrl } from './shared/api'
import Layout from './components/Layout'
import ErrorBoundary from './components/ErrorBoundary'

// 阶段T 双构建隔离：页面改为懒加载器，管理员专属页仅在 manager 构建注册。
// 注意：判断必须直接使用 __APP_ROLE__ 字面量（vite define 全局文本替换），
// user 构建下条件恒为 false，Rollup 死代码消除连同 import() 一并移除 → chunk 不产出；
// 若经由其他模块间接引用（如 auth.js 的 BUILD_ROLE），define 无法替换，裁剪将失效。
const PAGES = {
  Home: lazy(() => import('./pages/Home')),
  Login: lazy(() => import('./pages/Login')),
  DstEndPointManage: lazy(() => import('./pages/DstEndPointManage')),
  AIRouteManage: lazy(() => import('./pages/AIRouteManage')),
  ModelInfo: lazy(() => import('./pages/ModelInfo')),
  AgentInfo: lazy(() => import('./pages/AgentInfo')),
  ProtocolConvertAnalyzer: lazy(() => import('./pages/ProtocolConvertAnalyzer')),
  SpiderDataSource: lazy(() => import('./pages/SpiderDataSource')),
  SpiderDailyInfo: lazy(() => import('./pages/SpiderDailyInfo')),
  CleanupReport: lazy(() => import('./pages/CleanupReport')),
  ChatAnalysis: lazy(() => import('./pages/ChatAnalysis')),
  ChatAnalysisTotal: lazy(() => import('./pages/ChatAnalysisTotal')),
  ChatAnalysisSession: lazy(() => import('./pages/ChatAnalysisSession')),
  ChatAnalysisTask: lazy(() => import('./pages/ChatAnalysisTask')),
  ChatDialog: lazy(() => import('./pages/ChatDialog')),
}
if (__APP_ROLE__ === 'manager') {
  // 管理员专属页：UserManage（用户管理）、ManagerLogin（管理端登录）
  PAGES.UserManage = lazy(() => import('./pages/UserManage'))
  PAGES.ManagerLogin = lazy(() => import('./pages/ManagerLogin'))
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
    if (route.path === 'Login' || (__APP_ROLE__ === 'manager' && route.path === 'ManagerLogin')) return
    let alive = true
    // 角色由构建期常量决定（阶段T），不再运行时探测管理端接口
    get('UserInfoInterface')
      .then((d) => {
        if (!alive) return
        setUserInfo({ ...((d && d.data) || d), loaded: true, isAdmin: __APP_ROLE__ === 'manager' })
      })
      .catch(() => {
        if (!alive) return
        // 登录态失效：401 时 api.js 已按构建角色跳转，这里兜底处理其他失败（含网络超时）
        if (__APP_ROLE__ === 'manager') { window.location.href = baseUrl() + 'ManagerLogin'; return }
        // 强制完整跳转 + reload，避免仅改 hash 导致页面残留破损状态
        window.location.hash = '#/Login'
        window.location.reload()
      })
    return () => { alive = false }
  }, [route.path])

  const Page = PAGES[route.path] || PAGES.Home
  return (
    <ErrorBoundary>
      <Suspense fallback={<div className="page-loading" style={{ padding: 24 }}>加载中…</div>}>
        {route.path === 'Login' || (__APP_ROLE__ === 'manager' && route.path === 'ManagerLogin') ? (
          <Page route={route} />
        ) : (
          <Layout route={route.path} userInfo={userInfo}>
            <Page route={route} />
          </Layout>
        )}
      </Suspense>
    </ErrorBoundary>
  )
}
