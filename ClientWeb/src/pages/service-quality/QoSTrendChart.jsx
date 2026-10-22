// QoSTrendChart：服务质量趋势折线图（单轴多指标，禁止双轴）。
//
// v2.0.84 时间维度优化：
//   - 指标可切换：平均时延 / P95 时延 / 拥堵系数 / 卡顿率 / 错误率，
//     同一时刻只画一个 Y 轴（双轴会凭空制造相关性，见 dataviz anti-patterns）。
//   - 时延取成功请求口径（ok_*）：429/5xx 快速失败不再把曲线"拉好看"。
//   - 系数类指标在样本 < 5 的时段断线（不画 0），避免小样本伪造结论。
//   - 进行中时段（当前分钟/小时/天）以空心端点 + 脚注标识。
//   - 阈值参考线为虚线（虚线仅承载阈值语义），网格保持实线极细。
//   - tooltip 十字线一次性给出该时段全部指标，避免来回切指标读数。
import { useMemo, useState } from 'react'
import ResponsiveSvgChart from '../../components/ResponsiveSvgChart'
import {
  modelColor, windowSeries, bucketShortLabel, fmtMs, fmtMetric, metricValue, metricOf,
} from './constants'

const CHART_H = 300
const PAD = { left: 62, right: 64, top: 16, bottom: 30 }
const GRID = '#e2e8f0'
const AXIS = '#94a3b8'
const TEXT = '#475569'
const MUTED = '#94a3b8'

export default function QoSTrendChart({ models, allNames, selected, onToggle, onAll, onNone, win, metricKey, t }) {
  const [hoverIdx, setHoverIdx] = useState(null)
  const [vw, setVw] = useState(720)

  const metric = metricOf(metricKey)
  const shown = useMemo(
    () => models.filter((m) => selected.has(m.dst_model_name)),
    [models, selected],
  )

  const series = useMemo(() => shown.map((m) => {
    const pts = windowSeries(m, win)
    return {
      name: m.dst_model_name,
      color: modelColor(allNames, m.dst_model_name),
      pts,
      vals: pts.map((p) => metricValue(p, metric)),
    }
  }), [shown, win, allNames, metric])

  const bucketCount = series.length ? series[0].pts.length : 0

  const yMax = useMemo(() => {
    let max = 0
    for (const s of series) {
      for (const v of s.vals) {
        if (v !== null && v > max) max = v
      }
    }
    if (max <= 0) return metric.kind === 'ms' ? 1000 : 1
    // 系数/百分比指标顶部留白略小，参考线更容易落在图内
    return max * 1.15
  }, [series, metric])

  const xLabels = useMemo(() => {
    const n = Math.min(6, bucketCount)
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
    return <div style={{ padding: 24, color: MUTED, fontSize: 13 }}>{t('serviceQuality.trend.empty')}</div>
  }

  return (
    <div>
      {/* 图例：色块 + 模型名，点击切换；空心点说明在脚注 */}
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
                color: on ? TEXT : MUTED,
              }}
            >
              <span style={{ width: 14, height: 3, borderRadius: 2, background: on ? color : '#cbd5e1' }} />
              {m.dst_model_name}
            </button>
          )
        })}
        <button
          onClick={() => (shown.length === models.length ? onNone() : onAll())}
          style={{
            marginLeft: 'auto', fontSize: 11, padding: '2px 8px', borderRadius: 999,
            border: '1px solid #e2e8f0', background: '#fff', color: TEXT, cursor: 'pointer',
          }}
        >{shown.length === models.length ? t('serviceQuality.trend.clear') : t('serviceQuality.trend.selectAll')}</button>
      </div>

      <div style={{ position: 'relative' }}>
        <ResponsiveSvgChart minWidth={200} height={CHART_H} onWidthChange={setVw}>
          {(W) => {
            const innerW = Math.max(120, W - PAD.left - PAD.right)
            const innerH = CHART_H - PAD.top - PAD.bottom
            const xOf = (i) => PAD.left + (bucketCount <= 1 ? 0 : (i / (bucketCount - 1)) * innerW)
            const yOf = (v) => PAD.top + innerH - (Math.max(0, v) / yMax) * innerH
            // 断开折线：null 值不连线（空桶 / 低样本）
            const linePath = (vals) => {
              let d = ''
              let pen = false
              vals.forEach((v, i) => {
                if (v === null) { pen = false; return }
                d += `${pen ? 'L' : 'M'}${xOf(i).toFixed(1)},${yOf(v).toFixed(1)}`
                pen = true
              })
              return d
            }
            const hx = hoverIdx !== null ? xOf(hoverIdx) : 0
            const thresholds = metric.thresholds.filter((th) => th.value > 0 && th.value < yMax)
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
                  const val = yMax * (1 - r)
                  return (
                    <g key={r}>
                      <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y} stroke={GRID} strokeWidth={1} />
                      <text x={PAD.left - 6} y={y + 3} textAnchor="end" fontSize={10} fill={AXIS}>
                        {fmtMetric(val, metric)}
                      </text>
                    </g>
                  )
                })}

                {/* 阈值参考线（虚线只承载阈值语义；标签用文本色，色块承载状态语义） */}
                {thresholds.map((th) => {
                  const y = yOf(th.value)
                  return (
                    <g key={th.value}>
                      <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y}
                        stroke={th.color} strokeWidth={1} strokeDasharray="4,4" opacity={0.75} />
                      <rect x={W - PAD.right + 6} y={y - 3} width={8} height={6} rx={2} fill={th.color} />
                      <text x={W - PAD.right + 17} y={y + 3} fontSize={9.5} fill={TEXT}>
                        {t(th.i18n)}
                      </text>
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

                {/* 模型折线（稀疏视图补小圆点：单点/断段也可见） */}
                {series.map((s) => (
                  <g key={s.name}>
                    <path d={linePath(s.vals)} fill="none" stroke={s.color} strokeWidth={2}
                      strokeLinecap="round" strokeLinejoin="round" />
                    {bucketCount <= 24 ? s.vals.map((v, i) => (v === null ? null : (
                      <circle key={i} cx={xOf(i)} cy={yOf(v)} r={2.2} fill={s.color} />
                    ))) : null}
                  </g>
                ))}

                {/* 进行中时段端点：空心点（实心=完整时段） */}
                {series.map((s) => {
                  const last = s.pts.length - 1
                  if (last < 0 || !s.pts[last].partial || s.vals[last] === null) return null
                  return (
                    <circle key={'partial-' + s.name} cx={xOf(last)} cy={yOf(s.vals[last])} r={3.5}
                      fill="#fff" stroke={s.color} strokeWidth={2} />
                  )
                })}

                {/* 十字线 + 悬浮点 */}
                {hoverIdx !== null ? (
                  <g>
                    <line x1={hx} x2={hx} y1={PAD.top} y2={PAD.top + innerH} stroke={AXIS} strokeWidth={1} />
                    {series.map((s) => (s.vals[hoverIdx] === null ? null : (
                      <circle key={s.name} cx={hx} cy={yOf(s.vals[hoverIdx])} r={3.5}
                        fill="#fff" stroke={s.color} strokeWidth={2} />
                    )))}
                  </g>
                ) : null}
              </svg>
            )
          }}
        </ResponsiveSvgChart>

        {hoverIdx !== null ? (
          <div style={{
            position: 'absolute',
            left: Math.min(PAD.left + (bucketCount <= 1 ? 0 : (hoverIdx / (bucketCount - 1)) * Math.max(120, vw - PAD.left - PAD.right)) + 12, Math.max(0, vw - 300)),
            top: 6,
            background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8,
            boxShadow: '0 4px 12px rgba(15,23,42,.08)', padding: '7px 10px', fontSize: 12,
            pointerEvents: 'none', minWidth: 250, maxWidth: 320,
          }}>
            <div style={{ color: MUTED, marginBottom: 4 }}>
              {series[0] && series[0].pts[hoverIdx] ? series[0].pts[hoverIdx].bucket : ''}
              {series[0] && series[0].pts[hoverIdx] && series[0].pts[hoverIdx].partial
                ? ` · ${t('serviceQuality.period.partial')}` : ''}
            </div>
            {series.map((s) => {
              const p = s.pts[hoverIdx]
              if (!p) return null
              const low = (Number(p.coefficient_samples) || 0) < 5
              return (
                <div key={s.name} style={{ marginBottom: 4 }}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 6, color: TEXT, fontWeight: 600 }}>
                    <span style={{ width: 8, height: 8, borderRadius: 999, background: s.color, flexShrink: 0 }} />
                    {s.name}
                    <span style={{ marginLeft: 'auto', fontWeight: 400, color: MUTED }}>
                      {t('serviceQuality.unit.times', { count: p.count })}
                    </span>
                  </div>
                  {p.count > 0 ? (
                    <div style={{
                      display: 'grid', gridTemplateColumns: 'auto 1fr auto 1fr', columnGap: 8,
                      rowGap: 1, fontSize: 11, color: TEXT, fontVariantNumeric: 'tabular-nums', marginLeft: 14,
                    }}>
                      <span style={{ color: MUTED }}>{t('serviceQuality.metric.avg')}</span>
                      <span>{fmtMs(p.ok_avg_elapsed_ms)}</span>
                      <span style={{ color: MUTED }}>{t('serviceQuality.metric.p95')}</span>
                      <span>{fmtMs(p.ok_p95_ms)}</span>
                      <span style={{ color: MUTED }}>{t('serviceQuality.metric.congestion')}</span>
                      <span>{low ? t('serviceQuality.cards.lowSample', { count: p.coefficient_samples || 0 }) : fmtMetric(p.avg_congestion, { kind: 'ratio' })}</span>
                      <span style={{ color: MUTED }}>{t('serviceQuality.metric.stall')}</span>
                      <span>{low ? '-' : fmtMetric(p.stall_rate, { kind: 'pct' })}</span>
                      <span style={{ color: MUTED }}>{t('serviceQuality.metric.error')}</span>
                      <span>{fmtMetric(p.error_rate, { kind: 'pct' })}</span>
                      <span style={{ color: MUTED }}>ms/Token</span>
                      <span>{Number(p.ok_ms_per_token) > 0 ? Number(p.ok_ms_per_token).toFixed(2) : '-'}</span>
                    </div>
                  ) : (
                    <div style={{ marginLeft: 14, fontSize: 11, color: MUTED }}>{t('serviceQuality.period.empty')}</div>
                  )}
                </div>
              )
            })}
          </div>
        ) : null}
      </div>
      <div style={{ marginTop: 4, fontSize: 11, color: MUTED }}>
        {t('serviceQuality.trend.partial')} · {t('serviceQuality.trend.hint')}
      </div>
    </div>
  )
}
