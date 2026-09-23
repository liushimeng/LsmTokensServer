// 详情头部组件：协议流向 + KPI 卡片 + 请求信息行
// v2.0.7x 阶段CN：KPI 网格新增第 5 张「IP 地址」卡（数据源交易表字段
//   TAgentHttpTransactionDataItem.RequestRemoteAddr / JSON request_remote_addr，
//   列表接口白名单 selectTransactionColumns() 已包含该列，后端零改动），
//   与「耗时 / 输入 Tokens / 输出 Tokens / 请求·响应大小」同行展示：
//   主值 = host（等宽字体，IPv6 过长自动折行不截断），副行 = 端口，
//   title 悬浮展示落库原始值（形如 10.0.0.5:54321 / [2408:8207::1]:443）便于排查与复制。
import { fmtTime, fmtNum, fmtBytes, fmtMs } from '../../shared/format'
import { fmtRemoteAddr } from '../../shared/remoteAddr'
import { protocolName, protocolBadgeClass, protocolBadgeText, protocolBadgeTitle, ALGO_TYPE_CONVERTER } from './constants'
import { useI18n } from '../../i18n'

export default function DetailHeader({ row }) {
  const { t } = useI18n()
  if (!row) return null

  const algoType = row.dst_endpoint_algorithm_type
  const isConvert = algoType === ALGO_TYPE_CONVERTER
  const statusOk = String(row.response_status).startsWith('2')
  const remoteAddr = fmtRemoteAddr(row.request_remote_addr)

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
        {/* 阶段CN：客户端 IP 地址（request_remote_addr），与上面 4 项同一行栅格 */}
        <div className="detail-head-card">
          <span className="dhc-label">🌐 {t('chatAnalysis.clientIp')}</span>
          <span className="dhc-value dhc-value-mono dhc-value-wrap" title={row.request_remote_addr || remoteAddr.display}>
            {remoteAddr.host || remoteAddr.display}
          </span>
          {remoteAddr.port ? (
            <span className="dhc-sub dhc-sub-mono">{t('chatAnalysis.clientPort', { port: remoteAddr.port })}</span>
          ) : null}
        </div>
      </div>

      {/* 请求信息行 */}
      <div className="detail-head-request">
        <span className="dhreq-method">{row.request_method}</span>
        <span className="dhreq-url" title={row.request_url}>{row.request_url}</span>
        <span className={`dhreq-status ${statusOk ? 'ok' : 'err'}`}>{row.response_status}</span>
        <span>{fmtTime(row.created_at)}</span>
      </div>

      {/* v2.0.7x 阶段BG：Agent 工具定义块 —— 展示三字段（agent_tool_name / request_tools / agent_tool_session_id），
          其中「Agent工具定义」项使用请求体解析出的工具列表 request_tools 字段（不再使用 agent_tool_info / tool_identifier 字段），
          「Agent工具定义」独占一行完整展示（逗号分隔长列表自动多行换行，不截断），
          三字段全空时整块不渲染，避免无意义空块干扰阅读；详情头部仅展示 Agent 工具原生识别值
          （不展示合成 session_id 的归一化逻辑，由列表列负责）。 */}
      {(row.agent_tool_name || row.request_tools || row.agent_tool_session_id) ? (
        <div className="detail-head-agent">
          <span className="dha-title">🤖 {t('chatAnalysis.agentToolBlock')}</span>
          {row.agent_tool_name ? (
            <span className="dha-item" title={row.agent_tool_name}>
              <span className="dha-label">{t('chatAnalysis.agentTool')}：</span>
              <span className="dha-value">{row.agent_tool_name}</span>
            </span>
          ) : null}
          {row.request_tools ? (
            <span className="dha-item dha-item-full" title={row.request_tools}>
              <span className="dha-label">{t('chatAnalysis.agentToolInfo')}：</span>
              <span className="dha-value">{row.request_tools}</span>
            </span>
          ) : null}
          {row.agent_tool_session_id ? (
            <span className="dha-item" title={row.agent_tool_session_id}>
              <span className="dha-label">{t('chatAnalysis.agentSessionId')}：</span>
              <span className="dha-value">{row.agent_tool_session_id}</span>
            </span>
          ) : null}
        </div>
      ) : null}
    </header>
  )
}
