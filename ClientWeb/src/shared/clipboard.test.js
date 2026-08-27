// ClientWeb/src/shared/clipboard.test.js
//
// copyToClipboard 轻量自检脚本（无第三方测试框架）。
//
// 运行：
//   cd ClientWeb && node src/shared/clipboard.test.js

import { copyToClipboard } from './clipboard.js'

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) { pass++; console.log('ok -', name) } else { fail++; console.error('FAIL -', name) }
}

// 1) 空值/非字符串应安全返回字符串
;(async () => {
  // jsdom/Node 18+ 没有 navigator.clipboard.writeText；这里只验证函数是 async 函数、不会抛同步异常
  // 复制真实成功与否依赖运行环境，不强行 await
  try {
    const p1 = copyToClipboard(null)
    const p2 = copyToClipboard(undefined)
    const p3 = copyToClipboard(123)
    ok(p1 && typeof p1.then === 'function', 'null 入参返回 Promise')
    ok(p2 && typeof p2.then === 'function', 'undefined 入参返回 Promise')
    ok(p3 && typeof p3.then === 'function', 'number 入参返回 Promise')
    // 在没有 document.execCommand 的 Node 环境，复制应解析为 false 而不是抛错
    const r1 = await p1
    ok(typeof r1 === 'boolean', 'null 入参返回 boolean')
  } catch (e) {
    fail++; console.error('FAIL - 异常:', e.message)
  }

  console.log(`\n${pass} passed, ${fail} failed`)
  process.exit(fail ? 1 : 0)
})()