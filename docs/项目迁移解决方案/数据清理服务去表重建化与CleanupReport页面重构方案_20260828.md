# 数据清理服务去表重建化与 CleanupReport 页面重构方案

> 日期：2026-08-28
> 范围：后端清理服务（`models/mysql_http_agent_cleanup.go`）、后端 API（`api/server_api_manager_cleanup_report.go`）、前端页面（`pages/CleanupReport.jsx`）
> 版本：v2.0.x（阶段 AS 之后）

---

## 一、背景与目标

### 1.1 背景

当前 LsmTokensServer 的数据清理服务在每天分批删除过期记录后，会执行 `ALTER TABLE ... ENGINE=InnoDB`（即 MySQL 表重建 / OPTIMIZE TABLE）来将磁盘空间归还给操作系统。这套机制存在以下问题：

1. **重建风险高**：大表（几十 GB 级别）重建期间，磁盘上同时存在新旧两份 .ibd 文件，容易写满磁盘拖垮 MySQL（生产实证：133GB 的分表触发重建必然失败）
2. **锁表影响业务**：ALTER TABLE 即使是 online DDL，也会在收尾阶段持有元数据锁，阻塞线上写入
3. **必要性不足**：分表有 ID 自增主键，每天凌晨定时删除过期记录已能稳定控制表行数，InnoDB 内部空闲链表会复用已删除行的空间，表不会无限膨胀
4. **运维复杂度高**：代码里充斥着磁盘预检、表大小上限、重建失败降级、磁盘安全余量等防御性逻辑，维护成本高

### 1.2 目标

| 维度 | 目标 | 验收标准 |
|------|------|----------|
| 后端简化 | 彻底删除表重建（ALTER TABLE ENGINE=InnoDB）功能及所有相关防御代码 | `releaseTableSpace` 等 6 个函数不再存在 |
| 清理效果 | 数据清理仅保留「分批硬删除过期记录」 | 每日定时删除正常运行，status 语义清晰 |
| 页面重构 | CleanupReport 从「空间回收视角」转为「数据保留 + 容量监控视角」 | 不再展示 freed_bytes / 释放空间相关指标 |
| 垃圾清理 | 一次性清理本项目业务表中的历史垃圾数据 | 提供 SQL 清单，只动本项目表 |
| 向后兼容 | 历史报告中的 freed_bytes 不丢失，新版本不再写入 | 旧数据仍可查询，新数据 freed_bytes=0 |

---

## 二、后端修改详情

### 2.1 删除的代码（`models/mysql_http_agent_cleanup.go`）

#### A. 删除的常量

| 常量名 | 值 | 删除原因 |
|--------|-----|----------|
| `rebuildDiskSafetyMarginBytes` | 5GB | 重建磁盘安全余量，不再需要 |
| `maxAutoRebuildTableBytes` | 20GB | 自动重建单表大小上限，不再需要 |

#### B. 删除的变量

| 变量名 | 说明 |
|--------|------|
| `errSkipRebuildLowDisk` | 磁盘不足跳过重建的哨兵错误 |

#### C. 删除的函数

| 函数名 | 说明 |
|--------|------|
| `releaseTableSpace()` | ALTER TABLE ENGINE=InnoDB 重建表 + 估算释放字节数 |
| `checkDiskSpaceForRebuild()` | 重建前磁盘空间预检（表大小 + 可用磁盘 + 安全余量判断） |
| `getMySQLDataDir()` | 查询 MySQL `@@datadir` 路径（仅重建使用） |
| `getTableSizeBytes()` | 查询 information_schema 表大小（仅重建磁盘预检使用） |
| `getAvailableDiskBytes()` | `syscall.Statfs` 探测可用磁盘（仅重建使用） |
| `queryTableDataFree()` | 查询 `DATA_FREE`（仅用于估算释放空间） |

#### D. 删除的 import

- `"os"` — 确认仅被 `getCleanupRunHour` / `getCleanupBatchSize` 使用（保留，因为这两个函数还用）
- `"syscall"` — 仅被 `getAvailableDiskBytes` 使用（删除）

### 2.2 简化的函数

#### `cleanupOneSubTable()`（核心简化）

**原 4 步流程：**
1. Step 1+2: `scanAndDeleteExpired()` 边扫边删
2. Step 3: `releaseTableSpace()` ALTER TABLE 重建释放空间
3. Step 4: 构建报告

**新 2 步流程：**
1. Step 1: `scanAndDeleteExpired()` 边扫边删（保留）
2. Step 2: 构建报告并返回（`FreedBytes` 恒为 0）

**状态语义简化：**
- `success`：全部删除完成
- `partial`：仅因 ctx 超时部分完成（不再有"磁盘不足跳过重建"的 partial）
- `failed`：扫描/删除出错

### 2.3 聚合结构体调整

#### `CleanupReportsTotalSummary`

- ❌ 删除 `TotalFreedBytes int64` 字段
- `GetCleanupReportsTotalSummary()` 的 SQL 中移除 `SUM(freed_bytes)`

#### `CleanupReportsDailySummary`

- ❌ 删除 `FreedBytes int64` 字段
- `GetCleanupReportsDailySummary()` 的 SQL 中移除 `SUM(freed_bytes)`

### 2.4 保留但注释更新

#### `TAgentHttpTransactionCleanupReport.FreedBytes`（`models/mysql_http_agent_model.go`）

- **字段保留**：数据库列不删（AutoMigrate 不会自动删列，保持现状最安全；历史数据有值）
- **写入停止**：`saveCleanupReport()` 的 `DoUpdates` 列表中移除 `"freed_bytes"`，新报告不更新该列
- **注释更新**：标注「历史遗留字段，v2.0.x 起不再写入，恒为 0」

#### `SubTableInspectorInfo.DataFree`

- **保留**：这是 information_schema 的只读元数据，与重建功能无关
- 对运维仍有价值（了解表内有多少可复用空间）
- 前端不直接展示该字段

### 2.5 无需改动的部分

- `cleanupServiceState` 结构体 + `GetCleanupStateSnapshot()` — 状态字段与重建无关
- `scanAndDeleteExpired()` — 分页删除逻辑，完全保留
- `GetSubTableInspector()` — 分表元数据快照，完全保留
- `EnsureCleanupCreatedAtIndex()` — 清理扫描索引，完全保留
- 自动重跑机制（`cleanupAutoRerunDelay` / `cleanupAutoRerunMaxAttempts`）— 与重建无关，应对瞬时连接故障，保留

---

## 三、前端 CleanupReport 页面重构

### 3.1 设计理念转变

| 旧页面（空间回收视角） | 新页面（数据保留 + 容量监控视角） |
|----------------------|--------------------------------|
| 关注"释放了多少磁盘空间" | 关注"保留了多少数据、清理效果如何" |
| 核心指标：freed_bytes | 核心指标：deleted_rows、tokens、保留策略合规性 |
| 状态复杂：success/partial/failed + 磁盘不足原因 | 状态简化：success/partial/failed（partial 仅 ctx 超时） |

### 3.2 页面结构（从上到下）

```
┌─────────────────────────────────────────────────────────────┐
│  数据清理报告                              [时间跨度▾] [刷新] │
│  ● 运行中 / 上次执行 2026-08-28 03:30  保留配置：60 天       │
├─────────────────────────────────────────────────────────────┤
│  ┌────────────┐ ┌────────────┐ ┌────────────┐ ┌────────────┐ │
│  │ 累计删除条数 │ │ 累计回收Tokens│ │ 数据覆盖范围 │ │ 当前保留期 │ │
│  │  1,234,567  │ │  56,789,012 │ │   58 天    │ │   60 天   │ │
│  │ 所有任务累计  │ │  输入+输出  │ │ 最早~最新  │ │ 自动删除   │ │
│  └────────────┘ └────────────┘ └────────────┘ └────────────┘ │
│                                                             │
│  【卡片 3：数据覆盖范围 — 新增】                              │
│  - 展示 earliest ~ latest 的天数差                            │
│  - 颜色：绿色（正常）/ 黄色（轻度积压）/ 红色（清理异常）     │
│  - 数据来源：state.earliest_transaction_at / latest          │
├─────────────────────────────────────────────────────────────┤
│  每日清理趋势图                                               │
│  - 柱状图：deleted_rows（主指标）                             │
│  - tooltip：日期 + 删除行数 + Tokens（移除 freed）           │
├─────────────────────────────────────────────────────────────┤
│  分表容量监控（原"分表统计"改名）                              │
│  - 行数、数据大小、索引大小、时间范围                         │
│  - 精确计数按钮保留                                           │
│  - data_free 字段后端仍返回，前端不展示                       │
├─────────────────────────────────────────────────────────────┤
│  清理明细列表（DataTable）                                    │
│  - 删除列：释放空间（freed_bytes）                            │
│  - 保留列：清理日期、分表索引、分表名、删除条数、             │
│            输入/输出/总 Tokens、耗时、保留天数、              │
│            截止时间、状态、错误信息                           │
│  - 状态标签：移除"已删除·未重建"（磁盘不足）标签              │
└─────────────────────────────────────────────────────────────┘
```

### 3.3 KPI 卡片调整

| 序号 | 旧卡片 | 新卡片 | 说明 |
|------|--------|--------|------|
| 1 | 累计删除条数 | 累计删除条数 | 不变 |
| 2 | **累计释放磁盘空间** | **累计回收 Tokens** | 位置调整（从第 3 提到第 2） |
| 3 | 累计回收 Tokens | **数据覆盖范围** | 新卡片，展示实际数据跨度 |
| 4 | 当前保留天数配置 | 当前保留天数配置 | 不变 |

### 3.4 新增"数据覆盖范围"卡片详情

- **主数值**：`N 天`（latest - earliest 的天数差）
- **副标题**：`YYYY-MM-DD ~ YYYY-MM-DD`（最早 ~ 最新记录日期）
- **状态颜色**：
  - 🟢 绿色：覆盖天数 <= 保留天数 + 1（清理正常）
  - 🟡 黄色：覆盖天数 > 保留天数 + 1 且 <= 保留天数 + 7（轻度积压）
  - 🔴 红色：覆盖天数 > 保留天数 + 7（清理异常）
- **数据来源**：`state` 接口的 `earliest_transaction_at` / `latest_transaction_at`（已存在，无需后端新增字段）

### 3.5 i18n 翻译调整

#### 删除的键

| i18n key | 用途 |
|----------|------|
| `cleanup.totalFreedSpace` | KPI 卡片标题 |
| `cleanup.freedSpace` | 列表列名 |
| `cleanup.fromDataFree` | KPI 卡片副标题 |
| `cleanup.deletedNotRebuilt` | "已删除·未重建"状态标签 |
| `cleanup.diskSpaceLow` | 磁盘空间不足判断文本 |

#### 新增的键

| i18n key | 中文示例 | 用途 |
|----------|---------|------|
| `cleanup.dataCoverage` | 数据覆盖范围 | 新 KPI 卡片标题 |
| `cleanup.dataCoverageDesc` | 当前最早~最新记录跨度 | 新 KPI 卡片副标题 |
| `cleanup.coverageNormal` | 正常 | 覆盖天数符合预期 |
| `cleanup.coverageBacklog` | 轻度积压 | 超过保留天数 1~7 天 |
| `cleanup.coverageAbnormal` | 清理异常 | 超过保留天数 7 天以上 |
| `cleanup.capacityMonitor` | 分表容量监控 | 分表统计卡片标题 |
| `cleanup.totalDataSize` | 总数据大小 | 汇总统计用 |

#### 调整的键

| i18n key | 调整内容 |
|----------|---------|
| `cleanup.dailyBarDetail` | 字符串模板移除 freed 参数 |

---

## 四、垃圾数据一次性清理 SQL

### 4.1 本项目数据库表清单（确认范围）

| 表名 | 类型 | 用途 | 清理策略 |
|------|------|------|----------|
| `TAgentHttpTransactionDataItem_00` ~ `_07` | 分表（8 张） | HTTP 代理交易记录 | 已有定时清理，不动 |
| `TAgentHttpTransactionCleanupReport` | 单表 | 清理报告 | 保留（每年 ~2920 行，可忽略） |
| `TAgentHttpUserInfo` | 单表 | 用户信息 | 核心业务表，不动 |
| `TAgentHttpUserModelInfo` | 单表 | 用户-模型关联 | 核心业务表，不动 |
| `TAgentDstEndPoint` | 单表 | 源站端点 | 核心业务表，不动 |
| `TAgentHttpAIRoute` | 单表 | AI 路由配置 | 核心业务表，不动 |
| `TAgentModelInfo` | 单表 | 模型信息 | 核心业务表，不动 |
| `TAgentHttpAgentInfo` | 单表 | Agent 工具信息 | 核心业务表，不动 |
| `TAgentUserOperationLog` | 单表 | 用户操作日志 | 已有容量控制（10 万条上限），不动 |
| `t_spider_data_sources` | 单表 | 爬虫数据源配置 | 配置表，不动 |
| `t_spider_daily_info` | 单表 | 爬虫每日信息 | ✅ 清理空记录 |

**中间件表确认**：项目无 Redis / Session 表 / Cache 表 / Migration 表等中间件专用表。所有中间件状态（限流、nonce、缓存、爬虫 session）均为内存态，服务重启即清空。

### 4.2 清理 SQL

```sql
-- =====================================================
-- 清理 1：爬虫每日信息表中的空记录
-- 说明：标题和内容均为空的无效爬取数据
-- =====================================================
-- 先查看有多少空记录
SELECT COUNT(*) AS empty_count
FROM t_spider_daily_info
WHERE (title IS NULL OR TRIM(title) = '')
  AND (content IS NULL OR TRIM(content) = '');

-- 执行清理（分批删除，每批 1000 条，避免大事务）
DELETE FROM t_spider_daily_info
WHERE (title IS NULL OR TRIM(title) = '')
  AND (content IS NULL OR TRIM(content) = '')
LIMIT 1000;
-- 重复执行直到影响行数为 0
```

> 注：项目中已有 `CleanupEmptySpiderDailyInfos()` 函数（需 `LSM_CLEANUP_EMPTY_DAILY_INFO=1` 环境变量启动时执行），但这是首次手动清理历史累积空数据的一次性操作。

---

## 五、实施步骤

### 阶段 1：方案文档生成
- 输出 `docs/项目迁移解决方案/数据清理服务去表重建化与CleanupReport页面重构方案_20260828.md`

### 阶段 2：后端核心修改
- 修改 `models/mysql_http_agent_cleanup.go`
  - 删除 6 个重建相关函数
  - 删除 2 个常量 + 1 个变量
  - 简化 `cleanupOneSubTable()`（删除 Step 3）
  - `saveCleanupReport()` 的 `DoUpdates` 移除 `freed_bytes`
  - 调整两个聚合结构体（删除 `FreedBytes` / `TotalFreedBytes`）
  - 移除 `"syscall"` import
  - 更新所有相关注释

### 阶段 3：后端模型 & 测试更新
- `models/mysql_http_agent_model.go`：`FreedBytes` 字段注释更新
- `api/server_api_manager_cleanup_report.go`：注释更新
- 测试文件调整：移除重建相关断言和测试用例
- 运行 `go build ./...` 和 `go test ./...`

### 阶段 4：前端页面重构
- `pages/CleanupReport.jsx`
  - KPI 卡片：移除「累计释放空间」，新增「数据覆盖范围」
  - 趋势图 tooltip：移除 freed_bytes
  - 分表卡片：标题改为「分表容量监控」
  - 明细列表：移除 `freed_bytes` 列
  - 状态标签：移除「已删除·未重建」分支
- i18n 三语言文件同步更新

### 阶段 5：一次性垃圾数据清理
- 读取 `LsmTokensServer.conf` 获取数据库连接信息
- 执行 `t_spider_daily_info` 空记录清理 SQL
- 输出清理结果（删除行数）

### 阶段 6：编译验证 & 提交
- `./rebuild_restart_app.sh --build-only` 完整编译
- `go test ./...` 全量测试
- git 中文 commit 提交

---

## 六、验证与验收标准

### 6.1 功能验证

| 验证项 | 预期结果 |
|--------|---------|
| `go build ./...` | 编译通过 |
| `go test ./...` | 全量测试通过 |
| `npm run build` | 双构建（manager + user）均成功 |
| CleanupReport 页面加载 | 无 JS 错误，所有卡片正常渲染 |
| 清理服务运行 | 日志中无 ALTER TABLE / OPTIMIZE 字样 |
| 新报告 freed_bytes | 恒为 0 |
| 历史报告查询 | 旧数据的 freed_bytes 仍有值（向后兼容） |

### 6.2 安全验证

| 验证项 | 预期结果 |
|--------|---------|
| 表范围 | 只清理了本项目 11 类表中的 1 张（t_spider_daily_info） |
| DDL 操作 | 无 DROP / TRUNCATE / ALTER 操作 |
| 核心业务数据 | 用户、模型、路由、交易记录完整无损 |

---

## 七、向后兼容说明

1. **数据库层面**：`freed_bytes` 列保留，历史数据不变；新数据不写入该列
2. **API 层面**：每行报告仍返回 `freed_bytes` 字段（零值）；聚合 API（total_summary / daily_summaries）不再返回 freed_bytes 聚合字段；减少字段对前端安全
3. **状态语义**：`partial` 只有一种成因（ctx 超时部分完成），不再有"磁盘不足跳过重建"的 partial
4. **前端层面**：新版本移除释放空间相关展示；旧版前端访问新版后端，freed_bytes 显示为 0，不报错
