#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
Anthropic 协议分析脚本
从数据库中读取 Anthropic 相关的 HTTP 代理数据，分析协议规律
"""

import pymysql
import json
import re
import base64
import gzip
from datetime import datetime
from collections import defaultdict, Counter

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

# 分表数量
SUB_TABLE_NUM = 8


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
            except:
                pass
        
        # 尝试直接作为 UTF-8
        try:
            return decoded.decode('utf-8', errors='replace')
        except:
            pass
        
        # 如果都失败，返回原始 base64 字符串（截断）
        return body[:1000]
    except:
        # 不是 base64，直接返回
        return body


def query_all_anthropic_records(cursor):
    """查询所有 Anthropic 相关的记录（响应状态为 200）"""
    all_records = []
    
    # 遍历所有分表
    for i in range(SUB_TABLE_NUM):
        table_name = f"TAgentHttpTransactionDataItem_{i:02d}"
        
        # 查询该表中 Anthropic 协议且响应状态为 200 的记录
        sql = f"""
        SELECT * FROM {table_name}
        WHERE agent_src_protocol_type = 1  -- AgentProtocolType_Anthropic = 1
          AND response_status LIKE '200%'
        ORDER BY created_at ASC
        """
        cursor.execute(sql)
        records = cursor.fetchall()
        print(f"表 {table_name}: 找到 {len(records)} 条 Anthropic 记录")
        
        all_records.extend(records)
    
    print(f"\n总共找到 {len(all_records)} 条 Anthropic 记录（响应状态 200）")
    return all_records


def extract_session_info(request_body, response_body, request_headers):
    """从请求和响应中提取会话信息"""
    session_info = {
        'has_session_id': False,
        'session_id': None,
        'conversation_id': None,
        'message_id': None,
        'prompt_type': None,
        'model': None,
        'stream': False,
        'max_tokens': None,
        'has_system_prompt': False,
        'user_message_count': 0,
        'assistant_message_count': 0,
        'tool_use': False,
        'tool_name': None,
    }
    
    try:
        # 解码请求体
        request_body_decoded = decode_body(request_body)
        response_body_decoded = decode_body(response_body)
        
        # 解析请求体
        if request_body_decoded:
            # 可能是多行 SSE，取第一行尝试解析
            first_line = request_body_decoded.split('\n')[0]
            try:
                req_json = json.loads(first_line)
            except:
                try:
                    req_json = json.loads(request_body_decoded)
                except:
                    # 尝试清理 SSE 前缀
                    if request_body_decoded.startswith('data: '):
                        cleaned = request_body_decoded[6:]
                        try:
                            req_json = json.loads(cleaned)
                        except:
                            req_json = {}
                    else:
                        req_json = {}
            
            session_info['model'] = req_json.get('model')
            session_info['stream'] = req_json.get('stream', False)
            session_info['max_tokens'] = req_json.get('max_tokens')
            
            # 检查是否有 system prompt
            if 'system' in req_json:
                session_info['has_system_prompt'] = True
            
            # 分析消息 (Anthropic 格式)
            if 'messages' in req_json:
                messages = req_json['messages']
                if isinstance(messages, list):
                    for msg in messages:
                        role = msg.get('role')
                        if role == 'user':
                            session_info['user_message_count'] += 1
                        elif role == 'assistant':
                            session_info['assistant_message_count'] += 1
                        
                        # 检查是否有工具使用
                        content = msg.get('content', '')
                        if isinstance(content, list):
                            for item in content:
                                if isinstance(item, dict) and item.get('type') == 'tool_use':
                                    session_info['tool_use'] = True
                                    session_info['tool_name'] = item.get('name')
            
            # OpenAI 格式兼容性检查
            if 'choices' not in req_json and session_info['user_message_count'] == 0:
                # 可能是 OpenAI 格式
                pass
            
            # 检查是否是单次任务还是多轮会话
            if session_info['user_message_count'] == 1 and session_info['assistant_message_count'] == 0:
                session_info['prompt_type'] = 'single_turn'
            elif session_info['assistant_message_count'] > 0:
                session_info['prompt_type'] = 'multi_turn'
        
        # 解析响应头中的会话信息
        if request_headers:
            try:
                headers_json = json.loads(request_headers)
                for key in headers_json.keys():
                    key_lower = key.lower()
                    if 'session' in key_lower or 'conversation' in key_lower:
                        session_info['has_session_id'] = True
                        session_info['session_id'] = headers_json.get(key)
            except:
                pass
        
        # 解析响应体
        if response_body_decoded:
            # 处理流式响应（可能包含多个 JSON 对象）
            lines = response_body_decoded.split('\n')
            for line in lines:
                if line.startswith('data: '):
                    data_str = line[6:]
                    if data_str.strip() == '[DONE]':
                        session_info['stream'] = True
                        continue
                    try:
                        resp_json = json.loads(data_str)
                        if 'id' in resp_json and session_info['message_id'] is None:
                            session_info['message_id'] = resp_json['id']
                        if 'conversation_id' in resp_json:
                            session_info['conversation_id'] = resp_json['conversation_id']
                        if 'model' in resp_json:
                            session_info['model'] = resp_json['model']
                        if 'object' in resp_json and 'chunk' in resp_json['object']:
                            session_info['stream'] = True
                    except:
                        pass
                elif line.strip() and session_info['message_id'] is None:
                    # 尝试非流式 JSON
                    try:
                        resp_json = json.loads(line)
                        if 'id' in resp_json:
                            session_info['message_id'] = resp_json['id']
                        if 'model' in resp_json:
                            session_info['model'] = resp_json['model']
                    except:
                        pass
    
    except Exception as e:
        pass
    
    return session_info


def analyze_protocol_patterns(records):
    """分析 Anthropic 协议模式"""
    analysis = {
        'total_records': len(records),
        'service_distribution': Counter(),
        'model_distribution': Counter(),
        'method_distribution': Counter(),
        'url_patterns': Counter(),
        'stream_vs_nonstream': Counter(),
        'prompt_type_distribution': Counter(),
        'has_system_prompt': 0,
        'tool_use_count': 0,
        'session_correlation': defaultdict(list),
        'time_series': [],
        'message_length_stats': {
            'request_body_lengths': [],
            'response_body_lengths': [],
        },
        'elapsed_ms_stats': [],
    }
    
    for record in records:
        # 基本统计
        analysis['service_distribution'][record['agent_service_name']] += 1
        analysis['method_distribution'][record['request_method']] += 1
        
        # URL 模式分析
        url = record['request_url']
        # 提取 URL 路径模式（去掉查询参数）
        url_path = url.split('?')[0]
        # 进一步归一化
        url_pattern = re.sub(r'/v\d+/', '/vX/', url_path)  # 替换版本号
        url_pattern = re.sub(r'messages/[^/]+', 'messages/{id}', url_pattern)  # 替换消息 ID
        analysis['url_patterns'][url_pattern] += 1
        
        # 提取会话信息
        session_info = extract_session_info(
            record['request_body'],
            record['response_body'],
            record['request_headers']
        )
        
        if session_info['model']:
            analysis['model_distribution'][session_info['model']] += 1
        
        analysis['stream_vs_nonstream']['stream' if session_info['stream'] else 'non_stream'] += 1
        
        if session_info['prompt_type']:
            analysis['prompt_type_distribution'][session_info['prompt_type']] += 1
        
        if session_info['has_system_prompt']:
            analysis['has_system_prompt'] += 1
        
        if session_info['tool_use']:
            analysis['tool_use_count'] += 1
        
        # 长度统计（解码后）
        decoded_req = decode_body(record['request_body'])
        decoded_resp = decode_body(record['response_body'])
        analysis['message_length_stats']['request_body_lengths'].append(len(decoded_req))
        analysis['message_length_stats']['response_body_lengths'].append(len(decoded_resp))
        analysis['elapsed_ms_stats'].append(record['elapsed_ms'])
        
        # 时间序列
        analysis['time_series'].append({
            'time': record['created_at'],
            'service': record['agent_service_name'],
            'model': session_info['model'],
            'stream': session_info['stream'],
        })
    
    return analysis


def generate_markdown_report(analysis, sample_records):
    """生成 Markdown 分析报告"""
    md_content = "# Anthropic 协议分析报告\n\n"
    md_content += f"**生成时间:** {datetime.now().strftime('%Y-%m-%d %H:%M:%S')}\n\n"
    md_content += f"**分析记录数:** {analysis['total_records']} 条（响应状态 200）\n\n"
    
    md_content += "## 1. 基本统计\n\n"
    
    md_content += "### 1.1 服务分布\n"
    for service, count in analysis['service_distribution'].most_common():
        md_content += f"- `{service}`: {count} 次\n"
    md_content += "\n"
    
    md_content += "### 1.2 模型分布\n"
    if analysis['model_distribution']:
        for model, count in analysis['model_distribution'].most_common():
            md_content += f"- `{model}`: {count} 次\n"
    else:
        md_content += "- (部分模型信息在响应体中，已在流式响应时识别)\n"
    md_content += "\n"
    
    md_content += "### 1.3 HTTP 方法分布\n"
    for method, count in analysis['method_distribution'].most_common():
        md_content += f"- `{method}`: {count} 次\n"
    md_content += "\n"
    
    md_content += "### 1.4 URL 调用模式\n"
    for pattern, count in analysis['url_patterns'].most_common():
        md_content += f"- `{pattern}`: {count} 次\n"
    md_content += "\n"
    
    md_content += "## 2. 流式 vs 非流式\n\n"
    stream_count = analysis['stream_vs_nonstream'].get('stream', 0)
    non_stream_count = analysis['stream_vs_nonstream'].get('non_stream', 0)
    total = analysis['total_records']
    md_content += f"- **流式响应 (stream=true):** {stream_count} 次 ({stream_count/total*100:.1f}%)\n"
    md_content += f"- **非流式响应 (stream=false):** {non_stream_count} 次 ({non_stream_count/total*100:.1f}%)\n\n"
    
    md_content += "## 3. 会话模式分析\n\n"
    
    md_content += "### 3.1 单轮 vs 多轮对话\n"
    if analysis['prompt_type_distribution']:
        for pt, count in analysis['prompt_type_distribution'].most_common():
            md_content += f"- `{pt}`: {count} 次\n"
    else:
        md_content += "- (主要为 OpenAI 格式，messages 数组在请求体中)\n"
    md_content += "\n"
    
    md_content += "### 3.2 System Prompt 使用情况\n"
    md_content += f"- 包含 System Prompt 的请求: {analysis['has_system_prompt']} 次\n\n"
    
    md_content += "### 3.3 工具调用 (Tool Use) 情况\n"
    md_content += f"- 包含工具调用的请求: {analysis['tool_use_count']} 次\n\n"
    
    md_content += "## 4. 消息长度统计（解码后）\n\n"
    req_lengths = analysis['message_length_stats']['request_body_lengths']
    resp_lengths = analysis['message_length_stats']['response_body_lengths']
    
    md_content += "### 4.1 请求体长度\n"
    md_content += f"- 平均: {sum(req_lengths)/len(req_lengths):.1f} 字节\n"
    md_content += f"- 最小: {min(req_lengths)} 字节\n"
    md_content += f"- 最大: {max(req_lengths)} 字节\n\n"
    
    md_content += "### 4.2 响应体长度\n"
    md_content += f"- 平均: {sum(resp_lengths)/len(resp_lengths):.1f} 字节\n"
    md_content += f"- 最小: {min(resp_lengths)} 字节\n"
    md_content += f"- 最大: {max(resp_lengths)} 字节\n\n"
    
    md_content += "## 5. 响应时间统计\n\n"
    elapsed = analysis['elapsed_ms_stats']
    md_content += f"- 平均: {sum(elapsed)/len(elapsed):.1f} ms\n"
    md_content += f"- 最小: {min(elapsed)} ms\n"
    md_content += f"- 最大: {max(elapsed)} ms\n\n"
    
    md_content += "## 6. Anthropic 协议核心规律总结\n\n"
    
    md_content += "### 6.1 每次 Session 的特点\n"
    md_content += "- Anthropic API **本身不维护服务器端会话状态**，每次请求都是独立的\n"
    md_content += "- 会话上下文完全由 **客户端维护**，通过 `messages` 数组传递历史对话\n"
    md_content += "- `messages` 数组是交替的 `user` 和 `assistant` 消息，构成完整对话历史\n"
    md_content += "- 多轮对话 = 每次请求携带完整的历史消息数组\n"
    md_content += "- 单轮对话 = `messages` 数组只包含 1 条 `user` 消息\n\n"
    
    md_content += "### 6.2 每次人工提示词任务的特点\n"
    md_content += "- 每个请求对应 **一次模型推理任务**\n"
    md_content += "- 请求结构: `model` + `max_tokens` + `messages` 数组 (+ 可选 `system`)\n"
    md_content += "- 流式响应通过 SSE (Server-Sent Events) 逐块返回，格式为 `data: {...}`\n"
    md_content += "- 非流式响应一次性返回完整 JSON 结果\n"
    md_content += "- 响应包含唯一的消息 `id`，可用于追踪和审计\n"
    md_content += "- Claude Code 的 `/coding/vX/messages` 是特殊端点，专用于编码任务\n\n"
    
    md_content += "### 6.3 Session 与 人工提示词任务 的区分\n\n"
    md_content += "| 维度 | Session（会话） | 人工提示词任务 |\n"
    md_content += "|------|----------------|----------------|\n"
    md_content += "| 存储位置 | 客户端侧（Claude Code 等工具） | 服务器单次请求处理 |\n"
    md_content += "| 标识方式 | 无原生 Session ID，由客户端完全管理 | 每个请求有唯一 message_id |\n"
    md_content += "| 数据携带 | 通过 messages 数组携带完整历史 | 仅处理当前请求中的消息 |\n"
    md_content += "| 生命周期 | 跨多个 HTTP 请求，随客户端进程存在 | 单个 HTTP 请求生命周期 |\n"
    md_content += "| 状态维护 | 客户端完全负责上下文管理 | 服务端无状态（stateless） |\n"
    md_content += "| 上下文膨胀 | 每次请求都会增长，直到 token 超限 | 独立计算资源消耗 |\n\n"
    
    md_content += "### 6.4 核心规律总结\n\n"
    md_content += "1. **无状态设计**: Anthropic API 是完全无状态的，服务端不保存任何对话上下文\n"
    md_content += "2. **上下文传递**: 所有对话历史通过请求体中的 `messages` 数组完整传递给服务端\n"
    md_content += "3. **消息交替**: `messages` 数组必须严格交替 user/assistant 角色\n"
    md_content += "4. **流式优先**: 实际使用中，大部分编码任务使用流式响应获得更好体验\n"
    md_content += "5. **System Prompt**: 全局指令通过 `system` 参数单独传递（不在 messages 数组中）\n"
    md_content += "6. **工具调用**: 支持 Function Calling / Tool Use，通过消息内容中的 `tool_use` 类型传递\n"
    md_content += "7. **端点差异**: `/coding/vX/messages` (Claude Code) vs `/v1/messages` (标准 API)\n"
    md_content += "8. **数据压缩**: 请求/响应体使用 Base64 + Gzip 压缩存储，减少数据库空间占用\n\n"
    
    md_content += "## 7. 样本数据示例（已解码）\n\n"
    
    for i, record in enumerate(sample_records[:3]):  # 只显示前3条样本
        md_content += f"### 样本 {i+1}\n"
        md_content += f"- **服务名**: {record['agent_service_name']}\n"
        md_content += f"- **时间**: {record['created_at']}\n"
        md_content += f"- **URL**: {record['request_url']}\n"
        md_content += f"- **耗时**: {record['elapsed_ms']} ms\n\n"
        
        # 解码并截断显示请求体
        decoded_req = decode_body(record['request_body'])
        req_body = decoded_req[:800] + "..." if len(decoded_req) > 800 else decoded_req
        md_content += "**请求体（已解码，截断）:**\n```json\n" + req_body + "\n```\n\n"
        
        # 解码并截断显示响应体
        decoded_resp = decode_body(record['response_body'])
        resp_body = decoded_resp[:800] + "..." if len(decoded_resp) > 800 else decoded_resp
        md_content += "**响应体（已解码，截断）:**\n```json\n" + resp_body + "\n```\n\n"
        md_content += "---\n\n"
    
    return md_content


def main():
    print("=" * 60)
    print("Anthropic 协议分析工具")
    print("=" * 60 + "\n")
    
    try:
        # 连接数据库
        connection = pymysql.connect(**DB_CONFIG)
        cursor = connection.cursor()
        print("✓ 数据库连接成功\n")
        
        # 查询所有 Anthropic 记录
        records = query_all_anthropic_records(cursor)
        
        if not records:
            print("\n没有找到 Anthropic 相关的记录！")
            return
        
        # 分析协议模式
        print("\n正在分析协议模式...")
        analysis = analyze_protocol_patterns(records)
        
        # 生成报告
        print("正在生成分析报告...")
        md_report = generate_markdown_report(analysis, records)
        
        # 保存报告
        output_path = "/Users/dev02/Desktop/MyLocalGit/lsm-local-project-go/LsmHttpAgent/AnthropicAnalysis/OutputAnalysis.md"
        with open(output_path, 'w', encoding='utf-8') as f:
            f.write(md_report)
        
        print(f"\n✓ 分析报告已保存到: {output_path}")
        print("\n" + "=" * 60)
        print("分析完成！")
        print("=" * 60)
        
    except Exception as e:
        print(f"\n✗ 错误: {e}")
        import traceback
        traceback.print_exc()
    finally:
        if 'connection' in locals() and connection:
            connection.close()


if __name__ == "__main__":
    main()
