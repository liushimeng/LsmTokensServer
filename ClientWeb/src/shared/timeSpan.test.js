// ClientWeb/src/shared/timeSpan.test.js
//
// 时间跨度动态档位纯逻辑自检脚本（无第三方测试框架）。
//
// 运行：
//   node src/shared/timeSpan.test.js

import {
  buildSpanLevels,
  nearestSpan,
  normalizeConfig,
  spanLabel,
  spanToHours,
  hoursToSpan,
} from './timeSpan.js'

let pass = 0
let fail = 0

function eq(a, b, name) {
  const ja = JSON.stringify(a)
  const jb = JSON.stringify(b)
  if (ja === jb) {
    pass++
    // eslint-disable-next-line no-console
    console.log(`  ✓ ${name}`)
  } else {
    fail++
    // eslint-disable-next-line no-console
    console.error(`  ✗ ${name}\n      got  ${ja}\n      want ${jb}`)
  }
}

// ---------- span 编码换算 ----------
eq(spanToHours(0), 0, 'spanToHours(0)=0')
eq(spanToHours(3), 72, 'spanToHours(3)=72')
eq(spanToHours(-6), 6, 'spanToHours(-6)=6')
eq(hoursToSpan(1), -1, 'hoursToSpan(1)=-1')
eq(hoursToSpan(18), -18, 'hoursToSpan(18)=-18')
eq(hoursToSpan(24), 1, 'hoursToSpan(24)=1')
eq(hoursToSpan(1104), 46, 'hoursToSpan(1104)=46')

// ---------- 档位生成：结构不变量 ----------
for (const maxH of [48, 168, 744, 1104, 2184, 8760]) {
  const levels = buildSpanLevels(maxH)
  eq(levels.length, 10, `maxH=${maxH} 恰好 10 档`)
  eq(levels[0].hours, 1, `maxH=${maxH} 首档 1 小时`)
  eq(levels[levels.length - 1].hours, maxH, `maxH=${maxH} 末档=上限`)
  let asc = true
  for (let i = 1; i < levels.length; i++) if (levels[i].hours <= levels[i - 1].hours) asc = false
  eq(asc, true, `maxH=${maxH} 严格递增无重复`)
}

// ---------- 档位生成：典型保留期 ----------
eq(
  buildSpanLevels(46 * 24).map((l) => l.span),
  [-1, -3, -6, -12, 1, 4, 7, 14, 30, 46],
  'retention=45 → 1h,3h,6h,12h,1d,4d,7d,14d,30d,46d',
)
eq(
  buildSpanLevels(33 * 24).map((l) => l.span),
  [-1, -3, -6, -12, 1, 3, 5, 10, 21, 33],
  'retention=32 → 1h,3h,6h,12h,1d,3d,5d,10d,21d,33d',
)
eq(
  buildSpanLevels(31 * 24).map((l) => l.span),
  [-1, -3, -6, -12, 1, 3, 5, 10, 21, 31],
  'retention=30 → 1h,3h,6h,12h,1d,3d,5d,10d,21d,31d',
)

// ---------- 旧值就近迁移 ----------
const levels45 = buildSpanLevels(46 * 24)
eq(nearestSpan(levels45, 3), 4, '旧值 3 天 → 就近 4d 档（45d 档位无 3d）')
eq(nearestSpan(buildSpanLevels(31 * 24), 3), 3, '旧值 3 天 → 3d 档（30d 档位含 3d）')
eq(nearestSpan(levels45, 0), 46, '旧值 0(全部) → 最大档 46d')
eq(nearestSpan(levels45, 90), 46, '旧值 90 天(超上限) → 最大档')
eq(nearestSpan(levels45, 60), 46, '旧值 60 天 → 就近 46d')
eq(nearestSpan(levels45, -2), -1, '旧值 2 小时 → 就近 1h')
eq(nearestSpan(levels45, '-6'), -6, '字符串旧值 -6 → -6 档')
eq(nearestSpan(null, 3), 3, '档位未加载时透传')

// ---------- 档位文案 ----------
eq(spanLabel(-3), { key: 'timeRange.lastNHours', vars: { n: 3 } }, 'label 3 小时')
eq(spanLabel(5), { key: 'timeRange.lastNDays', vars: { n: 5 } }, 'label 5 天')
eq(spanLabel(46, 46 * 24), { key: 'timeRange.maxDays', vars: { n: 46 } }, 'label 最大档(全部保留期)')

// ---------- 配置归一化 ----------
eq(normalizeConfig({ transaction_retention_days: 45, max_span_days: 46, max_span_hours: 1104 }), {
  retentionDays: 45, maxSpanDays: 46, maxSpanHours: 1104, minSpanHours: 1, levels: 10,
}, 'normalize 正常响应')
eq(normalizeConfig(null).maxSpanDays, 365, 'normalize 空响应回落 365 天')
eq(normalizeConfig({ transaction_retention_days: 0, max_span_days: 0 }).maxSpanDays, 365, 'normalize 禁用清理回落 365 天')
eq(normalizeConfig({ max_span_days: 9999 }).maxSpanDays, 365, 'normalize 超大值封顶 365')

// eslint-disable-next-line no-console
console.log(`\n${pass} passed, ${fail} failed`)
if (fail > 0) process.exit(1)
