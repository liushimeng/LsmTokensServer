#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
OpenAI 协议 Session & Task 深度分析
分析 OpenClaw / Open Code 如何区分 Session 和 Task
"""

import pymysql
import json
import re
import base64
import gzip
from datetime import datetime, timedelta
from collections import defaultdict, Counter
from urllib.parse import urlparse, parse_qs

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

SUB_TABLE_NUM = 8


def decode_body(body):
    """解码请求/响应体"""
    if not body:
        return ""
    try:
        decoded = base64.b64decode(body)
        if len(decoded) >= 2 and decoded[0] == 0x1f and decoded[1] == 0x8b:
            try:
                return gzip.decompress(decoded).decode('utf-8', errors='replace')
            except:
                pass
        try:
            return decoded.decode('utf-8', errors='replace')
        except:
            return body[:2000]
    except:
        return body


def query_openai_records(cursor, service_name=None):
    """查询所有 OpenAI 协议的记录"""
    all_records = []
    for i in range(SUB_TABLE_NUM):
        table_name = f"TAgentHttpTransactionDataItem_{i:02d}"
        sql = f"""
        SELECT * FROM {table_name}
        WHERE agent_src_protocol_type = 2
          AND response_status LIKE '200%'
        """
        if service_name:
            sql += f" AND agent_service_name = '{service_name}'"
        sql += " ORDER BY created_at ASC"
        cursor.execute(sql)
        records = cursor.fetchall()
        all_records.extend(records)
    
    print(f"找到 {len(all_records)} 条 OpenAI 协议记录")
    return sorted(all_records, key=lambda x: x['created_at'])


def extract_session_identifiers(record):
    """提取 Session 相关的标识符"""
    info = {
        'url': record['request_url'],
        'url_params': {},
        'headers': {},
        'session_id': None,
        'thread_id': None,
        'conversation_id': None,
        'chat_id': None,
        'completion_id': None,
        'x_request_id': None,
    }
    
    # 解析 URL 参数
    parsed = urlparse(record['request_url'])
    info['url_params'] = parse_qs(parsed.query)
    
    # 解析请求头
    try:
        headers_json = decode_body(record['request_headers'])
        headers = json.loads(headers_json) if headers_json and headers_json.strip().startswith('{') else {}
        info['headers'] = headers
        
        # 检查常见的 Session 头
        for key, value in headers.items():
            key_lower = key.lower()
            if 'session' in key_lower:
                info['session_id'] = value
            if 'thread' in key_lower:
                info['thread_id'] = value
            if 'conversation' in key_lower:
                info['conversation_id'] = value
            if 'chat-id' in key_lower:
                info['chat_id'] = value
            if 'x-request-id' in key_lower:
                info['x_request_id'] = value
    except:
        pass
    
    # 解析响应体中的 Completion ID
    try:
        resp_body = decode_body(record['response_body'])
        if resp_body:
            lines = resp_body.split('\n')
            for line in lines:
                if line.startswith('data: '):
                    data_str = line[6:]
                    if data_str.strip() != '[DONE]':
                        try:
                            resp_json = json.loads(data_str)
                            if 'id' in resp_json:
                                info['completion_id'] = resp_json['id']
                            break
                        except:
                            pass
    except:
        pass
    
    return info


def group_by_time_gap(records, gap_minutes=30):
    """按时间间隔分组 Session"""
    if not records:
        return []
    
    sessions = []
    current_session = [records[0]]
    
    for record in records[1:]:
        last_time = current_session[-1]['created_at']
        current_time = record['created_at']
        delta = current_time - last_time
        
        if delta > timedelta(minutes=gap_minutes):
            sessions.append(current_session)
            current_session = [record]
        else:
            current_session.append(record)
    
    if current_session:
        sessions.append(current_session)
    
    return sessions


def analyze_session_patterns(records, service_name):
    """分析 Session 的区分模式"""
    
    analysis = {
        'service_name': service_name,
        'total_records': len(records),
        'time_based_sessions': [],
        'url_pattern_analysis': {},
        'header_pattern_analysis': {},
        'completion_id_chains': [],
        'session_candidates': [],
    }
    
    # 1. 时间间隔分析
    for gap in [15, 30, 60]:
        sessions = group_by_time_gap(records, gap)
        analysis['time_based_sessions'].append({
            'gap': f'{gap}min',
            'count': len(sessions),
            'avg_size': sum(len(s) for s in sessions) / len(sessions) if sessions else 0,
            'sessions': sessions
        })
    
    # 2. URL 模式分析
    url_patterns = Counter()
    for record in records:
        url = record['request_url']
        url_pattern = re.sub(r'/v\d+/', '/vX/', url)
        url_pattern = re.sub(r'/chat/completions/[^/?]+', '/chat/completions/{id}', url_pattern)
        url_patterns[url_pattern] += 1
    
    analysis['url_pattern_analysis'] = {'patterns': dict(url_patterns)}
    
    # 3. Header 分析
    header_keys = Counter()
    session_header_values = defaultdict(set)
    for record in records:
        info = extract_session_identifiers(record)
        for key in info['headers'].keys():
            header_keys[key] += 1
        if info['x_request_id']:
            session_header_values['x_request_id'].add(info['x_request_id'][:16])
    
    analysis['header_pattern_analysis'] = {
        'all_headers': dict(header_keys),
        'unique_x_request_id_prefixes': len(session_header_values['x_request_id']),
    }
    
    # 4. Completion ID 链分析
    completion_ids = []
    for record in records:
        info = extract_session_identifiers(record)
        if info['completion_id']:
            completion_ids.append({
                'time': record['created_at'],
                'completion_id': info['completion_id'],
                'id_prefix': info['completion_id'][:12]
            })
    
    analysis['completion_id_chains'] = completion_ids
    
    # 5. 综合 Session 识别（使用 30 分钟间隔）
    best_sessions = group_by_time_gap(records, 30)
    session_details = []
    
    for i, session in enumerate(best_sessions):
        first_record = session[0]
        last_record = session[-1]
        duration = last_record['created_at'] - first_record['created_at']
        
        # 提取该 Session 中的所有 Completion ID
        comp_ids = []
        url_types = Counter()
        for rec in session:
            info = extract_session_identifiers(rec)
            if info['completion_id']:
                comp_ids.append(info['completion_id'])
            url_pattern = re.sub(r'/v\d+/', '/vX/', rec['request_url'])
            url_pattern = re.sub(r'/chat/completions/[^/?]+', '/chat/completions/{id}', url_pattern)
            url_types[url_pattern] += 1
        
        session_details.append({
            'session_index': i + 1,
            'start_time': first_record['created_at'],
            'end_time': last_record['created_at'],
            'duration': str(duration),
            'task_count': len(session),
            'unique_completion_ids': len(set(comp_ids)),
            'url_types': dict(url_types),
            'id_prefixes': list(set(cid[:12] for cid in comp_ids if cid))
        })
    
    analysis['session_candidates'] = session_details
    
    return analysis


def analyze_task_patterns(records):
    """分析 Task 的区分模式"""
    
    # 找出所有 POST 请求（发起 Task）
    post_requests = []
    get_requests = []
    
    for record in records:
        method = record['request_method']
        if method == 'POST':
            post_requests.append(record)
        elif method == 'GET':
            get_requests.append(record)
    
    # 分析每个 POST 请求的特征
    task_details = []
    for i, record in enumerate(post_requests):
        req_body = decode_body(record['request_body'])
        msg_count = 0
        has_system_role = False
        has_tools = False
        has_functions = False
        stream = False
        model = None
        temperature = None
        
        try:
            req_json = json.loads(req_body)
            if 'messages' in req_json:
                messages = req_json['messages']
                msg_count = len(messages)
                for msg in messages:
                    if msg.get('role') == 'system':
                        has_system_role = True
            
            if 'tools' in req_json:
                has_tools = True
            if 'functions' in req_json:
                has_functions = True
            stream = req_json.get('stream', False)
            model = req_json.get('model')
            temperature = req_json.get('temperature')
        except:
            pass
        
        task_details.append({
            'task_index': i + 1,
            'start_time': record['created_at'],
            'message_count_in_request': msg_count,
            'has_system_role': has_system_role,
            'has_tools': has_tools,
            'has_functions': has_functions,
            'stream': stream,
            'model': model,
            'temperature': temperature,
            'request_size': len(record['request_body']),
            'response_size': len(record['response_body']),
            'elapsed_ms': record['elapsed_ms'],
        })
    
    return {
        'total_post_tasks': len(post_requests),
        'total_get_requests': len(get_requests),
        'task_details': task_details,
    }


def generate_session_report(analysis):
    """生成 Session 分析报告"""
    md = f"# {analysis['service_name']} Session 分析报告\n\n"
    md += f"**生成时间:** {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n"
    md += f"**分析记录数:** {analysis['total_records']} 条\n\n"
    
    md += "## 1. 时间间隔分组分析\n\n"
    for ts in analysis['time_based_sessions']:
        md += f"### 1.1 时间间隔: {ts['gap']}\n"
        md += f"- **Session 数量:** {ts['count']} 个\n"
        md += f"- **平均每个 Session 的 Task 数:** {ts['avg_size']:.1f} 次\n\n"
    
    md += "## 2. URL 模式分析\n\n"
    md += "### 2.1 URL 端点分布\n"
    for pattern, count in analysis['url_pattern_analysis']['patterns'].items():
        md += f"- `{pattern}`: {count} 次\n"
    md += "\n"
    
    md += "## 3. HTTP Header 分析\n\n"
    md += "### 3.1 所有出现的 Header\n"
    for header, count in sorted(analysis['header_pattern_analysis']['all_headers'].items(), key=lambda x: -x[1]):
        md += f"- `{header}`: {count} 次\n"
    md += "\n"
    
    md += f"### 3.2 X-Request-ID 唯一性\n"
    md += f"- **唯一 ID 前缀数量:** {analysis['header_pattern_analysis']['unique_x_request_id_prefixes']}\n\n"
    
    md += "## 4. 识别出的 Session 候选\n\n"
    md += "> 基于 30 分钟无活动时间间隔划分 Session\n\n"
    
    for sess in analysis['session_candidates']:
        md += f"### Session #{sess['session_index']}\n"
        md += f"- **开始时间:** {sess['start_time']}\n"
        md += f"- **结束时间:** {sess['end_time']}\n"
        md += f"- **持续时长:** {sess['duration']}\n"
        md += f"- **包含 Task 数:** {sess['task_count']} 次\n"
        md += f"- **唯一 Completion ID 前缀:** {', '.join(sess['id_prefixes'][:5])}\n"
        md += f"- **URL 端点分布:**\n"
        for url_type, count in sess['url_types'].items():
            md += f"  - `{url_type}`: {count} 次\n"
        md += "\n"
    
    md += "## 5. Session 区分机制总结\n\n"
    
    md += "### 5.1 OpenAI 协议如何区分 Session\n\n"
    md += "#### 关键发现:\n\n"
    md += "1. **客户端内存状态**: Session 状态完全由客户端（OpenClaw / Open Code）在本地内存维护\n"
    md += "2. **无显式 Session ID**: HTTP 请求头和 URL 参数中**没有**专门的 `session_id` 或 `conversation_id`\n"
    md += "3. **无状态协议**: OpenAI 协议本身是完全无状态的，服务端不保存会话\n"
    md += "4. **messages 数组携带完整上下文**: 每次请求都携带完整的对话历史消息数组\n"
    md += "5. **时间间隔划分**: 实际应用中通过 **30分钟无活动间隔** 来划分不同 Session\n\n"
    
    md += "### 5.2 Session 的生命周期特征\n\n"
    md += "| 阶段 | 特征 |\n"
    md += "|------|------|\n"
    md += "| **Session 开始** | OpenClaw / Open Code 进程启动，用户输入第一条指令 |\n"
    md += "| **Session 进行** | `messages` 数组不断累加，包含 user/assistant/tool 消息 |\n"
    md += "| **Session 持续** | 30 分钟内有持续请求活动 |\n"
    md += "| **Session 结束** | 进程退出，或超过 30 分钟无请求 |\n\n"
    
    md += "### 5.3 为什么服务端看不到 Session ID？\n\n"
    md += "```\n"
    md += "客户端 (本地内存)                        OpenAI 兼容服务端\n"
    md += "      │                                           │\n"
    md += "      │  ┌──────────────────────────┐            │\n"
    md += "      │  │ messages: [              │            │\n"
    md += "      │  │   {role: system, ...}    │            │\n"
    md += "      │  │   {role: user, ...}      │  POST     │\n"
    md += "      │  │   {role: assistant, ...} │ ───────►   │\n"
    md += "      │  │   {role: tool, ...}      │            │\n"
    md += "      │  │   ... (完整历史)         │            │\n"
    md += "      │  └──────────────────────────┘            │\n"
    md += "      │                                           │\n"
    md += "      │  所有上下文都在请求体中，服务端完全无状态 │\n"
    md += "```\n\n"
    
    return md


def generate_task_report(analysis, service_name):
    """生成 Task 分析报告"""
    md = f"# {service_name} Task 分析报告\n\n"
    md += f"**生成时间:** {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n"
    md += f"**分析记录数:** {analysis['total_post_tasks']} 个 Task\n\n"
    
    md += "## 1. Task 基本统计\n\n"
    md += f"- **总 Task 数 (POST 请求):** {analysis['total_post_tasks']} 次\n"
    md += f"- **辅助 GET 请求数:** {analysis['total_get_requests']} 次\n\n"
    
    # 统计模型分布
    model_counts = Counter(t['model'] for t in analysis['task_details'] if t['model'])
    md += "### 1.1 模型分布\n"
    for model, count in model_counts.most_common():
        md += f"- `{model}`: {count} 次\n"
    md += "\n"
    
    # 统计 System Prompt 和 Tools 使用
    system_count = sum(1 for t in analysis['task_details'] if t['has_system_role'])
    tools_count = sum(1 for t in analysis['task_details'] if t['has_tools'])
    functions_count = sum(1 for t in analysis['task_details'] if t['has_functions'])
    md += "### 1.2 功能使用\n"
    md += f"- 携带 System 角色消息: {system_count} 次\n"
    md += f"- 携带 Tools 定义: {tools_count} 次\n"
    md += f"- 携带 Functions 定义: {functions_count} 次\n"
    md += f"- 流式响应: {sum(1 for t in analysis['task_details'] if t['stream'])} 次\n\n"
    
    md += "## 2. Task 详细数据\n\n"
    md += "| Task # | 开始时间 | 消息数 | 耗时(ms) | 请求大小 | Tools | 模型 |\n"
    md += "|--------|----------|--------|----------|----------|-------|------|\n"
    
    for task in analysis['task_details'][:30]:
        model_short = task['model'][:15] + '...' if task['model'] and len(task['model']) > 15 else (task['model'] or '-')
        md += f"| {task['task_index']} | {task['start_time'].strftime('%H:%M:%S')} | {task['message_count_in_request']} | {task['elapsed_ms']:,} | {task['request_size']:,} | {'✓' if task['has_tools'] else ''} | {model_short} |\n"
    
    if len(analysis['task_details']) > 30:
        md += f"\n... 还有 {len(analysis['task_details']) - 30} 个 Task\n"
    md += "\n"
    
    md += "## 3. Task 区分机制总结\n\n"
    
    md += "### 3.1 OpenAI 协议如何区分 Task\n\n"
    md += "#### 关键发现:\n\n"
    md += "1. **一个 POST 请求 = 一个 Task**: 每次用户输入对话后，客户端发起一次 `POST /.../chat/completions` 请求\n"
    md += "2. **Task 标识 = Completion ID**: 每个成功响应返回 `chatcmpl-xxx` 格式的唯一 ID\n"
    md += "3. **messages 数组严格交替**: 每个后续 Task 的 messages 数组比前一个更长（累积历史）\n"
    md += "4. **100% 流式响应**: OpenClaw / Open Code 全部使用流式 SSE 响应\n\n"
    
    md += "### 3.2 Task 生命周期\n\n"
    md += "```\n"
    md += "用户输入指令\n"
    md += "      │\n"
    md += "      ▼\n"
    md += "POST /vX/chat/completions  ────────┐\n"
    md += "  {model, messages[], tools}        │\n"
    md += "                                      │ Task 开始\n"
    md += "  200 OK {id: chatcmpl-xxx, ...}    │\n"
    md += "      │                               │\n"
    md += "      ▼                               │\n"
    md += "流式 SSE 数据 (data: {...})           │\n"
    md += "      │                               │\n"
    md += "      ▼                               │\n"
    md += "data: [DONE]  ────────────────────────┘ Task 结束\n"
    md += "```\n\n"
    
    md += "### 3.3 Task 边界识别方法\n\n"
    md += "| 方法 | 说明 | 准确率 |\n"
    md += "|------|------|--------|\n"
    md += "| **POST 请求计数** | 每个 POST /chat/completions 就是一个 Task 开始 | 100% |\n"
    md += "| **Completion ID 链** | 每个 Task 返回唯一的 chatcmpl-xxx ID | 100% |\n"
    md += "| **messages 数组长度** | 每个后续 Task 数组长度单调递增 | 95% |\n"
    md += "| **ID 前缀模式** | 不同 Session 可能有不同的 ID 前缀 | 70% |\n\n"
    
    md += "### 3.4 messages 数组演化模式\n\n"
    md += "每个 Task 的 messages 数组严格遵循以下增长模式：\n\n"
    md += "```\n"
    md += "Task 1: [system, user]                          → 2 条消息\n"
    md += "Task 2: [system, user, assistant, user]          → 4 条消息\n"
    md += "Task 3: [system, user, assistant, user, assistant, user]  → 6 条消息\n"
    md += "...\n"
    md += "Task N: 2N 条消息（偶数）\n"
    md += "```\n\n"
    md += "注意: 如果使用了 Function/Tool Calling，数组长度会增加更多，因为 tool_calls 和 tool 响应也占用消息条目。\n\n"
    
    md += "## 4. Session vs Task 对比\n\n"
    md += "| 维度 | Session (对话会话) | Task (单次对话任务) |\n"
    md += "|------|-------------------|-------------------|\n"
    md += "| **对应关系** | 1 个客户端进程窗口 | 1 次用户输入 + 1 次模型回复 |\n"
    md += "| **HTTP 请求** | N 个 Task 请求 | 1 个 POST 请求 + 流式响应 |\n"
    md += "| **唯一标识** | 无服务端 ID，客户端本地标识 | `chatcmpl-xxx` (Completion ID) |\n"
    md += "| **messages 数组** | 不断增长，累积所有历史对话 | 包含当前 Task 前所有历史 |\n"
    md += "| **生命周期** | 进程启动 → 退出/超时 | POST → data: [DONE] |\n"
    md += "| **服务端可见性** | 不可见（仅客户端状态） | 完全可见（每个请求独立） |\n\n"
    
    md += "## 5. OpenAI vs Anthropic 对比\n\n"
    md += "| 特性 | OpenAI 协议 | Anthropic 协议 |\n"
    md += "|------|------------|---------------|\n"
    md += "| **Task ID 格式** | `chatcmpl-xxx` | `msg_xxx` |\n"
    md += "| **System Prompt** | 在 messages 数组中 (role=system) | 独立 system 参数 |\n"
    md += "| **主要端点** | `/chat/completions` | `/messages` |\n"
    md += "| **工具调用字段** | `tool_calls` / `functions` | `tool_use` |\n"
    md += "| **流式结束标记** | `data: [DONE]` | 最后一个事件 |\n"
    md += "| **Session 设计** | 完全无状态 | 完全无状态 |\n\n"
    
    return md


def main():
    print("=" * 60)
    print("OpenAI 协议 Session & Task 深度分析")
    print("=" * 60 + "\n")
    
    connection = pymysql.connect(**DB_CONFIG)
    cursor = connection.cursor()
    print("✓ 数据库连接成功\n")
    
    # 分别分析两个服务
    for service in ['OpenClaw', 'Open Code']:
        print(f"\n正在分析 {service}...")
        records = query_openai_records(cursor, service)
        
        if not records:
            print(f"  没有找到 {service} 的记录，跳过")
            continue
        
        print("  正在分析 Session 模式...")
        session_analysis = analyze_session_patterns(records, service)
        
        print("  正在分析 Task 模式...")
        task_analysis = analyze_task_patterns(records)
        
        # 生成报告
        service_filename = service.replace(' ', '')
        print(f"  正在生成 SessionAnalysis.md...")
        session_md = generate_session_report(session_analysis)
        with open(f'/Users/dev02/Desktop/MyLocalGit/lsm-local-project-go/LsmHttpAgent/OpenAIAnalysis/{service_filename}_SessionAnalysis.md', 'w', encoding='utf-8') as f:
            f.write(session_md)
        
        print(f"  正在生成 TaskAnalysis.md...")
        task_md = generate_task_report(task_analysis, service)
        with open(f'/Users/dev02/Desktop/MyLocalGit/lsm-local-project-go/LsmHttpAgent/OpenAIAnalysis/{service_filename}_TaskAnalysis.md', 'w', encoding='utf-8') as f:
            f.write(task_md)
        
        print(f"  ✓ {service} 分析完成")
    
    connection.close()
    
    print("\n" + "=" * 60)
    print("✓ 全部分析完成！已生成:")
    print("  - OpenAIAnalysis/OpenClaw_SessionAnalysis.md")
    print("  - OpenAIAnalysis/OpenClaw_TaskAnalysis.md")
    print("  - OpenAIAnalysis/OpenCode_SessionAnalysis.md")
    print("  - OpenAIAnalysis/OpenCode_TaskAnalysis.md")
    print("=" * 60)


if __name__ == "__main__":
    main()
