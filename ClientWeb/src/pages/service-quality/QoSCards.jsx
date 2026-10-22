// QoSCards：模型状态总览卡。
// 每模型一卡：状态点（四档色 + 文字标签）+ 实时拥堵系数 + 当前平均时延 + 错误率；
// 告警（拥堵系数 > 2.0 或错误率 > 5%）卡片红描边 + 角标；点击选中联动热力图。
import { STATUS_COLORS, fmtMs, fmtNum } from './constants'
import { useI18n } from '../../i18n'

export default function QoSCards({ models, selected, onSelect }) {
  const { t } = useI18n()

  if (!models.length) return null
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(230px, 1fr))', gap: 12 }}>
      {models.map((m) => {
        const color = STATUS_COLORS[m.status] || STATUS_COLORS.idle
        const isSel = selected === m.dst_model_name
        return (
          <div
            key={m.dst_model_name}
            role="button"
            tabIndex={0}
            onClick={() => onSelect && onSelect(m.dst_model_name)}
            onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { if (onSelect) onSelect(m.dst_model_name) } }}
            style={{
              border: '1px solid ' + (isSel ? '#2563eb' : '#e2e8f0'),
              boxShadow: m.alert ? '0 0 0 2px ' + STATUS_COLORS.severe + '33' : 'none',
              borderRadius: 10, padding: '12px 14px', cursor: 'pointer',
              background: '#fff', position: 'relative', outline: 'none',
            }}
            title={t('serviceQuality.heatmap.viewTrend')}
          >
            {m.alert ? (
              <span style={{
                position: 'absolute', top: 8, right: 8, fontSize: 10, fontWeight: 700,
                color: STATUS_COLORS.severe, border: '1px solid ' + STATUS_COLORS.severe,
                borderRadius: 4, padding: '0 4px',
              }}>{t('serviceQuality.cards.alertBadge')}</span>
            ) : null}
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginBottom: 8 }}>
              <span style={{ width: 10, height: 10, borderRadius: 999, background: color, flexShrink: 0 }} />
              <b style={{ fontSize: 13, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }} title={m.dst_model_name}>
                {m.dst_model_name}
              </b>
            </div>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginBottom: 4 }}>
              <span style={{ fontSize: 24, fontWeight: 800, color }}>{m.live.coefficient ? m.live.coefficient.toFixed(2) : '-'}</span>
              <span style={{ fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.cards.coefficient')}</span>
              <span style={{ marginLeft: 'auto', fontSize: 11, color: '#64748b' }}>
                {t(`serviceQuality.status.${m.status}`)}
              </span>
            </div>
            <div style={{ fontSize: 12, color: '#475569', lineHeight: 1.7 }}>
              {m.live.req_count > 0 ? (
                <>
                  <div>{t('serviceQuality.cards.latency')} {fmtMs(m.live.avg_elapsed_ms)}</div>
                  <div>{t('serviceQuality.cards.errorRate')} {Number(m.live.error_rate) ? m.live.error_rate.toFixed(1) + '%' : '0%'}</div>
                </>
              ) : (
                <div style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.noTraffic')}</div>
              )}
              <div style={{ color: '#94a3b8', fontSize: 11 }}>
                {m.baseline_ms_per_token > 0
                  ? t('serviceQuality.cards.baseline', { v: m.baseline_ms_per_token })
                  : ''} · {t('serviceQuality.cards.calls7d', { count: fmtNum(m.calls_7d) })}
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
