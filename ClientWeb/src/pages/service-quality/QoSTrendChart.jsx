// QoSTrendChart：时延趋势折线图。
// X 轴时间桶、Y 轴响应时长（ms，单轴）；多模型分色折线（同色实线 = 平均、
// 虚线 = P95 警戒线）；图例点击切换模型；十字线 + tooltip 悬浮查看数值。
// 颜色跟随模型实体（名称稳定分配，刷新不漂移），分类色经 CVD 校验。
import { useMemo, useState } from 'react'
import ResponsiveSvgChart from '../../components/ResponsiveSvgChart'
import { modelColor, windowSeries, bucketShortLabel, fmtMs } from './constants'

const CHART_H = 280
const PAD = { left: 58, right: 18, top: 14, bottom: 26 }
const GRID = '#e2e8f0'
const AXIS = '#94a3b8'
const TEXT = '#475569'

export default function QoSTrendChart({ models, allNames, selected, onToggle, win, t }) {
  const [hoverIdx, setHoverIdx] = useState(null)
  const [vw, setVw] = useState(720) // 容器实测宽度（tooltip 定位用）

  const shown = useMemo(
    () => models.filter((m) => selected.has(m.dst_model_name)),
    [models, selected],
  )

  const series = useMemo(() => shown.map((m) => ({
    name: m.dst_model_name,
    color: modelColor(allNames, m.dst_model_name),
    pts: windowSeries(m, win),
  })), [shown, win, allNames])

  const bucketCount = series.length ? series[0].pts.length : 0

  const yMax = useMemo(() => {
    let max = 0
    for (const s of series) {
      for (const p of s.pts) {
        if (Number(p.avg_elapsed_ms) > max) max = Number(p.avg_elapsed_ms)
        if (Number(p.p95_elapsed_ms) > max) max = Number(p.p95_elapsed_ms)
      }
    }
    return max > 0 ? max * 1.12 : 1000
  }, [series])

  const xLabels = useMemo(() => {
    const n = Math.min(5, bucketCount)
    const out = []
    if (!bucketCount) return out
    const step = (bucketCount - 1) / Math.max(1, n - 1)
    for (let i = 0; i < n; i++) {
      const idx = Math.round(i * step)
      const key = series[0] && series[0].pts[idx] ? series[0].pts[idx].bucket : ''
      out.push({ idx, label: bucketShortLabel(key) })
    }
    return out
  }, [bucketCount, series])

  if (!series.length || !bucketCount) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.trend.empty')}</div>
  }

  const hoverX = hoverIdx !== null
    ? (W) => PAD.left + (bucketCount <= 1 ? 0 : (hoverIdx / (bucketCount - 1)) * Math.max(120, W - PAD.left - PAD.right))
    : null

  return (
    <div>
      {/* 图例：色块 + 模型名，点击切换；虚线说明 P95 */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 6, marginBottom: 8, alignItems: 'center' }}>
        {models.map((m) => {
          const on = selected.has(m.dst_model_name)
          const color = modelColor(allNames, m.dst_model_name)
          return (
            <button
              key={m.dst_model_name}
              onClick={() => onToggle(m.dst_model_name)}
              style={{
                display: 'inline-flex', alignItems: 'center', gap: 5, cursor: 'pointer',
                fontSize: 12, padding: '2px 8px', borderRadius: 999,
                border: '1px solid ' + (on ? color : '#e2e8f0'),
                background: on ? color + '14' : '#fff',
                color: on ? TEXT : '#94a3b8',
              }}
            >
              <span style={{ width: 14, height: 3, borderRadius: 2, background: on ? color : '#cbd5e1' }} />
              {m.dst_model_name}
            </button>
          )
        })}
      </div>
      <div style={{ position: 'relative' }}>
        <ResponsiveSvgChart minWidth={200} height={CHART_H} onWidthChange={setVw}>
          {(W) => {
            const innerW = Math.max(120, W - PAD.left - PAD.right)
            const innerH = CHART_H - PAD.top - PAD.bottom
            const xOf = (i) => PAD.left + (bucketCount <= 1 ? 0 : (i / (bucketCount - 1)) * innerW)
            const yOf = (v) => PAD.top + innerH - (Math.max(0, v) / yMax) * innerH
            const linePath = (pts, field) => pts
              .map((p, i) => `${i === 0 ? 'M' : 'L'}${xOf(i).toFixed(1)},${yOf(p[field]).toFixed(1)}`)
              .join(' ')
            const hx = hoverIdx !== null ? xOf(hoverIdx) : 0
            return (
              <svg
                width={W} height={CHART_H}
                onMouseMove={(e) => {
                  const rect = e.currentTarget.getBoundingClientRect()
                  const x = e.clientX - rect.left
                  const idx = bucketCount <= 1 ? 0 : Math.round(((x - PAD.left) / innerW) * (bucketCount - 1))
                  setHoverIdx(idx >= 0 && idx < bucketCount ? idx : null)
                }}
                onMouseLeave={() => setHoverIdx(null)}
              >
                {/* 网格 + Y 轴刻度（实线极细，recessive） */}
                {[0, 0.25, 0.5, 0.75, 1].map((r) => {
                  const y = PAD.top + innerH * r
                  const val = Math.round(yMax * (1 - r))
                  return (
                    <g key={r}>
                      <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y} stroke={GRID} strokeWidth={1} />
                      <text x={PAD.left - 6} y={y + 3} textAnchor="end" fontSize={10} fill={AXIS}>{fmtMs(val)}</text>
                    </g>
                  )
                })}
                {/* X 轴刻度（首尾贴边对齐，避免越界裁切） */}
                {xLabels.map((it, i) => (
                  <text
                    key={it.idx}
                    x={xOf(it.idx)}
                    y={CHART_H - 8}
                    textAnchor={i === 0 ? 'start' : (i === xLabels.length - 1 ? 'end' : 'middle')}
                    fontSize={10}
                    fill={AXIS}
                  >
                    {it.label}
                  </text>
                ))}
                {/* 模型折线：实线平均 + 同色虚线 P95 */}
                {series.map((s) => (
                  <g key={s.name}>
                    <path d={linePath(s.pts, 'p95_elapsed_ms')} fill="none" stroke={s.color} strokeWidth={1.5}
                      strokeDasharray="5,4" opacity={0.55} strokeLinecap="round" />
                    <path d={linePath(s.pts, 'avg_elapsed_ms')} fill="none" stroke={s.color} strokeWidth={2}
                      strokeLinecap="round" strokeLinejoin="round" />
                  </g>
                ))}
                {/* 十字线 + 悬浮点 */}
                {hoverIdx !== null ? (
                  <g>
                    <line x1={hx} x2={hx} y1={PAD.top} y2={PAD.top + innerH} stroke={AXIS} strokeWidth={1} />
                    {series.filter((s) => s.pts[hoverIdx] && s.pts[hoverIdx].count > 0).map((s) => (
                      <circle key={s.name} cx={hx} cy={yOf(s.pts[hoverIdx].avg_elapsed_ms)} r={3.5}
                        fill="#fff" stroke={s.color} strokeWidth={2} />
                    ))}
                  </g>
                ) : null}
              </svg>
            )
          }}
        </ResponsiveSvgChart>
        {hoverIdx !== null ? (
          <div style={{
            position: 'absolute',
            left: Math.min((hoverX ? hoverX(vw) : 0) + 12, Math.max(0, vw - 210)),
            top: 6,
            background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8,
            boxShadow: '0 4px 12px rgba(15,23,42,.08)', padding: '6px 10px', fontSize: 12,
            pointerEvents: 'none', minWidth: 170,
          }}>
            <div style={{ color: '#94a3b8', marginBottom: 2 }}>
              {series[0] && series[0].pts[hoverIdx] ? series[0].pts[hoverIdx].bucket : ''}
            </div>
            {series.map((s) => {
              const p = s.pts[hoverIdx]
              return (
                <div key={s.name} style={{ display: 'flex', alignItems: 'center', gap: 6, lineHeight: 1.6 }}>
                  <span style={{ width: 8, height: 8, borderRadius: 999, background: s.color, flexShrink: 0 }} />
                  <span style={{ color: TEXT }}>{s.name}</span>
                  <span style={{ marginLeft: 'auto', color: TEXT, fontVariantNumeric: 'tabular-nums' }}>
                    {p && p.count > 0 ? `${fmtMs(p.avg_elapsed_ms)} / P95 ${fmtMs(p.p95_elapsed_ms)}` : '-'}
                  </span>
                </div>
              )
            })}
          </div>
        ) : null}
      </div>
      <div style={{ marginTop: 4, fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.trend.hint')}</div>
    </div>
  )
}
