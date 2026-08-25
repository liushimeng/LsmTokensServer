// 用户名+模型名级联下拉选项（仅管理端构建消费）
// 页面生命周期内模块级缓存：同一页面（含路由切换不刷新的 SPA 内）所有页面
// 共享一次请求；浏览器刷新即重建模块、缓存失效自动重取，避免点一下查一次。
// 用户端构建（__APP_ROLE__='user'）下不发起任何请求，接口名经 Rollup DCE 裁剪。
import { useEffect, useState } from 'react'
import { get } from './api'

let cachedPromise = null

// 加载（并缓存）全部用户及其名下模型名：[{user_name, model_names:[...]}]
export function loadUserModelOptions() {
  if (__APP_ROLE__ !== 'manager') return Promise.resolve([])
  if (!cachedPromise) {
    cachedPromise = get('UserModelOptionsInterface')
      .then((d) => (d && d.users) || [])
      .catch(() => { cachedPromise = null; return [] })
  }
  return cachedPromise
}

// 手动失效缓存（增删改用户/模型后需要刷新下拉数据时调用）
export function clearUserModelOptionsCache() {
  cachedPromise = null
}

// React Hook：挂载时加载一次，返回 {users, loading}
export function useUserModelOptions() {
  const [users, setUsers] = useState([])
  const [loading, setLoading] = useState(__APP_ROLE__ === 'manager')
  useEffect(() => {
    if (__APP_ROLE__ !== 'manager') return
    let alive = true
    loadUserModelOptions().then((list) => {
      if (!alive) return
      setUsers(list)
      setLoading(false)
    })
    return () => { alive = false }
  }, [])
  return { users, loading }
}

// 从选项列表中取指定用户的模型名数组
export function modelNamesOf(users, userName) {
  const u = (users || []).find((x) => x.user_name === userName)
  return (u && u.model_names) || []
}

// 全站模型名并集（去重）
export function allModelNames(users) {
  const set = new Set()
  ;(users || []).forEach((u) => (u.model_names || []).forEach((m) => set.add(m)))
  return Array.from(set)
}
