package cloud

// 本文件定义 Resource → CMDB CI 的映射契约，刻意不 import cmdb 包，
// 以保持 internal/cloud 纯库性、避免 import 环。具体 CIModel 的组装
// 由 cmd/core/cloud_adapter.go（实现 CMDBWriter）完成。

// CI 类型常量：按资源类型固定命名，对应设计文档 §2.2。
const (
	CITypeVM           = "cloud_vm"
	CITypeDatabase     = "cloud_database"
	CITypeLoadBalancer = "cloud_load_balancer"

	// K8s 资源类 CI 类型（经 Agent 通道发现，自然落 CMDB，无需预注册 schema）。
	CITypeK8sCluster   = "k8s_cluster"
	CITypeK8sNamespace = "k8s_namespace"
	CITypeK8sWorkload  = "k8s_workload"
	CITypeK8sPod       = "k8s_pod"
	CITypeK8sContainer = "k8s_container"
)

// CIMapping 是 Resource 到 CMDB CI 的中间映射（定义在 cloud 包内，避免直接依赖 cmdb）。
// cloudCMDBAdapter 据此构造 *model.CIModel。
type CIMapping struct {
	TypeID     string
	TenantID   string
	Name       string
	Status     string
	Attributes map[string]any
	Labels     map[string]string
	NaturalKey string
}

// CITypeOf 返回资源对应的 CMDB CI 类型。
func CITypeOf(res Resource) string {
	switch res.Type {
	case TypeVM:
		return CITypeVM
	case TypeDatabase:
		return CITypeDatabase
	case TypeLoadBalancer:
		return CITypeLoadBalancer
	case TypeK8sCluster:
		return CITypeK8sCluster
	case TypeK8sNamespace:
		return CITypeK8sNamespace
	case TypeK8sWorkload:
		return CITypeK8sWorkload
	case TypeK8sPod:
		return CITypeK8sPod
	case TypeK8sContainer:
		return CITypeK8sContainer
	default:
		return "cloud_" + string(res.Type)
	}
}

// resourceNaturalKey 生成资源自然键 "<provider>:<type>:<id>"，用于 upsert 去重。
func resourceNaturalKey(res Resource) string {
	return res.Provider + ":" + string(res.Type) + ":" + res.ID
}

// ToCIMapping 把一条 Resource 映射为 CMDB CI 的中间表示。
// 自然键 attributes["resource_id"] = "<provider>:<type>:<id>"；
// 其余属性透传 provider/resource_type/region/zone/status/public_ip/private_ip/labels/extra。
func ToCIMapping(res Resource, tenantID string) CIMapping {
	attrs := map[string]any{
		"provider":     res.Provider,
		"resource_type": string(res.Type),
		"region":       res.Region,
		"zone":         res.Zone,
		"status":       res.Status,
		"public_ip":    res.PublicIP,
		"private_ip":   res.PrivateIP,
		"resource_id":  resourceNaturalKey(res),
	}
	if len(res.Labels) > 0 {
		attrs["labels"] = res.Labels
	}
	if len(res.Extra) > 0 {
		attrs["extra"] = res.Extra
	}
	name := res.Name
	if name == "" {
		name = res.ID
	}
	return CIMapping{
		TypeID:     CITypeOf(res),
		TenantID:   tenantID,
		Name:       name,
		Status:     "active",
		Attributes: attrs,
		Labels:     res.Labels,
		NaturalKey: resourceNaturalKey(res),
	}
}
