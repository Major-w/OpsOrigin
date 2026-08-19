package agentic

import (
	"context"
	"strings"
	"testing"
)

// —— 验证修复闭环 + runbook 沉淀 ——

func TestVerifyRemediationSetsDefaultVerified(t *testing.T) {
	e := &Engine{remediations: map[string]*RemediationAction{}}
	a := &RemediationAction{
		ID:           "a1",
		TenantID:     "t1",
		Title:        "磁盘使用率超 90%",
		Suggestion:   "清理 /var/log 下 7 天前日志",
		Status:       RemediationSucceeded,
		VerifyStatus: "",
	}
	e.verifyRemediation(t.Context(), a)
	if a.VerifyStatus != RemediationVerified {
		t.Fatalf("期望默认验证通过, 实际 %s", a.VerifyStatus)
	}
}

func TestVerifyRemediationVerifierHealthy(t *testing.T) {
	e := &Engine{remediations: map[string]*RemediationAction{}}
	e.RemediationVerifierFunc = func(ctx context.Context, action *RemediationAction) (bool, string, error) {
		return true, "探活 200 OK", nil
	}
	a := &RemediationAction{ID: "a2", TenantID: "t1"}
	e.verifyRemediation(t.Context(), a)
	if a.VerifyStatus != RemediationVerified {
		t.Fatalf("期望 verified, 实际 %s", a.VerifyStatus)
	}
}

func TestVerifyRemediationVerifierUnhealthy(t *testing.T) {
	e := &Engine{remediations: map[string]*RemediationAction{}}
	e.RemediationVerifierFunc = func(ctx context.Context, action *RemediationAction) (bool, string, error) {
		return false, "探活 503", nil
	}
	a := &RemediationAction{ID: "a3", TenantID: "t1"}
	e.verifyRemediation(t.Context(), a)
	if a.VerifyStatus != RemediationVerifyFailed {
		t.Fatalf("期望 verify_failed, 实际 %s", a.VerifyStatus)
	}
	if !strings.Contains(a.VerifyNote, "人工") {
		t.Fatalf("未恢复应提示人工跟进: %s", a.VerifyNote)
	}
}

func TestRunbookCreateFromVerifiedAction(t *testing.T) {
	e := &Engine{}
	a := &RemediationAction{
		ID: "r1", TenantID: "t1", RunID: "run1", Level: "high",
		Title: "MySQL 连接数超限", Suggestion: "kill 空闲连接; 调大 max_connections",
		Status: RemediationSucceeded, VerifyStatus: RemediationVerified,
	}
	rb, reused := e.CreateRunbookFromRemediation(t.Context(), a)
	if rb == nil || reused {
		t.Fatalf("期望新建 runbook, got nil=%v reused=%v", rb == nil, reused)
	}
	if rb.TitlePrefix == "" || len(rb.Steps) != 2 {
		t.Fatalf("runbook 步骤解析异常: %+v", rb.Steps)
	}
	// 幂等: 同前缀再次沉淀 → 复用
	_, reused2 := e.CreateRunbookFromRemediation(t.Context(), a)
	if !reused2 {
		t.Fatal("期望复用已有 runbook")
	}
	// 未验证通过的不沉淀
	bad := &RemediationAction{ID: "r2", TenantID: "t1", Title: "X", Suggestion: "y", Status: RemediationSucceeded, VerifyStatus: ""}
	if rb2, _ := e.CreateRunbookFromRemediation(t.Context(), bad); rb2 != nil {
		t.Fatal("未验证通过的修复不应沉淀 runbook")
	}
}

func TestRunbookMatchByTitlePrefix(t *testing.T) {
	e := &Engine{}
	a := &RemediationAction{
		ID: "r3", TenantID: "t1", Level: "low", Title: "磁盘使用率超 90%",
		Suggestion: "清理日志", Status: RemediationSucceeded, VerifyStatus: RemediationVerified,
	}
	e.CreateRunbookFromRemediation(t.Context(), a)
	hit := e.MatchRunbook("t1", "磁盘使用率超 95% 触发告警")
	if hit == nil {
		t.Fatal("期望按标题前缀命中 runbook")
	}
	if e.MatchRunbook("t1", "CPU 飙高") != nil {
		t.Fatal("不相关标题不应命中")
	}
}

// —— 自治分级 ——

func TestAutonomyDefaultAndSet(t *testing.T) {
	e := &Engine{}
	if e.GetAutonomy() != DefaultAutonomy {
		t.Fatalf("默认档位应为 act_with_approval, 实际 %s", e.GetAutonomy())
	}
	e.SetAutonomy(AutonomyAutonomous)
	if e.GetAutonomy() != AutonomyAutonomous {
		t.Fatal("设置 autonomous 后读取不一致")
	}
	e.SetAutonomy("")
	if e.GetAutonomy() != DefaultAutonomy {
		t.Fatal("空档位应回落默认")
	}
}

// —— RCA 基准 ——

func TestRCARecordAndAccuracy(t *testing.T) {
	e := &Engine{}
	rec := e.RecordRCA("t1", &RCARecord{IncidentID: "inc1", Title: "A", Hypothesis: "H1", Confidence: 0.8})
	if rec.ID == "" {
		t.Fatal("RCA 记录应生成 ID")
	}
	if _, err := e.LabelRCA("t1", rec.ID, true, "命中"); err != nil {
		t.Fatal(err)
	}
	rec2 := e.RecordRCA("t1", &RCARecord{IncidentID: "inc2", Title: "B", Hypothesis: "H2", Confidence: 0.5})
	if _, err := e.LabelRCA("t1", rec2.ID, false, "未命中"); err != nil {
		t.Fatal(err)
	}
	stats := e.RCAAccuracy("t1")
	if stats.Total != 2 || stats.Labeled != 2 || stats.Accurate != 1 {
		t.Fatalf("统计异常: %+v", stats)
	}
	if stats.AccuracyRate != 0.5 {
		t.Fatalf("准确率应为 0.5, 实际 %v", stats.AccuracyRate)
	}
}

// —— 告警分级 JSON 容错解析 ——

func TestParseAlertTriageJSON(t *testing.T) {
	res := &AlertTriageResult{}
	parseAlertTriageJSON("```json\n{\"hypothesis\":\"连接池耗尽\",\"confidence\":0.85,\"severity\":\"critical\",\"suggestions\":[\"扩容\"],\"escalate\":true,\"investigation\":\"排查完成\"}\n```", res)
	if res.Hypothesis != "连接池耗尽" || res.Confidence != 0.85 || !res.Escalate {
		t.Fatalf("解析异常: %+v", res)
	}
	// 噪声包裹
	res2 := &AlertTriageResult{}
	parseAlertTriageJSON("好的，分析如下：{ \"hypothesis\": \"X\", \"severity\": \"warning\" } 完毕", res2)
	if res2.Hypothesis != "X" || res2.Severity != "warning" {
		t.Fatalf("噪声解析异常: %+v", res2)
	}
	// 非法 JSON → 标记升级
	res3 := &AlertTriageResult{}
	parseAlertTriageJSON("无法解析的内容", res3)
	if !res3.Escalate || !strings.Contains(res3.Investigation, "解析失败") {
		t.Fatalf("非法输入应升级人工: %+v", res3)
	}
}
