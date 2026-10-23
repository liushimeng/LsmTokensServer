// QoSPeriodMatrix：「时段 × 模型」四维明细矩阵（v2.0.88 阶段DC 重构）。
//
// v2.0.86 阶段DA 原版：单指标热力矩阵（指标切换按钮 + 时段 × 模型 单值格子 +
// maxHeight 460 的竖向滚动容器）。本阶段按需求重做：
//
//   ① **删除指标切换按钮**（拥堵系数 / 请求数 / 错误率 / 平均时延）——
//      运维要横向比较「同一时段某模型的四个维度」时不再需要反复点按钮；
//   ② **四维同表同屏**：双层表头（模型分组 → 4 个子列），
//      每个模型下同时给出 请求数 / 拥堵系数 / 错误率 / 成功平均时延；
//   ③ **去掉竖向滚动**：外层只保留横向滚动（模型多时必需），
//      表格随页面纵向撑满（需求：不要竖向滚轮、布局撑满页面）；
//   ④ **首列（时段）粘性**：横向滚动时时段不丢；
//   ⑤ **合计行**：请求数合计 / 拥堵系数（按系数样本加权）/ 错误率（按请求数加权）/
//      平均时延（按成功数加权）。
//
// 口径说明（与后端一致，零口径漂移）：
//   - 时延取成功请求口径（ok_avg_elapsed_ms）；错误单独看 error_rate；
//   - 拥堵系数样本 < MIN_COEFFICIENT_SAMPLES 的单元格显示「—」（低样本不伪造结论）；
//   - 颜色只承载分档语义（仅拥堵系数铺四档色），数值与文字标签始终同时出现；
//   - 进行中时段（partial）在首列标注。
import { useMemo } from 'react'
import {
  PERIOD_MATRIX_METRICS, windowSeries, bucketShortLabel,
  fmtMs, fmtNum, fmtPct, fmtRatio, MIN_COEFFICIENT_SAMPLES,
  STATUS_COLORS, CONGESTION_TIERS, tierOfCongestion, modelColor,
} from './constants'

// 首列（时段）粘性样式：横向滚动时固定在左侧。
const FIRST_COL = {
  position: 'sticky', left: 0, zIndex: 2, background: '#fff',
  borderRight: '1px solid #e2e8f0', whiteSpace: 'nowrap',
}

export default function QoSPeriodMatrix({ models, allNames, win, t }) {
  // 按时段（行）收集各模型的桶数据：rows[i].cells = [model0Bucket, model1Bucket, ...]
  const rows = useMemo(() => {
    if (!models.length) return []
    const series = models.map((m) => windowSeries(m, win))
    const n = series.reduce((max, s) => Math.max(max, s.length), 0)
    const out = []
    for (let i = 0; i < n; i++) {
      const cells = series.map((s) => (i < s.length ? s[i] : null))
      const first = cells.find((c) => c)
      const label = first ? first.bucket : ''
      out.push({
        idx: i,
        label,
        short: bucketShortLabel(label),
        cells,
        partial: cells.some((c) => c && c.partial),
      })
    }
    return out
  }, [models, win])

  // 单元格取值：null = 无数据 / 低样本（显示 —）
  const cellValue = (p, dim) => {
    if (!p || !p.count) return null
    if (dim.key === 'congestion') {
      if ((Number(p.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES) return null
      const v = Number(p.avg_congestion)
      return isFinite(v) && v > 0 ? v : null
    }
    if (dim.key === 'error') {
      const v = Number(p.error_rate)
      return isFinite(v) ? v : null
    }
    if (dim.key === 'calls') {
      const v = Number(p.count)
      return isFinite(v) ? v : null
    }
    const v = Number(p.ok_avg_elapsed_ms)
    return isFinite(v) && v > 0 ? v : null
  }

  const fmtCell = (v, dim) => {
    if (v === null) return '—'
    if (dim.kind === 'ratio') return fmtRatio(v)
    if (dim.kind === 'pct') return fmtPct(v)
    if (dim.kind === 'ms') return fmtMs(v)
    return fmtNum(v)
  }

  // 合计行：按模型分别汇总（该模型在所有时段上的加权汇总）。
  //   拥堵系数按 coefficient_samples 加权（与后端全站口径一致）；
  //   错误率按请求数加权；平均时延按成功数加权。
  const totalsByModel = useMemo(() => {
    return models.map((m, mi) => {
      let calls = 0
      let errWeighted = 0 // Σ error_rate% × count
      let okWeighted = 0 // Σ ok_avg × success_count
      let okCount = 0
      let coeffWeighted = 0 // Σ avg_congestion × coefficient_samples
      let coeffSamples = 0
      for (const r of rows) {
        const p = r.cells[mi]
        if (!p || !p.count) continue
        const c = Number(p.count) || 0
        calls += c
        errWeighted += (Number(p.error_rate) || 0) * c
        const okc = Number(p.success_count) || 0
        if (okc > 0) {
          okWeighted += (Number(p.ok_avg_elapsed_ms) || 0) * okc
          okCount += okc
        }
        const samples = Number(p.coefficient_samples) || 0
        const cong = Number(p.avg_congestion) || 0
        if (samples >= MIN_COEFFICIENT_SAMPLES && cong > 0) {
          coeffWeighted += cong * samples
          coeffSamples += samples
        }
      }
      return {
        calls,
        congestion: coeffSamples > 0 ? coeffWeighted / coeffSamples : null,
        error: calls > 0 ? errWeighted / calls : null,
        avg: okCount > 0 ? okWeighted / okCount : null,
      }
    })
  }, [models, rows])

  if (!models.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.matrix.empty')}</div>
  }

  const totalOf = (dim, tot) => {
    if (dim.key === 'calls') return fmtNum(tot.calls)
    if (dim.key === 'congestion') return tot.congestion === null ? '—' : fmtRatio(tot.congestion)
    if (dim.key === 'error') return tot.error === null ? '—' : fmtPct(tot.error)
    return tot.avg === null ? '—' : fmtMs(tot.avg)
  }

  return (
    <div>
      {/* 说明行（不再有指标切换按钮：四个维度同表同屏显示） */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', marginBottom: 8, fontSize: 11, color: '#64748b' }}>
        <span>{t('serviceQuality.period.matrixHint')}</span>
      </div>

      {/* 外层只保留横向滚动：不设 max-height → 无竖向滚动条，表格随页面撑满 */}
      <div style={{ overflowX: 'auto' }}>
        <table style={{ borderCollapse: 'collapse', width: '100%', minWidth: 640, fontSize: 12 }}>
          <thead>
            {/* 表头第一行：模型分组（每组 4 个子列） */}
            <tr>
              <th rowSpan={2} style={{ ...FIRST_COL, zIndex: 3, textAlign: 'left', padding: '6px 8px', borderBottom: '1px solid #e2e8f0', color: '#64748b', fontWeight: 600 }}>
                {t('serviceQuality.matrix.periodCol')}
              </th>
              {models.map((m) => (
                <th
                  key={m.dst_model_name}
                  colSpan={PERIOD_MATRIX_METRICS.length}
                  title={m.dst_model_name}
                  style={{
                    padding: '6px 6px', borderBottom: '1px solid #e2e8f0', color: '#334155',
                    fontWeight: 600, textAlign: 'center', minWidth: 230, maxWidth: 320,
                    borderLeft: '1px solid #f1f5f9',
                  }}
                >
                  <span style={{ display: 'inline-block', width: 7, height: 7, borderRadius: 999, marginRight: 5, background: modelColor(allNames, m.dst_model_name) }} />
                  <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', display: 'inline-block', maxWidth: 180, verticalAlign: 'bottom' }}>
                    {m.dst_model_name}
                  </span>
                </th>
              ))}
            </tr>
            {/* 表头第二行：四个维度子列 */}
            <tr>
              {models.map((m) => PERIOD_MATRIX_METRICS.map((dim) => (
                <th
                  key={m.dst_model_name + '-' + dim.key}
                  style={{
                    padding: '4px 4px', borderBottom: '1px solid #e2e8f0', color: '#64748b',
                    fontWeight: 600, fontSize: 11, textAlign: 'center', whiteSpace: 'nowrap',
                    borderLeft: '1px solid #f1f5f9',
                  }}
                >
                  {t(dim.i18n)}
                </th>
              )))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r) => (
              <tr key={r.idx}>
                <td style={{ ...FIRST_COL, padding: '5px 8px', borderBottom: '1px solid #f1f5f9', color: '#475569' }}>
                  {r.label || '-'}
                  {r.partial ? (
                    <span style={{ fontSize: 10, padding: '0 4px', borderRadius: 4, border: '1px solid #cbd5e1', color: '#64748b', background: '#f8fafc', marginLeft: 4 }}>
                      {t('serviceQuality.period.partial')}
                    </span>
                  ) : null}
                </td>
                {r.cells.map((p, ci) => {
                  const model = models[ci]
                  const tip = p && p.count
                    ? t('serviceQuality.matrix.cellTip', {
                        model: model.dst_model_name,
                        period: r.label,
                        metric: t('serviceQuality.period.count'),
                        value: fmtNum(p.count),
                        calls: fmtNum(p.count),
                        err: fmtPct(p.error_rate),
                        avg: fmtMs(p.ok_avg_elapsed_ms),
                        samples: p.coefficient_samples || 0,
                      })
                    : t('serviceQuality.period.empty')
                  return PERIOD_MATRIX_METRICS.map((dim) => {
                    const v = cellValue(p, dim)
                    const lowSample = dim.key === 'congestion' && p && p.count
                      && (Number(p.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES
                    let bg = 'transparent'
                    let fg = '#0f172a'
                    if (dim.tier && v !== null) {
                      bg = STATUS_COLORS[tierOfCongestion(v)]
                      fg = v >= 2 ? '#fff' : '#0f172a'
                    } else if (v === null) {
                      bg = '#f8fafc'
                      fg = '#cbd5e1'
                    } else if (dim.alert && v > dim.alert) {
                      fg = STATUS_COLORS.severe
                    }
                    return (
                      <td
                        key={model.dst_model_name + '-' + dim.key}
                        title={lowSample
                          ? t('serviceQuality.period.lowSample', { count: (p && p.coefficient_samples) || 0 })
                          : tip}
                        style={{
                          padding: '5px 4px', borderBottom: '1px solid #f1f5f9', textAlign: 'center',
                          background: bg, color: fg, fontVariantNumeric: 'tabular-nums',
                          borderLeft: '1px solid #f8fafc', whiteSpace: 'nowrap',
                        }}
                      >
                        {fmtCell(v, dim)}
                      </td>
                    )
                  })
                })}
              </tr>
            ))}
            {/* 合计行：按模型分组给出该模型在全部时段上的加权汇总 */}
            <tr>
              <td style={{ ...FIRST_COL, padding: '6px 8px', borderTop: '1px solid #e2e8f0', color: '#0f172a', fontWeight: 700 }}>
                {t('serviceQuality.period.matrixTotal')}
              </td>
              {totalsByModel.map((tot, mi) => PERIOD_MATRIX_METRICS.map((dim) => (
                <td
                  key={'total-' + models[mi].dst_model_name + '-' + dim.key}
                  title={t(dim.i18n)}
                  style={{
                    padding: '6px 4px', borderTop: '1px solid #e2e8f0', textAlign: 'center',
                    fontVariantNumeric: 'tabular-nums', color: '#0f172a', fontWeight: 700, whiteSpace: 'nowrap',
                    borderLeft: '1px solid #f8fafc',
                  }}
                >
                  {totalOf(dim, tot)}
                </td>
              )))}
            </tr>
          </tbody>
        </table>
      </div>

      {/* 共享图例 */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 8, alignItems: 'center', fontSize: 11, color: '#475569' }}>
        {CONGESTION_TIERS.map((tr) => (
          <span key={tr.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 3, background: tr.color }} />
            {t(`serviceQuality.status.${tr.key}`)}
          </span>
        ))}
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          <span style={{ width: 10, height: 10, borderRadius: 3, background: '#f8fafc', border: '1px solid #e2e8f0' }} />
          {t('serviceQuality.matrix.emptyCell')}
        </span>
      </div>
    </div>
  )
}
