package agentic

import (
	"context"
	"testing"

	"github.com/opsorigin/core/pkg/llm"
)

// TestChatStreamRunsTrajectoryHooksAndEvolution 锁定: 流式对话路径(chatWithToolsStream, 即纯净模式实际走的)
// 与非流式 chatWithToolsConv 对齐 —— 同样记录轨迹、运行 PreStep 钩子、回流技能演化。
// 此前流式路径只调 execToolChecked, 漏掉这三项运行级能力, 属于"缩减"。
func TestChatStreamRunsTrajectoryHooksAndEvolution(t *testing.T) {
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		return 200, ansResp("hello world")
	})

	traj := NewMemTrajectoryRecorder()
	e.SetTrajectoryRecorder(traj)

	backend := newFakeEvoBackend()
	evo := NewMemSkillEvolver(backend)
	e.SetSkillEvolver(evo)

	var preStepCalls int
	e.RegisterHook(Hook{PreStep: func(_ context.Context, _ string, _ string) (bool, error) {
		preStepCalls++
		return false, nil
	}})

	ch, _, err := e.ChatStream(context.Background(), "agentX", "hi", nil, false, "conv_parity", false)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	// 排空 channel, 确保后台 goroutine 完成(含轨迹/演化写入)。
	for range ch {
	}

	if preStepCalls != 1 {
		t.Fatalf("PreStep 钩子应在流式路径触发 1 次, got %d", preStepCalls)
	}

	turns, terr := traj.GetTurns(context.Background(), "conv_parity")
	if terr != nil {
		t.Fatalf("GetTurns error: %v", terr)
	}
	if len(turns) != 1 {
		t.Fatalf("流式路径应记录 1 条轨迹, got %d", len(turns))
	}
	if turns[0].Prompt != "hi" {
		t.Fatalf("轨迹 Prompt 应为 hi, got %q", turns[0].Prompt)
	}
	if turns[0].FinalContent != "hello world" {
		t.Fatalf("轨迹 FinalContent 应为 hello world, got %q", turns[0].FinalContent)
	}
	if len(turns[0].Steps) < 1 {
		t.Fatalf("轨迹应至少含 1 个 step, got %d", len(turns[0].Steps))
	}

	if len(backend.memories) != 1 {
		t.Fatalf("技能演化回流应写入 1 次经验记忆, got %d", len(backend.memories))
	}
}

// TestChatStreamTrajectoryWithEmptyAgent 锁定: 纯净模式默认不传 agent_id(空), 流式路径仍应记录轨迹
// (轨迹按 conversationID 维度, 与 agent 无关), 保证纯净模式对话可被回放/复盘。
func TestChatStreamTrajectoryWithEmptyAgent(t *testing.T) {
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		return 200, ansResp("pure mode answer")
	})
	traj := NewMemTrajectoryRecorder()
	e.SetTrajectoryRecorder(traj)

	ch, _, err := e.ChatStream(context.Background(), "", "纯模式提问", nil, false, "conv_pure", false)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	for range ch {
	}

	turns, terr := traj.GetTurns(context.Background(), "conv_pure")
	if terr != nil {
		t.Fatalf("GetTurns error: %v", terr)
	}
	if len(turns) != 1 {
		t.Fatalf("空 agent 的流式对话也应记录 1 条轨迹, got %d", len(turns))
	}
	if turns[0].FinalContent != "pure mode answer" {
		t.Fatalf("轨迹 FinalContent 应为 pure mode answer, got %q", turns[0].FinalContent)
	}
}
