// 客户端地址（IP / 端口）解析工具
// 数据源：交易表 TAgentHttpTransactionDataItem.RequestRemoteAddr（列 request_remote_addr），
// 落库值为 Go net/http 的 r.RemoteAddr 原样字符串，实测有四种形态：
//   1) IPv4 + 端口        10.0.0.5:54321
//   2) IPv6 + 端口（带[]）  [2408:8207::1]:443
//   3) 裸 IPv6（无端口）    2408:8207::1 / ::1        —— 多段冒号，禁止按最后一个冒号切端口
//   4) 无端口 IPv4 / 空值   10.0.0.5 / ""
// 与后端 proxy/server_http_ai_proxy_security.go 的 net.SplitHostPort 语义保持一致。
//
// 约定：本模块不得 import 任何依赖（含无扩展名导入），以便 `node xxx.test.js` 直接加载自检
// （沿用 src/pages/chat-analysis/agentToolFields.test.js 的无测试框架约定）。

// splitHostPort 拆分 "host:port" / "[v6]:port"。
// 返回 { host, port }：port 为空字符串表示无端口或不可识别为端口。
export function splitHostPort(v) {
  const s = String(v == null ? '' : v).trim()
  if (!s) return { host: '', port: '' }

  // 形态 2：[IPv6]:port
  if (s.startsWith('[')) {
    const close = s.indexOf(']')
    if (close > 0) {
      const host = s.slice(1, close)
      const rest = s.slice(close + 1)
      const port = rest.startsWith(':') ? rest.slice(1) : ''
      return { host, port: isPort(port) ? port : '' }
    }
    // 中括号不闭合 → 原样当主机名展示
    return { host: s, port: '' }
  }

  const firstColon = s.indexOf(':')
  const lastColon = s.lastIndexOf(':')
  // 形态 3：裸 IPv6（多于一个冒号）→ 整体是地址，无端口
  if (firstColon !== lastColon) return { host: s, port: '' }
  if (firstColon < 0) return { host: s, port: '' }

  const host = s.slice(0, firstColon)
  const port = s.slice(firstColon + 1)
  // host 里不含冒号且 port 全数字 → 形态 1；否则视为异常原样展示
  return isPort(port) ? { host, port } : { host: s, port: '' }
}

// isPort 端口形态校验：纯数字且 1~5 位（0-65535 的宽松判定，异常值一律不认作端口）
function isPort(p) {
  if (!p) return false
  if (!/^\d{1,5}$/.test(p)) return false
  const n = Number(p)
  return n > 0 && n <= 65535
}

// fmtRemoteAddr 详情头部展示用：拆出 host / port，并给出兜底展示串。
// 返回 { host, port, display }；display 在空值时为 '-'。
export function fmtRemoteAddr(v) {
  const { host, port } = splitHostPort(v)
  if (!host && !port) return { host: '', port: '', display: '-' }
  return { host: host || '-', port, display: port ? `${host}:${port}` : (host || '-') }
}
