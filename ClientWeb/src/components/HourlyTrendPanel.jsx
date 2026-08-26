// HourlyTrendPanel：调用次数 + Token 数趋势面板
// v2 简化版：只负责窗口切换 + 拉数据 + 持久化；viewport/缩放完全交给 KLineTrendChart 内部
//
// props:
//   api: 'ModelInfoInterface' | 'AgentInfoInterface'
//   defaultWindow: 默认窗口 hours（默认 24）
//   windowOptions: 备选窗口数组 hours（默认 [24, 72, 168, 720]）
//   labels: { '1d', '3d', '7d', '30d', loading, empty, call, token, zoomHint, reset }
//   storageKey: localStorage 记忆键
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../shared/api'
import KLineTrendChart from './KLineTrendChart'

const DEFAULT_WINDOWS = [24, 72, 168, 720]

function fmtHours(h) {
  if (h < 24) return `${h}h`
  const d = h / 24
  return Number.isInteger(d) ? `${d}d` : `${d}d`
}

export default function HourlyTrendPanel(props) {
  const {
    api,
    defaultWindow = 24,
    windowOptions = DEFAULT_WINDOWS,
    labels = {},
    storageKey,
  } = props

  const initialHours = useMemo(() => {
    if (storageKey) {
      const cached = parseInt(localStorage.getItem(storageKey), 10)
      if (windowOptions.includes(cached)) return cached
    }
    return defaultWindow
  }, [])

  const [hours, setHours] = useState(initialHours)
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
    if (storageKey) localStorage.setItem(storageKey, String(hours))
    load(hours)
  }, [hours, load, storageKey])

  return (
    <div>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 10, alignItems: 'center' }}>
        {windowOptions.map((h) => (
          <button
            key={h}
            className={'btn ' + (h === hours ? 'btn-primary' : '')}
            style={{ padding: '4px 10px', fontSize: 12 }}
            disabled={loading && h === hours}
            onClick={() => setHours(h)}
          >
            {labels[fmtHours(h)] || fmtHours(h)}
          </button>
        ))}
        <span style={{ color: '#94a3b8', fontSize: 12, marginLeft: 4 }}>
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