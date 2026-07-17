package monitor

import (
	"context"
	"sync"
)

type SimpleTopology struct {
	mu       sync.RWMutex
	parents  map[string]string
	children map[string][]string
	tenants  map[string]string
}

func NewSimpleTopology() *SimpleTopology {
	return &SimpleTopology{
		parents:  make(map[string]string),
		children: make(map[string][]string),
		tenants:  make(map[string]string),
	}
}

func (t *SimpleTopology) SetParent(ctx context.Context, ciID, parentID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.parents[ciID] = parentID
	t.children[parentID] = append(t.children[parentID], ciID)
}

func (t *SimpleTopology) VerifyTenant(ctx context.Context, ciID, tenantID string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	tid, ok := t.tenants[ciID]
	if !ok {
		return false
	}
	return tid == tenantID
}

func (t *SimpleTopology) RecordTenant(ctx context.Context, ciID, tenantID string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.tenants[ciID] = tenantID
}

func (t *SimpleTopology) GetParentCI(ctx context.Context, ciID string) (string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.parents[ciID], nil
}

func (t *SimpleTopology) GetChildrenCI(ctx context.Context, ciID string) ([]string, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.children[ciID], nil
}

func (t *SimpleTopology) IsAncestorOf(ctx context.Context, ancestorID, descendantID string) (bool, error) {
	t.mu.RLock()
	defer t.mu.RUnlock()

	current := descendantID
	for {
		parent, ok := t.parents[current]
		if !ok {
			return false, nil
		}
		if parent == ancestorID {
			return true, nil
		}
		current = parent
	}
}
