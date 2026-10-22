// QoSWindowStats：模型状态总览（统计图表，v2.0.87 阶段DB）。
//
// 回答三个问题：
//   ① 当前统计窗口（近 1 天 / 1 周 / 1 月）全站服务质量如何？
//   ② 各源站排队拥堵、质量谁好谁坏？横向差异多大？
//   ③ 每个源站的峰值/谷值时段在哪？
//
// 结构：
//   全站统计条（调用 / 成功率 / 拥堵 / 错误率 / 成功均值 / P95 / 峰 / 谷）
//   → 源站横向比较表（排名 + 拥堵条 + 状态 + 较全站中位偏差 + 错误率 + 时延 +
//      P95 + 调用量 + 峰/谷）
//   → 最好 / 最差结论行
//
// 口径说明：
//   - 拥堵系数是「相对该源站自身 7 天基准」的归一化量，跨源站可比；
//   - 时延 / P95 是各源站自身绝对值，跨源站只作参考（不同模型输出长度不同）；
//   - 系数样本 < 5 的源站显示 —，并排在正常样本之后（不伪造结论）。
// 颜色只承载分档语义，数值与文字标签始终同时出现。
import {
  STATUS_COLORS, MIN_COEFFICIENT_SAMPLES, tierOfCongestion,
  fmtMs, fmtNum, fmtNum1, fmtRatio, fmtPct, fmtHour, modelColor,
  windowStats, windowPeakValley,
} from './constants'

function medianOf(values) {
  const arr = values.filter((v) => isFinite(v) && v > 0).slice().sort((a, b) => a - b)
  if (!arr.length) return 0
  const mid = Math.floor(arr.length / 2)
  return arr.length % 2 ? arr[mid] : (arr[mid - 1] + arr[mid]) / 2
}

export default function QoSWindowStats({ report, models, allNames, win, t }) {
  const rv = report && report.windows ? report.windows[win] : null
  const site = (rv && rv.stats) || {}
  const sitePv = (rv && rv.peak_valley) || {}

  const rows = models.map((m) => {
    const s = windowStats(m, win) || {}
    const calls = Number(s.calls) || 0
    const samples = Number(s.coefficient_samples) || 0
    const cong = Number(s.avg_congestion) || 0
    const lowSample = samples < MIN_COEFFICIENT_SAMPLES
    return {
      name: m.dst_model_name,
      s,
      pv: windowPeakValley(m, win),
      calls,
      samples,
      cong,
      lowSample,
      valid: calls > 0 && !lowSample && cong > 0,
      status: s.status || 'idle',
    }
  })

  const sorted = rows.slice().sort((a, b) => {
    if (a.valid !== b.valid) return a.valid ? -1 : 1
    if (a.valid && b.valid && a.cong !== b.cong) return b.cong - a.cong
    if (a.calls !== b.calls) return b.calls - a.calls
    return a.name < b.name ? -1 : 1
  })

  const valid = sorted.filter((r) => r.valid)
  const median = medianOf(valid.map((r) => r.cong))
  const maxCong = Math.max(1e-9, ...valid.map((r) => r.cong))
  const best = valid.length ? valid[valid.length - 1] : null
  const worst = valid.length ? valid[0] : null

  const deltaOf = (r) => {
    if (!r.valid || median <= 0) return null
    return (r.cong / median - 1) * 100
  }

  const tiles = [
    { key: 'calls', label: t('serviceQuality.overview.tileCalls'), value: fmtNum(site.calls), hint: t('serviceQuality.overview.tileCallsHint') },
    {
      key: 'success',
      label: t('serviceQuality.overview.tileSuccess'),
      value: Number(site.calls) > 0 ? fmtPct(site.success_rate) : '-',
      hint: t('serviceQuality.overview.tileSuccessHint'),
    },
    {
      key: 'congestion',
      label: t('serviceQuality.metric.congestion'),
      value: Number(site.calls) > 0 ? fmtRatio(site.avg_congestion) : '-',
      hint: t('serviceQuality.overview.tileCongestionHint'),
      color: Number(site.avg_congestion) >= 2 ? STATUS_COLORS.moderate : undefined,
    },
    {
      key: 'error',
      label: t('serviceQuality.metric.error'),
      value: Number(site.calls) > 0 ? fmtPct(site.error_rate) : '-',
      hint: t('serviceQuality.overview.tileErrorHint'),
      color: Number(site.error_rate) > 5 ? STATUS_COLORS.severe : undefined,
    },
    { key: 'avg', label: t('serviceQuality.metric.avg'), value: fmtMs(site.ok_avg_elapsed_ms), hint: t('serviceQuality.overview.tileAvgHint') },
    { key: 'p95', label: t('serviceQuality.metric.p95'), value: fmtMs(site.ok_p95_ms), hint: t('serviceQuality.overview.tileP95Hint') },
    {
      key: 'peak',
      label: t('serviceQuality.profile.peak'),
      value: fmtHour(sitePv.peak_hour),
      hint: Number(sitePv.peak_calls_per_day) > 0
        ? t('serviceQuality.overview.tilePeakHint', { v: fmtNum1(sitePv.peak_calls_per_day) })
        : t('serviceQuality.overview.tilePeakEmpty'),
    },
    {
      key: 'valley',
      label: t('serviceQuality.profile.valley'),
      value: fmtHour(sitePv.valley_hour),
      hint: t('serviceQuality.overview.tileValleyHint'),
    },
  ]

  if (!models.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.overview.noData')}</div>
  }

  return (
    <div>
      {/* 全站统计条 */}
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(132px, 1fr))', gap: 8, marginBottom: 14 }}>
        {tiles.map((tile) => (
          <div key={tile.key} style={{ border: '1px solid #e2e8f0', borderRadius: 8, padding: '8px 10px', background: '#fff' }}>
            <div style={{ fontSize: 11, color: '#64748b' }}>{tile.label}</div>
            <div style={{ fontSize: 18, fontWeight: 800, color: tile.color || '#0f172a', fontVariantNumeric: 'tabular-nums' }}>
              {tile.value}
            </div>
            <div style={{ fontSize: 10, color: '#94a3b8', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={tile.hint}>
              {tile.hint}
            </div>
          </div>
        ))}
      </div>

      {/* 源站横向比较 */}
      <div style={{ fontSize: 12, fontWeight: 600, color: '#334155', marginBottom: 6 }}>
        {t('serviceQuality.overview.compareTitle')}
      </div>
      <div style={{ overflowX: 'auto' }}>
        <table style={{ borderCollapse: 'collapse', width: '100%', minWidth: 920, fontSize: 12 }}>
          <thead>
            <tr>
              {[
                'serviceQuality.overview.rank',
                'serviceQuality.table.model',
                'serviceQuality.metric.congestion',
                'serviceQuality.overview.vsMedian',
                'serviceQuality.metric.error',
                'serviceQuality.metric.avg',
                'serviceQuality.metric.p95',
                'serviceQuality.overview.calls',
                'serviceQuality.overview.peakValley',
                'serviceQuality.table.status',
              ].map((k) => (
                <th key={k} style={{ textAlign: 'left', padding: '6px 8px', borderBottom: '1px solid #e2e8f0', color: '#64748b', fontWeight: 600, whiteSpace: 'nowrap' }}>
                  {t(k)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {sorted.map((r, idx) => {
              const tier = r.valid ? tierOfCongestion(r.cong) : null
              const color = tier ? STATUS_COLORS[tier] : '#cbd5e1'
              const widthPct = r.valid ? (r.cong / maxCong) * 100 : 0
              const delta = deltaOf(r)
              const deltaColor = delta === null ? '#94a3b8' : delta > 5 ? STATUS_COLORS.severe : delta < -5 ? STATUS_COLORS.ok : '#64748b'
              return (
                <tr key={r.name}>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', color: '#94a3b8', fontVariantNumeric: 'tabular-nums' }}>
                    {r.valid ? idx + 1 : '—'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap' }}>
                    <span style={{ display: 'inline-block', width: 8, height: 8, borderRadius: 999, marginRight: 5, background: modelColor(allNames, r.name) }} />
                    <b style={{ color: '#0f172a' }}>{r.name}</b>
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', minWidth: 180 }}>
                    <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                      <span style={{ flex: 1, minWidth: 50, background: '#f1f5f9', borderRadius: 4, height: 12, position: 'relative', overflow: 'hidden' }}>
                        <span style={{
                          position: 'absolute', left: 0, top: 0, bottom: 0,
                          width: Math.max(r.valid ? 1.5 : 0, widthPct) + '%',
                          background: color, borderRadius: 4, opacity: 0.92,
                        }} />
                      </span>
                      <span style={{ width: 46, flexShrink: 0, textAlign: 'right', fontVariantNumeric: 'tabular-nums', color: r.valid ? '#0f172a' : '#94a3b8', fontWeight: r.valid ? 700 : 400 }}>
                        {r.valid ? fmtRatio(r.cong) : '—'}
                      </span>
                      <span style={{ width: 72, flexShrink: 0, fontSize: 11, color: tier ? color : '#94a3b8' }}>
                        {r.calls > 0 ? t(`serviceQuality.status.${r.status}`) : t('serviceQuality.status.idle')}
                      </span>
                    </div>
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums', color: deltaColor, whiteSpace: 'nowrap' }}>
                    {delta === null ? '—' : `${delta > 0 ? '+' : ''}${delta.toFixed(1)}%`}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums', color: Number(r.s.error_rate) > 5 ? STATUS_COLORS.severe : '#475569' }}>
                    {r.calls > 0 ? fmtPct(r.s.error_rate) : '—'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums', color: '#475569' }}>
                    {r.calls > 0 ? fmtMs(r.s.ok_avg_elapsed_ms) : '—'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums', color: '#475569' }}>
                    {r.calls > 0 ? fmtMs(r.s.ok_p95_ms) : '—'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', fontVariantNumeric: 'tabular-nums', color: '#475569' }}>
                    {fmtNum(r.calls)}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap', color: '#475569' }}>
                    {r.calls > 0 && Number(r.pv.peak_hour) >= 0
                      ? `${fmtHour(r.pv.peak_hour)} / ${fmtHour(r.pv.valley_hour)}`
                      : '—'}
                  </td>
                  <td style={{ padding: '6px 8px', borderBottom: '1px solid #f1f5f9', whiteSpace: 'nowrap' }}>
                    <span style={{ display: 'inline-flex', alignItems: 'center', gap: 5, color: tier ? color : '#94a3b8' }}>
                      <span style={{ width: 8, height: 8, borderRadius: 999, background: tier ? color : '#cbd5e1' }} />
                      {r.calls > 0 ? t(`serviceQuality.status.${r.status}`) : t('serviceQuality.status.idle')}
                    </span>
                  </td>
                </tr>
              )
            })}
          </tbody>
        </table>
      </div>

      {/* 最好 / 最差结论 + 口径说明 */}
      <div style={{ marginTop: 8, fontSize: 11, color: '#64748b', lineHeight: 1.8 }}>
        {best && worst ? (
          <div>
            {t('serviceQuality.overview.bestWorst', {
              best: best.name,
              bestV: fmtRatio(best.cong),
              bestDelta: `${deltaOf(best) > 0 ? '+' : ''}${deltaOf(best).toFixed(1)}%`,
              worst: worst.name,
              worstV: fmtRatio(worst.cong),
              worstDelta: `${deltaOf(worst) > 0 ? '+' : ''}${deltaOf(worst).toFixed(1)}%`,
            })}
          </div>
        ) : (
          <div>{t('serviceQuality.overview.notEnough')}</div>
        )}
        <div style={{ color: '#94a3b8' }}>
          {t('serviceQuality.overview.medianHint', { v: fmtRatio(median) })}
          {valid.length < rows.length ? ` · ${t('serviceQuality.overview.lowSampleNote', { count: rows.length - valid.length })}` : ''}
        </div>
      </div>
    </div>
  )
}
