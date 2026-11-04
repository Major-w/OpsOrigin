package form

import (
	"strings"
)

// 赋值模式
const (
	SetFixed    = "fixed"     // 固定值（默认）
	SetCopyFrom = "copy_from" // 复制其它字段当前值
)

// DataSet 单个赋值动作：条件命中后把值写入 Target 字段。
type DataSet struct {
	Target string `json:"target"`          // 目标字段 key
	Mode   string `json:"mode,omitempty"`  // fixed(默认) / copy_from
	Value  string `json:"value,omitempty"` // fixed=写入的值；copy_from=来源字段 key
}

// DataLinkRule 数据联动规则："选 A 自动带出 B"。
// 例：环境=生产 → 影响范围 自动置为"全局"（fixed）；
//
//	选择配置项 → 受影响系统 复制该配置项名称（copy_from）。
//
// 规则按声明顺序执行，后执行的覆盖先执行的；目标字段须在本 schema 中声明（容错跳过）。
type DataLinkRule struct {
	ID         string      `json:"id,omitempty"`
	Name       string      `json:"name,omitempty"`
	Conditions []Condition `json:"conditions"`      // 触发条件（基于任意字段值）
	Logic      string      `json:"logic,omitempty"` // and(默认) / or
	Sets       []DataSet   `json:"sets"`            // 命中后的赋值动作列表
}

// DecisionTable 决策表：条件列 × 输出列的规则矩阵。
// 自上而下逐行求值，首个「启用且全部条件列命中」的行生效（其输出写入对应字段）。
// 典型用法：环境=生产,预发 且 变更类型=紧急 → 生效时间=非工作时间、审批链=P1。
type DecisionTable struct {
	ID            string        `json:"id,omitempty"`
	Name          string        `json:"name,omitempty"`
	ConditionKeys []string      `json:"condition_keys"` // 条件列（字段 key，按列顺序）
	OutputKeys    []string      `json:"output_keys"`    // 输出列（字段 key，按列顺序）
	Rows          []DecisionRow `json:"rows"`
}

// DecisionRow 决策表行：Cells 空串=通配（不限该列）；"a,b"=任一命中（逗号分隔）；
// Outputs 为该行命中后写入对应输出列的值；Enabled 缺省启用。
type DecisionRow struct {
	ID      string            `json:"id,omitempty"`
	Cells   map[string]string `json:"cells,omitempty"`
	Outputs map[string]string `json:"outputs,omitempty"`
	Enabled *bool             `json:"enabled,omitempty"`
}

func rowEnabled(r DecisionRow) bool { return r.Enabled == nil || *r.Enabled }

// cellHits 决策表单元格匹配：空=通配；"a,b" 任一命中（逗号分隔，与 OpIn 同语义）；
// （对标量与数组都成立——多选字段任一元素命中即算命中，与 evalCondition 同语义）。
func cellHits(cell string, raw any) bool {
	cell = strings.TrimSpace(cell)
	if cell == "" {
		return true
	}
	set := splitList(cell)
	return matchAny(raw, func(s string) bool { return containsStr(set, s) })
}

// ApplyDataRules 应用数据联动与决策表推导（纯函数）。
// 返回推导后的值副本（不修改入参）与触发的规则说明列表（供日志/前端提示）。
// 求值顺序：先 DataLinks（声明顺序），再 DecisionTables（声明顺序，各自取首个命中行）。
func ApplyDataRules(schema *FormSchema, values map[string]any) (map[string]any, []string) {
	out := make(map[string]any, len(values)+4)
	for k, v := range values {
		out[k] = v
	}
	var fired []string
	if schema == nil {
		return out, fired
	}
	declared := make(map[string]bool, len(schema.Fields))
	for _, f := range schema.Fields {
		declared[f.Key] = true
	}

	// 1) 数据联动规则
	for _, r := range schema.DataLinks {
		if len(r.Sets) == 0 || !EvalConditions(r.Conditions, r.Logic, out) {
			continue
		}
		applied := 0
		for _, s := range r.Sets {
			target := strings.TrimSpace(s.Target)
			if target == "" || !declared[target] {
				continue // 目标字段不在本 schema（配置漂移，容错跳过）
			}
			switch strings.TrimSpace(strings.ToLower(s.Mode)) {
			case SetCopyFrom:
				src := strings.TrimSpace(s.Value)
				if src == "" || src == target {
					continue
				}
				if v, ok := out[src]; ok && v != nil {
					out[target] = v
					applied++
				}
			default: // fixed
				out[target] = s.Value
				applied++
			}
		}
		if applied == 0 {
			continue
		}
		label := strings.TrimSpace(r.Name)
		if label == "" {
			label = r.ID
		}
		fired = append(fired, "联动:"+label)
	}

	// 2) 决策表：首个命中且启用的行生效
	for _, dt := range schema.DecisionTables {
		for _, row := range dt.Rows {
			if !rowEnabled(row) {
				continue
			}
			hit := true
			for _, k := range dt.ConditionKeys {
				if !cellHits(row.Cells[k], out[k]) {
					hit = false
					break
				}
			}
			if !hit {
				continue
			}
			for _, k := range dt.OutputKeys {
				if !declared[k] {
					continue
				}
				if v, ok := row.Outputs[k]; ok {
					out[k] = v
				}
			}
			label := strings.TrimSpace(dt.Name)
			if label == "" {
				label = dt.ID
			}
			fired = append(fired, "决策表:"+label)
			break
		}
	}
	return out, fired
}
