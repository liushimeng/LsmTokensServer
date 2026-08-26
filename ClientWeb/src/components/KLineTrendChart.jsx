// KLineTrendChart：纯 SVG 双线趋势图（调用次数 + Tokens 数）
// 工程惯例：不引入第三方图表库（无 echarts/recharts/d3）。
//
// v2 重做要点：
//   1. SVG 等比缩放（preserveAspectRatio="xMidYMid meet"），轴刻度文字改用 HTML 浮层 → 字号不变形
//   2. 鼠标滚轮：以鼠标 X 为锚点缩放 X 轴；Ctrl/Cmd + 滚轮 缩放 Y；Shift + 滚轮 pan X
//   3. 左键拖拽框选：松手时缩放到选中区间
//   4. 跨度自适应桶粒度：≤48h 小时桶 / ≤720h 天桶 / >720h 周桶；前端二次聚合
//   5. 文案精简：单位紧凑显示（1.2k / 3.4M），完整数字保留在 tooltip
//
// props:
//   points: [{date:'YYYY-MM-DD HH:00',count,tokens_total,tokens_input,tokens_output}]
//   loading?: 是否显示加载遮罩
//   emptyHint?: 空数据提示
//   callLabel / tokenLabel: 左右轴名称（精简文案：调用次数 / Tokens）
//   callColor / tokenColor: 渐变起止色 [a, b]
//   animationMs?: 入场动画时长（默认 700）
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'

const VB_W = 720   // SVG viewBox 宽（等比缩放参考）
const VB_H = 280   // SVG viewBox 高
const PAD_T = 18
const PAD_B = 36
const PAD_L = 4    // SVG 内留少量边（轴文字外置）
const PAD_R = 4
const INNER_W = VB_W - PAD_L - PAD_R
const INNER_H = VB_H - PAD_T - PAD_B

const PALETTE = {
  callFrom: '#34d399', callTo: '#0ea5e9',
  tokenFrom: '#a855f7', tokenTo: '#ec4899',
  grid: '#e2e8f0',
  axis: '#94a3b8',
  text: '#475569',
  hover: '#0f172a',
  brush: 'rgba(14,165,233,0.18)',
  brushBorder: '#0ea5e9',
}

function fmtFull(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}
function fmtCompact(n) {
  n = Number(n) || 0
  const abs = Math.abs(n)
  if (abs >= 1_000_000) return (n / 1_000_000).toFixed(abs >= 10_000_000 ? 0 : 1).replace(/\.0$/, '') + 'M'
  if (abs >= 1_000) return (n / 1_000).toFixed(abs >= 10_000 ? 0 : 1).replace(/\.0$/, '') + 'k'
  return String(Math.round(n))
}
function niceTicks(maxVal, target = 4) {
  if (maxVal <= 0) return [0, 1]
  const exp = Math.floor(Math.log10(maxVal))
  const base = Math.pow(10, exp)
  const ratio = maxVal / base
  let nice
  if (ratio <= 1) nice = base
  else if (ratio <= 2) nice = 2 * base
  else if (ratio <= 5) nice = 5 * base
  else nice = 10 * base
  const step = nice / target
  const out = []
  for (let i = 0; i <= target; i++) out.push(Math.round(i * step))
  return out
}

// 解析 date 字段（"YYYY-MM-DD HH:00" 或 "YYYY-MM-DD"）为毫秒时间戳
function parseBucketTs(s) {
  if (!s) return NaN
  // 把空格替换为 "T"，浏览器对 "YYYY-MM-DDTHH:00" 可直接 parse
  const norm = s.replace(' ', 'T')
  const t = Date.parse(norm + (norm.length === 16 ? ':00' : ''))
  return t
}

function formatViewRange(fromTs, toTs) {
  if (!fromTs || !toTs) return ''
  const f = (ts, withTime) => {
    const d = new Date(ts)
    const ymd = `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    if (!withTime) return ymd
    return `${ymd} ${String(d.getHours()).padStart(2, '0')}:00`
  }
  // 同一天：省略前段日期
  const fromD = new Date(fromTs), toD = new Date(toTs)
  const sameDay = fromD.toDateString() === toD.toDateString()
  const fromHasTime = (toTs - fromTs) <= 48 * 3600_000
  return sameDay
    ? `${f(fromTs, true)} ~ ${f(toTs, true)}`
    : `${f(fromTs, fromHasTime)} ~ ${f(toTs, false)}`
}

// 二次聚合：根据视图跨度把 points 聚合成 day/week 桶
function aggregateByBucket(points, bucketSize) {
  if (bucketSize === 'hour' || !points.length) return points
  const keyOf = (ts) => {
    const d = new Date(ts)
    if (bucketSize === 'day') {
      return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
    }
    // week：以周一为起点（ISO 周）
    const day = (d.getDay() + 6) % 7 // 周一=0 ... 周日=6
    const mondayTs = ts - day * 86400_000
    const m = new Date(mondayTs)
    return `${m.getFullYear()}-${String(m.getMonth() + 1).padStart(2, '0')}-${String(m.getDate()).padStart(2, '0')}`
  }
  const out = new Map()
  for (const p of points) {
    const ts = parseBucketTs(p.date)
    if (!Number.isFinite(ts)) continue
    const k = keyOf(ts)
    const cur = out.get(k) || { date: k, count: 0, tokens_total: 0, tokens_input: 0, tokens_output: 0 }
    cur.count += p.count || 0
    cur.tokens_total += p.tokens_total || 0
    cur.tokens_input += p.tokens_input || 0
    cur.tokens_output += p.tokens_output || 0
    out.set(k, cur)
  }
  // 输出按日期升序
  const arr = Array.from(out.values()).sort((a, b) => a.date < b.date ? -1 : 1)
  return arr
}

function inferBucketSize(hourSpan) {
  if (!Number.isFinite(hourSpan)) return 'hour'
  if (hourSpan <= 48) return 'hour'
  if (hourSpan <= 720) return 'day'
  return 'week'
}

export default function KLineTrendChart(props) {
  const {
    points = [],
    loading = false,
    emptyHint = '—',
    callLabel = '调用次数',
    tokenLabel = 'Tokens',
    callColor = [PALETTE.callFrom, PALETTE.callTo],
    tokenColor = [PALETTE.tokenFrom, PALETTE.tokenTo],
    animationMs = 700,
    zoomHint,
    resetLabel,
    viewRangeTemplate,
    bucketTemplate,
  } = props

  const originalPoints = Array.isArray(points) ? points : []
  // 计算原始跨度（毫秒）
  const origSpan = useMemo(() => {
    if (originalPoints.length < 2) return 0
    const first = parseBucketTs(originalPoints[0].date)
    const last = parseBucketTs(originalPoints[originalPoints.length - 1].date)
    if (!Number.isFinite(first) || !Number.isFinite(last)) return 0
    return last - first
  }, [originalPoints])

  // 视图区间（毫秒时间戳）：初始为原始跨度
  const [view, setView] = useState({ from: NaN, to: NaN })
  useEffect(() => {
    if (!originalPoints.length) { setView({ from: NaN, to: NaN }); return }
    const first = parseBucketTs(originalPoints[0].date)
    const last = parseBucketTs(originalPoints[originalPoints.length - 1].date)
    setView({ from: first, to: last })
  }, [originalPoints])

  // 视图可见点 = 时间戳在 [view.from, view.to] 内的原始点
  const visibleRaw = useMemo(() => {
    if (!originalPoints.length) return []
    if (!Number.isFinite(view.from) || !Number.isFinite(view.to)) return originalPoints
    return originalPoints.filter((p) => {
      const t = parseBucketTs(p.date)
      return t >= view.from - 1 && t <= view.to + 1
    })
  }, [originalPoints, view])

  // 当前视图小时跨度
  const viewHourSpan = (view.to - view.from) / 3600_000
  const bucketSize = inferBucketSize(viewHourSpan)

  // 二次聚合（如需）
  const visible = useMemo(() => aggregateByBucket(visibleRaw, bucketSize), [visibleRaw, bucketSize])

  // ticks 与 max
  const maxCount = useMemo(() => Math.max(1, ...visible.map((p) => p.count || 0)), [visible])
  const maxToken = useMemo(() => Math.max(1, ...visible.map((p) => p.tokens_total || 0)), [visible])
  const countTicks = useMemo(() => niceTicks(maxCount), [maxCount])
  const tokenTicks = useMemo(() => niceTicks(maxToken), [maxToken])

  // 坐标映射
  const n = visible.length
  const xStep = n > 1 ? INNER_W / (n - 1) : 0
  const xAt = useCallback((i) => PAD_L + i * xStep, [xStep])
  const yCount = useCallback((v) => {
    const top = countTicks[countTicks.length - 1] || 1
    return PAD_T + INNER_H - (Number(v) || 0) / top * INNER_H
  }, [countTicks])
  const yToken = useCallback((v) => {
    const top = tokenTicks[tokenTicks.length - 1] || 1
    return PAD_T + INNER_H - (Number(v) || 0) / top * INNER_H
  }, [tokenTicks])

  // 折线 + 区域 path
  const countPath = useMemo(() => {
    if (!n) return ''
    return visible.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xAt(i)} ${yCount(p.count || 0)}`).join(' ')
  }, [visible, xAt, yCount])
  const tokenPath = useMemo(() => {
    if (!n) return ''
    return visible.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xAt(i)} ${yToken(p.tokens_total || 0)}`).join(' ')
  }, [visible, xAt, yToken])
  const countArea = useMemo(() => {
    if (!n) return ''
    const base = PAD_T + INNER_H
    let d = `M ${xAt(0)} ${base} `
    visible.forEach((p, i) => { d += `L ${xAt(i)} ${yCount(p.count || 0)} ` })
    d += `L ${xAt(n - 1)} ${base} Z`
    return d
  }, [visible, xAt, yCount])

  // X 轴标签：按可见点数自适应密度（目标 ≈ 8 个标签）
  const xLabels = useMemo(() => {
    if (!n) return []
    const target = 8
    const every = Math.max(1, Math.floor(n / target))
    const out = []
    for (let i = 0; i < n; i++) {
      const last = i === n - 1
      if (i % every === 0 || last) {
        const d = visible[i].date || ''
        let label = ''
        if (bucketSize === 'hour') {
          // "MM-DD HH:00" → "MD H" （精简）
          const m = d.match(/^(\d{2})-(\d{2}) (\d{2}):00$/)
          if (m) label = `${m[1]}/${m[2]} ${m[3]}h`
          else label = d.substring(5)
        } else if (bucketSize === 'day') {
          label = d.substring(5) // MM-DD
        } else {
          // week: 显示该周周一日期 MM-DD
          label = d.substring(5)
        }
        out.push({ i, label })
      }
    }
    return out
  }, [visible, n, bucketSize])

  // ---------- 交互：wheel zoom / pan，mousedown 框选 ----------
  const svgRef = useRef(null)
  // Y 轴缩放（独立 scale）
  const [yScale, setYScale] = useState({ count: 1, token: 1 })
  // 框选状态
  const [brush, setBrush] = useState(null) // {x1, x2}
  // hover 索引
  const [hoverIdx, setHoverIdx] = useState(-1)

  // svg 内坐标
  const eventToVbX = useCallback((e) => {
    const svg = svgRef.current
    if (!svg) return 0
    const rect = svg.getBoundingClientRect()
    const ratio = VB_W / rect.width
    return Math.max(PAD_L, Math.min(PAD_L + INNER_W, (e.clientX - rect.left) * ratio))
  }, [])

  // 鼠标所在 X 对应的数据点索引（最近）
  const idxAtVbX = useCallback((vbX) => {
    if (!n) return -1
    let best = 0
    let bestDist = Infinity
    for (let i = 0; i < n; i++) {
      const d = Math.abs(xAt(i) - vbX)
      if (d < bestDist) { bestDist = d; best = i }
    }
    return best
  }, [n, xAt])

  // 时间戳映射（vbX → 时间戳）线性插值
  const vbXToTs = useCallback((vbX) => {
    if (n < 2) return view.from
    const ratio = (vbX - PAD_L) / INNER_W
    return view.from + ratio * (view.to - view.from)
  }, [n, view])

  const tsToVbX = useCallback((ts) => {
    if (n < 2 || view.to === view.from) return PAD_L
    return PAD_L + (ts - view.from) / (view.to - view.from) * INNER_W
  }, [n, view])

  // wheel：缩放 X（鼠标锚点）/ 缩放 Y（Ctrl）/ pan X（Shift）
  useEffect(() => {
    const svg = svgRef.current
    if (!svg) return
    const onWheel = (e) => {
      e.preventDefault()
      if (!Number.isFinite(view.from)) return
      if (e.ctrlKey || e.metaKey) {
        // Y 缩放
        const factor = e.deltaY < 0 ? 1.1 : 1 / 1.1
        setYScale((s) => ({
          count: Math.max(0.2, Math.min(8, s.count * factor)),
          token: Math.max(0.2, Math.min(8, s.token * factor)),
        }))
        return
      }
      if (e.shiftKey) {
        // X pan
        const panRatio = e.deltaY < 0 ? -0.1 : 0.1
        const span = view.to - view.from
        const shift = span * panRatio
        setView({ from: view.from + shift, to: view.to + shift })
        return
      }
      // X zoom：以鼠标 X 为锚点
      const factor = e.deltaY < 0 ? 1 / 1.15 : 1.15
      const anchorTs = vbXToTs(eventToVbX(e))
      const span = view.to - view.from
      const newSpan = Math.max(60_000, Math.min(origSpan || span * 10, span * factor))
      const leftRatio = (anchorTs - view.from) / span
      const newFrom = anchorTs - newSpan * leftRatio
      const newTo = newFrom + newSpan
      setView({ from: newFrom, to: newTo })
    }
    svg.addEventListener('wheel', onWheel, { passive: false })
    return () => svg.removeEventListener('wheel', onWheel)
  }, [view, origSpan, eventToVbX, vbXToTs])

  // 鼠标事件：mousedown 框选 / dblclick 重置
  const dragRef = useRef(null) // {startX, startTs}
  const onMouseDown = useCallback((e) => {
    if (e.button !== 0) return
    if (!Number.isFinite(view.from)) return
    const x = eventToVbX(e)
    const ts = vbXToTs(x)
    if (e.shiftKey) {
      // pan 模式：记录起点；mousemove 跟随
      dragRef.current = { mode: 'pan', startX: e.clientX, startView: { ...view } }
    } else {
      dragRef.current = { mode: 'brush', startX: x, startTs: ts, lastClientX: e.clientX }
      setBrush({ x1: x, x2: x })
    }
  }, [eventToVbX, vbXToTs, view])

  const onMouseMove = useCallback((e) => {
    const drag = dragRef.current
    if (!drag) return
    if (drag.mode === 'pan') {
      const svg = svgRef.current
      const rect = svg.getBoundingClientRect()
      const dxPx = e.clientX - drag.startX
      const ratio = VB_W / rect.width
      const span = drag.startView.to - drag.startView.from
      const shift = -dxPx * ratio / INNER_W * span
      setView({ from: drag.startView.from + shift, to: drag.startView.to + shift })
    } else if (drag.mode === 'brush') {
      const x = eventToVbX(e)
      setBrush({ x1: drag.startX, x2: x })
    }
    // hover index
    if (n) setHoverIdx(idxAtVbX(eventToVbX(e)))
  }, [eventToVbX, idxAtVbX, n])

  const onMouseUp = useCallback((e) => {
    const drag = dragRef.current
    dragRef.current = null
    if (!drag) { setBrush(null); return }
    if (drag.mode === 'brush') {
      const x2 = eventToVbX(e)
      const x1 = drag.startX
      const xMin = Math.min(x1, x2)
      const xMax = Math.max(x1, x2)
      // 至少 6px 视口宽度才算有效框选
      const svg = svgRef.current
      const rect = svg.getBoundingClientRect()
      const px = (xMax - xMin) / (VB_W / rect.width)
      if (px >= 6) {
        const ts1 = vbXToTs(xMin)
        const ts2 = vbXToTs(xMax)
        setView({ from: ts1, to: ts2 })
      }
      setBrush(null)
    }
  }, [eventToVbX, vbXToTs])

  const onMouseEnter = useCallback((e) => {
    if (n) setHoverIdx(idxAtVbX(eventToVbX(e)))
  }, [eventToVbX, idxAtVbX, n])
  const onMouseLeave = useCallback(() => { setHoverIdx(-1) }, [])

  const onDblClick = useCallback(() => {
    if (!originalPoints.length) return
    const first = parseBucketTs(originalPoints[0].date)
    const last = parseBucketTs(originalPoints[originalPoints.length - 1].date)
    if (Number.isFinite(first) && Number.isFinite(last)) {
      setView({ from: first, to: last })
      setYScale({ count: 1, token: 1 })
    }
  }, [originalPoints])

  // 当前 hover 数据
  const hover = hoverIdx >= 0 ? visible[hoverIdx] : null
  const showReset = (() => {
    if (!originalPoints.length) return false
    const first = parseBucketTs(originalPoints[0].date)
    const last = parseBucketTs(originalPoints[originalPoints.length - 1].date)
    return Number.isFinite(view.from) && (view.from > first + 1 || view.to < last - 1 || yScale.count !== 1 || yScale.token !== 1)
  })()

  if (!originalPoints.length) {
    return (
      <div style={{ position: 'relative', minHeight: VB_H, display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#94a3b8', fontSize: 13 }}>
        {loading ? <div className="shimmer" style={{ position: 'absolute', inset: 0 }} /> : null}
        <span>{emptyHint}</span>
      </div>
    )
  }

  // yScale 应用于 ticks（保持对齐）
  const csTop = (countTicks[countTicks.length - 1] || 1) / yScale.count
  const tkTop = (tokenTicks[tokenTicks.length - 1] || 1) / yScale.token
  const yCountScaled = (v) => PAD_T + INNER_H - (Number(v) || 0) / csTop * INNER_H
  const yTokenScaled = (v) => PAD_T + INNER_H - (Number(v) || 0) / tkTop * INNER_H

  // 当前 hover 在视口外（缩放后不在 [PAD_L, PAD_L+INNER_W] 内）时隐藏
  const hoverVbX = hoverIdx >= 0 ? xAt(hoverIdx) : null

  // 视图描述
  const viewRangeStr = (() => {
    if (!Number.isFinite(view.from)) return ''
    if (typeof viewRangeTemplate === 'function') return viewRangeTemplate(view.from, view.to)
    return formatViewRange(view.from, view.to)
  })()
  const bucketStr = (() => {
    if (typeof bucketTemplate === 'function') return bucketTemplate(bucketSize, visible.length)
    return `${bucketSize} · ${visible.length} 点`
  })()

  return (
    <div style={{ position: 'relative', width: '100%' }}>
      {/* 标题行：图例 + 视图信息 */}
      <div style={{ display: 'flex', gap: 14, fontSize: 12, color: PALETTE.text, marginBottom: 6, flexWrap: 'wrap', alignItems: 'center' }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ display: 'inline-block', width: 16, height: 3, background: `linear-gradient(90deg, ${callColor[0]}, ${callColor[1]})`, borderRadius: 2 }} />
          {callLabel}
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ display: 'inline-block', width: 16, height: 3, background: `linear-gradient(90deg, ${tokenColor[0]}, ${tokenColor[1]})`, borderRadius: 2, borderTop: '1px dashed #a855f7' }} />
          {tokenLabel}
        </span>
        <span style={{ marginLeft: 'auto', color: '#94a3b8', fontSize: 11 }}>
          {viewRangeStr ? `${viewRangeStr} · ${bucketStr}` : ''}
        </span>
        {showReset ? (
          <button
            onClick={onDblClick}
            style={{ padding: '2px 10px', fontSize: 11, border: '1px solid #cbd5e1', borderRadius: 4, background: 'white', cursor: 'pointer', color: PALETTE.text }}
          >
            {resetLabel || '重置缩放'}
          </button>
        ) : null}
      </div>

      {/* 主图：左轴文字 / SVG / 右轴文字 三列 */}
      <div
        style={{ display: 'grid', gridTemplateColumns: '56px 1fr 56px', alignItems: 'stretch', userSelect: 'none' }}
        onMouseDown={onMouseDown}
        onMouseMove={onMouseMove}
        onMouseUp={onMouseUp}
        onMouseEnter={onMouseEnter}
        onMouseLeave={onMouseLeave}
        onDoubleClick={onDblClick}
      >
        {/* 左轴文字（HTML 浮层，字号不变形） */}
        <div style={{ position: 'relative', height: VB_H }}>
          {countTicks.map((v, i) => {
            const top = PAD_T + INNER_H - (v / csTop) * INNER_H
            return (
              <span key={`cl${i}`} style={{ position: 'absolute', right: 6, top: top - 7, fontSize: 11, color: PALETTE.axis, lineHeight: 1 }}>
                {fmtCompact(v)}
              </span>
            )
          })}
          <span style={{ position: 'absolute', top: 4, right: 6, fontSize: 10, color: PALETTE.text, fontWeight: 600 }}>{callLabel}</span>
        </div>

        {/* SVG 图区（等比缩放） */}
        <svg
          ref={svgRef}
          viewBox={`0 0 ${VB_W} ${VB_H}`}
          style={{ width: '100%', height: VB_H, display: 'block', cursor: brush ? 'crosshair' : 'ew-resize' }}
          preserveAspectRatio="xMidYMid meet"
        >
          <defs>
            <linearGradient id="kl-fill-call" x1="0" x2="0" y1="0" y2="1">
              <stop offset="0%" stopColor={callColor[1]} stopOpacity="0.32" />
              <stop offset="100%" stopColor={callColor[1]} stopOpacity="0.02" />
            </linearGradient>
            <linearGradient id="kl-stroke-call" x1="0" x2="1" y1="0" y2="0">
              <stop offset="0%" stopColor={callColor[0]} />
              <stop offset="100%" stopColor={callColor[1]} />
            </linearGradient>
            <linearGradient id="kl-stroke-token" x1="0" x2="1" y1="0" y2="0">
              <stop offset="0%" stopColor={tokenColor[0]} />
              <stop offset="100%" stopColor={tokenColor[1]} />
            </linearGradient>
          </defs>

          {/* 网格 */}
          {countTicks.map((v, i) => (
            <line key={`g${i}`} x1={PAD_L} x2={PAD_L + INNER_W} y1={yCountScaled(v)} y2={yCountScaled(v)} stroke={PALETTE.grid} strokeWidth="1" strokeDasharray="2,3" />
          ))}

          {/* X 轴基线 */}
          <line x1={PAD_L} x2={PAD_L + INNER_W} y1={PAD_T + INNER_H} y2={PAD_T + INNER_H} stroke={PALETTE.axis} />

          {/* X 轴文字（SVG 内文字也等比缩放，但因字号统一 + 内容精简，可接受） */}
          {xLabels.map((it) => (
            <text key={`xl${it.i}`} x={xAt(it.i)} y={VB_H - 14} textAnchor="middle" fontSize="10" fill={PALETTE.axis}>
              {it.label}
            </text>
          ))}

          {/* 调用次数面积 + 折线 */}
          <path d={countArea} fill="url(#kl-fill-call)">
            <animate attributeName="opacity" from="0" to="1" dur={`${animationMs}ms`} fill="freeze" />
          </path>
          <path d={countPath} fill="none" stroke="url(#kl-stroke-call)" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
            <animate attributeName="stroke-dashoffset" from={INNER_W + 100} to="0" dur={`${animationMs}ms`} fill="freeze" />
          </path>

          {/* Tokens 折线（虚线样式） */}
          <path d={tokenPath} fill="none" stroke="url(#kl-stroke-token)" strokeWidth="2" strokeDasharray="5,3" strokeLinecap="round" strokeLinejoin="round">
            <animate attributeName="stroke-dashoffset" from={INNER_W + 100} to="0" dur={`${animationMs}ms`} fill="freeze" />
          </path>

          {/* 框选矩形 */}
          {brush ? (
            <rect
              x={Math.min(brush.x1, brush.x2)}
              y={PAD_T}
              width={Math.abs(brush.x2 - brush.x1)}
              height={INNER_H}
              fill={PALETTE.brush}
              stroke={PALETTE.brushBorder}
              strokeWidth="1"
              strokeDasharray="3,3"
            />
          ) : null}

          {/* hover 十字线 + 点 */}
          {hoverVbX !== null ? (
            <g>
              <line x1={hoverVbX} x2={hoverVbX} y1={PAD_T} y2={PAD_T + INNER_H} stroke={PALETTE.hover} strokeWidth="1" strokeDasharray="3,3" />
              <circle cx={hoverVbX} cy={yCountScaled(hover?.count || 0)} r="4" fill="white" stroke={callColor[1]} strokeWidth="2" />
              <circle cx={hoverVbX} cy={yTokenScaled(hover?.tokens_total || 0)} r="3" fill="white" stroke={tokenColor[0]} strokeWidth="1.5" />
            </g>
          ) : null}
        </svg>

        {/* 右轴文字 */}
        <div style={{ position: 'relative', height: VB_H }}>
          {tokenTicks.map((v, i) => {
            const top = PAD_T + INNER_H - (v / tkTop) * INNER_H
            return (
              <span key={`cr${i}`} style={{ position: 'absolute', left: 6, top: top - 7, fontSize: 11, color: PALETTE.axis, lineHeight: 1 }}>
                {fmtCompact(v)}
              </span>
            )
          })}
          <span style={{ position: 'absolute', top: 4, right: 6, fontSize: 10, color: PALETTE.text, fontWeight: 600 }}>{tokenLabel}</span>
        </div>
      </div>

      {/* 悬浮 tooltip（绝对定位，HTML 字号不变形） */}
      {hover && hoverVbX !== null ? (
        <div style={{
          position: 'absolute',
          left: `calc(${(hoverVbX - PAD_L) / INNER_W * 100}% + 56px)`,
          top: PAD_T + 4,
          transform: hoverVbX > PAD_L + INNER_W * 0.7 ? 'translateX(-100%)' : 'none',
          padding: '6px 10px',
          background: 'rgba(15, 23, 42, 0.92)',
          color: '#f1f5f9',
          borderRadius: 6,
          fontSize: 12,
          pointerEvents: 'none',
          whiteSpace: 'nowrap',
          boxShadow: '0 2px 8px rgba(0,0,0,0.15)',
        }}>
          <div style={{ fontWeight: 600, marginBottom: 2 }}>{hover.date}</div>
          <div><span style={{ color: callColor[0] }}>●</span> {callLabel} {fmtFull(hover.count || 0)}</div>
          <div><span style={{ color: tokenColor[0] }}>●</span> {tokenLabel} {fmtFull(hover.tokens_total || 0)}</div>
        </div>
      ) : null}

      {/* 操作提示 */}
      <div style={{ marginTop: 4, fontSize: 11, color: '#94a3b8' }}>
        {zoomHint || '滚轮缩放 · Shift+滚轮 pan · Ctrl+滚轮 缩放Y · 左键拖拽框选 · 双击重置'}
      </div>
    </div>
  )
}