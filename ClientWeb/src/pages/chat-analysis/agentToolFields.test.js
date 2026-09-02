// ClientWeb/src/pages/chat-analysis/agentToolFields.test.js
//
// ChatAnalysis AgentTool* 三字段显示的轻量自检脚本（无第三方测试框架）。
// 覆盖：
//   1. 三语 i18n key（chatAnalysis.agentToolInfo / chatAnalysis.agentToolBlock）完整性
//   2. Agent工具信息 列渲染纯函数（数据源 tool_identifier 字段；长截断 + 空降级 + title 属性）
//   3. Agent 工具详情块条件渲染逻辑（三字段全空 → 不渲染；任一非空 → 渲染对应项）
//
// 运行：
//   cd ClientWeb && node src/pages/chat-analysis/agentToolFields.test.js

import fs from 'node:fs'
import path from 'node:path'
import url from 'node:url'

const __dirname = path.dirname(url.fileURLToPath(import.meta.url))
const localesDir = path.resolve(__dirname, '../../i18n/locales')
function loadJson(name) {
  return JSON.parse(fs.readFileSync(path.join(localesDir, name), 'utf8'))
}
const zhCN = loadJson('zh-CN.json')
const en = loadJson('en.json')
const ja = loadJson('ja.json')

let pass = 0
let fail = 0

function ok(cond, name) {
  if (cond) {
    pass++;
    // eslint-disable-next-line no-console
    console.log(`  ✓ ${name}`)
  } else {
    fail++;
    // eslint-disable-next-line no-console
    console.error(`  ✗ ${name}`)
  }
}

// ===== 1. 三语 i18n key 完整性 =====
const requiredKeys = ['chatAnalysis.agentToolInfo', 'chatAnalysis.agentToolBlock']
for (const key of requiredKeys) {
  ok(typeof zhCN[key] === 'string' && zhCN[key].length > 0, `[zh-CN] ${key} 非空`)
  ok(typeof en[key] === 'string' && en[key].length > 0, `[en]    ${key} 非空`)
  ok(typeof ja[key] === 'string' && ja[key].length > 0, `[ja]    ${key} 非空`)
}

// 保留旧 key（避免破坏阶段 BD 已有的展示）
const preservedKeys = ['chatAnalysis.agentTool', 'chatAnalysis.agentSessionId', 'chatAnalysis.sessionId']
for (const key of preservedKeys) {
  ok(typeof zhCN[key] === 'string' && zhCN[key].length > 0, `[zh-CN] 保留 ${key} 非空`)
  ok(typeof en[key] === 'string' && en[key].length > 0, `[en]    保留 ${key} 非空`)
  ok(typeof ja[key] === 'string' && ja[key].length > 0, `[ja]    保留 ${key} 非空`)
}

// ===== 2. Agent工具信息 列渲染纯函数（与 index.jsx 的 render 行为一致） =====
// v2.0.7x：该列数据源由 agent_tool_info 字段切换为 tool_identifier 字段，渲染逻辑不变。
// 重构自 index.jsx 的 render 表达式：
//   v => v ? <span title={v}>{String(v).length > 60 ? String(v).slice(0, 60) + '…' : v}</span> : '-'
function renderToolIdentifier(v) {
  if (!v) return '-'
  const s = String(v)
  if (s.length > 60) {
    return { tag: 'span', attrs: { title: s }, text: s.slice(0, 60) + '…' }
  }
  return { tag: 'span', attrs: { title: s }, text: s }
}

// 2.1 空值 / null / undefined / 空串 → '-'
ok(renderToolIdentifier(null) === '-', 'null → "-"')
ok(renderToolIdentifier(undefined) === '-', 'undefined → "-"')
ok(renderToolIdentifier('') === '-', '空字符串 → "-"')

// 2.2 短字符串（含版本号 / 运行时）原样展示 + title 属性完整
const short = 'claude-cli/1.0.27 (external, cli)'
const shortOut = renderToolIdentifier(short)
ok(shortOut.tag === 'span', '短字符串包 span')
ok(shortOut.text === short, '短字符串原样展示')
ok(shortOut.attrs.title === short, '短字符串 title 完整')

// 2.3 长字符串（> 60 字符）截断展示 + title 完整
const long = 'a'.repeat(80)
const longOut = renderToolIdentifier(long)
ok(longOut.text.length === 61, '长字符串截断到 60 字符 + ellipsis（总长 61）')
ok(longOut.text.endsWith('…'), '长字符串以 ellipsis 结尾')
ok(longOut.attrs.title === long, '长字符串 title 保留完整 80 字符')
ok(longOut.attrs.title.length === 80, '长字符串 title 长度 = 80')

// 2.4 边界 60 字符恰好
const sixty = 'b'.repeat(60)
const sixtyOut = renderToolIdentifier(sixty)
ok(sixtyOut.text === sixty, '恰好 60 字符不截断（边界正确）')
ok(sixtyOut.text.length === 60, '恰好 60 字符总长 = 60')

// 2.5 边界 61 字符截断
const sixtyOne = 'c'.repeat(61)
const sixtyOneOut = renderToolIdentifier(sixtyOne)
ok(sixtyOneOut.text.length === 61, '61 字符截断（60 + ellipsis）')
ok(sixtyOneOut.text.endsWith('…'), '61 字符以 ellipsis 结尾')

// ===== 3. Agent 工具详情块条件渲染逻辑（与 DetailHeader.jsx 行为一致） =====
// v2.0.7x：详情块「Agent工具信息」项数据源由 agent_tool_info 字段切换为 tool_identifier 字段。
// 重构自 DetailHeader.jsx 的三元判断：
//   const hasAny = row.agent_tool_name || row.tool_identifier || row.agent_tool_session_id
//   const itemName = row.agent_tool_name ? { label: '...', value: row.agent_tool_name } : null
//   const itemInfo = row.tool_identifier ? { ... } : null
//   const itemSession = row.agent_tool_session_id ? { ... } : null
function buildAgentBlock(row) {
  const items = []
  if (!row) return null
  if (row.agent_tool_name) items.push({ key: 'name', label: 'agentTool', value: row.agent_tool_name })
  if (row.tool_identifier) items.push({ key: 'info', label: 'agentToolInfo', value: row.tool_identifier })
  if (row.agent_tool_session_id) items.push({ key: 'session', label: 'agentSessionId', value: row.agent_tool_session_id })
  return items.length ? items : null
}

// 3.1 三字段全空 → 不渲染
ok(buildAgentBlock({ agent_tool_name: '', tool_identifier: '', agent_tool_session_id: '' }) === null, '三字段全空 → 不渲染')
ok(buildAgentBlock({}) === null, '三字段缺失 → 不渲染')
ok(buildAgentBlock(null) === null, 'row 为 null → 不渲染')

// 3.2 单字段非空 → 仅渲染该项
const onlyName = buildAgentBlock({ agent_tool_name: 'claude-cli' })
ok(Array.isArray(onlyName) && onlyName.length === 1, '仅 agent_tool_name → 1 项')
ok(onlyName[0].key === 'name' && onlyName[0].value === 'claude-cli', 'name 项 value 匹配')

const onlyInfo = buildAgentBlock({ tool_identifier: 'claude-cli/1.0.27 (external, cli)' })
ok(Array.isArray(onlyInfo) && onlyInfo.length === 1, '仅 tool_identifier → 1 项')
ok(onlyInfo[0].key === 'info' && onlyInfo[0].value === 'claude-cli/1.0.27 (external, cli)', 'info 项 value 匹配')

const onlySession = buildAgentBlock({ agent_tool_session_id: 'sess-abc-123' })
ok(Array.isArray(onlySession) && onlySession.length === 1, '仅 agent_tool_session_id → 1 项')
ok(onlySession[0].key === 'session' && onlySession[0].value === 'sess-abc-123', 'session 项 value 匹配')

// 3.3 三字段均有值 → 渲染 3 项，按固定顺序 name → info → session
const all = buildAgentBlock({
  agent_tool_name: 'claude-cli',
  tool_identifier: 'claude-cli/1.0.27 (external, cli)',
  agent_tool_session_id: 'sess-xyz-456',
})
ok(Array.isArray(all) && all.length === 3, '三字段全有 → 3 项')
ok(all[0].key === 'name' && all[1].key === 'info' && all[2].key === 'session', '三字段顺序：name → info → session')

// 3.4 任意两字段组合
const nameInfo = buildAgentBlock({ agent_tool_name: 'opencode', tool_identifier: 'opencode/0.5.0' })
ok(nameInfo.length === 2 && nameInfo[0].key === 'name' && nameInfo[1].key === 'info', 'name + info 组合')
const nameSess = buildAgentBlock({ agent_tool_name: 'claude-cli', agent_tool_session_id: 'sess-789' })
ok(nameSess.length === 2 && nameSess[0].key === 'name' && nameSess[1].key === 'session', 'name + session 组合')
const infoSess = buildAgentBlock({ tool_identifier: 'codex/0.1.0', agent_tool_session_id: 'sess-000' })
ok(infoSess.length === 2 && infoSess[0].key === 'info' && infoSess[1].key === 'session', 'info + session 组合')

// 3.5 详情块不展示 session_id 归一化逻辑——只展示原生 agent_tool_session_id
const detailBlock = buildAgentBlock({
  agent_tool_name: 'claude-cli',
  agent_tool_session_id: '',
  session_id: 'self_generate_12345', // 列表列会降级展示；详情块仅看 agent_tool_session_id
})
ok(detailBlock.length === 1 && detailBlock[0].key === 'name', 'agent_tool_session_id 为空时，详情块不展示合成 session_id')

console.log(`\n${pass} passed, ${fail} failed`)
process.exit(fail ? 1 : 0)
