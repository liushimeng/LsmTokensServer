// QoSErrorBars：错误堆叠柱状图（全模型汇总）。
// 按时间桶堆叠 429 限流 / 5xx 服务故障 / 其他错误计数，段间 1px 表面色描边
// 形成 2px 视觉缝隙；悬浮 tooltip 显示各分类计数；零数据桶不画柱。
import { useMemo, useState } from 'react'
import ResponsiveSvgChart from '../../components/ResponsiveSvgChart'
import { ERROR_COLORS, windowSeries, bucketShortLabel, fmtNum } from './constants'

const CHART_H = 220
const PAD = { left: 48, right: 14, top: 12, bottom: 24 }
const GRID = '#e2e8f0'
const AXIS = '#94a3b8'
const TEXT = '#475569'

export default function QoSErrorBars({ models, win, t }) {
  const [hoverIdx, setHoverIdx] = useState(null)
  const [vw, setVw] = useState(720)

  const buckets = useMemo(() => {
    if (!models.length) return []
    const seriesList = models.map((m) => windowSeries(m, win))
    // 桶数取各模型序列最大值：此前取 models[0] 的长度，模型桶数不一致时会截断其它模型
    // （v2.0.88 阶段DC 修复 D2）
    const n = seriesList.reduce((mx, s) => Math.max(mx, s.length), 0)
    const out = []
    for (let i = 0; i < n; i++) out.push({ e429: 0, e5xx: 0, other: 0, total: 0, partial: false })
    for (const pts of seriesList) {
      for (let i = 0; i < Math.min(n, pts.length); i++) {
        out[i].e429 += Number(pts[i].error_429) || 0
        out[i].e5xx += Number(pts[i].error_5xx) || 0
        out[i].other += Number(pts[i].error_other) || 0
        out[i].total += Number(pts[i].count) || 0
        out[i].partial = out[i].partial || !!pts[i].partial
      }
    }
    return out
  }, [models, win])

  // 参考序列（桶数最多的模型）：用于取桶键（首尾时间标注 / tooltip 标题）。
  // 此前直接取 models[0]，当首个模型无桶时会抛异常导致整页崩溃（修复 D2）。
  const refSeries = useMemo(() => {
    let best = null
    for (const m of models) {
      const pts = windowSeries(m, win)
      if (!best || pts.length > best.length) best = pts
    }
    return best || []
  }, [models, win])

  const total = useMemo(
    () => buckets.reduce((s, b) => s + b.e429 + b.e5xx + b.other, 0),
    [buckets],
  )

  const legend = useMemo(() => ([
    { key: 'e429', label: t('serviceQuality.errors.429'), color: ERROR_COLORS.e429 },
    { key: 'e5xx', label: t('serviceQuality.errors.5xx'), color: ERROR_COLORS.e5xx },
    { key: 'other', label: t('serviceQuality.errors.other'), color: ERROR_COLORS.other },
  ]), [t])

  if (!buckets.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.errors.empty')}</div>
  }

  const yMax = Math.max(1, ...buckets.map((b) => b.e429 + b.e5xx + b.other)) * 1.12

  return (
    <div>
      <div style={{ display: 'flex', gap: 12, marginBottom: 6, alignItems: 'center', fontSize: 11, color: TEXT }}>
        {legend.map((l) => (
          <span key={l.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 2, background: l.color }} />
            {l.label}
          </span>
        ))}
        <span style={{ marginLeft: 'auto', color: '#94a3b8' }}>
          {total > 0 ? t('serviceQuality.errors.total', { count: fmtNum(total) }) : t('serviceQuality.errors.empty')}
        </span>
      </div>
      <div style={{ position: 'relative' }}>
        <ResponsiveSvgChart minWidth={200} height={CHART_H} onWidthChange={setVw}>
          {(W) => {
            const innerW = Math.max(120, W - PAD.left - PAD.right)
            const innerH = CHART_H - PAD.top - PAD.bottom
            const slot = innerW / buckets.length
            const xOf = (i) => PAD.left + slot * i + slot * 0.15
            const barW = Math.max(2, slot * 0.7)
            return (
              <svg
                width={W} height={CHART_H}
                onMouseLeave={() => setHoverIdx(null)}
                onMouseMove={(e) => {
                  const rect = e.currentTarget.getBoundingClientRect()
                  const x = e.clientX - rect.left
                  const idx = Math.floor((x - PAD.left) / slot)
                  setHoverIdx(idx >= 0 && idx < buckets.length ? idx : null)
                }}
              >
                {[0, 0.5, 1].map((r) => {
                  const y = PAD.top + innerH * r
                  return (
                    <g key={r}>
                      <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y} stroke={GRID} strokeWidth={1} />
                      <text x={PAD.left - 6} y={y + 3} textAnchor="end" fontSize={10} fill={AXIS}>
                        {fmtNum(Math.round(yMax * (1 - r)))}
                      </text>
                    </g>
                  )
                })}
                {buckets.map((b, i) => {
                  const totalI = b.e429 + b.e5xx + b.other
                  if (!totalI) return null
                  let yCursor = PAD.top + innerH
                  const segs = []
                  for (const l of legend) {
                    const cnt = b[l.key]
                    if (!cnt) continue
                    const hSeg = (cnt / yMax) * innerH
                    yCursor -= hSeg
                    segs.push(
                      <rect
                        key={l.key} x={xOf(i)} y={yCursor} width={barW} height={Math.max(1, hSeg)}
                        fill={l.color} stroke="#fff" strokeWidth={1} rx={1.5}
                      />,
                    )
                  }
                  return (
                    <g key={i} onMouseEnter={() => setHoverIdx(i)}>
                      {segs}
                      {hoverIdx === i ? (
                        <rect x={xOf(i) - 1} y={PAD.top} width={barW + 2} height={innerH}
                          fill="#0f172a" opacity={0.04} />
                      ) : null}
                    </g>
                  )
                })}
                {/* 首尾桶时间标注 */}
                <text x={xOf(0)} y={CHART_H - 6} fontSize={10} fill={AXIS} textAnchor="start">
                  {refSeries[0] ? bucketShortLabel(refSeries[0].bucket) : ''}
                </text>
                <text x={xOf(buckets.length - 1) + barW} y={CHART_H - 6}
                  fontSize={10} fill={AXIS} textAnchor="end">
                  {refSeries[buckets.length - 1] ? bucketShortLabel(refSeries[buckets.length - 1].bucket) : ''}
                </text>
              </svg>
            )
          }}
        </ResponsiveSvgChart>
        {hoverIdx !== null && buckets[hoverIdx] ? (
          <div style={{
            position: 'absolute',
            left: Math.min(PAD.left + slotLeft(vw, buckets.length, hoverIdx) + 8, Math.max(0, vw - 170)),
            top: 4,
            background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8,
            boxShadow: '0 4px 12px rgba(15,23,42,.08)', padding: '5px 10px', fontSize: 12,
            pointerEvents: 'none', color: TEXT,
          }}>
            <div style={{ color: '#94a3b8', marginBottom: 2 }}>
              {refSeries[hoverIdx] ? refSeries[hoverIdx].bucket : ''}
              {buckets[hoverIdx].partial ? ` · ${t('serviceQuality.period.partial')}` : ''}
            </div>
            {legend.map((l) => (
              <div key={l.key} style={{ display: 'flex', alignItems: 'center', gap: 6, lineHeight: 1.5 }}>
                <span style={{ width: 8, height: 8, borderRadius: 2, background: l.color }} />
                {l.label}
                <span style={{ marginLeft: 'auto', fontVariantNumeric: 'tabular-nums' }}>{fmtNum(buckets[hoverIdx][l.key])}</span>
              </div>
            ))}
            <div style={{ marginTop: 3, paddingTop: 3, borderTop: '1px solid #e2e8f0', color: '#475569' }}>
              {t('serviceQuality.errors.totalBucket', { count: fmtNum(buckets[hoverIdx].total) })}
              {' · '}
              {t('serviceQuality.errors.rate', {
                v: buckets[hoverIdx].total > 0
                  ? ((buckets[hoverIdx].e429 + buckets[hoverIdx].e5xx + buckets[hoverIdx].other) / buckets[hoverIdx].total * 100).toFixed(1) + '%'
                  : '0%',
              })}
            </div>
          </div>
        ) : null}
      </div>
    </div>
  )
}

// slotLeft tooltip 定位用：第 idx 个桶的左缘 x（与渲染公式一致）
function slotLeft(W, n, idx) {
  const innerW = Math.max(120, W - PAD.left - PAD.right)
  return (innerW / n) * idx + (innerW / n) * 0.15
}
