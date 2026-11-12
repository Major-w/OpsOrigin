package itsm

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/internal/itsm/form"
)

// 路由级集成测试：MemoryStore + 原生 ServeMux，不依赖 PG/Docker。
// 同时验证 Go 1.22 ServeMux 注册无同段数歧义（有歧义时 HandleFunc 会 panic）。
func newAssetTestMux(t *testing.T) (*Service, *http.ServeMux) {
	t.Helper()
	s := NewService(nil, nil)
	mux := http.NewServeMux()
	s.RegisterRoutes(mux)
	return s, mux
}

func callJSON(t *testing.T, mux *http.ServeMux, method, path string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		raw = b
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var out map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("%s %s 返回非对象 JSON (%d): %s", method, path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

func callArray(t *testing.T, mux *http.ServeMux, path string) (int, []map[string]any) {
	t.Helper()
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
	var out []map[string]any
	if rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatalf("GET %s 返回非数组 (%d): %s", path, rec.Code, rec.Body.String())
		}
	}
	return rec.Code, out
}

func sampleTable() map[string]any {
	return map[string]any{
		"name":           "事件分级矩阵",
		"condition_keys": []string{"env", "impact"},
		"output_keys":    []string{"level"},
		"rows": []map[string]any{{
			"cells":   map[string]string{"env": "prod", "impact": "global"},
			"outputs": map[string]string{"level": "P1"},
		}},
	}
}

func TestDecisionTableAssetLifecycle(t *testing.T) {
	s, mux := newAssetTestMux(t)

	// 1) 新建草稿
	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables",
		map[string]any{"name": "事件分级", "description": "生产全局→P1", "table": sampleTable()})
	if code != http.StatusOK {
		t.Fatalf("新建资产失败 %d: %v", code, created)
	}
	asset, ok := created["asset"].(map[string]any)
	if !ok || asset["id"] == "" || asset["id"] == nil {
		t.Fatalf("返回体缺少 asset.id: %v", created)
	}
	id, _ := asset["id"].(string)
	if asset["status"] != "draft" {
		t.Fatalf("新建应为草稿，实际 %v", asset["status"])
	}

	// 2) 发布 → 不可变版本 v1
	code, pub := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables/"+id+"/publish",
		map[string]any{"note": "首个版本"})
	if code != http.StatusOK || pub["version"] != float64(1) {
		t.Fatalf("发布失败 %d: %v", code, pub)
	}
	code, versions := callArray(t, mux, "/api/v1/itsm/decision-tables/"+id+"/versions")
	if code != http.StatusOK || len(versions) != 1 {
		t.Fatalf("版本列表异常 %d: %v", code, versions)
	}

	// 3) 独立试算：prod+global 应命中第 1 行并输出 P1
	code, preview := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables/"+id+"/preview",
		map[string]any{"version": 1, "values": map[string]any{"env": "prod", "impact": "global"}})
	if code != http.StatusOK {
		t.Fatalf("试算失败 %d: %v", code, preview)
	}
	values, _ := preview["values"].(map[string]any)
	if values["level"] != "P1" {
		t.Fatalf("试算输出应为 P1，实际 %v", values)
	}
	fired, _ := preview["fired"].([]any)
	if len(fired) == 0 {
		t.Fatalf("试算应记录命中行，实际 %v", preview["fired"])
	}

	// 4) 表单引用资产：保存后 schema 落库仍应是「纯引用」（不固化快照）
	schemaBody := map[string]any{
		"scope": "test:asset-ref",
		"schema": map[string]any{
			"scope":   "test:asset-ref",
			"version": 1,
			"fields": []map[string]any{
				{"key": "env", "label": "环境", "type": "text"},
				{"key": "impact", "label": "影响范围", "type": "text"},
				{"key": "level", "label": "级别", "type": "text"},
			},
			"decision_tables": []map[string]any{{
				"id": "dt-ref", "asset_ref": map[string]any{"asset_id": id, "version": 0},
			}},
		},
	}
	code, saved := callJSON(t, mux, http.MethodPut, "/api/v1/itsm/forms", schemaBody)
	if code != http.StatusOK {
		t.Fatalf("保存引用表单失败 %d: %v", code, saved)
	}
	// auth.TenantFromContext 在无请求身份时回落 "default"，与 handler 取值保持一致。
	stored, err := s.store.GetFormSchema(context.Background(), "default", "test:asset-ref")
	if err != nil || stored == nil {
		t.Fatalf("读取落库 schema 失败: %v", err)
	}
	if len(stored.DecisionTables) != 1 || stored.DecisionTables[0].AssetRef == nil {
		t.Fatalf("保存态引用标记丢失: %+v", stored.DecisionTables)
	}
	if len(stored.DecisionTables[0].Rows) != 0 || len(stored.DecisionTables[0].ConditionKeys) != 0 {
		t.Fatalf("保存态不应含内容快照（否则资产升级失效）: %+v", stored.DecisionTables[0])
	}

	// 5) GET schema 应展开为已发布版本内容，且仍保留引用标记
	code, got := callJSON(t, mux, http.MethodGet, "/api/v1/itsm/forms?scope=test:asset-ref", nil)
	if code != http.StatusOK {
		t.Fatalf("读取展开 schema 失败 %d: %v", code, got)
	}
	gs, _ := got["schema"].(map[string]any)
	dts, _ := gs["decision_tables"].([]any)
	if len(dts) != 1 {
		t.Fatalf("展开后决策表数量异常: %v", gs["decision_tables"])
	}
	dt0, _ := dts[0].(map[string]any)
	if dt0["asset_ref"] == nil {
		t.Fatal("展开后必须保留引用标记（否则保存会把快照烤进表单）")
	}
	rows, _ := dt0["rows"].([]any)
	if len(rows) != 1 {
		t.Fatalf("展开后应有 1 行规则，实际 %v", dt0["rows"])
	}
	// 展开只发生在响应对象上，不得污染存储（MemoryStore 返回内部指针的历史缺陷）。
	storedAfter, err := s.store.GetFormSchema(context.Background(), "default", "test:asset-ref")
	if err != nil || storedAfter == nil {
		t.Fatalf("GET 后读取落库 schema 失败: %v", err)
	}
	if len(storedAfter.DecisionTables[0].Rows) != 0 || storedAfter.DecisionTables[0].AssetRef == nil {
		t.Fatalf("GET 展开污染了保存态（快照被写回）: %+v", storedAfter.DecisionTables[0])
	}

	// 6) 被引用资产禁止删除（fail-closed，避免静默改变派单结果）
	code, del := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/decision-tables/"+id, nil)
	if code != http.StatusConflict {
		t.Fatalf("被引用资产删除应 409，实际 %d: %v", code, del)
	}

	// 7) 移除引用后可删除
	schemaBody["schema"].(map[string]any)["decision_tables"] = []any{}
	if code, resp := callJSON(t, mux, http.MethodPut, "/api/v1/itsm/forms", schemaBody); code != http.StatusOK {
		t.Fatalf("移除引用失败 %d: %v", code, resp)
	}
	if code, resp := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/decision-tables/"+id, nil); code != http.StatusOK {
		t.Fatalf("删除资产失败 %d: %v", code, resp)
	}
}

func TestActionTemplateLifecycleAndWhitelist(t *testing.T) {
	_, mux := newAssetTestMux(t)

	// 非内置动作类型必须 400（避免设计出运行期必然失败的动作）
	code, bad := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates",
		map[string]any{"name": "非法动作", "action_type": "not_exists", "inputs": map[string]any{}, "enabled": true})
	if code != http.StatusBadRequest {
		t.Fatalf("非法 action_type 应 400，实际 %d: %v", code, bad)
	}

	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/action-templates",
		map[string]any{
			"name": "变更后自动建单", "action_type": "create_ticket", "enabled": true,
			"inputs": map[string]any{"title": "由 ${ticket.title} 派生"},
		})
	if code != http.StatusOK || created["id"] == nil {
		t.Fatalf("新建模板失败 %d: %v", code, created)
	}
	id, _ := created["id"].(string)

	code, list := callArray(t, mux, "/api/v1/itsm/action-templates")
	if code != http.StatusOK || len(list) != 1 || list[0]["action_type"] != "create_ticket" {
		t.Fatalf("模板列表异常 %d: %v", code, list)
	}
	if code, resp := callJSON(t, mux, http.MethodDelete, "/api/v1/itsm/action-templates/"+id, nil); code != http.StatusOK {
		t.Fatalf("删除模板失败 %d: %v", code, resp)
	}
	code, list = callArray(t, mux, "/api/v1/itsm/action-templates")
	if code != http.StatusOK || len(list) != 0 {
		t.Fatalf("删除后列表应为空，实际 %d: %v", code, list)
	}
}

// 表单保存侧校验：资产引用展开后的列必须在本表单字段中声明，否则拒绝保存。
func TestFormSchemaRejectsUndeclaredAssetColumns(t *testing.T) {
	_, mux := newAssetTestMux(t)
	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables",
		map[string]any{"name": "列校验", "table": sampleTable()})
	if code != http.StatusOK {
		t.Fatalf("新建资产失败: %v", created)
	}
	id, _ := created["asset"].(map[string]any)["id"].(string)
	if code, resp := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables/"+id+"/publish",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("发布失败: %v", resp)
	}
	code, resp := callJSON(t, mux, http.MethodPut, "/api/v1/itsm/forms", map[string]any{
		"scope": "test:undeclared",
		"schema": map[string]any{
			"scope":   "test:undeclared",
			"version": 1,
			"fields":  []map[string]any{{"key": "env", "label": "环境", "type": "text"}},
			"decision_tables": []map[string]any{{
				"id": "dt-ref", "asset_ref": map[string]any{"asset_id": id},
			}},
		},
	})
	if code != http.StatusBadRequest {
		t.Fatalf("资产列未声明应 400，实际 %d: %v", code, resp)
	}
}

// 表单试算通道同样支持资产引用（提交/预览共用一条展开链路）。
func TestFormRulesPreviewExpandsAssetRef(t *testing.T) {
	_, mux := newAssetTestMux(t)
	code, created := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables",
		map[string]any{"name": "预览引用", "table": sampleTable()})
	if code != http.StatusOK {
		t.Fatalf("新建资产失败: %v", created)
	}
	id, _ := created["asset"].(map[string]any)["id"].(string)
	if code, resp := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/decision-tables/"+id+"/publish",
		map[string]any{}); code != http.StatusOK {
		t.Fatalf("发布失败: %v", resp)
	}
	schema := map[string]any{
		"version": 1,
		"fields": []map[string]any{
			{"key": "env", "type": "text"}, {"key": "impact", "type": "text"}, {"key": "level", "type": "text"},
		},
		"decision_tables": []map[string]any{{
			"id": "dt-ref", "asset_ref": map[string]any{"asset_id": id, "version": 0},
		}},
	}
	code, preview := callJSON(t, mux, http.MethodPost, "/api/v1/itsm/form/rules-preview",
		map[string]any{"schema": schema, "values": map[string]any{"env": "prod", "impact": "global"}})
	if code != http.StatusOK {
		t.Fatalf("试算失败 %d: %v", code, preview)
	}
	values, _ := preview["values"].(map[string]any)
	if values["level"] != "P1" {
		t.Fatalf("引用表未在试算中展开，values=%v", values)
	}
}

// 编译期兜底：确保引用类型在 form 包保持导出（改动 DecisionTable 结构时不会静默漏字段）。
var _ = form.DecisionTableAssetRef{}
