import { useEffect, useState } from 'react'
import { post } from '../shared/api'
import { useI18n } from '../i18n'
import Modal from './Modal'

// 批量编辑路由弹窗：追加 / 删除 源站（逐条处理 + 算法策略可选）
// props:
//   open: boolean
//   onClose: () => void
//   routes: array - 已选路由列表
//   onSuccess: () => void - 操作成功后回调（刷新列表）
export default function BatchEditModal({ open, onClose, routes, onSuccess }) {
  const { t } = useI18n()
  const [tab, setTab] = useState('append') // 'append' | 'remove'
  const [availableEndpoints, setAvailableEndpoints] = useState([])
  const [loadingEndpoints, setLoadingEndpoints] = useState(false)
  const [selectedEpIds, setSelectedEpIds] = useState([])
  const [algoStrategy, setAlgoStrategy] = useState(0) // 0=不修改 1=指定型 2=稳定型 3=经济型
  const [processing, setProcessing] = useState(false)
  const [result, setResult] = useState(null) // {success_count, skip_count, fail_count, details}

  // 弹窗打开时加载可选源站列表
  useEffect(() => {
    if (!open || !routes.length) {
      setAvailableEndpoints([])
      setSelectedEpIds([])
      setResult(null)
      setAlgoStrategy(0)
      return
    }
    let cancelled = false
    setLoadingEndpoints(true)
    setResult(null)
    // 从已选路由的所属用户获取源站列表（合并去重）
    const userIds = [...new Set(routes.map((r) => r.user_id).filter(Boolean))]
    Promise.all(userIds.map((uid) => post('AIRouteManageInterface', { action: 'list_endpoints', user_id: uid })))
      .then((results) => {
        if (cancelled) return
        const epMap = new Map()
        results.forEach((d) => {
          const list = (d && d.data) || []
          list.forEach((ep) => {
            if (!epMap.has(ep.id)) epMap.set(ep.id, ep)
          })
        })
        const eps = [...epMap.values()]
          .filter((ep) => ep.status == 1) // eslint-disable-line eqeqeq
          .sort((a, b) => ((a.platform_name || '') + (a.model_name || '')).localeCompare((b.platform_name || '') + (b.model_name || ''), 'zh-Hans-CN'))
        setAvailableEndpoints(eps)
      })
      .catch(() => { if (!cancelled) setAvailableEndpoints([]) })
      .finally(() => { if (!cancelled) setLoadingEndpoints(false) })
    return () => { cancelled = true }
  }, [open, routes])

  if (!open) return null

  const protocolName = (v) => (parseInt(v, 10) === 1 ? 'Anthropic' : 'OpenAI')

  const toggleEndpoint = (epId) => {
    setSelectedEpIds((prev) => prev.includes(epId) ? prev.filter((id) => id !== epId) : [...prev, epId])
  }

  const handleExecute = async () => {
    if (!selectedEpIds.length) { alert(t('aiRouteManage.noEndpointsSelected')); return }
    const action = tab === 'append' ? 'batch_append_endpoints' : 'batch_remove_endpoints'
    const confirmMsg = tab === 'append'
      ? t('aiRouteManage.confirmBatchAppend', { count: routes.length, epCount: selectedEpIds.length })
      : t('aiRouteManage.confirmBatchRemove', { count: routes.length, epCount: selectedEpIds.length })
    if (!confirm(confirmMsg)) return
    setProcessing(true)
    setResult(null)
    try {
      const d = await post('AIRouteManageInterface', {
        action,
        ids: routes.map((r) => r.id),
        endpoint_ids: selectedEpIds,
        algorithm_strategy_type: algoStrategy,
      })
      if (d && d.data) {
        setResult(d.data)
        if (d.data.success_count > 0) {
          // 操作成功 → 通知父组件刷新
          setTimeout(() => { onSuccess && onSuccess() }, 600)
        }
      } else if (d && !d.success) {
        alert(d.message || '操作失败')
      }
    } catch (e) {
      alert(e.message || '操作失败')
    } finally {
      setProcessing(false)
    }
  }

  const handleClose = () => {
    if (processing) return
    setResult(null)
    setSelectedEpIds([])
    setAlgoStrategy(0)
    onClose()
  }

  const selectedEpObjects = availableEndpoints.filter((ep) => selectedEpIds.includes(ep.id))

  return (
    <Modal
      title={t('aiRouteManage.batchEditRoute')}
      width={760}
      onClose={handleClose}
      footer={
        <>
          <button className="btn" onClick={handleClose} disabled={processing}>{t('aiRouteManage.cancel')}</button>
          <button className="btn btn-primary" onClick={handleExecute} disabled={processing || !selectedEpIds.length}>
            {processing ? t('aiRouteManage.processing') : t('aiRouteManage.confirmExecute')}
          </button>
        </>
      }
    >
      {/* 已选路由预览 */}
      <div style={{ marginBottom: 12 }}>
        <div style={{ fontWeight: 600, marginBottom: 6 }}>{t('aiRouteManage.routePreview')} ({t('aiRouteManage.selectedRoutes', { count: routes.length })})</div>
        <div style={{ maxHeight: 100, overflowY: 'auto', border: '1px solid #eee', borderRadius: 4, padding: 8, background: '#fafafa', fontSize: 12 }}>
          {routes.map((r) => (
            <div key={r.id} style={{ padding: '2px 0' }}>
              <span style={{ color: '#666' }}>#{r.id}</span> {r.user_name} / {r.model_name} · {protocolName(r.protocol_type)}
            </div>
          ))}
        </div>
      </div>

      {/* Tab 切换 */}
      <div style={{ display: 'flex', gap: 0, marginBottom: 12, borderBottom: '1px solid #ddd' }}>
        <button className="btn" style={{ borderRadius: '4px 4px 0 0', borderBottom: tab === 'append' ? '2px solid #0066cc' : 'none', fontWeight: tab === 'append' ? 600 : 400 }} onClick={() => setTab('append')}>
          {t('aiRouteManage.appendEndpointsTab')}
        </button>
        <button className="btn" style={{ borderRadius: '4px 4px 0 0', borderBottom: tab === 'remove' ? '2px solid #0066cc' : 'none', fontWeight: tab === 'remove' ? 600 : 400 }} onClick={() => setTab('remove')}>
          {t('aiRouteManage.removeEndpointsTab')}
        </button>
      </div>

      {/* 源站多选 */}
      <div style={{ marginBottom: 12 }}>
        <div style={{ fontWeight: 600, marginBottom: 6 }}>{t('aiRouteManage.selectEndpointsPlaceholder')}</div>
        {loadingEndpoints ? (
          <div style={{ color: '#999', fontSize: 13 }}>{t('aiRouteManage.loadingEndpoints')}</div>
        ) : availableEndpoints.length === 0 ? (
          <div style={{ color: '#999', fontSize: 13 }}>{t('aiRouteManage.noAvailableEndpoints')}</div>
        ) : (
          <select multiple size={6} style={{ width: '100%', border: '1px solid #ddd', borderRadius: 4, padding: 4 }}
            value={selectedEpIds.map(String)} onChange={(e) => {
              const opts = [...e.target.selectedOptions]
              setSelectedEpIds(opts.map((o) => parseInt(o.value, 10)))
            }}>
            {availableEndpoints.map((ep) => (
              <option key={ep.id} value={ep.id}>
                {ep.platform_name} / {ep.model_name} [{protocolName(ep.protocol_type)}]
              </option>
            ))}
          </select>
        )}
        {selectedEpObjects.length > 0 && (
          <div style={{ marginTop: 8, display: 'flex', flexWrap: 'wrap', gap: 6 }}>
            {selectedEpObjects.map((ep) => (
              <span key={ep.id} className="ep-chip" style={{ display: 'inline-flex', alignItems: 'center', gap: 4 }}>
                {ep.platform_name} / {ep.model_name}
                <button className="btn btn-sm" style={{ padding: '0 4px', lineHeight: 1.2 }} onClick={() => toggleEndpoint(ep.id)} disabled={processing}>✕</button>
              </span>
            ))}
          </div>
        )}
      </div>

      {/* 算法策略（可选） */}
      <div style={{ marginBottom: 12 }}>
        <div style={{ fontWeight: 600, marginBottom: 6 }}>{t('aiRouteManage.algoStrategyOptional')}</div>
        <div style={{ display: 'flex', gap: 12, fontSize: 13 }}>
          <label><input type="radio" checked={algoStrategy === 0} onChange={() => setAlgoStrategy(0)} disabled={processing} /> {t('aiRouteManage.algoNoChange')}</label>
          <label><input type="radio" checked={algoStrategy === 1} onChange={() => setAlgoStrategy(1)} disabled={processing} /> {t('aiRouteManage.algoDesignated')}</label>
          <label><input type="radio" checked={algoStrategy === 2} onChange={() => setAlgoStrategy(2)} disabled={processing} /> {t('aiRouteManage.algoStable')}</label>
          <label><input type="radio" checked={algoStrategy === 3} onChange={() => setAlgoStrategy(3)} disabled={processing} /> {t('aiRouteManage.algoEconomic')}</label>
        </div>
      </div>

      {/* 操作结果 */}
      {result && (
        <div style={{ marginTop: 12, padding: 10, background: '#f0f7ff', borderRadius: 6, fontSize: 13 }}>
          <div style={{ fontWeight: 600, marginBottom: 6 }}>{t('aiRouteManage.batchResult')}</div>
          <div style={{ display: 'flex', gap: 12, marginBottom: 6 }}>
            <span style={{ color: '#155724' }}>✓ {t('aiRouteManage.batchSuccess', { success: result.success_count })}</span>
            {result.skip_count > 0 && <span style={{ color: '#856404' }}>⏭ {t('aiRouteManage.batchSkip', { skip: result.skip_count })}</span>}
            {result.fail_count > 0 && <span style={{ color: '#721c24' }}>✗ {t('aiRouteManage.batchFail', { fail: result.fail_count })}</span>}
          </div>
          {result.details && result.details.length > 0 && (
            <div style={{ maxHeight: 120, overflowY: 'auto', border: '1px solid #ddd', borderRadius: 4, padding: 6, background: '#fff', fontSize: 12 }}>
              {result.details.map((d) => (
                <div key={d.route_id} style={{ padding: '2px 0' }}>
                  <span style={{ color: '#666' }}>#{d.route_id}</span>
                  {d.status === 'success' && <span style={{ color: '#155724', marginLeft: 6 }}>{t('aiRouteManage.routeStatusSuccess')}</span>}
                  {d.status === 'skip' && <span style={{ color: '#856404', marginLeft: 6 }}>{t('aiRouteManage.routeStatusSkip')}{d.reason ? ` (${d.reason})` : ''}</span>}
                  {d.status === 'fail' && <span style={{ color: '#721c24', marginLeft: 6 }}>{t('aiRouteManage.routeStatusFail')}{d.reason ? ` (${d.reason})` : ''}</span>}
                </div>
              ))}
            </div>
          )}
        </div>
      )}
    </Modal>
  )
}
