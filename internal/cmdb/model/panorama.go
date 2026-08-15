package model

import "context"

// 全景可观测拓扑（业务可观测全景）查询期投影结构。
// 融合 = 给 CMDB 拓扑「加 layer 维度边」+「加节点健康度」，CI ID 是唯一胶水。
// 不在 CMDB/监控之外新建存储：本文件仅定义投影结构 + 监控快照接口。
// 详见 .docs/design-panorama-observability-topology.md。

// PanoramaNode 是融合后图中的一个节点（= 一个 CI）。
type PanoramaNode struct {
	ID       string `json:"id"`       // = CI ID（唯一身份）
	Type     string `json:"type"`     // = ci.TypeID（mysql / postgresql / redis ...）
	Category string `json:"category"` // 由 typeId 经模型类型映射推导（数据库/中间件/主机/应用/服务）
	Name     string `json:"name"`
	// —— 健康度 rollup ——
	Health       string `json:"health"` // critical | warning | healthy | unknown
	Status       string `json:"status"` // up | down | unknown（来自 MonitorTarget）
	AlertSummary struct {
		Critical  int  `json:"critical"`
		Warning   int  `json:"warning"`
		Info      int  `json:"info"`
		RootCause bool `json:"rootCause"` // 是否为根因节点
	} `json:"alertSummary"`
	Metrics map[string]any `json:"metrics,omitempty"` // 关键指标 tooltip（latency/pcpu/...）
}

// PanoramaEdge 是融合后图中的一条边。纵向（CMDB）与横向（callchain）是同一张图里的不同 layer 边。
type PanoramaEdge struct {
	Source       string  `json:"source"`              // CI ID
	Target       string  `json:"target"`              // CI ID
	Layer        string  `json:"layer"`               // cmdb-structural | cmdb-logical | callchain
	RelationType string  `json:"relationType"`        // runs_on | depends_on | connects_to | calls
	LatencyMs    float64 `json:"latencyMs,omitempty"` // 仅 callchain 边带
	ErrorRate    float64 `json:"errorRate,omitempty"` // 仅 callchain 边带
	Qps          float64 `json:"qps,omitempty"`       // 仅 callchain 边带
	Health       string  `json:"health,omitempty"`    // calls 边按 errorRate 标红
}

// PanoramaAlert 是活跃告警的轻量投影（供前端侧栏/影响域渲染）。
type PanoramaAlert struct {
	ID          string `json:"id"`
	CIID        string `json:"ciId"`
	Severity    string `json:"severity"` // critical | warning | info
	RootCause   bool   `json:"rootCause"`
	RootCauseCI string `json:"rootCauseCi,omitempty"`
	Message     string `json:"message"`
}

// PanoramaResponse 是 GET /api/v1/panorama 的返回体。
type PanoramaResponse struct {
	Nodes   []PanoramaNode  `json:"nodes"`
	Edges   []PanoramaEdge  `json:"edges"`
	Alerts  []PanoramaAlert `json:"alerts"`
	Summary PanoramaSummary `json:"summary"`
}

// PanoramaSummary 是全景图的整体健康统计。
type PanoramaSummary struct {
	Total             int     `json:"total"`
	Critical          int     `json:"critical"`
	Warning           int     `json:"warning"`
	Healthy           int     `json:"healthy"`
	Unknown           int     `json:"unknown"`
	CallsErrorRateP95 float64 `json:"callsErrorRateP95,omitempty"`
}

// MonitorSnapshot 是全景端点从监控侧拉取的轻量快照。
// 与 monitor 包完全解耦：由 cmd/core 的适配器把 monitor.Service 的数据映射进来，
// cmdb 不 import monitor，避免循环依赖。
type MonitorSnapshot struct {
	// StatusByCI: CIID -> "up" | "down" | "unknown"（来自 MonitorTarget）。
	StatusByCI map[string]string
	// Alerts: 活跃告警列表。
	Alerts []PanoramaAlert
}

// MonitorSnapshotProvider 由 cmd/core 注入（适配 monitor.Service），nil 安全：
// 未注入时全景图仅呈现 CMDB 拓扑骨架，健康度全部为 unknown。
type MonitorSnapshotProvider interface {
	Snapshot(ctx context.Context, tenantID string) (*MonitorSnapshot, error)
}

// 关系类型 → 全景 layer 映射（设计 §3）。
//
//	runs_on / runs_on_node        → cmdb-structural（部署骨架）
//	depends_on / connects_to / connects → cmdb-logical（逻辑依赖）
func LayerOfRelation(relationType string) string {
	switch relationType {
	case "runs_on", "runs_on_node":
		return "cmdb-structural"
	case "depends_on", "connects_to", "connects":
		return "cmdb-logical"
	default:
		return "cmdb-logical"
	}
}
