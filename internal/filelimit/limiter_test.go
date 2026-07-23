package filelimit

import (
	"testing"
	"time"
)

func TestAllow_WithinLimit(t *testing.T) {
	l := New(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("agent-1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if l.Allow("agent-1") {
		t.Fatal("4th request should be rate-limited")
	}
	// 不同 key 互不影响
	if !l.Allow("agent-2") {
		t.Fatal("different key should not be limited")
	}
}

func TestAllow_CircuitBreaker(t *testing.T) {
	l := New(100, time.Minute)
	for i := 0; i < MaxConsecFails; i++ {
		l.RecordFailure("agent-x")
	}
	if l.GetState("agent-x") != CircuitOpen {
		t.Fatal("circuit should be open after max consecutive fails")
	}
	if l.Allow("agent-x") {
		t.Fatal("request should be blocked while circuit is open")
	}
}

func TestAllow_CircuitReset(t *testing.T) {
	l := New(100, time.Minute)
	for i := 0; i < MaxConsecFails; i++ {
		l.RecordFailure("agent-y")
	}
	if l.GetState("agent-y") != CircuitOpen {
		t.Fatal("expected open")
	}
	// 模拟熔断窗口过去
	b := l.GetState("agent-y")
	_ = b
	// 直接借助 Allow 的自动重置逻辑:把 lastFail 推到窗口前
	l.mu.Lock()
	if bk, ok := l.buckets["agent-y"]; ok {
		bk.lastFail = time.Now().Add(-(CircuitResetTime + time.Second))
	}
	l.mu.Unlock()
	if !l.Allow("agent-y") {
		t.Fatal("circuit should reset after window and allow request")
	}
}

func TestRecordSuccess_ClearsFails(t *testing.T) {
	l := New(100, time.Minute)
	l.RecordFailure("agent-z")
	l.RecordFailure("agent-z")
	l.RecordSuccess("agent-z")
	// 再次失败不应触发熔断(计数已清零)
	l.RecordFailure("agent-z")
	if l.GetState("agent-z") == CircuitOpen {
		t.Fatal("success should clear consecutive fails")
	}
}
