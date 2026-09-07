// 阶段BV：ModelInfo 页面筛选工具栏
// 管理端：用户名 + 模型名（联动）+ 时间档位 + 查询
// 用户端：模型名 + 时间档位 + 查询（userName 取自 JWT claims）
import TimeRangeSelector from '../../components/TimeRangeSelector'
import { modelNamesOf } from '../../shared/userModelOptions'
import { useI18n } from '../../i18n'

export default function ModelInfoToolbar({
  isAdmin,
  userName, setUserName,
  modelName, setModelName,
  days, setDays,
  levels, levelsLoading,
  onQuery, loading,
  userOptions, myModelNames,
}) {
  const { t } = useI18n()

  // 切换用户名时清空模型名（与对话分析联动语义一致）
  const handleUserChange = (v) => {
    setUserName(v)
    setModelName('')
  }

  return (
    <div className="toolbar">
      {isAdmin ? (
        <label>{t('modelInfo.userNameLabel')}
          <select value={userName} onChange={(e) => handleUserChange(e.target.value)} style={{ width: 140 }}>
            <option value="">{t('modelInfo.selectUser')}</option>
            {(userOptions || []).map((u) => (
              <option key={u.user_name} value={u.user_name}>{u.user_name}</option>
            ))}
          </select>
        </label>
      ) : null}
      <label>{t('modelInfo.modelNameLabel')}
        <select value={modelName} onChange={(e) => setModelName(e.target.value)} style={{ width: 170 }}>
          <option value="">{t('modelInfo.selectModel')}</option>
          {(isAdmin ? modelNamesOf(userOptions, userName) : (myModelNames || [])).map((m) => (
            <option key={m} value={m}>{m}</option>
          ))}
        </select>
      </label>
      <label>{t('modelInfo.timeSpan')}
        <TimeRangeSelector span={days ?? 3} onChange={setDays} levels={levels} loading={levelsLoading} />
      </label>
      <button
        className="btn btn-primary"
        onClick={onQuery}
        disabled={loading}
      >{loading ? t('modelInfo.loading') : t('modelInfo.refresh')}</button>
      <span style={{ color: '#888', fontSize: 13 }}>
        {userName && modelName ? t('modelInfo.scopedToUserModel', { user: userName, model: modelName })
          : modelName ? t('modelInfo.scopedToModel', { model: modelName })
          : isAdmin ? t('modelInfo.adminStats') : t('modelInfo.userStats')}
      </span>
    </div>
  )
}
