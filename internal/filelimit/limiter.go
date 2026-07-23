// Package filelimit 实现 §5 文件分发链路的限流与熔断。
//
// 设计目标:保护文件服务(Core 内部端点 + Proxy 文件代理)不被单租户/单 Agent
// 的突发下载打垮。采用 per-key(通常为 AgentID 或 file_id)令牌桶 + 连续失败熔断器。
//
// 与 internal/ai/ratelimit 的区别:本包语义中立(不绑定"AI Agent 暂停"日志),
// 专用于文件传输场景,默认阈值更宽松(下载是低频大流量操作)。
package filelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu      sync.Mutex
	window  time.Duration
	limit   int
	buckets map[string]*bucket
}

type bucket struct {
	tokens      []time.Time
	circuitOpen bool
	lastFail    time.Time
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
	DefaultLimit     = 30 // 单 key 每窗口最多 30 次下载请求(含 Range 续传)
	MaxConsecFails  = 5
	CircuitResetTime = 120 * time.Second
)

// New 构造限流器。limit<=0 或 window<=0 时使用默认值。
func New(limit int, window time.Duration) *Limiter {
	if limit <= 0 {
		limit = DefaultLimit
	}
	if window <= 0 {
		window = DefaultWindow
	}
	l := &Limiter{
		window:  window,
		limit:   limit,
		buckets: make(map[string]*bucket),
	}
	go l.reapLoop()
	return l
}

// reapLoop 定期清理无活动且未处于打开状态的 bucket,避免内存泄漏。
func (l *Limiter) reapLoop() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		l.reap()
	}
}

func (l *Limiter) reap() {
	l.mu.Lock()
	defer l.mu.Unlock()
	cutoff := time.Now().Add(-l.window)
	for id, b := range l.buckets {
		if b.circuitOpen {
			continue
		}
		recent := false
		for _, t := range b.tokens {
			if t.After(cutoff) {
				recent = true
				break
			}
		}
		if !recent {
			delete(l.buckets, id)
		}
	}
}

// Allow 判断是否放行本次请求。
//  - 熔断器打开且在重置窗口内:直接拒绝。
//  - 否则滑动窗口统计窗口内请求数,超过 limit 则拒绝(限流)。
func (l *Limiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.getOrCreateBucket(key)

	if b.circuitOpen {
		if time.Since(b.lastFail) > CircuitResetTime {
			b.circuitOpen = false
			b.consecFails = 0
			b.tokens = nil
		} else {
			return false
		}
	}

	now := time.Now()
	cutoff := now.Add(-l.window)
	var valid []time.Time
	for _, t := range b.tokens {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}
	b.tokens = valid

	if len(b.tokens) >= l.limit {
		return false
	}
	b.tokens = append(b.tokens, now)
	return true
}

// RecordFailure 记录一次失败,连续失败达到阈值则触发熔断。
func (l *Limiter) RecordFailure(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.getOrCreateBucket(key)
	b.consecFails++
	b.lastFail = time.Now()
	if b.consecFails >= MaxConsecFails {
		b.circuitOpen = true
	}
}

// RecordSuccess 记录一次成功,清零连续失败计数。
func (l *Limiter) RecordSuccess(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	b := l.getOrCreateBucket(key)
	b.consecFails = 0
}

func (l *Limiter) getOrCreateBucket(key string) *bucket {
	if b, ok := l.buckets[key]; ok {
		return b
	}
	b := &bucket{}
	l.buckets[key] = b
	return b
}

// GetState 返回指定 key 的熔断状态(用于可观测性)。
func (l *Limiter) GetState(key string) CircuitState {
	l.mu.Lock()
	defer l.mu.Unlock()
	b := l.getOrCreateBucket(key)
	if b.circuitOpen {
		return CircuitOpen
	}
	return CircuitClosed
}
