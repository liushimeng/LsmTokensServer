import { Component } from 'react'

// React 错误边界：捕获子树渲染期异常（含 lazy import 失败、chunk 加载失败等）
// 防止整个应用白屏，提供用户可操作的兜底 UI
export default class ErrorBoundary extends Component {
  constructor(props) {
    super(props)
    this.state = { hasError: false, error: null }
  }

  static getDerivedStateFromError(error) {
    return { hasError: true, error }
  }

  componentDidCatch(error, info) {
    // 记录错误信息供调试；生产环境可改为上报到日志服务
    console.error('[ErrorBoundary] 渲染异常:', error, info)
  }

  handleReload = () => {
    window.location.reload()
  }

  render() {
    if (this.state.hasError) {
      return (
        <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', minHeight: '60vh', padding: 24, textAlign: 'center' }}>
          <div style={{ fontSize: 48, marginBottom: 16 }}>⚠️</div>
          <h2 style={{ margin: '0 0 8px', color: '#1e293b' }}>页面加载异常</h2>
          <p style={{ margin: '0 0 20px', color: '#64748b', maxWidth: 360 }}>
            页面资源加载失败，可能是服务已更新。请刷新页面重试。
          </p>
          <button className="btn" onClick={this.handleReload}>刷新页面</button>
          {this.state.error && (
            <pre style={{ marginTop: 20, padding: 12, background: '#f1f5f9', borderRadius: 6, fontSize: 12, color: '#64748b', maxWidth: 500, overflow: 'auto', whiteSpace: 'pre-wrap', wordBreak: 'break-all' }}>
              {this.state.error.message || String(this.state.error)}
            </pre>
          )}
        </div>
      )
    }
    return this.props.children
  }
}
