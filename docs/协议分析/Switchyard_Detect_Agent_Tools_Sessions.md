# Switchyard Agent 工具检测与 Session 识别机制分析

> **分析日期**: 2026-08-13
> **源码版本**: Switchyard (NVIDIA 开源, Apache-2.0)
> **源码路径**: `/usr/local/LsmGitOpenSource/Switchyard`

---

## 1. 架构概览

Switchyard 的 Agent 检测和 Session 识别采用**完全不同的设计哲学**——**不做 User-Agent 字符串解析**，而是通过 **HTTP 头归一化（Header Normalization）** 实现 Agent 身份识别和会话追踪。

核心设计原则：
- **Harness-Agnostic（工具无关）**：没有 `AgentType` 枚举，不判断"你是 Claude Code 还是 Codex"
- **Header-First**：所有身份信息来自 HTTP 请求头，不解析请求体
- **Precedence Chain（优先级链）**：每个字段有多个候选头，按优先级逐一查找，首个命中即返回
- **Switchyard Override（原生覆盖）**：`x-switchyard-*` 头始终最高优先级，允许运维显式覆盖

---

## 2. 核心数据结构：`Metadata`

**文件**: `crates/protocol/src/metadata.rs` (lines 158-194)

```rust
pub struct Metadata {
    pub session_id: Option<String>,       // 稳定的多请求会话标识
    pub agent_id: Option<String>,         // 发起请求的 Agent ID
    pub parent_agent_id: Option<String>,  // 父 Agent ID（子 Agent 场景）
    pub is_subagent: bool,                // 是否来自子 Agent
    pub is_delegated_work: bool,          // 是否携带委托的子 Agent 工作
    pub agent_kind: Option<String>,       // Harness 定义的 Agent 类型（如 "collab_spawn", "review"）
    pub agent_role: Option<String>,       // 语义角色（"explorer", "worker", "reviewer"）
    pub task_id: Option<String>,          // 请求所属任务 ID
    pub task_kind: Option<String>,        // 语义任务类别
    pub turn_id: Option<String>,          // 当前 Agent 轮次 ID
    pub session_final: Option<bool>,      // 是否为会话最后一个请求
    pub correlation_id: Option<String>,   // 外部追踪/请求 ID
    pub extra_metadata: Option<BTreeMap<String, String>>,  // 自定义元数据
    pub http_headers: Option<http::HeaderMap>,              // 原始 HTTP 头
    pub wire_format: Option<WireFormat>,                    // 请求/响应编码格式
}
```

---

## 3. HTTP 头归一化：`HEADER_CONFIG`

**文件**: `crates/protocol/src/metadata.rs` (lines 77-149)

Switchyard 定义了一个**头配置表**，为每个元数据字段指定候选头列表（按优先级排列）。`Metadata::from_headers()` 按此表逐一查找，首个非空命中即返回。

### 3.1 各字段候选头优先级

#### Session ID（`x-switchyard-session-id`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-session-id` | Switchyard 原生覆盖 |
| 2 | `x-claude-code-session-id` | Claude Code |
| 3 | `x-nemo-relay-session-id` | Nemo Relay |
| 4 | `x-session-id` | OpenCode（仅 session_id 关联，非路由信号） |
| 5 | `x-codex-turn-metadata.session_id` | Codex CLI（嵌套 JSON 头，点路径解析） |
| 6 | `session-id` | 通用 Codex 兼容 |

#### Agent ID（`x-switchyard-agent-id`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-agent-id` | Switchyard 原生覆盖 |
| 2 | `x-claude-code-agent-id` | Claude Code |
| 3 | `x-nemo-relay-subagent-id` | Nemo Relay |
| 4 | `x-dynamo-session-id` | Dynamo |
| 5 | `x-codex-turn-metadata.thread_id` | Codex CLI（嵌套 JSON） |
| 6 | `thread-id` | 通用兼容 |

#### Parent Agent ID（`x-switchyard-parent-agent-id`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-parent-agent-id` | Switchyard 原生覆盖 |
| 2 | `x-dynamo-parent-session-id` | Dynamo |
| 3 | `x-codex-turn-metadata.parent_thread_id` | Codex CLI（嵌套 JSON） |
| 4 | `x-codex-parent-thread-id` | Codex 兼容 |

#### Agent Kind（`x-switchyard-agent-kind`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-agent-kind` | Switchyard 原生覆盖 |
| 2 | `x-codex-turn-metadata.subagent_kind` | Codex CLI（嵌套 JSON） |
| 3 | `x-openai-subagent` | OpenAI |

#### Agent Role（`x-switchyard-agent-role`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-agent-role` | Switchyard 原生覆盖 |
| 2 | `x-codex-turn-metadata.agent_role` | Codex CLI（嵌套 JSON） |

#### Task ID（`x-switchyard-task-id`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-task-id` | Switchyard 原生覆盖 |
| 2 | `x-codex-turn-metadata.task_id` | Codex CLI（嵌套 JSON） |
| 3 | `x-task-id` | 通用 |

#### Task Kind（`x-switchyard-task-kind`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-task-kind` | Switchyard 原生覆盖 |
| 2 | `x-codex-turn-metadata.task_kind` | Codex CLI（嵌套 JSON） |

#### Turn ID（`x-switchyard-turn-id`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-turn-id` | Switchyard 原生覆盖 |
| 2 | `x-codex-turn-metadata.turn_id` | Codex CLI（嵌套 JSON） |

#### Request ID（`x-switchyard-request-id`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-request-id` | Switchyard 原生覆盖 |
| 2 | `x-request-id` | 通用 |
| 3 | `x-client-request-id` | 通用 |

#### Session Final（`x-switchyard-session-final`）
| 优先级 | 头名称 | 来源 Agent |
|--------|--------|-----------|
| 1 | `x-switchyard-session-final` | Switchyard 原生覆盖 |
| 2 | `x-dynamo-session-final` | Dynamo |

### 3.2 嵌套 JSON 路径解析

Codex CLI 将多个元数据字段打包在一个 `x-codex-turn-metadata` JSON 头中。Switchyard 使用**点路径**（dotted path）寻址：

```
x-codex-turn-metadata.session_id
x-codex-turn-metadata.thread_id
x-codex-turn-metadata.parent_thread_id
x-codex-turn-metadata.turn_id
x-codex-turn-metadata.subagent_kind
x-codex-turn-metadata.agent_role
x-codex-turn-metadata.task_id
x-codex-turn-metadata.task_kind
```

`resolve_path()` 函数解析 JSON 值并沿点路径逐级下降提取目标字段。

---

## 4. Sub-Agent 检测逻辑

**文件**: `crates/protocol/src/metadata.rs` (lines 239-271)

### 4.1 三重检测信号

```rust
fn parse_sub_agent(headers: &HeaderMap) -> (Option<String>, bool, bool) {
    // 信号 1：显式覆盖
    let explicit = header(headers, "x-switchyard-is-subagent").and_then(parse_bool);

    // 信号 2：Claude Code 世系（任何非空 agent_id = 子 Agent）
    let (claude_parent, claude_subagent) = claude_lineage(headers);

    // 信号 3：Codex/OpenAI 子 Agent 标记
    let harness_kind = resolve_path(headers, "x-codex-turn-metadata.subagent_kind")
        .or_else(|| header(headers, "x-openai-subagent"));

    let is_subagent = explicit.unwrap_or(claude_subagent || harness_kind.is_some());
    // ...
}
```

### 4.2 Sub-Agent 判定规则

| 信号源 | 判定条件 | 说明 |
|--------|---------|------|
| `x-switchyard-is-subagent` | 布尔值 true/false | 显式覆盖，最高优先级 |
| `x-claude-code-agent-id` | 任何非空值 | Claude Code 的根 Agent 不发送此头，所以非空=子 Agent |
| `x-codex-turn-metadata.subagent_kind` | 存在即为子 Agent | Codex CLI 子 Agent 标记 |
| `x-openai-subagent` | 存在即为子 Agent | OpenAI 子 Agent 标记 |

### 4.3 委托工作判定

并非所有子 Agent 都携带委托工作。Switchyard 区分"子 Agent 身份"和"委托工作"：

```rust
const SUBAGENT_WORK_KINDS: &[&str] = &["collab_spawn", "review"];
```

- `collab_spawn`（协作生成）和 `review`（审查）= 委托工作 → 路由到子 Agent 目标
- `compact`（上下文压缩）、`memory_consolidation`（内存整理）等 = 子 Agent 身份但非委托工作

---

## 5. 路由身份与会话追踪

**文件**: `crates/libsy/src/core/algorithm.rs` (lines 330-363)

```rust
pub(crate) enum RoutingIdentity {
    Session(String),                              // 根 Agent：按 session_id 路由
    Subagent { session: String, agent: String },  // 子 Agent：按 session + agent 路由
}
```

### 5.1 用途

- **Session Affinity Routing**（`AffinityRouter`）：将 session 钉到特定模型
- **Context Window Overflow Tracking**（`SessionEvictions`）：记录哪些目标溢出
- **Sub-Agent Routing Override**（`SubagentOverride`）：将委托工作路由到固定的 worker 目标

---

## 6. 工具级行为检测（Tool-Based Behavioral Detection）

**文件**: `crates/libsy/src/algorithms/util/tool_signals.rs`

Switchyard 不通过请求头识别 Agent 类型，而是分析**对话中的工具调用模式**来推断 Agent 正在做什么（编写代码、运行测试、读取文件等）。这用于 `StageRouter` 的路由决策。

### 6.1 工具名称分类

#### 编辑工具（Edit Tools）
```rust
static EDIT_TOOL_NAMES: &[&str] = &[
    "edit",           // Claude Code
    "multiedit",      // Claude Code
    "notebookedit",   // Claude Code
    "str_replace",    // 通用
    "str_replace_based_edit_tool",
    "text_editor",
    "patch",          // Hermes 的 str_replace 风格编辑工具
];
```

#### 写入工具（Write Tools）
```rust
static WRITE_TOOL_NAMES: &[&str] = &["write", "create_file", "new_file", "write_file"];
```

#### 读取工具（Read Tools）
```rust
static READ_TOOL_NAMES: &[&str] = &["read", "view", "read_file", "search_files"];
```

#### 计划工具（Plan Tools）
```rust
static PLAN_TOOL_NAMES: &[&str] = &[
    "todowrite",       // Claude Code
    "todo_write",
    "todo",
    "update_plan",     // Codex
];
```

#### Bash/Shell 工具
```rust
static BASH_TOOL_NAMES: &[&str] = &[
    "bash",             // Claude Code
    "shell_command",    // Codex
    "shell",            // OpenAI 衍生
    "local_shell_call", // OpenAI 衍生
    "terminal",         // Hermes
];
```

### 6.2 Bash 子命令模式匹配

对 Bash 类工具，Switchyard 进一步分析命令文本以判定意图：

**写入模式**（`BASH_WRITE_PATTERNS`）：
- `cat >`, `cat >>`, `echo >`, `echo >>`, `tee `, `printf >`, `printf >>`
- `> /`, `>> /`, `<< 'eof'`, `<<eof`, `<<'eof'`, `<< eof`

**编辑模式**（`BASH_EDIT_PATTERNS`）：
- `sed -i`, `sed --in-place`, `awk -i inplace`, `awk 'inplace=1'`
- `patch `, `patch -p`, `perl -i`, `perl -p -i`, `perl -pi`

**读取模式**（`BASH_READ_PATTERNS`）：
- `cat /`, `cat ./`, `cat ../`, `grep `, `ls `, `find `, `head `, `tail `
- `wc `, `diff `, `which `, `ps `, `df `, `du `, `stat `, `file `, `less `, `more `

### 6.3 测试检测

Switchyard 检测测试通过/失败模式：

**通过短语**（`TEST_PASS_PHRASES`）：
- `" passed"`, `"passed in"`, `"tests passed"`, `"all tests passed"`
- `"test ok"`, `"test result: ok"`, `"passed.\n"`, `"tests pass"`
- `"\nok "`（go test 输出）, `"✓ "`

**失败短语**（`TEST_FAILURE_LITERAL`）：
- `"✗ "`, `"fatal:"`, `"assertionerror"`, `"error:"`

### 6.4 ToolSignals 数据结构

```rust
pub struct ToolSignals {
    pub severity: f32,           // 最近窗口内最高严重度 (0.0-1.0)
    pub no_error_streak: u32,    // 连续无错工具结果数
    pub edit_count: u32,         // 编辑类工具调用总数
    pub write_count: u32,        // 写入类工具调用总数
    pub read_count: u32,         // 读取类工具调用总数
    pub todowrite_count: u32,    // 计划类工具调用总数
    pub recent_edit_count: u32,  // 最近窗口内编辑数
    pub recent_write_count: u32, // 最近窗口内写入数
    pub recent_read_count: u32,  // 最近窗口内读取数
    pub recent_todowrite_count: u32,
    pub pure_bash_streak: u32,   // 连续纯 Bash 调用数
    pub tests_passed: bool,      // 最近 3 个工具结果中有测试通过
    pub turn_depth: u32,         // 消息数代理的轮次深度
    pub compacted: bool,         // 是否携带上下文压缩摘要
}
```

---

## 7. 启动器基础设施（Python）

Switchyard 提供三个 Python 启动器来配置各 Agent 的路由环境：

| 启动器文件 | 目标 Agent | 配置机制 |
|-----------|-----------|---------|
| `switchyard/cli/launchers/claude_code_launcher.py` | Claude Code | 设置 `ANTHROPIC_BASE_URL` / `ANTHROPIC_MODEL` 环境变量 |
| `switchyard/cli/launchers/codex_cli_launcher.py` | Codex CLI | 使用 `-c model_provider=switchyard` CLI 参数 |
| `switchyard/cli/launchers/openclaw_launcher.py` | OpenClaw | 写入 `openclaw.json` 工作区配置 |

**注意**：启动器**不注入 Agent 识别头**——只配置 Agent 经过 Switchyard 路由，Agent 自身会发送其原生识别头。

---

## 8. 可观测性

### 8.1 Tracing Span

**文件**: `crates/libsy/src/observability.rs`

`libsy.run` span 记录以下元数据字段：
- `session_id` / `session.id`
- `agent_id`
- `task_id`
- `correlation_id`
- `extra_metadata`

### 8.2 路由日志

**文件**: `crates/switchyard-server/src/routing_log.rs`

使用 `proxy_x_session_id` 头关联路由决策，支持 `GET /v1/routing/session-stats` 查询每个 session 的路由快照。

---

## 9. 与 LsmTokensServer 的对比总结

| 维度 | Switchyard | LsmTokensServer |
|------|-----------|-------------|
| **Agent 识别方式** | HTTP 头归一化（不解析 UA） | User-Agent 字符串解析 |
| **Agent 类型枚举** | 无（harness-agnostic） | 无显式枚举，用字符串匹配 |
| **Session 识别** | 从 HTTP 头提取（header-first） | 从请求体提取（body-first） + 头兜底 |
| **Sub-Agent 检测** | 三重信号（显式/Claude/Codex） | 无 |
| **工具行为分析** | 工具名分类 + Bash 命令模式匹配 | 仅提取工具名列表入库 |
| **测试结果检测** | 通过/失败短语匹配 | 无 |
| **覆盖 Agent 数** | 7+ 生态（Claude/Codex/OpenAI/OpenCode/Nemo/Dynamo/Switchyard） | 6 个（claude-cli/OpenAI-JS/OpenAI-Python/opencode/kilo-code/OpenClaw） |
| **覆盖头数量** | 30+ 个候选头 | ~5 个（User-Agent + 3 个 Anthropic 自定义头 + X-Client-Name） |

### 关键差异

1. **Switchyard 不看 User-Agent**：它依赖各 Agent 原生发送的专属头（如 `x-claude-code-session-id`、`x-codex-turn-metadata`）。LsmTokensServer 反过来，主要依赖 User-Agent 字符串解析。

2. **Switchyard 的 Session 识别是 header-first**：所有 session 信息从 HTTP 头提取。LsmTokensServer 是 body-first：先解析请求体中的 `metadata.user_id`、`client_metadata` 等字段，头只是兜底。

3. **Switchyard 有 Sub-Agent 维度**：能区分根 Agent 和子 Agent，路由策略不同。LsmTokensServer 没有此维度。

4. **Switchyard 有工具行为分析**：不仅记录用了哪些工具，还分类工具行为（编辑/写入/读取/计划/Bash）并检测测试结果。LsmTokensServer 仅提取工具名列表。

5. **Switchyard 用嵌套 JSON 头**：Codex CLI 的 `x-codex-turn-metadata` 是一个 JSON 对象，Switchyard 用点路径解析内部字段。这是一种更结构化的方式，避免了为每个字段单独发一个头。
