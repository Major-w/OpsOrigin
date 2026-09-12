package itsm

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/opsorigin/core/internal/itsm/model"
)

// TestHoldResumeCompensatesOnce 验证挂起补偿只补「本次」挂起时长。
// 修复前 resume 用累计 HoldSeconds 顺延 DeadlineAt，第二次挂起会把第一次
// 已补过的时长再补一遍（挂起 N 次，SLA 被放得越来越宽）。
func TestHoldResumeCompensatesOnce(t *testing.T) {
	s := newTestService()
	ctx := context.Background()

	deadline := time.Now().Add(4 * time.Hour)
	tk, err := s.CreateTicket(ctx, model.CreateTicketRequest{
		Title:      "挂起补偿测试",
		CreatedBy:  "u1",
		RiskLevel:  model.RiskCritical,
		DeadlineAt: &deadline,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}
	base := tk.DeadlineAt.Unix()

	// 连续两次挂起，每次虚构为已挂起 10 分钟（直接回拨 HoldAt 模拟）。
	for i := 1; i <= 2; i++ {
		rec := callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/hold", `{"hold_by":"u1","reason":"test"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("hold #%d failed: %d %s", i, rec.Code, rec.Body.String())
		}
		cur, err := s.store.Get(ctx, tk.ID, tk.TenantID)
		if err != nil {
			t.Fatalf("get ticket: %v", err)
		}
		back := time.Now().Add(-10 * time.Minute)
		cur.HoldAt = &back
		if err := s.store.Update(ctx, cur); err != nil {
			t.Fatalf("backdate hold: %v", err)
		}

		rec = callPost(t, s, "/api/v1/itsm/tickets/"+tk.ID+"/resume", `{"resumed_by":"u1"}`)
		if rec.Code != http.StatusOK {
			t.Fatalf("resume #%d failed: %d %s", i, rec.Code, rec.Body.String())
		}
		cur, _ = s.store.Get(ctx, tk.ID, tk.TenantID)
		got := cur.DeadlineAt.Unix() - base
		want := int64(i) * 600
		if got < want-5 || got > want+5 {
			t.Fatalf("第 %d 次挂起后顺延 %ds，期望约 %ds（旧实现会给出 %ds 的过度补偿）",
				i, got, want, want+int64(i-1)*600)
		}
		wantHeld := i * 600
		if cur.HoldSeconds < wantHeld-5 || cur.HoldSeconds > wantHeld+5 {
			t.Fatalf("第 %d 次挂起后累计挂起秒数应为约 %d，实际 %d", i, wantHeld, cur.HoldSeconds)
		}
	}
}

// TestOLABaseIgnoresUpdatedAt 验证 OLA 计时基准不随工单 UpdatedAt 漂移。
// 修复前 checkOLAPolicy 用 time.Since(t.UpdatedAt)，写评论/改字段就把环节耗时清零。
func TestOLABaseIgnoresUpdatedAt(t *testing.T) {
	s := newTestService()
	ctx := context.Background()

	tk, err := s.CreateTicket(ctx, model.CreateTicketRequest{
		Title: "OLA 基准测试", CreatedBy: "u1", RiskLevel: model.RiskLow,
	})
	if err != nil {
		t.Fatalf("create ticket: %v", err)
	}

	before := s.olaElapsedBase(ctx, tk.TenantID, tk)
	if !before.Equal(tk.CreatedAt) {
		t.Fatalf("无流程工单的 OLA 基准应等于创建时间，实际 %v", before)
	}

	// 模拟写评论/改字段：UpdatedAt 跳到未来，基准不应变化。
	tk.UpdatedAt = time.Now().Add(3 * time.Hour)
	if err := s.store.Update(ctx, tk); err != nil {
		t.Fatalf("update ticket: %v", err)
	}
	after := s.olaElapsedBase(ctx, tk.TenantID, tk)
	if !after.Equal(before) {
		t.Fatalf("UpdatedAt 变化后 OLA 基准漂移：%v → %v", before, after)
	}
}
