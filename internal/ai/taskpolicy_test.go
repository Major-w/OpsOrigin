// T3 权限策略与规划解析单测。
package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/opsorigin/core/internal/ai/mcp"
)

func TestJudgeToolPermission(t *testing.T) {
	cases := []struct {
		mode, risk, tool, want string
	}{
		// readonly: 只读放行, 写/高危一律拒绝(不可批)
		{"readonly", "read", "cmdb_query", "allow"},
		{"readonly", "write", "doc_write", "deny"},
		{"readonly", "critical", "script_run", "deny"},
		// standard: 只读 + 任务命名空间写放行; 跨域写/高危需审批
		{"standard", "read", "knowledge_search", "allow"},
		{"standard", "write", "doc_write", "allow"},
		{"standard", "write", "ticket_create", "approval"},
		{"standard", "write", "doc_to_knowledge", "approval"}, // 跨域写(知识库)
		{"standard", "critical", "script_run", "approval"},
		// auto: 读写放行, 仅高危审批
		{"auto", "write", "doc_write", "allow"},
		{"auto", "write", "ticket_create", "allow"},
		{"auto", "critical", "script_run", "approval"},
		// full_auto: 全放行
		{"full_auto", "critical", "script_run", "allow"},
		{"full_auto", "write", "doc_write", "allow"},
		// 未知模式回落 standard
		{"", "write", "doc_write", "allow"},
		{"", "critical", "script_run", "approval"},
	}
	for _, c := range cases {
		if got := judgeToolPermission(c.mode, c.risk, c.tool); got != c.want {
			t.Errorf("judgeToolPermission(%q,%q,%q) = %q, want %q", c.mode, c.risk, c.tool, got, c.want)
		}
	}
}

func TestToolRisk(t *testing.T) {
	s := &Service{}
	// 未显式标注的存量工具走内置默认表
	if got := s.toolRisk("cmdb_query"); got != "read" {
		t.Errorf("cmdb_query risk = %q, want read", got)
	}
	if got := s.toolRisk("script_run"); got != "critical" {
		t.Errorf("script_run risk = %q, want critical", got)
	}
	// 未知/外部 MCP 工具保守按 write
	if got := s.toolRisk("some_external_tool"); got != "write" {
		t.Errorf("unknown tool risk = %q, want write", got)
	}
	// 显式标注优先(任务工具在 registerTaskTools 标注; 这里直接验证显式字段路径)
	s2 := &Service{Registry: mcp.NewRegistry()}
	s2.Registry.Register(mcp.ToolDefinition{Name: "my_tool", Risk: "critical", Handler: func(ctx context.Context, input map[string]any) (any, error) { return nil, nil }})
	if got := s2.toolRisk("my_tool"); got != "critical" {
		t.Errorf("explicit risk = %q, want critical", got)
	}
}

func TestParsePlanSteps(t *testing.T) {
	// 纯数组
	steps := parsePlanSteps(`[{"title":"收集资产清单"},{"title":"执行巡检"},{"title":"输出报告"}]`)
	if len(steps) != 3 || steps[0].Title != "收集资产清单" || steps[0].Status != "pending" {
		t.Fatalf("plain array parse failed: %+v", steps)
	}
	// markdown 围栏包裹
	steps = parsePlanSteps("```json\n[{\"title\":\"步骤一\"}]\n```")
	if len(steps) != 1 || steps[0].Title != "步骤一" {
		t.Fatalf("fenced parse failed: %+v", steps)
	}
	// 带前后废话
	steps = parsePlanSteps("好的, 以下是计划: [{\"title\":\"A\"},{\"title\":\"\"},{\"title\":\"B\"}] 请确认")
	if len(steps) != 2 || steps[0].Title != "A" || steps[1].Title != "B" {
		t.Fatalf("noisy parse failed: %+v", steps)
	}
	// {"steps":[...]} 包裹
	steps = parsePlanSteps(`{"steps":[{"title":"X"}]}`)
	if len(steps) != 1 || steps[0].Title != "X" {
		t.Fatalf("wrapped parse failed: %+v", steps)
	}
	// 无效输出
	if got := parsePlanSteps("这不是 JSON"); got != nil {
		t.Fatalf("invalid parse should be nil, got %+v", got)
	}
	// 步骤数截断到 12
	var sb strings.Builder
	sb.WriteString("[")
	for i := 0; i < 20; i++ {
		if i > 0 {
			sb.WriteString(",")
		}
		sb.WriteString(`{"title":"s"}`)
	}
	sb.WriteString("]")
	if got := parsePlanSteps(sb.String()); len(got) != 12 {
		t.Fatalf("cap to 12 failed, got %d", len(got))
	}
}
