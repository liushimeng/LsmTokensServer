# Anthropic 协议请求体结构分析

> 数据来源：`TAgentHttpTransactionDataItem` 哈希分表（protocol_type = 1）
> 分析样本：20 条真实请求记录，提取最复杂结构作为协议转换参考
> 生成日期：2026-06-14
> 用途：协议转换分析器（ProtocolConvertAnalyzer）字段映射与转换规则参考

---

## 1. 顶层字段概览

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 目标模型名称（如 `kimi-for-coding`） |
| `max_tokens` | integer | 否 | 最大输出 token 数（如 32000） |
| `messages` | array | 是 | 对话消息列表 |
| `system` | array/string | 否 | 系统提示（Anthropic 特有，支持数组形式） |
| `tools` | array | 否 | 工具定义列表 |
| `metadata` | object | 否 | 元数据（如 user_id 等） |
| `output_config` | object | 否 | 输出配置（如 effort 级别） |
| `stream` | boolean | 否 | 是否流式输出 |

---

## 2. Messages 结构详解

Anthropic 的 `messages` 数组中，每个消息对象包含 `role` 和 `content`：

### 2.1 Role 取值

- `user` — 用户输入
- `assistant` — 助手回复
- `system` — 系统提示（Anthropic 支持，但官方推荐用顶层 `system` 字段）

### 2.2 Content 格式

Content 可以是**字符串**或**内容块数组**（`content_blocks`）。数组形式支持多模态：

#### text 块
```json
{
  "type": "text",
  "text": "用户输入的文本内容..."
}
```

#### tool_use 块（助手调用工具）
```json
{
  "type": "tool_use",
  "id": "tool_zLJG5ePAdanM6LVZRIIA7YpA",
  "name": "TaskCreate",
  "input": {
    "subject": "任务标题",
    "description": "任务描述...",
    "activeForm": "任务进行时的显示文本"
  }
}
```

**字段说明：**
- `id` — 工具调用唯一标识（用于后续 tool_result 关联）
- `name` — 工具名称（对应 tools 数组中的定义）
- `input` — 工具输入参数（JSON 对象，结构由工具的 input_schema 定义）

#### tool_result 块（工具执行结果）
```json
{
  "tool_use_id": "tool_zLJG5ePAdanM6LVZRIIA7YpA",
  "type": "tool_result",
  "content": "工具执行返回的文本内容..."
}
```

**字段说明：**
- `tool_use_id` — 对应 tool_use 的 `id`，用于关联
- `content` — 工具执行结果（字符串或数组）

#### cache_control 块（提示缓存）
```json
{
  "type": "text",
  "text": "需要缓存的文本内容...",
  "cache_control": {
    "type": "ephemeral"
  }
}
```

**字段说明：**
- `cache_control.type` — 目前仅支持 `ephemeral`
- 可附加在 `text`、`tool_use`、`tool_result` 块上

---

## 3. System 字段结构

Anthropic 支持两种 system 格式：

### 字符串形式（简单场景）
```json
"system": "You are a helpful assistant."
```

### 数组形式（复杂场景，支持缓存控制）
```json
"system": [
  {
    "type": "text",
    "text": "x-anthropic-billing-header: cc_version=2.1.159.0fe; cc_entrypoint=cli; cch=9b31d;"
  },
  {
    "type": "text",
    "text": "You are Claude Code, Anthropic's official CLI for Claude.",
    "cache_control": {
      "type": "ephemeral"
    }
  }
]
```

---

## 4. Tools 结构详解

Anthropic 的工具定义采用 `name` + `description` + `input_schema` 模式：

```json
{
  "name": "Agent",
  "description": "Launch a new agent to handle complex, multi-step tasks...",
  "input_schema": {
    "$schema": "https://json-schema.org/draft-2020-12/schema",
    "type": "object",
    "properties": {
      "description": {
        "description": "A short (3-5 word) description of the task",
        "type": "string"
      },
      "prompt": {
        "description": "The task for the agent to perform",
        "type": "string"
      },
      "subagent_type": {
        "description": "The type of specialized agent to use for this task",
        "type": "string"
      },
      "model": {
        "type": "string",
        "enum": ["sonnet", "opus", "haiku"]
      },
      "run_in_background": {
        "type": "boolean"
      },
      "isolation": {
        "type": "string",
        "enum": ["worktree"]
      }
    },
    "required": ["description", "prompt"],
    "additionalProperties": false
  }
}
```

**字段说明：**
- `name` — 工具名称（唯一标识，用于 tool_use 的 `name` 字段）
- `description` — 工具功能描述（LLM 据此判断何时调用）
- `input_schema` — 输入参数 JSON Schema（定义 `input` 对象的结构）
- `required` — 必填参数列表
- `additionalProperties` — 是否允许额外属性

---

## 5. Metadata 结构

```json
"metadata": {
  "user_id": "{\"device_id\":\"...\",\"account_uuid\":\"\",\"session_id\":\"...\"}"
}
```

---

## 6. Output Config 结构

```json
"output_config": {
  "effort": "high"
}
```

- `effort` 取值：`low` | `medium` | `high`

---

## 7. 完整复杂示例（精简版）

```json
{
  "model": "kimi-for-coding",
  "max_tokens": 32000,
  "messages": [
    {
      "role": "user",
      "content": [
        {
          "type": "text",
          "text": "用户问题..."
        }
      ]
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "助手思考..."
        },
        {
          "type": "tool_use",
          "id": "tool_xxx",
          "name": "Bash",
          "input": {
            "command": "ls -la",
            "description": "List files in current directory"
          }
        }
      ]
    },
    {
      "role": "user",
      "content": [
        {
          "tool_use_id": "tool_xxx",
          "type": "tool_result",
          "content": "total 68\ndrwxrwxr-x  ..."
        }
      ]
    },
    {
      "role": "assistant",
      "content": [
        {
          "type": "text",
          "text": "根据目录内容...",
          "cache_control": {
            "type": "ephemeral"
          }
        }
      ]
    }
  ],
  "system": [
    {
      "type": "text",
      "text": "You are Claude Code, Anthropic's official CLI for Claude.",
      "cache_control": {
        "type": "ephemeral"
      }
    }
  ],
  "tools": [
    {
      "name": "Bash",
      "description": "Executes a bash command...",
      "input_schema": {
        "type": "object",
        "properties": {
          "command": { "type": "string" },
          "description": { "type": "string" }
        },
        "required": ["command"]
      }
    }
  ],
  "metadata": {
    "user_id": "..."
  },
  "output_config": {
    "effort": "high"
  },
  "stream": true
}
```

---

## 8. 与 OpenAI 协议的关键差异（转换要点）

| 维度 | Anthropic | OpenAI |
|------|-----------|--------|
| **System 提示** | 顶层 `system` 字段（数组/字符串） | `messages` 中 `role: "system"` |
| **工具调用** | `tool_use` / `tool_result` 内容块 | `tool_calls` / `role: "tool"` 消息 |
| **工具定义** | `name` + `description` + `input_schema` | `type: "function"` + `function.name` + `function.parameters` |
| **工具调用 ID** | `tool_use.id` / `tool_result.tool_use_id` | `tool_calls[].id` / `tool.tool_call_id` |
| **工具输入** | `tool_use.input` | `tool_calls[].function.arguments`（JSON 字符串） |
| **消息角色** | `user` / `assistant`（含 system 在顶层） | `system` / `user` / `assistant` / `tool` |
| **缓存控制** | `cache_control: { type: "ephemeral" }` | 不支持（OpenAI 无对应机制） |
| **输出配置** | `output_config.effort` | 无直接对应 |
| **元数据** | `metadata.user_id` | 无直接对应 |

---

## 9. 协议转换映射表

### 9.1 Anthropic → OpenAI 字段映射

| Anthropic 字段 | OpenAI 对应字段 | 转换规则 |
|----------------|-----------------|----------|
| `model` | `model` | 直接透传 |
| `max_tokens` | `max_completion_tokens` | 字段名转换 |
| `messages` | `messages` | 需要转换内容块格式 |
| `system` (顶层) | `messages[0]` with `role: "system"` | 数组/字符串转消息 |
| `tools` | `tools` | 结构转换：name→function.name，input_schema→parameters |
| `tool_use` 块 | `tool_calls` | 内容块转消息字段 |
| `tool_result` 块 | `role: "tool"` 消息 | 内容块转独立消息 |
| `stream` | `stream` | 直接透传 |
| `metadata` | — | 丢弃或放 custom metadata |
| `output_config` | — | 丢弃 |
| `cache_control` | — | 丢弃 |

### 9.2 消息内容块转换规则

**Anthropic `assistant` + `tool_use` → OpenAI `assistant` + `tool_calls`：**

```
Anthropic:
{
  "role": "assistant",
  "content": [
    { "type": "text", "text": "让我执行命令" },
    { "type": "tool_use", "id": "tool_xxx", "name": "Bash", "input": { "command": "ls" } }
  ]
}

OpenAI:
{
  "role": "assistant",
  "content": "让我执行命令",
  "tool_calls": [
    {
      "id": "tool_xxx",
      "type": "function",
      "function": { "name": "Bash", "arguments": "{\"command\":\"ls\"}" }
    }
  ]
}
```

**Anthropic `tool_result` → OpenAI `role: "tool"`：**

```
Anthropic:
{
  "role": "user",
  "content": [
    { "tool_use_id": "tool_xxx", "type": "tool_result", "content": "执行结果..." }
  ]
}

OpenAI:
{
  "role": "tool",
  "content": "执行结果...",
  "tool_call_id": "tool_xxx"
}
```

---

## 10. 工具定义转换规则

**Anthropic → OpenAI：**

```
Anthropic:
{
  "name": "Bash",
  "description": "执行 bash 命令",
  "input_schema": {
    "type": "object",
    "properties": { "command": { "type": "string" } },
    "required": ["command"]
  }
}

OpenAI:
{
  "type": "function",
  "function": {
    "name": "Bash",
    "description": "执行 bash 命令",
    "parameters": {
      "type": "object",
      "properties": { "command": { "type": "string" } },
      "required": ["command"]
    }
  }
}
```

**关键转换点：**
1. 添加 `type: "function"` 包装层
2. `input_schema` → `function.parameters`
3. `name` 移到 `function.name`
4. `description` 移到 `function.description`

---

*本文档基于真实数据库请求数据生成，用于协议转换分析器的字段映射参考。*
