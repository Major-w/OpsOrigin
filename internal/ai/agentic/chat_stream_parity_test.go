package agentic

import (
	"context"
	"testing"

	"github.com/opsorigin/core/internal/ai/model"
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

	ch, _, err := e.ChatStream(context.Background(), "agentX", "hi", nil, false, "conv_parity", false, "")
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

	ch, _, err := e.ChatStream(context.Background(), "", "纯模式提问", nil, false, "conv_pure", false, "")
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

// TestModelOverrideRoutesToLLM 锁定: 聊天请求携带的 model 字段能正确路由到 LLM 层,
// 且遵循"请求覆盖 > agent 配置模型 > 引擎默认(空)"三档回退(对应 modelForAgent)。
// 这是"全功能/纯净模式可会话级选模型"的底层契约: 前端下拉选的模型必须真实生效。
func TestModelOverrideRoutesToLLM(t *testing.T) {
	var gotModel string
	e := newTeamTestEngine(t, func(req llm.ChatRequest) (int, string) {
		gotModel = req.Model
		return 200, ansResp("ok")
	})
	// 注册一个带默认模型的 agent, 验证 agent 档与请求覆盖档。
	e.agents["agentX"] = &model.AgentInstance{ID: "agentX", ModelName: "agent-model"}

	// 1) 无覆盖: 应使用 agent 配置模型
	gotModel = ""
	if _, _, err := e.Chat(context.Background(), "agentX", "hi", nil, "", "", false); err != nil {
		t.Fatalf("chat(agent, no override): %v", err)
	}
	if gotModel != "agent-model" {
		t.Errorf("无覆盖 model = %q, want agent-model", gotModel)
	}

	// 2) 请求覆盖: 应优先生效, 压过 agent 配置模型
	gotModel = ""
	if _, _, err := e.Chat(context.Background(), "agentX", "hi", nil, "", "req-model", false); err != nil {
		t.Fatalf("chat(agent, override): %v", err)
	}
	if gotModel != "req-model" {
		t.Errorf("请求覆盖 model = %q, want req-model", gotModel)
	}

	// 3) 空 agent + 空覆盖: 回退引擎默认模型(单 client 以自身默认模型兜底, 本测试 harness 为 "test")。
	gotModel = "unset"
	if _, _, err := e.Chat(context.Background(), "", "hi", nil, "", "", false); err != nil {
		t.Fatalf("chat(empty, no override): %v", err)
	}
	if gotModel != "test" {
		t.Errorf("空 agent/空覆盖 model = %q, want 引擎默认模型 test", gotModel)
	}
	if gotModel == "agent-model" || gotModel == "req-model" {
		t.Errorf("空覆盖不应命中 agent/请求档, got %q", gotModel)
	}

	// 4) 流式路径同样生效
	gotModel = ""
	ch, _, err := e.ChatStream(context.Background(), "agentX", "hi", nil, false, "conv-m", false, "req-model-stream")
	if err != nil {
		t.Fatalf("ChatStream(override): %v", err)
	}
	for range ch {
	}
	if gotModel != "req-model-stream" {
		t.Errorf("流式请求覆盖 model = %q, want req-model-stream", gotModel)
	}
}
