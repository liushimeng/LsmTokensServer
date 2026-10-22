// QoSHeatmap：拥堵热力图（近 7 天 × 24 小时）。
// 行 = 日期（上=6 天前，下=今天），列 = 0~23 时；单元格颜色 = 该小时桶平均
// 拥堵系数四档色（绿/黄/橙/红），无数据灰色；悬浮 tooltip 显示系数/请求数。
// 颜色仅承载分档语义，永远伴随图例文字与 tooltip（非仅靠颜色传达）。
import { useState } from 'react'
import { CONGESTION_TIERS, tierOfCongestion } from './constants'

const HOUR = '00 01 02 03 04 05 06 07 08 09 10 11 12 13 14 15 16 17 18 19 20 21 22 23'.split(' ')

export default function QoSHeatmap({ model, t }) {
  const [hover, setHover] = useState(null) // { dayIdx, hour }
  const currentHour = new Date().getHours() // 进行中小时（今天最后一行）

  if (!model || !Array.isArray(model.heatmap)) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.heatmap.empty')}</div>
  }

  const cellColor = (v) => {
    if (v === null || v === undefined || v < 0) return '#f1f5f9'
    const tier = CONGESTION_TIERS.find((tr) => v < tr.max)
    return (tier || CONGESTION_TIERS[CONGESTION_TIERS.length - 1]).color
  }

  const dayLabel = (i) => {
    // 行序与后端一致：0 = 6 天前 … 6 = 今天（用桶数据推导不了日期，从今天的 Date 回推）
    const d = new Date()
    d.setDate(d.getDate() - (6 - i))
    const p = (n) => String(n).padStart(2, '0')
    return `${p(d.getMonth() + 1)}-${p(d.getDate())}`
  }

  const hoverInfo = hover ? {
    date: dayLabel(hover.dayIdx),
    hour: hover.hour,
    v: model.heatmap[hover.dayIdx][hover.hour],
    count: model.heatmap_counts && model.heatmap_counts[hover.dayIdx] ? model.heatmap_counts[hover.dayIdx][hover.hour] : 0,
    // 今天最后一行 + 当前小时 = 进行中的小时桶（样本未满）
    partial: hover.dayIdx === model.heatmap.length - 1 && hover.hour === currentHour,
  } : null

  return (
    <div>
      <div style={{ overflowX: 'auto' }}>
        <div style={{ display: 'grid', width: '100%', gridTemplateColumns: '48px repeat(24, minmax(0, 1fr))', gap: 2, minWidth: 520 }}>
          {/* 顶行：小时刻度（每 3 小时） */}
          <span />
          {HOUR.map((h) => (
            <span key={h} style={{ fontSize: 9, color: '#94a3b8', textAlign: 'center' }}>
              {Number(h) % 3 === 0 ? h : ''}
            </span>
          ))}
          {/* 数据行：日期 + 24 格 */}
          {model.heatmap.map((row, dayIdx) => (
            <div key={dayIdx} style={{ display: 'contents' }}>
              <span style={{ fontSize: 10, color: '#64748b', alignSelf: 'center' }}>{dayLabel(dayIdx)}</span>
              {row.map((v, hour) => {
                const isHover = hover && hover.dayIdx === dayIdx && hover.hour === hour
                const isPartial = dayIdx === model.heatmap.length - 1 && hour === currentHour
                return (
                  <div
                    key={hour}
                    onMouseEnter={() => setHover({ dayIdx, hour })}
                    onMouseLeave={() => setHover(null)}
                    title={isPartial ? t('serviceQuality.heatmap.partial') : undefined}
                    style={{
                      height: 20, borderRadius: 3, background: cellColor(v),
                      outline: isHover ? '2px solid #0f172a' : (isPartial ? '1px solid #64748b' : 'none'),
                      cursor: 'default',
                      opacity: tierOfCongestion(v) === 'idle' ? 1 : 0.92,
                    }}
                  />
                )
              })}
            </div>
          ))}
        </div>
      </div>
      {/* 图例：四档 + 无数据（文字标签，颜色不单独承载语义） */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 8, alignItems: 'center', fontSize: 11, color: '#475569' }}>
        <span>{t('serviceQuality.legend.congestion')}:</span>
        {CONGESTION_TIERS.map((tr) => (
          <span key={tr.key} style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
            <span style={{ width: 10, height: 10, borderRadius: 3, background: tr.color }} />
            {t(`serviceQuality.status.${tr.key}`)}
          </span>
        ))}
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
          <span style={{ width: 10, height: 10, borderRadius: 3, background: '#f1f5f9', border: '1px solid #e2e8f0' }} />
          {t('serviceQuality.heatmap.noData')}
        </span>
        {hoverInfo ? (
          <span style={{ marginLeft: 'auto', color: '#0f172a' }}>
            {t('serviceQuality.heatmap.cell', {
              date: hoverInfo.date, hour: String(hoverInfo.hour).padStart(2, '0'),
              v: hoverInfo.v >= 0 ? hoverInfo.v.toFixed(2) : t('serviceQuality.heatmap.noData'),
              count: hoverInfo.count,
            })}
            {hoverInfo.partial ? ` · ${t('serviceQuality.heatmap.partial')}` : ''}
          </span>
        ) : null}
      </div>
    </div>
  )
}
