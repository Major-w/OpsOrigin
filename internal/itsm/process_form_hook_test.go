package itsm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/itsm/form"
	"github.com/opsorigin/core/internal/itsm/process"
)

// 真实 hook 实现（itsm.Service）+ 删除保护覆盖流程主表单的回归测试。
// 与 process 包的接线测试互补：那边验证「何时调用」，这边验证「调用后结果正确」。

func publishSampleAsset(t *testing.T, mux *http.ServeMux, name string) string {
	t.Helper()
	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables",
		map[string]any{"name": name, "table": sampleTable()})
	if code != http.StatusOK {
		t.Fatalf("新建资产失败 %d: %v", code, created)
	}
	id, _ := created["asset"].(map[string]any)["id"].(string)
	if id == "" {
		t.Fatalf("返回体缺少 asset.id: %v", created)
	}
	if code, resp := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables/"+id+"/publish",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("发布失败 %d: %v", code, resp)
	}
	return id
}

func processMainForm(assetID string, fields string) json.RawMessage {
	return json.RawMessage(`{"version":1,"scope":"","fields":` + fields + `,` +
		`"decision_tables":[{"id":"dt-ref","asset_ref":{"asset_id":"` + assetID + `","version":0}}],` +
		`"designer_ext":{"keep":true}}`)
}

const fullFields = `[{"key":"env","label":"环境","type":"text"},` +
	`{"key":"impact","label":"影响范围","type":"text"},` +
	`{"key":"level","label":"级别","type":"text"}]`

func TestProcessFormHookExpandNormalizeAndEvaluate(t *testing.T) {
	s, mux := newAssetTestMux(t)
	id := publishSampleAsset(t, mux, "流程主表单引用")
	raw := processMainForm(id, fullFields)
	ctx := context.Background()

	// 1) 保存归一：只留引用标记，且不得丢失设计器扩展字段
	normalized, err := s.NormalizeProcessForm(ctx, "default", raw)
	if err != nil {
		t.Fatalf("归一侧 unexpectedly 报错: %v", err)
	}
	var nObj map[string]any
	if err := json.Unmarshal(normalized, &nObj); err != nil {
		t.Fatalf("归一结果非法 JSON: %v", err)
	}
	if _, ok := nObj["designer_ext"]; !ok {
		t.Fatalf("归一丢失了设计器扩展字段: %s", normalized)
	}
	nTables, _ := nObj["decision_tables"].([]any)
	if len(nTables) != 1 {
		t.Fatalf("归一后决策表数量异常: %s", normalized)
	}
	ndt, _ := nTables[0].(map[string]any)
	if ndt["asset_ref"] == nil || ndt["rows"] != nil || ndt["condition_keys"] != nil {
		t.Fatalf("归一后应为纯引用（无内容快照）: %v", ndt)
	}

	// 2) 读取展开：拿到已发布版本的行矩阵，前端 applyDataRules 才能实时推导
	expanded, err := s.ExpandProcessForm(ctx, "default", raw)
	if err != nil {
		t.Fatalf("展开失败: %v", err)
	}
	var eObj map[string]any
	if err := json.Unmarshal(expanded, &eObj); err != nil {
		t.Fatalf("展开结果非法 JSON: %v", err)
	}
	if _, ok := eObj["designer_ext"]; !ok {
		t.Fatalf("展开丢失了设计器扩展字段: %s", expanded)
	}
	eTables, _ := eObj["decision_tables"].([]any)
	edt, _ := eTables[0].(map[string]any)
	rows, _ := edt["rows"].([]any)
	if edt["asset_ref"] == nil || len(rows) != 1 {
		t.Fatalf("展开后应保留引用标记且带 1 行: %v", edt)
	}

	// 3) 前端等效求值：展开态能命中行并输出 P1（未展开时 rows 为空会静默失效）
	var fs form.FormSchema
	if err := json.Unmarshal(expanded, &fs); err != nil {
		t.Fatalf("解析展开 schema 失败: %v", err)
	}
	values, fired := form.ApplyDataRules(&fs, map[string]any{"env": "prod", "impact": "global"})
	if values["level"] != "P1" || len(fired) == 0 {
		t.Fatalf("展开态未命中决策表: values=%v fired=%v", values, fired)
	}

	// 4) 列错配：资产的列未在流程主表单声明 → 保存拒绝（与独立表单同标准）
	_, err = s.NormalizeProcessForm(ctx, "default",
		processMainForm(id, `[{"key":"env","label":"环境","type":"text"}]`))
	if err == nil || !strings.Contains(err.Error(), "未在表单字段中声明") {
		t.Fatalf("列错配应被拦截，实际 err=%v", err)
	}

	// 5) 无决策表的存量流程零改动
	legacy := json.RawMessage(`{"version":1,"fields":[],"designer_ext":{"keep":true}}`)
	if got, err := s.ExpandProcessForm(ctx, "default", legacy); err != nil || string(got) != string(legacy) {
		t.Fatalf("存量流程 form 不应被改写: %s err=%v", got, err)
	}
	if got, err := s.NormalizeProcessForm(ctx, "default", legacy); err != nil || string(got) != string(legacy) {
		t.Fatalf("存量流程 form 不应被改写: %s err=%v", got, err)
	}
}

// listingProcessEngine 给最小 ProcessEngine 接口补一个 List（鸭子类型可选能力）。
type listingProcessEngine struct {
	ProcessEngine
	procs []*process.Process
}

func (f *listingProcessEngine) List(_ context.Context, _ string) ([]*process.Process, error) {
	return f.procs, nil
}

func TestAssetDeletionBlockedByProcessMainForm(t *testing.T) {
	s, mux := newAssetTestMux(t)
	id := publishSampleAsset(t, mux, "被引用的流程资产")
	refForm := processMainForm(id, fullFields)
	s.processStore = &listingProcessEngine{procs: []*process.Process{
		{ID: "p1", Name: "事件处置", Form: refForm},
	}}

	// 1) used_by 应带上流程主表单
	code, list := callArray(t, mux, "/api/v1/itsm/decision-tables")
	if code != http.StatusOK || len(list) != 1 {
		t.Fatalf("资产列表异常 %d: %v", code, list)
	}
	usedBy := fmt.Sprint(list[0]["used_by"])
	if !strings.Contains(usedBy, "事件处置") {
		t.Fatalf("used_by 未覆盖流程主表单: %v", list[0]["used_by"])
	}

	// 2) 被流程主表单引用 → 删除 409（漏扫会让资产被删，引用静默退回空表）
	code, del := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/decision-tables/"+id, nil)
	if code != http.StatusConflict || !strings.Contains(fmt.Sprint(del), "事件处置") {
		t.Fatalf("流程主表单引用的资产删除应 409 且提示流程名，实际 %d: %v", code, del)
	}

	// 3) 移除引用后可删除
	s.processStore = &listingProcessEngine{procs: []*process.Process{
		{ID: "p1", Name: "事件处置", Form: json.RawMessage(`{"version":1,"fields":[],"decision_tables":[]}`)},
	}}
	if code, resp := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/decision-tables/"+id, nil); code != http.StatusOK {
		t.Fatalf("移除引用后应可删除，实际 %d: %v", code, resp)
	}
}
