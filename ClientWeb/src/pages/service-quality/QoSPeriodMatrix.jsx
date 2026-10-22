// QoSPeriodMatrix：「时段 × 模型」矩阵（v2.0.86 阶段DA）。
//
// 逐时段明细的图形化视图（替代旧版的单模型下拉）：
//   - 行 = 时段（1h → 60 个分钟桶；24h → 24 个小时桶；7d → 7 个天桶；30d → 30 个天桶）；
//   - 列 = 源站模型；
//   - 单元格 = 该时段该模型的指标值（可切换：拥堵系数 / 请求数 / 错误率 / 成功平均时延）。
//
// 颜色口径：系数与错误率按四档状态色着色（语义色 + 数值直接标注）；
// 请求数与时延是**量级**不是序数等级 → 单色，不做 value-ramp（避免双重编码）。
// 系数类指标样本 < MIN_COEFFICIENT_SAMPLES 的单元格显示「—」并注明低样本。
import { useMemo, useState } from 'react'
import {
  PERIOD_MATRIX_METRICS, periodMatrixMetricOf, windowSeries, bucketShortLabel,
  fmtMetric, fmtNum, fmtPct, fmtRatio, MIN_COEFFICIENT_SAMPLES,
  STATUS_COLORS, CONGESTION_TIERS, tierOfCongestion, modelColor,
} from './constants'

export default function QoSPeriodMatrix({ models, allNames, win, t }) {
  const [metricKey, setMetricKey] = useState('congestion')
  const metric = periodMatrixMetricOf(metricKey)

  // 按时段（行）收集各模型的桶数据：rows[i].cells = [model0Bucket, model1Bucket, ...]
  const rows = useMemo(() => {
    if (!models.length) return []
    const series = models.map((m) => windowSeries(m, win))
    const n = series.reduce((max, s) => Math.max(max, s.length), 0)
    const out = []
    for (let i = 0; i < n; i++) {
      const cells = series.map((s) => (i < s.length ? s[i] : null))
      const label = cells.find((c) => c) ? cells.find((c) => c).bucket : ''
      out.push({ idx: i, label, short: bucketShortLabel(label), cells, partial: cells.some((c) => c && c.partial) })
    }
    return out
  }, [models, win])

  const valueOf = (p) => {
    if (!p || !p.count) return null
    if (metric.key === 'congestion') {
      if ((Number(p.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES) return null
      const v = Number(p.avg_congestion)
      return isFinite(v) && v > 0 ? v : null
    }
    if (metric.key === 'error') {
      const v = Number(p.error_rate)
      return isFinite(v) ? v : null
    }
    if (metric.key === 'calls') {
      const v = Number(p.count)
      return isFinite(v) ? v : null
    }
    const v = Number(p.ok_avg_elapsed_ms)
    return isFinite(v) && v > 0 ? v : null
  }

  const cellStyle = (v) => {
    if (v === null) return { background: '#f8fafc', color: '#cbd5e1' }
    if (!metric.tier) return { background: '#eff6ff', color: '#0f172a' }
    return { background: STATUS_COLORS[tierOfCongestion(v)], color: v >= 2 ? '#fff' : '#0f172a' }
  }

  const fmtCell = (v) => {
    if (v === null) return '—'
    if (metric.kind === 'ratio') return fmtRatio(v)
    if (metric.kind === 'pct') return fmtPct(v)
    if (metric.kind === 'ms') return fmtMetric(v, { kind: 'ms' })
    return fmtNum(v)
  }

  if (!models.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.matrix.empty')}</div>
  }

  return (
    <div>
      {/* 指标切换（一个筛选器统辖本模块） */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginBottom: 10 }}>
        {PERIOD_MATRIX_METRICS.map((m, i) => (
          <button
            key={m.key}
            className={'btn ' + (metricKey === m.key ? 'btn-primary' : '')}
            style={{
              borderRadius: i === 0 ? '6px 0 0 6px' : i === PERIOD_MATRIX_METRICS.length - 1 ? '0 6px 6px 0' : 0,
              padding: '2px 12px', fontSize: 12,
            }}
            onClick={() => setMetricKey(m.key)}
          >{t(m.i18n)}</button>
        ))}
        <span style={{ marginLeft: 'auto', fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.matrix.hint')}</span>
      </div>

      <div style={{ overflowX: 'auto', maxHeight: 460, overflowY: 'auto' }}>
        <table style={{ borderCollapse: 'collapse', width: '100%', minWidth: 560, fontSize: 12 }}>
          <thead style={{ position: 'sticky', top: 0, zIndex: 1 }}>
            <tr>
              <th style={{ textAlign: 'left', padding: '6px 8px', background: '#fff', borderBottom: '1px solid #e2e8f0', color: '#64748b', fontWeight: 600, whiteSpace: 'nowrap' }}>
                {t('serviceQuality.matrix.periodCol')}
              </th>
              {models.map((m) => (
                <th
                  key={m.dst_model_name}
                  title={m.dst_model_name}
                  style={{
                    padding: '6px 4px', background: '#fff', borderBottom: '1px solid #e2e8f0', color: '#334155',
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
            {rows.map((r) => (
              <tr key={r.idx}>
                <td style={{ padding: '5px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap', color: '#475569' }}>
                  {r.label || '-'}
                  {r.partial ? (
                    <span style={{ fontSize: 10, padding: '0 4px', borderRadius: 4, border: '1px solid #cbd5e1', color: '#64748b', background: '#f8fafc', marginLeft: 4 }}>
                      {t('serviceQuality.period.partial')}
                    </span>
                  ) : null}
                </td>
                {r.cells.map((p, ci) => {
                  const v = valueOf(p)
                  const st = cellStyle(v)
                  const lowSample = metric.key === 'congestion' && p && p.count
                    && (Number(p.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES
                  const tip = p && p.count
                    ? t('serviceQuality.matrix.cellTip', {
                        model: models[ci].dst_model_name,
                        period: r.label,
                        metric: t(metric.i18n),
                        value: fmtCell(v),
                        calls: fmtNum(p.count),
                        err: fmtPct(p.error_rate),
                        avg: fmtMetric(p.ok_avg_elapsed_ms, { kind: 'ms' }),
                        samples: p.coefficient_samples || 0,
                      })
                    : t('serviceQuality.period.empty')
                  return (
                    <td
                      key={models[ci].dst_model_name}
                      title={lowSample ? t('serviceQuality.period.lowSample', { count: (p && p.coefficient_samples) || 0 }) : tip}
                      style={{
                        padding: '5px 4px', borderBottom: '1px solid #f1f5f9', textAlign: 'center',
                        background: st.background, color: st.color, fontVariantNumeric: 'tabular-nums',
                      }}
                    >
                      {fmtCell(v)}
                    </td>
                  )
                })}
              </tr>
            ))}
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
          {t('serviceQuality.matrix.emptyCell')}
        </span>
      </div>
    </div>
  )
}
