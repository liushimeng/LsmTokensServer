// 用户名+模型名级联下拉选项（双端统一入口）
// - 管理端（manager）：UserModelOptionsInterface 全站用户级联下拉
// - 用户端（user）：UserModelListInterface 本人模型名下拉（阶段Y，登录态即身份，无用户名控件）
// 页面生命周期内模块级缓存：同一页面（含路由切换不刷新的 SPA 内）所有页面
// 共享一次请求；浏览器刷新即重建模块、缓存失效自动重取，避免点一下查一次。
import { useEffect, useState } from 'react'
import { get, post } from './api'

let cachedPromise = null
let myModelsCache = null

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
  myModelsCache = null
}

// 用户端：加载（并缓存）当前登录用户本人的模型名数组（登录态即身份，无需用户名参数）
export function loadMyModelNames() {
  if (__APP_ROLE__ !== 'user') return Promise.resolve([])
  if (!myModelsCache) {
    myModelsCache = post('UserModelListInterface', {})
      .then((d) => ((d && d.data) || []).map((m) => m.model_name).filter(Boolean))
      .catch(() => { myModelsCache = null; return [] })
  }
  return myModelsCache
}

// React Hook（用户端）：挂载时加载一次本人模型名，返回 {modelNames, loading}
// SPA 路由切换共享模块缓存，全站用户端仅请求一次；浏览器刷新自动重取
export function useMyModelNames() {
  const [modelNames, setModelNames] = useState([])
  const [loading, setLoading] = useState(__APP_ROLE__ === 'user')
  useEffect(() => {
    if (__APP_ROLE__ !== 'user') return
    let alive = true
    loadMyModelNames().then((list) => {
      if (!alive) return
      setModelNames(list)
      setLoading(false)
    })
    return () => { alive = false }
  }, [])
  return { modelNames, loading }
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
