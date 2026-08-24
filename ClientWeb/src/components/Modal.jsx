import { useEffect } from 'react'

// 通用弹窗：title / onClose / footer / width
export default function Modal({ title, onClose, children, footer, width = 720 }) {
  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose && onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])
  return (
    <div className="modal-mask" onClick={(e) => { if (e.target === e.currentTarget) onClose && onClose() }}>
      <div className="modal-box" style={{ maxWidth: width }}>
        <div className="modal-head">
          <span className="modal-title">{title}</span>
          <button className="modal-close" onClick={onClose}>✕</button>
        </div>
        <div className="modal-body">{children}</div>
        {footer ? <div className="modal-foot">{footer}</div> : null}
      </div>
    </div>
  )
}
