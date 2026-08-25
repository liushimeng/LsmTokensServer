// ClientWeb/src/shared/json.test.js
//
// JSON 共用工具的轻量自检脚本（无第三方测试框架）。
//
// 运行：
//   node src/shared/json.test.js

import { prettyJSON } from './json.js'

let pass = 0
let fail = 0

function eq(a, b, name) {
  if (a === b) {
    pass++
    // eslint-disable-next-line no-console
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    // eslint-disable-next-line no-console
    console.error(`  ✗ ${name}`)
    // eslint-disable-next-line no-console
    console.error(`    expected: ${b}`)
    // eslint-disable-next-line no-console
    console.error(`    actual:   ${a}`)
  }
}

function section(t) {
  // eslint-disable-next-line no-console
  console.log(`\n${t}`)
}

section('prettyJSON')
eq(prettyJSON(''), '', '空字符串 → ""')
eq(prettyJSON(null), '', 'null → ""')
eq(prettyJSON('not a json'), 'not a json', '非法 JSON → 原样返回')
eq(prettyJSON('{"a":1}'), '{\n  "a": 1\n}', '合法 JSON 美化缩进 2')
eq(prettyJSON('[1,2,3]'), '[\n  1,\n  2,\n  3\n]', '数组美化')

// eslint-disable-next-line no-console
console.log(`\n总计：${pass} 通过 / ${fail} 失败`)
if (fail > 0) process.exit(1)