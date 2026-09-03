package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
)

// TestBuildToolDefsDefaultAssistantDeniesWriteTools 验证默认助手(agentID=="")不暴露
// 写/执行类工具(ticket_create/script_run/script_generate), 避免纯对话场景下推理模型反复调用
// 产生工单泛滥/脚本误执行副作用或"调用→失败→重试"死循环。只读/安全工具仍应暴露。
func TestBuildToolDefsDefaultAssistantDeniesWriteTools(t *testing.T) {
	reg := mcp.NewRegistry()
	for _, name := range []string{
		"cmdb_query", "knowledge_search", "script_list", "script_detail", "script_executions",
		"ticket_create", "script_run", "script_generate",
	} {
		reg.Register(mcp.ToolDefinition{
			Name:        name,
			Description: "x",
			Parameters:  map[string]mcp.ParamSchema{},
			Handler:     func(_ context.Context, _ map[string]any) (any, error) { return nil, nil },
		})
	}
	e := NewEngine(reg, nil, nil, nil)

	defs := e.buildToolDefs("") // 默认助手
	byName := map[string]bool{}
	for _, d := range defs {
		byName[d.Function.Name] = true
	}
	for _, deny := range []string{"ticket_create", "script_run", "script_generate"} {
		if byName[deny] {
			t.Fatalf("default assistant must NOT expose write/exec tool %q, but it did", deny)
		}
	}
	for _, allow := range []string{"cmdb_query", "knowledge_search", "script_list", "script_detail", "script_executions"} {
		if !byName[allow] {
			t.Fatalf("default assistant should expose read-only tool %q, but it was filtered out", allow)
		}
	}
}

// TestToolFeedbackContentErrorHint 验证工具报错/空结果时回填内容携带反重试提示(retry:false + hint),
// 从反馈环源头打断 error→retry 与 空结果→换词重搜 两类死循环; 正常非空结果原样回填。
func TestToolFeedbackContentErrorHint(t *testing.T) {
	// 1) execErr=true: execToolChecked 已写入 hint/retry
	errResult := map[string]any{"error": "db connection refused", "retry": false, "hint": "工具执行失败, 请勿重复重试同一工具; 请直接告知用户该能力暂不可用或检查相关后端是否就绪。"}
	c1 := toolFeedbackContent(errResult, true)
	if !strings.Contains(c1, "请勿重复重试") {
		t.Fatalf("execErr result should carry anti-retry hint, got: %s", c1)
	}

	// 2) execErr=false 但结果内含 error 字段(handler 把错误包进结果): 应补注 hint
	inMapErr := map[string]any{"error": "validation failed"}
	c2 := toolFeedbackContent(inMapErr, false)
	if !strings.Contains(c2, "请勿重复重试") {
		t.Fatalf("result-with-error-field should be annotated with anti-retry hint, got: %s", c2)
	}

	// 3) 空结果 count:0: 应注入"空结果反重试"提示(retry:false + 工具返回为空), 而非错误提示。
	//    这是打断 空结果→换词重搜→仍为空 死循环的关键: 明确告诉模型"无数据≠失败, 不要重试"。
	empty := map[string]any{"count": 0, "items": []any{}}
	c3 := toolFeedbackContent(empty, false)
	if !strings.Contains(c3, "工具返回为空") {
		t.Fatalf("empty result (count:0) should carry empty-result anti-retry hint, got: %s", c3)
	}
	if strings.Contains(c3, "工具执行失败") {
		t.Fatalf("empty result must NOT be annotated with the error hint, got: %s", c3)
	}

	// 4) 正常非空结果: 不注入任何提示, 原样回填。
	normal := map[string]any{"count": 2, "items": []any{"a", "b"}}
	c4 := toolFeedbackContent(normal, false)
	if strings.Contains(c4, "retry") || strings.Contains(c4, "hint") {
		t.Fatalf("normal non-empty result must NOT be annotated, got: %s", c4)
	}
}
