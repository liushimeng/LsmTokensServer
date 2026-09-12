#!/usr/bin/env node
// i18n 键完整性自检：提取源码全部 t('key') 引用，逐一核对三语 locale 是否定义。
// 任一缺失 → 退出码 1（供 npm run check / rebuild 前置门禁使用）。
// 背景：t() 对缺失键返回键名本身（truthy），调用处的 `|| '共 N 条'` 兜底永不生效，
// 页面会直接显示 chatAnalysis.totalCount 之类原始键名（2026-09-13 崩溃排查发现的配套回归）。
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const ROOT = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src')

function walk(dir, out) {
  for (const name of fs.readdirSync(dir)) {
    const p = path.join(dir, name)
    if (fs.statSync(p).isDirectory()) walk(p, out)
    else if (/\.(jsx?|mjs)$/.test(name)) out.push(p)
  }
  return out
}

const files = walk(ROOT, [])
const used = new Map() // key -> [file:line]
for (const f of files) {
  const lines = fs.readFileSync(f, 'utf8').split('\n')
  lines.forEach((ln, i) => {
    for (const m of ln.matchAll(/\bt\(\s*['"]([^'"]+)['"]/g)) {
      if (!used.has(m[1])) used.set(m[1], [])
      used.get(m[1]).push(`${path.relative(ROOT, f)}:${i + 1}`)
    }
  })
}

const LANGS = ['zh-CN', 'en', 'ja']
const locales = LANGS.map((l) => JSON.parse(fs.readFileSync(path.join(ROOT, 'i18n/locales', `${l}.json`), 'utf8')))
const missing = []
for (const [key, refs] of used) {
  const miss = LANGS.filter((_, i) => locales[i][key] === undefined)
  if (miss.length) missing.push({ key, miss, ref: refs[0] })
}

if (!missing.length) {
  console.log(`✅ i18n 自检通过：${used.size} 个引用键三语齐全`)
  process.exit(0)
}
console.error(`❌ i18n 自检失败：${missing.length} 个键缺失（页面将显示原始键名）：`)
for (const m of missing) console.error(`  ${m.key}  缺失语言: ${m.miss.join(',')}  首处引用: src/${m.ref}`)
process.exit(1)
