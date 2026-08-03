package agentic

import (
	"context"
	"strings"
	"testing"
	"time"
)

// seedRunWithFindings 构造一个带 findings 的 InspectionRun 并注册进引擎。
func seedRunWithFindings(e *Engine, tenantID string, findings []InspectionFinding) *InspectionRun {
	run := &InspectionRun{
		ID:           "run-" + tenantID + "-1",
		InspectionID: "insp-1",
		TenantID:     tenantID,
		Status:       "success",
		Findings:     mustJSONFindings(findings),
		StartedAt:    time.Now(),
	}
	e.mu.Lock()
	if e.inspectionRuns == nil {
		e.inspectionRuns = map[string][]*InspectionRun{}
	}
	e.inspectionRuns[run.InspectionID] = append(e.inspectionRuns[run.InspectionID], run)
	e.mu.Unlock()
	return run
}

// TestRemediationRiskClassify 分级: 低危 auto / 中高危 review / 危险词 blocked。
func TestRemediationRiskClassify(t *testing.T) {
	cases := []struct {
		level, suggestion, want string
	}{
		{"low", "清理临时日志文件", "auto"},
		{"medium", "重启数据库服务", "review"},
		{"high", "回滚应用版本到 v1.2", "review"},
		{"low", "执行 rm -rf / 清理", "blocked"},
		{"high", "drop table users 清空数据", "blocked"},
		{"medium", "shutdown 重启主机", "blocked"},
	}
	for _, c := range cases {
		if got := classifyRemediationRisk(c.level, c.suggestion); got != c.want {
			t.Fatalf("classify(%s, %s) = %s, want %s", c.level, c.suggestion, got, c.want)
		}
	}
}

// TestRemediationGenerateAndAutoExecute 生成 + 低危自动执行闭环。
func TestRemediationGenerateAndAutoExecute(t *testing.T) {
	e := newEngineForFinetune()
	seedRunWithFindings(e, "t1", []InspectionFinding{
		{Level: "low", Title: "临时文件过多", Suggestion: "清理 /tmp 下的临时文件"},
		{Level: "high", Title: "数据库连接数接近上限", Suggestion: "扩容连接池"},
		{Level: "low", Title: "磁盘告警", Suggestion: "rm -rf / 危险操作"},
	})

	acts, err := e.GenerateRemediations(context.Background(), "t1", "run-t1-1")
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if len(acts) != 3 {
		t.Fatalf("expected 3 actions, got %d", len(acts))
	}
	// 分级正确
	if acts[0].Risk != "auto" || acts[1].Risk != "review" || acts[2].Risk != "blocked" {
		t.Fatalf("unexpected risks: %s/%s/%s", acts[0].Risk, acts[1].Risk, acts[2].Risk)
	}
	// blocked 直接 failed
	if acts[2].Status != RemediationFailed || !strings.Contains(acts[2].Error, "拦截") {
		t.Fatalf("expected blocked failed with intercept message, got %s %s", acts[2].Status, acts[2].Error)
	}

	// 幂等: 再次生成返回同一批
	again, _ := e.GenerateRemediations(context.Background(), "t1", "run-t1-1")
	if len(again) != 3 || again[0].ID != acts[0].ID {
		t.Fatal("expected idempotent generation")
	}

	// 低危自动执行(演示执行器)
	done, _ := e.ExecuteRemediation(context.Background(), "t1", acts[0].ID)
	if done.Status != RemediationSucceeded {
		t.Fatalf("expected auto succeeded, got %s err=%s", done.Status, done.Error)
	}
	if !strings.Contains(done.Result, "演示执行") {
		t.Fatalf("expected demo executor result, got %q", done.Result)
	}
}

// TestRemediationReviewApproval 中高危走审批: 批准后执行 / 拒绝后 rejected。
func TestRemediationReviewApproval(t *testing.T) {
	e := newEngineForFinetune()
	seedRunWithFindings(e, "t1", []InspectionFinding{
		{Level: "high", Title: "数据库连接数接近上限", Suggestion: "扩容连接池"},
	})

	acts, _ := e.GenerateRemediations(context.Background(), "t1", "run-t1-1")
	act := acts[0]
	if act.Risk != "review" {
		t.Fatalf("expected review risk, got %s", act.Risk)
	}

	// 发起执行 -> awaiting_approval
	cur, err := e.ExecuteRemediation(context.Background(), "t1", act.ID)
	if err != nil {
		t.Fatalf("execute review: %v", err)
	}
	if cur.Status != RemediationAwaitingApproval {
		t.Fatalf("expected awaiting_approval, got %s", cur.Status)
	}
	// 等待审批单出现(审批请求在 goroutine 中触发)
	waitFor(t, func() bool { return len(e.approvals.ListPending("t1")) >= 1 })
	pending := e.approvals.ListPending("t1")
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending approval, got %d", len(pending))
	}
	if !e.approvals.Respond(pending[0].ID, "approve", "admin", "确认修复") {
		t.Fatal("approve failed")
	}
	waitFor(t, func() bool {
		a, _ := e.GetRemediation(act.ID)
		return a.Status == RemediationSucceeded
	})
	final, _ := e.GetRemediation(act.ID)
	if final.Status != RemediationSucceeded {
		t.Fatalf("expected succeeded after approve, got %s", final.Status)
	}
}

// TestRemediationReviewReject 审批拒绝 -> rejected。
func TestRemediationReviewReject(t *testing.T) {
	e := newEngineForFinetune()
	seedRunWithFindings(e, "t1", []InspectionFinding{
		{Level: "medium", Title: "慢查询", Suggestion: "优化索引"},
	})
	acts, _ := e.GenerateRemediations(context.Background(), "t1", "run-t1-1")
	_, _ = e.ExecuteRemediation(context.Background(), "t1", acts[0].ID)
	waitFor(t, func() bool { return len(e.approvals.ListPending("t1")) >= 1 })
	pending := e.approvals.ListPending("t1")
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending, got %d", len(pending))
	}
	e.approvals.Respond(pending[0].ID, "reject", "admin", "暂不执行")
	waitFor(t, func() bool {
		a, _ := e.GetRemediation(acts[0].ID)
		return a.Status == RemediationRejected
	})
	final, _ := e.GetRemediation(acts[0].ID)
	if final.Status != RemediationRejected {
		t.Fatalf("expected rejected, got %s", final.Status)
	}
}

// TestRemediationBlockedExecute 危险动作执行被拦截。
func TestRemediationBlockedExecute(t *testing.T) {
	e := newEngineForFinetune()
	seedRunWithFindings(e, "t1", []InspectionFinding{
		{Level: "high", Title: "清理", Suggestion: "dd if=/dev/zero of=/dev/sda"},
	})
	acts, _ := e.GenerateRemediations(context.Background(), "t1", "run-t1-1")
	if _, err := e.ExecuteRemediation(context.Background(), "t1", acts[0].ID); err == nil {
		t.Fatal("expected error for blocked action")
	}
}
