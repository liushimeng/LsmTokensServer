import { useEffect, useMemo, useState } from 'react'
import { baseUrl } from '../shared/api'
import {
  NAV_TREES, findMenuEntry,
  loadCollapsedGroups, saveCollapsedGroups,
} from '../shared/navConfig'
import { useI18n } from '../i18n'

// 分级侧边菜单：一级分组可折叠（localStorage 按角色记忆），二级页面项激活高亮。
// 激活项所在组若被折叠则"临时展开"（不写回记忆），保证当前页始终可见。
// 阶段CA（侧边菜单折叠展开方案）：
// - mini 模式（56px）下分组图标 tooltip 显示分组名
// - 一级分组标题 hover 时背景高亮，点击整行可折叠
// - 与全局快捷键 ] 协同：] 触发"全部折叠/全部展开"切换（事件总线）
export default function SideNav({ role, route, collapsed, open, onClose, onExpand }) {
  const tree = NAV_TREES[role] || NAV_TREES.user
  const [collapsedGroups, setCollapsedGroups] = useState(() => loadCollapsedGroups(role))
  const { t } = useI18n()

  // 角色切换（登录信息加载完成）时重载该角色的折叠记忆
  useEffect(() => {
    setCollapsedGroups(loadCollapsedGroups(role))
  }, [role])

  // 阶段CA：监听"全部折叠/全部展开"快捷键事件（由 Layout.jsx 触发 \`]\`）
  useEffect(() => {
    const onToggleAll = () => {
      setCollapsedGroups((prev) => {
        // 全部已折叠 → 全部展开；否则 → 全部折叠
        const allCollapsed = tree.length > 0 && tree.every((g) => prev.has(g.id))
        const next = allCollapsed ? new Set() : new Set(tree.map((g) => g.id))
        saveCollapsedGroups(role, next)
        return next
      })
    }
    window.addEventListener('lsm:nav:toggleAllGroups', onToggleAll)
    return () => window.removeEventListener('lsm:nav:toggleAllGroups', onToggleAll)
  }, [role, tree])

  const active = findMenuEntry(role, route)

  const toggleGroup = (gid) => {
    setCollapsedGroups((prev) => {
      const next = new Set(prev)
      if (next.has(gid)) next.delete(gid)
      else next.add(gid)
      saveCollapsedGroups(role, next)
      return next
    })
  }

  const groups = useMemo(() => tree.map((g) => {
    const isActiveGroup = active && active.groupId === g.id
    // 临时展开：激活组即使被记忆为折叠，本次也展开（不修改 collapsedGroups）
    const isCollapsed = collapsedGroups.has(g.id) && !isActiveGroup
    return { ...g, isActiveGroup, isCollapsed }
  }), [tree, active, collapsedGroups])

  return (
    <nav className={
      'layout-nav' +
      (open ? ' open' : '') +
      (collapsed ? ' mini' : '')
    }>
      {collapsed && <img className="nav-mini-logo" src={baseUrl() + 'logo-32.png'} alt="L" />}
      {groups.map((g) => (
        <div key={g.id} className={'nav-group' + (g.isActiveGroup ? ' active-group' : '')}>
          {collapsed ? (
            // 图标栏模式：组图标点击展开侧栏并定位到该组
            <button
              className={'nav-group-mini' + (g.isActiveGroup ? ' active' : '')}
              title={t(g.label)}
              aria-label={t(g.label)}
              onClick={() => {
                // 若激活项不在该组，跳到该组第一个页（不强制整侧栏展开，让路由引导）
                if (!g.isActiveGroup) onExpand()
                // 通过事件总线通知路由跳转
                const firstKey = g.items && g.items[0] && g.items[0].key
                if (firstKey) window.location.hash = `#/${firstKey}`
              }}
            >{g.icon}</button>
          ) : (
            <>
              <button
                className={
                  'nav-group-title' +
                  (g.isActiveGroup ? ' active' : '') +
                  (g.isActiveGroup && g.isCollapsed ? ' has-active' : '')
                }
                onClick={() => toggleGroup(g.id)}
                title={g.isCollapsed ? '展开分组' : '折叠分组'}
                aria-expanded={!g.isCollapsed}
              >
                <span className="nav-group-icon">{g.icon}</span>
                <span className="nav-group-label">{t(g.label)}</span>
                {g.isActiveGroup && g.isCollapsed && <span className="nav-active-dot" />}
                <span className={'nav-arrow' + (g.isCollapsed ? '' : ' down')}>▸</span>
              </button>
              {!g.isCollapsed && (
                <div className="nav-items">
                  {g.items.map((it) => (
                    <a
                      key={it.key}
                      href={`#/${it.key}`}
                      className={'nav-item' + (active && active.itemKey === it.key ? ' active' : '')}
                      onClick={onClose}
                    >{t(it.label)}</a>
                  ))}
                </div>
              )}
            </>
          )}
        </div>
      ))}
    </nav>
  )
}
