// HourlyTrendPanel：调用次数 + Token 数趋势面板
// v2 简化版：只负责窗口切换 + 拉数据 + 持久化；viewport/缩放完全交给 KLineTrendChart 内部
//
// 20260826 动态档位：窗口按钮选项改由 /TimeSpanConfigInterface 推导（与页面级
// TimeRangeSelector 同一档位表），取其中 ≥24h 且 ≤720h（后端 trend 接口上限）的子集；
// 档位加载前沿用固定 [24, 72, 168, 720]，避免首帧空白。
//
// props:
//   api: 'ModelInfoInterface' | 'AgentInfoInterface'
//   defaultWindow: 默认窗口 hours（默认 24）
//   labels: { loading, empty, call, token, zoomHint, reset }
//   storageKey: localStorage 记忆键
import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { post } from '../shared/api'
import KLineTrendChart from './KLineTrendChart'
import { useTimeSpanLevels } from '../shared/useTimeSpanLevels'
import { hoursToSpan, spanLabel } from '../shared/timeSpan'
import { useI18n } from '../i18n'

const DEFAULT_WINDOWS = [24, 72, 168, 720]
const MAX_TREND_HOURS = 720 // 后端 GetHourlyTrendAll 上限

function fmtHours(h) {
  if (h < 24) return `${h}h`
  const d = h / 24
  return Number.isInteger(d) ? `${d}d` : `${d}d`
}

export default function HourlyTrendPanel(props) {
  const { t } = useI18n()
  const {
    api,
    defaultWindow = 24,
    labels = {},
    storageKey,
  } = props
  const { levels } = useTimeSpanLevels()

  // 动态窗口：档位表取 ≥24h 且 ≤720h 的子集；档位未加载时用固定默认窗口
  const windowOptions = useMemo(() => {
    const wins = levels.map((l) => l.hours).filter((h) => h >= 24 && h <= MAX_TREND_HOURS)
    return wins.length >= 2 ? wins : DEFAULT_WINDOWS.filter((h) => h <= MAX_TREND_HOURS)
  }, [levels])

  const initialHours = useMemo(() => {
    if (storageKey) {
      const cached = parseInt(localStorage.getItem(storageKey), 10)
      if (windowOptions.includes(cached)) return cached
    }
    return defaultWindow
    // eslint-disable-next-line react-hooks/exhaustive-deps
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

  // 动态档位到达后：当前窗口不在档位内 → 就近切换（保持持久化值合法）
  useEffect(() => {
    if (!windowOptions.length || windowOptions.includes(hours)) return
    let best = windowOptions[0]
    let bestDist = Infinity
    for (const h of windowOptions) {
      const dist = Math.abs(h - hours)
      if (dist < bestDist) {
        bestDist = dist
        best = h
      }
    }
    setHours(best)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [windowOptions])

  const windowLabel = (h) => {
    const lab = spanLabel(hoursToSpan(h))
    return t(lab.key, lab.vars)
  }

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
            {windowLabel(h) || fmtHours(h)}
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
