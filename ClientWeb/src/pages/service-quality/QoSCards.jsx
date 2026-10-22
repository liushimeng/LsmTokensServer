// QoSCards：模型状态总览（统计卡，v2.0.86 阶段DA）。
//
// v2.0.84/85 以「实时窗口（5/15/60 分钟）」为卡片主角；v2.0.86 页面移除自动刷新后，
// 实时窗口会迅速陈旧且抖动（5 分钟窗口的系数忽高忽低），因此**卡片改为统计口径**：
//
//   ┌ 状态点 模型名                                  [告警角标]
//   │ 24h 拥堵系数 1.38（轻度排队）      ← stats_24h 主导
//   │ 近24h  2,713 次 · 错误率 0.2% · 成功均值 8.0s · P95 24.0s
//   │ 近7天  8,097 次 · 拥堵 1.21 · 成功均值 7.6s · 错误率 0.4%
//   │ 近30天 …（v2.0.87 新增，观察长期趋势）
//   │ 下一时段预测：正常（可信度高）
//   │ 峰 14:00（日均 512 次）· 谷 03:00（日均 4 次）
//   │ 最后请求 2 分钟前
//
// 状态分档改用 stats_24h.status（窗口口径稳定），系数类指标样本 < 5 仍显示「—」
// （沿用 MIN_COEFFICIENT_SAMPLES 低样本保护）。
import { STATUS_COLORS, MIN_COEFFICIENT_SAMPLES, tierOfCongestion, fmtMs, fmtNum, fmtNum1, fmtAgo, fmtHour } from './constants'
import { useI18n } from '../../i18n'

export default function QoSCards({ models, selected, onSelect }) {
  const { t } = useI18n()

  if (!models.length) return null
  return (
    <div style={{ display: 'grid', gridTemplateColumns: 'repeat(auto-fill, minmax(280px, 1fr))', gap: 12 }}>
      {models.map((m) => {
        const s24 = m.stats_24h || {}
        const s7d = m.stats_7d || {}
        const s30 = m.stats_30d || {}
        const status = s24.status || m.status || 'idle'
        const color = STATUS_COLORS[status] || STATUS_COLORS.idle
        const isSel = selected === m.dst_model_name
        const lowSample = (Number(s24.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES && Number(s24.calls) > 0
        const next = m.prediction && m.prediction.next_hour
        const nextTier = next && next.samples > 0 ? tierOfCongestion(next.avg_congestion) : null
        const pv = m.peak_valley || {}
        return (
          <div
            key={m.dst_model_name}
            role="button"
            tabIndex={0}
            onClick={() => onSelect && onSelect(m.dst_model_name)}
            onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { if (onSelect) onSelect(m.dst_model_name) } }}
            style={{
              border: '1px solid ' + (isSel ? '#2563eb' : '#e2e8f0'),
              boxShadow: (s24.alert || m.alert) ? '0 0 0 2px ' + STATUS_COLORS.severe + '33' : 'none',
              borderRadius: 10, padding: '12px 14px', cursor: 'pointer',
              background: '#fff', position: 'relative', outline: 'none',
            }}
            title={t('serviceQuality.heatmap.viewTrend')}
          >
            {(s24.alert || m.alert) ? (
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

            {/* 24h 主导指标（统计口径，非实时窗口） */}
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 6, marginBottom: 2 }}>
              <span style={{ fontSize: 24, fontWeight: 800, color }}>
                {lowSample ? '—' : (Number(s24.avg_congestion) ? s24.avg_congestion.toFixed(2) : '-')}
              </span>
              <span style={{ fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.cards.coefficient24h')}</span>
              <span style={{ marginLeft: 'auto', fontSize: 11, color, fontWeight: 600 }}>
                {t(`serviceQuality.status.${status}`)}
              </span>
            </div>

            <div style={{ fontSize: 12, color: '#475569', lineHeight: 1.75 }}>
              <div>
                <span style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.stats24hLabel')}</span>
                {' '}
                {Number(s24.calls) > 0 ? (
                  <>
                    {t('serviceQuality.unit.times', { count: fmtNum(s24.calls) })}
                    {' · '}{t('serviceQuality.cards.errorRate')} {Number(s24.error_rate) ? s24.error_rate.toFixed(1) + '%' : '0%'}
                    {' · '}{t('serviceQuality.cards.latency')} {fmtMs(s24.ok_avg_elapsed_ms)}
                    {' · '}P95 {fmtMs(s24.ok_p95_ms)}
                  </>
                ) : (
                  <span style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.noTraffic')}</span>
                )}
              </div>
              <div>
                <span style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.stats7dLabel')}</span>
                {' '}
                {Number(s7d.calls) > 0 ? (
                  <>
                    {fmtNum(s7d.calls)}
                    {' · '}{t('serviceQuality.cards.coefficient')} {Number(s7d.avg_congestion) ? s7d.avg_congestion.toFixed(2) : '-'}
                    {' · '}{t('serviceQuality.cards.latency')} {fmtMs(s7d.ok_avg_elapsed_ms)}
                    {' · '}{t('serviceQuality.cards.errorRate')} {Number(s7d.error_rate) ? s7d.error_rate.toFixed(1) + '%' : '0%'}
                  </>
                ) : (
                  <span style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.noTraffic')}</span>
                )}
              </div>
              <div>
                <span style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.stats30dLabel')}</span>
                {' '}
                {Number(s30.calls) > 0 ? (
                  <>
                    {fmtNum(s30.calls)}
                    {' · '}{t('serviceQuality.cards.coefficient')} {Number(s30.avg_congestion) ? s30.avg_congestion.toFixed(2) : '-'}
                    {' · '}{t('serviceQuality.cards.latency')} {fmtMs(s30.ok_avg_elapsed_ms)}
                    {' · '}{t('serviceQuality.cards.errorRate')} {Number(s30.error_rate) ? s30.error_rate.toFixed(1) + '%' : '0%'}
                  </>
                ) : (
                  <span style={{ color: '#94a3b8' }}>{t('serviceQuality.cards.noTraffic')}</span>
                )}
              </div>
              {/* 下一时段预测（无同时段历史 → 灰字提示，不伪造结论） */}
              <div style={{ fontSize: 11, marginTop: 2 }}>
                {t('serviceQuality.cards.nextHour')}:{' '}
                {nextTier ? (
                  <span style={{ color: STATUS_COLORS[nextTier], fontWeight: 600 }}>
                    {t(`serviceQuality.status.${nextTier}`)}
                  </span>
                ) : (
                  <span style={{ color: '#cbd5e1' }}>{t('serviceQuality.cards.noHistory')}</span>
                )}
                {next && next.samples > 0 ? (
                  <span style={{ color: '#94a3b8' }}> · {t(`serviceQuality.forecast.conf.${next.confidence}`)}</span>
                ) : null}
              </div>
              {/* 峰谷（近 7 天时段画像口径） */}
              <div style={{ fontSize: 11, color: '#94a3b8' }}>
                {pv.peak_hour >= 0 ? (
                  <span>
                    {t('serviceQuality.profile.peak')} {fmtHour(pv.peak_hour)}
                    {pv.peak_calls_per_day ? ` (${fmtNum1(pv.peak_calls_per_day)}/${t('serviceQuality.profile.perDay')})` : ''}
                    {' · '}{t('serviceQuality.profile.valley')} {fmtHour(pv.valley_hour)}
                  </span>
                ) : null}
              </div>
              <div style={{ color: '#94a3b8', fontSize: 11 }}>
                {t('serviceQuality.cards.lastRequest', { ago: fmtAgo(m.last_request_at_ms, t) })}
                {m.baseline_ms_per_token > 0 ? (
                  <span> · {t('serviceQuality.cards.baseline', { v: m.baseline_ms_per_token })}</span>
                ) : null}
              </div>
            </div>
          </div>
        )
      })}
    </div>
  )
}
