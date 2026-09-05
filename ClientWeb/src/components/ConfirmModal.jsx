import { createContext, useCallback, useContext, useState } from 'react'
import { createPortal } from 'react-dom'
import { useI18n } from '../i18n'

// 全局确认弹窗：替代原生 window.confirm，风格与 Modal 统一。
// 用法：
//   const confirm = useConfirm()
//   if (await confirm('确定删除吗？')) { /* 用户点击确认 */ }
const ConfirmContext = createContext(null)

export function ConfirmProvider({ children }) {
  const [state, setState] = useState(null) // { message, onResolve }
  const { t } = useI18n()

  const confirm = useCallback((message) => new Promise((resolve) => {
    setState({
      message,
      onResolve: (result) => { setState(null); resolve(result) },
    })
  }), [])

  return (
    <ConfirmContext.Provider value={confirm}>
      {children}
      {state && createPortal((
        <div className="modal-mask" onClick={(e) => { if (e.target === e.currentTarget) state.onResolve(false) }}>
          <div className="modal-box" style={{ maxWidth: 420 }}>
            <div className="modal-body" style={{ padding: 24 }}>
              <div style={{ fontSize: 15, lineHeight: 1.6, whiteSpace: 'pre-wrap' }}>{state.message}</div>
            </div>
            <div className="modal-foot">
              <button className="btn" onClick={() => state.onResolve(false)}>{t('common.cancel')}</button>
              <button className="btn btn-primary" onClick={() => state.onResolve(true)}>{t('common.confirm')}</button>
            </div>
          </div>
        </div>
      ), document.body)}
    </ConfirmContext.Provider>
  )
}

export function useConfirm() {
  const ctx = useContext(ConfirmContext)
  if (!ctx) throw new Error('useConfirm 必须在 <ConfirmProvider> 内使用')
  return ctx
}
