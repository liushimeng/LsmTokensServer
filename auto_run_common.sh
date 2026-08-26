#!/usr/bin/env bash
# auto_run_common.sh
# ---------------------------------------------------------------
# 自动化脚本公共库（被 AutoDebugTestReport.sh / AutoTestAndSaveReport.sh
# source 引用）。
#
# 职责（与 agent_cli_common.sh 严格分工）：
#   - agent_cli_common.sh: Agent CLI 选择 + 调用（pick_agent / run_agent_with_prompt）
#   - auto_run_common.sh:   报告 glob 单一事实源 + 脚本样板（日志头 / prompt 定位 /
#                           git add/commit 封装 / 后台启动封装）
#                           + 统一日志工具（§20260821-01）
#
# 关键规约：
#   - LsmTokensServer 单一工程：主报告 TestReport/、子工程工具报告 go-web-debug-tool/UseReport/。
#   - 时间戳格式：`YYYYMMDD_HHMMSS`，精度到秒。
#   - 日志规约（§20260821-01）：
#     * 单次运行日志 `logs/auto_<类型>_<Agent名>_<时间戳>.log` —— 文件名含 Agent 程序名，
#       记录启动日志头 + Agent 全程 stdout/stderr + 各阶段时间戳。
#     * 运行索引日志 `logs/auto_run_index.log` —— 每次运行追加 key=value 单行，
#       记录脚本名 / Agent 程序名 / 事件 / 时间 / PID / 退出码，便于事后 grep 审计。
#     * 旧日志保留期缺省 30 天（LOG_RETAIN_DAYS 可覆盖），由 cleanup_old_logs 清理。
#
# 用法（在调用脚本中）：
#   source "${PROJECT_DIR}/auto_run_common.sh"
#   SCRIPT_TAG="AutoTestAndSaveReport"
#   print_section_header "${TAG}" "${PROMPT_FILE}" "${LOG_FILE}" "${PROJECT_DIR}" "${SELECTED_AGENT}"
#   PROMPT_FILE="$(locate_prompt_file "${PROMPT_FILE_NAME}")"
#   git_add_safe "TestReport/自动化测试报告_*.md"      # 单路径 add 容错封装
#   git_commit_chinese "测试" "${TS}" "${SCRIPT_TAG}"   # 中文 commit 封装
#   start_agent_in_background "<log_file>" "<bash_script>"   # nohup setsid & disown 封装
#   bg_log "${TAG}" "message"                 # 后台段带时间戳日志
#   append_run_index script=X agent=Y event=Z # 运行索引日志追加一行
#   cleanup_old_logs                          # 清理超过保留期的旧日志
#   any_pending_report "${PROJECT_DIR}"       # 扫描待处理报告（主 + 子工程）
# ---------------------------------------------------------------

# ---------- 报告 Glob 单一事实源（LsmTokensServer 单一工程） ----------
# 主报告：TestReport/ 下自动化测试报告 / 协议抓包分析报告
# 子工程工具报告：go-web-debug-tool/UseReport/ 下测试工具使用报告
REPORT_GLOBS_MAIN=(
    "自动化测试报告_*.md"
    "协议抓包分析报告_*.md"
)

REPORT_GLOBS_SUB=(
    "测试工具使用报告_*.md"
)

# ---------- 统一日志工具（§20260821-01 新增） ----------

# bg_log <tag> <message...>
# 后台段日志助手：start_agent_in_background 已把后台 shell 的 stdout/stderr
# 重定向到本次运行日志文件，此处仅负责补上统一时间戳前缀：
#   [2026-08-21 12:00:00] [AutoDebugTestReport] claude 退出码 : 0
bg_log() {
    local tag="$1"
    shift
    echo "[$(date '+%F %T')] [${tag}] $*"
}

# append_run_index <key=value> ...
# 运行索引日志：向 ${PROJECT_DIR}/logs/auto_run_index.log 追加一行，
# 格式 `[YYYY-MM-DD HH:MM:SS] key=value key=value ...`。
append_run_index() {
    local idx_dir="${PROJECT_DIR:-$(pwd)}/logs"
    local idx_file="${idx_dir}/auto_run_index.log"
    local line="[$(date '+%F %T')]"
    local kv
    for kv in "$@"; do
        line+=" ${kv}"
    done
    mkdir -p "${idx_dir}" 2>/dev/null || true
    printf '%s\n' "${line}" >> "${idx_file}" 2>/dev/null || true
}

# cleanup_old_logs [retain_days]
# 清理 logs/ 下超过保留期的 *.log 运行日志。
cleanup_old_logs() {
    local retain_days="${1:-${LOG_RETAIN_DAYS:-30}}"
    local log_dir="${PROJECT_DIR:-$(pwd)}/logs"
    [[ -d "${log_dir}" ]] || return 0
    find "${log_dir}" -maxdepth 1 -type f -name '*.log' \
        ! -name 'auto_run_index.log' \
        -mtime +"${retain_days}" -delete 2>/dev/null || true
}

# ---------- print_section_header <tag> <prompt_file> <log_file> <workdir> <agent> ----------
print_section_header() {
    local tag="$1"
    local prompt_file="$2"
    local log_file="$3"
    local workdir="$4"
    local agent="$5"
    local git_head avail_agents
    git_head="$(git -C "${workdir}" rev-parse --short HEAD 2>/dev/null || echo 'unknown')"
    avail_agents="$(list_available_agents 2>/dev/null | tr '\n' ' ')"
    {
        echo "============================================================"
        echo "[${tag}] 启动时间 : $(date '+%F %T')"
        echo "[${tag}] 工作目录 : ${workdir}"
        echo "[${tag}] 提示词文件: ${prompt_file}"
        echo "[${tag}] 日志文件  : ${log_file}"
        echo "[${tag}] 选中 Agent : ${agent}"
        echo "[${tag}] Agent 二进制: $(command -v "$(agent_binary_of "${agent}")" 2>/dev/null || echo 'NOT FOUND')"
        echo "[${tag}] 可用 Agents: ${avail_agents:-none}"
        echo "[${tag}] Git HEAD  : ${git_head}"
        echo "[${tag}] Bash 版本 : ${BASH_VERSION:-unknown}"
        echo "============================================================"
    } >> "${log_file}"
}

# ---------- locate_prompt_file <prompt_name> ----------
# 三层定位 prompt 文件:PROJECT_DIR 绝对路径 → ./相对 → $PWD 相对。
locate_prompt_file() {
    local prompt_name="$1"
    local project_dir="${PROJECT_DIR:-$(pwd)}"
    local candidate
    for candidate in \
        "${project_dir}/${prompt_name}" \
        "./${prompt_name}" \
        "${PWD}/${prompt_name}"; do
        if [[ -f "${candidate}" ]]; then
            readlink -f "${candidate}"
            return 0
        fi
    done
    echo "[auto_run_common] [ERROR] 找不到 ${prompt_name},已检查:" >&2
    echo "  - ${project_dir}/${prompt_name}" >&2
    echo "  - ./${prompt_name}" >&2
    echo "  - ${PWD}/${prompt_name}" >&2
    return 1
}

# ---------- git_add_safe <glob_path> ----------
# 单路径 git add,失败不阻塞(目录不存在 / 被 .gitignore 整体忽略等)。
git_add_safe() {
    local path_glob="$1"
    git add -- "${path_glob}" 2>/dev/null || true
}

# ---------- git_commit_chinese <type_zh> <commit_ts> <source_tag> [extra_msg ...] ----------
# 中文 commit 封装（LsmTokensServer 单一工程版）。
#   type_zh: "测试" / "截图" / "修复" / "处理"
#   commit_ts: 时间戳(YYYYMMDD_HHMMSS)
#   source_tag: 来源脚本名(如 AutoTestAndSaveReport.sh)
#   extra_msg: 可选追加 commit message 行
git_commit_chinese() {
    local type_zh="$1"
    local commit_ts="$2"
    local source_tag="${3:-auto_run}"
    shift 3 2>/dev/null || true
    local -a msg_args=("-m" "${type_zh}: LsmTokensServer ${type_zh}报告 ${commit_ts} 已完成")
    msg_args+=("-m" "自动提交由 ${source_tag} 生成")
    if [[ $# -gt 0 ]]; then
        msg_args+=("-m" "包含: $*")
    fi
    git commit "${msg_args[@]}"
}

# ---------- start_agent_in_background <log_file> <bash_script> ----------
# nohup setsid bash -c "<script>" &; disown 封装。
start_agent_in_background() {
    local log_file="$1"
    local bash_script="$2"
    nohup setsid bash -c "${bash_script}" >> "${log_file}" 2>&1 </dev/null &
    local pid=$!
    disown "${pid}" 2>/dev/null || true
    echo "${pid}"
}

# ---------- any_pending_report <project_dir> ----------
# 扫描主工程 TestReport/ + 子工程 go-web-debug-tool/UseReport/ 中
# 文件名不含 `_无问题` 后缀的待处理报告。
# 输出:首个命中文件名(或空串)。配套 .sh 自行判空退出。
any_pending_report() {
    local project_dir="${1:-${PROJECT_DIR:-$(pwd)}}"
    local main_report sub_report glob

    # 主工程：遍历所有主报告 glob
    for glob in "${REPORT_GLOBS_MAIN[@]}"; do
        main_report="$(find "${project_dir}/TestReport" -maxdepth 1 -name "${glob}" ! -name '*_无问题.md' 2>/dev/null | head -1)"
        if [[ -n "${main_report}" ]]; then
            echo "${main_report}"
            return 0
        fi
    done

    # 子工程：UseReport 工具报告
    if [[ -d "${project_dir}/go-web-debug-tool/UseReport" ]]; then
        for glob in "${REPORT_GLOBS_SUB[@]}"; do
            sub_report="$(find "${project_dir}/go-web-debug-tool/UseReport" -maxdepth 1 \
                -name "${glob}" ! -name '*_无问题.md' 2>/dev/null | head -1)"
            if [[ -n "${sub_report}" ]]; then
                echo "${sub_report}"
                return 0
            fi
        done
    fi

    echo ""
}
