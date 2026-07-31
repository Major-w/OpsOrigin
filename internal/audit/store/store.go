package store

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/audit/model"
)

type Store interface {
	Record(ctx context.Context, event *model.AuditEvent) error
	// Query 按过滤条件查询审计事件, 返回 (匹配事件列表, 符合条件的总条数, error)。
	// total 用于前端分页(不受 limit/offset 影响)。
	Query(ctx context.Context, filter model.AuditFilter) ([]*model.AuditEvent, int, error)
}

type MemoryStore struct {
	mu     sync.RWMutex
	events []*model.AuditEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{events: make([]*model.AuditEvent, 0)}
}

func (s *MemoryStore) Record(ctx context.Context, event *model.AuditEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	event.ID = uuid.New().String()
	event.Timestamp = time.Now()
	s.events = append(s.events, event)
	return nil
}

func (s *MemoryStore) Query(ctx context.Context, filter model.AuditFilter) ([]*model.AuditEvent, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var matched []*model.AuditEvent
	for _, e := range s.events {
		if !matchFilter(e, filter) {
			continue
		}
		matched = append(matched, e)
	}
	total := len(matched)

	// 时间倒序(与 PG 行为一致)
	for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
		matched[i], matched[j] = matched[j], matched[i]
	}

	// 分页
	start := filter.Offset
	if start < 0 {
		start = 0
	}
	if start >= len(matched) {
		return []*model.AuditEvent{}, total, nil
	}
	end := len(matched)
	if filter.Limit > 0 && start+filter.Limit < end {
		end = start + filter.Limit
	}
	return matched[start:end], total, nil
}

// matchFilter 逐条判断事件是否命中过滤条件(tenant/trace/event/actor/target/关键词/成功状态/时间)。
func matchFilter(e *model.AuditEvent, f model.AuditFilter) bool {
	if f.TenantID != "" && e.TenantID != f.TenantID {
		return false
	}
	if f.TraceID != "" && e.TraceID != f.TraceID {
		return false
	}
	if f.EventType != "" && e.EventType != f.EventType {
		return false
	}
	if f.ActorType != "" && string(e.Actor.Type) != f.ActorType {
		return false
	}
	if f.ActorID != "" && !containsFold(e.Actor.ID, f.ActorID) && !containsFold(e.Actor.DisplayName, f.ActorID) {
		return false
	}
	if f.TargetType != "" && e.Target.Type != f.TargetType {
		return false
	}
	if f.Keyword != "" {
		kw := f.Keyword
		if !containsFold(e.Action, kw) && !containsFold(e.Target.Name, kw) && !containsFold(e.TraceID, kw) && !containsFold(e.EventType, kw) {
			return false
		}
	}
	if f.Success != nil && e.Success != *f.Success {
		return false
	}
	if !f.From.IsZero() && e.Timestamp.Before(f.From) {
		return false
	}
	if !f.To.IsZero() && e.Timestamp.After(f.To) {
		return false
	}
	return true
}

// containsFold 大小写不敏感子串匹配(空 needle 视为命中)。
func containsFold(haystack, needle string) bool {
	if needle == "" {
		return true
	}
	return strings.Contains(strings.ToLower(haystack), strings.ToLower(needle))
}
