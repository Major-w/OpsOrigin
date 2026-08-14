package model

type TopologyNode struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Name     string `json:"name"`
	TenantID string `json:"tenant_id"`
}

type TopologyEdge struct {
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
}

type ImpactResult struct {
	SourceCI   string   `json:"source_ci"`
	AffectedCI []string `json:"affected_ci"`
	Depth      int      `json:"depth"`
	HasCycle   bool     `json:"has_cycle"`

	// —— 业务影响视角（服务建模层, 由 Service 层聚合后填充, 可省略）——
	// AffectedCIDetails 受影响 CI 的明细(名称/类型/关键性), 供前端列表渲染。
	AffectedCIDetails []AffectedCIDetail `json:"affected_ci_details,omitempty"`
	// BusinessServices 受影响且位于服务链顶层的业务服务(按关键性降序)。
	BusinessServices []BusinessImpact `json:"business_services,omitempty"`
	// ImpactLevel 综合影响等级(critical/high/medium/low), 取受影响业务服务的最高级。
	ImpactLevel string `json:"impact_level,omitempty"`
}

// AffectedCIDetail 单个受影响 CI 的消费视图。
type AffectedCIDetail struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	TypeID      string `json:"type_id"`
	Criticality string `json:"criticality,omitempty"` // business_criticality 属性值
}

// BusinessImpact 受影响的一个业务服务及其关键性。
type BusinessImpact struct {
	CIID        string `json:"ci_id"`
	Name        string `json:"name"`
	Criticality string `json:"criticality"`
}

type TopologyQuery struct {
	TenantID string `json:"tenant_id"`
	CIType   string `json:"ci_type,omitempty"`
	MaxDepth int    `json:"max_depth,omitempty"`
}
