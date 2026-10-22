// QoSPeriodTable：全模型逐时段明细长表（v2.0.86 阶段DA）。
//
// v2.0.84 原本是「单模型 + 模型下拉」；v2.0.86 按需求删除模型下拉，
// 改为**所有模型一起展示**（行 = 模型 × 时段，长表）：
//   - 每个模型 × 每个时段一行，默认按「模型 → 时段倒序」；
//   - 保留全部 14 项指标（请求数/成功/时延/P95/ms-Token/系数/卡顿/抖动/错误分类/错误率）；
//   - 系数类指标样本 < MIN_COEFFICIENT_SAMPLES 时显示 —（低样本不伪造结论）；
//   - 进行中时段整行浅色底 + 「进行中」标签。
// 图形化的「时段 × 模型」视图见同目录 QoSPeriodMatrix.jsx。
import { useMemo } from 'react'
import DataTable from '../../components/DataTable'
import {
  windowSeries, fmtMs, fmtPct, fmtRatio, fmtNum, bucketShortLabel, MIN_COEFFICIENT_SAMPLES,
} from './constants'

export default function QoSPeriodTable({ models, win, t }) {
  const rows = useMemo(() => {
    const out = []
    for (const m of models) {
      const series = windowSeries(m, win)
      // 升序桶序列 → 最新在前（运维第一眼看当前时段）
      series.filter((p) => Number(p.count) > 0).slice().reverse().forEach((p) => {
        out.push({
          ...p,
          model: m.dst_model_name,
          label: bucketShortLabel(p.bucket),
          rowKey: m.dst_model_name + '|' + p.bucket,
        })
      })
    }
    return out
  }, [models, win])

  const low = (p) => (Number(p.coefficient_samples) || 0) < MIN_COEFFICIENT_SAMPLES

  const columns = [
    {
      key: 'model', title: t('serviceQuality.table.model'), nowrap: true,
      sortValue: (p) => p.model,
      render: (_, p) => <b style={{ color: '#0f172a' }}>{p.model}</b>,
    },
    {
      key: 'bucket', title: t('serviceQuality.period.bucket'), nowrap: true,
      sortValue: (p) => p.bucket,
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
        rowKey="rowKey"
        rows={rows}
        columns={columns}
        empty={t('serviceQuality.period.empty')}
        rowClass={(p) => (p.partial ? 'qos-period-partial' : '')}
      />
      <div style={{ marginTop: 4, fontSize: 11, color: '#94a3b8' }}>{t('serviceQuality.period.hintAll')}</div>
    </div>
  )
}
