// 详情底部状态栏：字段名、视图类型、大小、行数、复制按钮
import { fmtBytes } from '../../shared/format'
import { useI18n } from '../../i18n'
import { DETAIL_FIELDS, viewLabels } from './constants'

export default function DetailFooter({ tab, view, value, copyOk, onCopy }) {
  const { t } = useI18n()
  const isBody = tab.includes('body')
  const labels = viewLabels(t)
  const byteSize = fmtBytes((value || '').length)
  const lineCount = (value || '').split('\n').length
  const fieldLabel = DETAIL_FIELDS.find((f) => f.key === tab)?.titleKey
    ? t(DETAIL_FIELDS.find((f) => f.key === tab).titleKey)
    : tab

  return (
    <footer className="detail-foot">
      <div className="detail-foot-meta">
        <span className="muted">{t('chatAnalysis.field')}</span>
        <span>{fieldLabel}</span>
        {isBody ? <span className="muted">{t('chatAnalysis.view')}</span> : null}
        {isBody ? <span>{labels[view]}</span> : null}
        <span className="muted">{t('chatAnalysis.size')}</span><span>{byteSize}</span>
        <span className="muted">{t('chatAnalysis.lines')}</span><span>{lineCount}</span>
      </div>
      <div className="detail-foot-actions">
        <button className="btn btn-sm" onClick={onCopy}>
          {copyOk ? t('common.copied') + ' ✓' : t('chatAnalysis.copyView')}
        </button>
      </div>
    </footer>
  )
}
