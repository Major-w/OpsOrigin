// Package probe 实现 K8s 集群探针（DaemonSet / 集群级采集器）。
// 探针以「参考主机 Agent」的方式接入：采集后通过 HTTP 上报 Core，
// 也可经 Agent 的 WS/gRPC 隧道通道（后续接入），无需 Core 主动连集群。
package probe

import "time"

// K8sReport 是一次采集的完整结果，POST 到 Core 的 /api/v1/k8s/report。
type K8sReport struct {
	Cluster   string           `json:"cluster"`             // 集群标识（用户在 Core 登记）
	AgentID   string           `json:"agent_id"`          // 探针自身 ID（首次由 Core 分配）
	CollectedAt time.Time        `json:"collected_at"`
	Nodes     []K8sNode      `json:"nodes,omitempty"`
	Pods      []K8sPod       `json:"pods,omitempty"`
	Workloads []K8sWorkload  `json:"workloads,omitempty"`
	Events    []K8sEvent     `json:"events,omitempty"`
	Metrics   []K8sMetric    `json:"metrics,omitempty"`
}

// K8sNode 是单节点快照。
type K8sNode struct {
	Name      string `json:"name"`
	Ready     bool   `json:"ready"`
	Roles     string `json:"roles,omitempty"` // 如 "master"/"worker"
	Kubelet   string `json:"kubelet,omitempty"`
	OSImage   string `json:"os_image,omitempty"`
	CPUCapacity string `json:"cpu_capacity,omitempty"`
	MemCapacity string `json:"mem_capacity,omitempty"`
}

// K8sPod 是单 Pod 快照。
type K8sPod struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Node      string `json:"node,omitempty"`
	Phase     string `json:"phase,omitempty"`
	Restarts  int32  `json:"restarts"`
	Ready     bool   `json:"ready"`
}

// K8sWorkload 是工作负载（Deployment/StatefulSet 等）快照。
type K8sWorkload struct {
	Kind       string `json:"kind"` // Deployment / StatefulSet / DaemonSet
	Name       string `json:"name"`
	Namespace  string `json:"namespace"`
	Replicas  int32  `json:"replicas"`
	Ready      int32  `json:"ready_replicas"`
	Available  int32  `json:"available_replicas"`
}

// K8sEvent 是关键事件（Warning/异常为主）。
type K8sEvent struct {
	Type      string `json:"type"` // Normal / Warning
	Reason    string `json:"reason"`
	Object     string `json:"object"` // 涉及对象（kind/name）
	Message   string `json:"message,omitempty"`
	Count      int32  `json:"count"`
	LastSeen  int64  `json:"last_seen_unix"`
}

// K8sMetric 是一条指标（状态/派生计数类，无需 metrics-server）。
// 如需原始 CPU/内存，可后续接 metrics-server 或 kubelet /stats/summary。
type K8sMetric struct {
	Name      string  `json:"name"` // 如 node_ready / pod_restarts_total
	Value     float64 `json:"value"`
	Unit      string  `json:"unit,omitempty"`
	Labels    map[string]string `json:"labels,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}
