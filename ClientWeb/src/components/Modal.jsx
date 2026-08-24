import { useEffect, useState } from 'react'

// 通用弹窗：title / onClose / footer / width；支持全屏切换（迁移自旧 server_web_common_toolbar_base.go）
export default function Modal({ title, onClose, children, footer, width = 720 }) {
  const [full, setFull] = useState(false)

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose && onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return (
    <div className="modal-mask" onClick={(e) => { if (e.target === e.currentTarget) onClose && onClose() }}>
      <div className={`modal-box${full ? ' modal-full' : ''}`} style={full ? undefined : { maxWidth: width }}>
        <div className="modal-head">
          <span className="modal-title">{title}</span>
          <span style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <button className="modal-close" title="全屏切换"
                    onClick={() => setFull(!full)}>{full ? '⋍' : '⛶'}</button>
            <button className="modal-close" onClick={onClose}>✕</button>
          </span>
        </div>
        <div className="modal-body">{children}</div>
        {footer ? <div className="modal-foot">{footer}</div> : null}
      </div>
    </div>
  )
}
