// TimeRangeSelector：时间跨度通用下拉组件（20260826 动态档位）
//
// 档位由 /TimeSpanConfigInterface 下发的 transactionRetentionDays 推导：
//   最小 1 小时，最大 transactionRetentionDays + 1 天（≤365 天），共 10 档。
// 管理员 Web 与用户 Web 双构建共用（不含任何管理端专属代码）。
// 档位数据 hook 见 shared/useTimeSpanLevels.js（与组件分文件，避免 only-export-components）。
//
// 用法：
//   const { levels, loading } = useTimeSpanLevels()
//   <TimeRangeSelector span={span} onChange={setSpan} levels={levels} loading={loading} />
//
// value 语义为统一 span 编码：0 无限制（新 UI 不再提供）；>0 最近 N 天；<0 最近 |N| 小时。
import { useI18n } from '../i18n'
import { spanLabel } from '../shared/timeSpan'

export default function TimeRangeSelector(props) {
  const { t } = useI18n()
  const { span, onChange, levels, loading, disabled, style } = props

  return (
    <select
      value={span}
      disabled={disabled || loading || !levels.length}
      onChange={(e) => onChange(Number(e.target.value))}
      style={style}
      title={loading ? t('timeRange.loadingConfig') : undefined}
    >
      {levels.map((l) => {
        const lab = spanLabel(l.span, levels.length ? levels[levels.length - 1].hours : 0)
        return (
          <option key={l.span} value={l.span}>{t(lab.key, lab.vars)}</option>
        )
      })}
    </select>
  )
}
