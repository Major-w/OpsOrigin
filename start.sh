#!/usr/bin/env bash
# OpsOrigin 一键启动脚本
# 用法：
#   ./start.sh            启动所有服务（依赖 + 后端 + 前端）
#   ./start.sh start      同上
#   ./start.sh stop        停止所有服务
#   ./start.sh restart     重启所有服务
#   ./start.sh status      查看运行状态
#   ./start.sh logs [b|f]  查看日志（b=后端, f=前端）
#   ./start.sh deps        仅启动依赖服务（PG/Dgraph/Redis）
#   ./start.sh backend     仅启动后端
#   ./start.sh frontend    仅启动前端

set -euo pipefail

# ---------- 路径与常量 ----------
ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$ROOT_DIR/deploy/docker-compose.yml"
ENV_FILE="$ROOT_DIR/.env"
ENV_EXAMPLE="$ROOT_DIR/.env.example"
LOG_DIR="$ROOT_DIR/logs"
PID_DIR="$ROOT_DIR/.run"

BACKEND_PID_FILE="$PID_DIR/core.pid"
FRONTEND_PID_FILE="$PID_DIR/web.pid"
BACKEND_LOG="$LOG_DIR/core.log"
FRONTEND_LOG="$LOG_DIR/web.log"

BACKEND_PORT="${OPSORIGIN_CORE_PORT:-8080}"
FRONTEND_PORT="${PORT:-3000}"
COMPOSE_PROJECT="opsorigin"

# ---------- 初始化 ----------
mkdir -p "$LOG_DIR" "$PID_DIR"

# 颜色输出
C_RESET="\033[0m"
C_GREEN="\033[32m"
C_YELLOW="\033[33m"
C_RED="\033[31m"
C_BLUE="\033[34m"
C_GRAY="\033[90m"
log()   { echo -e "${C_GREEN}[✓]${C_RESET} $*"; }
warn()  { echo -e "${C_YELLOW}[!]${C_RESET} $*"; }
err()   { echo -e "${C_RED}[✗]${C_RESET} $*" >&2; }
info()  { echo -e "${C_BLUE}[i]${C_RESET} $*"; }
dim()   { echo -e "${C_GRAY}    $*${C_RESET}"; }

# ---------- 依赖检查 ----------
check_cmd() {
  local cmd="$1"
  if ! command -v "$cmd" >/dev/null 2>&1; then
    err "未找到命令: $cmd"
    return 1
  fi
}

check_deps() {
  local missing=0
  for c in go docker node npm; do
    if command -v "$c" >/dev/null 2>&1; then
      dim "$c -> $(command -v "$c")"
    else
      err "缺少依赖: $c"
      missing=1
    fi
  done
  if ! docker compose version >/dev/null 2>&1; then
    err "docker compose 不可用（需要 Docker Compose v2+）"
    missing=1
  fi
  [[ $missing -eq 0 ]] || { err "依赖检查未通过，请先安装缺失的工具"; exit 1; }
}

# ---------- .env 初始化 ----------
ensure_env() {
  if [[ ! -f "$ENV_FILE" ]]; then
    if [[ -f "$ENV_EXAMPLE" ]]; then
      cp "$ENV_EXAMPLE" "$ENV_FILE"
      log "已从 .env.example 创建 .env"
    else
      warn "未找到 .env.example，使用默认环境变量启动"
    fi
  fi
  # 加载 .env
  if [[ -f "$ENV_FILE" ]]; then
    set -a
    # shellcheck disable=SC1090
    source "$ENV_FILE"
    set +a
    BACKEND_PORT="${OPSORIGIN_CORE_PORT:-8080}"
  fi
}

# ---------- 依赖服务（Docker） ----------
start_deps() {
  info "启动依赖服务（PostgreSQL / Dgraph / Redis）..."
  docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" up -d postgres dgraph-zero dgraph-alpha redis

  info "等待 PostgreSQL 就绪..."
  local tries=0
  until docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" exec -T postgres pg_isready -U opsorigin >/dev/null 2>&1; do
    tries=$((tries + 1))
    [[ $tries -gt 30 ]] && { err "PostgreSQL 启动超时"; return 1; }
    sleep 1
  done
  log "PostgreSQL 已就绪"

  info "等待 Redis 就绪..."
  tries=0
  until docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" exec -T redis redis-cli ping >/dev/null 2>&1; do
    tries=$((tries + 1))
    [[ $tries -gt 30 ]] && { err "Redis 启动超时"; return 1; }
    sleep 1
  done
  log "Redis 已就绪"

  info "等待 Dgraph Alpha 就绪..."
  tries=0
  # Dgraph HTTP 8080 与后端 8080 冲突，改为容器内部健康检查
  until docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" exec -T dgraph-alpha \
      curl -sf http://localhost:8080/health >/dev/null 2>&1; do
    tries=$((tries + 1))
    [[ $tries -gt 60 ]] && { warn "Dgraph 健康检查未通过，后端会使用内存模式降级"; return 0; }
    sleep 1
  done
  log "Dgraph 已就绪"
}

stop_deps() {
  info "停止依赖服务..."
  docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" down
  log "依赖服务已停止"
}

status_deps() {
  info "依赖服务状态："
  docker compose -f "$COMPOSE_FILE" -p "$COMPOSE_PROJECT" ps 2>/dev/null || warn "依赖服务未启动"
}

# ---------- 后端 ----------
start_backend() {
  if [[ -f "$BACKEND_PID_FILE" ]] && kill -0 "$(cat "$BACKEND_PID_FILE")" 2>/dev/null; then
    warn "后端已在运行（PID $(cat "$BACKEND_PID_FILE")）"
    return 0
  fi
  info "启动后端（Core，端口 ${BACKEND_PORT}）..."
  cd "$ROOT_DIR"
  # GOCACHE 默认指向系统缓存目录, 部分受限环境下不可写会导致 go run 失败;
  # 统一落到项目内 .run/go-cache(已 gitignore), 保证任何环境可启动。
  export GOCACHE="${GOCACHE:-$ROOT_DIR/.run/go-cache}"
  nohup go run ./cmd/core >"$BACKEND_LOG" 2>&1 &
  echo $! >"$BACKEND_PID_FILE"
  sleep 2
  if kill -0 "$(cat "$BACKEND_PID_FILE")" 2>/dev/null; then
    log "后端已启动（PID $(cat "$BACKEND_PID_FILE")）"
    dim "日志: $BACKEND_LOG"
  else
    err "后端启动失败，查看日志: $BACKEND_LOG"
    tail -n 20 "$BACKEND_LOG" 2>/dev/null || true
    return 1
  fi
}

stop_backend() {
  if [[ -f "$BACKEND_PID_FILE" ]]; then
    local pid
    pid="$(cat "$BACKEND_PID_FILE")"
    if kill -0 "$pid" 2>/dev/null; then
      kill "$pid" 2>/dev/null || true
      sleep 1
      kill -9 "$pid" 2>/dev/null || true
      log "后端已停止（PID ${pid}）"
    else
      dim "后端进程已不存在"
    fi
    rm -f "$BACKEND_PID_FILE"
  else
    dim "后端未运行"
  fi
}

status_backend() {
  if [[ -f "$BACKEND_PID_FILE" ]] && kill -0 "$(cat "$BACKEND_PID_FILE")" 2>/dev/null; then
    log "后端运行中（PID $(cat "$BACKEND_PID_FILE")）→ http://localhost:$BACKEND_PORT"
  else
    warn "后端未运行"
  fi
}

# ---------- 前端 ----------
start_frontend() {
  if [[ -f "$FRONTEND_PID_FILE" ]] && kill -0 "$(cat "$FRONTEND_PID_FILE")" 2>/dev/null; then
    warn "前端已在运行（PID $(cat "$FRONTEND_PID_FILE")）"
    return 0
  fi
  info "启动前端（web-next，端口 ${FRONTEND_PORT}）..."
  cd "$ROOT_DIR/web-next"
  if [[ ! -d node_modules ]]; then
    info "首次运行，安装依赖..."
    npm install
  fi
  nohup npm run dev >"$FRONTEND_LOG" 2>&1 &
  echo $! >"$FRONTEND_PID_FILE"
  sleep 3
  if kill -0 "$(cat "$FRONTEND_PID_FILE")" 2>/dev/null; then
    log "前端已启动（PID $(cat "$FRONTEND_PID_FILE")）"
    dim "日志: $FRONTEND_LOG"
  else
    err "前端启动失败，查看日志: $FRONTEND_LOG"
    tail -n 20 "$FRONTEND_LOG" 2>/dev/null || true
    return 1
  fi
}

stop_frontend() {
  if [[ -f "$FRONTEND_PID_FILE" ]]; then
    local pid
    pid="$(cat "$FRONTEND_PID_FILE")"
    if kill -0 "$pid" 2>/dev/null; then
      # npm run dev 会派生子进程，杀掉整个进程组
      kill -- "-$pid" 2>/dev/null || kill "$pid" 2>/dev/null || true
      sleep 1
      kill -9 "$pid" 2>/dev/null || true
      log "前端已停止（PID ${pid}）"
    else
      dim "前端进程已不存在"
    fi
    rm -f "$FRONTEND_PID_FILE"
  else
    dim "前端未运行"
  fi
}

status_frontend() {
  if [[ -f "$FRONTEND_PID_FILE" ]] && kill -0 "$(cat "$FRONTEND_PID_FILE")" 2>/dev/null; then
    log "前端运行中（PID $(cat "$FRONTEND_PID_FILE")）→ http://localhost:$FRONTEND_PORT"
  else
    warn "前端未运行"
  fi
}

# ---------- 综合命令 ----------
start_all() {
  check_deps
  ensure_env
  start_deps
  start_backend
  start_frontend
  echo
  info "OpsOrigin 已全部启动"
  echo -e "  ${C_GREEN}前端${C_RESET}: http://localhost:$FRONTEND_PORT  (登录: admin/admin123)"
  echo -e "  ${C_GREEN}后端${C_RESET}: http://localhost:$BACKEND_PORT"
  echo -e "  ${C_GRAY}停止: ./start.sh stop${C_RESET}"
}

stop_all() {
  stop_frontend
  stop_backend
  stop_deps
  echo
  info "OpsOrigin 已全部停止"
}

status_all() {
  status_backend
  status_frontend
  status_deps
}

show_logs() {
  local target="${1:-}"
  case "$target" in
    b|backend) tail -f "$BACKEND_LOG" ;;
    f|frontend|web) tail -f "$FRONTEND_LOG" ;;
    *)
      info "用法: ./start.sh logs [b|f]"
      info "  b  后端日志"
      info "  f  前端日志"
      ;;
  esac
}

# ---------- 入口 ----------
usage() {
  cat <<EOF
OpsOrigin 一键启动脚本

用法: ./start.sh [命令]

命令:
  start       启动所有服务（默认）
  stop        停止所有服务
  restart     重启所有服务
  status      查看运行状态
  logs [b|f]  查看日志（b=后端, f=前端）
  deps        仅启动依赖服务（PG/Dgraph/Redis）
  backend     仅启动后端
  frontend    仅启动前端
  help        显示本帮助

不带参数运行等价于 start。
EOF
}

main() {
  local cmd="${1:-start}"
  case "$cmd" in
    start)    start_all ;;
    stop)     stop_all ;;
    restart)  stop_all; start_all ;;
    status)   status_all ;;
    logs)     show_logs "${2:-}" ;;
    deps)     check_deps; ensure_env; start_deps ;;
    backend)  check_deps; ensure_env; start_backend ;;
    frontend) check_deps; start_frontend ;;
    help|-h|--help) usage ;;
    *) err "未知命令: $cmd"; usage; exit 1 ;;
  esac
}

main "$@"
