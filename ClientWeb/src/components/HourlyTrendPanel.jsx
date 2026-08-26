// HourlyTrendPanel：调用次数 + Token 数趋势面板（受控版）
// v3 受控版：接收页面级 span prop，不再自管窗口按钮；viewport/缩放完全交给 KLineTrendChart 内部
//
// 20260826 重构：
//   - 从「自管状态的容器组件」改为「受控的展示组件」
//   - 时间范围由父页面的 TimeRangeSelector 统一控制（span prop）
//   - 移除内部 hours state、窗口按钮组、独立持久化
//   - 超 720h（后端 trend 接口上限）自动截断并显示提示
//
// props:
//   api: 'ModelInfoInterface' | 'AgentInfoInterface'
//   span: 统一 span 编码（正=天，负=小时，0=全部）
//   labels: { loading, empty, call, token, zoomHint, reset, truncated }
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../shared/api'
import KLineTrendChart from './KLineTrendChart'
import { spanToHours } from '../shared/timeSpan'
import { useI18n } from '../i18n'

const MAX_TREND_HOURS = 720 // 后端 GetHourlyTrendAll 上限

export default function HourlyTrendPanel(props) {
  const { t } = useI18n()
  const {
    api,
    span,
    labels = {},
  } = props

  // span → hours 换算（超上限截断）
  const hours = useMemo(() => {
    const h = spanToHours(span)
    if (h <= 0) return 24
    return Math.min(h, MAX_TREND_HOURS)
  }, [span])

  const truncated = useMemo(() => {
    const h = spanToHours(span)
    return h > MAX_TREND_HOURS
  }, [span])

  const [points, setPoints] = useState([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const seqRef = useRef(0) // 防并发覆盖

  const load = useCallback((h) => {
    const seq = ++seqRef.current
    setLoading(true)
    setError('')
    post(api, { action: 'trend', hours: h })
      .then((res) => {
        if (seqRef.current !== seq) return
        const data = (res && res.data) || {}
        setPoints(Array.isArray(data.points) ? data.points : [])
      })
      .catch((e) => {
        if (seqRef.current !== seq) return
        setError(e && e.message ? e.message : String(e))
      })
      .finally(() => {
        if (seqRef.current === seq) setLoading(false)
      })
  }, [api])

  useEffect(() => {
    load(hours)
  }, [hours, load])

  return (
    <div>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 10, alignItems: 'center' }}>
        {truncated ? (
          <span style={{ color: '#f59e0b', fontSize: 12 }}>
            {labels.truncated || t('trend.truncatedHint', { days: 30 }) || '仅展示最近 30 天趋势'}
          </span>
        ) : null}
        <span style={{ color: '#94a3b8', fontSize: 12, marginLeft: 'auto' }}>
          {loading ? (labels.loading || '加载中…') : ''}
        </span>
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <KLineTrendChart
        points={points}
        loading={loading && !points.length}
        emptyHint={labels.empty || '暂无数据'}
        callLabel={labels.call || '调用次数'}
        tokenLabel={labels.token || 'Tokens'}
        zoomHint={labels.zoomHint}
        resetLabel={labels.reset}
      />
    </div>
  )
}
