#!/bin/bash
#
# rebuild_restart_app.sh - LsmTokensServer 编译与重启脚本（Claude Code 标准入口）
# 用法: ./scripts/rebuild_restart_app.sh [options]
#
# 规则: 所有涉及编译、启动的操作，必须通过此脚本完成，禁止直接执行 go build 或 nohup。
# 功能: 自动编译后端(ServerGo) + 前端(ClientWeb)，部署前端产物，启动并验证服务。
# 注意: 迁移期内旧服务 LsmHttpAgent 仍在运行且端口相同，请使用 --build-only（默认推荐）
#       仅编译不启动；待全部功能迁移并人工确认后，再执行完整 restart 切换。
# 兼容: Linux / macOS

set -e

APP_NAME="LsmTokensServer"
CONFIG_FILE="LsmTokensServer.conf"

# 获取脚本所在目录（兼容 macOS 和 Linux）
get_script_dir() {
    local src="${BASH_SOURCE[0]}"
    while [ -L "$src" ]; do
        local dir="$(cd "$(dirname "$src")" && pwd)"
        src="$(readlink "$src")"
        [[ "$src" != /* ]] && src="$dir/$src"
    done
    cd "$(dirname "$src")/.." && pwd   # scripts/ 的上一级 = 工程根目录
}

PROJECT_DIR="$(get_script_dir)"
SERVER_DIR="$PROJECT_DIR/ServerGo"
WEB_DIR="$PROJECT_DIR/ClientWeb"

# 颜色输出
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

log_info()  { echo -e "${GREEN}[INFO]${NC}  $1"; }
log_warn()  { echo -e "${YELLOW}[WARN]${NC}  $1"; }
log_error() { echo -e "${RED}[ERROR]${NC} $1"; }
log_step()  { echo -e "${BLUE}[STEP]${NC}  $1"; }

# 查找所有 LsmTokensServer 进程 PID
find_pids() {
    local pids=""
    if command -v pgrep >/dev/null 2>&1; then
        pids=$(pgrep -f "$APP_NAME" 2>/dev/null || true)
    fi
    if [ -z "$pids" ] && command -v ps >/dev/null 2>&1; then
        pids=$(ps aux 2>/dev/null | grep -E "[./]$APP_NAME" | awk '{print $2}' | tr '\n' ' ' || true)
    fi
    echo "$pids" | tr ' ' '\n' | grep -v '^$' || true
}

# 检查端口是否被占用
check_port_free() {
    local port=$1
    if command -v lsof >/dev/null 2>&1; then
        lsof -i :"$port" >/dev/null 2>&1 && return 1 || return 0
    elif command -v ss >/dev/null 2>&1; then
        ss -tlnp 2>/dev/null | grep -q ":$port " && return 1 || return 0
    elif command -v netstat >/dev/null 2>&1; then
        netstat -an 2>/dev/null | grep -q "\.$port " && return 1 || return 0
    else
        (echo >/dev/tcp/localhost/"$port") 2>/dev/null && return 1 || return 0
    fi
}

# 从配置文件读取端口
read_config_ports() {
    local conf="$PROJECT_DIR/$CONFIG_FILE"
    MANAGER_PORT=9101
    USER_PORT=29001
    AGENT_PORT=29000
    if [ -f "$conf" ]; then
        local mp up ap
        mp=$(grep '"managerWebListenPort"' "$conf" 2>/dev/null | sed -E 's/.*"managerWebListenPort"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/' || echo "")
        up=$(grep '"userWebListenPort"' "$conf" 2>/dev/null | sed -E 's/.*"userWebListenPort"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/' || echo "")
        ap=$(grep '"agentListenPort"' "$conf" 2>/dev/null | sed -E 's/.*"agentListenPort"[[:space:]]*:[[:space:]]*([0-9]+).*/\1/' || echo "")
        [ -n "$mp" ] && MANAGER_PORT="$mp"
        [ -n "$up" ] && USER_PORT="$up"
        [ -n "$ap" ] && AGENT_PORT="$ap"
    fi
}

# 验证服务是否可访问（端口 + HTTP 状态码）
verify_service() {
    local port=$1
    local name=$2
    local expected=$3
    local max_wait=${4:-10}
    local waited=0
    local code
    while [ $waited -lt $max_wait ]; do
        local target_path="/"
        local proto="http"
        if [ "$name" = "User Web" ]; then
            target_path="/UserLogin"
            if grep -q '"userWebUseHTTPS"[[:space:]]*:[[:space:]]*true' "$PROJECT_DIR/$CONFIG_FILE" 2>/dev/null; then
                proto="https"
            fi
        fi
        local probe_url="${proto}://localhost:${port}${target_path}"
        if [ "$proto" = "https" ]; then
            code=$(curl -sk -o /dev/null -w "%{http_code}" "$probe_url" 2>/dev/null || echo "000")
        else
            code=$(curl -s -o /dev/null -w "%{http_code}" "$probe_url" 2>/dev/null || echo "000")
        fi
        if echo "$code" | grep -qE "^($expected)$"; then
            log_info "$name OK (port $port, HTTP $code)"
            return 0
        fi
        sleep 1
        waited=$((waited + 1))
    done
    log_warn "$name check failed after ${max_wait}s: HTTP $code (port $port)"
    return 1
}

usage() {
    cat <<'EOF'
用法: ./scripts/rebuild_restart_app.sh [选项]

选项:
  --stop-only        仅停止所有运行中的实例，不编译不启动
  --build-only       仅编译前后端，不启动（迁移期默认推荐）
  --skip-web         跳过前端编译（仅编译后端）
  --test             编译前运行 go test ./...
  -h, --help         显示此帮助信息

默认行为:
  1. 编译前端 ClientWeb（npm ci + npm run build）并部署产物
  2. 编译后端 ServerGo（注入 buildTime）
  3. 查找并停止旧进程，等待端口释放
  4. 启动新实例
  5. 验证各服务端口是否正常响应

EOF
}

# ====== 参数解析 ======
MODE="restart"
RUN_TEST=false
SKIP_WEB=false

while [ $# -gt 0 ]; do
    case "$1" in
        --stop-only)     MODE="stop-only"; shift ;;
        --build-only)    MODE="build-only"; shift ;;
        --skip-web)      SKIP_WEB=true; shift ;;
        --test)          RUN_TEST=true; shift ;;
        -h|--help)       usage; exit 0 ;;
        *) log_error "未知参数: $1"; usage; exit 1 ;;
    esac
done

echo "========================================"
echo "  $APP_NAME Rebuild & Restart"
echo "  Mode: $MODE"
echo "  Project: $PROJECT_DIR"
echo "  $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================"
echo ""

read_config_ports

# ---- 仅停止模式 ----
if [ "$MODE" = "stop-only" ]; then
    OLD_PIDS=$(find_pids)
    if [ -n "$OLD_PIDS" ]; then
        log_step "Stopping all old processes..."
        echo "$OLD_PIDS" | while read -r pid; do
            [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
        done
        sleep 2
        still_running=$(find_pids)
        if [ -n "$still_running" ]; then
            echo "$still_running" | while read -r pid; do
                [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null || true
            done
        fi
        log_info "All old processes stopped"
    else
        log_info "No old process running"
    fi
    exit 0
fi

# ---- Step 1: 编译前端 ----
if [ "$SKIP_WEB" = false ] && [ -d "$WEB_DIR" ]; then
    log_step "Building frontend (ClientWeb)..."
    (cd "$WEB_DIR" && npm ci --no-fund --no-audit 2>/dev/null || npm install --no-fund --no-audit)
    (cd "$WEB_DIR" && npm run build)
    log_info "Frontend build OK -> $WEB_DIR/dist"
else
    log_info "Skip frontend build"
fi

# ---- Step 2: 运行测试（可选） ----
if [ "$RUN_TEST" = true ]; then
    log_step "Running backend tests..."
    (cd "$SERVER_DIR" && go test ./... ) && log_info "Tests passed" || { log_error "Tests failed, abort"; exit 1; }
    echo ""
fi

# ---- Step 3: 编译后端 ----
log_step "Building backend binary..."
BUILD_TIME=$(date '+%Y-%m-%d_%H:%M:%S')
(cd "$SERVER_DIR" && go build -ldflags "-X main.buildTime=$BUILD_TIME" -o "./$APP_NAME" .)

if [ ! -f "$SERVER_DIR/$APP_NAME" ]; then
    log_error "Build failed"
    exit 1
fi
log_info "Backend build OK (buildTime: $BUILD_TIME)"

# ---- 记录编译时间到日志文件 ----
BUILD_DATE_TIME_LOG="$PROJECT_DIR/${APP_NAME}BuildDateTime.log"
BUILD_DATE_TIME=$(date '+%Y-%m-%d %H:%M:%S')
if [ -f "$BUILD_DATE_TIME_LOG" ]; then
    { echo "$BUILD_DATE_TIME"; cat "$BUILD_DATE_TIME_LOG"; } > "$BUILD_DATE_TIME_LOG.tmp" && mv "$BUILD_DATE_TIME_LOG.tmp" "$BUILD_DATE_TIME_LOG"
else
    echo "$BUILD_DATE_TIME" > "$BUILD_DATE_TIME_LOG"
fi
log_info "Build time recorded to $BUILD_DATE_TIME_LOG"

# ---- 仅编译模式 ----
if [ "$MODE" = "build-only" ]; then
    log_info "Build-only mode, done.（迁移期默认，不启动、不占用端口）"
    exit 0
fi

# ---- Step 4: 检查配置文件 ----
if [ ! -f "$PROJECT_DIR/$CONFIG_FILE" ]; then
    if [ -f "$PROJECT_DIR/${CONFIG_FILE}.example" ]; then
        log_error "缺少 $CONFIG_FILE，请复制 ${CONFIG_FILE}.example 并填写实际配置（含敏感信息，勿提交 git）"
    else
        log_error "缺少 $CONFIG_FILE"
    fi
    exit 1
fi

# ---- Step 5: 停止旧进程 ----
OLD_PIDS=$(find_pids)
if [ -n "$OLD_PIDS" ]; then
    log_step "Stopping old processes..."
    echo "$OLD_PIDS" | while read -r pid; do
        [ -n "$pid" ] && kill "$pid" 2>/dev/null || true
    done
    wait_count=0
    while [ $wait_count -lt 15 ]; do
        remaining=$(find_pids)
        [ -z "$remaining" ] && break
        sleep 0.2
        wait_count=$((wait_count + 1))
    done
    still_running=$(find_pids)
    if [ -n "$still_running" ]; then
        log_warn "Force killing residual PIDs: $(echo "$still_running" | tr '\n' ' ')"
        echo "$still_running" | while read -r pid; do
            [ -n "$pid" ] && kill -9 "$pid" 2>/dev/null || true
        done
        sleep 0.5
    fi
    log_info "Old processes stopped"
else
    log_info "No old process to stop"
fi

# ---- Step 6: 等待端口释放 ----
log_step "Waiting for ports to be released..."
port_wait=0
while [ $port_wait -lt 20 ]; do
    if check_port_free "$MANAGER_PORT" && check_port_free "$USER_PORT" && check_port_free "$AGENT_PORT"; then
        break
    fi
    sleep 0.2
    port_wait=$((port_wait + 1))
done
if ! check_port_free "$MANAGER_PORT"; then
    log_warn "Port $MANAGER_PORT still in use (旧服务 LsmHttpAgent 可能仍在运行)，continuing anyway..."
fi

# ---- Step 7: 启动新实例 ----
log_step "Starting new instance..."
cd "$SERVER_DIR"
nohup "./$APP_NAME" -c "$PROJECT_DIR/$CONFIG_FILE" > "$PROJECT_DIR/lsmtokensserver.out.log" 2> "$PROJECT_DIR/lsmtokensserver.err.log" &
NEW_PID=$!
log_info "New PID: $NEW_PID"

# ---- Step 8: 验证 ----
echo ""
log_step "Verifying services..."
sleep 2

verify_service "$MANAGER_PORT" "Manager Web" "200" 10
verify_service "$USER_PORT" "User Web" "200|302" 10

check_port_free "$AGENT_PORT" && log_warn "Agent proxy port $AGENT_PORT not listening" || log_info "Agent proxy listening on port $AGENT_PORT"

echo ""
echo "========================================"
echo "  Rebuild & Restart Complete"
echo "  Binary: $SERVER_DIR/$APP_NAME"
echo "  PID: $NEW_PID"
echo "  Time: $(date '+%Y-%m-%d %H:%M:%S')"
echo "========================================"
