// Package plugins 提供 CMDB 自动发现的具体插件实现。
//
// 脚本输出统一规范（与「自动采集插件」的 collect/report 格式对齐）：
//   - 推荐格式（collect/report 契约）：脚本最终输出每行一个 report JSON，
//     含 model_id（模型类型 ID）+ attrs（字段 ID/名称 → 值）+ 可选 relations
//     （relation_id → 目标）。该格式与 cmdb.Service.ApplyCollectReports 完全对齐，
//     使内置插件脚本与自定义采集脚本共用一套落库语义。
//   - 兼容格式（历史 scriptLine）：{"name","ip","attrs"} 行格式，由插件的 ciType 决定模型类型。
//
// Go 侧解析器 parseReports 同时支持上述两种格式（含整体 JSON / 裸数组 / 多行 JSON），
// 单边失败不阻断整体。脚本通过环境变量读取目标地址与凭证（由发现任务的 Config 透传为 params），
// 避免把密钥写进脚本体。
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
	return parseReports(out, ciType)
}

// parseReports 把脚本输出解析为 Item 列表，统一支持两种格式：
//  1. 推荐格式（collect/report 契约）：单行/整体/裸数组 JSON，每条含 model_id + attrs + 可选 relations；
//  2. 兼容格式（历史 scriptLine）：{"name","ip","attrs"} 行格式，由 fallbackCIType 决定模型类型。
//
// 解析策略：优先按 report 格式探测（任一条含 model_id 即整体走 report 解析）；
// 否则回退 scriptLine 逐行解析。单边失败跳过，不阻断整体。
func parseReports(out, fallbackCIType string) ([]*discovery.Item, error) {
	trimmed := strings.TrimSpace(out)
	if trimmed == "" {
		return nil, nil
	}

	// 尝试 report 格式：{reports:[...]} / 裸数组 [...]
	var wrapped struct {
		Reports []map[string]any `json:"reports"`
	}
	if err := json.Unmarshal([]byte(trimmed), &wrapped); err == nil && len(wrapped.Reports) > 0 {
		items := reportsToItems(wrapped.Reports, fallbackCIType)
		if len(items) > 0 {
			return items, nil
		}
	}
	var arr []map[string]any
	if err := json.Unmarshal([]byte(trimmed), &arr); err == nil && len(arr) > 0 {
		items := reportsToItems(arr, fallbackCIType)
		if len(items) > 0 {
			return items, nil
		}
	}

	// 回退：逐行 scriptLine 解析（兼容历史脚本）。
	return parseLines(out, fallbackCIType)
}

// reportsToItems 把 report 格式的 map 列表转换为 Item（每条必须含非空 model_id 才纳入）。
func reportsToItems(reports []map[string]any, fallbackCIType string) []*discovery.Item {
	items := make([]*discovery.Item, 0, len(reports))
	for _, r := range reports {
		if r == nil {
			continue
		}
		modelID, _ := r["model_id"].(string)
		if modelID == "" {
			if id, ok := r["modelId"].(string); ok {
				modelID = id
			}
		}
		if modelID == "" {
			continue // 非 report 格式行，跳过（交给 scriptLine 回退）
		}
		name, _ := r["name"].(string)
		attrs, _ := r["attrs"].(map[string]any)
		if attrs == nil {
			attrs = map[string]any{}
		}
		item := &discovery.Item{
			Name:       name,
			CIType:     fallbackCIType, // 未命中 model_id 时的回退类型
			ModelID:    modelID,
			Attributes: attrs,
		}
		if rels, ok := r["relations"].([]any); ok {
			for _, rr := range rels {
				if m, ok := rr.(map[string]any); ok {
					rel := discovery.ItemRelation{}
					if rid, ok := m["relation_id"].(string); ok {
						rel.RelationID = rid
					}
					if rt, ok := m["relation_type"].(string); ok {
						rel.RelationType = rt
					}
					if t, ok := m["target"].(string); ok {
						rel.Target = t
					}
					if rel.RelationID != "" || rel.Target != "" {
						item.Relations = append(item.Relations, rel)
					}
				}
			}
		}
		items = append(items, item)
	}
	return items
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
