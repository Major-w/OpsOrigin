package plugins

import (
	"context"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// ServerDiscoverer 发现操作系统级主机（无论底层是物理机还是虚拟机）：在探针 Agent
// （即目标主机自身或其可达跳板）上采集系统信息（hostname/os/kernel/CPU/内存/磁盘/序列号/厂商）。
// CI 映射到模型类型 host（与 cmd/core/agent_cmdb_adapter.SyncHost 落库目标一致，统一主机模型）。
type ServerDiscoverer struct{}

func (d *ServerDiscoverer) Types() []string { return []string{"host", "server"} }

func (d *ServerDiscoverer) Discover(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner) ([]*discovery.Item, error) {
	return wrapDiscover(ctx, task, runner, renderServerScript, nil, "host")
}

// renderServerScript 在探针 Agent 本地采集主机硬件与系统信息。
// 优先用 dmidecode 取序列号/厂商（需权限，失败回退 unknown），其余读 /proc 与 /etc/os-release。
func renderServerScript(task *model.DiscoveryTask) string {
	_ = task
	return `#!/bin/bash
set +e
hostname=$(hostname 2>/dev/null || cat /etc/hostname 2>/dev/null)
os=$(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME" || uname -o 2>/dev/null)
kernel=$(uname -r 2>/dev/null)
cpu_model=$(grep -m1 "model name" /proc/cpuinfo 2>/dev/null | cut -d: -f2 | sed 's/^ *//')
cpu_cores=$(grep -c "^processor" /proc/cpuinfo 2>/dev/null)
mem_kb=$(grep MemTotal /proc/meminfo 2>/dev/null | awk '{print $2}')
mem_gb=$(awk "BEGIN{ if($mem_kb) printf \"%.1f\", $mem_kb/1024/1024; else print 0 }")
disk_gb=$(df -l --total 2>/dev/null | tail -1 | awk '{printf "%.1f", $2/1024/1024}')
serial=$(dmidecode -s system-serial-number 2>/dev/null || cat /sys/class/dmi/id/product_serial 2>/dev/null || echo "unknown")
vendor=$(dmidecode -s system-manufacturer 2>/dev/null || echo "unknown")
product=$(dmidecode -s system-product-name 2>/dev/null || echo "unknown")
arch=$(uname -m 2>/dev/null)
cat <<EOF
{"name":"$hostname","ip":"","attrs":{"os":"$os","kernel":"$kernel","arch":"$arch","cpu_model":"$cpu_model","cpu_cores":$cpu_cores,"memory_gb":"$mem_gb","disk_total_gb":"$disk_gb","serial":"$serial","vendor":"$vendor","product":"$product","discovery_method":"host_agent"}}
EOF
`
}
