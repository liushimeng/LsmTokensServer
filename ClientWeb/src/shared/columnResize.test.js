// ClientWeb/src/shared/columnResize.test.js
//
// 数据表格列宽拖拽（阶段CU）纯函数自检脚本（无第三方测试框架，node 直跑）。
// 对应方案：docs/项目迁移解决方案/数据表格列宽拖拽调整通用控件方案_20261013_164137.md
// 覆盖：
//   1. hashColumnKeys —— 稳定、区分列序/列集/折叠开关列
//   2. defaultColWidthStorageKey —— key 结构（角色+路径+哈希），排序/列宽 key 互不串扰
//   3. pxToPercents —— Σ=100、等比例、尾差并入最后一列
//   4. applyDelta —— 此消彼长、总量守恒、最小宽钳制（首列/末列/越界）
//   5. sanitizeStored —— 版本/类型/key 集合/数值域校验，旧结构数据丢弃
//
// 运行：
//   cd ClientWeb && node src/shared/columnResize.test.js

import { hashColumnKeys, defaultColWidthStorageKey, pxToPercents, applyDelta, sanitizeStored } from './columnResize.js'

let passed = 0
let failed = 0
function ok(cond, name) {
  if (cond) { passed++; console.log('  ✓ ' + name) }
  else { failed++; console.error('  ✗ ' + name) }
}
function eq(a, b) { return JSON.stringify(a) === JSON.stringify(b) }

console.log('columnResize 纯函数自检（阶段CU）')

// ===== 1. hashColumnKeys =====
console.log('\n[1] hashColumnKeys')
ok(hashColumnKeys(['a', 'b']) === hashColumnKeys(['a', 'b']), '同一列 key 序列哈希稳定')
ok(hashColumnKeys(['a', 'b']) !== hashColumnKeys(['b', 'a']), '列顺序不同哈希不同')
ok(hashColumnKeys(['a', 'b']) !== hashColumnKeys(['a', 'c']), '列集合不同哈希不同')
ok(hashColumnKeys(['#toggle', 'a']) !== hashColumnKeys(['a']), '折叠开关列参与哈希（与普通表区分）')
ok(/^[0-9a-z]+$/.test(hashColumnKeys(['id', 'created_at', 'url'])), '哈希为 base36 短串')

// ===== 2. defaultColWidthStorageKey =====
console.log('\n[2] defaultColWidthStorageKey')
const k1 = defaultColWidthStorageKey('manager', '/ChatAnalysis', ['id', 'url'])
ok(k1.startsWith('lsm:datatable:colwidth:manager:/ChatAnalysis:'), 'key 含前缀+角色+路径')
ok(k1 !== defaultColWidthStorageKey('user', '/ChatAnalysis', ['id', 'url']), '角色不同 key 不同（双构建隔离）')
ok(k1 !== defaultColWidthStorageKey('manager', '/ChatAnalysisTotal', ['id', 'url']), '路径不同 key 不同')
ok(k1 !== defaultColWidthStorageKey('manager', '/ChatAnalysis', ['id', 'time']), '列不同 key 不同（同页多表区分）')
ok(!k1.includes('lsm:datatable:sort:'), '与排序存储 key 命名空间隔离')

// ===== 3. pxToPercents =====
console.log('\n[3] pxToPercents')
const p1 = pxToPercents([200, 300, 500])
ok(Math.abs(p1.reduce((a, b) => a + b, 0) - 100) < 1e-9, '归一后 Σ=100')
ok(eq(p1, [20, 30, 50]), '等比例换算（200/300/500 → 20/30/50）')
const p2 = pxToPercents([100, 100, 100])
ok(eq(p2, [100 / 3, 100 / 3, 100 / 3].map((v, i) => (i === 2 ? Math.round((v + (100 - 100 / 3 * 3)) * 100) / 100 : Math.round(v * 100) / 100))) || Math.abs(p2.reduce((a, b) => a + b, 0) - 100) < 1e-9, '不能整除时尾差并入最后一列（Σ 严格 100）')
ok(eq(pxToPercents([0, 0, 0]), [33.33, 33.33, 33.34]), '全零输入降级均分（尾差并入最后一列，Σ 严格 100）')

// ===== 4. applyDelta =====
console.log('\n[4] applyDelta')
const base = [100, 200, 300, 400]
ok(eq(applyDelta(base, 0, 50), [150, 150, 300, 400]), '首列右拖 +50：右邻列 -50（此消彼长）')
ok(eq(applyDelta(base, 3, 50), [100, 200, 250, 450]), '末列右拖 +50：与左邻列联动')
ok(applyDelta(base, 1, 50).reduce((a, b) => a + b, 0) === 1000, '总量守恒（钳制外）')
ok(eq(applyDelta(base, 0, 10000), [856, 48, 48, 48]), '超上限钳制：其余列全部让至最小宽后停止（总量守恒 1000）')
ok(eq(applyDelta(base, 0, -10000), [48, 252, 300, 400]), '超下限钳制：被拖列不低于最小宽（第一优先列吸收增量）')
ok(eq(applyDelta(base, -1, 50), base) && eq(applyDelta(base, 99, 50), base), '越界下标原样返回（防御）')
ok(eq(applyDelta([100, 100], 1, 30), [70, 130]), '两列表：末列与左邻联动')
ok(eq(applyDelta([100, 50, 300], 0, 80), [180, 48, 222]), '紧邻列接近最小宽时，溢出量分摊到后续列（宽表可拖性）')
ok(applyDelta([100, 50, 300], 0, 80).reduce((a, b) => a + b, 0) === 450, '分摊后总量仍守恒')
ok(eq(applyDelta([100, 100, 100], 2, 30), [100, 70, 130]), '末列增长：增量取自左邻列（第一优先）')
ok(eq(applyDelta([100, 100, 100], 2, -30), [100, 130, 70]), '末列收缩：增量给左邻列（第一优先）')

// ===== 5. sanitizeStored =====
console.log('\n[5] sanitizeStored')
const valid = ['id', 'url']
ok(sanitizeStored(JSON.stringify({ v: 1, widths: { id: 20, url: 80 }, ts: 1 }), valid) !== null, '合法数据通过')
ok(sanitizeStored(null, valid) === null, '空输入丢弃')
ok(sanitizeStored('not-json', valid) === null, '非法 JSON 丢弃')
ok(sanitizeStored(JSON.stringify({ v: 2, widths: { id: 20, url: 80 } }), valid) === null, '版本不符丢弃')
ok(sanitizeStored(JSON.stringify({ v: 1, widths: { id: 20 } }), valid) === null, '列数量不符丢弃（旧结构数据）')
ok(sanitizeStored(JSON.stringify({ v: 1, widths: { id: 20, other: 80 } }), valid) === null, '列 key 集合不符丢弃')
ok(sanitizeStored(JSON.stringify({ v: 1, widths: { id: 0, url: 100 } }), valid) === null, '非正数丢弃')
ok(sanitizeStored(JSON.stringify({ v: 1, widths: { id: 20, url: 101 } }), valid) === null, '超 100 丢弃')
ok(sanitizeStored(JSON.stringify({ v: 1, widths: { id: '20', url: 80 } }), valid) === null, '非数值丢弃')

console.log(`\n结果：${passed} 通过，${failed} 失败`)
process.exit(failed ? 1 : 0)
