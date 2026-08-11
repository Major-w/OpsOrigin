package message

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemStore 内存实现,用于无数据库场景与单元测试。
type MemStore struct {
	mu   sync.RWMutex
	msgs []*Message
	// max 保留上限,超出后丢弃最旧消息,防止长跑进程内存无限增长。
	max int
}

func NewMemStore() *MemStore {
	return &MemStore{max: 5000}
}

func (s *MemStore) Insert(ctx context.Context, msgs ...*Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range msgs {
		if m == nil {
			continue
		}
		normalize(m)
		cp := *m
		s.msgs = append(s.msgs, &cp)
	}
	if s.max > 0 && len(s.msgs) > s.max {
		s.msgs = s.msgs[len(s.msgs)-s.max:]
	}
	return nil
}

func (s *MemStore) filter(q ListQuery) []*Message {
	var out []*Message
	for _, m := range s.msgs {
		if m.TenantID != q.TenantID {
			continue
		}
		if !q.Recipient.Matches(m) {
			continue
		}
		if q.Category != "" && string(m.Category) != q.Category {
			continue
		}
		if q.OnlyUnread && m.Read {
			continue
		}
		out = append(out, m)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

func (s *MemStore) List(ctx context.Context, q ListQuery) ([]*Message, int, error) {
	q = q.normalized()
	s.mu.RLock()
	defer s.mu.RUnlock()

	all := s.filter(q)
	total := len(all)
	if q.Offset >= total {
		return []*Message{}, total, nil
	}
	end := q.Offset + q.Limit
	if end > total {
		end = total
	}
	page := make([]*Message, 0, end-q.Offset)
	for _, m := range all[q.Offset:end] {
		cp := *m
		page = append(page, &cp)
	}
	return page, total, nil
}

func (s *MemStore) UnreadCount(ctx context.Context, tenantID string, r Recipient) (int, error) {
	q := ListQuery{TenantID: tenantID, Recipient: r, OnlyUnread: true}.normalized()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.filter(q)), nil
}

func (s *MemStore) MarkRead(ctx context.Context, tenantID, id string, r Recipient) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, m := range s.msgs {
		if m.ID == id && m.TenantID == tenantID && r.Matches(m) && !m.Read {
			m.Read = true
			m.ReadAt = &now
		}
	}
	return nil
}

func (s *MemStore) MarkAllRead(ctx context.Context, tenantID string, r Recipient) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	n := 0
	for _, m := range s.msgs {
		if m.TenantID == tenantID && r.Matches(m) && !m.Read {
			m.Read = true
			m.ReadAt = &now
			n++
		}
	}
	return n, nil
}

func (s *MemStore) Delete(ctx context.Context, tenantID, id string, r Recipient) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.msgs[:0]
	for _, m := range s.msgs {
		if m.ID == id && m.TenantID == tenantID && r.Matches(m) {
			continue
		}
		out = append(out, m)
	}
	s.msgs = out
	return nil
}

// normalize 补齐消息的必填缺省值,让投递方可以只填业务字段。
func normalize(m *Message) {
	if m.ID == "" {
		m.ID = uuid.New().String()
	}
	if strings.TrimSpace(m.TenantID) == "" {
		m.TenantID = "default"
	}
	if m.Category == "" {
		m.Category = CategorySystem
	}
	if m.Level == "" {
		m.Level = LevelInfo
	}
	if m.CreatedAt.IsZero() {
		m.CreatedAt = time.Now()
	}
}
