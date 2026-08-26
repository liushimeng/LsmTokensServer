# I18N 国际化开发指南

> LsmTokensServer 前端国际化（i18n）开发参考手册

---

## 1. 快速使用

### 1.1 在组件中使用翻译

```jsx
import { useI18n } from '../i18n'

function MyComponent() {
  const { t } = useI18n()
  
  return (
    <div>
      <h1>{t('home.title')}</h1>
      <button>{t('common.save')}</button>
    </div>
  )
}
```

### 1.2 带插值的翻译

```jsx
// 翻译文件: { "total": "共 {count} 条" }
t('datatable.total', { count: 42 })  // → "共 42 条"
```

### 1.3 切换语言

```jsx
const { setLocale } = useI18n()
setLocale('en')  // 切换到 English
```

### 1.4 区域感知格式化

```jsx
import { fmtNum, fmtTime } from '../i18n/format'

// 根据当前 i18n locale 自动选择 Intl 区域
fmtNum(12345.67)   // zh-CN → "12,345.67"  en → "12,345.67"  ja → "12,345.67"
fmtTime(1710000000) // 根据区域格式化日期
```

---

## 2. 翻译键命名规范

### 2.1 命名格式

```
{namespace}.{key}
```

- **namespace**：页面/组件名（小驼峰）
- **key**：具体文案标识（小驼峰）

### 2.2 命名空间清单

| 命名空间 | 用途 |
|----------|------|
| `common` | 通用（按钮、状态、提示） |
| `nav` | 侧边栏导航 |
| `login` | 用户登录 |
| `managerLogin` | 管理员登录 |
| `home` | 首页 |
| `userManage` | 用户管理 |
| `chatAnalysis` | 对话分析 |
| `aiRouteManage` | 智能路由 |
| `modelInfo` | 模型统计 |
| `agentInfo` | Agent 统计 |
| `protocolConvert` | 协议转换 |
| `spider` | 爬虫相关 |
| `cleanup` | 数据清理 |
| `dstEndPoint` | 目标端点 |
| `toolbar` | 工具栏弹窗 |
| `datatable` | 数据表格 |
| `errors` | 错误提示 |

---

## 3. 添加新翻译

### 3.1 步骤

1. 在 `src/i18n/locales/zh-CN.json` 中添加源语言键值
2. 在 `src/i18n/locales/en.json` 中添加英文翻译
3. 在 `src/i18n/locales/ja.json` 中添加日文翻译
4. 在组件中使用 `t('namespace.key')` 引用

### 3.2 示例

```json
// zh-CN.json
{
  "home": {
    "welcome": "欢迎回来",
    "stats": {
      "totalRequests": "总请求数",
      "tokenUsage": "Token 使用量"
    }
  }
}
```

```json
// en.json
{
  "home": {
    "welcome": "Welcome back",
    "stats": {
      "totalRequests": "Total Requests",
      "tokenUsage": "Token Usage"
    }
  }
}
```

```json
// ja.json
{
  "home": {
    "welcome": "おかえりなさい",
    "stats": {
      "totalRequests": "総リクエスト数",
      "tokenUsage": "トークン使用量"
    }
  }
}
```

---

## 4. 文件结构

```
src/i18n/
├── index.js                 # 统一导出
├── I18nContext.jsx          # Provider + Context
├── useI18n.js               # Hook
├── interpolate.js           # 插值引擎
├── format.js                # 区域格式化
├── LanguageSwitcher.jsx     # 切换器组件
└── locales/
    ├── zh-CN.json           # 简体中文
    ├── en.json              # English
    └── ja.json              # 日本語
```

---

## 5. 区域映射

| i18n locale | Intl locale | 日期格式 | 数字格式 |
|-------------|-------------|----------|----------|
| `zh-CN` | `zh-CN` | 2026/8/26 | 1,234.56 |
| `en` | `en-US` | 8/26/2026 | 1,234.56 |
| `ja` | `ja-JP` | 2026/8/26 | 1,234.56 |

---

## 6. 注意事项

- 所有用户可见文案必须通过 `t()` 翻译，禁止硬编码
- 翻译键使用点分命名（`namespace.key`），不使用嵌套对象
- 插值变量使用 `{varName}` 语法
- 新增页面时同步创建对应命名空间的翻译条目
- 翻译文件修改后需重新构建才能生效
