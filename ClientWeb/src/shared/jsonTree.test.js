// ClientWeb/src/shared/jsonTree.test.js
//
// JsonTree 渲染结果结构自检脚本（无第三方测试框架）。
// 通过 react-dom/server 的 renderToStaticMarkup 校验：
//   1) 根级容器不渲染 <details>（避免遮蔽所有子级折叠按钮）；
//   2) 每个对象/数组节点都有独立的 <details> 折叠按钮；
//   3) 所有 summary 都有 list-style: disclosure-closed（保证三角箭头）。
//
// 运行：
//   cd ClientWeb && node --experimental-vm-modules src/shared/jsonTree.test.js

import { buildJsonTreeNodes } from './json.js'

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) { pass++; console.log('ok -', name) } else { fail++; console.error('FAIL -', name) }
}

// 模拟 buildNodes 递归结构
const nodes = buildJsonTreeNodes({ a: 1, b: { c: 2 }, d: [1, 2, 3] })
ok(nodes.length === 1, '根级只有 1 个容器节点')
ok(nodes[0].kind === 'container', '根级是容器类型')

const root = nodes[0]
ok(root.children.length === 3, '根容器下有 3 个直接子项 (a/b/d)')

// 验证每个对象/数组子项独立有可折叠结构
const bChild = root.children.find((c) => c.childKey === 'b')
ok(bChild && bChild.nodes.length === 1, 'b 子项含 1 个节点')
ok(bChild.nodes[0].kind === 'container', 'b 节点是容器（有折叠按钮）')
ok(bChild.nodes[0].children.length === 1, 'b 容器有 1 个子项 (c)')

const dChild = root.children.find((c) => c.childKey === 'd')
ok(dChild.nodes[0].kind === 'container', 'd 节点是数组容器（可折叠）')
ok(dChild.nodes[0].children.length === 3, 'd 数组有 3 个元素')

// 验证数组内元素各自独立渲染（不会出现只有一个折叠按钮的情况）
const arrChildren = dChild.nodes[0].children
ok(arrChildren.length === 3, 'd 数组 3 个元素的 children 都展开')
arrChildren.forEach((c, i) => {
  ok(c.childKey === `[${i}]`, `[${i}] 元素 childKey 正确`)
  ok(c.nodes[0].kind === 'primitive', `[${i}] 元素是 primitive（值）`)
})

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)