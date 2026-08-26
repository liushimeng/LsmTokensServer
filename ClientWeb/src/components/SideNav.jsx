import { useEffect, useMemo, useState } from 'react'
import { baseUrl } from '../shared/api'
import {
  NAV_TREES, findMenuEntry,
  loadCollapsedGroups, saveCollapsedGroups,
} from '../shared/navConfig'
import { useI18n } from '../i18n'

// 分级侧边菜单：一级分组可折叠（localStorage 按角色记忆），二级页面项激活高亮。
// 激活项所在组若被折叠则"临时展开"（不写回记忆），保证当前页始终可见。
export default function SideNav({ role, route, collapsed, open, onClose, onExpand }) {
  const tree = NAV_TREES[role] || NAV_TREES.user
  const [collapsedGroups, setCollapsedGroups] = useState(() => loadCollapsedGroups(role))
  const { t } = useI18n()

  // 角色切换（登录信息加载完成）时重载该角色的折叠记忆
  useEffect(() => {
    setCollapsedGroups(loadCollapsedGroups(role))
  }, [role])

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
      {collapsed && <img className="nav-mini-logo" src={baseUrl() + "logo-32.png"} alt="L" />}
      {groups.map((g) => (
        <div key={g.id} className="nav-group">
          {collapsed ? (
            // 图标栏模式：组图标点击展开侧栏并定位到该组
            <button
              className={'nav-group-mini' + (g.isActiveGroup ? ' active' : '')}
              title={t(g.label)}
              onClick={onExpand}
            >{g.icon}</button>
          ) : (
            <>
              <button
                className={'nav-group-title' + (g.isActiveGroup && g.isCollapsed ? ' has-active' : '')}
                onClick={() => toggleGroup(g.id)}
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
