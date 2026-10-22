// QoSForecast：运行经验预测 —— **全模型**视图（v2.0.86 阶段DA）。
//
// v2.0.85 原本是「单模型 + 模型下拉」；v2.0.86 按需求删除模型下拉，
// 改为所有模型一起呈现（源站数常 >8，分类色槽不够 → 用**小倍数**而非叠图，
// 避免颜色循环复用导致无法区分）：
//
//   ① 下一时段预测：全模型横向条形（拥堵系数 / 错误率 切换），按值降序，
//      四档状态色着色 + 数值 + 文字标签（不靠颜色单独传意）。
//   ② 明日预测：全模型表（系数 / 错误率 / 预计调用 / 可信度 / 明日高峰时段带）。
//   ③ 小倍数网格：每模型一张「今日实际(实线) + 剩余时段预测(虚线)」迷你折线，
//      叠加 1.2 / 2.0 / 5.0 阈值虚线，图下给出下一时段 / 明日 / 峰谷摘要。
//
// 预测是「经验外推」不是承诺：可信度低时显式标注「仅供参考」。
import { useMemo, useState } from 'react'
import {
  STATUS_COLORS, CONGESTION_TIERS, tierOfCongestion,
  fmtNum, fmtPct, fmtRatio, fmtHour, fmtSignedPct, WEEKDAY_KEYS, modelColor,
} from './constants'

const MINI_H = 120
const GRID = '#e2e8f0'
const AXIS = '#94a3b8'
const TEXT = '#475569'
const ACTUAL = '#2a78d6'
const PREDICT = '#6250d6'

// NEXT_METRICS 下一时段预测条形图指标集。
const NEXT_METRICS = [
  { key: 'congestion', i18n: 'serviceQuality.metric.congestion', kind: 'ratio' },
  { key: 'error', i18n: 'serviceQuality.metric.error', kind: 'pct' },
]

export default function QoSForecast({ models, allNames, t }) {
  const [metricKey, setMetricKey] = useState('congestion')
  const metric = NEXT_METRICS.find((m) => m.key === metricKey) || NEXT_METRICS[0]

  // 下一时段预测：全模型（按值降序；无同时段历史的模型排尾部并置灰）
  const nextRows = useMemo(() => {
    const out = []
    for (const m of models) {
      const next = (m.prediction && m.prediction.next_hour) || {}
      const raw = metric.key === 'error' ? next.error_rate : next.avg_congestion
      const v = Number(raw) || 0
      out.push({
        name: m.dst_model_name,
        value: v,
        samples: Number(next.samples) || 0,
        confidence: next.confidence || 'low',
        hour: next.hour,
        noHistory: (Number(next.samples) || 0) <= 0,
      })
    }
    out.sort((a, b) => (a.noHistory === b.noHistory ? b.value - a.value : (a.noHistory ? 1 : -1)))
    return out
  }, [models, metric])

  const maxV = Math.max(1e-9, ...nextRows.filter((r) => !r.noHistory).map((r) => r.value))

  if (!models.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.forecast.empty')}</div>
  }

  return (
    <div>
      {/* ① 下一时段预测（全模型条形） */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap', marginBottom: 8 }}>
        <span style={{ fontSize: 12, fontWeight: 600, color: TEXT }}>{t('serviceQuality.forecast.nextHourAll')}</span>
        <span style={{ display: 'inline-flex' }}>
          {NEXT_METRICS.map((m, i) => (
            <button
              key={m.key}
              className={'btn ' + (metricKey === m.key ? 'btn-primary' : '')}
              style={{
                borderRadius: i === 0 ? '6px 0 0 6px' : i === NEXT_METRICS.length - 1 ? '0 6px 6px 0' : 0,
                padding: '2px 12px', fontSize: 12,
              }}
              onClick={() => setMetricKey(m.key)}
            >{t(m.i18n)}</button>
          ))}
        </span>
        <span style={{ marginLeft: 'auto', fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.forecast.nextHourAllHint')}</span>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 5, marginBottom: 14 }}>
        {nextRows.map((r) => {
          const tier = r.noHistory ? null : tierOfCongestion(r.value)
          const color = tier ? STATUS_COLORS[tier] : '#cbd5e1'
          const widthPct = r.noHistory ? 0 : (r.value / maxV) * 100
          return (
            <div
              key={r.name}
              style={{ display: 'flex', alignItems: 'center', gap: 8 }}
              title={t('serviceQuality.forecast.nextRowTip', {
                model: r.name,
                metric: t(metric.i18n),
                value: r.noHistory ? '-' : (metric.kind === 'ratio' ? fmtRatio(r.value) : fmtPct(r.value)),
                count: fmtNum(r.samples),
              })}
            >
              <span style={{
                width: 190, flexShrink: 0, fontSize: 12, color: '#334155',
                overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', textAlign: 'right',
              }}>
                <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 999, marginRight: 5, background: modelColor(allNames, r.name) }} />
                {r.name}
              </span>
              <span style={{ flex: 1, minWidth: 60, background: '#f1f5f9', borderRadius: 4, height: 16, position: 'relative', overflow: 'hidden' }}>
                <span style={{
                  position: 'absolute', left: 0, top: 0, bottom: 0,
                  width: Math.max(1.5, widthPct) + '%', background: color, borderRadius: 4, opacity: 0.92,
                }} />
              </span>
              <span style={{ width: 92, flexShrink: 0, fontSize: 12, fontVariantNumeric: 'tabular-nums', color: r.noHistory ? '#94a3b8' : '#0f172a' }}>
                {r.noHistory ? '—' : (metric.kind === 'ratio' ? fmtRatio(r.value) : fmtPct(r.value))}
              </span>
              <span style={{ width: 84, flexShrink: 0, fontSize: 11, color: tier ? color : '#94a3b8' }}>
                {r.noHistory ? t('serviceQuality.cards.noHistory') : t(`serviceQuality.status.${tier}`)}
              </span>
              <span style={{ width: 96, flexShrink: 0, fontSize: 11, color: '#94a3b8' }}>
                {r.noHistory ? '' : t(`serviceQuality.forecast.conf.${r.confidence}`)}
              </span>
            </div>
          )
        })}
      </div>

      {/* ② 明日预测（全模型表） */}
      <div style={{ fontSize: 12, fontWeight: 600, color: TEXT, marginBottom: 6 }}>
        {t('serviceQuality.forecast.tomorrowAll')}
      </div>
      <div style={{ overflowX: 'auto' }}>
        <table style={{ borderCollapse: 'collapse', width: '100%', minWidth: 620, fontSize: 12 }}>
          <thead>
            <tr>
              {[
                'serviceQuality.table.model',
                'serviceQuality.forecast.tomorrowCongestion',
                'serviceQuality.forecast.tomorrowError',
                'serviceQuality.forecast.tomorrowCallsCol',
                'serviceQuality.forecast.confidence',
                'serviceQuality.forecast.tomorrowPeakBand',
              ].map((k) => (
                <th key={k} style={{ textAlign: 'left', padding: '6px 8px', borderBottom: '1px solid #e2e8f0', color: '#64748b', fontWeight: 600, whiteSpace: 'nowrap' }}>
                  {t(k)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {models.map((m) => {
              const tm = (m.prediction && m.prediction.tomorrow) || {}
              const pv = (m.peak_valley) || {}
              const tier = Number(tm.avg_congestion) > 0 ? tierOfCongestion(tm.avg_congestion) : null
              return (
                <tr key={m.dst_model_name}>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap' }}>
                    <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 999, marginRight: 5, background: modelColor(allNames, m.dst_model_name) }} />
                    <b style={{ color: '#0f172a' }}>{m.dst_model_name}</b>
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums', color: tier ? STATUS_COLORS[tier] : '#94a3b8', fontWeight: tier ? 700 : 400 }}>
                    {Number(tm.avg_congestion) > 0 ? fmtRatio(tm.avg_congestion) : '—'}
                    {tier ? <span style={{ marginLeft: 6, fontSize: 10, fontWeight: 400 }}>{t(`serviceQuality.status.${tier}`)}</span> : null}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums' }}>
                    {Number(tm.error_rate) ? fmtPct(tm.error_rate) : '0%'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums' }}>
                    {Number(tm.calls) > 0 ? fmtNum(tm.calls) : '—'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', color: '#64748b' }}>
                    {t(`serviceQuality.forecast.conf.${tm.confidence || 'low'}`)}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap', color: '#475569' }}>
                    {pv.tomorrow_peak_band_key ? t(`serviceQuality.band.${pv.tomorrow_peak_band_key}`) : '—'}
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* ③ 小倍数网格：每模型今日实际 + 剩余预测 */}
      <div style={{ marginTop: 16 }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: TEXT, marginBottom: 8 }}>
          {t('serviceQuality.forecast.gridTitle')}
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(300px, 1fr))', gap: 12 }}>
          {models.map((m) => (
            <MiniForecast key={m.dst_model_name} model={m} t={t} />
          ))}
        </div>
      </div>
    </div>
  )
}

// MiniForecast 单模型「今日实际 + 剩余预测」迷你折线（小倍数网格的一格）。
function MiniForecast({ model, t }) {
  const currentHour = new Date().getHours()

  const { actuals, forecasts, yMax } = useMemo(() => {
    const heat = Array.isArray(model.heatmap) ? model.heatmap[model.heatmap.length - 1] : null
    const predMap = {}
    for (const f of ((model.prediction && model.prediction.today_remaining) || [])) {
      predMap[f.hour] = f
    }
    const a = []
    const f = []
    let max = 6 // 至少覆盖 5.0 严重线
    for (let h = 0; h < 24; h++) {
      const av = heat && heat[h] >= 0 ? heat[h] : null
      a.push(av)
      const fv = predMap[h] && predMap[h].samples > 0 ? predMap[h].avg_congestion : null
      f.push(fv)
      if (av !== null && av > max) max = av
      if (fv !== null && fv > max) max = fv
    }
    return { actuals: a, forecasts: f, yMax: max * 1.12 }
  }, [model])

  const hasAny = actuals.some((v) => v !== null) || forecasts.some((v) => v !== null)
  if (!hasAny) {
    return (
      <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, padding: '10px 12px', background: '#fff' }}>
        <div style={{ fontSize: 12, fontWeight: 600, color: '#334155', marginBottom: 4 }}>{model.dst_model_name}</div>
        <div style={{ fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.forecast.empty')}</div>
      </div>
    )
  }

  const pad = { left: 34, right: 8, top: 8, bottom: 16 }
  const W = 280
  const innerW = W - pad.left - pad.right
  const innerH = MINI_H - pad.top - pad.bottom
  const xOf = (h) => pad.left + (innerW / 24) * (h + 0.5)
  const yOf = (v) => pad.top + innerH * (1 - Math.min(v, yMax) / yMax)
  const actualPts = actuals.map((v, h) => (v === null ? null : `${xOf(h)},${yOf(v)}`)).filter(Boolean)
  const forecastPts = forecasts.map((v, h) => (v === null ? null : `${xOf(h)},${yOf(v)}`)).filter(Boolean)
  const pred = model.prediction || {}
  const next = pred.next_hour || {}
  const nextTier = next.samples > 0 ? tierOfCongestion(next.avg_congestion) : null
  const tomorrow = pred.tomorrow || {}
  const pv = model.peak_valley || {}

  return (
    <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, padding: '10px 12px', background: '#fff' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <b style={{ fontSize: 12, color: '#334155', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={model.dst_model_name}>
          {model.dst_model_name}
        </b>
        <span style={{ marginLeft: 'auto', fontSize: 10, color: '#94a3b8', display: 'inline-flex', alignItems: 'center', gap: 8 }}>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
            <span style={{ width: 10, height: 2.5, background: ACTUAL, borderRadius: 2 }} />
            {t('serviceQuality.forecast.chart.actual')}
          </span>
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 3 }}>
            <span style={{ width: 10, height: 0, borderTop: '2px dashed ' + PREDICT }} />
            {t('serviceQuality.forecast.chart.predicted')}
          </span>
        </span>
      </div>

      <svg width="100%" height={MINI_H} viewBox={`0 0 ${W} ${MINI_H}`}>
        {/* 阈值参考线（虚线仅承载阈值语义） */}
        {CONGESTION_TIERS.filter((tr) => isFinite(tr.max)).map((tr) => (
          <line key={tr.key}
            x1={pad.left} x2={W - pad.right} y1={yOf(tr.max)} y2={yOf(tr.max)}
            stroke={tr.color} strokeWidth={1} strokeDasharray="4 3" opacity={0.5} />
        ))}
        {[0, 0.5, 1].map((r) => {
          const y = pad.top + innerH * r
          return (
            <g key={r}>
              <line x1={pad.left} x2={W - pad.right} y1={y} y2={y} stroke={GRID} strokeWidth={1} />
              <text x={pad.left - 4} y={y + 3} textAnchor="end" fontSize={8} fill={AXIS}>
                {(yMax * (1 - r)).toFixed(0)}
              </text>
            </g>
          )
        })}
        {/* 预测（虚线 + 空心点） */}
        {forecastPts.length ? (
          <polyline fill="none" stroke={PREDICT} strokeWidth={1.4} strokeDasharray="4 3" points={forecastPts.join(' ')} />
        ) : null}
        {forecasts.map((v, h) => (v === null ? null : (
          <circle key={'f' + h} cx={xOf(h)} cy={yOf(v)} r={2.4} fill="#fff" stroke={PREDICT} strokeWidth={1.3} />
        )))}
        {/* 实际（实线；进行中小时空心点） */}
        {actualPts.length ? (
          <polyline fill="none" stroke={ACTUAL} strokeWidth={1.6} points={actualPts.join(' ')} />
        ) : null}
        {actuals.map((v, h) => (v === null ? null : (
          <circle key={'a' + h} cx={xOf(h)} cy={yOf(v)} r={2.4}
            fill={h === currentHour ? '#fff' : ACTUAL} stroke={ACTUAL} strokeWidth={1.3} />
        )))}
        {/* X 轴刻度 */}
        {[0, 6, 12, 18].map((h) => (
          <text key={h} x={xOf(h)} y={MINI_H - 3} textAnchor="middle" fontSize={8} fill={AXIS}>
            {String(h).padStart(2, '0')}
          </text>
        ))}
      </svg>

      {/* 摘要行：下一时段 / 明日 / 峰谷 */}
      <div style={{ fontSize: 11, color: '#64748b', lineHeight: 1.7, marginTop: 4 }}>
        <div>
          {t('serviceQuality.cards.nextHour')}
          {': '}
          {nextTier ? (
            <span style={{ color: STATUS_COLORS[nextTier], fontWeight: 600 }}>
              {fmtRatio(next.avg_congestion)} · {t(`serviceQuality.status.${nextTier}`)}
            </span>
          ) : (
            <span style={{ color: '#cbd5e1' }}>{t('serviceQuality.cards.noHistory')}</span>
          )}
          {next && next.samples > 0 ? (
            <span style={{ color: '#94a3b8' }}> · {t(`serviceQuality.forecast.conf.${next.confidence}`)}</span>
          ) : null}
        </div>
        <div>
          {t('serviceQuality.forecast.tomorrow')}
          {': '}
          {Number(tomorrow.avg_congestion) > 0 ? fmtRatio(tomorrow.avg_congestion) : '—'}
          {tomorrow.date ? (
            <span style={{ color: '#94a3b8' }}>
              {' '}({tomorrow.date.slice(5)}{WEEKDAY_KEYS[tomorrow.weekday] ? ' ' + t(WEEKDAY_KEYS[tomorrow.weekday]) : ''})
            </span>
          ) : null}
          {pred.trend_24h && pred.trend_24h !== 'unknown' ? (
            <span style={{ color: pred.trend_24h === 'up' ? STATUS_COLORS.moderate : STATUS_COLORS.ok }}>
              {' '}{pred.trend_24h === 'up' ? '↑' : pred.trend_24h === 'down' ? '↓' : '→'}
            </span>
          ) : null}
          {pred.today_vs_week ? (
            <span style={{ color: '#94a3b8' }}> {fmtSignedPct(pred.today_vs_week)}</span>
          ) : null}
        </div>
        <div style={{ color: '#94a3b8' }}>
          {t('serviceQuality.profile.peakValleyShort', {
            peak: fmtHour(pv.peak_hour),
            valley: fmtHour(pv.valley_hour),
          })}
          {pv.forecast_peak_hour >= 0 ? (
            <span style={{ color: '#64748b' }}>
              {' · '}{t('serviceQuality.profile.forecastPeak', { hour: fmtHour(pv.forecast_peak_hour) })}
            </span>
          ) : null}
        </div>
      </div>
    </div>
  )
}
