// QoSCards：模型状态总览卡。
// v2.0.84：实时窗口不再写死 5 分钟 —— 后端按 5→15→60 分钟阶梯回退，
// 卡片显示实际窗口与「最后请求」相对时间；无流量模型也能看出最近活跃时间。
// 时延取成功请求口径（ok_avg），错误率与样本数并列；系数样本 < 5 给低样本提示。
// 告警（拥堵系数 > 2.0 或错误率 > 5%）卡片红描边 + 角标；点击选中联动热力图/时段明细。
// v2.0.85：新增「下一时段预测」行（源站运行经验，四档着色 + 可信度）与
// 「近24h 次数/错误率」统计行（stats_24h 固定窗口统计）。
import { STATUS_COLORS, MIN_COEFFICIENT_SAMPLES, tierOfCongestion, fmtMs, fmtNum, fmtAgo } from './constants'
import { useI18n } from '../../i18n'

export default function QoSCards({ models, selected, onSelect }) {
  const { t } = useI18n()

  if (!models.length) return null
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(240px, 1fr))', gap: 12 }}>
      {models.map((m) => {
        const color = STATUS_COLORS[m.status] || STATUS_COLORS.idle
        const isSel = selected === m.dst_model_name
        const live = m.live || {}
        const lowSample = (Number(live.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES
        const next = m.prediction && m.prediction.next_hour
        const nextTier = next && next.samples > 0 ? tierOfCongestion(next.avg_congestion) : null
        const stats24 = m.stats_24h || {}
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
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginBottom: 2 }}>
              <span style={{ fontSize: 24, fontWeight: 800, color }}>
                {lowSample ? '—' : (live.coefficient ? live.coefficient.toFixed(2) : '-')}
              </span>
              <span style={{ fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.cards.coefficient')}</span>
              <span style={{ marginLeft: 'auto', fontSize: 11, color: '#64748b' }}>
                {t(`serviceQuality.status.${m.status}`)}
              </span>
            </div>
            <div style={{ fontSize: 11, color: '#94a3b8', marginBottom: 6 }}>
              {live.window_minutes
                ? t('serviceQuality.cards.liveWindow', { minutes: live.window_minutes })
                : t('serviceQuality.cards.noTrafficWindow')}
              {lowSample && live.req_count > 0
                ? ` · ${t('serviceQuality.cards.lowSample', { count: live.coefficient_samples || 0 })}`
                : ''}
            </div>
            <div style={{ fontSize: 12, color: '#475569', lineHeight: 1.7 }}>
              {live.req_count > 0 ? (
                <>
                  <div>{t('serviceQuality.cards.latency')} {fmtMs(live.ok_avg_elapsed_ms || live.avg_elapsed_ms)}</div>
                  <div>
                    {t('serviceQuality.cards.errorRate')} {Number(live.error_rate) ? live.error_rate.toFixed(1) + '%' : '0%'}
                    <span style={{ color: '#94a3b8' }}> · {t('serviceQuality.cards.stallRate')} {Number(live.stall_rate) ? live.stall_rate.toFixed(1) + '%' : '0%'}</span>
                  </div>
                </>
              ) : (
                <div style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.noTraffic')}</div>
              )}
              {/* 下一时段预测（v2.0.85）：无同时段历史 → 灰字提示，不伪造结论 */}
              <div style={{ fontSize: 11, marginTop: 2 }}>
                {nextTier ? (
                  <span style={{ color: STATUS_COLORS[nextTier], fontWeight: 600 }}>
                    {t('serviceQuality.cards.nextHour')}: {t(`serviceQuality.status.${nextTier}`)}
                  </span>
                ) : (
                  <span style={{ color: '#cbd5e1' }}>{t('serviceQuality.cards.noHistory')}</span>
                )}
                {next && next.samples > 0 ? (
                  <span style={{ color: '#94a3b8' }}> · {t(`serviceQuality.forecast.conf.${next.confidence}`)}</span>
                ) : null}
              </div>
              <div style={{ color: '#94a3b8', fontSize: 11 }}>
                {t('serviceQuality.cards.lastRequest', { ago: fmtAgo(m.last_request_at_ms, t) })}
              </div>
              <div style={{ color: '#94a3b8', fontSize: 11 }}>
                {stats24.calls > 0
                  ? t('serviceQuality.cards.stats24h', {
                      count: fmtNum(stats24.calls),
                      rate: Number(stats24.error_rate) ? stats24.error_rate.toFixed(1) + '%' : '0%',
                    })
                  : (m.baseline_ms_per_token > 0
                      ? t('serviceQuality.cards.baseline', { v: m.baseline_ms_per_token })
                      : '')}
                {' · '}{t('serviceQuality.cards.calls7d', { count: fmtNum(m.calls_7d) })}
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
