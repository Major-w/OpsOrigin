package principal

import (
	"context"
	"testing"
)

func TestParseType(t *testing.T) {
	cases := []struct {
		in    string
		want  Type
		valid bool
	}{
		{"HUMAN", TypeHuman, true},
		{"human", TypeHuman, true},
		{" Human ", TypeHuman, true},
		{"AI_AGENT", TypeAIAgent, true},
		{"ai_agent", TypeAIAgent, true},
		{"SERVICE", TypeService, true},
		{"robot", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := ParseType(c.in)
		if ok != c.valid || got != c.want {
			t.Fatalf("ParseType(%q) = (%q,%v), want (%q,%v)", c.in, got, ok, c.want, c.valid)
		}
	}
}

func TestValid(t *testing.T) {
	if (Principal{Type: TypeHuman}).Valid() {
		t.Fatal("空 ID 应判定无效")
	}
	if (Principal{Type: "GHOST", ID: "u1"}).Valid() {
		t.Fatal("未知类型应判定无效")
	}
	if !(Principal{Type: TypeHuman, ID: "u1"}).Valid() {
		t.Fatal("合法主体应判定有效")
	}
}

func TestHasPermissionMatching(t *testing.T) {
	p := Principal{
		Type:        TypeHuman,
		ID:          "u1",
		Permissions: []string{"cmdb:read", "itsm:*"},
	}
	if !p.Has("cmdb:read") {
		t.Fatal("精确匹配失败")
	}
	if p.Has("cmdb:write") {
		t.Fatal("未授予的权限不应命中")
	}
	if !p.Has("itsm:approve") {
		t.Fatal("模块通配 itsm:* 应命中 itsm:approve")
	}
	if p.Has("") {
		t.Fatal("空权限名不应命中")
	}
	// 通配不得跨模块越界
	if p.Has("itsmx:read") {
		t.Fatal("itsm:* 不应命中 itsmx:read")
	}

	super := Principal{Type: TypeHuman, ID: "root", Permissions: []string{"*"}}
	if !super.Has("anything:goes") {
		t.Fatal("超级通配 * 应命中一切")
	}

	// 最小权限:空集不放行
	none := Principal{Type: TypeHuman, ID: "u2"}
	if none.Has("cmdb:read") {
		t.Fatal("空权限集必须一律拒绝")
	}
}

func TestHasAnyAll(t *testing.T) {
	p := Principal{Type: TypeHuman, ID: "u1", Permissions: []string{"cmdb:read", "itsm:read"}}
	if !p.HasAny("cmdb:write", "itsm:read") {
		t.Fatal("HasAny 应在任一命中时返回 true")
	}
	if p.HasAny("cmdb:write", "agent:write") {
		t.Fatal("HasAny 全不命中应返回 false")
	}
	if !p.HasAll("cmdb:read", "itsm:read") {
		t.Fatal("HasAll 全命中应返回 true")
	}
	if p.HasAll("cmdb:read", "cmdb:write") {
		t.Fatal("HasAll 缺一应返回 false")
	}
	if p.HasAll() {
		t.Fatal("HasAll 空参数应返回 false,避免空断言被误判为放行")
	}
}

func TestNameFallback(t *testing.T) {
	if got := (Principal{ID: "u1"}).Name(); got != "u1" {
		t.Fatalf("DisplayName 缺失应回落 ID,得到 %q", got)
	}
	if got := (Principal{ID: "u1", DisplayName: "张三"}).Name(); got != "张三" {
		t.Fatalf("应优先返回 DisplayName,得到 %q", got)
	}
}

func TestContextRoundTrip(t *testing.T) {
	base := context.Background()

	if _, ok := FromContext(base); ok {
		t.Fatal("空 context 不应存在主体")
	}
	if UserID(base) != "" || TenantID(base) != "" || Role(base) != "" {
		t.Fatal("空 context 的 helper 应返回空串")
	}
	if Has(base, "cmdb:read") {
		t.Fatal("空 context 必须拒绝一切权限")
	}
	//nolint:staticcheck // 显式验证 nil context 的防御分支
	if _, ok := FromContext(nil); ok {
		t.Fatal("nil context 不应 panic 且不应存在主体")
	}

	want := Principal{
		Type:        TypeHuman,
		ID:          "u-1001",
		DisplayName: "张三",
		TenantID:    "t-acme",
		Role:        "operator",
		Permissions: []string{"cmdb:read"},
	}
	ctx := NewContext(base, want)

	got, ok := FromContext(ctx)
	if !ok || got.ID != want.ID {
		t.Fatalf("回读主体失败: %+v ok=%v", got, ok)
	}
	if UserID(ctx) != "u-1001" || TenantID(ctx) != "t-acme" || Role(ctx) != "operator" {
		t.Fatal("helper 取值与写入不一致")
	}
	if DisplayName(ctx) != "张三" {
		t.Fatal("DisplayName helper 取值错误")
	}
	if !Has(ctx, "cmdb:read") || Has(ctx, "cmdb:write") {
		t.Fatal("Has helper 判定错误")
	}
	if !HasAny(ctx, "x:y", "cmdb:read") {
		t.Fatal("HasAny helper 判定错误")
	}
}

func TestPermissionsReturnsCopy(t *testing.T) {
	ctx := NewContext(context.Background(), Principal{
		Type: TypeHuman, ID: "u1", Permissions: []string{"cmdb:read"},
	})
	perms := Permissions(ctx)
	perms[0] = "system:admin" // 调用方篡改副本

	if Has(ctx, "system:admin") {
		t.Fatal("Permissions 必须返回副本,防止调用方篡改上下文权限")
	}
	if !Has(ctx, "cmdb:read") {
		t.Fatal("原始权限被污染")
	}
	if Permissions(NewContext(context.Background(), Principal{ID: "u2"})) != nil {
		t.Fatal("空权限集应返回 nil")
	}
}

func TestWithHelpers(t *testing.T) {
	p := Principal{Type: TypeHuman, ID: "u1"}
	enriched := p.WithPermissions([]string{"cmdb:read"}).WithDisplayName("张三")

	if len(p.Permissions) != 0 || p.DisplayName != "" {
		t.Fatal("With* 必须返回副本,不得修改原值")
	}
	if !enriched.Has("cmdb:read") || enriched.DisplayName != "张三" {
		t.Fatal("With* 回填失败")
	}
}

func TestTypePredicatesAndString(t *testing.T) {
	human := Principal{Type: TypeHuman, ID: "u1", DisplayName: "张三", TenantID: "t1"}
	if !human.IsHuman() || human.IsAI() || human.IsService() {
		t.Fatal("类型判定错误")
	}
	if got := human.String(); got != "HUMAN:u1(张三)@t1" {
		t.Fatalf("String() = %q", got)
	}
	ai := Principal{Type: TypeAIAgent, ID: "agent-1"}
	if !ai.IsAI() || ai.String() != "AI_AGENT:agent-1" {
		t.Fatalf("AI 主体 String() = %q", ai.String())
	}
	svc := Principal{Type: TypeService, ID: "collector"}
	if !svc.IsService() {
		t.Fatal("服务主体判定错误")
	}
}
