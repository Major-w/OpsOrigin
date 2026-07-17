package ratelimit

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	redisRateLimitPrefix   = "ratelimit:%s"
	redisCircuitStateKey   = "circuit:%s:state"
	redisCircuitCountKey   = "circuit:%s:fails"
	redisCircuitLastFail   = "circuit:%s:last_fail"
)

type RedisLimiter struct {
	client  *redis.Client
	window  time.Duration
	limit   int
}

func NewRedisLimiter(client *redis.Client) *RedisLimiter {
	return &RedisLimiter{
		client: client,
		window: DefaultWindow,
		limit:  DefaultLimit,
	}
}

func (l *RedisLimiter) Allow(agentID string) bool {
	return l.AllowContext(context.Background(), agentID)
}

func (l *RedisLimiter) AllowContext(ctx context.Context, agentID string) bool {
	stateKey := fmt.Sprintf(redisCircuitStateKey, agentID)
	countKey := fmt.Sprintf(redisCircuitCountKey, agentID)
	lastFailKey := fmt.Sprintf(redisCircuitLastFail, agentID)

	state, err := l.client.Get(ctx, stateKey).Result()
	if err != nil && err != redis.Nil {
		slog.Error("redis circuit check error", "agent_id", agentID, "error", err)
		return l.fallbackAllow(agentID)
	}

	if state == "open" {
		lastFailStr, err := l.client.Get(ctx, lastFailKey).Result()
		if err == nil {
			lastFail, parseErr := time.Parse(time.RFC3339Nano, lastFailStr)
			if parseErr == nil && time.Since(lastFail) > CircuitResetTime {
				l.client.Del(ctx, stateKey, countKey, lastFailKey)
				slog.Warn("circuit breaker auto-reset (half-open)", "agent_id", agentID)
			} else {
				slog.Warn("circuit breaker open, request blocked", "agent_id", agentID)
				return false
			}
		} else {
			l.client.Del(ctx, stateKey, countKey, lastFailKey)
		}
	}

	key := fmt.Sprintf(redisRateLimitPrefix, agentID)
	now := time.Now()
	cutoff := now.Add(-l.window)

	pipe := l.client.Pipeline()
	pipe.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", cutoff.UnixNano()))
	pipe.ZCard(ctx, key)
	pipe.ZAdd(ctx, key, redis.Z{
		Score:  float64(now.UnixNano()),
		Member: fmt.Sprintf("%d:%s", now.UnixNano(), agentID),
	})
	pipe.Expire(ctx, key, l.window*2)

	cmders, err := pipe.Exec(ctx)
	if err != nil {
		slog.Error("redis rate limit error", "agent_id", agentID, "error", err)
		return l.fallbackAllow(agentID)
	}

	if len(cmders) >= 2 {
		cardCmd := cmders[1].(*redis.IntCmd)
		count, err := cardCmd.Result()
		if err == nil && int(count) > l.limit {
			slog.Warn("rate limit exceeded",
				"agent_id", agentID,
				"count", count,
				"limit", l.limit,
				"window", l.window)
			return false
		}
	}

	return true
}

func (l *RedisLimiter) RecordFailure(agentID string) {
	l.RecordFailureContext(context.Background(), agentID)
}

func (l *RedisLimiter) RecordFailureContext(ctx context.Context, agentID string) {
	stateKey := fmt.Sprintf(redisCircuitStateKey, agentID)
	countKey := fmt.Sprintf(redisCircuitCountKey, agentID)
	lastFailKey := fmt.Sprintf(redisCircuitLastFail, agentID)

	count, err := l.client.Incr(ctx, countKey).Result()
	if err != nil {
		slog.Error("redis circuit count error", "agent_id", agentID, "error", err)
		return
	}

	if count == 1 {
		l.client.Expire(ctx, countKey, CircuitResetTime*2)
	}

	now := time.Now().Format(time.RFC3339Nano)
	l.client.Set(ctx, lastFailKey, now, CircuitResetTime*2)

	if count >= MaxConsecFails {
		l.client.Set(ctx, stateKey, "open", CircuitResetTime)
		slog.Error("circuit breaker OPEN - AI agent suspended",
			"agent_id", agentID,
			"consecutive_fails", count,
			"reset_in", CircuitResetTime)
	}
}

func (l *RedisLimiter) RecordSuccess(agentID string) {
	l.RecordSuccessContext(context.Background(), agentID)
}

func (l *RedisLimiter) RecordSuccessContext(ctx context.Context, agentID string) {
	countKey := fmt.Sprintf(redisCircuitCountKey, agentID)
	stateKey := fmt.Sprintf(redisCircuitStateKey, agentID)

	l.client.Del(ctx, countKey)
	l.client.Del(ctx, stateKey)
}

func (l *RedisLimiter) GetState(agentID string) CircuitState {
	stateKey := fmt.Sprintf(redisCircuitStateKey, agentID)
	state, err := l.client.Get(context.Background(), stateKey).Result()
	if err != nil {
		return CircuitClosed
	}
	if state == "open" {
		return CircuitOpen
	}
	return CircuitClosed
}

func (l *RedisLimiter) GetCount(agentID string) int {
	key := fmt.Sprintf(redisRateLimitPrefix, agentID)
	ctx := context.Background()

	cutoff := time.Now().Add(-l.window)
	l.client.ZRemRangeByScore(ctx, key, "0", fmt.Sprintf("%d", cutoff.UnixNano()))

	count, err := l.client.ZCard(ctx, key).Result()
	if err != nil {
		slog.Error("redis get count error", "agent_id", agentID, "error", err)
		return 0
	}
	return int(count)
}

func (l *RedisLimiter) fallbackAllow(agentID string) bool {
	slog.Warn("redis unavailable, falling back to allow", "agent_id", agentID)
	return true
}
