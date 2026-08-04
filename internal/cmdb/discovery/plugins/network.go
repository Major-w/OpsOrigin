package plugins

import (
	"context"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// NetworkDiscoverer 经 SNMP 发现网络设备（交换机/路由器/防火墙等）。
// 探针 Agent 需预装 snmpwalk；目标为设备管理 IP，凭证（community / 版本）由任务 Config 透传。
// CI 统一映射到模型类型 switch（role 等由 Config.role 补充），覆盖 router/switch/firewall 共用一类。
type NetworkDiscoverer struct{}

func (d *NetworkDiscoverer) Types() []string { return []string{"snmp", "network"} }

func (d *NetworkDiscoverer) Discover(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner) ([]*discovery.Item, error) {
	items, err := wrapDiscover(ctx, task, runner, renderNetworkScript, []string{"community", "snmp_ver"}, "switch")
	if err != nil {
		return nil, err
	}
	if role := discovery.CredOf(task, "role"); role != "" && len(items) > 0 {
		items[0].Attributes["role"] = role
	}
	if eng := discovery.CredOf(task, "env"); eng != "" && len(items) > 0 {
		items[0].Attributes["env"] = eng
	}
	return items, nil
}

// renderNetworkScript 在探针 Agent 上走 SNMP v2c/v3 walk 取系统信息、接口数与厂商（按 sysDescr 启发式推断）。
func renderNetworkScript(task *model.DiscoveryTask) string {
	_ = task
	return `#!/bin/bash
set +e
TARGET="${TARGET:?target required}"
COMMUNITY="${COMMUNITY:-public}"
VER="${SNMP_VER:-2c}"
oid() { snmpwalk -v "$VER" -c "$COMMUNITY" -Oqv -r 1 -t 5 "$TARGET" "$1" 2>/dev/null | tr -d '"\n' | head -c 400; }
sysName=$(oid 1.3.6.1.2.1.1.5)
sysDescr=$(oid 1.3.6.1.2.1.1.1)
sysObjectID=$(oid 1.3.6.1.2.1.1.2)
ports=$(snmpwalk -v "$VER" -c "$COMMUNITY" -Oqv -r 1 -t 5 "$TARGET" 1.3.6.1.2.1.2.2.1.2 2>/dev/null | grep -c .)
uptime=$(oid 1.3.6.1.2.1.1.3)
vendor="unknown"
case "$sysDescr" in
  *[Cc]isco*) vendor="cisco";;
  *[Hh]uawei*) vendor="huawei";;
  *H3C*|*HPE*) vendor="h3c";;
  *[Jj]uniper*) vendor="juniper";;
  *[Rr]uijie*) vendor="ruijie";;
  *[Mm]ikroTik*) vendor="mikrotik";;
esac
[ -z "$sysName" ] && sysName="$TARGET"
cat <<EOF
{"name":"$sysName","ip":"$TARGET","attrs":{"vendor":"$vendor","sys_object_id":"$sysObjectID","sys_descr":"$sysDescr","ports":$ports,"uptime":"$uptime","snmp_version":"$VER","discovery_method":"snmp"}}
EOF
`
}
