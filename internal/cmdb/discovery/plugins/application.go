package plugins

import (
	"context"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// AppDiscoverer 探测运行中的应用程序实例：按 Config.ports 给出的端口列表（逗号分隔）在目标主机上
// 探测监听服务，并可选地用 HTTP 探测取应用指纹（Server / 标题 / 响应码）。CI 映射到模型类型 app。
// 典型用途：发现自研服务、Web 站点、Tomcat/JBoss 等未纳入标准中间件的进程。
type AppDiscoverer struct{}

func (d *AppDiscoverer) Types() []string { return []string{"application", "app"} }

func (d *AppDiscoverer) Discover(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner) ([]*discovery.Item, error) {
	return wrapDiscover(ctx, task, runner, renderAppScript, []string{"ports"}, "app")
}

// renderAppScript 按 PORTS（逗号分隔）逐端口探测：用 /dev/tcp 测试连通性，命中后尝试 HTTP 指纹；
// 端口命名以 mtype 前缀（Config.mtype 可选，如 tomcat/web）便于区分应用类型。
func renderAppScript(task *model.DiscoveryTask) string {
	_ = task
	return `#!/bin/bash
set +e
TARGET="${TARGET:?target required}"
PORTS="${PORTS:-80,8080,443}"
MTYPE="${MTYPE:-unknown}"
IFS=',' read -ra ARR <<< "$PORTS"
for p in "${ARR[@]}"; do
  p=$(echo "$p" | tr -d ' ')
  [ -z "$p" ] && continue
  (exec 3<>/dev/tcp/$TARGET/$p) 2>/dev/null || continue
  exec 3>&- 2>/dev/null
  # HTTP 指纹探测（优先 http，失败回退 https）
  fp=$(curl -s -m 4 -o /dev/null -w "%{http_code}" "http://$TARGET:$p/" 2>/dev/null)
  server=$(curl -s -m 4 -I "http://$TARGET:$p/" 2>/dev/null | grep -i "^server:" | tr -d '\r' | cut -d: -f2- | xargs | cut -c1-60)
  title=$(curl -s -m 4 "http://$TARGET:$p/" 2>/dev/null | grep -io "<title>[^<]*</title>" | head -1 | sed 's/<[^>]*>//g' | cut -c1-80)
  [ -z "$fp" ] && fp=$(curl -s -m 4 -o /dev/null -w "%{http_code}" "https://$TARGET:$p/" 2>/dev/null)
  echo "{\"name\":\"${MTYPE}-${TARGET}:${p}\",\"ip\":\"$TARGET\",\"attrs\":{\"app_type\":\"$MTYPE\",\"port\":$p,\"http_code\":\"$fp\",\"server\":\"$server\",\"title\":\"$title\",\"discovery_method\":\"app_probe\"}}"
done
`
}
