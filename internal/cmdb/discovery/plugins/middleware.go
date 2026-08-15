package plugins

import (
	"context"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// MiddlewareDiscoverer 探测常见中间件实例（Redis / Nginx / Elasticsearch / Kafka / ZooKeeper / RabbitMQ）。
// 探针 Agent 需预装对应客户端或 curl/nc；类型由 Config.mtype 指定（redis/nginx/es/kafka/zk/rabbitmq/auto），
// auto 按常见端口启发式尝试。方案 C：CI 的 TypeID 下沉为具体引擎（redis/nginx/elasticsearch/kafka/zookeeper/rabbitmq），
// middleware_type 作为冗余兼容字段保留在 attributes 中。
type MiddlewareDiscoverer struct{}

// Types 返回该插件可承接的发现任务类型：具体引擎 leaf 子类 + middleware 别名（兼容既有任务）。
func (d *MiddlewareDiscoverer) Types() []string {
	return []string{"redis", "nginx", "elasticsearch", "kafka", "zookeeper", "rabbitmq", "middleware"}
}

func (d *MiddlewareDiscoverer) Discover(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner) ([]*discovery.Item, error) {
	items, err := wrapDiscover(ctx, task, runner, renderMiddlewareScript,
		[]string{"mtype", "port"}, "middleware")
	if err != nil {
		return nil, err
	}
	// 方案 C：TypeID 下沉为具体引擎；middleware_type 冗余保留。
	for _, it := range items {
		if mt, ok := it.Attributes["middleware_type"].(string); ok && mt != "" && mt != "unknown" {
			it.CIType = mt
		}
	}
	return items, nil
}

// renderMiddlewareScript 按 MTYPE 选择探测方式，输出对应中间件的版本/监听端口。
func renderMiddlewareScript(task *model.DiscoveryTask) string {
	_ = task
	return `#!/bin/bash
set +e
TARGET="${TARGET:?target required}"
MTYPE="${MTYPE:-auto}"
PORT="${PORT:-}"
probe_redis() { local p="${PORT:-6379}"; local info=$(redis-cli -h "$TARGET" -p "$p" INFO server 2>/dev/null);
  local ver=$(echo "$info" | grep -i "^redis_version" | cut -d: -f2 | tr -d '\r');
  local role=$(echo "$info" | grep -i "^role" | cut -d: -f2 | tr -d '\r';);
  echo "{\"name\":\"redis-$TARGET:$p\",\"ip\":\"$TARGET\",\"attrs\":{\"middleware_type\":\"redis\",\"version\":\"$ver\",\"port\":$p,\"role\":\"$role\",\"discovery_method\":\"mw_probe\"}}"; }
probe_nginx() { local p="${PORT:-80}";
  local ver=$(curl -s -m 5 -I "http://$TARGET:$p" 2>/dev/null | grep -i "Server:" | tr -d '\r' | cut -d: -f2- | xargs | cut -d' ' -f2);
  echo "{\"name\":\"nginx-$TARGET:$p\",\"ip\":\"$TARGET\",\"attrs\":{\"middleware_type\":\"nginx\",\"version\":\"$ver\",\"port\":$p,\"discovery_method\":\"mw_probe\"}}"; }
probe_es() { local p="${PORT:-9200}";
  local ver=$(curl -s -m 5 "http://$TARGET:$p/" 2>/dev/null | grep -o '"number":"[^"]*"' | head -1 | cut -d'"' -f4);
  echo "{\"name\":\"elasticsearch-$TARGET:$p\",\"ip\":\"$TARGET\",\"attrs\":{\"middleware_type\":\"elasticsearch\",\"version\":\"$ver\",\"port\":$p,\"discovery_method\":\"mw_probe\"}}"; }
probe_kafka() { local p="${PORT:-9092}";
  local b=$(kafkacat -b "$TARGET:$p" -L 2>/dev/null | grep -i "broker" | head -1 | tr -d '\r' | cut -c1-60);
  echo "{\"name\":\"kafka-$TARGET:$p\",\"ip\":\"$TARGET\",\"attrs\":{\"middleware_type\":\"kafka\",\"version\":\"\",\"port\":$p,\"broker_info\":\"$b\",\"discovery_method\":\"mw_probe\"}}"; }
probe_zk() { local p="${PORT:-2181}";
  local r=$(echo ruok | nc -w 3 "$TARGET" "$p" 2>/dev/null | tr -d '\r');
  echo "{\"name\":\"zookeeper-$TARGET:$p\",\"ip\":\"$TARGET\",\"attrs\":{\"middleware_type\":\"zookeeper\",\"version\":\"\",\"port\":$p,\"ruok\":\"$r\",\"discovery_method\":\"mw_probe\"}}"; }
probe_rabbitmq() { local p="${PORT:-15672}";
  local ver=$(curl -s -m 5 "http://$TARGET:$p/api/overview" 2>/dev/null | grep -o '"rabbitmq_version":"[^"]*"' | cut -d'"' -f4);
  echo "{\"name\":\"rabbitmq-$TARGET:$p\",\"ip\":\"$TARGET\",\"attrs\":{\"middleware_type\":\"rabbitmq\",\"version\":\"$ver\",\"port\":$p,\"discovery_method\":\"mw_probe\"}}"; }
case "$MTYPE" in
  redis) probe_redis;;
  nginx) probe_nginx;;
  elasticsearch|es) probe_es;;
  kafka) probe_kafka;;
  zookeeper|zk) probe_zk;;
  rabbitmq) probe_rabbitmq;;
  auto)
    for pr in 6379 80 9200 9092 2181 15672; do
      case $pr in
        6379) PORT=6379; probe_redis; break;;
        80) PORT=80; probe_nginx; break;;
        9200) PORT=9200; probe_es; break;;
        9092) PORT=9092; probe_kafka; break;;
        2181) PORT=2181; probe_zk; break;;
        15672) PORT=15672; probe_rabbitmq; break;;
      esac
    done;;
esac
`
}
