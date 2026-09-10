// components/Skeleton.jsx —— 骨架屏组件（阶段BZ）
//
// 解决"加载中… 一行文字"信息量不足、用户看不出页面是否能交互的问题。
// 三个 preset：
//   - table：8 行假行（高度 14px，宽度 30%-95% 不等模拟内容差异）
//   - card：4 张假卡片（高度 80px）
//   - chart：40% 高度的假折线图（块状灰白渐变 + animation shimmer）
//
// 用法：<Skeleton preset="table" rows={6} />
import { useI18n } from '../i18n'

function PulseRow({ width = '80%', height = 14, mb = 8 }) {
  return (
    <div
      className="skeleton-pulse"
      style={{
        width,
        height,
        marginBottom: mb,
        background: 'linear-gradient(90deg, #f0f0f0 25%, #e8e8e8 50%, #f0f0f0 75%)',
        backgroundSize: '200% 100%',
        borderRadius: 4,
      }}
    />
  )
}

export default function Skeleton({ preset = 'table', rows = 6, title, hint }) {
  const { t } = useI18n()
  if (preset === 'card') {
    return (
      <div className="skeleton-card-grid">
        {Array.from({ length: rows }).map((_, i) => (
          <div key={i} className="skeleton-card">
            <PulseRow width="40%" height={18} mb={10} />
            <PulseRow width="70%" height={28} mb={6} />
            <PulseRow width="55%" height={12} />
          </div>
        ))}
      </div>
    )
  }
  if (preset === 'chart') {
    return (
      <div className="skeleton-chart">
        <div
          className="skeleton-pulse"
          style={{
            width: '100%',
            height: '40%',
            background: 'linear-gradient(90deg, #f0f0f0 25%, #e8e8e8 50%, #f0f0f0 75%)',
            backgroundSize: '200% 100%',
            borderRadius: 6,
          }}
        />
        <div style={{ marginTop: 8 }}>
          <PulseRow width="30%" height={12} mb={4} />
          <PulseRow width="50%" height={12} mb={4} />
          <PulseRow width="40%" height={12} />
        </div>
      </div>
    )
  }
  // table
  return (
    <div className="skeleton-table">
      {title ? <div style={{ fontSize: 12, color: '#888', marginBottom: 8 }}>{title}</div> : null}
      {Array.from({ length: rows }).map((_, i) => {
        const widths = ['95%', '70%', '85%', '60%', '90%', '55%', '75%', '80%']
        return <PulseRow key={i} width={widths[i % widths.length]} height={14} mb={8} />
      })}
      {hint ? <div style={{ marginTop: 8, fontSize: 12, color: '#888' }}>{hint || t('common.loading')}</div> : null}
    </div>
  )
}