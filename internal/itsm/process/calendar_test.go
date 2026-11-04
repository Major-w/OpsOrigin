package process

import (
	"context"
	"testing"
	"time"
)

// fakeCalendar 业务耗时按真实墙钟乘以 scale 折算（模拟 8x5 日历的压缩/放大效应）。
type fakeCalendar struct{ scale float64 }

func (f *fakeCalendar) BusinessElapsed(from, to time.Time) time.Duration {
	return time.Duration(float64(to.Sub(from)) * f.scale)
}

func newCalInst(stepID string, startedAgo time.Duration) *ProcessInstance {
	started := time.Now().Add(-startedAgo)
	return &ProcessInstance{
		TicketID: "t-cal", TenantID: "default", Status: "running", CurrentStep: stepID,
		StepResults: map[string]any{stepID: map[string]any{"started_at": started.Format(time.RFC3339)}},
	}
}

// 配置日历后, 业务耗时未达时限不应触发超时（即使墙钟已超过）。
func TestUserTimeoutCalendarCompressesElapsed(t *testing.T) {
	s := &Service{store: &memStore{procs: map[string]*Process{}, insts: map[string]*ProcessInstance{}}}
	s.SetCalendarResolver(func(ctx context.Context, tenantID, name string) (WorkCalendar, bool) {
		return &fakeCalendar{scale: 0.1}, true // 业务时间 = 墙钟的 1/10
	})
	step := &Step{ID: "approve", Type: StepUser, TimeoutDuration: "1h", TimeoutAction: "notify_only", Calendar: "8x5"}
	inst := newCalInst("approve", 10*time.Minute) // 墙钟 10 分钟, 业务 1 分钟

	s.checkUserStepTimeout(context.Background(), inst, step, time.Now())

	rec := inst.StepResults["approve"].(map[string]any)
	if rec["timeout_handled"] == true {
		t.Fatal("business elapsed (1min) < timeout (1h) should NOT trigger timeout")
	}
}

// 业务耗时达时限必须触发（墙钟未到也触发 —— 日历场景的核心价值）。
func TestUserTimeoutCalendarAccelerates(t *testing.T) {
	st := &memStore{procs: map[string]*Process{}, insts: map[string]*ProcessInstance{}}
	s := &Service{store: st}
	s.SetCalendarResolver(func(ctx context.Context, tenantID, name string) (WorkCalendar, bool) {
		return &fakeCalendar{scale: 10}, true // 业务时间 = 墙钟 x10
	})
	step := &Step{ID: "approve", Type: StepUser, TimeoutDuration: "30m", TimeoutAction: "notify_only", Calendar: "8x5"}
	inst := newCalInst("approve", 5*time.Minute) // 墙钟 5 分钟, 业务 50 分钟 > 30 分钟

	s.checkUserStepTimeout(context.Background(), inst, step, time.Now())

	rec := inst.StepResults["approve"].(map[string]any)
	if rec["timeout_handled"] != true {
		t.Fatal("business elapsed (50min) >= timeout (30m) SHOULD trigger timeout")
	}
}

// 未配置日历: 自然时间, 行为不变。
func TestUserTimeoutWallClockDefault(t *testing.T) {
	s := &Service{store: &memStore{procs: map[string]*Process{}, insts: map[string]*ProcessInstance{}}}
	step := &Step{ID: "approve", Type: StepUser, TimeoutDuration: "1h", TimeoutAction: "notify_only"}
	inst := newCalInst("approve", 10*time.Minute)
	s.checkUserStepTimeout(context.Background(), inst, step, time.Now())
	if rec := inst.StepResults["approve"].(map[string]any); rec["timeout_handled"] == true {
		t.Fatal("wall clock 10min < 1h should NOT trigger")
	}
}

// 日历解析失败: 回退自然时间, 不 panic。
func TestUserTimeoutCalendarUnresolvedFallback(t *testing.T) {
	s := &Service{store: &memStore{procs: map[string]*Process{}, insts: map[string]*ProcessInstance{}}}
	s.SetCalendarResolver(func(ctx context.Context, tenantID, name string) (WorkCalendar, bool) {
		return nil, false
	})
	step := &Step{ID: "approve", Type: StepUser, TimeoutDuration: "5m", TimeoutAction: "notify_only", Calendar: "missing"}
	inst := newCalInst("approve", 10*time.Minute) // 自然 10 分钟 > 5 分钟 → 触发
	s.checkUserStepTimeout(context.Background(), inst, step, time.Now())
	if rec := inst.StepResults["approve"].(map[string]any); rec["timeout_handled"] != true {
		t.Fatal("fallback to wall clock: 10min >= 5m SHOULD trigger")
	}
}
