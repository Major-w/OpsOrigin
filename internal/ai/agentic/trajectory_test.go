package agentic

import (
	"context"
	"testing"
)

func TestMemTrajectoryRecorder(t *testing.T) {
	r := NewMemTrajectoryRecorder()
	ctx := context.Background()

	if turns, err := r.GetTurns(ctx, "conv1"); err != nil || len(turns) != 0 {
		t.Fatalf("empty get: got %d err %v", len(turns), err)
	}

	t1 := &TrajectoryTurn{ConversationID: "conv1", AgentID: "a1", Steps: []TrajectoryStep{{Index: 0, Finished: true, Assistant: "hi"}}}
	t2 := &TrajectoryTurn{ConversationID: "conv1", AgentID: "a1", Steps: []TrajectoryStep{{Index: 0, ToolCalls: []ToolCallRecord{{Name: "k", Result: "v"}}}}}
	if err := r.RecordTurn(ctx, t1); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordTurn(ctx, t2); err != nil {
		t.Fatal(err)
	}

	turns, err := r.GetTurns(ctx, "conv1")
	if err != nil {
		t.Fatal(err)
	}
	if len(turns) != 2 {
		t.Fatalf("want 2 turns, got %d", len(turns))
	}
	if turns[0].Steps[0].Assistant != "hi" {
		t.Errorf("turn0 assistant = %q", turns[0].Steps[0].Assistant)
	}
	if len(turns[1].Steps[0].ToolCalls) != 1 {
		t.Errorf("turn1 toolcalls = %d", len(turns[1].Steps[0].ToolCalls))
	}

	// 不同会话隔离
	if other, _ := r.GetTurns(ctx, "conv2"); len(other) != 0 {
		t.Errorf("conv2 should be empty, got %d", len(other))
	}
	// nil 安全
	if err := r.RecordTurn(ctx, nil); err != nil {
		t.Errorf("nil record should be noop, got %v", err)
	}
}
