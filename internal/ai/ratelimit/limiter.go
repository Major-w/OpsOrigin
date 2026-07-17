package ratelimit

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

type RateLimiter interface {
	Allow(agentID string) bool
	RecordFailure(agentID string)
	RecordSuccess(agentID string)
	GetState(agentID string) CircuitState
	GetCount(agentID string) int
}

type ContextRateLimiter interface {
	RateLimiter
	AllowContext(ctx context.Context, agentID string) bool
	RecordFailureContext(ctx context.Context, agentID string)
	RecordSuccessContext(ctx context.Context, agentID string)
}

type Limiter struct {
	mu       sync.Mutex
	window   time.Duration
	limit    int
	buckets  map[string]*tokenBucket
}

type tokenBucket struct {
	tokens    []time.Time
	circuitOpen bool
	lastFail   time.Time
	consecFails int
}

type CircuitState string

const (
	CircuitClosed   CircuitState = "closed"
	CircuitOpen     CircuitState = "open"
	CircuitHalfOpen CircuitState = "half_open"
)

const (
	DefaultWindow    = 60 * time.Second
	DefaultLimit     = 10
	MaxConsecFails   = 3
	CircuitResetTime = 120 * time.Second
)

func New() *Limiter {
	return &Limiter{
		window:  DefaultWindow,
		limit:   DefaultLimit,
		buckets: make(map[string]*tokenBucket),
	}
}

func (l *Limiter) Allow(agentID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.getOrCreateBucket(agentID)

	if bucket.circuitOpen {
		if time.Since(bucket.lastFail) > CircuitResetTime {
			bucket.circuitOpen = false
			bucket.consecFails = 0
			bucket.tokens = nil
			slog.Warn("circuit breaker reset (half-open)", "agent_id", agentID)
		} else {
			slog.Warn("circuit breaker open, request blocked", "agent_id", agentID)
			return false
		}
	}

	now := time.Now()
	cutoff := now.Add(-l.window)

	var valid []time.Time
	for _, t := range bucket.tokens {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	bucket.tokens = valid

	if len(bucket.tokens) >= l.limit {
		slog.Warn("rate limit exceeded",
			"agent_id", agentID,
			"count", len(bucket.tokens),
			"limit", l.limit,
			"window", l.window)
		return false
	}

	bucket.tokens = append(bucket.tokens, now)
	return true
}

func (l *Limiter) RecordFailure(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.getOrCreateBucket(agentID)

	bucket.consecFails++
	bucket.lastFail = time.Now()

	if bucket.consecFails >= MaxConsecFails {
		bucket.circuitOpen = true
		slog.Error("circuit breaker OPEN - AI agent suspended",
			"agent_id", agentID,
			"consecutive_fails", bucket.consecFails,
			"reset_in", CircuitResetTime)
	}
}

func (l *Limiter) RecordSuccess(agentID string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.getOrCreateBucket(agentID)
	bucket.consecFails = 0
}

func (l *Limiter) getOrCreateBucket(agentID string) *tokenBucket {
	if bucket, ok := l.buckets[agentID]; ok {
		return bucket
	}
	bucket := &tokenBucket{}
	l.buckets[agentID] = bucket
	return bucket
}

func (l *Limiter) GetState(agentID string) CircuitState {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.getOrCreateBucket(agentID)
	if bucket.circuitOpen {
		return CircuitOpen
	}
	return CircuitClosed
}

func (l *Limiter) GetCount(agentID string) int {
	l.mu.Lock()
	defer l.mu.Unlock()

	bucket := l.getOrCreateBucket(agentID)

	now := time.Now()
	cutoff := now.Add(-l.window)
	count := 0
	for _, t := range bucket.tokens {
		if t.After(cutoff) {
			count++
		}
	}
	return count
}
