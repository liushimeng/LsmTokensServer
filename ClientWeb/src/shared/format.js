// 时间格式化：Unix 秒 / ISO 字符串 → YYYY-MM-DD HH:mm:ss（本地时区）
export function fmtTime(v) {
  if (!v && v !== 0) return '-'
  const d = typeof v === 'number' ? new Date(v * 1000) : new Date(v)
  if (isNaN(d.getTime())) return String(v)
  const p = (n) => String(n).padStart(2, '0')
  return `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`
}

// 千分位数字
export function fmtNum(v) {
  if (v === null || v === undefined || v === '') return '-'
  const n = Number(v)
  if (isNaN(n)) return String(v)
  return n.toLocaleString('zh-CN')
}

// 字节数人性化
export function fmtBytes(v) {
  const n = Number(v)
  if (!n || isNaN(n)) return '0 B'
  if (n < 1024) return `${n} B`
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`
  if (n < 1024 * 1024 * 1024) return `${(n / 1024 / 1024).toFixed(2)} MB`
  return `${(n / 1024 / 1024 / 1024).toFixed(2)} GB`
}

// 毫秒耗时人性化
export function fmtMs(v) {
  const n = Number(v)
  if (n === 0 || isNaN(n)) return '-'
  if (n < 1000) return `${n} ms`
  if (n < 60000) return `${(n / 1000).toFixed(2)} s`
  return `${Math.floor(n / 60000)} 分 ${Math.round((n % 60000) / 1000)} 秒`
}

// 从路由 query 初始化筛选条件（UserManage 等页面跳转带入 user_name / model_name）
export function pickRouteQuery(query) {
  const q = query || new URLSearchParams()
  return {
    userName: q.get('user_name') || '',
    modelName: q.get('model_name') || '',
  }
}
