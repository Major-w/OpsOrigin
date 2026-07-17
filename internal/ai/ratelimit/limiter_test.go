package ratelimit

import (
	"fmt"
	"testing"
)

func TestLimiter_Allow(t *testing.T) {
	l := New()
	agentID := "test-agent-01"

	for i := 0; i < DefaultLimit; i++ {
		if !l.Allow(agentID) {
			t.Errorf("expected allow at call %d", i+1)
		}
	}

	if l.Allow(agentID) {
		t.Error("expected deny after rate limit exceeded")
	}
}

func TestLimiter_GetState(t *testing.T) {
	l := New()
	agentID := "test-agent-02"

	if s := l.GetState(agentID); s != CircuitClosed {
		t.Errorf("expected closed, got %s", s)
	}

	for i := 0; i < MaxConsecFails; i++ {
		l.RecordFailure(agentID)
	}

	if s := l.GetState(agentID); s != CircuitOpen {
		t.Errorf("expected open after consecutive failures, got %s", s)
	}
}

func TestLimiter_GetCount(t *testing.T) {
	l := New()
	agentID := "test-agent-03"

	for i := 0; i < 5; i++ {
		l.Allow(agentID)
	}

	if c := l.GetCount(agentID); c != 5 {
		t.Errorf("expected count 5, got %d", c)
	}
}

func TestLimiter_RecordSuccess(t *testing.T) {
	l := New()
	agentID := "test-agent-04"

	l.RecordFailure(agentID)
	l.RecordFailure(agentID)
	l.RecordSuccess(agentID)

	bucket := l.buckets[agentID]
	if bucket.consecFails != 0 {
		t.Errorf("expected consecFails 0 after success, got %d", bucket.consecFails)
	}
}

func TestLimiter_Concurrent(t *testing.T) {
	l := New()
	done := make(chan bool, 20)

	for i := 0; i < 20; i++ {
		go func(id int) {
			agentID := fmt.Sprintf("agent-%d", id)
			l.Allow(agentID)
			done <- true
		}(i)
	}

	for i := 0; i < 20; i++ {
		<-done
	}
}
