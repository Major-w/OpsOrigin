package process

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// 流程主表单决策表引用：读取展开 / 保存归一的接线测试。
// 用嵌入接口的最小 fake store（只覆写测试路径会调用的方法），不依赖 PG。

type formHookStore struct {
	Store
	items map[string]*Process
}

func (f *formHookStore) Create(_ context.Context, p *Process) error {
	cp := *p
	f.items[p.ID] = &cp
	return nil
}
func (f *formHookStore) Get(_ context.Context, id, _ string) (*Process, error) {
	return f.items[id], nil
}
func (f *formHookStore) List(_ context.Context, _ string) ([]*Process, error) {
	out := []*Process{}
	for _, p := range f.items {
		out = append(out, p)
	}
	return out, nil
}
func (f *formHookStore) Update(_ context.Context, p *Process) error {
	cp := *p
	f.items[p.ID] = &cp
	return nil
}
func (f *formHookStore) SaveVersion(_ context.Context, _ *ProcessVersion) error {
	return nil
}

type stubFormHook struct {
	normalized    string
	expanded      string
	normalizeErr  error
	normalizeCall int
	expandCall    int
}

func (h *stubFormHook) NormalizeProcessForm(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	h.normalizeCall++
	if h.normalizeErr != nil {
		return nil, h.normalizeErr
	}
	if h.normalized != "" {
		return json.RawMessage(h.normalized), nil
	}
	return json.RawMessage(`{"decision_tables":[]}`), nil
}

func (h *stubFormHook) ExpandProcessForm(_ context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
	h.expandCall++
	if h.expanded != "" {
		return json.RawMessage(h.expanded), nil
	}
	return json.RawMessage(`{"decision_tables":[{"rows":[{"outputs":{"level":"P1"}}]}]}`), nil
}

func newFormHookMux(hook FormSchemaResolver) (*formHookStore, *http.ServeMux) {
	st := &formHookStore{items: map[string]*Process{}}
	svc := &Service{store: st}
	if hook != nil {
		svc.SetFormSchemaResolver(hook)
	}
	mux := http.NewServeMux()
	svc.RegisterRoutes(mux)
	return st, mux
}

func doProcessJSON(t *testing.T, mux *http.ServeMux, method, path string, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
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

const rawProcessForm = `{"version":1,"fields":[],"decision_tables":[{"id":"dt","rows":[{"outputs":{"level":"P1"}}]}]}`

func TestProcessFormHookSaveNormalizeReadExpand(t *testing.T) {
	hook := &stubFormHook{}
	st, mux := newFormHookMux(hook)
	body := `{"id":"p1","name":"事件处置","form":` + rawProcessForm + `}`

	// 1) 创建：落库的 form 必须是归一态（原始 rows 快照不得固化）
	code, resp := doProcessJSON(t, mux, http.MethodPost, "/api/v1/itsm/processes", body)
	if code != http.StatusCreated {
		t.Fatalf("创建流程失败 %d: %v", code, resp)
	}
	if hook.normalizeCall != 1 {
		t.Fatalf("保存路径应调用归一一次，实际 %d", hook.normalizeCall)
	}
	stored := st.items["p1"]
	if stored == nil || string(stored.Form) != `{"decision_tables":[]}` {
		t.Fatalf("落库 form 未归一: %v", stored)
	}

	// 2) GET 详情：展开成可读内容（前端 applyDataRules 才能实时推导）
	code, got := doProcessJSON(t, mux, http.MethodGet, "/api/v1/itsm/processes/p1", "")
	if code != http.StatusOK {
		t.Fatalf("读取流程失败 %d: %v", code, got)
	}
	formObj, _ := got["form"].(map[string]any)
	tables, _ := formObj["decision_tables"].([]any)
	if len(tables) != 1 {
		t.Fatalf("GET 详情未展开决策表: %v", got["form"])
	}

	// 3) GET 列表：工单页读的是列表快照，同样必须展开
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/itsm/processes", nil))
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil || len(list) != 1 {
		t.Fatalf("列表解析失败 (%d): %s", rec.Code, rec.Body.String())
	}
	listForm, _ := list[0]["form"].(map[string]any)
	if _, ok := listForm["decision_tables"].([]any); !ok {
		t.Fatalf("GET 列表未展开决策表: %v", list[0]["form"])
	}

	// 4) 展开不得污染 store 返回的对象（内存态曾出现过就地改写回写存储）
	if string(st.items["p1"].Form) != `{"decision_tables":[]}` {
		t.Fatalf("GET 展开污染了落库 form: %s", st.items["p1"].Form)
	}

	// 5) 更新同样走归一
	code, resp = doProcessJSON(t, mux, http.MethodPut, "/api/v1/itsm/processes/p1", body)
	if code != http.StatusOK {
		t.Fatalf("更新流程失败 %d: %v", code, resp)
	}
	if hook.normalizeCall != 2 || string(st.items["p1"].Form) != `{"decision_tables":[]}` {
		t.Fatalf("更新路径未归一: calls=%d form=%s", hook.normalizeCall, st.items["p1"].Form)
	}
}

func TestProcessFormHookNormalizeErrorBlocksSave(t *testing.T) {
	hook := &stubFormHook{normalizeErr: errFormColumnsUndeclared{}}
	st, mux := newFormHookMux(hook)
	code, resp := doProcessJSON(t, mux, http.MethodPost, "/api/v1/itsm/processes",
		`{"id":"p2","name":"错配流程","form":`+rawProcessForm+`}`)
	if code != http.StatusBadRequest {
		t.Fatalf("列错配应 400，实际 %d: %v", code, resp)
	}
	if st.items["p2"] != nil {
		t.Fatal("校验失败不应落库")
	}
}

type errFormColumnsUndeclared struct{}

func (errFormColumnsUndeclared) Error() string {
	return "决策表列未在流程主表单字段中声明"
}

func TestProcessFormHookNilKeepsLegacyBehavior(t *testing.T) {
	st, mux := newFormHookMux(nil)
	code, _ := doProcessJSON(t, mux, http.MethodPost, "/api/v1/itsm/processes",
		`{"id":"p3","name":"存量流程","form":`+rawProcessForm+`}`)
	if code != http.StatusCreated {
		t.Fatalf("未注入 hook 时创建应照常成功，实际 %d", code)
	}
	if string(st.items["p3"].Form) != rawProcessForm {
		t.Fatalf("未注入 hook 时不应改动 form: %s", st.items["p3"].Form)
	}
	code, got := doProcessJSON(t, mux, http.MethodGet, "/api/v1/itsm/processes/p3", "")
	if code != http.StatusOK || got["form"] == nil {
		t.Fatalf("未注入 hook 时 GET 应原样返回: %d %v", code, got)
	}
}
