// KLineTrendChart：纯 SVG 双线趋势图（调用次数 + Tokens 数）
// 工程惯例：不引入第三方图表库（无 echarts/recharts/d3）。
//
// props:
//   points: [{ date: 'YYYY-MM-DD HH:00', count, tokens_total, tokens_input, tokens_output }]
//   title?: 卡片标题（可选，外部通常用 card 包裹）
//   height?: 像素（默认 280）
//   callLabel?: 左 Y 轴名称（默认 "调用次数"）
//   tokenLabel?: 右 Y 轴名称（默认 "Tokens 数"）
//   callColor?: 左线颜色（默认蓝绿渐变）
//   tokenColor?: 右线颜色（默认紫色）
//   loading?: 是否显示加载遮罩
//   emptyHint?: 空数据提示文案
//   animationMs?: 入场动画时长（默认 700）
//
// 动画：path stroke-dashoffset 由大到小，配合 SVG <animate>。
import { useMemo } from 'react'
import { useI18n } from '../i18n'

const PALETTE = {
  call: ['#34d399', '#0ea5e9'], // 调用次数（绿 → 蓝）
  token: ['#a855f7', '#ec4899'], // Tokens（紫 → 粉）
  grid: '#e2e8f0',
  axis: '#94a3b8',
  text: '#475569',
  hover: '#1e293b',
}

function formatNumber(n) {
  n = Number(n) || 0
  return Math.round(n).toString().replace(/\B(?=(\d{3})+(?!\d))/g, ',')
}

function compactNumber(n) {
  n = Number(n) || 0
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
  if (n >= 10_000) return (n / 1_000).toFixed(1).replace(/\.0$/, '') + 'k'
  if (n >= 1_000) return (n / 1_000).toFixed(2).replace(/\.0$/, '') + 'k'
  return String(Math.round(n))
}

function formatTickDate(d, granularity) {
  // "2026-08-26 14:00" -> "08-26 14:00" 或 "08-26"
  if (!d) return ''
  if (granularity === 'day' || !d.includes(' ')) {
    return d.substring(5) // MM-DD
  }
  // hour bucket: "2026-08-26 14:00"
  const [datePart, timePart] = d.split(' ')
  return `${datePart.substring(5)} ${timePart.substring(0, 5)}`
}

function buildTicks(maxVal, target = 4) {
  // 选一个「漂亮的」刻度上限（maxVal 向上取整到合适粒度）
  if (maxVal <= 0) return [0, 1]
  const exp = Math.floor(Math.log10(maxVal))
  const base = Math.pow(10, exp)
  const ratio = maxVal / base
  let niceMax
  if (ratio <= 1) niceMax = base
  else if (ratio <= 2) niceMax = 2 * base
  else if (ratio <= 5) niceMax = 5 * base
  else niceMax = 10 * base
  const step = niceMax / target
  const ticks = []
  for (let i = 0; i <= target; i++) ticks.push(Math.round(i * step))
  return ticks
}

export default function KLineTrendChart(props) {
  const { t } = useI18n()
  const {
    points = [],
    height = 280,
    callLabel = t('common.tokens') ? null : null, // 占位
    tokenLabel = null,
    callColor = PALETTE.call,
    tokenColor = PALETTE.token,
    loading = false,
    emptyHint,
    animationMs = 700,
  } = props

  // 调用次数/Tokens 单位标签用 props 优先，否则从 i18n 取
  const callSeriesLabel = props.callSeriesLabel || t('modelInfo.trendCallSeries')
  const tokenSeriesLabel = props.tokenSeriesLabel || t('modelInfo.trendTokenSeries')
  const tooltipTemplate = props.tooltipTemplate || t('modelInfo.trendTooltip')

  const width = 720 // viewBox 宽；外层容器自适应缩放
  const padL = 56
  const padR = 56
  const padT = 18
  const padB = 36
  const innerW = width - padL - padR
  const innerH = height - padT - padB

  const data = Array.isArray(points) ? points : []
  const granularity = data.length && data[0].date && data[0].date.includes(' ')
    ? 'hour'
    : 'day'

  const maxCount = useMemo(() => Math.max(1, ...data.map((p) => p.count || 0)), [data])
  const maxToken = useMemo(() => Math.max(1, ...data.map((p) => p.tokens_total || 0)), [data])

  const countTicks = useMemo(() => buildTicks(maxCount), [maxCount])
  const tokenTicks = useMemo(() => buildTicks(maxToken), [maxToken])

  // X 轴采样：≤ 24 全显示；25~168 按窗口采样；>168 等距采样
  const xStep = (() => {
    const n = data.length
    if (n <= 1) return 0
    return innerW / (n - 1)
  })()

  function xAt(i) {
    return padL + i * xStep
  }
  function yCount(v) {
    const top = countTicks[countTicks.length - 1] || 1
    return padT + innerH - (Number(v) / top) * innerH
  }
  function yToken(v) {
    const top = tokenTicks[tokenTicks.length - 1] || 1
    return padT + innerH - (Number(v) / top) * innerH
  }

  // 调用次数折线路径
  const countPath = useMemo(() => {
    if (!data.length) return ''
    return data.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xAt(i)} ${yCount(p.count || 0)}`).join(' ')
  }, [data, maxCount])

  // Tokens 折线路径
  const tokenPath = useMemo(() => {
    if (!data.length) return ''
    return data.map((p, i) => `${i === 0 ? 'M' : 'L'} ${xAt(i)} ${yToken(p.tokens_total || 0)}`).join(' ')
  }, [data, maxToken])

  // 区域填充（仅 count，下方淡色）
  const countAreaPath = useMemo(() => {
    if (!data.length) return ''
    const base = padT + innerH
    let d = `M ${xAt(0)} ${base} `
    data.forEach((p, i) => { d += `L ${xAt(i)} ${yCount(p.count || 0)} ` })
    d += `L ${xAt(data.length - 1)} ${base} Z`
    return d
  }, [data, maxCount])

  // X 轴标签采样
  const xLabelEvery = (() => {
    const n = data.length
    if (n <= 12) return 1
    if (n <= 48) return Math.ceil(n / 12)
    if (n <= 168) return Math.ceil(n / 12)
    return Math.ceil(n / 10)
  })()
  const xLabels = data.map((p, i) => ({
    i,
    label: i % xLabelEvery === 0 || i === data.length - 1 ? formatTickDate(p.date, granularity) : '',
  }))

  // hover 命中：以 X 坐标最近点
  const handleMove = (e) => {
    const svg = e.currentTarget
    const rect = svg.getBoundingClientRect()
    const ratio = width / rect.width
    const localX = (e.clientX - rect.left) * ratio
    if (!data.length) return
    let nearest = 0
    let minDist = Infinity
    for (let i = 0; i < data.length; i++) {
      const d = Math.abs(xAt(i) - localX)
      if (d < minDist) { minDist = d; nearest = i }
    }
    const hover = svg.querySelector('#hover-line')
    const dot = svg.querySelector('#hover-dot')
    const label = svg.querySelector('#hover-label')
    if (hover) hover.setAttribute('x1', xAt(nearest)); hover.setAttribute('x2', xAt(nearest))
    if (dot) {
      dot.setAttribute('cx', xAt(nearest))
      dot.setAttribute('cy', yCount(data[nearest].count || 0))
      dot.setAttribute('r', 4)
    }
    if (label) {
      const p = data[nearest]
      const text = tooltipTemplate
        .replace('{date}', p.date || '')
        .replace('{count}', formatNumber(p.count || 0))
        .replace('{tokens}', formatNumber(p.tokens_total || 0))
      label.textContent = text
      const lw = Math.min(text.length * 7.5 + 18, width - padL - padR)
      let lx = xAt(nearest) + 10
      if (lx + lw > width - padR) lx = xAt(nearest) - lw - 10
      label.setAttribute('x', lx + 8)
      label.setAttribute('y', padT + 14)
      // 同步背景框
      const bg = svg.querySelector('#hover-label-bg')
      if (bg) {
        bg.setAttribute('x', lx)
        bg.setAttribute('y', padT + 2)
        bg.setAttribute('width', lw)
      }
    }
  }
  const handleLeave = (e) => {
    const svg = e.currentTarget
    const hover = svg.querySelector('#hover-line')
    const dot = svg.querySelector('#hover-dot')
    const label = svg.querySelector('#hover-label')
    const bg = svg.querySelector('#hover-label-bg')
    if (hover) { hover.setAttribute('x1', -9999); hover.setAttribute('x2', -9999) }
    if (dot) dot.setAttribute('r', 0)
    if (label) { label.textContent = ''; label.setAttribute('x', -9999) }
    if (bg) bg.setAttribute('x', -9999)
  }

  if (!data.length) {
    return (
      <div style={{ position: 'relative', height, display: 'flex', alignItems: 'center', justifyContent: 'center', color: '#94a3b8' }}>
        {loading ? <div className="shimmer" style={{ position: 'absolute', inset: 0 }} /> : null}
        <span>{emptyHint || '—'}</span>
      </div>
    )
  }

  return (
    <div style={{ position: 'relative', width: '100%' }}>
      {loading ? (
        <div style={{ position: 'absolute', inset: 0, background: 'rgba(255,255,255,0.5)', zIndex: 2, display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 12, color: '#64748b' }}>
          …
        </div>
      ) : null}
      <div style={{ display: 'flex', gap: 12, fontSize: 12, color: '#475569', marginBottom: 6, flexWrap: 'wrap' }}>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ display: 'inline-block', width: 14, height: 3, background: `linear-gradient(90deg, ${callColor[0]}, ${callColor[1]})`, borderRadius: 2 }} />
          {callSeriesLabel}
        </span>
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          <span style={{ display: 'inline-block', width: 14, height: 3, background: `linear-gradient(90deg, ${tokenColor[0]}, ${tokenColor[1]})`, borderRadius: 2 }} />
          {tokenSeriesLabel}
        </span>
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        style={{ width: '100%', height, display: 'block' }}
        preserveAspectRatio="none"
        onMouseMove={handleMove}
        onMouseLeave={handleLeave}
      >
        <defs>
          <linearGradient id="kl-fill-call" x1="0" x2="0" y1="0" y2="1">
            <stop offset="0%" stopColor={callColor[1]} stopOpacity="0.35" />
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
          <g key={`gc${i}`}>
            <line x1={padL} x2={width - padR} y1={yCount(v)} y2={yCount(v)} stroke={PALETTE.grid} strokeWidth="1" strokeDasharray="2,3" />
            <text x={padL - 8} y={yCount(v) + 4} textAnchor="end" fontSize="10" fill={PALETTE.axis}>{compactNumber(v)}</text>
          </g>
        ))}
        {/* 右轴 Tokens 标签 */}
        {tokenTicks.map((v, i) => (
          <text key={`xt${i}`} x={width - padR + 8} y={yToken(v) + 4} textAnchor="start" fontSize="10" fill={PALETTE.axis}>{compactNumber(v)}</text>
        ))}

        {/* X 轴 */}
        <line x1={padL} x2={width - padR} y1={padT + innerH} y2={padT + innerH} stroke={PALETTE.axis} />
        {xLabels.map((it) => (
          <text key={`xl${it.i}`} x={xAt(it.i)} y={height - 12} textAnchor="middle" fontSize="10" fill={PALETTE.axis}>
            {it.label}
          </text>
        ))}

        {/* 调用次数面积 + 折线 */}
        <path d={countAreaPath} fill="url(#kl-fill-call)">
          <animate attributeName="opacity" from="0" to="1" dur={`${animationMs}ms`} fill="freeze" />
        </path>
        <path d={countPath} fill="none" stroke="url(#kl-stroke-call)" strokeWidth="2.5" strokeLinecap="round" strokeLinejoin="round">
          <animate attributeName="stroke-dashoffset" from={innerW + 100} to="0" dur={`${animationMs}ms`} fill="freeze" />
        </path>

        {/* Tokens 折线 */}
        <path d={tokenPath} fill="none" stroke="url(#kl-stroke-token)" strokeWidth="2" strokeDasharray="4,3" strokeLinecap="round" strokeLinejoin="round">
          <animate attributeName="stroke-dashoffset" from={innerW + 100} to="0" dur={`${animationMs}ms`} fill="freeze" />
        </path>

        {/* hover 层 */}
        <line id="hover-line" x1={-9999} x2={-9999} y1={padT} y2={padT + innerH} stroke={PALETTE.hover} strokeWidth="1" strokeDasharray="3,3" />
        <circle id="hover-dot" cx={0} cy={0} r={0} fill="url(#kl-stroke-call)" stroke="white" strokeWidth="1.5" />
        <rect id="hover-label-bg" x={-9999} y={padT} width={0} height={20} rx={4} fill="#0f172a" opacity="0.92" />
        <text id="hover-label" x={-9999} y={padT + 14} fontSize="11" fill="#f1f5f9"></text>
      </svg>
    </div>
  )
}