package store

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
)

// ErrNotFound 扫描/发现项不存在。
var ErrNotFound = errors.New("scan not found")

// Store 漏洞扫描存储接口；内存兜底与 PG 实现均满足此契约。
type Store interface {
	CreateScan(ctx context.Context, scan *model.Scan) error
	UpdateScan(ctx context.Context, scan *model.Scan) error
	GetScan(ctx context.Context, id, tenantID string) (*model.Scan, error)
	ListScans(ctx context.Context, tenantID string) ([]*model.Scan, error)
	DeleteScan(ctx context.Context, id, tenantID string) error
	CreateFindings(ctx context.Context, findings []*model.Finding) error
	ListFindings(ctx context.Context, scanID, tenantID string) ([]*model.Finding, error)
	ListFindingsAll(ctx context.Context, tenantID string) ([]*model.Finding, error)
	GetStats(ctx context.Context, tenantID string) (*model.VulnStats, error)
}

// MemoryStore 内存兜底实现，按 tenant_id 隔离 + mutex 保护。
type MemoryStore struct {
	mu       sync.RWMutex
	scans    map[string]*model.Scan
	findings []*model.Finding
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{scans: make(map[string]*model.Scan)}
}

func (s *MemoryStore) CreateScan(ctx context.Context, scan *model.Scan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	scan.ID = uuid.New().String()
	now := time.Now()
	scan.CreatedAt = now
	if scan.SeverityCounts == nil {
		scan.SeverityCounts = map[string]int{}
	}
	s.scans[scan.ID] = scan
	return nil
}

func (s *MemoryStore) UpdateScan(ctx context.Context, scan *model.Scan) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.scans[scan.ID]
	if !ok || existing.TenantID != scan.TenantID {
		return ErrNotFound
	}
	s.scans[scan.ID] = scan
	return nil
}

func (s *MemoryStore) GetScan(ctx context.Context, id, tenantID string) (*model.Scan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sc, ok := s.scans[id]
	if !ok || sc.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return sc, nil
}

func (s *MemoryStore) ListScans(ctx context.Context, tenantID string) ([]*model.Scan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Scan
	for _, sc := range s.scans {
		if sc.TenantID == tenantID {
			out = append(out, sc)
		}
	}
	return out, nil
}

func (s *MemoryStore) DeleteScan(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.scans[id]
	if !ok || sc.TenantID != tenantID {
		return ErrNotFound
	}
	delete(s.scans, id)
	var kept []*model.Finding
	for _, f := range s.findings {
		if f.ScanID == id {
			continue
		}
		kept = append(kept, f)
	}
	s.findings = kept
	return nil
}

func (s *MemoryStore) CreateFindings(ctx context.Context, findings []*model.Finding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, f := range findings {
		if f.ID == "" {
			f.ID = uuid.New().String()
		}
		if f.CreatedAt.IsZero() {
			f.CreatedAt = now
		}
		s.findings = append(s.findings, f)
	}
	return nil
}

func (s *MemoryStore) ListFindings(ctx context.Context, scanID, tenantID string) ([]*model.Finding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Finding
	for _, f := range s.findings {
		if f.ScanID == scanID && f.TenantID == tenantID {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *MemoryStore) ListFindingsAll(ctx context.Context, tenantID string) ([]*model.Finding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Finding
	for _, f := range s.findings {
		if f.TenantID == tenantID {
			out = append(out, f)
		}
	}
	return out, nil
}

func (s *MemoryStore) GetStats(ctx context.Context, tenantID string) (*model.VulnStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := &model.VulnStats{
		BySeverity: map[string]int{},
		ByStatus:   map[string]int{},
	}
	for _, sc := range s.scans {
		if sc.TenantID != tenantID {
			continue
		}
		stats.TotalScans++
		stats.ByStatus[string(sc.Status)]++
		stats.TotalFindings += sc.TotalFindings
		for k, v := range sc.SeverityCounts {
			stats.BySeverity[k] += v
		}
	}
	return stats, nil
}
