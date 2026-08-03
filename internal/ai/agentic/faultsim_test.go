package agentic

import (
	"context"
	"encoding/json"
	"testing"
)

// TestFaultSimScenarios 场景库完整: 5 类故障可列出且字段齐全。
func TestFaultSimScenarios(t *testing.T) {
	e := newEngineForFinetune()
	scs := e.ListFaultScenarios()
	if len(scs) != 5 {
		t.Fatalf("expected 5 preset scenarios, got %d", len(scs))
	}
	for _, s := range scs {
		if s.ID == "" || s.Name == "" || s.Type == "" || len(s.Keywords) == 0 {
			t.Fatalf("scenario incomplete: %+v", s)
		}
	}
}

// TestFaultSimEvalMatched 评估闭环: 巡检返回命中故障关键词的 findings -> matched=true + 高分。
func TestFaultSimEvalMatched(t *testing.T) {
	e := newEngineForFinetune()
	run, err := e.StartFaultInjection(context.Background(), "t1", "fault-disk-full", "host-1", "web-01", "")
	if err != nil {
		t.Fatalf("start injection: %v", err)
	}
	// 直接调用评估逻辑(不走真实巡检): 模拟巡检返回磁盘故障
	inspRun := &InspectionRun{
		Status:  "success",
		Summary: "检测到目标主机磁盘空间告急, 使用率 96%, 建议清理日志并扩容。",
		Findings: mustJSONFindings([]InspectionFinding{
			{Level: "high", Title: "磁盘空间耗尽", Detail: "使用率 96%", Suggestion: "清理日志"},
		}),
	}
	e.evaluateInjection(run.ID, inspRun)
	cur := faultInjectionByID(e, run.ID)
	if !cur.MatchedScenario {
		t.Fatalf("expected matched scenario, report=%s", cur.Report)
	}
	if cur.Score < 80 {
		t.Fatalf("expected high score, got %d", cur.Score)
	}
	if cur.Status != FaultInjectionInjecting {
		t.Fatalf("expected injecting until recover, got %s", cur.Status)
	}
}

// TestFaultSimEvalUnmatched 未命中: Agent 未识别故障 -> matched=false + 低分。
func TestFaultSimEvalUnmatched(t *testing.T) {
	e := newEngineForFinetune()
	run, _ := e.StartFaultInjection(context.Background(), "t1", "fault-process-kill", "host-2", "app-02", "")
	inspRun := &InspectionRun{
		Status:  "success",
		Summary: "日常巡检完成, 未发现异常。",
	}
	e.evaluateInjection(run.ID, inspRun)
	cur := faultInjectionByID(e, run.ID)
	if cur.MatchedScenario {
		t.Fatal("expected unmatched")
	}
	if cur.Score > 70 {
		t.Fatalf("expected low score for no findings, got %d", cur.Score)
	}
}

// TestFaultSimRecover 恢复/回滚: injecting -> recovered + 记录恢复时间。
func TestFaultSimRecover(t *testing.T) {
	e := newEngineForFinetune()
	run, _ := e.StartFaultInjection(context.Background(), "t1", "fault-cpu-high", "host-3", "", "")
	cur, err := e.RecoverFaultInjection(run.ID)
	if err != nil {
		t.Fatalf("recover: %v", err)
	}
	if cur.Status != FaultInjectionRecovered || cur.RecoveredAt == nil {
		t.Fatalf("expected recovered with timestamp, got %+v", cur)
	}
}

// TestFaultSimInvalidScenario 无效场景 -> 报错。
func TestFaultSimInvalidScenario(t *testing.T) {
	e := newEngineForFinetune()
	if _, err := e.StartFaultInjection(context.Background(), "t1", "fault-nope", "h", "", ""); err == nil {
		t.Fatal("expected error for unknown scenario")
	}
}

// mustJSONFindings 把 findings 序列化为 JSON 字符串(模拟 run.Findings 存储格式)。
func mustJSONFindings(fs []InspectionFinding) string {
	b, _ := json.Marshal(fs)
	return string(b)
}

// faultInjectionByID 测试辅助: 从租户列表按 ID 找演练记录。
func faultInjectionByID(e *Engine, id string) *FaultInjectionRun {
	for _, r := range e.ListFaultInjections("t1") {
		if r.ID == id {
			return r
		}
	}
	return nil
}
