import { useI18n } from '../i18n'

// src/components/OrderButtons.jsx
//
// 通用「列表顺序调整」按钮组：置顶 / ↑ 上移 / ↓ 下移 / 置底。
// 语义约定：列表下标即优先级，0 = 首位 = 最高优先级（智能路由「目标源站列表」中
// 同时是主源站 dst_endpoint_id）。失败自动切换时后端把第 0 个滚动到末尾。
//
// 本组件只负责渲染与给出「目标下标」，实际搬移由调用方配合
// shared/listOrder.js 的 moveItem/pinToTop 完成 —— 保证搬移逻辑可单测。
//
// props:
//   index: number        当前行下标
//   total: number        列表长度（决定 disabled 状态）
//   onMove: (toIndex) => void  目标下标：0 / index-1 / index+1 / total-1
//   disabled?: boolean   整组禁用（保存中、只读态）
//   showPinBottom?: boolean    关闭「置底」（默认 true）
export default function OrderButtons({ index, total, onMove, disabled = false, showPinBottom = true }) {
  const { t } = useI18n()
  const count = Number.isFinite(total) ? Math.max(0, Math.trunc(total)) : 0
  const i = Number.isInteger(index) ? index : -1
  // 少于 2 条、或下标已失效（列表在异步中变短）→ 整组禁用，位置保持不跳动
  const reorderable = count > 1 && i >= 0 && i < count
  const first = i === 0
  const last = i === count - 1

  const btn = (target, label, hint, off) => (
    <button type="button" className="btn btn-sm order-btn"
      title={hint} aria-label={hint}
      disabled={disabled || !reorderable || off}
      onClick={() => onMove && onMove(target)}>
      {label}
    </button>
  )

  return (
    <span className="order-btn-group" role="group" aria-label={t('common.orderOps')}>
      {btn(0, t('common.pinTop'), t('common.pinTopHint'), first)}
      {btn(Math.max(0, i - 1), '↑', t('common.moveUp'), first)}
      {btn(Math.min(count - 1, i + 1), '↓', t('common.moveDown'), last)}
      {showPinBottom ? btn(count - 1, t('common.pinBottom'), t('common.pinBottomHint'), last) : null}
    </span>
  )
}
