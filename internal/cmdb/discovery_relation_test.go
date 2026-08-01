package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

func TestBareID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"aliyun:vpc:vpc-123", "vpc-123"},
		{"vpc-123", "vpc-123"},
		{"aws:vm:i-abc", "i-abc"},
		{"no-colon", "no-colon"},
	}
	for _, c := range cases {
		if got := bareID(c.in); got != c.want {
			t.Errorf("bareID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestRelationTypeFor(t *testing.T) {
	cases := []struct{ in, want string }{
		{"vpc_id", "belongs_to_vpc"},
		{"subnet_id", "in_subnet"},
		{"instance_id", "runs_on"},
		{"host_id", "runs_on"},
		{"cluster_id", "member_of_cluster"},
		{"node", "runs_on_node"},
		{"security_group_id", "protected_by_sg"},
		{"zone_id", "located_in_zone"},
		{"foo_id", "references"},
	}
	for _, c := range cases {
		if got := relationTypeFor(c.in); got != c.want {
			t.Errorf("relationTypeFor(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestMatchTargets(t *testing.T) {
	vpc := &model.CIModel{ID: "vpc1", TenantID: "t1", TypeID: "cloud_vpc",
		Attributes: map[string]any{"resource_id": "aliyun:vpc:vpc-123", "provider": "aliyun"}}
	vm := &model.CIModel{ID: "vm1", TenantID: "t1", TypeID: "cloud_vm",
		Attributes: map[string]any{"resource_id": "aliyun:vm:i-1", "provider": "aliyun", "vpc_id": "vpc-123"}}

	byFull := map[string]*model.CIModel{
		vpc.Attributes["resource_id"].(string): vpc,
		vm.Attributes["resource_id"].(string):  vm,
	}
	byBare := map[string][]*model.CIModel{}
	for _, ci := range []*model.CIModel{vpc, vm} {
		b := bareID(ci.Attributes["resource_id"].(string))
		byBare[b] = append(byBare[b], ci)
	}

	// vm.vpc_id="vpc-123" → 裸 id 匹配 + 同 provider → vpc1
	got := matchTargets("vpc-123", vm, byFull, byBare)
	if len(got) != 1 || got[0].ID != "vpc1" {
		t.Fatalf("matchTargets(vpc-123) = %+v, want [vpc1]", got)
	}

	// 跨 provider 不应误连：加入一个 aws 的 vpc-123
	awsVPC := &model.CIModel{ID: "awsVpc", TenantID: "t1", TypeID: "cloud_vpc",
		Attributes: map[string]any{"resource_id": "aws:vpc:vpc-123", "provider": "aws"}}
	byBare[bareID("aws:vpc:vpc-123")] = append(byBare[bareID("aws:vpc:vpc-123")], awsVPC)
	got = matchTargets("vpc-123", vm, byFull, byBare)
	if len(got) != 1 || got[0].ID != "vpc1" {
		t.Fatalf("matchTargets cross-provider = %+v, want only [vpc1]", got)
	}

	// 精确全键匹配
	got = matchTargets("aliyun:vpc:vpc-123", vm, byFull, byBare)
	if len(got) != 1 || got[0].ID != "vpc1" {
		t.Fatalf("matchTargets(full) = %+v, want [vpc1]", got)
	}

	// 不回连自身
	got = matchTargets("aliyun:vm:i-1", vm, byFull, byBare)
	if len(got) != 0 {
		t.Fatalf("matchTargets(self) = %+v, want []", got)
	}
}
