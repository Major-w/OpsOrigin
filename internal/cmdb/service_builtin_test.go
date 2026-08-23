package cmdb

import (
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// 内置模型必须与自动发现插件产出的 CIType 对齐, 否则发现结果无法落库。
func TestBuildBuiltinModelTypes_AlignsWithDiscoveryPlugins(t *testing.T) {
	types := buildBuiltinModelTypes()
	byID := map[string]model.CIModelType{}
	for _, mt := range types {
		byID[mt.ID] = mt
	}
	// discovery/plugins 产出的 CIType: server/switch/database/middleware/app
	for _, want := range []string{
		model.ModelTypeServer, model.ModelTypeNetwork, model.ModelTypeDatabase,
		model.ModelTypeMiddleware, model.ModelTypeApp,
	} {
		if _, ok := byID[want]; !ok {
			t.Errorf("内置模型缺少 %q(发现插件产出依赖)", want)
		}
	}
	if _, ok := byID[model.ModelTypeBusinessService]; !ok {
		t.Error("内置模型缺少 business_service(服务建模层)")
	}
	if _, ok := byID[model.ModelTypeService]; !ok {
		t.Error("内置模型缺少 service(应用下一层进程级服务)")
	}
	// service 应声明 runs_on 关系(进程落地到资源)
	rels := map[string]bool{}
	for _, r := range byID[model.ModelTypeService].Relations {
		rels[r.RelationType] = true
	}
	if !rels[model.RelationRunsOn] {
		t.Error("service 模型应声明 runs_on 关系")
	}
}

func TestBuildBuiltinModelTypes_AllBuiltinAndCategorized(t *testing.T) {
	types := buildBuiltinModelTypes()
	// 5 个顶层大类(server/switch/database/middleware/app) + business_service + service
	// + 9 个 leaf 子类(mysql/oracle/postgresql/redis/kafka/nginx/elasticsearch/zookeeper/rabbitmq)
	// + storage/vm/firewall/lb/router/dc/room/rack(8)
	// + K8S 容器系列(kubernetes 抽象父类 + 7 leaf) = 32
	// + 云资源系列(11 厂商抽象父类 + 公有云 6×10 + 私有云 5×4 = 80 leaf) = 91
	if len(types) != 123 {
		t.Fatalf("应内置 123 类模型(32 + 云资源系列 91), got %d", len(types))
	}
	categorySeen := map[string]bool{}
	for _, mt := range types {
		if !mt.Builtin || mt.Author != model.BuiltinAuthor {
			t.Errorf("模型 %s 应为内置(builtin=true, author=%s)", mt.ID, mt.Author)
		}
		if mt.ID == "" || mt.Name == "" || mt.Category == "" {
			t.Errorf("模型 %s 字段不完整: %+v", mt.ID, mt)
		}
		categorySeen[mt.Category] = true
	}
	// 五个内置分类应都被覆盖
	for _, c := range model.ModelCategories() {
		if !categorySeen[c] {
			t.Errorf("分类 %s 无内置模型", c)
		}
	}
}

func TestBuildBuiltinModelTypes_BusinessServiceSchema(t *testing.T) {
	for _, mt := range buildBuiltinModelTypes() {
		if mt.ID != model.ModelTypeBusinessService {
			continue
		}
		def, ok := mt.Schema[model.AttrKeyCriticality]
		if !ok {
			t.Fatal("business_service 应含 business_criticality 字段")
		}
		if def.Type != "string" {
			t.Errorf("criticality 字段类型应为 string, got %s", def.Type)
		}
		if def.Default != "medium" {
			t.Errorf("criticality 默认值应为 medium, got %v", def.Default)
		}
		if mt.Category != model.ModelCategoryBusiness {
			t.Errorf("business_service 分类应为业务资源, got %s", mt.Category)
		}
	}
}

func TestBusinessImpactLevel(t *testing.T) {
	cases := []struct {
		name  string
		crits []string
		want  string
	}{
		{"空", nil, "low"},
		{"单 critical", []string{"critical"}, "critical"},
		{"最高级生效", []string{"low", "high", "critical"}, "critical"},
		{"high 高于 medium", []string{"medium", "high"}, "high"},
		{"非法值忽略", []string{"unknown", "low"}, "low"},
		{"全非法兜底", []string{"zzz", "yyy"}, "low"},
	}
	for _, tc := range cases {
		if got := businessImpactLevel(tc.crits); got != tc.want {
			t.Errorf("%s: businessImpactLevel(%v) = %s, want %s", tc.name, tc.crits, got, tc.want)
		}
	}
}

func TestSortBusinessServices(t *testing.T) {
	svcs := []model.BusinessImpact{
		{CIID: "a", Criticality: "low"},
		{CIID: "b", Criticality: "critical"},
		{CIID: "c", Criticality: "medium"},
		{CIID: "d", Criticality: "high"},
	}
	sortBusinessServices(svcs)
	want := []string{"b", "d", "c", "a"}
	for i, w := range want {
		if svcs[i].CIID != w {
			t.Fatalf("排序错误: got %v, want %v", svcs, want)
		}
	}
}
