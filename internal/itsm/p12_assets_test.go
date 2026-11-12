package itsm

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/itsm/process"
)

// P1/P2 真实实现与路由级验证：MemoryStore + ServeMux，不依赖 PG/Docker。

func TestEvaluateDecisionFromPublishedAsset(t *testing.T) {
	s, mux := newAssetTestMux(t)
	id := publishSampleAsset(t, mux, "流程分级矩阵")

	out, fired, err := s.EvaluateDecision(context.Background(), "default", id, 0,
		map[string]any{"env": "prod", "impact": "global"})
	if err != nil {
		t.Fatalf("已发布资产求值失败: %v", err)
	}
	if out["level"] != "P1" {
		t.Fatalf("决策表输出列未回传: %v", out)
	}
	if len(fired) != 1 {
		t.Fatalf("命中行标记缺失: %v", fired)
	}
	// 只回传输出列，输入字段不应混进 decision.<alias> 命名空间。
	if _, ok := out["env"]; ok {
		t.Fatalf("输入列不应出现在输出中: %v", out)
	}
	// 锁定到不存在的版本 / 未发布资产 → 返回错误，由 process 侧容错跳过该绑定。
	if _, _, err := s.EvaluateDecision(context.Background(), "default", id, 99, nil); err == nil {
		t.Fatal("不存在的版本应返回错误")
	}
	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables",
		map[string]any{"name": "未发布草稿", "table": sampleTable()})
	if code != http.StatusOK {
		t.Fatalf("新建草稿失败 %d: %v", code, created)
	}
	draftID, _ := created["asset"].(map[string]any)["id"].(string)
	if _, _, err := s.EvaluateDecision(context.Background(), "default", draftID, 0, nil); err == nil {
		t.Fatal("未发布资产应返回错误")
	}
}

func TestDecisionBindingUsageBlocksDelete(t *testing.T) {
	s, mux := newAssetTestMux(t)
	id := publishSampleAsset(t, mux, "被流程绑定的资产")
	s.processStore = &listingProcessEngine{procs: []*process.Process{{
		ID: "p1", Name: "分级流程",
		DecisionBindings: []process.DecisionBinding{{AssetID: id, Alias: "route"}},
	}}}
	code, list := callArray(t, mux, "/api/v1/itsm/decision-tables")
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(list[0]["used_by"]), "决策绑定") {
		t.Fatalf("used_by 未覆盖流程级决策绑定: %d %v", code, list)
	}
	code, del := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/decision-tables/"+id, nil)
	if code != http.StatusConflict {
		t.Fatalf("被流程绑定的资产删除应 409，实际 %d: %v", code, del)
	}
}

func createActionTemplate(t *testing.T, mux *http.ServeMux, name string, inputs map[string]any) string {
	t.Helper()
	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates", map[string]any{
		"name": name, "action_type": "create_ticket", "inputs": inputs, "enabled": true,
	})
	if code != http.StatusOK {
		t.Fatalf("新建模板失败 %d: %v", code, created)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("返回体缺少 id: %v", created)
	}
	return id
}

func TestActionTemplatePublishResolveVersionsAndUsage(t *testing.T) {
	s, mux := newAssetTestMux(t)
	id := createActionTemplate(t, mux, "自动建单", map[string]any{"title": "来自模板 v1"})

	// 1) 草稿不可被运行期引用（fail-closed）
	if _, _, err := s.ResolveActionTemplate(context.Background(), "default", id, 0); err == nil {
		t.Fatal("未发布模板解析应失败")
	}
	// 2) 发布 v1
	code, v1 := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates/"+id+"/publish",
		map[string]any{"note": "首个版本"})
	if code != http.StatusOK || v1["version"] != float64(1) {
		t.Fatalf("发布失败 %d: %v", code, v1)
	}
	at, in, err := s.ResolveActionTemplate(context.Background(), "default", id, 0)
	if err != nil || at != "create_ticket" || in["title"] != "来自模板 v1" {
		t.Fatalf("按最新版解析异常: %q %v %v", at, in, err)
	}
	// 3) 列表带回 status / version
	code, list := callArray(t, mux, "/api/v1/itsm/action-templates")
	if code != http.StatusOK || len(list) != 1 || list[0]["status"] != "published" || list[0]["version"] != float64(1) {
		t.Fatalf("列表未反映发布态: %d %v", code, list)
	}
	// 4) 版本历史端点
	code, versions := callArray(t, mux, "/api/v1/itsm/action-templates/"+id+"/versions")
	if code != http.StatusOK || len(versions) != 1 {
		t.Fatalf("版本历史异常 %d: %v", code, versions)
	}
	// 5) 改草稿并发布 v2，跟随版立即切到 v2，锁定 v1 仍可复现
	code, _ = callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates", map[string]any{
		"id": id, "name": "自动建单", "action_type": "create_ticket",
		"inputs": map[string]any{"title": "来自模板 v2"}, "enabled": true,
	})
	if code != http.StatusOK {
		t.Fatalf("改草稿失败 %d", code)
	}
	code, v2 := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates/"+id+"/publish", map[string]any{})
	if code != http.StatusOK || v2["version"] != float64(2) {
		t.Fatalf("发布 v2 失败 %d: %v", code, v2)
	}
	_, in, err = s.ResolveActionTemplate(context.Background(), "default", id, 0)
	if err != nil || in["title"] != "来自模板 v2" {
		t.Fatalf("跟随版未切到 v2: %v %v", in, err)
	}
	_, in, err = s.ResolveActionTemplate(context.Background(), "default", id, 1)
	if err != nil || in["title"] != "来自模板 v1" {
		t.Fatalf("锁定 v1 不可变: %v %v", in, err)
	}
	// 6) 回滚端点：以 v1 内容发布 v3
	code, v3 := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates/"+id+"/rollback",
		map[string]any{"version": 1})
	if code != http.StatusOK || v3["version"] != float64(3) {
		t.Fatalf("回滚失败 %d: %v", code, v3)
	}
	_, in, err = s.ResolveActionTemplate(context.Background(), "default", id, 0)
	if err != nil || in["title"] != "来自模板 v1" {
		t.Fatalf("回滚后跟随版应为 v1 内容: %v %v", in, err)
	}
	// 7) 流程步骤引用 → UsedBy 可见 + 删除 409
	s.processStore = &listingProcessEngine{procs: []*process.Process{{
		ID: "p1", Name: "事件处置", Steps: []process.Step{{
			ID: "svc", Name: "自动建单", Type: process.StepService,
			TemplateRef: &process.ActionTemplateRef{TemplateID: id},
		}},
	}}}
	code, list = callArray(t, mux, "/api/v1/itsm/action-templates")
	if code != http.StatusOK || !strings.Contains(fmt.Sprint(list[0]["used_by"]), "事件处置") {
		t.Fatalf("used_by 未覆盖流程步骤引用: %d %v", code, list)
	}
	code, del := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/action-templates/"+id, nil)
	if code != http.StatusConflict {
		t.Fatalf("被流程引用的模板删除应 409，实际 %d: %v", code, del)
	}
	// 8) 移除引用后可删除
	s.processStore = &listingProcessEngine{procs: []*process.Process{{ID: "p1", Name: "事件处置"}}}
	code, _ = callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/action-templates/"+id, nil)
	if code != http.StatusOK {
		t.Fatalf("移除引用后应可删除，实际 %d", code)
	}
}
