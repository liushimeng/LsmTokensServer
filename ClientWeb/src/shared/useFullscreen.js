// 真·全屏状态机 Hook（Element Fullscreen API + CSS 降级双态）
//
// 语义：
//   mode === 'native'   —— 目标元素已通过 requestFullscreen() 进入整屏（top layer，跳出浏览器框架）
//   mode === 'fallback' —— 浏览器不支持/被拒绝，降级为页面内 CSS 最大化（position:fixed; inset:0）
//   mode === 'none'     —— 常规内联展示
//
// 设计要点：
//   1. 原生态以 document.fullscreenElement 为唯一事实来源（订阅 fullscreenchange），
//      用户按 Esc、点浏览器自带「退出全屏」、切标签页等外部退出都能同步回 'none'；
//      原生态不注册 keydown(Escape) —— 由浏览器自己处理，避免抢事件导致状态错乱。
//   2. 只认「自己这个 DOM」在全屏（getActiveElement() === targetRef.current），
//      多条记录同时展开时互不误伤，也不会把别行的全屏踢掉。
//   3. 组件卸载（收起详情行 / 翻页 / 路由切换）时，若全屏元素正是自己则主动退出，不留残留。
//   4. 不支持原生全屏（iOS Safari、iframe 无 allow="fullscreen"、非用户手势 reject）→
//      自动降级并置 nativeUnavailable，UI 侧用于 title 提示。
import { useCallback, useEffect, useRef, useState } from 'react'
import {
  pickFullscreenApi, getActiveElement, enterFullscreen, exitFullscreen,
} from './fullscreen'

export default function useFullscreen(targetRef) {
  const apiRef = useRef(null)
  const [mode, setMode] = useState('none')
  // nativeUnavailable：原生全屏不可用（不支持或被 reject），已在降级态
  const [nativeUnavailable, setNativeUnavailable] = useState(false)
  // 稳定读取最新 mode，保证 toggle 引用不变（传给子组件/事件处理器不会 stale）
  const modeRef = useRef(mode)
  useEffect(() => { modeRef.current = mode }, [mode])

  const getApi = useCallback(() => {
    if (apiRef.current === null) apiRef.current = pickFullscreenApi()
    return apiRef.current
  }, [])

  // 订阅全屏变化：仅同步原生态（降级态由状态机自身管理，不受原生事件干扰）
  useEffect(() => {
    const api = getApi()
    if (!api.supported || typeof document.addEventListener !== 'function') return undefined
    const onChange = () => {
      const active = getActiveElement(api, document)
      setMode((m) => {
        if (m !== 'native') return m
        return active && active === targetRef.current ? 'native' : 'none'
      })
    }
    document.addEventListener(api.changeEvent, onChange)
    return () => document.removeEventListener(api.changeEvent, onChange)
  }, [getApi, targetRef])

  const enter = useCallback(async () => {
    const el = targetRef.current
    if (!el) return
    const api = getApi()
    if (!api.supported) {
      setNativeUnavailable(true)
      setMode('fallback')
      return
    }
    const r = await enterFullscreen(api, el, document)
    if (r.ok) {
      setNativeUnavailable(false)
      setMode('native')
    } else {
      setNativeUnavailable(true)
      setMode('fallback')
    }
  }, [getApi, targetRef])

  const exit = useCallback(async () => {
    const api = getApi()
    const el = targetRef.current
    if (api.supported && el && getActiveElement(api, document) === el) {
      await exitFullscreen(api, document)
    }
    setMode('none')
  }, [getApi, targetRef])

  const toggle = useCallback(() => {
    if (modeRef.current !== 'none') { exit(); return }
    enter()
  }, [enter, exit])

  // 卸载兜底：全屏元素是自己 → 主动退出（收起行/翻页/切路由时不留残留全屏）
  useEffect(() => () => {
    const api = apiRef.current
    if (!api || !api.supported) return
    const el = targetRef.current
    if (el && getActiveElement(api, document) === el) exitFullscreen(api, document)
  }, [targetRef])

  return {
    mode,
    isFullscreen: mode !== 'none',
    isNative: mode === 'native',
    isFallback: mode === 'fallback',
    nativeUnavailable,
    enter,
    exit,
    toggle,
  }
}
