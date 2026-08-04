package plugins

import (
	"context"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// DatabaseDiscoverer 经本地客户端探测数据库实例（MySQL / PostgreSQL / Oracle）。
// 探针 Agent 需预装对应客户端（mysql / psql / sqlplus）；引擎由 Config.engine 指定，
// 端口/账号/密码由 Config 透传。CI 映射到模型类型 database（engine/version/port）。
type DatabaseDiscoverer struct{}

func (d *DatabaseDiscoverer) Types() []string { return []string{"database"} }

func (d *DatabaseDiscoverer) Discover(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner) ([]*discovery.Item, error) {
	return wrapDiscover(ctx, task, runner, renderDatabaseScript,
		[]string{"engine", "port", "db_user", "db_pass", "service"}, "database")
}

// renderDatabaseScript 按 ENGINE 选择对应的本地客户端探测版本信息；版本字段脱双引号并截断，避免破坏 JSON。
func renderDatabaseScript(task *model.DiscoveryTask) string {
	_ = task
	return `#!/bin/bash
set +e
TARGET="${TARGET:?target required}"
ENGINE="${ENGINE:-mysql}"
PORT="${PORT:-}"
USER="${DB_USER:-}"
PASS="${DB_PASS:-}"
SERVICE="${SERVICE:-ORCL}"
clean() { tr -d '"' | cut -c1-80; }
case "$ENGINE" in
  mysql)
    PORT="${PORT:-3306}"
    ver=$(mysql -h "$TARGET" -P "$PORT" -u "$USER" -p"$PASS" -N -e "SELECT VERSION()" 2>/dev/null | head -1 | clean)
    comment=$(mysql -h "$TARGET" -P "$PORT" -u "$USER" -p"$PASS" -N -e "SELECT @@version_comment" 2>/dev/null | head -1 | clean)
    echo "{\"name\":\"mysql-$TARGET:$PORT\",\"ip\":\"$TARGET\",\"attrs\":{\"engine\":\"mysql\",\"version\":\"$ver\",\"port\":$PORT,\"comment\":\"$comment\",\"discovery_method\":\"db_probe\"}}"
    ;;
  postgres)
    PORT="${PORT:-5432}"
    ver=$(PGPASSWORD="$PASS" psql -h "$TARGET" -p "$PORT" -U "$USER" -t -A -c "SELECT version()" 2>/dev/null | head -1 | clean)
    echo "{\"name\":\"pg-$TARGET:$PORT\",\"ip\":\"$TARGET\",\"attrs\":{\"engine\":\"postgresql\",\"version\":\"$ver\",\"port\":$PORT,\"discovery_method\":\"db_probe\"}}"
    ;;
  oracle)
    PORT="${PORT:-1521}"
    ver=$(sqlplus -S "$USER/$PASS@$TARGET:$PORT/$SERVICE" <<< "SELECT version FROM v\$instance;" 2>/dev/null | grep -i "Release" | head -1 | clean)
    echo "{\"name\":\"oracle-$TARGET:$PORT\",\"ip\":\"$TARGET\",\"attrs\":{\"engine\":\"oracle\",\"version\":\"$ver\",\"port\":$PORT,\"service\":\"$SERVICE\",\"discovery_method\":\"db_probe\"}}"
    ;;
  *)
    echo "{\"name\":\"unknown-db-$TARGET\",\"ip\":\"$TARGET\",\"attrs\":{\"engine\":\"unknown\",\"discovery_method\":\"db_probe\"}}"
    ;;
esac
`
}
