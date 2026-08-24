// 统一请求封装占位 —— 阶段5 按 API 契约实现（JWT 登录态、HTTPS、错误处理）
export async function request(path, options = {}) {
  const res = await fetch(path, {
    headers: { 'Content-Type': 'application/json', ...options.headers },
    credentials: 'include',
    ...options,
  })
  if (!res.ok) throw new Error(`HTTP ${res.status}`)
  return res.json()
}
