// QoSHeatmap：拥堵热力图 —— 全模型小倍数网格（v2.0.85）。
// 每个模型一块 7 天 × 24 小时迷你热力图，共用同一四档色标，所有源站同屏对比，
// 质量区别一眼可见（需求：「各个模型一起显示出来，才能看到区别」）。
// 行 = 日期（上 = 6 天前，下 = 今天），列 = 0~23 时；进行中格描边；
// 小倍数场景悬浮用原生 title（避免每格一个 hover 状态）；点击选中联动时段明细/预测。
// 颜色仅承载分档语义，永远伴随共享图例文字与 tooltip（非仅靠颜色传达）。
import { CONGESTION_TIERS } from './constants'

export default function QoSHeatmap({ models, selected, onSelect, t }) {
  const currentHour = new Date().getHours() // 进行中小时（今天最后一行）
  const today = new Date()

  if (!models.length) {
    return <div style={{ padding: 24, color: '#94a3b8', fontSize: 13 }}>{t('serviceQuality.heatmap.empty')}</div>
  }

  const cellColor = (v) => {
    if (v === null || v === undefined || v < 0) return '#f1f5f9'
    const tier = CONGESTION_TIERS.find((tr) => v < tr.max)
    return (tier || CONGESTION_TIERS[CONGESTION_TIERS.length - 1]).color
  }

  // 行序与后端一致：0 = 6 天前 … 6 = 今天
  const dayLabel = (i) => {
    const d = new Date(today)
    d.setDate(d.getDate() - (6 - i))
    const p = (n) => String(n).padStart(2, '0')
    return `${p(d.getMonth() + 1)}-${p(d.getDate())}`
  }

  const cellText = (m, dayIdx, hour) => {
    const v = m.heatmap[dayIdx][hour]
    const count = m.heatmap_counts && m.heatmap_counts[dayIdx] ? m.heatmap_counts[dayIdx][hour] : 0
    const partial = dayIdx === 6 && hour === currentHour
    return t('serviceQuality.heatmap.cell', {
      date: dayLabel(dayIdx),
      hour: String(hour).padStart(2, '0'),
      v: v >= 0 ? v.toFixed(2) : t('serviceQuality.heatmap.noData'),
      count,
    }) + (partial ? ` · ${t('serviceQuality.heatmap.partial')}` : '')
  }

  return (
    <div>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
        {models.map((m) => {
          const isSel = selected === m.dst_model_name
          if (!Array.isArray(m.heatmap)) return null
          return (
            <div
              key={m.dst_model_name}
              role="button"
              tabIndex={0}
              onClick={() => onSelect && onSelect(m.dst_model_name)}
              onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { if (onSelect) onSelect(m.dst_model_name) } }}
              style={{
                border: '1px solid ' + (isSel ? '#2563eb' : '#e2e8f0'),
                borderRadius: 8, padding: '8px 10px', cursor: 'pointer', background: '#fff', outline: 'none',
              }}
              title={m.dst_model_name}
            >
              <div style={{
                fontSize: 12, fontWeight: 600, color: '#334155', marginBottom: 6,
                overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
              }}>
                {m.dst_model_name}
              </div>
              {/* 迷你 7×24 网格：日期列 + 小时格 */}
              <div style={{ display: 'grid', gridTemplateColumns: '30px repeat(24, minmax(0, 1fr))', gap: 1 }}>
                <span />
                {Array.from({ length: 24 }, (_, h) => (
                  <span key={h} style={{ fontSize: 7, color: '#94a3b8', textAlign: 'center', lineHeight: '10px' }}>
                    {h % 6 === 0 ? String(h).padStart(2, '0') : ''}
                  </span>
                ))}
                {m.heatmap.map((row, dayIdx) => (
                  <div key={dayIdx} style={{ display: 'contents' }}>
                    <span style={{ fontSize: 8, color: '#64748b', alignSelf: 'center' }}>{dayLabel(dayIdx)}</span>
                    {row.map((v, hour) => {
                      const isPartial = dayIdx === 6 && hour === currentHour
                      return (
                        <div
                          key={hour}
                          title={cellText(m, dayIdx, hour)}
                          style={{
                            height: 11, borderRadius: 2, background: cellColor(v),
                            outline: isPartial ? '1px solid #64748b' : 'none',
                            opacity: v >= 0 ? 0.92 : 1,
                          }}
                        />
                      )
                    })}
                  </div>
                ))}
              </div>
            </div>
          )
        })}
      </div>
      {/* 共享图例：四档 + 无数据（文字标签，颜色不单独承载语义） */}
      <div style={{ display: 'flex', flexWrap: 'wrap', gap: 10, marginTop: 10, alignItems: 'center', fontSize: 11, color: '#475569' }}>
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
        <span style={{ marginLeft: 'auto', color: '#94a3b8' }}>{t('serviceQuality.heatmap.allModels')}</span>
      </div>
    </div>
  )
}
