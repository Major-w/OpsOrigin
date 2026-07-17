package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/itsm/model"
)

var ErrNotFound = errors.New("ticket not found")

type Store interface {
	Create(ctx context.Context, ticket *model.Ticket) error
	Get(ctx context.Context, id, tenantID string) (*model.Ticket, error)
	Update(ctx context.Context, ticket *model.Ticket) error
	List(ctx context.Context, tenantID string, status model.TicketStatus) ([]*model.Ticket, error)
	AddEvent(ctx context.Context, event *model.TicketEvent) error
	GetEvents(ctx context.Context, ticketID, tenantID string) ([]*model.TicketEvent, error)
	GetStats(ctx context.Context, tenantID string) (*model.ITSMStats, error)
}

type MemoryStore struct {
	mu      sync.RWMutex
	tickets map[string]*model.Ticket
	events  map[string][]*model.TicketEvent
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		tickets: make(map[string]*model.Ticket),
		events:  make(map[string][]*model.TicketEvent),
	}
}

func (s *MemoryStore) Create(ctx context.Context, ticket *model.Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ticket.ID = uuid.New().String()
	now := time.Now()
	ticket.CreatedAt = now
	ticket.UpdatedAt = now

	s.tickets[ticket.ID] = ticket
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*model.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.tickets[id]
	if !ok {
		return nil, ErrNotFound
	}
	if t.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return t, nil
}

func (s *MemoryStore) Update(ctx context.Context, ticket *model.Ticket) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.tickets[ticket.ID]
	if !ok {
		return ErrNotFound
	}
	if existing.TenantID != ticket.TenantID {
		return ErrNotFound
	}

	ticket.UpdatedAt = time.Now()
	s.tickets[ticket.ID] = ticket
	return nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string, status model.TicketStatus) ([]*model.Ticket, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []*model.Ticket
	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		if status != "" && t.Status != status {
			continue
		}
		result = append(result, t)
	}
	return result, nil
}

func (s *MemoryStore) AddEvent(ctx context.Context, event *model.TicketEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	event.CreatedAt = time.Now()
	s.events[event.TicketID] = append(s.events[event.TicketID], event)
	return nil
}

func (s *MemoryStore) GetEvents(ctx context.Context, ticketID, tenantID string) ([]*model.TicketEvent, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	events, ok := s.events[ticketID]
	if !ok {
		return []*model.TicketEvent{}, nil
	}
	result := make([]*model.TicketEvent, len(events))
	copy(result, events)
	return result, nil
}

func (s *MemoryStore) GetStats(ctx context.Context, tenantID string) (*model.ITSMStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	stats := &model.ITSMStats{
		ByStatus: make(map[string]int),
		ByRisk:   make(map[string]int),
	}
	var approvedCount int
	var totalApprovalMs int64
	var approvedWithAt int
	var totalExecutionMs int64
	var executedWithAt int

	for _, t := range s.tickets {
		if t.TenantID != tenantID {
			continue
		}
		stats.Total++
		stats.ByStatus[string(t.Status)]++
		stats.ByRisk[string(t.RiskLevel)]++
		if t.Status == model.StatusApproved {
			approvedCount++
		}
		if t.ApprovedAt != nil && t.CreatedAt.Before(*t.ApprovedAt) {
			totalApprovalMs += t.ApprovedAt.Sub(t.CreatedAt).Milliseconds()
			approvedWithAt++
		}
		if t.ExecutedAt != nil && t.ApprovedAt != nil && t.ExecutedAt.After(*t.ApprovedAt) {
			totalExecutionMs += t.ExecutedAt.Sub(*t.ApprovedAt).Milliseconds()
			executedWithAt++
		}
	}

	if stats.Total > 0 {
		stats.ApprovalRate = float64(approvedCount) / float64(stats.Total)
	}
	if approvedWithAt > 0 {
		stats.AvgApprovalMs = totalApprovalMs / int64(approvedWithAt)
	}
	if executedWithAt > 0 {
		stats.AvgExecutionMs = totalExecutionMs / int64(executedWithAt)
	}
	return stats, nil
}
