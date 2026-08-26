// 纯 JS 文件（无 JSX），避免构建工具对 JSX 文件导出分析的兼容问题
import { createContext } from 'react'

const I18nContext = createContext(null)

export default I18nContext
