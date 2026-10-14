// 阶段CV：状态筛选候选码清单 + 输入净化函数
// 独立纯数据/纯函数模块（无任何 import），便于 node 自检脚本直接加载；
// constants.js re-export 保持既有 import 路径不变。
//
// 后端配套：ServerGo/models.NormalizeStatusCodeFilter + QueryAgentHttpTransactions
// 状态码前缀匹配（response_status LIKE 'code%' / NOT LIKE）。

// 标准 RFC 9110 状态码 + Anthropic 扩展 529（Overloaded），共 34 项，必含 429/529/404。
// value 为纯数字码（入库查询用），label 为语义短语（辅助识别）。
export const HTTP_STATUS_OPTIONS = [
  { code: '100', label: 'Continue' },
  { code: '101', label: 'Switching Protocols' },
  { code: '200', label: 'OK' },
  { code: '201', label: 'Created' },
  { code: '202', label: 'Accepted' },
  { code: '204', label: 'No Content' },
  { code: '206', label: 'Partial Content' },
  { code: '301', label: 'Moved Permanently' },
  { code: '302', label: 'Found' },
  { code: '304', label: 'Not Modified' },
  { code: '307', label: 'Temporary Redirect' },
  { code: '308', label: 'Permanent Redirect' },
  { code: '400', label: 'Bad Request' },
  { code: '401', label: 'Unauthorized' },
  { code: '402', label: 'Payment Required' },
  { code: '403', label: 'Forbidden' },
  { code: '404', label: 'Not Found' },
  { code: '405', label: 'Method Not Allowed' },
  { code: '408', label: 'Request Timeout' },
  { code: '409', label: 'Conflict' },
  { code: '413', label: 'Payload Too Large' },
  { code: '415', label: 'Unsupported Media Type' },
  { code: '422', label: 'Unprocessable Entity' },
  { code: '429', label: 'Too Many Requests' },
  { code: '451', label: 'Unavailable For Legal Reasons' },
  { code: '500', label: 'Internal Server Error' },
  { code: '501', label: 'Not Implemented' },
  { code: '502', label: 'Bad Gateway' },
  { code: '503', label: 'Service Unavailable' },
  { code: '504', label: 'Gateway Timeout' },
  { code: '505', label: 'HTTP Version Not Supported' },
  { code: '507', label: 'Insufficient Storage' },
  { code: '511', label: 'Network Authentication Required' },
  { code: '529', label: 'Overloaded (Anthropic)' },
]

// 状态筛选输入净化——只保留数字、最长 3 位。
// 输入框 onChange 与 localStorage 恢复路径共用（兜底历史脏值如 "200 OK"）。
export function sanitizeStatusCodeInput(v) {
  return String(v ?? '').replace(/\D/g, '').slice(0, 3)
}
