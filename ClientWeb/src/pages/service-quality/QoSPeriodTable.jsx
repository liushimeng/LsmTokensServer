// QoSPeriodTable：单模型「逐时段服务质量明细」。
//
// v2.0.84 新增（方案 §5.3）：正面回答「最近 1 天每个小时 / 最近 1 周每一天，
// 这个源站模型到底差在哪」——趋势图给形状，本表给逐时段数值与样本量。
//   - 时段粒度跟随页面窗口：1h/24h → 小时桶（1h 视图用分钟桶），7d → 天桶
//   - 仅展示有请求的时段（空桶在趋势图里已以断线表达，表格不铺零值噪声）
//   - 进行中时段整行浅色底 + 「进行中」标签，避免被当成完整时段结论
//   - 系数类指标样本 < MIN_COEFFICIENT_SAMPLES 时显示 — （低样本不伪造结论）
//   - 列宽拖拽 / 排序 / 排序持久化由通用 DataTable 自动提供
import { useMemo } from 'react'
import DataTable from '../../components/DataTable'
import {
  windowSeries, fmtMs, fmtPct, fmtRatio, fmtNum, bucketShortLabel, MIN_COEFFICIENT_SAMPLES,
} from './constants'

export default function QoSPeriodTable({ model, win, t }) {
  // 1h → 分钟桶（60 行）、24h → 小时桶（24 行）、7d → 天桶（7 行）
  const series = useMemo(() => windowSeries(model, win), [model, win])

  // 升序桶序列 → 最新在前（运维第一眼看当前时段）
  const rows = useMemo(
    () => series.filter((p) => Number(p.count) > 0).slice().reverse().map((p) => ({
      ...p, label: bucketShortLabel(p.bucket),
    })),
    [series],
  )

  const low = (p) => (Number(p.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES

  const columns = [
    {
      key: 'bucket',
      title: t('serviceQuality.period.bucket'),
      nowrap: true,
      render: (_, p) => (
        <span style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
          {p.bucket}
          {p.partial ? (
            <span style={{
              fontSize: 10, padding: '0 4px', borderRadius: 4,
              border: '1px solid #cbd5e1', color: '#64748b', background: '#f8fafc',
            }}>{t('serviceQuality.period.partial')}</span>
          ) : null}
        </span>
      ),
    },
    {
      key: 'count', title: t('serviceQuality.period.count'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.count) || 0,
      render: (_, p) => fmtNum(p.count),
    },
    {
      key: 'success', title: t('serviceQuality.period.success'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.success_count) || 0,
      render: (_, p) => fmtNum(p.success_count),
    },
    {
      key: 'ok_avg', title: t('serviceQuality.period.avg'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.ok_avg_elapsed_ms) || 0,
      render: (_, p) => fmtMs(p.ok_avg_elapsed_ms),
    },
    {
      key: 'ok_p95', title: t('serviceQuality.period.p95'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.ok_p95_ms) || 0,
      render: (_, p) => fmtMs(p.ok_p95_ms),
    },
    {
      key: 'ms_per_token', title: t('serviceQuality.period.msPerToken'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.ok_ms_per_token) || 0,
      render: (_, p) => (Number(p.ok_ms_per_token) > 0 ? Number(p.ok_ms_per_token).toFixed(2) : '-'),
    },
    {
      key: 'coeff', title: t('serviceQuality.period.coeff'), sortable: true, nowrap: true,
      sortValue: (p) => (low(p) ? -1 : Number(p.avg_congestion) || 0),
      render: (_, p) => (low(p) ? <span title={t('serviceQuality.period.lowSample', { count: p.coefficient_samples || 0 })}>—</span> : fmtRatio(p.avg_congestion)),
    },
    {
      key: 'stall', title: t('serviceQuality.period.stall'), sortable: true, nowrap: true,
      sortValue: (p) => (low(p) ? -1 : Number(p.stall_rate) || 0),
      render: (_, p) => (low(p) ? '—' : fmtPct(p.stall_rate)),
    },
    {
      key: 'jitter', title: t('serviceQuality.period.jitter'), sortable: true, nowrap: true,
      sortValue: (p) => (low(p) ? -1 : Number(p.jitter) || 0),
      render: (_, p) => (low(p) ? '—' : fmtPct(p.jitter)),
    },
    {
      key: 'e429', title: t('serviceQuality.period.e429'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.error_429) || 0,
      render: (_, p) => fmtNum(p.error_429),
    },
    {
      key: 'e5xx', title: t('serviceQuality.period.e5xx'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.error_5xx) || 0,
      render: (_, p) => fmtNum(p.error_5xx),
    },
    {
      key: 'other', title: t('serviceQuality.period.other'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.error_other) || 0,
      render: (_, p) => fmtNum(p.error_other),
    },
    {
      key: 'error_rate', title: t('serviceQuality.period.errorRate'), sortable: true, nowrap: true,
      sortValue: (p) => Number(p.error_rate) || 0,
      render: (_, p) => fmtPct(p.error_rate),
    },
  ]

  return (
    <div>
      <DataTable
        rowKey="bucket"
        rows={rows}
        columns={columns}
        empty={t('serviceQuality.period.empty')}
        rowClass={(p) => (p.partial ? 'qos-period-partial' : '')}
      />
      <div style={{ marginTop: 4, fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.period.hint')}</div>
    </div>
  )
}
