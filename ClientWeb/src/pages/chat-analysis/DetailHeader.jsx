// 详情头部组件：协议流向 + KPI 卡片 + 请求信息行
import { fmtTime, fmtNum, fmtBytes, fmtMs } from '../../shared/format'
import { protocolName, protocolBadgeClass, protocolBadgeText, protocolBadgeTitle, ALGO_TYPE_CONVERTER } from './constants'
import { useI18n } from '../../i18n'

export default function DetailHeader({ row }) {
  const { t } = useI18n()
  if (!row) return null

  const algoType = row.dst_endpoint_algorithm_type
  const isConvert = algoType === ALGO_TYPE_CONVERTER
  const statusOk = String(row.response_status).startsWith('2')

  return (
    <header className="detail-head">
      {/* 协议流向 */}
      <div className="detail-protocol-flow">
        <span className={row.protocol_type === 1 ? 'protocol-badge protocol-anthropic' : row.protocol_type === 2 ? 'protocol-badge protocol-openai' : 'protocol-badge unknown'}>
          {protocolName(row.protocol_type, t)}
        </span>
        <span className="pf-arrow">→</span>
        <span className={protocolBadgeClass(algoType)} title={protocolBadgeTitle(algoType, t)}>
          {protocolBadgeText(algoType, t)}
        </span>
        {isConvert ? (
          <>
            <span className="pf-arrow">→</span>
            <span className="protocol-badge unknown">{t('chatAnalysis.target')}</span>
          </>
        ) : null}
        <span className="pf-label">{t('chatAnalysis.idWithHash', { id: row.id })}</span>
      </div>

      {/* KPI 指标网格 */}
      <div className="detail-head-grid">
        <div className="detail-head-card">
          <span className="dhc-label">⏱ {t('chatAnalysis.elapsed')}</span>
          <span className="dhc-value">{fmtMs(row.elapsed_ms)}</span>
        </div>
        <div className="detail-head-card">
          <span className="dhc-label">📥 {t('chatAnalysis.inputTokensCard')}</span>
          <span className="dhc-value">{fmtNum(row.tokens_input_size)}</span>
        </div>
        <div className="detail-head-card">
          <span className="dhc-label">📤 {t('chatAnalysis.outputTokensCard')}</span>
          <span className="dhc-value">{fmtNum(row.tokens_output_size)}</span>
        </div>
        <div className="detail-head-card">
          <span className="dhc-label">📦 {t('chatAnalysis.reqRespSize')}</span>
          <span className="dhc-value">{fmtBytes(row.request_content_length)} / {fmtBytes(row.response_content_length)}</span>
        </div>
      </div>

      {/* 请求信息行 */}
      <div className="detail-head-request">
        <span className="dhreq-method">{row.request_method}</span>
        <span className="dhreq-url" title={row.request_url}>{row.request_url}</span>
        <span className={`dhreq-status ${statusOk ? 'ok' : 'err'}`}>{row.response_status}</span>
        <span>{fmtTime(row.created_at)}</span>
      </div>
    </header>
  )
}
