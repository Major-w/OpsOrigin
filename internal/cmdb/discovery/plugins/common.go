// Package plugins 提供 CMDB 自动发现的具体插件实现。
//
// 每个插件向探针 Agent 下发一段 shell 脚本，脚本把发现的资源以 JSON 行
// （每行一个 `{"name":..., "ip":..., "attrs":{...}}`）输出到 stdout；
// Go 侧再逐行解析为 discovery.Item。脚本通过环境变量读取目标地址与凭证
// （由发现任务的 Config 透传为 params），避免把密钥写进脚本体。
//
// 诚实声明：以下脚本依赖探针 Agent 上已安装的常见 CLI（snmpwalk / mysql / psql /
// redis-cli / curl 等）。代码已适配协议与解析逻辑，但未对接真实设备联调，
// 故标记为「待测」——与云厂商 Provider 的 untested 策略一致。
package plugins

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/opsorigin/core/internal/cmdb/discovery"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// scriptLine 是脚本 stdout 的单行 JSON 结构。
type scriptLine struct {
	Name  string         `json:"name"`
	IP    string         `json:"ip"`
	Attrs map[string]any `json:"attrs"`
}

// runScript 在探针 Agent 上执行脚本并解析 JSON 行输出为 Item 列表。
// ciType 为所有产出 CI 统一映射的模型类型 ID；脚本异常以非零退出码暴露。
func runScript(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner,
	script string, params map[string]string, ciType string) ([]*discovery.Item, error) {
	agentID, err := discovery.AgentIDOf(task), error(nil)
	if agentID == "" {
		return nil, fmt.Errorf("discovery task %q 未配置探针 Agent（Config.agent_id 必填）", task.ID)
	}
	_ = err
	out, code, rerr := runner.RunScriptOnAgent(ctx, agentID, task.TenantID, script, params, 180, "")
	if rerr != nil {
		return nil, fmt.Errorf("run discovery script on agent %s: %w", agentID, rerr)
	}
	if code != 0 {
		return nil, fmt.Errorf("discovery script exited %d on agent %s: %s", code, agentID, strings.TrimSpace(out))
	}
	return parseLines(out, ciType)
}

// parseLines 把脚本的 JSON 行输出解析为 Item 列表（跳过空行/无效行，单边失败不影响整体）。
func parseLines(out, ciType string) ([]*discovery.Item, error) {
	items := make([]*discovery.Item, 0)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || !strings.HasPrefix(line, "{") {
			continue
		}
		var sl scriptLine
		if err := json.Unmarshal([]byte(line), &sl); err != nil {
			continue // 容错：单行解析失败跳过，不阻断其余资源
		}
		if sl.Name == "" {
			sl.Name = sl.IP
		}
		if sl.Attrs == nil {
			sl.Attrs = map[string]any{}
		}
		items = append(items, &discovery.Item{
			Name:       sl.Name,
			CIType:     ciType,
			IPAddress:  sl.IP,
			Attributes: sl.Attrs,
		})
	}
	return items, nil
}

// wrapDiscover 是各插件 Discover 的通用骨架：取凭证 → 渲染脚本 → 执行 → 解析。
func wrapDiscover(ctx context.Context, task *model.DiscoveryTask, runner discovery.ScriptRunner,
	render func(*model.DiscoveryTask) string, credKeys []string, ciType string) ([]*discovery.Item, error) {
	params := discovery.ToParams(task, credKeys...)
	return runScript(ctx, task, runner, render(task), params, ciType)
}
