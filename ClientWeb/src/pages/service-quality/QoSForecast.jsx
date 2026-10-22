// QoSForecast：运行经验预测（v2.0.85）。
// 左：今日逐小时折线 —— 已发生小时画实际拥堵系数（当前进行中小时空心点），
//     剩余小时画预测（虚线 + 空心点，源站近 7 天同时段加权均值），
//     叠加 1.2 / 2.0 / 5.0 阈值虚线（虚线仅承载阈值语义）。
// 右：研判面板 —— 下一时段预测（四档着色 + 可信度）、明日预测（星期模式）、
//     趋势研判（近24h 环比 + 今日 vs 周均值）。
// 预测是「经验外推」不是承诺：可信度低时显式标注「仅供参考」。
import { useMemo, useState } from 'react'
import ResponsiveSvgChart from '../../components/ResponsiveSvgChart'
import {
  STATUS_COLORS, CONGESTION_TIERS, tierOfCongestion,
  fmtNum, fmtSignedPct, WEEKDAY_KEYS,
} from './constants'

const CHART_H = 260
const PAD = { left: 40, right: 14, top: 14, bottom: 26 }
const GRID = '#e2e8f0'
const AXIS = '#94a3b8'
const TEXT = '#475569'
const ACTUAL = '#2a78d6'
const PREDICT = '#6250d6'

export default function QoSForecast({ model, allNames, t }) {
  const [hoverHour, setHoverHour] = useState(null)
  const [vw, setVw] = useState(720)
  const currentHour = new Date().getHours()

  // 今日逐小时：实际取自热力图今天行（heatmap[6][h]，-1 = 无请求），
  // 预测取自 prediction.today_remaining；next_hour 在 23 点时属于明日，不并入。
  const hours = useMemo(() => {
    const out = []
    if (!model) return out
    const heat = Array.isArray(model.heatmap) ? model.heatmap[model.heatmap.length - 1] : null
    const counts = Array.isArray(model.heatmap_counts) ? model.heatmap_counts[model.heatmap_counts.length - 1] : null
    const predMap = {}
    for (const f of ((model.prediction && model.prediction.today_remaining) || [])) {
      predMap[f.hour] = f
    }
    for (let h = 0; h < 24; h++) {
      const actual = heat && heat[h] >= 0 ? heat[h] : null
      out.push({
        hour: h,
        actual,
        actualCount: counts ? Number(counts[h]) || 0 : 0,
        partial: h === currentHour,
        forecast: predMap[h] || null,
      })
    }
    return out
  }, [model, currentHour])

  const hasAny = hours.some((h) => h.actual !== null || (h.forecast && h.forecast.samples > 0))

  if (!model || !hasAny) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.forecast.empty')}</div>
  }

  const pred = model.prediction || {}
  const next = pred.next_hour || {}
  const nextTier = next.samples > 0 ? tierOfCongestion(next.avg_congestion) : null
  const tomorrow = pred.tomorrow || {}
  const tomorrowTier = tomorrow.avg_congestion > 0 ? tierOfCongestion(tomorrow.avg_congestion) : null

  const yMax = (() => {
    let max = 6 // 至少覆盖 5.0 严重线
    for (const h of hours) {
      if (h.actual !== null && h.actual > max) max = h.actual
      if (h.forecast && h.forecast.avg_congestion > max) max = h.forecast.avg_congestion
    }
    return max * 1.12
  })()

  const xOf = (i, W) => PAD.left + ((W - PAD.left - PAD.right) / 24) * (i + 0.5)
  const yOf = (v) => PAD.top + (CHART_H - PAD.top - PAD.bottom) * (1 - Math.min(v, yMax) / yMax)
  const actualPts = hours.filter((h) => h.actual !== null)
  const forecastPts = hours.filter((h) => h.forecast && h.forecast.samples > 0)

  return (
    <div style={{ display: 'flex', gap: 18, flexWrap: 'wrap' }}>
      {/* 左：今日实际 + 剩余预测折线 */}
      <div style={{ flex: '1 1 480px', minWidth: 320, position: 'relative' }}>
        <div style={{ display: 'flex', gap: 12, marginBottom: 6, alignItems: 'center', fontSize: 11, color: TEXT }}>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 14, height: 3, background: ACTUAL, borderRadius: 2 }} />
            {t('serviceQuality.forecast.chart.actual')}
          </span>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 14, height: 0, borderTop: '2px dashed ' + PREDICT }} />
            {t('serviceQuality.forecast.chart.predicted')}
          </span>
        </div>
        <ResponsiveSvgChart minWidth={200} height={CHART_H} onWidthChange={setVw}>
          {(W) => (
            <svg
              width={W} height={CHART_H}
              onMouseLeave={() => setHoverHour(null)}
              onMouseMove={(e) => {
                const rect = e.currentTarget.getBoundingClientRect()
                const x = e.clientX - rect.left
                const h = Math.floor(((x - PAD.left) / (W - PAD.left - PAD.right)) * 24)
                setHoverHour(h >= 0 && h < 24 ? h : null)
              }}
            >
              {/* 阈值参考线（虚线） */}
              {CONGESTION_TIERS.filter((tr) => isFinite(tr.max)).map((tr) => (
                <line key={tr.key}
                  x1={PAD.left} x2={W - PAD.right} y1={yOf(tr.max)} y2={yOf(tr.max)}
                  stroke={tr.color} strokeWidth={1} strokeDasharray="4 3" opacity={0.55} />
              ))}
              {[0, 0.5, 1].map((r) => {
                const y = PAD.top + (CHART_H - PAD.top - PAD.bottom) * r
                return (
                  <g key={r}>
                    <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y} stroke={GRID} strokeWidth={1} />
                    <text x={PAD.left - 6} y={y + 3} textAnchor="end" fontSize={10} fill={AXIS}>
                      {(yMax * (1 - r)).toFixed(0)}
                    </text>
                  </g>
                )
              })}
              {/* X 轴小时刻度（每 3 小时） */}
              {hours.map((h, i) => (i % 3 === 0 ? (
                <text key={i} x={xOf(i, W)} y={CHART_H - 8} textAnchor="middle" fontSize={10} fill={AXIS}>
                  {String(i).padStart(2, '0')}
                </text>
              ) : null))}
              {/* 预测（虚线 + 空心点） */}
              {forecastPts.length ? (
                <polyline
                  fill="none" stroke={PREDICT} strokeWidth={1.6} strokeDasharray="5 4"
                  points={forecastPts.map((h) => `${xOf(h.hour, W)},${yOf(h.forecast.avg_congestion)}`).join(' ')}
                />
              ) : null}
              {forecastPts.map((h) => (
                <circle key={'f' + h.hour}
                  cx={xOf(h.hour, W)} cy={yOf(h.forecast.avg_congestion)} r={3}
                  fill="#fff" stroke={PREDICT} strokeWidth={1.6} />
              ))}
              {/* 实际（实线；进行中小时空心点） */}
              {actualPts.length ? (
                <polyline
                  fill="none" stroke={ACTUAL} strokeWidth={1.8}
                  points={actualPts.map((h) => `${xOf(h.hour, W)},${yOf(h.actual)}`).join(' ')}
                />
              ) : null}
              {actualPts.map((h) => (
                <circle key={'a' + h.hour}
                  cx={xOf(h.hour, W)} cy={yOf(h.actual)} r={3}
                  fill={h.partial ? '#fff' : ACTUAL} stroke={ACTUAL} strokeWidth={1.8} />
              ))}
              {/* 悬浮十字线 */}
              {hoverHour !== null && hours[hoverHour] ? (
                <line x1={xOf(hoverHour, W)} x2={xOf(hoverHour, W)}
                  y1={PAD.top} y2={CHART_H - PAD.bottom} stroke="#0f172a" strokeWidth={0.8} opacity={0.25} />
              ) : null}
            </svg>
          )}
        </ResponsiveSvgChart>
        {hoverHour !== null && hours[hoverHour] ? (
          <div style={{
            position: 'absolute',
            left: Math.min(PAD.left + ((vw - PAD.left - PAD.right) / 24) * (hoverHour + 0.5) + 10, Math.max(0, vw - 210)),
            top: 22,
            background: '#fff', border: '1px solid #e2e8f0', borderRadius: 8,
            boxShadow: '0 4px 12px rgba(15,23,42,.08)', padding: '5px 10px', fontSize: 12,
            pointerEvents: 'none', color: TEXT, maxWidth: 220,
          }}>
            <div style={{ color: '#94a3b8', marginBottom: 2 }}>
              {String(hoverHour).padStart(2, '0')}:00
              {hours[hoverHour].partial ? ` · ${t('serviceQuality.period.partial')}` : ''}
            </div>
            {hours[hoverHour].actual !== null ? (
              <div>{t('serviceQuality.forecast.tooltip.actual', { v: hours[hoverHour].actual.toFixed(2), count: fmtNum(hours[hoverHour].actualCount) })}</div>
            ) : null}
            {hours[hoverHour].forecast && hours[hoverHour].forecast.samples > 0 ? (
              <div>{t('serviceQuality.forecast.tooltip.predicted', {
                v: hours[hoverHour].forecast.avg_congestion.toFixed(2),
                conf: t(`serviceQuality.forecast.conf.${hours[hoverHour].forecast.confidence}`),
                count: fmtNum(hours[hoverHour].forecast.samples),
              })}</div>
            ) : null}
            {hours[hoverHour].actual === null && !(hours[hoverHour].forecast && hours[hoverHour].forecast.samples > 0)
              ? <div>{t('serviceQuality.forecast.tooltip.none')}</div> : null}
          </div>
        ) : null}
      </div>

      {/* 右：研判面板 */}
      <div style={{ flex: '0 0 250px', minWidth: 230, display: 'flex', flexDirection: 'column', gap: 10 }}>
        {/* 下一时段预测 */}
        <div style={{ border: '1px solid #e2e8f0', borderRadius: 10, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 4 }}>{t('serviceQuality.forecast.nextHour')}</div>
          {nextTier ? (
            <>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 6 }}>
                <span style={{ fontSize: 22, fontWeight: 800, color: STATUS_COLORS[nextTier] }}>
                  {next.avg_congestion.toFixed(2)}
                </span>
                <span style={{ fontSize: 12, color: STATUS_COLORS[nextTier], fontWeight: 600 }}>
                  {t(`serviceQuality.status.${nextTier}`)}
                </span>
              </div>
              <div style={{ fontSize: 11, color: '#94a3b8', marginTop: 2 }}>
                {t(`serviceQuality.forecast.conf.${next.confidence}`)}
                {' · '}
                {t('serviceQuality.forecast.nextHourSub', {
                  hour: String(next.hour).padStart(2, '0'),
                  nextHour: String((next.hour + 1) % 24).padStart(2, '0'),
                  count: fmtNum(next.samples),
                })}
              </div>
            </>
          ) : (
            <div style={{ fontSize: 12, color: '#cbd5e1' }}>{t('serviceQuality.forecast.noHistory')}</div>
          )}
        </div>

        {/* 明日预测 */}
        <div style={{ border: '1px solid #e2e8f0', borderRadius: 10, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 4 }}>{t('serviceQuality.forecast.tomorrow')}</div>
          {tomorrow.avg_congestion > 0 ? (
            <>
              <div style={{ fontSize: 13, fontWeight: 700, color: '#0f172a' }}>
                {tomorrow.date.slice(5)} {WEEKDAY_KEYS[tomorrow.weekday] ? t(WEEKDAY_KEYS[tomorrow.weekday]) : ''}
              </div>
              <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginTop: 2 }}>
                <span style={{ fontSize: 18, fontWeight: 800, color: tomorrowTier ? STATUS_COLORS[tomorrowTier] : TEXT }}>
                  {tomorrow.avg_congestion.toFixed(2)}
                </span>
                <span style={{ fontSize: 11, color: '#64748b' }}>{t('serviceQuality.metric.congestion')}</span>
              </div>
              <div style={{ fontSize: 11, color: '#64748b', lineHeight: 1.7 }}>
                <div>{t('serviceQuality.metric.error')} {Number(tomorrow.error_rate) ? tomorrow.error_rate.toFixed(1) + '%' : '0%'}</div>
                {tomorrow.calls > 0 ? <div>{t('serviceQuality.forecast.tomorrowCalls', { count: fmtNum(tomorrow.calls) })}</div> : null}
                <div>{t(`serviceQuality.forecast.conf.${tomorrow.confidence || 'low'}`)}</div>
              </div>
            </>
          ) : (
            <div style={{ fontSize: 12, color: '#cbd5e1' }}>{t('serviceQuality.forecast.noHistory')}</div>
          )}
        </div>

        {/* 趋势研判 */}
        <div style={{ border: '1px solid #e2e8f0', borderRadius: 10, padding: '10px 12px' }}>
          <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 4 }}>{t('serviceQuality.forecast.trend')}</div>
          <div style={{ fontSize: 12, color: TEXT, lineHeight: 1.8 }}>
            <div>
              {pred.trend_24h === 'up' ? <span style={{ color: STATUS_COLORS.severe }}>↑ </span>
                : pred.trend_24h === 'down' ? <span style={{ color: STATUS_COLORS.ok }}>↓ </span>
                  : <span>→ </span>}
              {t(`serviceQuality.forecast.trend24h.${pred.trend_24h || 'unknown'}`)}
            </div>
            {pred.today_vs_week ? (
              <div>
                <span style={{ color: pred.today_vs_week > 0 ? STATUS_COLORS.moderate : STATUS_COLORS.ok }}>
                  {fmtSignedPct(pred.today_vs_week)}
                </span>
                {' '}{t('serviceQuality.forecast.todayVsWeekTail')}
              </div>
            ) : null}
          </div>
        </div>
      </div>
    </div>
  )
}
