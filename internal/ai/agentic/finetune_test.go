package agentic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/ratelimit"
	"github.com/opsorigin/core/pkg/llm"
)

func newEngineForFinetune() *Engine {
	e := NewEngine(mcp.NewRegistry(), ratelimit.New(), nil, nil)
	e.SetModelRouter(NewModelRouter(llm.Config{Model: "deepseek-chat", BaseURL: "http://x", Timeout: time.Second}, nil))
	return e
}

// TestFinetuneClosedLoop 微调闭环: 创建任务 -> runner 执行 -> succeeded +
// 产物模型注册进模型池(可见新 provider, 可被 Agent 选用)。
func TestFinetuneClosedLoop(t *testing.T) {
	e := newEngineForFinetune()
	e.SetFinetuneRunner(func(ctx context.Context, task *FinetuneTask) (string, error) {
		return task.BaseModel + "-ft", nil
	})

	task, err := e.CreateFinetuneTask("t1", "容量预测微调", "latest", "deepseek-chat", "loRA",
		map[string]any{"epochs": 3}, "admin")
	if err != nil {
		t.Fatalf("create finetune task: %v", err)
	}
	// runner 是同步返回的, 但 executeFinetune 在 goroutine 中; 等待完成
	waitFor(t, func() bool {
		cur, _ := e.GetFinetuneTask(task.ID)
		return cur.Status == FinetuneStatusSucceeded
	})
	cur, _ := e.GetFinetuneTask(task.ID)
	if cur.Status != FinetuneStatusSucceeded {
		t.Fatalf("expected succeeded, got %s (err=%s)", cur.Status, cur.Error)
	}
	if cur.ResultModel != "deepseek-chat-ft" {
		t.Fatalf("unexpected result model: %s", cur.ResultModel)
	}
	if cur.ResultProviderName == "" {
		t.Fatal("expected provider name set")
	}

	// 产物应已注册进模型池
	found := false
	for _, m := range e.router.Models() {
		if m.Name == cur.ResultProviderName && m.Model == "deepseek-chat-ft" {
			found = true
		}
	}
	if !found {
		t.Fatalf("finetuned provider not registered in model pool: %v", e.router.Models())
	}
}

// TestFinetuneFailed 微调失败 -> 任务 failed + 不注册模型池。
func TestFinetuneFailed(t *testing.T) {
	e := newEngineForFinetune()
	e.SetFinetuneRunner(func(ctx context.Context, task *FinetuneTask) (string, error) {
		return "", &errMsg{msg: "训练后端不可用"}
	})
	task, _ := e.CreateFinetuneTask("t1", "失败任务", "latest", "deepseek-chat", "loRA", nil, "admin")
	waitFor(t, func() bool {
		cur, _ := e.GetFinetuneTask(task.ID)
		return cur.Status == FinetuneStatusFailed
	})
	cur, _ := e.GetFinetuneTask(task.ID)
	if cur.Status != FinetuneStatusFailed || cur.Error == "" {
		t.Fatalf("expected failed with error, got %s err=%q", cur.Status, cur.Error)
	}
	if len(e.router.Models()) != 1 {
		t.Fatalf("model pool should have only default provider, got %d", len(e.router.Models()))
	}
}

// TestFinetuneNoRunner 未配置 runner 且未开 mock -> 停留 pending + 提示。
func TestFinetuneNoRunner(t *testing.T) {
	e := newEngineForFinetune()
	task, _ := e.CreateFinetuneTask("t1", "未配置任务", "latest", "deepseek-chat", "loRA", nil, "admin")
	cur, _ := e.GetFinetuneTask(task.ID)
	if cur.Status != FinetuneStatusPending {
		t.Fatalf("expected pending, got %s", cur.Status)
	}
	if !strings.Contains(cur.Error, "未配置微调后端") {
		t.Fatalf("expected hint about missing backend, got %q", cur.Error)
	}
}

// TestFinetuneMock 启用 mock -> 任务自动走 mock 执行成功。
func TestFinetuneMock(t *testing.T) {
	old := finetuneMock
	SetFinetuneMock(true)
	defer SetFinetuneMock(old)

	e := newEngineForFinetune()
	task, _ := e.CreateFinetuneTask("t1", "mock 任务", "latest", "qwen-max", "loRA", nil, "admin")
	waitFor(t, func() bool {
		cur, _ := e.GetFinetuneTask(task.ID)
		return cur.Status == FinetuneStatusSucceeded
	})
	cur, _ := e.GetFinetuneTask(task.ID)
	if cur.ResultModel != "qwen-max-ft" {
		t.Fatalf("mock result model unexpected: %s", cur.ResultModel)
	}
}

type errMsg struct{ msg string }

func (e *errMsg) Error() string { return e.msg }

// waitFor 轮询等待条件满足(最多 3s)。
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}
