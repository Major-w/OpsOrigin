package aiworkspace

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/report"
	"github.com/opsorigin/core/pkg/llm"
)

// fakeLLM 用固定响应替代真实模型，把测试焦点收在"白名单修复"逻辑上。
type fakeLLM struct {
	resp    *llm.ChatResponse
	err     error
	lastReq llm.ChatRequest
}

func (f *fakeLLM) Chat(_ context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.resp, nil
}

// toolResp 构造一条带 tool_calls 的模型响应。
// ChatResponse.Choices 是匿名结构体切片，走 JSON 反序列化最省事也最贴近真实链路。
func toolResp(args string) *llm.ChatResponse {
	payload := map[string]any{
		"choices": []any{map[string]any{
			"index": 0,
			"message": map[string]any{
				"role": "assistant",
				"tool_calls": []any{map[string]any{
					"id":       "call_1",
					"type":     "function",
					"function": map[string]any{"name": toolName, "arguments": args},
				}},
			},
			"finish_reason": "tool_calls",
		}},
	}
	b, _ := json.Marshal(payload)
	var r llm.ChatResponse
	_ = json.Unmarshal(b, &r)
	return &r
}

func contentResp(text string) *llm.ChatResponse {
	payload := map[string]any{
		"choices": []any{map[string]any{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
	}
	b, _ := json.Marshal(payload)
	var r llm.ChatResponse
	_ = json.Unmarshal(b, &r)
	return &r
}

func emptyResolver() report.Resolver {
	return report.ResolverFunc(func(_ context.Context, _ string, q *report.Query, ds *report.Dataset) (*report.Result, error) {
		return &report.Result{Columns: q.Columns(ds), Rows: [][]any{}}, nil
	})
}

// testRegistry 构造一个内存目录：一个带时间字段的数据集 + 一个无时间字段的数据集。
func testRegistry(t *testing.T) *report.Registry {
	t.Helper()
	reg := report.NewRegistry()
	if err := reg.Register(&report.Dataset{
		Name: "test.tickets", Label: "工单", Module: "test", TimeField: "created_at",
		Dimensions: []report.Dimension{
			{Name: "status", Label: "状态", Type: report.FieldString, Enum: []string{"open", "closed"}, Filterable: true, Groupable: true},
			{Name: "title", Label: "标题", Type: report.FieldString, Filterable: true, Groupable: false},
			{Name: "cost", Label: "耗时", Type: report.FieldNumber, Filterable: true, Groupable: false},
			{Name: "created_at", Label: "创建时间", Type: report.FieldTime, Filterable: true, Groupable: true},
		},
		Metrics: []report.Metric{
			{Name: "cnt", Label: "工单数", Agg: report.AggCount},
			{Name: "avg_cost", Label: "平均耗时", Agg: report.AggAvg},
		},
		Resolver: emptyResolver(),
	}); err != nil {
		t.Fatalf("register tickets: %v", err)
	}
	if err := reg.Register(&report.Dataset{
		Name: "test.assets", Label: "资产", Module: "test",
		Dimensions: []report.Dimension{
			{Name: "kind", Label: "类型", Type: report.FieldString, Filterable: true, Groupable: true},
		},
		Metrics:  []report.Metric{{Name: "cnt", Label: "数量", Agg: report.AggCount}},
		Resolver: emptyResolver(),
	}); err != nil {
		t.Fatalf("register assets: %v", err)
	}
	return reg
}

func TestGenerateHappyPath(t *testing.T) {
	reg := testRegistry(t)
	args := `{
      "name":"工单运营看板",
      "description":"工单核心视角",
      "explanation":"回答工单量趋势与状态分布",
      "widgets":[
        {"id":"w_trend","title":"工单趋势","type":"line",
         "query":{"dataset":"test.tickets","metrics":["cnt"],"granularity":"day","timeRange":{"last":"7d"}},
         "layout":{"x":0,"y":0,"w":12,"h":8}},
        {"id":"w_status","title":"状态分布","type":"pie",
         "query":{"dataset":"test.tickets","dimensions":["status"],"metrics":["cnt"]},
         "layout":{"x":0,"y":8,"w":6,"h":8}}
      ]}`
	fake := &fakeLLM{resp: toolResp(args)}
	draft, err := New(fake, "gpt-test").Generate(context.Background(), reg, "看一下工单情况")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if draft.Dashboard.Name != "工单运营看板" {
		t.Fatalf("unexpected name %q", draft.Dashboard.Name)
	}
	if len(draft.Dashboard.Widgets) != 2 {
		t.Fatalf("want 2 widgets, got %d", len(draft.Dashboard.Widgets))
	}
	if len(draft.Warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", draft.Warnings)
	}
	w0 := draft.Dashboard.Widgets[0]
	if w0.Query.Granularity != report.Granularity("day") || w0.Query.TimeRange == nil || w0.Query.TimeRange.Last != "7d" {
		t.Fatalf("time settings lost: %+v", w0.Query)
	}
	// Normalize 应补上默认排序与 limit
	if w0.Query.Limit != report.DefaultLimit {
		t.Fatalf("limit not defaulted: %d", w0.Query.Limit)
	}
	if len(w0.Query.OrderBy) == 0 || w0.Query.OrderBy[0].Field != report.TimeBucketColumn {
		t.Fatalf("order by not defaulted: %+v", w0.Query.OrderBy)
	}
	// 强制 function calling 必须真的下发
	if fake.lastReq.ToolChoice == nil || len(fake.lastReq.Tools) != 1 {
		t.Fatalf("tool choice not enforced: %+v", fake.lastReq.ToolChoice)
	}
	if fake.lastReq.Tools[0].Function.Name != toolName {
		t.Fatalf("unexpected tool %q", fake.lastReq.Tools[0].Function.Name)
	}
	// 系统提示词必须内嵌数据集目录，否则模型只能靠猜
	if !strings.Contains(fake.lastReq.Messages[0].Content, "test.tickets") {
		t.Fatal("catalog missing from system prompt")
	}
}

func TestGenerateStripsUnknownFields(t *testing.T) {
	reg := testRegistry(t)
	args := `{
      "name":"脏数据看板",
      "widgets":[
        {"id":"W Bad ID!","title":"混杂字段","type":"donut",
         "query":{"dataset":"test.tickets",
                  "dimensions":["status","not_exist","title"],
                  "metrics":["cnt","fake_metric"],
                  "filters":[{"field":"status","op":"eq","value":"open"},
                             {"field":"ghost","op":"eq","value":"x"},
                             {"field":"cost","op":"contains","value":"x"}]},
         "layout":{"x":9,"y":0,"w":8,"h":99}}
      ]}`
	draft, err := New(&fakeLLM{resp: toolResp(args)}, "").Generate(context.Background(), reg, "随便看看")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	w := draft.Dashboard.Widgets[0]
	if len(w.Query.Dimensions) != 1 || w.Query.Dimensions[0] != "status" {
		t.Fatalf("dimensions not sanitized: %v", w.Query.Dimensions)
	}
	if len(w.Query.Metrics) != 1 || w.Query.Metrics[0] != "cnt" {
		t.Fatalf("metrics not sanitized: %v", w.Query.Metrics)
	}
	if len(w.Query.Filters) != 1 || w.Query.Filters[0].Field != "status" {
		t.Fatalf("filters not sanitized: %+v", w.Query.Filters)
	}
	if w.Type != "bar" {
		t.Fatalf("unknown chart type not fallen back: %q", w.Type)
	}
	if err := w.Validate(reg); err != nil {
		t.Fatalf("sanitized widget id invalid: %v", err)
	}
	// 布局越界必须夹回 12 列栅格内
	if w.Layout.W > gridColumns || w.Layout.X+w.Layout.W > gridColumns || w.Layout.H > maxWidgetH {
		t.Fatalf("layout not clamped: %+v", w.Layout)
	}
	if len(draft.Warnings) < 4 {
		t.Fatalf("want warnings for each repair, got %v", draft.Warnings)
	}
}

func TestGenerateDropsUnknownDataset(t *testing.T) {
	reg := testRegistry(t)
	args := `{
      "name":"混合看板",
      "widgets":[
        {"id":"w_ghost","title":"幽灵","type":"bar","query":{"dataset":"ghost.table","metrics":["cnt"]}},
        {"id":"w_ok","title":"资产分布","type":"pie","query":{"dataset":"test.assets","dimensions":["kind"],"metrics":["cnt"]}}
      ]}`
	draft, err := New(&fakeLLM{resp: toolResp(args)}, "").Generate(context.Background(), reg, "资产情况")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(draft.Dashboard.Widgets) != 1 || draft.Dashboard.Widgets[0].ID != "w_ok" {
		t.Fatalf("unknown dataset widget not dropped: %+v", draft.Dashboard.Widgets)
	}
	if len(draft.Warnings) == 0 || !strings.Contains(strings.Join(draft.Warnings, "|"), "ghost.table") {
		t.Fatalf("missing warning for dropped widget: %v", draft.Warnings)
	}
}

func TestGenerateAllWidgetsInvalid(t *testing.T) {
	reg := testRegistry(t)
	args := `{"name":"空看板","widgets":[{"id":"w1","title":"坏图","type":"bar","query":{"dataset":"nope.nope"}}]}`
	if _, err := New(&fakeLLM{resp: toolResp(args)}, "").Generate(context.Background(), reg, "看点啥"); err == nil {
		t.Fatal("want error when no widget survives")
	}
}

func TestGenerateNumberWidgetDropsGrouping(t *testing.T) {
	reg := testRegistry(t)
	args := `{"name":"总量","widgets":[
      {"id":"w_total","title":"工单总数","type":"number",
       "query":{"dataset":"test.tickets","dimensions":["status"],"metrics":["cnt","avg_cost"],"granularity":"day"}}]}`
	draft, err := New(&fakeLLM{resp: toolResp(args)}, "").Generate(context.Background(), reg, "总数")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	w := draft.Dashboard.Widgets[0]
	if len(w.Query.Dimensions) != 0 || w.Query.Granularity != report.GranNone {
		t.Fatalf("number widget still grouped: %+v", w.Query)
	}
	if len(w.Query.Metrics) != 1 {
		t.Fatalf("number widget should keep single metric: %v", w.Query.Metrics)
	}
	// 未给 layout 时应自动排版
	if w.Layout.W <= 0 || w.Layout.H <= 0 {
		t.Fatalf("auto layout missing: %+v", w.Layout)
	}
}

func TestGenerateDedupWidgetIDs(t *testing.T) {
	reg := testRegistry(t)
	args := `{"name":"重复 ID","widgets":[
      {"id":"w_same","title":"A","type":"pie","query":{"dataset":"test.assets","dimensions":["kind"],"metrics":["cnt"]}},
      {"id":"w_same","title":"B","type":"table","query":{"dataset":"test.assets","dimensions":["kind"],"metrics":["cnt"]}}]}`
	draft, err := New(&fakeLLM{resp: toolResp(args)}, "").Generate(context.Background(), reg, "资产")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(draft.Dashboard.Widgets) != 2 {
		t.Fatalf("want 2 widgets, got %d", len(draft.Dashboard.Widgets))
	}
	if draft.Dashboard.Widgets[0].ID == draft.Dashboard.Widgets[1].ID {
		t.Fatalf("widget ids not deduped: %v", draft.Dashboard.Widgets)
	}
}

func TestGenerateFallbackToContentJSON(t *testing.T) {
	reg := testRegistry(t)
	text := "好的，这是看板：\n```json\n{\"name\":\"资产看板\",\"widgets\":[{\"id\":\"w_kind\",\"title\":\"资产类型\",\"type\":\"pie\",\"query\":{\"dataset\":\"test.assets\",\"dimensions\":[\"kind\"],\"metrics\":[\"cnt\"]}}]}\n```"
	draft, err := New(&fakeLLM{resp: contentResp(text)}, "").Generate(context.Background(), reg, "资产")
	if err != nil {
		t.Fatalf("fallback parse failed: %v", err)
	}
	if draft.Dashboard.Name != "资产看板" || len(draft.Dashboard.Widgets) != 1 {
		t.Fatalf("unexpected draft: %+v", draft.Dashboard)
	}
}

func TestGenerateTimeRangeOnDatasetWithoutTimeField(t *testing.T) {
	reg := testRegistry(t)
	args := `{"name":"资产趋势","widgets":[
      {"id":"w_t","title":"资产趋势","type":"line",
       "query":{"dataset":"test.assets","metrics":["cnt"],"granularity":"day","timeRange":{"last":"7d"}}}]}`
	draft, err := New(&fakeLLM{resp: toolResp(args)}, "").Generate(context.Background(), reg, "资产趋势")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	w := draft.Dashboard.Widgets[0]
	if w.Query.TimeRange != nil || w.Query.Granularity != report.GranNone {
		t.Fatalf("time settings should be cleared: %+v", w.Query)
	}
	// 清掉时间口径后仍需给出可画的分组，回落到首个可分组维度
	if len(w.Query.Dimensions) != 1 || w.Query.Dimensions[0] != "kind" {
		t.Fatalf("fallback grouping missing: %v", w.Query.Dimensions)
	}
}

func TestGenerateRejectsEmptyInput(t *testing.T) {
	reg := testRegistry(t)
	g := New(&fakeLLM{resp: toolResp(`{"name":"x","widgets":[]}`)}, "")
	if _, err := g.Generate(context.Background(), reg, "   "); err == nil {
		t.Fatal("want error for empty prompt")
	}
	if _, err := g.Generate(context.Background(), reg, "看板"); err == nil {
		t.Fatal("want error for empty widget list")
	}
}
