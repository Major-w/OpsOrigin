package chatops

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/ai/agentic"
)

func TestClipForIM(t *testing.T) {
	if got := clipForIM("  hello  "); got != "hello" {
		t.Fatalf("trim: %q", got)
	}
	long := strings.Repeat("字", replyMaxRunes+100)
	got := clipForIM(long)
	if !strings.Contains(got, "已截断") {
		t.Fatal("clip notice missing")
	}
	if len([]rune(strings.SplitN(got, "\n\n", 2)[0])) != replyMaxRunes {
		t.Fatalf("clip length mismatch")
	}
}

// TestApprovalRespondContract 验证 chatops 审批桥所依赖的 ApprovalManager 契约:
// ListPending 拿 ID → Respond(approve) 解除阻塞; 重复 Respond 返回 false(桥据此提示"已处理")。
func TestApprovalRespondContract(t *testing.T) {
	mgr := agentic.NewApprovalManager()

	respCh := make(chan *agentic.ApprovalResponse, 1)
	go func() {
		respCh <- mgr.Request(context.Background(), "default", "agent-1", "script_run", `{"id":"s1"}`, "执行巡检脚本", 5*time.Second)
	}()

	// 等 Request 挂起
	var pa *agentic.PendingApproval
	for i := 0; i < 50; i++ {
		time.Sleep(10 * time.Millisecond)
		if pend := mgr.ListPending("default"); len(pend) > 0 {
			pa = pend[0]
			break
		}
	}
	if pa == nil {
		t.Fatal("approval not pending")
	}
	if pa.Tool != "script_run" || pa.Summary != "执行巡检脚本" {
		t.Fatalf("pending fields mismatch: %+v", pa)
	}

	if !mgr.Respond(pa.ID, "approve", "im:feishu:ou_x", "") {
		t.Fatal("first respond should succeed")
	}
	select {
	case resp := <-respCh:
		if resp == nil || resp.Action != "approve" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting response")
	}

	// 已处理的审批单再 Respond 必须返回 false
	if mgr.Respond(pa.ID, "reject", "im:feishu:ou_x", "") {
		t.Fatal("double respond should fail")
	}
}
