package cloud

import (
	"reflect"
	"testing"
)

// TestCITypeOf 验证资源类型 -> CMDB CI 类型常量的映射正确性。
func TestCITypeOf(t *testing.T) {
	cases := []struct {
		typ      ResourceType
		provider string
		want     string
	}{
		// 有 Provider：厂商化命名 <provider>_<type>（与内置云资源模型对齐）
		{TypeVM, "aliyun", "aliyun_vm"},
		{TypeDatabase, "tencent", "tencent_database"},
		{TypeLoadBalancer, "aws", "aws_load_balancer"},
		{TypeVPC, "openstack", "openstack_vpc"},
		// 无 Provider：回退通用 cloud_<type>
		{TypeVM, "", CITypeVM},
		{TypeDatabase, "", CITypeDatabase},
		{TypeLoadBalancer, "", CITypeLoadBalancer},
		{TypeVPC, "", "cloud_vpc"},
		{TypeBucket, "", "cloud_bucket"},
		// K8s 类型经 Agent 通道发现（非 Provider），有显式映射分支，不带厂商/cloud_ 前缀
		{TypeK8sCluster, "k8s", CITypeK8sCluster},
	}
	for _, c := range cases {
		got := CITypeOf(Resource{Type: c.typ, Provider: c.provider})
		if got != c.want {
			t.Errorf("CITypeOf(%q, provider=%q) = %q, want %q", c.typ, c.provider, got, c.want)
		}
	}
}

// TestResourceNaturalKey 验证自然键格式 <provider>:<type>:<id>。
func TestResourceNaturalKey(t *testing.T) {
	cases := []struct {
		res  Resource
		want string
	}{
		{Resource{Provider: "aws", Type: TypeVM, ID: "i-123"}, "aws:vm:i-123"},
		{Resource{Provider: "aliyun", Type: TypeDatabase, ID: "rm-abc"}, "aliyun:database:rm-abc"},
		{Resource{Provider: "tencent", Type: TypeLoadBalancer, ID: "lb-x/y"}, "tencent:load_balancer:lb-x/y"},
		{Resource{Provider: "huawei", Type: TypeVM, ID: ""}, "huawei:vm:"},
	}
	for _, c := range cases {
		if got := resourceNaturalKey(c.res); got != c.want {
			t.Errorf("resourceNaturalKey(%+v) = %q, want %q", c.res, got, c.want)
		}
	}
}

// TestToCIMapping 验证 Resource -> CIMapping 完整映射（纯函数单测）。
func TestToCIMapping(t *testing.T) {
	res := Resource{
		Provider:  "aws",
		Type:      TypeVM,
		ID:        "i-123",
		Name:      "my-vm",
		Region:    "us-east-1",
		Zone:      "us-east-1a",
		Status:    "running",
		PublicIP:  "1.1.1.1",
		PrivateIP: "10.0.0.1",
		Labels:    map[string]string{"env": "prod"},
	}
	m := ToCIMapping(res, "tenantA")

	if m.TypeID != "aws_vm" {
		t.Errorf("TypeID = %q, want %q", m.TypeID, "aws_vm")
	}
	if m.TenantID != "tenantA" {
		t.Errorf("TenantID = %q, want %q", m.TenantID, "tenantA")
	}
	if m.Status != "active" {
		t.Errorf("Status = %q, want %q", m.Status, "active")
	}
	if m.Name != "my-vm" {
		t.Errorf("Name = %q, want %q", m.Name, "my-vm")
	}
	if m.NaturalKey != "aws:vm:i-123" {
		t.Errorf("NaturalKey = %q, want %q", m.NaturalKey, "aws:vm:i-123")
	}

	// Attributes 透传与关键自然键
	if rid, ok := m.Attributes["resource_id"].(string); !ok || rid != "aws:vm:i-123" {
		t.Errorf("Attributes[resource_id] = %v, want %q", m.Attributes["resource_id"], "aws:vm:i-123")
	}
	expectAttrs := map[string]any{
		"provider":      "aws",
		"resource_type": "vm",
		"region":        "us-east-1",
		"zone":          "us-east-1a",
		"status":        "running",
		"public_ip":     "1.1.1.1",
		"private_ip":    "10.0.0.1",
	}
	for k, v := range expectAttrs {
		if m.Attributes[k] != v {
			t.Errorf("Attributes[%q] = %v, want %v", k, m.Attributes[k], v)
		}
	}
	if !reflect.DeepEqual(m.Labels, map[string]string{"env": "prod"}) {
		t.Errorf("Labels = %v, want map[env:prod]", m.Labels)
	}
}

// TestToCIMappingNameFallback 验证 Name 为空时回退到 ID。
func TestToCIMappingNameFallback(t *testing.T) {
	res := Resource{Provider: "aws", Type: TypeDatabase, ID: "db-1"}
	m := ToCIMapping(res, "tenantA")

	if m.Name != "db-1" {
		t.Errorf("Name should fallback to ID, got %q", m.Name)
	}
	if m.TypeID != "aws_database" {
		t.Errorf("TypeID = %q, want %q", m.TypeID, "aws_database")
	}
	if m.Status != "active" {
		t.Errorf("Status = %q, want %q", m.Status, "active")
	}
}

// TestToCIMappingEmptyLabels 验证无 Labels/Extra 时不写入多余字段。
func TestToCIMappingEmptyLabels(t *testing.T) {
	res := Resource{Provider: "aws", Type: TypeVM, ID: "i-9"}
	m := ToCIMapping(res, "t")
	if _, ok := m.Attributes["labels"]; ok {
		t.Errorf("Attributes should not contain 'labels' when empty")
	}
	if _, ok := m.Attributes["extra"]; ok {
		t.Errorf("Attributes should not contain 'extra' when empty")
	}
}
