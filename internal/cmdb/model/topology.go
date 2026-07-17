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
}

type TopologyQuery struct {
	TenantID string `json:"tenant_id"`
	CIType   string `json:"ci_type,omitempty"`
	MaxDepth int    `json:"max_depth,omitempty"`
}
