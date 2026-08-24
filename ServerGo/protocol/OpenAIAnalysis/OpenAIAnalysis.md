# OpenAI 协议请求体结构分析

> 数据来源：`TAgentHttpTransactionDataItem` 哈希分表（protocol_type = 2）
> 分析样本：20 条真实请求记录，提取最复杂结构作为协议转换参考
> 生成日期：2026-06-14
> 用途：协议转换分析器（ProtocolConvertAnalyzer）字段映射与转换规则参考

---

## 1. 顶层字段概览

| 字段 | 类型 | 必填 | 说明 |
|------|------|------|------|
| `model` | string | 是 | 目标模型名称（如 `doubao-seed-2.0-code`） |
| `messages` | array | 是 | 对话消息列表 |
| `stream` | boolean | 否 | 是否流式输出 |
| `stream_options` | object | 否 | 流式选项 |
| `max_completion_tokens` | integer | 否 | 最大完成 token 数 |
| `tools` | array | 否 | 工具定义列表 |
| `tool_choice` | string/object | 否 | 工具选择策略 |

---

## 2. Messages 结构详解

OpenAI 的 `messages` 数组中，每个消息对象包含 `role` 和 `content`：

### 2.1 Role 取值

- `system` — 系统提示
- `user` — 用户输入
- `assistant` — 助手回复
- `tool` — 工具执行结果

### 2.2 Content 格式

Content 可以是**字符串**或**内容块数组**（多模态支持）。

#### 字符串形式（简单文本）
```json
{
  "role": "system",
  "content": "You are a helpful assistant."
}
```

#### 数组形式（多模态/结构化内容）
```json
{
  "role": "user",
  "content": [
    {
      "type": "text",
      "text": "用户输入的文本内容..."
    }
  ]
}
```

**支持的 content 块类型：**
- `type: "text"` — 文本内容
- `type: "image_url"` — 图片 URL（`image_url: { url: "...", detail: "auto" }`）
- `type: "image"` — 图片数据（base64）

### 2.3 Assistant 消息 with tool_calls

```json
{
  "role": "assistant",
  "content": "Let's start by exploring the current workspace...",
  "tool_calls": [
    {
      "id": "callsljc792mcunzjxejr9x6ki2i",
      "type": "function",
      "function": {
        "name": "exec",
        "arguments": "{\"command\":\"ls -la\",\"workdir\":\"/home/aicon/.openclaw/workspace\"}"
      }
    }
  ]
}
```

**字段说明：**
- `content` — 助手文本回复（可为 null 或空字符串）
- `tool_calls` — 工具调用列表
  - `id` — 工具调用唯一标识
  - `type` — 固定为 `"function"`
  - `function.name` — 工具名称
  - `function.arguments` — 工具参数（**JSON 字符串**，不是对象）

### 2.4 Tool 消息（工具执行结果）

```json
{
  "role": "tool",
  "content": "total 68\ndrwxrwxr-x  7 aicon aicon 4096 May 31 14:03 .\n...",
  "tool_call_id": "callsljc792mcunzjxejr9x6ki2i"
}
```

**字段说明：**
- `role` — 固定为 `"tool"`
- `content` — 工具执行结果（字符串）
- `tool_call_id` — 对应 `tool_calls[].id`，用于关联

---

## 3. Tools 结构详解

OpenAI 的工具定义采用 `type: "function"` 包装：

```json
{
  "type": "function",
  "function": {
    "name": "agents_list",
    "description": "List agent ids allowed for `sessions_spawn runtime=\"subagent\"`.",
    "parameters": {
      "type": "object",
      "properties": {}
    }
  }
}
```

**字段说明：**
- `type` — 固定为 `"function"`
- `function.name` — 工具名称（唯一标识）
- `function.description` — 工具功能描述
- `function.parameters` — 输入参数 JSON Schema（`type: "object"` + `properties` + `required`）

### 复杂工具定义示例

```json
{
  "type": "function",
  "function": {
    "name": "browser",
    "description": "Control the browser via OpenClaw's browser control server...",
    "parameters": {
      "type": "object",
      "required": ["action"],
      "properties": {
        "action": {
          "type": "string",
          "enum": [
            "doctor", "status", "start", "stop", "profiles", "tabs",
            "open", "focus", "close", "snapshot", "screenshot", "navigate",
            "console", "pdf", "upload", "dialog", "act"
          ]
        },
        "target": {
          "type": "string",
          "enum": ["sandbox", "host", "node"]
        },
        "url": { "type": "string" },
        "selector": { "type": "string" },
        "timeoutMs": { "type": "number" },
        "kind": {
          "type": "string",
          "enum": [
            "click", "clickCoords", "type", "press", "hover", "drag",
            "select", "fill", "resize", "wait", "evaluate", "close"
          ]
        },
        "text": { "type": "string" },
        "x": { "type": "number" },
        "y": { "type": "number" }
      }
    }
  }
}
```

---

## 4. Tool Choice 结构

`tool_choice` 控制模型是否/如何使用工具：

### 字符串形式
```json
"tool_choice": "auto"      // 模型决定是否使用工具
"tool_choice": "none"      // 不使用工具
"tool_choice": "required"  // 必须使用工具
```

### 对象形式（强制调用特定工具）
```json
"tool_choice": {
  "type": "function",
  "function": {
    "name": "Bash"
  }
}
```

---

## 5. Stream Options 结构

```json
"stream_options": {
  "include_usage": true
}
```

- `include_usage` — 是否在流式输出的最后包含 token 使用量信息

---

## 6. 完整复杂示例（精简版）

```json
{
  "model": "doubao-seed-2.0-code",
  "messages": [
    {
      "role": "system",
      "content": "You are a personal assistant running inside OpenClaw.\n## Tooling\nAvailable tools are policy-filtered..."
    },
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
      "content": "Let's start by exploring...",
      "tool_calls": [
        {
          "id": "call_xxx",
          "type": "function",
          "function": {
            "name": "exec",
            "arguments": "{\"command\":\"ls -la\",\"workdir\":\"/path\"}"
          }
        }
      ]
    },
    {
      "role": "tool",
      "content": "total 68\ndrwxrwxr-x...",
      "tool_call_id": "call_xxx"
    },
    {
      "role": "assistant",
      "content": "Based on the directory listing..."
    }
  ],
  "stream": true,
  "stream_options": {
    "include_usage": true
  },
  "max_completion_tokens": 32000,
  "tools": [
    {
      "type": "function",
      "function": {
        "name": "exec",
        "description": "Run shell commands...",
        "parameters": {
          "type": "object",
          "required": ["command"],
          "properties": {
            "command": { "type": "string" },
            "workdir": { "type": "string" }
          }
        }
      }
    }
  ],
  "tool_choice": "auto"
}
```

---

## 7. 与 Anthropic 协议的关键差异（转换要点）

| 维度 | OpenAI | Anthropic |
|------|--------|-----------|
| **System 提示** | `messages` 中 `role: "system"` | 顶层 `system` 字段 |
| **工具调用** | `tool_calls` / `role: "tool"` | `tool_use` / `tool_result` 内容块 |
| **工具定义** | `type: "function"` + `function.*` | `name` + `input_schema` |
| **工具调用 ID** | `tool_calls[].id` / `tool.tool_call_id` | `tool_use.id` / `tool_result.tool_use_id` |
| **工具参数** | `function.arguments`（JSON 字符串） | `tool_use.input`（JSON 对象） |
| **消息角色** | `system` / `user` / `assistant` / `tool` | `user` / `assistant`（system 在顶层） |
| **流式选项** | `stream_options.include_usage` | 无对应机制 |
| **工具选择** | `tool_choice`（auto/none/required/function） | 无直接对应 |
| **最大 Token** | `max_completion_tokens` | `max_tokens` |

---

## 8. 协议转换映射表

### 8.1 OpenAI → Anthropic 字段映射

| OpenAI 字段 | Anthropic 对应字段 | 转换规则 |
|-------------|-------------------|----------|
| `model` | `model` | 直接透传 |
| `max_completion_tokens` | `max_tokens` | 字段名转换 |
| `messages` | `messages` | 需要转换消息格式 |
| `messages[role="system"]` | `system`（顶层） | 提取到顶层字段 |
| `tools` | `tools` | 结构转换：function.name→name，parameters→input_schema |
| `tool_calls` | `tool_use` 内容块 | 消息字段转内容块 |
| `role: "tool"` | `tool_result` 内容块 | 独立消息转内容块（并入 user 消息） |
| `stream` | `stream` | 直接透传 |
| `stream_options` | — | 丢弃 |
| `tool_choice` | — | 丢弃（Anthropic 无对应机制） |

### 8.2 消息转换规则

**OpenAI `assistant` + `tool_calls` → Anthropic `assistant` + `tool_use`：**

```
OpenAI:
{
  "role": "assistant",
  "content": "Let's start by exploring...",
  "tool_calls": [
    {
      "id": "call_xxx",
      "type": "function",
      "function": { "name": "exec", "arguments": "{\"command\":\"ls\"}" }
    }
  ]
}

Anthropic:
{
  "role": "assistant",
  "content": [
    { "type": "text", "text": "Let's start by exploring..." },
    {
      "type": "tool_use",
      "id": "call_xxx",
      "name": "exec",
      "input": { "command": "ls" }
    }
  ]
}
```

**OpenAI `role: "tool"` → Anthropic `tool_result`：**

```
OpenAI:
{
  "role": "tool",
  "content": "执行结果...",
  "tool_call_id": "call_xxx"
}

Anthropic:
{
  "role": "user",
  "content": [
    {
      "tool_use_id": "call_xxx",
      "type": "tool_result",
      "content": "执行结果..."
    }
  ]
}
```

**注意：** Anthropic 没有独立的 `tool` 角色，工具结果需要放在 `user` 消息的 `content` 数组中作为 `tool_result` 块。

---

## 9. 工具定义转换规则

**OpenAI → Anthropic：**

```
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
```

**关键转换点：**
1. 移除 `type: "function"` 包装层
2. `function.name` → `name`
3. `function.description` → `description`
4. `function.parameters` → `input_schema`

---

## 10. 特殊字段处理

### 10.1 tool_choice 转换

OpenAI 的 `tool_choice` 在 Anthropic 中没有直接对应：
- `"auto"` → 默认行为，无需特殊处理
- `"none"` → 需要从 `tools` 数组中移除工具定义
- `"required"` → Anthropic 不支持，需移除或报错
- `{"type": "function", "function": {"name": "..."}}` → Anthropic 不支持强制调用特定工具

### 10.2 stream_options 转换

OpenAI `stream_options.include_usage` 在 Anthropic 中没有对应机制，转换时丢弃。

### 10.3 图片内容转换

OpenAI `content` 数组支持 `image_url` 和 `image` 类型，Anthropic 同样支持 `image` 类型（但字段结构不同）：

```
OpenAI:
{ "type": "image_url", "image_url": { "url": "https://...", "detail": "auto" } }

Anthropic:
{ "type": "image", "source": { "type": "url", "url": "https://..." } }
```

---

*本文档基于真实数据库请求数据生成，用于协议转换分析器的字段映射参考。*
