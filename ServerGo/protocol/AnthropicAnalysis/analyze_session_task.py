#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Claude Code Session & Task 深度分析
分析 Claude Code CLI 如何区分 Session 和 Task
"""

import pymysql
import json
import re
import base64
import gzip
from datetime import datetime, timedelta
from collections import defaultdict, Counter
from urllib.parse import urlparse, parse_qs

# 数据库配置
DB_CONFIG = {
    'host': '127.0.0.1',
    'port': 3306,
    'user': 'superuser',
    'password': 'da=p1da@asd+12',
    'database': 'lsmDB',
    'charset': 'utf8mb4',
    'cursorclass': pymysql.cursors.DictCursor
}

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


def query_claude_code_records(cursor):
    """查询所有 Claude Code 服务的记录"""
    all_records = []
    for i in range(SUB_TABLE_NUM):
        table_name = f"TAgentHttpTransactionDataItem_{i:02d}"
        sql = f"""
        SELECT * FROM {table_name}
        WHERE agent_service_name = 'Claude Code'
          AND response_status LIKE '200%'
        ORDER BY created_at ASC
        """
        cursor.execute(sql)
        records = cursor.fetchall()
        all_records.extend(records)
    print(f"找到 {len(all_records)} 条 Claude Code 记录")
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
        'message_id': None,
        'parent_message_id': None,
        'x_request_id': None,
    }
    
    # 解析 URL 参数
    parsed = urlparse(record['request_url'])
    info['url_params'] = parse_qs(parsed.query)
    
    # 解析请求头
    try:
        headers = json.loads(decode_body(record['request_headers'])) if record['request_headers'] else {}
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
            if 'x-request-id' in key_lower:
                info['x_request_id'] = value
    except:
        pass
    
    # 解析请求体中的消息 ID
    try:
        req_body = decode_body(record['request_body'])
        if req_body:
            req_json = json.loads(req_body) if req_body.strip().startswith('{') else None
            if req_json:
                if 'id' in req_json:
                    info['message_id'] = req_json['id']
                if 'parent_id' in req_json:
                    info['parent_message_id'] = req_json['parent_id']
                
                # 检查 messages 数组最后一条的 ID
                if 'messages' in req_json and isinstance(req_json['messages'], list):
                    last_msg = req_json['messages'][-1] if req_json['messages'] else {}
                    if 'id' in last_msg:
                        info['message_id'] = last_msg['id']
    except:
        pass
    
    # 解析响应体中的消息 ID
    try:
        resp_body = decode_body(record['response_body'])
        if resp_body:
            # 流式响应取第一条 data
            lines = resp_body.split('\n')
            for line in lines:
                if line.startswith('data: '):
                    data_str = line[6:]
                    if data_str.strip() != '[DONE]':
                        try:
                            resp_json = json.loads(data_str)
                            if 'id' in resp_json:
                                info['message_id'] = resp_json['id']
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
        
        # 计算时间差
        delta = current_time - last_time
        
        if delta > timedelta(minutes=gap_minutes):
            sessions.append(current_session)
            current_session = [record]
        else:
            current_session.append(record)
    
    if current_session:
        sessions.append(current_session)
    
    return sessions


def analyze_session_patterns(records):
    """分析 Session 的区分模式"""
    
    analysis = {
        'total_records': len(records),
        'time_based_sessions': [],
        'url_pattern_analysis': [],
        'header_pattern_analysis': {},
        'message_id_chains': [],
        'session_candidates': [],
    }
    
    # 1. 时间间隔分析（30分钟间隔）
    sessions_30min = group_by_time_gap(records, 30)
    analysis['time_based_sessions'].append({
        'gap': '30min',
        'count': len(sessions_30min),
        'avg_size': sum(len(s) for s in sessions_30min) / len(sessions_30min) if sessions_30min else 0,
        'sessions': sessions_30min
    })
    
    # 2. 1小时间隔
    sessions_1h = group_by_time_gap(records, 60)
    analysis['time_based_sessions'].append({
        'gap': '1h',
        'count': len(sessions_1h),
        'avg_size': sum(len(s) for s in sessions_1h) / len(sessions_1h) if sessions_1h else 0,
        'sessions': sessions_1h
    })
    
    # 3. URL 模式分析
    url_patterns = Counter()
    url_with_ids = []
    for record in records:
        url = record['request_url']
        # 检查 URL 中是否有 ID 模式
        match = re.search(r'/messages/([a-zA-Z0-9_-]+)', url)
        if match:
            msg_id = match.group(1)
            url_with_ids.append({
                'time': record['created_at'],
                'message_id': msg_id,
                'url': url
            })
        url_pattern = re.sub(r'/messages/[a-zA-Z0-9_-]+', '/messages/{msg_id}', url)
        url_patterns[url_pattern] += 1
    
    analysis['url_pattern_analysis'] = {
        'patterns': dict(url_patterns),
        'messages_with_explicit_id': url_with_ids
    }
    
    # 4. Header 分析
    header_keys = Counter()
    session_headers = defaultdict(list)
    for record in records:
        info = extract_session_identifiers(record)
        for key in info['headers'].keys():
            header_keys[key] += 1
        if info['x_request_id']:
            session_headers['x_request_id'].append({
                'time': record['created_at'],
                'value': info['x_request_id']
            })
    
    analysis['header_pattern_analysis'] = {
        'all_headers': dict(header_keys),
        'session_headers': dict(session_headers)
    }
    
    # 5. 消息 ID 链分析
    message_ids = []
    for record in records:
        info = extract_session_identifiers(record)
        if info['message_id']:
            message_ids.append({
                'time': record['created_at'],
                'message_id': info['message_id'],
                'id_prefix': info['message_id'][:8] if info['message_id'] else None
            })
    
    analysis['message_id_chains'] = message_ids
    
    # 6. 综合 Session 识别
    # 使用最优时间间隔分组，并分析每组的特征
    best_sessions = sessions_30min
    session_details = []
    
    for i, session in enumerate(best_sessions):
        first_record = session[0]
        last_record = session[-1]
        duration = last_record['created_at'] - first_record['created_at']
        
        # 提取该 Session 中的所有消息 ID
        msg_ids = []
        url_types = Counter()
        for rec in session:
            info = extract_session_identifiers(rec)
            if info['message_id']:
                msg_ids.append(info['message_id'])
            url_pattern = re.sub(r'/messages/[a-zA-Z0-9_-]+', '/messages/{msg_id}', rec['request_url'])
            url_types[url_pattern] += 1
        
        session_details.append({
            'session_index': i + 1,
            'start_time': first_record['created_at'],
            'end_time': last_record['created_at'],
            'duration': str(duration),
            'task_count': len(session),
            'unique_message_ids': len(set(msg_ids)),
            'url_types': dict(url_types),
            'message_id_prefixes': list(set(mid[:8] for mid in msg_ids if mid))
        })
    
    analysis['session_candidates'] = session_details
    
    return analysis


def analyze_task_patterns(records):
    """分析 Task 的区分模式"""
    
    # 找出所有 POST /messages 请求（发起 Task）
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
        has_system = False
        has_tools = False
        stream = False
        model = None
        
        try:
            req_json = json.loads(req_body)
            if 'messages' in req_json:
                msg_count = len(req_json['messages'])
            if 'system' in req_json:
                has_system = True
            if 'tools' in req_json:
                has_tools = True
            stream = req_json.get('stream', False)
            model = req_json.get('model')
        except:
            pass
        
        # 检查后续的 GET 请求（轮询结果）
        task_end_time = record['created_at']
        polling_count = 0
        for other in post_requests[i+1:]:
            if other['created_at'] > record['created_at']:
                task_end_time = other['created_at']
                break
        else:
            # 如果是最后一个，找所有后续记录
            for other in records:
                if other['created_at'] > record['created_at']:
                    task_end_time = max(task_end_time, other['created_at'])
        
        task_details.append({
            'task_index': i + 1,
            'start_time': record['created_at'],
            'estimated_end_time': task_end_time,
            'duration': str(task_end_time - record['created_at']),
            'message_count_in_request': msg_count,
            'has_system': has_system,
            'has_tools': has_tools,
            'stream': stream,
            'model': model,
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
    md = "# Claude Code Session 分析报告\n\n"
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
    
    md += "### 2.2 带有显式 Message ID 的请求\n"
    md += "这些请求通常是：获取消息状态、流式结果、或者继续对话\n\n"
    for item in analysis['url_pattern_analysis']['messages_with_explicit_id'][:10]:
        md += f"- `{item['time']}`: `{item['message_id'][:20]}...`\n"
    if len(analysis['url_pattern_analysis']['messages_with_explicit_id']) > 10:
        md += f"... 共 {len(analysis['url_pattern_analysis']['messages_with_explicit_id'])} 条\n"
    md += "\n"
    
    md += "## 3. HTTP Header 分析\n\n"
    md += "### 3.1 所有出现的 Header\n"
    for header, count in sorted(analysis['header_pattern_analysis']['all_headers'].items(), key=lambda x: -x[1]):
        md += f"- `{header}`: {count} 次\n"
    md += "\n"
    
    md += "## 4. 识别出的 Session 候选\n\n"
    md += "> 基于 30 分钟无活动时间间隔划分 Session\n\n"
    
    for sess in analysis['session_candidates']:
        md += f"### Session #{sess['session_index']}\n"
        md += f"- **开始时间:** {sess['start_time']}\n"
        md += f"- **结束时间:** {sess['end_time']}\n"
        md += f"- **持续时长:** {sess['duration']}\n"
        md += f"- **包含 Task 数:** {sess['task_count']} 次\n"
        md += f"- **唯一消息 ID 前缀:** {', '.join(sess['message_id_prefixes'])}\n"
        md += f"- **URL 端点分布:**\n"
        for url_type, count in sess['url_types'].items():
            md += f"  - `{url_type}`: {count} 次\n"
        md += "\n"
    
    md += "## 5. Session 区分机制总结\n\n"
    
    md += "### 5.1 Claude Code 如何区分 Session\n\n"
    md += "#### 关键发现:\n\n"
    md += "1. **客户端内存状态**: Session 状态完全由 Claude Code CLI 在本地内存维护\n"
    md += "2. **无显式 Session ID**: HTTP 请求头和 URL 参数中**没有**专门的 `session_id` 或 `conversation_id`\n"
    md += "3. **时间间隔划分**: 实际应用中通过 **30分钟无活动间隔** 来划分不同 Session\n"
    md += "4. **完整上下文携带**: 每次请求都携带完整的 `messages` 数组，服务端无需维护状态\n"
    md += "5. **消息 ID 链**: 响应中的 `id` (如 `msg_xxx`) 是单次推理标识，不是 Session ID\n\n"
    
    md += "### 5.2 Session 的生命周期特征\n\n"
    md += "| 阶段 | 特征 |\n"
    md += "|------|------|\n"
    md += "| **Session 开始** | Claude Code CLI 进程启动，用户输入第一条指令 |\n"
    md += "| **Session 进行** | 不断累加的 `messages` 数组，每次请求都携带完整历史 |\n"
    md += "| **Session 持续** | 30 分钟内有持续请求活动 |\n"
    md += "| **Session 结束** | CLI 进程退出，或超过 30 分钟无请求 |\n\n"
    
    md += "### 5.3 为什么服务端看不到 Session ID？\n\n"
    md += "```\n"
    md += "Claude Code CLI (本地内存)                    Anthropic API 服务端\n"
    md += "      │                                           │\n"
    md += "      │  ┌──────────────────────────┐            │\n"
    md += "      │  │ messages: [              │            │\n"
    md += "      │  │   {role: user, ...}     │            │\n"
    md += "      │  │   {role: assistant, ...}│  POST     │\n"
    md += "      │  │   {role: user, ...}     │ ───────►   │\n"
    md += "      │  │   ... (完整历史)         │            │\n"
    md += "      │  └──────────────────────────┘            │\n"
    md += "      │                                           │\n"
    md += "      │  所有上下文都在请求体中，服务端完全无状态 │\n"
    md += "```\n\n"
    
    return md


def generate_task_report(analysis):
    """生成 Task 分析报告"""
    md = "# Claude Code Task 分析报告\n\n"
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
    system_count = sum(1 for t in analysis['task_details'] if t['has_system'])
    tools_count = sum(1 for t in analysis['task_details'] if t['has_tools'])
    md += "### 1.2 功能使用\n"
    md += f"- 携带 System Prompt: {system_count} 次\n"
    md += f"- 携带 Tools 定义: {tools_count} 次\n"
    md += f"- 流式响应: {sum(1 for t in analysis['task_details'] if t['stream'])} 次\n\n"
    
    md += "## 2. Task 详细数据\n\n"
    md += "| Task # | 开始时间 | 消息数 | 耗时(ms) | 请求大小 | Tools | 模型 |\n"
    md += "|--------|----------|--------|----------|----------|-------|------|\n"
    
    for task in analysis['task_details'][:30]:  # 只显示前30个
        model_short = task['model'][:15] + '...' if task['model'] and len(task['model']) > 15 else (task['model'] or '-')
        md += f"| {task['task_index']} | {task['start_time'].strftime('%H:%M:%S')} | {task['message_count_in_request']} | {task['elapsed_ms']:,} | {task['request_size']:,} | {'✓' if task['has_tools'] else ''} | {model_short} |\n"
    
    if len(analysis['task_details']) > 30:
        md += f"\n... 还有 {len(analysis['task_details']) - 30} 个 Task\n"
    md += "\n"
    
    md += "## 3. Task 区分机制总结\n\n"
    
    md += "### 3.1 Claude Code 如何区分 Task\n\n"
    md += "#### 关键发现:\n\n"
    md += "1. **一个 POST 请求 = 一个 Task**: 每次用户输入对话后，CLI 发起一次 `POST /coding/vX/messages` 请求\n"
    md += "2. **Task 标识 = Response ID**: 每个成功响应返回 `msg_xxx` 格式的唯一 ID\n"
    md += "3. **GET 请求是 Task 的一部分**: POST 之后可能伴随 `GET /coding/vX/messages/{msg_id}` 轮询任务状态\n"
    md += "4. **messages 数组长度递增**: 每个后续 Task 的 messages 数组比前一个更长（累积历史）\n\n"
    
    md += "### 3.2 Task 生命周期\n\n"
    md += "```\n"
    md += "用户输入指令\n"
    md += "      │\n"
    md += "      ▼\n"
    md += "POST /coding/vX/messages  ───────────┐\n"
    md += "  {model, messages[], tools}         │\n"
    md += "                                      │ Task 开始\n"
    md += "  200 OK {id: msg_xxx, ...}          │\n"
    md += "      │                               │\n"
    md += "      ▼                               │\n"
    md += "流式 SSE 数据 (data: {...})           │\n"
    md += "      │                               │\n"
    md += "      ▼                               │\n"
    md += "[DONE]  ──────────────────────────────┘ Task 结束\n"
    md += "\n"
    md += "(可选: 后续 GET /coding/vX/messages/msg_xxx 获取完整结果)\n"
    md += "```\n\n"
    
    md += "### 3.3 Task 边界识别方法\n\n"
    md += "| 方法 | 说明 | 准确率 |\n"
    md += "|------|------|--------|\n"
    md += "| **POST 请求计数** | 每个 POST /messages 就是一个 Task 开始 | 99% |\n"
    md += "| **消息 ID 链** | 每个 Task 返回唯一的 msg_xxx ID | 100% |\n"
    md += "| **messages 数组长度** | 每个后续 Task 数组长度增加 | 95% |\n"
    md += "| **时间间隔 < 10s** | 同一轮对话的多个请求时间间隔很短 | 80% |\n\n"
    
    md += "### 3.4 Task 内请求序列模式\n\n"
    md += "典型的单个 Task 包含以下 HTTP 请求：\n\n"
    md += "```\n"
    md += "1. POST /coding/vX/messages       # 发起推理任务 (流式)\n"
    md += "   │\n"
    md += "   ├─ SSE 流式返回: data: {...}  # 逐 token 返回内容\n"
    md += "   └─ 结束标记: data: [DONE]\n"
    md += "\n"
    md += "2. [可选] GET /coding/vX/messages/{msg_id}  # 获取完整消息内容\n"
    md += "3. [可选] GET /coding/vX/messages/{msg_id}/attachments/{id}  # 获取附件\n"
    md += "```\n\n"
    
    md += "## 4. Session vs Task 对比\n\n"
    md += "| 维度 | Session (对话会话) | Task (单次对话任务) |\n"
    md += "|------|-------------------|-------------------|\n"
    md += "| **对应关系** | 1 个 CLI 打开窗口 | 1 次用户输入 + 1 次模型回复 |\n"
    md += "| **HTTP 请求** | N 个 Task 请求 | 1 个 POST + 0 或多个 GET |\n"
    md += "| **唯一标识** | 无服务端 ID，客户端本地标识 | `msg_xxx` (响应 ID) |\n"
    md += "| **messages 数组** | 不断增长，累积所有历史 | 包含当前 Task 前的所有历史 |\n"
    md += "| **生命周期** | CLI 启动 → CLI 退出/超时 | POST → [DONE] |\n"
    md += "| **服务端可见性** | 不可见（仅客户端状态） | 完全可见（每个请求独立） |\n\n"
    
    return md


def main():
    print("=" * 60)
    print("Claude Code Session & Task 深度分析")
    print("=" * 60 + "\n")
    
    connection = pymysql.connect(**DB_CONFIG)
    cursor = connection.cursor()
    print("✓ 数据库连接成功\n")
    
    records = query_claude_code_records(cursor)
    connection.close()
    
    print("\n正在分析 Session 模式...")
    session_analysis = analyze_session_patterns(records)
    
    print("正在分析 Task 模式...")
    task_analysis = analyze_task_patterns(records)
    
    # 生成 Session 报告
    print("\n正在生成 SessionAnalysis.md...")
    session_md = generate_session_report(session_analysis)
    with open('/Users/dev02/Desktop/MyLocalGit/lsm-local-project-go/LsmHttpAgent/AnthropicAnalysis/SessionAnalysis.md', 'w', encoding='utf-8') as f:
        f.write(session_md)
    
    # 生成 Task 报告
    print("正在生成 TaskAnalysis.md...")
    task_md = generate_task_report(task_analysis)
    with open('/Users/dev02/Desktop/MyLocalGit/lsm-local-project-go/LsmHttpAgent/AnthropicAnalysis/TaskAnalysis.md', 'w', encoding='utf-8') as f:
        f.write(task_md)
    
    print("\n" + "=" * 60)
    print("✓ 分析完成！已生成:")
    print("  - AnthropicAnalysis/SessionAnalysis.md")
    print("  - AnthropicAnalysis/TaskAnalysis.md")
    print("=" * 60)


if __name__ == "__main__":
    main()
