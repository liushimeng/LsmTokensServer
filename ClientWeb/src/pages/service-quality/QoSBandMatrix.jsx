// QoSBandMatrix：业务时段带 × 源站模型 矩阵（v2.0.86 阶段DA）。
//
// 横向比较的主视图：行 = 6 个业务时段带（凌晨/早间/上午/午间/下午/晚间），
// 列 = 各源站模型，单元格 = 该时段带的指标值。
//   - 指标可切换：拥堵系数（四档着色）/ 每小时调用强度 / 错误率（四档着色）；
//   - 系数与错误率按四档状态色着色，**数值始终直接标在格内**（不靠颜色单独传意）；
//   - 调用强度是量级不是序数等级 → 单色（不做 value-ramp，避免双重编码）。
// 行首给出时段带名 + 小时区间 + 日均调用 + 调用占比，让"这一行代表什么"一目了然。
import { useMemo, useState } from 'react'
import {
  TIME_BANDS, fmtHourRange, fmtNum, fmtNum1, fmtMetric, fmtPct,
  STATUS_COLORS, CONGESTION_TIERS, tierOfCongestion, modelColor, findBand,
} from './constants'

// BAND_METRICS 时段带矩阵指标集。
const BAND_METRICS = [
  { key: 'congestion', i18n: 'serviceQuality.metric.congestion', kind: 'ratio', tier: true },
  { key: 'callsPerHour', i18n: 'serviceQuality.band.metricPerHour', kind: 'num', tier: false },
  { key: 'error', i18n: 'serviceQuality.metric.error', kind: 'pct', tier: true },
  { key: 'share', i18n: 'serviceQuality.band.metricShare', kind: 'pct', tier: false },
]

export default function QoSBandMatrix({ models, allNames, t }) {
  const [metricKey, setMetricKey] = useState('congestion')
  const metric = BAND_METRICS.find((m) => m.key === metricKey) || BAND_METRICS[0]

  // 单元格取值：按模型取 bands_7d（近 7 天口径，与时段画像同源）
  const cellOf = useMemo(() => {
    const map = new Map() // modelName → { bandKey → band }
    for (const m of models) {
      const byKey = new Map()
      for (const b of (m.bands_7d || [])) byKey.set(b.key, b)
      map.set(m.dst_model_name, byKey)
    }
    return map
  }, [models])

  const valueOf = (band) => {
    if (!band) return null
    switch (metric.key) {
      case 'callsPerHour': {
        const v = Number(band.avg_calls_per_hour)
        return isFinite(v) ? v : null
      }
      case 'share': {
        const v = Number(band.share)
        return isFinite(v) ? v : null
      }
      case 'error': {
        const v = Number(band.error_rate)
        return isFinite(v) ? v : null
      }
      default: {
        const v = Number(band.avg_congestion)
        return isFinite(v) && v > 0 ? v : null
      }
    }
  }

  const cellStyle = (v) => {
    if (v === null) return { background: '#f8fafc', color: '#cbd5e1' }
    if (!metric.tier) return { background: '#eff6ff', color: '#0f172a' }
    return { background: STATUS_COLORS[tierOfCongestion(v)], color: v >= 2 ? '#fff' : '#0f172a' }
  }

  const fmtCell = (v) => {
    if (v === null) return '—'
    if (metric.kind === 'ratio') return v.toFixed(2)
    if (metric.kind === 'pct') return v >= 10 ? v.toFixed(0) + '%' : v.toFixed(1) + '%'
    return fmtNum1(v)
  }

  if (!models.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.bandMatrix.empty')}</div>
  }

  return (
    <div>
      {/* 指标切换 */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginBottom: 10 }}>
        {BAND_METRICS.map((m, i) => (
          <button
            key={m.key}
            className={'btn ' + (metricKey === m.key ? 'btn-primary' : '')}
            style={{
              borderRadius: i === 0 ? '6px 0 0 6px' : i === BAND_METRICS.length - 1 ? '0 6px 6px 0' : 0,
              padding: '2px 12px', fontSize: 12,
            }}
            onClick={() => setMetricKey(m.key)}
          >{t(m.i18n)}</button>
        ))}
        <span style={{ marginLeft: 'auto', fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.bandMatrix.hint')}</span>
      </div>

      <div style={{ overflowX: 'auto' }}>
        <table style={{ borderCollapse: 'collapse', width: '100%', minWidth: 560, fontSize: 12 }}>
          <thead>
            <tr>
              <th style={{ textAlign: 'left', padding: '6px 8px', borderBottom: '1px solid #e2e8f0', color: '#64748b', fontWeight: 600, whiteSpace: 'nowrap' }}>
                {t('serviceQuality.bandMatrix.bandCol')}
              </th>
              {models.map((m) => (
                <th
                  key={m.dst_model_name}
                  title={m.dst_model_name}
                  style={{
                    padding: '6px 4px', borderBottom: '1px solid #e2e8f0', color: '#334155',
                    fontWeight: 600, minWidth: 62, maxWidth: 92,
                    overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
                  }}
                >
                  <span style={{ display: 'inline-block', width: 7, height: 7, borderRadius: 999, marginRight: 4, background: modelColor(allNames, m.dst_model_name) }} />
                  {m.dst_model_name}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {TIME_BANDS.map((def) => {
              const defLabel = t(`serviceQuality.band.${def.key}`)
              return (
                <tr key={def.key}>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap' }}>
                    <div style={{ fontWeight: 700, color: '#0f172a' }}>{defLabel}</div>
                    <div style={{ fontSize: 10, color: '#94a3b8' }}>{fmtHourRange(def.start, def.end)}</div>
                    <BandSummary def={def} models={models} t={t} />
                  </td>
                  {models.map((m) => {
                    const band = (cellOf.get(m.dst_model_name) || new Map()).get(def.key)
                    const v = valueOf(band)
                    const st = cellStyle(v)
                    const tip = band
                      ? t('serviceQuality.bandMatrix.cellTip', {
                          model: m.dst_model_name,
                          band: defLabel,
                          metric: t(metric.i18n),
                          value: fmtCell(v),
                          calls: fmtNum(band.calls || 0),
                          err: fmtPct(band.error_rate),
                          avg: fmtMetric(band.ok_avg_elapsed_ms, { kind: 'ms' }),
                        })
                      : t('serviceQuality.bandMatrix.noData')
                    return (
                      <td
                        key={m.dst_model_name}
                        title={tip}
                        style={{
                          padding: '6px 4px', borderBottom: '1px solid #f1f5f9', textAlign: 'center',
                          background: st.background, color: st.color, fontVariantNumeric: 'tabular-nums',
                        }}
                      >
                        {fmtCell(v)}
                      </td>
                    )
                  })}
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* 共享图例 */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 8, alignItems: 'center', fontSize: 11, color: '#475569' }}>
        {metric.tier ? CONGESTION_TIERS.map((tr) => (
          <span key={tr.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 3, background: tr.color }} />
            {t(`serviceQuality.status.${tr.key}`)}
          </span>
        )) : (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 3, background: '#eff6ff', border: '1px solid #e2e8f0' }} />
            {t(metric.i18n)}
          </span>
        )}
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          <span style={{ width: 10, height: 10, borderRadius: 3, background: '#f8fafc', border: '1px solid #e2e8f0' }} />
          {t('serviceQuality.heatmap.noData')}
        </span>
      </div>
    </div>
  )
}

// BandSummary 行首汇总（全站口径的该时段带调用量与占比）。
function BandSummary({ def, models, t }) {
  let calls = 0
  let perDay = 0
  let share = 0
  for (const m of models) {
    const b = findBand(m.bands_7d || [], def.key)
    if (!b) continue
    calls += Number(b.calls) || 0
    perDay += Number(b.avg_calls_per_day) || 0
    share += Number(b.share) || 0
  }
  return (
    <div style={{ fontSize: 10, color: '#64748b', marginTop: 2 }}>
      {t('serviceQuality.bandMatrix.rowSummary', {
        calls: fmtNum(calls),
        perDay: fmtNum1(perDay),
        share: share.toFixed(1),
      })}
    </div>
  )
}
