// Package quality 实现 CMDB 数据质量中心：扫描 CI 的完整性、唯一性、一致性（孤儿关系），
// 产出按模型类型的质量分与问题清单。它是 CMDB 被信任的「信用基石」——
// 复用已有的字段级置信度/来源机制（AttrMeta）作为输入信号之一。
//
// MVP 范围：
//   - 完整性：模型类型 schema 中 required 字段的填充率（按类型聚合）。
//   - 唯一性：同类型内重复 CI（共享 ip 或 name）。
//   - 一致性：孤儿关系（source/target 指向已不存在的 CI）。
//   - 综合分：上述维度加权；问题清单按严重度排序，供前端下钻。
package quality

import (
	"context"
	"strconv"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// Dimension 是质量维度。
type Dimension string

const (
	DimCompleteness Dimension = "completeness" // 必填字段填充率
	DimUniqueness   Dimension = "uniqueness"   // 重复 CI
	DimConsistency  Dimension = "consistency"  // 孤儿关系
)

// Severity 问题严重度。
type Severity string

const (
	SeverityHigh   Severity = "high"
	SeverityMedium Severity = "medium"
	SeverityLow    Severity = "low"
)

// Issue 是一条数据质量问题。
type Issue struct {
	CIID      string    `json:"ci_id,omitempty"`
	CIName    string    `json:"ci_name,omitempty"`
	TypeID    string    `json:"type_id,omitempty"`
	Dimension Dimension `json:"dimension"`
	Severity  Severity  `json:"severity"`
	Message   string    `json:"message"`
	// RelationID 一致性(孤儿关系)问题携带的关系 ID, 供一键修复定位删除目标。
	RelationID string `json:"relation_id,omitempty"`
}

// TypeReport 是单个模型类型的质量报告。
type TypeReport struct {
	TypeID       string  `json:"type_id"`
	Name         string  `json:"name"`
	Total        int     `json:"total"`
	Completeness float64 `json:"completeness"` // 0~100
	IssueCount   int     `json:"issue_count"`
}

// Report 是租户级数据质量报告。
type Report struct {
	TenantID  string       `json:"tenant_id"`
	ScannedAt time.Time    `json:"scanned_at"`
	Score     float64      `json:"score"` // 0~100 综合
	ByType    []TypeReport `json:"by_type"`
	Issues    []Issue      `json:"issues"`
}

// Engine 持有扫描所需的仓储依赖。
type Engine struct {
	pg *pg.Repository
}

// NewEngine 创建质量引擎。
func NewEngine(repo *pg.Repository) *Engine {
	return &Engine{pg: repo}
}

// Scan 执行一次全量数据质量扫描，返回报告。
func (e *Engine) Scan(ctx context.Context, tenantID string) (*Report, error) {
	report := &Report{
		TenantID:  tenantID,
		ScannedAt: time.Now(),
		ByType:    []TypeReport{},
		Issues:    []Issue{},
	}

	types, err := e.pg.ListCIModelTypes(ctx, tenantID, 1000, 0)
	if err != nil {
		return nil, err
	}

	// 收集全部 CI ID（用于孤儿关系检测）。
	allIDs := make(map[string]bool)

	var completenessSum float64
	for _, mt := range types {
		cis, err := e.pg.ListCI(ctx, tenantID, mt.ID)
		if err != nil {
			return nil, err
		}
		for _, ci := range cis {
			allIDs[ci.ID] = true
		}

		tr := TypeReport{TypeID: mt.ID, Name: mt.Name, Total: len(cis)}

		// 完整性：required 字段填充率。
		required := requiredFields(mt.Schema)
		if len(required) > 0 && len(cis) > 0 {
			var filled int
			for _, ci := range cis {
				for _, f := range required {
					if nonEmpty(ci.Attributes[f]) {
						filled++
					}
				}
			}
			tr.Completeness = float64(filled) / float64(len(required)*len(cis)) * 100
		}
		completenessSum += tr.Completeness

		// 唯一性：同类型内重复 ip / name。
		byIP := map[string][]*model.CIModel{}
		byName := map[string][]*model.CIModel{}
		for _, ci := range cis {
			if ip := firstIP(ci.Attributes); ip != "" {
				byIP[ip] = append(byIP[ip], ci)
			}
			if ci.Name != "" {
				byName[ci.Name] = append(byName[ci.Name], ci)
			}
		}
		for ip, group := range byIP {
			if len(group) > 1 {
				for _, ci := range group {
					report.Issues = append(report.Issues, Issue{
						CIID: ci.ID, CIName: ci.Name, TypeID: mt.ID,
						Dimension: DimUniqueness, Severity: SeverityHigh,
						Message: "重复 IP 地址 " + ip + "（同类型 " + mt.Name + " 存在 " + strconv.Itoa(len(group)) + " 个 CI）",
					})
					tr.IssueCount++
				}
			}
		}
		for nm, group := range byName {
			if len(group) > 1 {
				for _, ci := range group {
					report.Issues = append(report.Issues, Issue{
						CIID: ci.ID, CIName: ci.Name, TypeID: mt.ID,
						Dimension: DimUniqueness, Severity: SeverityMedium,
						Message: "重复名称 " + nm + "（同类型存在 " + strconv.Itoa(len(group)) + " 个 CI）",
					})
					tr.IssueCount++
				}
			}
		}

		report.ByType = append(report.ByType, tr)
	}

	// 一致性：孤儿关系。
	relations, err := e.pg.ListRelations(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	orphan := 0
	for _, rel := range relations {
		if !allIDs[rel.SourceID] || !allIDs[rel.TargetID] {
			report.Issues = append(report.Issues, Issue{
				Dimension:  DimConsistency,
				Severity:   SeverityMedium,
				Message:    "孤儿关系 " + rel.ID + "（" + rel.RelationType + "）：端点 " + rel.SourceID + "→" + rel.TargetID + " 指向已不存在的 CI",
				RelationID: rel.ID,
			})
			orphan++
		}
	}

	// 综合分：完整性均值（主），并扣减唯一性/一致性问题占比。
	if len(types) > 0 {
		report.Score = completenessSum / float64(len(types))
	}
	penalty := float64(len(report.Issues)) * 0.5
	if penalty > 40 {
		penalty = 40
	}
	report.Score -= penalty
	if report.Score < 0 {
		report.Score = 0
	}

	// 问题按严重度排序（high > medium > low）。
	sortIssues(report.Issues)
	return report, nil
}

// requiredFields 返回 schema 中标记为 required 的字段名。
func requiredFields(schema map[string]model.AttrDef) []string {
	out := make([]string, 0, len(schema))
	for name, def := range schema {
		if def.Required {
			out = append(out, name)
		}
	}
	return out
}

// nonEmpty 判断属性值是否非空（nil / 空串 / 空切片视为空）。
func nonEmpty(v any) bool {
	if v == nil {
		return false
	}
	switch x := v.(type) {
	case string:
		return x != ""
	case float64:
		return x != 0
	case int:
		return x != 0
	case bool:
		return x
	case []any:
		return len(x) > 0
	case map[string]any:
		return len(x) > 0
	}
	return true
}

// firstIP 从属性中提取首个 IP（兼容 ip / ip_address / management_ip）。
func firstIP(attrs map[string]any) string {
	for _, k := range []string{"ip", "ip_address", "management_ip", "private_ip"} {
		if v, ok := attrs[k]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// severityRank 严重度排序权重。
func severityRank(s Severity) int {
	switch s {
	case SeverityHigh:
		return 0
	case SeverityMedium:
		return 1
	default:
		return 2
	}
}

func sortIssues(issues []Issue) {
	for i := 1; i < len(issues); i++ {
		for j := i; j > 0 && severityRank(issues[j].Severity) < severityRank(issues[j-1].Severity); j-- {
			issues[j], issues[j-1] = issues[j-1], issues[j]
		}
	}
}
