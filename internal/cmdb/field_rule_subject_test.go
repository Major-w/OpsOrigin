package cmdb

import (
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/pkg/principal"
)

func ciWithSecret() *model.CIModel {
	return &model.CIModel{
		ID:     "ci-1",
		TypeID: "mt-1",
		Attributes: map[string]any{
			"ip":     "10.0.0.1",
			"secret": "p@ss",
		},
		AttributeMeta: map[string]model.AttrMeta{},
	}
}

func secretRules() []*model.FieldRule {
	return []*model.FieldRule{
		{ModelTypeID: "mt-1", Field: "secret", Permission: "cmdb:read", Action: model.FieldActionHidden},
		{ModelTypeID: "mt-1", Field: "ip", Permission: "cmdb:read", Action: model.FieldActionReadonly},
	}
}

// 回归:客户端自报 X-Permissions 不得改变自己受哪些字段规则约束。
func TestSubjectIgnoresClientPermissionHeader(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/cmdb/ci", nil)
	r.Header.Set("X-Permissions", "system:admin") // 攻击者自报,期望绕开 cmdb:read 约束
	r = r.WithContext(principal.NewContext(r.Context(), principal.Principal{
		Type:        principal.TypeHuman,
		ID:          "u-viewer",
		Permissions: []string{"cmdb:read"},
	}))

	ci := ciWithSecret()
	applyFieldRules([]*model.CIModel{ci}, secretRules(), subjectFromRequest(r))

	if _, ok := ci.Attributes["secret"]; ok {
		t.Fatal("受约束字段被自报权限头绕过 —— 权限必须只取可信上下文")
	}
	if ci.FieldAccess["ip"] != "readonly" {
		t.Fatalf("只读约束未生效: %v", ci.FieldAccess)
	}
}

// 主体缺失(绕过鉴权链)时按最严格处理,不得 fail-open。
func TestSubjectFailsClosedWithoutPrincipal(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/cmdb/ci", nil)

	subj := subjectFromRequest(r)
	if !subj.restrictAll {
		t.Fatal("无主体上下文必须置 restrictAll")
	}
	if !subj.matches("any:permission") {
		t.Fatal("restrictAll 应命中一切规则")
	}

	ci := ciWithSecret()
	applyFieldRules([]*model.CIModel{ci}, secretRules(), subj)
	if _, ok := ci.Attributes["secret"]; ok {
		t.Fatal("主体不可识别时必须遮蔽受约束字段(fail-closed)")
	}
}

// 不受该规则约束的用户应看到完整字段。
func TestSubjectUnaffectedRoleSeesAllFields(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/cmdb/ci", nil)
	r = r.WithContext(principal.NewContext(r.Context(), principal.Principal{
		Type:        principal.TypeHuman,
		ID:          "u-admin",
		Permissions: []string{"cmdb:admin", "cmdb:write"},
	}))

	ci := ciWithSecret()
	applyFieldRules([]*model.CIModel{ci}, secretRules(), subjectFromRequest(r))

	if _, ok := ci.Attributes["secret"]; !ok {
		t.Fatal("未被规则约束的主体不应被遮蔽字段")
	}
	if len(ci.FieldAccess) != 0 {
		t.Fatalf("不应产生 field_access: %v", ci.FieldAccess)
	}
}

// 跨模型通配 *|field 仍生效。
func TestFieldRuleWildcardModel(t *testing.T) {
	r := httptest.NewRequest("GET", "/api/v1/cmdb/ci", nil)
	r = r.WithContext(principal.NewContext(r.Context(), principal.Principal{
		Type: principal.TypeHuman, ID: "u1", Permissions: []string{"cmdb:read"},
	}))

	ci := ciWithSecret()
	rules := []*model.FieldRule{
		{ModelTypeID: "*", Field: "secret", Permission: "cmdb:read", Action: model.FieldActionHidden},
	}
	applyFieldRules([]*model.CIModel{ci}, rules, subjectFromRequest(r))

	if _, ok := ci.Attributes["secret"]; ok {
		t.Fatal("跨模型通配规则未生效")
	}
}

// hidden 优先于 readonly。
func TestFieldRuleStrictestWins(t *testing.T) {
	subj := fieldRuleSubject{perms: []string{"cmdb:read"}}
	ci := ciWithSecret()
	rules := []*model.FieldRule{
		{ModelTypeID: "mt-1", Field: "secret", Permission: "cmdb:read", Action: model.FieldActionReadonly},
		{ModelTypeID: "mt-1", Field: "secret", Permission: "cmdb:read", Action: model.FieldActionHidden},
	}
	applyFieldRules([]*model.CIModel{ci}, rules, subj)

	if ci.FieldAccess["secret"] != string(model.FieldActionHidden) {
		t.Fatalf("同字段多规则应取最严格,得到 %v", ci.FieldAccess)
	}
}
