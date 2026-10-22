// QoSCompareChart：模型质量对比（全模型横向条形图，v2.0.85）。
// 统计窗口（近24h/近7d）× 对比指标（6 项）双切换，数据取后端 stats_24h / stats_7d
// 固定窗口统计（与小时桶同一套成功口径）。
// 按值降序排列，阈值类指标按语义着色（四档状态色），数值标签始终伴随；
// 系数类指标样本 < 5 的模型灰条 + 「—」+ tooltip 低样本说明，不伪造结论；
// 窗口内零调用的模型不出现在图中（图下脚注说明数量）。
import { useMemo, useState } from 'react'
import {
  MIN_COEFFICIENT_SAMPLES,
  COMPARE_METRICS, compareMetricOf, compareMetricColor, fmtCompareValue, fmtNum, modelColor,
} from './constants'

export default function QoSCompareChart({ models, allNames, t }) {
  const [win, setWin] = useState('24h')
  const [metricKey, setMetricKey] = useState('avg_congestion')
  const metric = compareMetricOf(metricKey)

  const rows = useMemo(() => {
    const out = []
    for (const m of models) {
      const s = win === '24h' ? m.stats_24h : m.stats_7d
      if (!s || !s.calls) continue
      const lowSample = !!metric.coefficient && (Number(s.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES
      out.push({
        name: m.dst_model_name,
        value: Number(s[metric.key]) || 0,
        calls: s.calls,
        samples: s.coefficient_samples || 0,
        lowSample,
      })
    }
    // 有效值降序在前，低样本排尾部（不混入正常排序误导）
    out.sort((a, b) => (a.lowSample === b.lowSample ? b.value - a.value : (a.lowSample ? 1 : -1)))
    return out
  }, [models, win, metric])

  const excluded = models.length - rows.length
  const maxV = Math.max(1e-9, ...rows.filter((r) => !r.lowSample).map((r) => r.value))

  if (!rows.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.compare.empty')}</div>
  }

  return (
    <div>
      {/* 筛选行：统计窗口 + 对比指标（统辖本图） */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, flexWrap: 'wrap', marginBottom: 10 }}>
        <span style={{ display: 'inline-flex' }}>
          {[{ k: '24h', l: t('serviceQuality.window24h') }, { k: '7d', l: t('serviceQuality.window7d') }].map((w, i, arr) => (
            <button
              key={w.k}
              className={'btn ' + (win === w.k ? 'btn-primary' : '')}
              style={{ borderRadius: i === 0 ? '6px 0 0 6px' : '0 6px 6px 0', padding: '2px 12px', fontSize: 12 }}
              onClick={() => setWin(w.k)}
            >{w.l}</button>
          ))}
        </span>
        <select
          style={{ maxWidth: 200, fontSize: 12 }}
          value={metricKey}
          onChange={(e) => setMetricKey(e.target.value)}
          aria-label={t('serviceQuality.compare.metric')}
        >
          {COMPARE_METRICS.map((m) => (
            <option key={m.key} value={m.key}>{t(m.i18n)}</option>
          ))}
        </select>
      </div>

      {/* 横向条形：模型名 + 条 + 数值（HTML 条形容器，天然响应式） */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: 5 }}>
        {rows.map((r) => {
          const color = r.lowSample ? '#cbd5e1' : compareMetricColor(metric, r.value)
          const widthPct = (r.value / maxV) * 100
          const tip = r.lowSample
            ? t('serviceQuality.compare.lowSample', { count: r.samples })
            : `${t(metric.i18n)} ${fmtCompareValue(metric, r.value)} · ${t('serviceQuality.compare.calls', { count: fmtNum(r.calls) })}`
          return (
            <div key={r.name} style={{ display: 'flex', alignItems: 'center', gap: 8 }} title={tip}>
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
                  width: Math.max(1.5, widthPct) + '%',
                  background: color, borderRadius: 4,
                  opacity: r.lowSample ? 0.7 : 0.92,
                }} />
              </span>
              <span style={{ width: 72, flexShrink: 0, fontSize: 12, fontVariantNumeric: 'tabular-nums', color: r.lowSample ? '#94a3b8' : '#0f172a' }}>
                {r.lowSample ? '—' : fmtCompareValue(metric, r.value)}
              </span>
            </div>
          )
        })}
      </div>

      <div style={{ marginTop: 8, fontSize: 11, color: '#94a3b8' }}>
        {excluded > 0 ? t('serviceQuality.compare.excluded', { count: excluded }) : ''}
      </div>
    </div>
  )
}
