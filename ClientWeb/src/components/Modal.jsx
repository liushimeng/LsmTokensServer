import { useEffect, useState } from 'react'
import { createPortal } from 'react-dom'
import { useI18n } from '../i18n'

// 通用弹窗：title / onClose / footer / width；支持全屏切换（迁移自旧 server_web_common_toolbar_base.go）
export default function Modal({ title, onClose, children, footer, width = 720 }) {
  const [full, setFull] = useState(false)
  const { t } = useI18n()

  useEffect(() => {
    const onKey = (e) => { if (e.key === 'Escape') onClose && onClose() }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  // 挂载到 document.body：脱离父级 DOM 的 color 继承（如深色顶栏 color:#fff）与层叠上下文，
  // 否则从顶栏工具栏（ToolbarDialogs）打开的弹窗会出现白底白字
  return createPortal((
    <div className="modal-mask" onClick={(e) => { if (e.target === e.currentTarget) onClose && onClose() }}>
      <div className={`modal-box${full ? ' modal-full' : ''}`} style={full ? undefined : { maxWidth: width }}>
        <div className="modal-head">
          <span className="modal-title">{title}</span>
          <span style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
            <button className="modal-close" title={t('common.expand') + ' / ' + t('common.collapse')}
                    onClick={() => setFull(!full)}>{full ? '⋍' : '⛶'}</button>
            <button className="modal-close" onClick={onClose}>✕</button>
          </span>
        </div>
        <div className="modal-body">{children}</div>
        {footer ? <div className="modal-foot">{footer}</div> : null}
      </div>
    </div>
  ), document.body)
}
