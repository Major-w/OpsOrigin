package store

import (
	"context"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/audit/model"
)

type Store interface {
	Record(ctx context.Context, event *model.AuditEvent) error
	Query(ctx context.Context, filter model.AuditFilter) ([]*model.AuditEvent, error)
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

func (s *MemoryStore) Query(ctx context.Context, filter model.AuditFilter) ([]*model.AuditEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*model.AuditEvent
	for _, e := range s.events {
		if filter.TenantID != "" && e.TenantID != filter.TenantID {
			continue
		}
		if filter.TraceID != "" && e.TraceID != filter.TraceID {
			continue
		}
		if filter.EventType != "" && e.EventType != filter.EventType {
			continue
		}
		if filter.ActorType != "" && string(e.Actor.Type) != filter.ActorType {
			continue
		}
		if !filter.From.IsZero() && e.Timestamp.Before(filter.From) {
			continue
		}
		if !filter.To.IsZero() && e.Timestamp.After(filter.To) {
			continue
		}
		result = append(result, e)
	}

	if filter.Limit > 0 && len(result) > filter.Limit {
		result = result[:filter.Limit]
	}
	if result == nil {
		result = []*model.AuditEvent{}
	}

	return result, nil
}
