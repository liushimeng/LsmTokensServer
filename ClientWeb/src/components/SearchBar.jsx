// ClientWeb/src/components/SearchBar.jsx
//
// 查找栏组件：关键词输入 + 上一个/下一个遍历 + 匹配计数 + 关闭。
// 2026-08-27 对话分析记录查看器升级新增，通用可复用。
//
// 键盘：Enter = 下一个，Shift+Enter = 上一个，Esc = 关闭。

import { useEffect, useRef } from 'react'
import { useI18n } from '../i18n'

/**
 * @param {object} props
 * @param {string} props.query          当前关键词（受控）
 * @param {(q:string)=>void} props.onQueryChange
 * @param {number} props.activeIndex    当前匹配序号（0 基）
 * @param {number} props.matchCount     匹配总数
 * @param {()=>void} props.onPrev
 * @param {()=>void} props.onNext
 * @param {()=>void} props.onClose
 */
export default function SearchBar({ query, onQueryChange, activeIndex, matchCount, onPrev, onNext, onClose }) {
  const { t } = useI18n()
  const inputRef = useRef(null)

  // 打开时自动聚焦
  useEffect(() => { inputRef.current && inputRef.current.focus() }, [])

  const onKeyDown = (e) => {
    if (e.key === 'Enter') {
      e.preventDefault()
      if (e.shiftKey) { onPrev() } else { onNext() }
    } else if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      onClose()
    }
  }

  const hasQuery = (query || '').length > 0
  const countText = !hasQuery
    ? ''
    : matchCount === 0
      ? t('chatAnalysis.noMatch')
      : `${activeIndex + 1}/${matchCount}`

  return (
    <div className="detail-search-bar">
      <span className="dsb-icon">🔍</span>
      <input
        ref={inputRef}
        className="dsb-input"
        type="text"
        value={query}
        placeholder={t('chatAnalysis.searchPlaceholder')}
        onChange={(e) => onQueryChange(e.target.value)}
        onKeyDown={onKeyDown}
      />
      <span className={`dsb-count${hasQuery && matchCount === 0 ? ' dsb-nomatch' : ''}`}>{countText}</span>
      <button type="button" className="btn btn-sm" disabled={!hasQuery || matchCount === 0}
              title={t('chatAnalysis.prevMatch')} onClick={onPrev}>↑</button>
      <button type="button" className="btn btn-sm" disabled={!hasQuery || matchCount === 0}
              title={t('chatAnalysis.nextMatch')} onClick={onNext}>↓</button>
      <button type="button" className="btn btn-sm" title={t('common.close')} onClick={onClose}>✕</button>
    </div>
  )
}
