import { lazy, Suspense, useEffect, useState } from 'react'
import { get, baseUrl } from './shared/api'
import Layout from './components/Layout'
import ErrorBoundary from './components/ErrorBoundary'
import { I18nProvider } from './i18n'
import { ConfirmProvider } from './components/ConfirmModal'

// 阶段T 双构建隔离：页面改为懒加载器，管理员专属页仅在 manager 构建注册。
// 注意：判断必须直接使用 __APP_ROLE__ 字面量（vite define 全局文本替换），
// user 构建下条件恒为 false，Rollup 死代码消除连同 import() 一并移除 → chunk 不产出；
// 若经由其他模块间接引用（如 auth.js 的 BUILD_ROLE），define 无法替换，裁剪将失效。
const PAGES = {
  Home: lazy(() => import('./pages/Home')),
  Login: lazy(() => import('./pages/Login')),
  DstEndPointManage: lazy(() => import('./pages/DstEndPointManage')),
  AIRouteManage: lazy(() => import('./pages/AIRouteManage')),
  ModelInfo: lazy(() => import('./pages/model-info')),
  AgentInfo: lazy(() => import('./pages/agent-info')),
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
    let attempts = 0
    const tryFetchUser = () => {
      attempts++
      // 阶段BZ：UserInfoInterface 走短档 12s；非 401 失败时指数退避重试 2 次，
      // 避免服务短暂重启时把用户直接踢回登录页。仅在所有重试都失败后才回 Login，
      // 且不调用 window.location.reload()，避免与 main.jsx 全局监听形成重试风暴。
      get('UserInfoInterface')
        .then((d) => {
          if (!alive) return
          setUserInfo({ ...((d && d.data) || d), loaded: true, isAdmin: __APP_ROLE__ === 'manager' })
        })
        .catch((err) => {
          if (!alive) return
          const code = err && err.code
          if (code === 'http_4xx') return // api.js 已按构建角色跳转
          if ((code === 'timeout' || code === 'network' || code === 'http_5xx') && attempts <= 2) {
            const delay = 800 * Math.pow(2, attempts - 1) + Math.random() * 200
            setTimeout(tryFetchUser, delay)
            return
          }
          // 三次都失败：按构建角色跳登录页，但用 location.replace 避免残留破损状态，
          // 不调用 reload（避免与 main.jsx 全局 chunk-error 监听形成循环）。
          if (__APP_ROLE__ === 'manager') {
            window.location.replace(baseUrl() + 'ManagerLogin')
          } else {
            window.location.replace(baseUrl() + 'Login')
          }
        })
    }
    tryFetchUser()
    return () => { alive = false }
  }, [route.path])

  const Page = PAGES[route.path] || PAGES.Home
  return (
    <I18nProvider>
      <ConfirmProvider>
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
      </ConfirmProvider>
    </I18nProvider>
  )
}
