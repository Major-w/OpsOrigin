// Package cloud 提供多云资源纳管的统一抽象层。
// 设计目标：纳管常见公有云（阿里云/腾讯云/华为云/AWS）的计算、数据库、
// 负载均衡等资源，结果统一映射为 Resource，并衔接 CMDB。
//
// 各云差异通过 Provider 接口隔离；新增云只需实现 Provider 并注册，
// 上层（采集调度、CMDB 同步、UI）无需感知具体厂商 SDK。
package cloud

import (
	"context"
	"fmt"
	"time"
)

// ResourceType 标识被纳管资源的种类。
type ResourceType string

const (
	TypeVM           ResourceType = "vm"            // 云主机 / 实例
	TypeDatabase     ResourceType = "database"      // 云数据库实例
	TypeLoadBalancer ResourceType = "load_balancer" // 负载均衡
	TypeVPC          ResourceType = "vpc"           // 私有网络
	TypeBucket       ResourceType = "bucket"        // 对象存储桶
	TypeDisk         ResourceType = "disk"          // 云硬盘
	TypeFirewall     ResourceType = "firewall"      // 安全组/防火墙
	TypeNAT          ResourceType = "nat_gateway"   // NAT 网关
	TypeCDN          ResourceType = "cdn"           // CDN 加速
	TypeDNS          ResourceType = "dns"           // DNS 域名解析
	TypeK8sCluster   ResourceType = "k8s_cluster"    // 托管 K8s 集群（经 Agent 通道发现，非 Provider）
	TypeK8sNamespace ResourceType = "k8s_namespace"  // K8s 命名空间
	TypeK8sWorkload  ResourceType = "k8s_workload"   // K8s 工作负载
	TypeK8sPod       ResourceType = "k8s_pod"        // K8s Pod
	TypeK8sContainer ResourceType = "k8s_container"  // K8s 容器
)

// K8sResourceTypes 返回经 Agent 通道纳管的五类 K8s 资源类型。
// 注意：这些类型不属于 AK/SK 模型的 Provider，故不计入 AllResourceTypes。
func K8sResourceTypes() []ResourceType {
	return []ResourceType{
		TypeK8sCluster, TypeK8sNamespace, TypeK8sWorkload, TypeK8sPod, TypeK8sContainer,
	}
}

// AllResourceTypes 返回全部受支持的资源类型。
func AllResourceTypes() []ResourceType {
	return []ResourceType{
		TypeVM, TypeDatabase, TypeLoadBalancer,
		TypeVPC, TypeBucket, TypeDisk, TypeFirewall, TypeNAT, TypeCDN, TypeDNS,
	}
}

// Resource 是多云资源的统一表示，直接可映射为 CMDB CI。
type Resource struct {
	Provider   string            `json:"provider"`              // 厂商名（与 Provider.Name 一致）
	Type       ResourceType     `json:"type"`                  // 资源类型
	ID         string            `json:"id"`                    // 云侧资源 ID
	Name       string            `json:"name"`                  // 展示名
	Region     string            `json:"region"`                // 地域
	Zone       string            `json:"zone,omitempty"`       // 可用区
	Status     string            `json:"status"`                // 运行状态（running/available/...）
	PublicIP   string            `json:"public_ip,omitempty"`  // 公网 IP
	PrivateIP  string            `json:"private_ip,omitempty"` // 私网 IP
	Labels     map[string]string `json:"labels,omitempty"`    // 标签
	Extra      map[string]any   `json:"extra,omitempty"`     // 厂商特有字段
	DiscoveredAt time.Time       `json:"discovered_at"`        // 采集时间
}

// Metric 是一条资源指标（如 CPU/内存/连接数）。
type Metric struct {
	Provider  string    `json:"provider"`
	ResourceID string    `json:"resource_id"`
	Type      ResourceType `json:"type"`
	Name      string    `json:"name"`
	Value     float64   `json:"value"`
	Unit      string    `json:"unit,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Credential 描述访问某云的凭证。凭证内容仅在内存保存，
// 生产环境应替换为加密存储（见优化报告 2.4）。
type Credential struct {
	Provider string            `json:"provider"`
	Region   string            `json:"region,omitempty"`
	// 通用字段（兼容多数云）
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	// 云厂商特有
	Extra map[string]string `json:"extra,omitempty"`
}

// Provider 是单云的纳管实现契约。
// 新增云：实现本接口并在 init() 中调用 cloud.Register。
type Provider interface {
	// Name 返回厂商标识（如 "aliyun"），须全局唯一。
	Name() string
	// ListResources 列举指定地域、指定类型的资源。
	ListResources(ctx context.Context, cred Credential, region string, kinds []ResourceType) ([]Resource, error)
	// GetResource 获取单个资源详情。
	GetResource(ctx context.Context, cred Credential, region, kind, id string) (*Resource, error)
	// CollectMetrics 采集给定资源的指标（按需实现，可返回 nil）。
	CollectMetrics(ctx context.Context, cred Credential, region string, resources []Resource) ([]Metric, error)
}

// supportedKinds 记录各 Provider 对外声明的可纳管资源类型，用于 UI 类型选择。
// 各 Provider 包在 init() 中调用 RegisterKinds 填充；未注册的厂商回退到默认三类。
var supportedKinds = map[string][]ResourceType{}

// RegisterKinds 由 Provider 在 init() 中调用，声明其支持的资源类型。
func RegisterKinds(name string, kinds ...ResourceType) {
	supportedKinds[name] = kinds
}

// ProviderKinds 返回指定厂商对外声明的资源类型；未注册时回退到默认三类。
func ProviderKinds(name string) []ResourceType {
	if k, ok := supportedKinds[name]; ok && len(k) > 0 {
		return k
	}
	return []ResourceType{TypeVM, TypeDatabase, TypeLoadBalancer}
}

// ProviderNotFound 在查询未注册厂商时返回。
type ProviderNotFound struct{ Name string }

func (e *ProviderNotFound) Error() string {
	return fmt.Sprintf("cloud provider %q not registered", e.Name)
}

// NoSuchResource 表示资源不存在。
type NoSuchResource struct{ Provider, ID string }

func (e *NoSuchResource) Error() string {
	return fmt.Sprintf("provider %q: resource %q not found", e.Provider, e.ID)
}
