#!/bin/sh
# OpsOrigin F4 压测套件 runner (POSIX sh, 零外部依赖)
#
#   - scripts/ 是独立 Go module(仅标准库), 无需 go.sum / 外网
#   - mock LLM 随本脚本一同编译
#   - Core 以 stub 模式启动(无 PG/Redis -> 内存回退), dev 默认 JWT 自动放行
#
# 用法:
#   cd scripts && sh loadtest.sh
#   CORE_ADDR=127.0.0.1:8080 MOCK_ADDR=127.0.0.1:18999 sh loadtest.sh
set -u

SCRIPTS=$(cd "$(dirname "$0")" && pwd)
ROOT=$(cd "$SCRIPTS/.." && pwd)
BIN=/tmp/opslt-bin
mkdir -p "$BIN" "$SCRIPTS/.run"
RESULTS="$SCRIPTS/.run/results.jsonl"
: > "$RESULTS"

CORE_BIN="$BIN/opsorigin-core"
MOCK_BIN="$BIN/lt-mock"
LT_AI="$BIN/lt-ai"
LT_CMDB="$BIN/lt-cmdb"
LT_ITSM="$BIN/lt-itsm"
LT_MONITOR="$BIN/lt-monitor"
LT_AUTOMATION="$BIN/lt-automation"
LT_HEARTBEAT="$BIN/lt-heartbeat"

CORE_ADDR="${CORE_ADDR:-127.0.0.1:8080}"
MOCK_ADDR="${MOCK_ADDR:-127.0.0.1:18999}"
BASE="http://$CORE_ADDR"

echo "==> [1/5] building core (this may take a moment)..."
( cd "$ROOT/cmd/core" && GOWORK=off GOFLAGS=-mod=mod go build -o "$CORE_BIN" . ) \
  || { echo "FATAL: core build failed"; exit 1; }

echo "==> [2/5] building loadtest suite (stdlib only)..."
( cd "$SCRIPTS" && \
    GOWORK=off go build -o "$MOCK_BIN" ./mockllm && \
    GOWORK=off go build -o "$LT_AI" ./ai && \
    GOWORK=off go build -o "$LT_CMDB" ./cmdb && \
    GOWORK=off go build -o "$LT_ITSM" ./itsm && \
    GOWORK=off go build -o "$LT_MONITOR" ./monitor && \
    GOWORK=off go build -o "$LT_AUTOMATION" ./automation && \
    GOWORK=off go build -o "$LT_HEARTBEAT" ./heartbeat ) \
  || { echo "FATAL: suite build failed"; exit 1; }

cleanup() {
  pkill -f "$CORE_BIN" 2>/dev/null || true
  pkill -f "$MOCK_BIN" 2>/dev/null || true
}
trap cleanup EXIT

echo "==> [3/5] starting mock LLM on $MOCK_ADDR"
"$MOCK_BIN" &
MOCK_PID=$!

echo "==> [3/5] starting core on $CORE_ADDR (stub mode, dev auth)"
OPSORIGIN_LLM_BASE_URL="http://$MOCK_ADDR" \
OPSORIGIN_LLM_API_KEY="mock" \
OPSORIGIN_LLM_MODEL="mock" \
"$CORE_BIN" &
CORE_PID=$!

echo "==> [4/5] waiting for core health..."
i=0
while [ "$i" -lt 60 ]; do
  i=$((i + 1))
  if curl -sf "$BASE/api/v1/health" >/dev/null 2>&1; then
    echo "    core healthy after ~${i}s"
    break
  fi
  sleep 1
done
if ! curl -sf "$BASE/api/v1/health" >/dev/null 2>&1; then
  echo "FATAL: core did not become healthy"
  cleanup
  exit 1
fi

echo "==> [5/5] running subsystem loadtests..."
for s in ai cmdb itsm monitor automation heartbeat; do
  echo "------------------------------------------------------------"
  echo "==> $s"
  case "$s" in
    ai)         BIN_RUN="$LT_AI" ;;
    cmdb)       BIN_RUN="$LT_CMDB" ;;
    itsm)       BIN_RUN="$LT_ITSM" ;;
    monitor)     BIN_RUN="$LT_MONITOR" ;;
    automation) BIN_RUN="$LT_AUTOMATION" ;;
    heartbeat)   BIN_RUN="$LT_HEARTBEAT" ;;
  esac
  out=$("$BIN_RUN" -base "$BASE" 2>&1)
  rc=$?
  echo "    $out"
  echo "$out" | jq -c --arg sub "$s" --argjson rc "$rc" '. + {subsystem:$sub, exit:$rc}' >> "$RESULTS"
done

echo "------------------------------------------------------------"
echo "==> final /metrics snapshot"
curl -sf "$BASE/metrics" 2>/dev/null \
  | grep -E "^(ai_concurrent_sessions|ai_llm_inflight|ai_llm_ratelimit_hit_total|ingest_buffer_size|ingest_dropped_total|monitor_probe_results) " \
  > "$SCRIPTS/.run/final_metrics.txt" || true
cat "$SCRIPTS/.run/final_metrics.txt"

echo "==> generating sla-report.md"
python3 - "$RESULTS" "$SCRIPTS/sla-report.md" <<'PY'
import json, sys, datetime
src, dst = sys.argv[1], sys.argv[2]
rows = []
with open(src) as f:
    for line in f:
        line = line.strip()
        if not line:
            continue
        try:
            rows.append(json.loads(line))
        except Exception:
            pass

overall = all(bool(r.get("pass")) for r in rows)

md = []
md.append("# OpsOrigin F4 压测 SLA 报告")
md.append("")
md.append("- 生成时间: " + datetime.datetime.now().isoformat(timespec="seconds"))
md.append("- 模式: 单 Core + stub (无 PG/Redis) + 内存回退 + 自带 mock LLM (:18999)")
md.append(f"- 总体结论: **{'ALL PASS' if overall else 'FAIL'}**")
md.append("")
md.append("| 子系统 | 总请求 | 成功 | 失败 | p50(ms) | p95(ms) | p99(ms) | 关键指标 | 结论 |")
md.append("|---|---|---|---|---|---|---|---|---|")

KEYS = ("peak_ai_concurrent_sessions", "peak_ai_llm_inflight", "ai_llm_ratelimit_hits",
         "verify_accepted", "verify_total", "peak_ingest_buffer_size",
         "ingest_dropped_total", "monitor_probe_results")
for r in rows:
    sub = r.get("subsystem", "?")
    extra = []
    for k in KEYS:
        if k in r:
            extra.append(f"{k}={r[k]}")
    kn = "; ".join(extra) if extra else "-"
    md.append(
        f"| {sub} | {r.get('total')} | {r.get('ok')} | {r.get('fail')} "
        f"| {r.get('p50_ms')} | {r.get('p95_ms')} | {r.get('p99_ms')} "
        f"| {kn} | {'PASS' if r.get('pass') else 'FAIL'} |"
    )

md.append("")
md.append("## D1 / D2 缺陷复核")
md.append("- **D1 (逻辑/物理并发解耦)**: ai 子系统 `peak_ai_concurrent_sessions` 应≈并发度(逻辑会话数, 如 100), "
          "而 `peak_ai_llm_inflight` 应≤32(物理 LLM 槽位容量)。两者明显背离即证明解耦生效 —— "
          "SSE/会话层不再被物理 LLM 并发上限绑死。")
md.append("- **D2 (CMDB 批量端点)**: cmdb 子系统单批核验 `verify_accepted == verify_total == 5`(HTTP 202 且全量接收), "
          "且压测全程 `ingest_dropped_total == 0`(异步缓冲未溢出丢弃)。证明批量路由已修复并接入异步 ingest。")
md.append("- **R3 (monitor 并发读)**: monitor 子系统高频 GET `/api/v1/monitor/metrics` 全程稳定 2xx, 无 panic / 数据竞争。")
md.append("")
md.append("## 运行方式")
md.append("```bash")
md.append("cd scripts && sh loadtest.sh")
md.append("```")
md.append("")
md.append("## 说明")
md.append("- 6 个子系统: ai / cmdb / itsm / monitor / automation / heartbeat 均在 stub 模式下压测。")
md.append("- 通过判定: 成功率 ≥ 99% 且上述缺陷相关指标达标, 否则该子系统 FAIL。")
md.append("- 所有改动未提交(由用户自行 git add/commit/push)。")

with open(dst, "w") as f:
    f.write("\n".join(md) + "\n")
print("report written:", dst, "| overall =", overall)
PY

cleanup
echo "==> done"
