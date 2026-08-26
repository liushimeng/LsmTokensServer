// 字符串插值工具：支持 {varName} 语法的简单插值
const INTERPOLATION_RE = /\{(\w+)\}/g

export function interpolate(template, vars) {
  if (!template || typeof template !== 'string') return ''
  if (!vars || typeof vars !== 'object') return template
  return template.replace(INTERPOLATION_RE, (match, key) => {
    return Object.prototype.hasOwnProperty.call(vars, key) ? String(vars[key]) : match
  })
}
