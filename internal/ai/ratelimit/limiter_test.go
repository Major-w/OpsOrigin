package ratelimit

import (
	"testing"
	"time"
)

func TestLimiter_WithinLimit(t *testing.T) {
	l := New()
	for i := 0; i < DefaultLimit; i++ {
		if !l.Allow("agent1") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if l.Allow("agent1") {
		t.Fatal("request beyond limit should be blocked")
	}
}

func TestLimiter_CircuitOpensAfterFails(t *testing.T) {
	l := New()
	for i := 0; i < MaxConsecFails; i++ {
		l.RecordFailure("agent2")
	}
	if l.GetState("agent2") != CircuitOpen {
		t.Fatal("circuit should be open after max consecutive fails")
	}
	if l.Allow("agent2") {
		t.Fatal("requests must be blocked while circuit is open")
	}
}

func TestLimiter_CircuitHalfOpenAfterReset(t *testing.T) {
	l := New()
	for i := 0; i < MaxConsecFails; i++ {
		l.RecordFailure("agent3")
	}
	// simulate the reset window elapsing
	l.buckets["agent3"].lastFail = time.Now().Add(-(CircuitResetTime + time.Second))
	if !l.Allow("agent3") {
		t.Fatal("circuit should allow a trial request after the reset window")
	}
}

func TestLimiter_ReapRemovesIdleBuckets(t *testing.T) {
	l := New()
	l.Allow("idle-agent")
	// simulate all tokens expired
	l.buckets["idle-agent"].tokens = []time.Time{time.Now().Add(-2 * DefaultWindow)}
	l.reap()
	if _, ok := l.buckets["idle-agent"]; ok {
		t.Fatal("idle bucket should have been reaped")
	}

	// an active bucket must be retained
	l.Allow("active-agent")
	l.reap()
	if _, ok := l.buckets["active-agent"]; !ok {
		t.Fatal("active bucket should be retained")
	}

	// an open circuit must be retained even if idle
	l.RecordFailure("open-agent")
	for i := 0; i < MaxConsecFails; i++ {
		l.RecordFailure("open-agent")
	}
	l.buckets["open-agent"].tokens = []time.Time{time.Now().Add(-2 * DefaultWindow)}
	l.reap()
	if _, ok := l.buckets["open-agent"]; !ok {
		t.Fatal("open-circuit bucket should be retained for state visibility")
	}
}
