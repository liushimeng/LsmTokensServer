// 20260826-05 工具栏版本号/编译时间显示：
// 前端版本与编译时间为构建期常量（vite define 注入 __APP_VERSION__/__APP_BUILD_TIME__，
// 与后端 config.APP_VERSION 同源），后端版本与编译时间挂载时经 /AppVersionInterface 拉取一次。
// 接口失败时优雅降级：仅展示前端构建期信息，不阻塞页面。
import { useEffect, useState } from 'react'
import { get } from '../shared/api'
import { useI18n } from '../i18n'

// '2026-08-26_17:00:00' → '08-26 17:00'（紧凑内联展示，完整值放 title 提示）
function compactTime(s) {
  const m = String(s || '').match(/^(\d{4})-(\d{2})-(\d{2})_(\d{2}):(\d{2})/)
  if (!m) return s || ''
  return `${m[2]}-${m[3]} ${m[4]}:${m[5]}`
}

export default function VersionInfo() {
  const { t } = useI18n()
  const [backend, setBackend] = useState(null)

  // 版本/编译时间为进程常量，挂载时拉取一次即可，不轮询
  useEffect(() => {
    let alive = true
    get('/AppVersionInterface')
      .then((d) => { if (alive && d) setBackend(d) })
      .catch(() => { /* 离线/服务重启：降级为仅前端信息 */ })
    return () => { alive = false }
  }, [])

  const version = (backend && backend.version) || __APP_VERSION__
  const beTime = compactTime(backend && backend.backend_build_time)
  const feTime = compactTime(__APP_BUILD_TIME__)

  const title = backend
    ? [
        `${backend.app_name} · ${backend.product_name}`,
        `${t('common.version.backend')}: ${backend.version} · ${backend.backend_build_time || '-'}`,
        `${t('common.version.frontend')}: ${__APP_VERSION__} · ${__APP_BUILD_TIME__}`,
        `${t('common.version.go')}: ${backend.go_version || '-'}`,
      ].join('\n')
    : `${t('common.version.frontend')}: ${__APP_VERSION__} · ${__APP_BUILD_TIME__}`

  return (
    <span className="app-version" title={title}>
      <span className="app-version-num">{version}</span>
      <span className="app-version-times">
        {beTime ? ` · ${t('common.version.backend')} ${beTime}` : ''}
        {feTime ? ` · ${t('common.version.frontend')} ${feTime}` : ''}
      </span>
    </span>
  )
}
