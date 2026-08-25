#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Anthropic 协议数据提取脚本
从数据库哈希分表中提取 Anthropic 协议的真实请求/响应样本
生成 Markdown 格式分析报告，用于驱动 Go 协议转换器开发
"""

import json
import os
import sys
import base64
import gzip
from datetime import datetime
from collections import Counter

# 添加项目根目录到路径以导入配置
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

try:
    import pymysql
except ImportError:
    print("错误: 请先安装 pymysql: pip3 install pymysql")
    sys.exit(1)

# 数据库配置（v2.0.56 安全加固：凭证从环境变量读取，禁止硬编码）
import os
DB_CONFIG = {
    'host': os.environ.get('LSM_MYSQL_HOST', '127.0.0.1'),
    'port': int(os.environ.get('LSM_MYSQL_PORT', '3306')),
    'user': os.environ.get('LSM_MYSQL_USER', ''),
    'password': os.environ.get('LSM_MYSQL_PASSWORD', ''),
    'database': os.environ.get('LSM_MYSQL_DATABASE', 'lsmDB'),
    'charset': 'utf8mb4',
    'cursorclass': pymysql.cursors.DictCursor
}
if not DB_CONFIG['user'] or not DB_CONFIG['password']:
    raise SystemExit('请设置环境变量 LSM_MYSQL_USER / LSM_MYSQL_PASSWORD')

# 分表数量（从 LsmHttpAgent.conf 读取）
SUB_TABLE_NUM = 8

# 协议类型常量
PROTOCOL_TYPE_ANTHROPIC = 1
PROTOCOL_TYPE_OPENAI = 2


def decode_body(body):
    """解码请求/响应体（处理 base64 和 gzip 压缩）"""
    if not body:
        return ""

    try:
        # 尝试 base64 解码
        decoded = base64.b64decode(body)

        # 检查是否是 gzip 压缩 (magic number: 0x1f 0x8b)
        if len(decoded) >= 2 and decoded[0] == 0x1f and decoded[1] == 0x8b:
            try:
                decompressed = gzip.decompress(decoded)
                return decompressed.decode('utf-8', errors='replace')
            except Exception:
                pass

        # 尝试直接作为 UTF-8
        try:
            return decoded.decode('utf-8', errors='replace')
        except Exception:
            pass

        # 如果都失败，返回原始 base64 字符串（截断）
        return body[:1000]
    except Exception:
        # 不是 base64，直接返回
        return body


def query_anthropic_records(cursor, limit_per_table=50):
    """查询所有 Anthropic 相关的记录（响应状态为 200）"""
    all_records = []

    # 遍历所有分表
    for i in range(SUB_TABLE_NUM):
        table_name = f"TAgentHttpTransactionDataItem_{i:02d}"

        # 查询该表中 Anthropic 协议且响应状态为 200 的记录
        sql = f"""
        SELECT id, created_at, user_name, model_name, dst_model_name,
               protocol_type, request_method, request_url,
               request_headers, request_body,
               response_status, response_headers, response_body,
               elapsed_ms, is_stream, has_system_prompt, has_tool_call,
               message_count, user_message_count
        FROM {table_name}
        WHERE protocol_type = %s
          AND response_status LIKE '200%%'
        ORDER BY id DESC
        LIMIT %s
        """
        cursor.execute(sql, (PROTOCOL_TYPE_ANTHROPIC, limit_per_table))
        records = cursor.fetchall()
        print(f"  表 {table_name}: 找到 {len(records)} 条 Anthropic 记录")

        # 添加表名到记录中，方便追溯
        for rec in records:
            rec['_table_name'] = table_name

        all_records.extend(records)

    print(f"\n  总共找到 {len(all_records)} 条 Anthropic 记录（响应状态 200）")
    return all_records


def extract_request_structure(request_body):
    """提取 Anthropic 请求结构"""
    decoded = decode_body(request_body)
    if not decoded:
        return None

    try:
        req = json.loads(decoded)
    except json.JSONDecodeError:
        return None

    structure = {
        'model': req.get('model'),
        'max_tokens': req.get('max_tokens'),
        'stream': req.get('stream', False),
        'temperature': req.get('temperature'),
        'top_p': req.get('top_p'),
        'top_k': req.get('top_k'),
        'system': req.get('system'),
        'has_system': 'system' in req,
        'messages_count': 0,
        'messages_sample': None,
        'tools_count': 0,
        'tools_sample': None,
        'tool_choice': req.get('tool_choice'),
        'thinking': req.get('thinking'),
    }

    if 'messages' in req and isinstance(req['messages'], list):
        structure['messages_count'] = len(req['messages'])
        # 保存第一条消息的样本
        if req['messages']:
            msg = req['messages'][0]
            structure['messages_sample'] = {
                'role': msg.get('role'),
                'content_type': type(msg.get('content')).__name__,
                'content_preview': str(msg.get('content'))[:200] if msg.get('content') else None,
            }

    if 'tools' in req and isinstance(req['tools'], list):
        structure['tools_count'] = len(req['tools'])
        if req['tools']:
            tool = req['tools'][0]
            structure['tools_sample'] = {
                'name': tool.get('name') if isinstance(tool, dict) else None,
                'type': tool.get('type') if isinstance(tool, dict) else None,
            }

    return structure


def extract_response_structure(response_body, is_stream=False):
    """提取 Anthropic 响应结构"""
    decoded = decode_body(response_body)
    if not decoded:
        return None

    if is_stream:
        # 流式响应：逐行解析 SSE
        lines = decoded.strip().split('\n')
        events = []
        for line in lines:
            if line.startswith('data: '):
                data = line[6:]
                if data.strip() == '[DONE]':
                    continue
                try:
                    event = json.loads(data)
                    events.append(event)
                except json.JSONDecodeError:
                    pass

        if not events:
            return None

        # 分析事件类型分布
        event_types = Counter()
        for e in events:
            event_types[e.get('type', 'unknown')] += 1

        # 获取第一个和最后一个事件的结构样本
        first_event = events[0] if events else None
        last_event = events[-1] if events else None

        return {
            'is_stream': True,
            'event_count': len(events),
            'event_types': dict(event_types),
            'first_event_type': first_event.get('type') if first_event else None,
            'last_event_type': last_event.get('type') if last_event else None,
            'has_content_block_delta': 'content_block_delta' in dict(event_types),
            'has_message_delta': 'message_delta' in dict(event_types),
        }
    else:
        # 非流式响应
        try:
            resp = json.loads(decoded)
        except json.JSONDecodeError:
            return None

        return {
            'is_stream': False,
            'id': resp.get('id'),
            'model': resp.get('model'),
            'role': resp.get('role'),
            'content_type': type(resp.get('content')).__name__ if 'content' in resp else None,
            'stop_reason': resp.get('stop_reason'),
            'usage': resp.get('usage'),
        }


def generate_markdown_report(records, output_dir):
    """生成 Markdown 分析报告"""
    md_content = "# Anthropic 协议数据包分析报告\n\n"
    md_content += f"**生成时间:** {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n"
    md_content += f"**分析记录数:** {len(records)} 条（响应状态 200）\n\n"
    md_content += "**用途:** 本报告用于驱动 Go 协议转换器开发，提供真实的协议结构样本\n\n"

    # 1. 请求结构分析
    md_content += "## 1. Anthropic 请求结构分析\n\n"

    models = Counter()
    stream_count = 0
    non_stream_count = 0
    has_system_count = 0
    has_tools_count = 0
    avg_messages = []

    for rec in records:
        req_struct = extract_request_structure(rec['request_body'])
        if not req_struct:
            continue

        if req_struct['model']:
            models[req_struct['model']] += 1
        if req_struct['stream']:
            stream_count += 1
        else:
            non_stream_count += 1
        if req_struct['has_system']:
            has_system_count += 1
        if req_struct['tools_count'] > 0:
            has_tools_count += 1
        avg_messages.append(req_struct['messages_count'])

    md_content += "### 1.1 模型分布\n"
    for model, count in models.most_common():
        md_content += f"- `{model}`: {count} 次\n"
    md_content += "\n"

    md_content += "### 1.2 流式 vs 非流式\n"
    total = len(records)
    md_content += f"- **流式 (stream=true):** {stream_count} 次 ({stream_count/total*100:.1f}%)\n"
    md_content += f"- **非流式 (stream=false):** {non_stream_count} 次 ({non_stream_count/total*100:.1f}%)\n\n"

    md_content += "### 1.3 System Prompt 使用情况\n"
    md_content += f"- 包含 System Prompt: {has_system_count} 次 ({has_system_count/total*100:.1f}%)\n\n"

    md_content += "### 1.4 工具调用使用情况\n"
    md_content += f"- 包含 Tools: {has_tools_count} 次 ({has_tools_count/total*100:.1f}%)\n\n"

    if avg_messages:
        md_content += "### 1.5 消息数量统计\n"
        md_content += f"- 平均消息数: {sum(avg_messages)/len(avg_messages):.1f}\n"
        md_content += f"- 最小消息数: {min(avg_messages)}\n"
        md_content += f"- 最大消息数: {max(avg_messages)}\n\n"

    # 2. 典型请求样本
    md_content += "## 2. 典型请求样本（完整 JSON）\n\n"

    sample_count = 0
    for rec in records:
        decoded_req = decode_body(rec['request_body'])
        if not decoded_req:
            continue

        try:
            req_json = json.loads(decoded_req)
        except json.JSONDecodeError:
            continue

        sample_count += 1
        md_content += f"### 样本 {sample_count}: {rec['model_name']} | stream={req_json.get('stream', False)}\n\n"
        md_content += f"- **用户:** {rec['user_name']}\n"
        md_content += f"- **模型:** {rec['model_name']} → {rec['dst_model_name']}\n"
        md_content += f"- **URL:** {rec['request_url']}\n"
        md_content += f"- **流式:** {req_json.get('stream', False)}\n"
        md_content += f"- **消息数:** {rec['message_count']}\n"
        md_content += f"- **System Prompt:** {rec['has_system_prompt']}\n\n"

        # 格式化输出 JSON
        formatted = json.dumps(req_json, indent=2, ensure_ascii=False)
        md_content += "**请求体:**\n```json\n" + formatted + "\n```\n\n"

        # 对应的响应样本
        decoded_resp = decode_body(rec['response_body'])
        if decoded_resp:
            md_content += "**响应体（前 2000 字符）:**\n```\n"
            md_content += decoded_resp[:2000]
            if len(decoded_resp) > 2000:
                md_content += "\n... (截断)"
            md_content += "\n```\n\n"

        md_content += "---\n\n"

        if sample_count >= 10:  # 最多展示 10 个完整样本
            break

    # 3. 响应结构分析
    md_content += "## 3. Anthropic 响应结构分析\n\n"

    stream_resp_structures = []
    non_stream_resp_structures = []

    for rec in records:
        resp_struct = extract_response_structure(rec['response_body'], rec['is_stream'])
        if not resp_struct:
            continue
        if resp_struct['is_stream']:
            stream_resp_structures.append(resp_struct)
        else:
            non_stream_resp_structures.append(resp_struct)

    md_content += "### 3.1 流式响应结构\n\n"
    if stream_resp_structures:
        avg_events = sum(s['event_count'] for s in stream_resp_structures) / len(stream_resp_structures)
        md_content += f"- 样本数: {len(stream_resp_structures)}\n"
        md_content += f"- 平均事件数: {avg_events:.1f}\n"

        # 收集所有事件类型
        all_event_types = Counter()
        for s in stream_resp_structures:
            for et, count in s.get('event_types', {}).items():
                all_event_types[et] += count
        md_content += "- 事件类型分布:\n"
        for et, count in all_event_types.most_common():
            md_content += f"  - `{et}`: {count} 次\n"
    else:
        md_content += "- 无流式响应样本\n"
    md_content += "\n"

    md_content += "### 3.2 非流式响应结构\n\n"
    if non_stream_resp_structures:
        md_content += f"- 样本数: {len(non_stream_resp_structures)}\n"
        stop_reasons = Counter(s.get('stop_reason') for s in non_stream_resp_structures if s.get('stop_reason'))
        if stop_reasons:
            md_content += "- Stop Reason 分布:\n"
            for sr, count in stop_reasons.most_common():
                md_content += f"  - `{sr}`: {count} 次\n"
    else:
        md_content += "- 无非流式响应样本\n"
    md_content += "\n"

    # 4. 协议字段映射参考（用于 Go 转换器开发）
    md_content += "## 4. Anthropic 协议字段映射参考（Go 转换器开发用）\n\n"

    md_content += "### 4.1 请求字段\n\n"
    md_content += "| Anthropic 字段 | 类型 | 说明 | OpenAI 对应字段 |\n"
    md_content += "|---------------|------|------|----------------|\n"
    md_content += "| `model` | string | 模型名称 | `model` |\n"
    md_content += "| `messages` | array | 消息数组 | `messages` |\n"
    md_content += "| `max_tokens` | int | 最大生成 token 数 | `max_tokens` / `max_completion_tokens` |\n"
    md_content += "| `stream` | bool | 是否流式 | `stream` |\n"
    md_content += "| `system` | string | 系统提示（独立参数）| `messages` 中 role=system |\n"
    md_content += "| `temperature` | float | 温度 | `temperature` |\n"
    md_content += "| `top_p` | float | 核采样 | `top_p` |\n"
    md_content += "| `top_k` | int | Top-K 采样 | 无直接对应 |\n"
    md_content += "| `tools` | array | 工具定义 | `tools` |\n"
    md_content += "| `tool_choice` | object/string | 工具选择策略 | `tool_choice` |\n"
    md_content += "| `thinking` | object | 思考模式配置 | 无直接对应 |\n"
    md_content += "\n"

    md_content += "### 4.2 消息角色映射\n\n"
    md_content += "| Anthropic 角色 | OpenAI 角色 | 说明 |\n"
    md_content += "|---------------|------------|------|\n"
    md_content += "| `user` | `user` | 用户消息 |\n"
    md_content += "| `assistant` | `assistant` | 助手消息 |\n"
    md_content += "| 无（system 为独立参数）| `system` | 系统提示 |\n"
    md_content += "| 无 | `tool` | 工具返回结果（需映射为 anthropic 的 user 消息）|\n"
    md_content += "\n"

    md_content += "### 4.3 响应字段\n\n"
    md_content += "| Anthropic 字段 | 类型 | 说明 | OpenAI 对应字段 |\n"
    md_content += "|---------------|------|------|----------------|\n"
    md_content += "| `id` | string | 消息 ID (msg_xxx) | `id` (chatcmpl-xxx) |\n"
    md_content += "| `type` | string | 类型（message）| - |\n"
    md_content += "| `role` | string | 角色（assistant）| `choices[0].message.role` |\n"
    md_content += "| `content` | array | 内容块数组 | `choices[0].message.content` |\n"
    md_content += "| `model` | string | 模型名称 | `model` |\n"
    md_content += "| `stop_reason` | string | 停止原因 | `choices[0].finish_reason` |\n"
    md_content += "| `usage` | object | Token 使用统计 | `usage` |\n"
    md_content += "\n"

    md_content += "### 4.4 流式事件类型\n\n"
    md_content += "| Anthropic 事件类型 | 说明 | OpenAI 对应 |\n"
    md_content += "|-------------------|------|------------|\n"
    md_content += "| `message_start` | 消息开始 | `choices[0].delta.role=assistant` |\n"
    md_content += "| `content_block_start` | 内容块开始 | - |\n"
    md_content += "| `content_block_delta` | 内容块增量 | `choices[0].delta.content` |\n"
    md_content += "| `content_block_stop` | 内容块结束 | - |\n"
    md_content += "| `message_delta` | 消息增量（usage 等）| `choices[0].delta` + usage |\n"
    md_content += "| `message_stop` | 消息结束 | `[DONE]` |\n"
    md_content += "\n"

    # 保存报告
    output_path = os.path.join(output_dir, "AnthropicProtocolAnalysis.md")
    with open(output_path, 'w', encoding='utf-8') as f:
        f.write(md_content)

    print(f"\n  ✓ 分析报告已保存到: {output_path}")
    return output_path


def save_raw_samples(records, output_dir):
    """保存原始 JSON 样本到文件，供 Go 测试使用"""
    samples = []

    for rec in records:
        decoded_req = decode_body(rec['request_body'])
        decoded_resp = decode_body(rec['response_body'])

        if not decoded_req:
            continue

        try:
            req_json = json.loads(decoded_req)
        except json.JSONDecodeError:
            continue

        sample = {
            'id': rec['id'],
            'user_name': rec['user_name'],
            'model_name': rec['model_name'],
            'dst_model_name': rec['dst_model_name'],
            'protocol_type': rec['protocol_type'],
            'request_url': rec['request_url'],
            'is_stream': rec['is_stream'],
            'has_system_prompt': rec['has_system_prompt'],
            'has_tool_call': rec['has_tool_call'],
            'message_count': rec['message_count'],
            'request': req_json,
        }

        if decoded_resp:
            sample['response_raw'] = decoded_resp[:5000]  # 限制大小

        samples.append(sample)

        if len(samples) >= 20:  # 最多保存 20 个样本
            break

    output_path = os.path.join(output_dir, "AnthropicRawSamples.json")
    with open(output_path, 'w', encoding='utf-8') as f:
        json.dump(samples, f, indent=2, ensure_ascii=False)

    print(f"  ✓ 原始样本已保存到: {output_path} ({len(samples)} 个样本)")
    return output_path


def main():
    print("=" * 70)
    print("Anthropic 协议数据提取工具")
    print("=" * 70 + "\n")

    # 确定输出目录
    script_dir = os.path.dirname(os.path.abspath(__file__))
    output_dir = script_dir

    try:
        # 连接数据库
        connection = pymysql.connect(**DB_CONFIG)
        cursor = connection.cursor()
        print("✓ 数据库连接成功\n")

        # 查询 Anthropic 记录
        print("正在查询 Anthropic 协议记录...")
        records = query_anthropic_records(cursor, limit_per_table=30)

        if not records:
            print("\n  ✗ 没有找到 Anthropic 相关的记录！")
            return

        # 生成 Markdown 分析报告
        print("\n正在生成 Markdown 分析报告...")
        report_path = generate_markdown_report(records, output_dir)

        # 保存原始样本
        print("\n正在保存原始 JSON 样本...")
        samples_path = save_raw_samples(records, output_dir)

        print("\n" + "=" * 70)
        print("分析完成！")
        print(f"  报告: {report_path}")
        print(f"  样本: {samples_path}")
        print("=" * 70)

    except Exception as e:
        print(f"\n  ✗ 错误: {e}")
        import traceback
        traceback.print_exc()
    finally:
        if 'connection' in locals() and connection:
            connection.close()


if __name__ == "__main__":
    main()
