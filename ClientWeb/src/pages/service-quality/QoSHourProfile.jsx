// QoSHourProfile：24 小时时段画像与高峰低谷（v2.0.86 阶段DA）。
//
// 回答「哪个小时是高峰/低谷」「源站在某个时段的典型表现」：
//   - 上：全站 24 小时柱状图（指标可切换：日均调用量 / 拥堵系数 / 错误率），
//     峰/谷柱高亮 + 直接标注，背景按 6 个业务时段带做极淡分区（辅助读时段）。
//   - 下：小倍数网格 —— 每模型一张 24 点迷你柱图，同屏对比各源站的峰谷位置。
//
// 口径说明（与后端一致）：
//   - 时间轴 = request_start_at（用户发起调用的时刻），不是落库时间；
//   - 数值 = 近 7 天同时段汇总后的日均调用量（按该小时"有数据的天数"归一）；
//   - 同一时刻只有一个 Y 轴（禁止双轴），指标靠切换器切换。
// 颜色仅承载分档语义（四档状态色），永远伴随图例文字与数值标签。
import { useMemo, useState } from 'react'
import ResponsiveSvgChart from '../../components/ResponsiveSvgChart'
import {
  TIME_BANDS, HOUR_PROFILE_METRICS, hourProfileMetricOf, bandOfHour,
  fmtHour, fmtNum1, fmtMetric, STATUS_COLORS, CONGESTION_TIERS, tierOfCongestion,
} from './constants'

const CHART_H = 240
const MINI_H = 92
const PAD = { left: 52, right: 16, top: 18, bottom: 26 }
const GRID = '#e2e8f0'
const AXIS = '#94a3b8'
const TEXT = '#475569'

// 单色（调用量/时延等量级指标）—— 不用 value-ramp，避免双重编码
const PLAIN = '#2a78d6'

export default function QoSHourProfile({ report, models, t }) {
  const [metricKey, setMetricKey] = useState('calls')
  const metric = hourProfileMetricOf(metricKey)

  const profile = useMemo(() => (report && Array.isArray(report.hour_profile) ? report.hour_profile : []), [report])
  const sitePv = useMemo(() => (report && report.peak_valley) || {}, [report])

  if (!profile.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.profile.empty')}</div>
  }

  const values = profile.map((p) => {
    const v = Number(p[metric.field])
    return isFinite(v) ? v : 0
  })
  const hasData = values.some((v) => v > 0)
  if (!hasData) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.profile.empty')}</div>
  }

  const yMax = Math.max(1e-9, ...values) * 1.18

  // 峰/谷（按当前指标取值；调用量口径与后端 peak_valley 一致）
  let peakIdx = -1
  let valleyIdx = -1
  values.forEach((v, i) => {
    if (v <= 0) return
    if (peakIdx < 0 || v > values[peakIdx]) peakIdx = i
    if (valleyIdx < 0 || v < values[valleyIdx]) valleyIdx = i
  })

  const barColor = (v) => {
    if (!metric.tier) return PLAIN
    return STATUS_COLORS[tierOfCongestion(v)] || STATUS_COLORS.idle
  }

  return (
    <div>
      {/* 指标切换（一个筛选器统辖本模块全部图） */}
      <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', marginBottom: 10 }}>
        {HOUR_PROFILE_METRICS.map((m, i) => (
          <button
            key={m.key}
            className={'btn ' + (metricKey === m.key ? 'btn-primary' : '')}
            style={{
              borderRadius: i === 0 ? '6px 0 0 6px' : i === HOUR_PROFILE_METRICS.length - 1 ? '0 6px 6px 0' : 0,
              padding: '2px 12px', fontSize: 12,
            }}
            onClick={() => setMetricKey(m.key)}
          >{t(m.i18n)}</button>
        ))}
        <span style={{ marginLeft: 'auto', fontSize: 11, color: '#94a3b8' }}>
          {t('serviceQuality.profile.hint')}
        </span>
      </div>

      {/* 全站 24 小时柱状图 */}
      <ResponsiveSvgChart minWidth={240} height={CHART_H}>
        {(W) => {
          const innerW = Math.max(120, W - PAD.left - PAD.right)
          const innerH = CHART_H - PAD.top - PAD.bottom
          const slot = innerW / 24
          const barW = Math.max(3, slot * 0.62)
          const xOf = (h) => PAD.left + slot * h + (slot - barW) / 2
          const yOf = (v) => PAD.top + innerH - (Math.max(0, v) / yMax) * innerH
          return (
            <svg width={W} height={CHART_H}>
              {/* 时段带分区底色（极淡，仅辅助读时段，不承载数据） */}
              {TIME_BANDS.map((b, bi) => (
                <rect
                  key={b.key}
                  x={PAD.left + slot * b.start}
                  y={PAD.top}
                  width={slot * (b.end - b.start)}
                  height={innerH}
                  fill={bi % 2 === 0 ? '#f8fafc' : '#ffffff'}
                />
              ))}
              {/* 网格 + Y 轴刻度 */}
              {[0, 0.25, 0.5, 0.75, 1].map((r) => {
                const y = PAD.top + innerH * r
                return (
                  <g key={r}>
                    <line x1={PAD.left} x2={W - PAD.right} y1={y} y2={y} stroke={GRID} strokeWidth={1} />
                    <text x={PAD.left - 6} y={y + 3} textAnchor="end" fontSize={10} fill={AXIS}>
                      {fmtMetric(yMax * (1 - r), metric)}
                    </text>
                  </g>
                )
              })}
              {/* 柱 */}
              {values.map((v, h) => {
                if (v <= 0) return null
                const isPeak = h === peakIdx
                const isValley = h === valleyIdx
                return (
                  <g key={h}>
                    <rect
                      x={xOf(h)} y={yOf(v)} width={barW} height={Math.max(1, PAD.top + innerH - yOf(v))}
                      rx={3} fill={barColor(v)} opacity={isPeak || isValley ? 1 : 0.78}
                    />
                    {isPeak ? (
                      <text x={xOf(h) + barW / 2} y={yOf(v) - 5} textAnchor="middle" fontSize={10} fontWeight={700} fill={TEXT}>
                        {t('serviceQuality.profile.peak')}
                      </text>
                    ) : null}
                    {isValley ? (
                      <text x={xOf(h) + barW / 2} y={yOf(v) + 12} textAnchor="middle" fontSize={9.5} fill={AXIS}>
                        {t('serviceQuality.profile.valley')}
                      </text>
                    ) : null}
                  </g>
                )
              })}
              {/* X 轴：每 3 小时一个刻度 */}
              {profile.map((p, h) => (h % 3 === 0 ? (
                <text key={h} x={xOf(h) + barW / 2} y={CHART_H - 8} textAnchor="middle" fontSize={10} fill={AXIS}>
                  {String(h).padStart(2, '0')}
                </text>
              ) : null))}
            </svg>
          )
        }}
      </ResponsiveSvgChart>

      {/* 图例 + 峰谷结论（颜色不单独承载语义） */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, alignItems: 'center', fontSize: 11, color: TEXT, marginTop: 4 }}>
        {metric.tier ? CONGESTION_TIERS.map((tr) => (
          <span key={tr.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 3, background: tr.color }} />
            {t(`serviceQuality.status.${tr.key}`)}
          </span>
        )) : (
          <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 3, background: PLAIN }} />
            {t(metric.i18n)}
          </span>
        )}
        <span style={{ color: '#94a3b8' }}>
          {t('serviceQuality.profile.sitePeakValley', {
            peak: fmtHour(sitePv.peak_hour),
            valley: fmtHour(sitePv.valley_hour),
          })}
        </span>
      </div>

      {/* 小倍数网格：每模型一张 24 点迷你柱图 */}
      <div style={{ marginTop: 14 }}>
        <div style={{ fontSize: 12, color: TEXT, fontWeight: 600, marginBottom: 8 }}>
          {t('serviceQuality.profile.perModelTitle')}
        </div>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(260px, 1fr))', gap: 10 }}>
          {models.map((m) => (
            <MiniProfile
              key={m.dst_model_name}
              model={m}
              metric={metric}
              t={t}
            />
          ))}
        </div>
      </div>
    </div>
  )
}

// MiniProfile 单模型 24 点迷你柱图（小倍数网格的一格）。
function MiniProfile({ model, metric, t }) {
  const profile = Array.isArray(model.hour_profile) ? model.hour_profile : []
  const pv = (model && model.peak_valley) || {}
  const vals = profile.map((p) => {
    const v = Number(p[metric.field])
    return isFinite(v) ? v : 0
  })
  const max = Math.max(1e-9, ...vals)
  const pad = { left: 4, right: 4, top: 6, bottom: 12 }
  const innerW = 236
  const innerH = MINI_H - pad.top - pad.bottom
  const slot = innerW / 24

  // 小图内按当前指标自算极值（高亮当前指标的最极端小时）；
  // 文字摘要仍用后端 peak_valley（调用量口径的峰/谷）。
  let peakIdx = -1
  vals.forEach((v, i) => {
    if (v <= 0) return
    if (peakIdx < 0 || v > vals[peakIdx]) peakIdx = i
  })

  const barColor = (v) => {
    if (!metric.tier) return PLAIN
    return STATUS_COLORS[tierOfCongestion(v)] || STATUS_COLORS.idle
  }

  return (
    <div style={{ border: '1px solid #e2e8f0', borderRadius: 8, padding: '8px 10px', background: '#fff' }}>
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 4 }}>
        <b style={{ fontSize: 12, color: '#334155', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={model.dst_model_name}>
          {model.dst_model_name}
        </b>
        <span style={{ marginLeft: 'auto', fontSize: 10, color: '#94a3b8' }}>
          {t('serviceQuality.profile.peakValleyShort', {
            peak: fmtHour(pv.peak_hour),
            valley: fmtHour(pv.valley_hour),
          })}
        </span>
      </div>
      <svg width="100%" height={MINI_H} viewBox={`0 0 ${innerW} ${MINI_H}`} preserveAspectRatio="none">
        {TIME_BANDS.map((b, bi) => (
          <rect
            key={b.key}
            x={b.start * slot} y={pad.top} width={slot * (b.end - b.start)} height={innerH}
            fill={bi % 2 === 0 ? '#f8fafc' : '#ffffff'}
          />
        ))}
        {vals.map((v, h) => {
          if (v <= 0) return null
          const hPx = Math.max(1, (v / max) * innerH)
          const isPeak = h === peakIdx
          return (
            <rect
              key={h}
              x={h * slot + 1}
              y={pad.top + innerH - hPx}
              width={Math.max(1.5, slot - 2)}
              height={hPx}
              rx={1.5}
              fill={barColor(v)}
              opacity={isPeak ? 1 : 0.75}
            />
          )
        })}
        {profile.map((p, h) => (h % 6 === 0 ? (
          <text key={h} x={h * slot + slot / 2} y={MINI_H - 2} textAnchor="middle" fontSize={7} fill={AXIS}>
            {String(h).padStart(2, '0')}
          </text>
        ) : null))}
      </svg>
      <div style={{ fontSize: 10, color: '#94a3b8', marginTop: 2 }}>
        {t('serviceQuality.profile.bandOfPeak', { band: t(`serviceQuality.band.${bandOfHour(pv.peak_hour)}`) })}
        {' · '}
        {t('serviceQuality.profile.peakCalls', { v: fmtNum1(pv.peak_calls_per_day) })}
      </div>
    </div>
  )
}
