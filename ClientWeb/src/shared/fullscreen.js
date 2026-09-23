// Element Fullscreen API 适配层（零依赖纯函数）
//
// 为什么需要它：对话详情「全屏」按钮要的是「跳出浏览器页面框架、在整个窗口全屏显示」。
// JS 无法触发浏览器自身的 F11（那是浏览器 chrome 开关），唯一正确手段是
// Element Fullscreen API —— el.requestFullscreen() 会把元素放入 top layer，
// 铺满整块屏幕并隐藏地址栏/标签栏/书签栏。
//
// 兼容性覆盖：
//   - 标准：requestFullscreen / exitFullscreen / fullscreenElement / fullscreenchange
//   - 旧 WebKit：webkitRequestFullscreen / webkitExitFullscreen / webkitFullscreenElement
//     / webkitfullscreenchange（不接收 options 参数）
//   - 完全不支持（iOS Safari 等）：supported=false，调用方降级为页面内 CSS 最大化
//
// 约定：本模块不得 import 任何依赖（含无扩展名导入），以便 `node xxx.test.js` 直接加载自检。

// VENDOR 顺序即优先级：标准名优先，其次旧 WebKit 前缀。
const VENDOR = [
  {
    requestName: 'requestFullscreen',
    exitName: 'exitFullscreen',
    elementName: 'fullscreenElement',
    changeEvent: 'fullscreenchange',
    errorEvent: 'fullscreenerror',
    prefixed: false,
  },
  {
    requestName: 'webkitRequestFullscreen',
    exitName: 'webkitExitFullscreen',
    elementName: 'webkitFullscreenElement',
    changeEvent: 'webkitfullscreenchange',
    errorEvent: 'webkitfullscreenerror',
    prefixed: true,
  },
]

const UNSUPPORTED = {
  supported: false,
  requestName: '',
  exitName: '',
  elementName: '',
  changeEvent: '',
  errorEvent: '',
  prefixed: false,
}

// defaultDoc 取全局 document（SSR / node 环境下为 undefined）
function defaultDoc() {
  return typeof document !== 'undefined' ? document : null
}

// pickFullscreenApi 探测当前文档可用的全屏实现。
// 判定依据：documentElement 上是否存在对应方法（比特征字符串更可靠）。
export function pickFullscreenApi(doc = defaultDoc()) {
  const root = doc && doc.documentElement
  if (!root) return { ...UNSUPPORTED }
  const hit = VENDOR.find((v) => typeof root[v.requestName] === 'function')
  if (!hit) return { ...UNSUPPORTED }
  return { supported: true, ...hit }
}

// getActiveElement 当前处于全屏的元素（无则 null）
export function getActiveElement(api, doc = defaultDoc()) {
  if (!api || !api.supported || !doc) return null
  return doc[api.elementName] || null
}

// isTargetActive 全屏中的元素是否就是目标元素（多行同时展开时避免互相误判）
export function isTargetActive(api, doc, el) {
  if (!el) return false
  return getActiveElement(api, doc) === el
}

// errText 从异常 / 字符串里取可读信息（仅用于调试与提示，不参与逻辑判定）
function errText(e) {
  if (!e) return 'unknown'
  if (typeof e === 'string') return e
  return String(e.message || e.name || e)
}

// enterFullscreen 请求目标元素进入原生全屏。
// 一律 resolve，不 reject：调用方据 ok=false 走降级分支（页面内 CSS 最大化）。
// 非用户手势、iframe 缺 allow="fullscreen"、iOS Safari 等都会走到 ok=false。
export function enterFullscreen(api, el, doc = defaultDoc()) {
  if (!api || !api.supported || !el) {
    return Promise.resolve({ ok: false, error: 'unsupported' })
  }
  const fn = el[api.requestName]
  if (typeof fn !== 'function') {
    return Promise.resolve({ ok: false, error: 'method-missing' })
  }
  let ret
  try {
    // 标准实现支持 options（navigationUI:'hide' 隐藏 Android 后退条）；旧 WebKit 不传参
    ret = api.prefixed ? fn.call(el) : fn.call(el, { navigationUI: 'hide' })
  } catch (e) {
    return Promise.resolve({ ok: false, error: errText(e) })
  }
  // 老实现可能返回 undefined（非 Promise）：以全屏元素结果为准
  if (!ret || typeof ret.then !== 'function') {
    const ok = isTargetActive(api, doc, el)
    return Promise.resolve({ ok, error: ok ? '' : 'not-active' })
  }
  return ret.then(
    () => ({ ok: true, error: '' }),
    (e) => ({ ok: false, error: errText(e) }),
  )
}

// exitFullscreen 退出原生全屏（无元素在全屏时直接 resolve ok）
export function exitFullscreen(api, doc = defaultDoc()) {
  if (!api || !api.supported || !doc) {
    return Promise.resolve({ ok: false, error: 'unsupported' })
  }
  if (!getActiveElement(api, doc)) return Promise.resolve({ ok: true, error: '' })
  const fn = doc[api.exitName]
  if (typeof fn !== 'function') return Promise.resolve({ ok: false, error: 'method-missing' })
  let ret
  try {
    ret = fn.call(doc)
  } catch (e) {
    return Promise.resolve({ ok: false, error: errText(e) })
  }
  if (!ret || typeof ret.then !== 'function') return Promise.resolve({ ok: true, error: '' })
  return ret.then(
    () => ({ ok: true, error: '' }),
    (e) => ({ ok: false, error: errText(e) }),
  )
}
