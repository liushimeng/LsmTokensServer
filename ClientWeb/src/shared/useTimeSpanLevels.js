// useTimeSpanLevels：时间跨度动态档位 React hook（20260826）
// 从 shared/ 提供（与 useUserModelOptions 同模式），避免组件文件混导非组件触发
// react(only-export-components) 告警。模块级 60s 缓存，多页面共享一次请求。
import { useEffect, useMemo, useState } from 'react'
import { buildSpanLevels, fetchTimeSpanConfig } from './timeSpan'

export function useTimeSpanLevels() {
  const [config, setConfig] = useState(null)

  useEffect(() => {
    let alive = true
    fetchTimeSpanConfig().then((c) => {
      if (alive) setConfig(c)
    })
    return () => { alive = false }
  }, [])

  const levels = useMemo(() => (config ? buildSpanLevels(config.maxSpanHours, config.levels) : []), [config])
  return { levels, loading: !config, config }
}
