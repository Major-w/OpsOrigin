package store

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/user/model"
)

type MemoryStore struct {
	mu    sync.RWMutex
	users map[string]*model.User
}

func NewMemoryStore() *MemoryStore {
	s := &MemoryStore{
		users: make(map[string]*model.User),
	}
	s.seed()
	return s
}

func (s *MemoryStore) seed() {
	now := time.Now()
	for _, u := range []model.User{
		{ID: uuid.NewString(), TenantID: "default", Username: "admin", Password: "admin123", Role: model.RoleAdmin, Status: "active", CreatedAt: now, UpdatedAt: now},
		{ID: uuid.NewString(), TenantID: "default", Username: "ops", Password: "ops123", Role: model.RoleOperator, Status: "active", CreatedAt: now, UpdatedAt: now},
	} {
		s.users[u.Username] = &u
	}
}

func (s *MemoryStore) Create(u *model.User) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.users[u.Username]; exists {
		return fmt.Errorf("user %q already exists", u.Username)
	}
	s.users[u.Username] = u
	return nil
}

func (s *MemoryStore) GetByUsername(username string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.users[username]
	if !ok {
		return nil, fmt.Errorf("user %q not found", username)
	}
	return u, nil
}

func (s *MemoryStore) GetByID(id string) (*model.User, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, u := range s.users {
		if u.ID == id {
			return u, nil
		}
	}
	return nil, fmt.Errorf("user %q not found", id)
}

func (s *MemoryStore) List() []*model.User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]*model.User, 0, len(s.users))
	for _, u := range s.users {
		result = append(result, u)
	}
	return result
}
