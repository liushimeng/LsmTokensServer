// HourlyTrendPanel：调用次数 + Token 数趋势面板
// - 窗口切换：1d / 3d / 7d / 30d，对应 hours = 24 / 72 / 168 / 720
// - 数据源：后端 /{api} action=trend（ModelInfoInterface / AgentInfoInterface）
// - 动画式加载：先拉 24h 立即渲染；窗口切换走分批 fetch + requestIdleCallback 预取
// - 慢查询防护：后端按 hours 走 keyset 分页 + 25s context；客户端 8s 超时让用户感知
//
// props:
//   api: 'ModelInfoInterface' | 'AgentInfoInterface'
//   defaultWindow: 默认窗口 hours（默认 24）
//   windowOptions: 备选窗口数组 hours（默认 [24, 72, 168, 720]）
//   labels: { d1, d3, d7, d30, loading, empty, call, token }
//   storageKey: localStorage 记忆键（按 role 隔离由调用方负责）
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../shared/api'
import { useI18n } from '../i18n'
import KLineTrendChart from './KLineTrendChart'

const DEFAULT_WINDOWS = [24, 72, 168, 720]

function fmtHours(h) {
  if (h < 24) return `${h}h`
  const d = h / 24
  if (Number.isInteger(d)) return `${d}d`
  return `${d}d`
}

export default function HourlyTrendPanel(props) {
  const { t } = useI18n()
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
  const idleRef = useRef(null)
  const seqRef = useRef(0) // 防止并发覆盖

  const load = useCallback((h) => {
    const seq = ++seqRef.current
    setLoading(true)
    setError('')
    // 客户端超时由 shared/api.js 的 REQUEST_TIMEOUT_MS (5s) 兜底。
    // 后端独立走 database.StatsDB() 25s context，超时驱动向 MySQL 发 KILL。
    // 序列号 seq 防并发：旧请求 resolve 不覆盖新数据。
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

  // 主拉：当前窗口
  useEffect(() => {
    if (storageKey) localStorage.setItem(storageKey, String(hours))
    load(hours)
  }, [hours, load, storageKey])

  // 预取：下一个窗口在 idle 时拉一次缓存到内存（仅用于切窗时缩短等待）
  useEffect(() => {
    if (typeof window.requestIdleCallback !== 'function') return
    const nextIdx = windowOptions.indexOf(hours) + 1
    if (nextIdx >= windowOptions.length) return
    const nextH = windowOptions[nextIdx]
    if (idleRef.current) cancelIdleCallbackSafe(idleRef.current)
    idleRef.current = window.requestIdleCallback(() => {
      // 仅静默预热，不更新 UI（避免闪烁）
      post(api, { action: 'trend', hours: nextH }).catch(() => {})
    }, { timeout: 4000 })
    return () => cancelIdleCallbackSafe(idleRef.current)
  }, [hours, api, windowOptions])

  return (
    <div>
      <div style={{ display: 'flex', gap: 6, flexWrap: 'wrap', marginBottom: 10 }}>
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
        <span style={{ color: '#94a3b8', fontSize: 12, alignSelf: 'center' }}>
          {loading ? (labels.loading || '…') : ''}
        </span>
      </div>
      {error ? <div className="alert alert-error">{error}</div> : null}
      <KLineTrendChart
        points={points}
        loading={loading && !points.length}
        emptyHint={labels.empty || '—'}
        callSeriesLabel={labels.call || t('modelInfo.trendCallSeries')}
        tokenSeriesLabel={labels.token || t('modelInfo.trendTokenSeries')}
        tooltipTemplate={labels.tooltip || t('modelInfo.trendTooltip')}
      />
    </div>
  )
}

function cancelIdleCallbackSafe(id) {
  if (typeof window.cancelIdleCallback === 'function') window.cancelIdleCallback(id)
}