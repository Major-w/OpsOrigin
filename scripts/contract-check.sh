#!/usr/bin/env bash
# OpsOrigin 开发契约一键校验（make contract 等价）
# 用法: bash scripts/contract-check.sh
# 退出码: 0 = FAIL 级全过（WARN 可容忍）；1 = 存在 FAIL 级问题
set -uo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

FAIL=0
WARN=0
SUMMARY=""

pass() { printf '  \033[32mOK\033[0m   %s\n' "$1"; }
fail() { printf '  \033[31mFAIL\033[0m %s\n' "$1"; FAIL=1; }
warn() { printf '  \033[33mWARN\033[0m %s\n' "$1"; WARN=1; }

echo "== [1/7] 竞品/品牌字样扫描（git grep 全仓）=="
if git grep -nE "EasyOps|easyops|优维|WorkBuddy|CodeBuddy" -- ':!docs/contracts/*' ':!scripts/contract-check.sh' >/tmp/contract-clean.txt 2>/dev/null; then
  fail "命中竞品/品牌字样:"; sed 's/^/    /' /tmp/contract-clean.txt
else
  pass "无竞品/品牌字样"
fi

echo "== [2/7] 前端 TypeScript 类型检查（tsc --noEmit）=="
if [ -x web/node_modules/.bin/tsc ]; then
  # 大仓 tsc 需要放大 Node 堆内存, 否则默认 4GB 不足会被 SIGKILL(exit 137)
  if (cd web && NODE_OPTIONS=--max-old-space-size=6144 ./node_modules/.bin/tsc --noEmit -p tsconfig.json >/tmp/contract-tsc.txt 2>&1); then
    pass "tsc 0 error"
  else
    n=$(grep -c "error TS" /tmp/contract-tsc.txt || true)
    fail "tsc $n 个类型错误（前 10 行）:"; grep "error TS" /tmp/contract-tsc.txt | head -10 | sed 's/^/    /'
  fi
else
  fail "web/node_modules/.bin/tsc 不存在（先 npm ci）"
fi

echo "== [3/7] 前端 Vitest（web/ npm test）=="
if [ -x web/node_modules/.bin/vitest ]; then
  if (cd web && ./node_modules/.bin/vitest run >/tmp/contract-vitest.txt 2>&1); then
    pass "vitest 通过"
  else
    fail "vitest 失败（末 20 行）:"; tail -20 /tmp/contract-vitest.txt | sed 's/^/    /'
  fi
else
  fail "web/node_modules/.bin/vitest 不存在（先 npm ci）"
fi

echo "== [4/7] 后端 gofmt + go vet =="
GOVET_ENV="GOTOOLCHAIN=go1.26.6 GOPROXY=https://goproxy.cn,direct"
if gofmt_out=$(gofmt -l internal pkg cmd 2>/dev/null); then
  if [ -n "$gofmt_out" ]; then fail "gofmt 未格式化文件:"; echo "$gofmt_out" | sed 's/^/    /'; else pass "gofmt 干净"; fi
else
  fail "gofmt 执行失败"
fi
if env $GOVET_ENV go vet ./... >/tmp/contract-vet.txt 2>&1; then
  pass "go vet 通过"
else
  fail "go vet 报错（前 10 行）:"; head -10 /tmp/contract-vet.txt | sed 's/^/    /'
fi

echo "== [5/7] 前端原生 confirm( 残留（必须 0）=="
if grep -rn "confirm(" web/src --include="*.tsx" --include="*.ts" >/tmp/contract-confirm.txt 2>/dev/null; then
  fail "原生 confirm( 残留:"; sed 's/^/    /' /tmp/contract-confirm.txt
else
  pass "无原生 confirm("
fi

echo "== [6/7] 前端硬编码色（WARN，SVG 图元必要除外）=="
if grep -rnE "bg-\[#|text-\[#|border-\[#" web/src --include="*.tsx" --include="*.ts" >/tmp/contract-color.txt 2>/dev/null; then
  warn "发现硬编码色（人工确认是否 SVG 图元必要）:"; head -10 /tmp/contract-color.txt | sed 's/^/    /'
else
  pass "无硬编码色"
fi

echo "== [7/7] 后端裸 SQL / context.Background()（WARN，命中须人工确认）=="
if grep -rnE "SELECT .* FROM|INSERT INTO|UPDATE .* SET|DELETE FROM" internal --include="*.go" >/tmp/contract-sql.txt 2>/dev/null; then
  warn "发现裸 SQL（须确认在 report 白名单体系内）:"; head -8 /tmp/contract-sql.txt | sed 's/^/    /'
else
  pass "未发现裸 SQL"
fi
if grep -rn "context.Background()" internal pkg cmd --include="*.go" >/tmp/contract-ctx.txt 2>/dev/null; then
  warn "发现 context.Background()（仅允许无请求上下文的初始化/定时任务）:"; head -8 /tmp/contract-ctx.txt | sed 's/^/    /'
else
  pass "无 context.Background()"
fi

echo ""
echo "=============================================="
if [ "$FAIL" -eq 1 ]; then
  echo "契约校验：\033[31m未通过（FAIL 级问题存在，任务未完成）\033[0m"
  exit 1
elif [ "$WARN" -eq 1 ]; then
  echo "契约校验：\033[33m通过（存在 WARN 项，请在提交说明注明理由）\033[0m"
  exit 0
else
  echo "契约校验：\033[32m全部通过\033[0m"
  exit 0
fi
