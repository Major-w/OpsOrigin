package agentic

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/llm"
)

func newTestEngineForDataset() *Engine {
	return NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
}

// TestExportDatasetFeedback 验证反馈样本导出: 点赞/点踩 + 对话上下文。
func TestExportDatasetFeedback(t *testing.T) {
	e := newTestEngineForDataset()
	// 造对话
	conv := &Conversation{
		ID:       "conv-1",
		TenantID: "t1",
		Messages: []llm.Message{
			{Role: "user", Content: "帮我看看生产环境数据库连接数是否异常"},
			{Role: "assistant", Content: "已查询 CMDB, 当前连接数 920/1000, 接近上限, 建议扩容连接池。"},
		},
	}
	if err := e.SaveConversation(context.Background(), conv); err != nil {
		t.Fatal(err)
	}

	feedbacks := []*Feedback{
		{TenantID: "t1", ConversationID: "conv-1", MessageID: "m-up", Rating: "up", CreatedAt: time.Now()},
		{TenantID: "t1", ConversationID: "conv-1", MessageID: "m-down", Rating: "down", CreatedAt: time.Now()},
	}

	jsonl, summary, err := e.ExportDataset(context.Background(), "t1", feedbacks)
	if err != nil {
		t.Fatal(err)
	}
	if summary.PreferenceUp != 1 || summary.PreferenceDown != 1 || summary.Total != 2 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	lines := strings.Split(strings.TrimSpace(jsonl), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d", len(lines))
	}
	// 每行可解析且 instruction 来自对话
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(first["instruction"].(string), "连接数") {
		t.Fatalf("instruction should come from conversation, got %v", first["instruction"])
	}
}

// TestExportDatasetDisposalCase 验证处置案例导出(经回调注入)。
func TestExportDatasetDisposalCase(t *testing.T) {
	e := newTestEngineForDataset()
	e.SetDisposalCaseProvider(func(ctx context.Context, tenantID string) ([]DisposalCase, error) {
		return []DisposalCase{
			{ID: "kb-1", Title: "[处置案例] 数据库连接数接近上限", Content: "扩容连接池并排查慢连接, 连接数回落至 600/1000。"},
		}, nil
	})

	jsonl, summary, err := e.ExportDataset(context.Background(), "t1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if summary.DisposalCase != 1 || summary.Total != 1 {
		t.Fatalf("unexpected summary: %+v", summary)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonl)), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["category"] != "disposal_case" {
		t.Fatalf("expected disposal_case category, got %v", entry["category"])
	}
}

// TestExportDatasetEmpty 空数据不报错。
func TestExportDatasetEmpty(t *testing.T) {
	e := newTestEngineForDataset()
	jsonl, summary, err := e.ExportDataset(context.Background(), "t1", nil)
	if err != nil {
		t.Fatal(err)
	}
	if jsonl != "" || summary.Total != 0 {
		t.Fatalf("expected empty dataset, got %q %+v", jsonl, summary)
	}
}
