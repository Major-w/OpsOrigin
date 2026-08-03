// Package k8s 提供 K8s 容器资源纳管（接入方式 B：经 Agent 通道）。
//
// 设计要点（见 docs/k8s-onboarding-design.md）：
//   - K8s 不走 cloud.Provider（那是 AK/SK 模型），而是经既有 Agent 隧道
//     在目标集群的网关节点（单副本 Deployment）执行 `kubectl get ... -o json`，
//     回收 JSON 解析为 cloud.Resource。
//   - 本包仅 import cloud（取 Resource/ResourceType/接口），cloud 包不 import 本包，
//     避免 import 环；Agent 通道经 cloud.K8sAgentRunner 鸭子类型满足 *agentmgr.Service。
//   - 安全：kubeconfig 经 env（params）注入，绝不拼进命令行；脚本仅 list/get、不回显密钥。
//   - 失败容错：单类型失败仅记日志 continue（vuln 范式），返回已成功部分。
package k8s

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cloud"
)

// Discoverer 经 Agent 通道发现 K8s 集群的五类资源（cluster/namespace/workload/pod/container）。
type Discoverer struct {
	runner cloud.K8sAgentRunner // 实为由 cmd/core 注入的 *agentmgr.Service
}

// NewDiscoverer 构造 K8s 发现器。
func NewDiscoverer(runner cloud.K8sAgentRunner) *Discoverer {
	return &Discoverer{runner: runner}
}

// Discover 按请求类型，经 Agent 通道下发对应 kubectl 脚本，回收 JSON 解析为 Resource。
// 单类型失败仅记日志 continue，返回已成功部分（不拖垮整轮）。
func (d *Discoverer) Discover(ctx context.Context, tenantID, agentID string, kinds []cloud.ResourceType, opts cloud.K8sDiscoverOptions) ([]cloud.Resource, error) {
	if agentID == "" {
		return nil, fmt.Errorf("agent_id is required for k8s discovery")
	}
	if d.runner == nil {
		return nil, fmt.Errorf("k8s agent runner not configured")
	}
	params := map[string]string{}
	if opts.KubeconfigB64 != "" {
		params["OPS_KUBECONFIG_B64"] = opts.KubeconfigB64
	}
	if opts.KubeContext != "" {
		params["OPS_KUBE_CONTEXT"] = opts.KubeContext
	}

	// 1. 集群名（作为其余资源 ID 的 path 前缀，保证跨集群唯一）。
	clusterName, err := d.fetchCluster(ctx, tenantID, agentID, params)
	if err != nil {
		slog.Error("k8s: fetch cluster name failed, fallback to agent id", "agent", agentID, "error", err)
		clusterName = "agent:" + agentID
	}

	var out []cloud.Resource
	if cloudContains(kinds, cloud.TypeK8sCluster) {
		out = append(out, clusterResource(clusterName, agentID))
	}
	if cloudContains(kinds, cloud.TypeK8sNamespace) {
		rs, e := d.fetchNamespaces(ctx, tenantID, agentID, clusterName, params)
		if e != nil {
			slog.Error("k8s: discover namespaces failed, skipped", "agent", agentID, "error", e)
		} else {
			out = append(out, rs...)
		}
	}
	if cloudContains(kinds, cloud.TypeK8sWorkload) {
		rs, e := d.fetchWorkloads(ctx, tenantID, agentID, clusterName, params)
		if e != nil {
			slog.Error("k8s: discover workloads failed, skipped", "agent", agentID, "error", e)
		} else {
			out = append(out, rs...)
		}
	}
	if cloudContains(kinds, cloud.TypeK8sPod) || cloudContains(kinds, cloud.TypeK8sContainer) {
		pods, containers, e := d.fetchPods(ctx, tenantID, agentID, clusterName, params)
		if e != nil {
			slog.Error("k8s: discover pods failed, skipped", "agent", agentID, "error", e)
		} else {
			if cloudContains(kinds, cloud.TypeK8sPod) {
				out = append(out, pods...)
			}
			if cloudContains(kinds, cloud.TypeK8sContainer) {
				out = append(out, containers...)
			}
		}
	}
	return out, nil
}

// ---- 各类型发现（经 Agent 下发脚本） ----

func (d *Discoverer) fetchCluster(ctx context.Context, tenantID, agentID string, params map[string]string) (string, error) {
	out, _, err := d.runner.RunScriptOnAgent(ctx, agentID, tenantID, scriptForCluster(), params, 60, "")
	if err != nil {
		return "", err
	}
	name := strings.TrimSpace(out)
	if name == "" || name == "unknown" {
		return "agent:" + agentID, nil
	}
	return name, nil
}

func (d *Discoverer) fetchNamespaces(ctx context.Context, tenantID, agentID, clusterName string, params map[string]string) ([]cloud.Resource, error) {
	out, _, err := d.runner.RunScriptOnAgent(ctx, agentID, tenantID, scriptForNamespaces(), params, 60, "")
	if err != nil {
		return nil, err
	}
	items, err := parseList[k8sNamespace](out)
	if err != nil {
		return nil, err
	}
	var rs []cloud.Resource
	for _, it := range items {
		ns := it.Metadata.Name
		if ns == "" {
			continue
		}
		rs = append(rs, namespaceResource(clusterName, agentID, ns, it.Metadata.Labels))
	}
	return rs, nil
}

func (d *Discoverer) fetchWorkloads(ctx context.Context, tenantID, agentID, clusterName string, params map[string]string) ([]cloud.Resource, error) {
	out, _, err := d.runner.RunScriptOnAgent(ctx, agentID, tenantID, scriptForWorkloads(), params, 120, "")
	if err != nil {
		return nil, err
	}
	items, err := parseList[k8sWorkload](out)
	if err != nil {
		return nil, err
	}
	var rs []cloud.Resource
	for _, it := range items {
		ns := it.Metadata.Namespace
		name := it.Metadata.Name
		if name == "" {
			continue
		}
		var images []string
		for _, c := range it.Spec.Template.Spec.Containers {
			if c.Image != "" {
				images = append(images, c.Image)
			}
		}
		rs = append(rs, workloadResource(clusterName, agentID, ns, it.Kind, name, it.Metadata.Labels, images, derefInt32(it.Spec.Replicas)))
	}
	return rs, nil
}

func (d *Discoverer) fetchPods(ctx context.Context, tenantID, agentID, clusterName string, params map[string]string) ([]cloud.Resource, []cloud.Resource, error) {
	out, _, err := d.runner.RunScriptOnAgent(ctx, agentID, tenantID, scriptForPods(), params, 120, "")
	if err != nil {
		return nil, nil, err
	}
	items, err := parseList[k8sPod](out)
	if err != nil {
		return nil, nil, err
	}
	var pods, containers []cloud.Resource
	for _, it := range items {
		ns := it.Metadata.Namespace
		pod := it.Metadata.Name
		if pod == "" {
			continue
		}
		ownerKind, ownerName := ownerRef(it.Metadata.OwnerReferences)
		pods = append(pods, podResource(clusterName, agentID, ns, pod, it.Spec.NodeName, it.Status.Phase, it.Status.PodIP, ownerKind, ownerName, it.Metadata.Labels))
		for _, c := range it.Spec.Containers {
			if c.Name == "" {
				continue
			}
			containers = append(containers, containerResource(clusterName, agentID, ns, pod, c, ownerKind, ownerName, it.Metadata.Labels))
		}
	}
	return pods, containers, nil
}

// ---- Resource 组装（path 风格 ID，cluster 前缀保证跨集群唯一） ----

func clusterResource(clusterName, agentID string) cloud.Resource {
	return cloud.Resource{
		Provider: cloud.K8sProviderName,
		Type:     cloud.TypeK8sCluster,
		ID:       "cluster=" + clusterName,
		Name:     clusterName,
		Status:   "active",
		Extra: map[string]any{
			"agent_id":         agentID,
			"gateway_agent_id": agentID,
			"current_context":  clusterName,
		},
		DiscoveredAt: time.Now().UTC(),
	}
}

func namespaceResource(clusterName, agentID, ns string, labels map[string]string) cloud.Resource {
	return cloud.Resource{
		Provider: cloud.K8sProviderName,
		Type:     cloud.TypeK8sNamespace,
		ID:       clusterName + "/ns/" + ns,
		Name:     ns,
		Status:   "active",
		Labels:   labels,
		Extra: map[string]any{
			"cluster":  clusterName,
			"agent_id": agentID,
		},
		DiscoveredAt: time.Now().UTC(),
	}
}

func workloadResource(clusterName, agentID, ns, kind, name string, labels map[string]string, images []string, replicas int32) cloud.Resource {
	return cloud.Resource{
		Provider: cloud.K8sProviderName,
		Type:     cloud.TypeK8sWorkload,
		ID:       clusterName + "/ns/" + ns + "/wl/" + kind + "/" + name,
		Name:     name,
		Status:   "running",
		Labels:   labels,
		Extra: map[string]any{
			"cluster":   clusterName,
			"namespace": ns,
			"kind":      kind,
			"replicas":  replicas,
			"images":    images,
			"agent_id":  agentID,
		},
		DiscoveredAt: time.Now().UTC(),
	}
}

func podResource(clusterName, agentID, ns, pod, node, phase, podIP, ownerKind, ownerName string, labels map[string]string) cloud.Resource {
	extra := map[string]any{
		"cluster":   clusterName,
		"namespace": ns,
		"node":      node,
		"phase":     phase,
		"pod_ip":    podIP,
		"agent_id":  agentID,
	}
	if ownerKind != "" {
		extra["owner_kind"] = ownerKind
		extra["owner_name"] = ownerName
	}
	return cloud.Resource{
		Provider:     cloud.K8sProviderName,
		Type:         cloud.TypeK8sPod,
		ID:           clusterName + "/ns/" + ns + "/pod/" + pod,
		Name:         pod,
		Status:       phase,
		Labels:       labels,
		Extra:        extra,
		DiscoveredAt: time.Now().UTC(),
	}
}

func containerResource(clusterName, agentID, ns, pod string, c k8sContainer, ownerKind, ownerName string, labels map[string]string) cloud.Resource {
	ports := make([]map[string]any, 0, len(c.Ports))
	for _, p := range c.Ports {
		ports = append(ports, map[string]any{"container_port": p.ContainerPort, "protocol": p.Protocol})
	}
	extra := map[string]any{
		"cluster":           clusterName,
		"namespace":         ns,
		"pod":               pod,
		"image":             c.Image,
		"image_pull_policy": c.ImagePullPolicy,
		"ports":             ports,
		"agent_id":          agentID,
	}
	if ownerKind != "" {
		extra["owner_kind"] = ownerKind
		extra["owner_name"] = ownerName
	}
	return cloud.Resource{
		Provider:     cloud.K8sProviderName,
		Type:         cloud.TypeK8sContainer,
		ID:           clusterName + "/ns/" + ns + "/pod/" + pod + "/c/" + c.Name,
		Name:         c.Name,
		Status:       "running",
		Labels:       labels,
		Extra:        extra,
		DiscoveredAt: time.Now().UTC(),
	}
}

// ---- kubectl JSON 解析结构（最小化，按需扩展） ----

type k8sMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	Labels          map[string]string `json:"labels"`
	OwnerReferences []struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	} `json:"ownerReferences"`
}

type k8sContainer struct {
	Name            string `json:"name"`
	Image           string `json:"image"`
	ImagePullPolicy string `json:"imagePullPolicy"`
	Ports           []struct {
		ContainerPort int32  `json:"containerPort"`
		Protocol      string `json:"protocol"`
	} `json:"ports"`
	Resources any `json:"resources"`
}

type k8sPodSpec struct {
	NodeName       string         `json:"nodeName"`
	Containers     []k8sContainer `json:"containers"`
	InitContainers []k8sContainer `json:"initContainers"`
}

type k8sPod struct {
	Metadata k8sMeta    `json:"metadata"`
	Spec     k8sPodSpec `json:"spec"`
	Status   struct {
		Phase string `json:"phase"`
		PodIP string `json:"podIP"`
	} `json:"status"`
}

type k8sWorkload struct {
	Kind     string  `json:"kind"`
	Metadata k8sMeta `json:"metadata"`
	Spec     struct {
		Replicas *int32 `json:"replicas"`
		Template struct {
			Spec struct {
				Containers []k8sContainer `json:"containers"`
			} `json:"spec"`
		} `json:"template"`
	} `json:"spec"`
	Status struct {
		Replicas int32 `json:"replicas"`
	} `json:"status"`
}

type k8sNamespace struct {
	Metadata k8sMeta `json:"metadata"`
}

// parseList 解析 kubectl get ... -o json 的 items 列表（泛型，Go 1.21+）。
func parseList[T any](out string) ([]T, error) {
	var l struct {
		Items []T `json:"items"`
	}
	if err := json.Unmarshal([]byte(out), &l); err != nil {
		return nil, err
	}
	return l.Items, nil
}

func ownerRef(refs []struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
}) (string, string) {
	if len(refs) == 0 {
		return "", ""
	}
	return refs[0].Kind, refs[0].Name
}

func derefInt32(p *int32) int32 {
	if p == nil {
		return 0
	}
	return *p
}

func cloudContains(kinds []cloud.ResourceType, t cloud.ResourceType) bool {
	for _, k := range kinds {
		if k == t {
			return true
		}
	}
	return false
}

// ---- 脚本构造（安全：env 注入 kubeconfig，绝不拼命令行参数） ----

// k8sEnvSetup 仅在经 params 注入了 kubeconfig/context 时启用；
// OPS_KUBECONFIG_B64 / OPS_KUBE_CONTEXT 由 Agent 端以环境变量形式设置。
const k8sEnvSetup = `# 如经 params 注入了 kubeconfig，则启用（env 由 Agent 端设置，绝不当 CLI 参数）
if [ -n "$OPS_KUBECONFIG_B64" ]; then
  echo "$OPS_KUBECONFIG_B64" | base64 -d > /tmp/ops-kc.yaml
  export KUBECONFIG=/tmp/ops-kc.yaml
fi
[ -n "$OPS_KUBE_CONTEXT" ] && kubectl config use-context "$OPS_KUBE_CONTEXT" >/dev/null 2>&1
`

func scriptForCluster() string {
	return "#!/bin/bash\nset -o pipefail\n" +
		"kubectl config current-context 2>/dev/null || echo unknown\n"
}

func scriptForNamespaces() string {
	return "#!/bin/bash\nset -o pipefail\n" + k8sEnvSetup +
		"kubectl get namespaces -o json\n"
}

func scriptForWorkloads() string {
	return "#!/bin/bash\nset -o pipefail\n" + k8sEnvSetup +
		"kubectl get deployments.apps,statefulsets.apps,daemonsets.apps -A -o json\n"
}

func scriptForPods() string {
	return "#!/bin/bash\nset -o pipefail\n" + k8sEnvSetup +
		"kubectl get pods -A -o json\n"
}
