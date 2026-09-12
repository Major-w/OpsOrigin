package store

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/vuln/model"
)

// ErrNotFound 扫描/发现项不存在。
var ErrNotFound = errors.New("scan not found")

// ScanFilter 扫描列表过滤与分页。
type ScanFilter struct {
	Status string // running/completed/failed/partial,空=全部
	Type   string
	Limit  int // <=0 视为不分页(上限 500)
	Offset int
}

// FindingFilter 发现项列表过滤与分页。
type FindingFilter struct {
	Severity string // critical/high/medium/low
	Status   string // open/fixed/ignored/false_positive
	Host     string // 资产精确匹配
	Search   string // CVE/包名/标题模糊匹配
	Limit    int
	Offset   int
}

// FindingUpdate 发现项处置更新的字段集合(nil = 不改动)。
type FindingUpdate struct {
	Status      *model.FindingStatus
	Note        *string
	IgnoreUntil *time.Time
	// ClearIgnore 显式把 ignore_until 置空(与「传 nil 表示不改」区分开)。
	ClearIgnore bool
}

// Store 漏洞扫描存储接口；内存兜底与 PG 实现均满足此契约。
type Store interface {
	CreateScan(ctx context.Context, scan *model.Scan) error
	UpdateScan(ctx context.Context, scan *model.Scan) error
	GetScan(ctx context.Context, id, tenantID string) (*model.Scan, error)
	ListScans(ctx context.Context, tenantID string, f ScanFilter) ([]*model.Scan, int, error)
	// ListDueScans 跨租户返回到期的计划扫描(后台调度专用:无用户上下文)。
	ListDueScans(ctx context.Context, now time.Time) ([]*model.Scan, error)
	DeleteScan(ctx context.Context, id, tenantID string) error

	// UpsertFindings 以 (tenant_id, dedupe_key) 为唯一实例写入:命中已有实例则更新
	// last_seen/occurrences 并保留 first_seen;被抑制(忽略/误报未过期)的保持抑制。
	UpsertFindings(ctx context.Context, findings []*model.Finding) error
	ListFindings(ctx context.Context, scanID, tenantID string, f FindingFilter) ([]*model.Finding, int, error)
	ListFindingsAll(ctx context.Context, tenantID string, f FindingFilter) ([]*model.Finding, int, error)
	GetFinding(ctx context.Context, id, tenantID string) (*model.Finding, error)
	UpdateFinding(ctx context.Context, id, tenantID string, upd FindingUpdate) (*model.Finding, error)
	// CloseMissedFindings 复扫验证:把 hosts 范围内容 lastSeen 早于 before 的 open 项置为 fixed。
	// hosts 为空表示「无覆盖信息」,直接返回 0(不做任何关闭,避免误判为已修复)。
	CloseMissedFindings(ctx context.Context, tenantID string, hosts []string, before time.Time) (int, error)
	GetStats(ctx context.Context, tenantID string) (*model.VulnStats, error)
}

// filterScans 内存实现的扫描过滤/排序/分页。
func filterScans(all []*model.Scan, f ScanFilter) ([]*model.Scan, int) {
	out := make([]*model.Scan, 0, len(all))
	for _, sc := range all {
		if f.Status != "" && string(sc.Status) != f.Status {
			continue
		}
		if f.Type != "" && string(sc.Type) != f.Type {
			continue
		}
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	total := len(out)
	out = pageScans(out, f.Limit, f.Offset)
	return out, total
}

func pageScans(in []*model.Scan, limit, offset int) []*model.Scan {
	if offset < 0 {
		offset = 0
	}
	if offset >= len(in) {
		return []*model.Scan{}
	}
	in = in[offset:]
	if limit <= 0 {
		limit = 500
	}
	if limit > len(in) {
		limit = len(in)
	}
	return in[:limit]
}

// matchFinding 内存实现的发现项过滤。
func matchFinding(f *model.Finding, fl FindingFilter) bool {
	if fl.Severity != "" && string(f.Severity) != fl.Severity {
		return false
	}
	if fl.Status != "" && string(f.Status) != fl.Status {
		return false
	}
	if fl.Host != "" && f.Host != fl.Host {
		return false
	}
	if fl.Search != "" {
		s := strings.ToLower(fl.Search)
		hit := strings.Contains(strings.ToLower(f.CVE), s) ||
			strings.Contains(strings.ToLower(f.Affected), s) ||
			strings.Contains(strings.ToLower(f.Title), s)
		if !hit {
			return false
		}
	}
	return true
}

// MemoryStore 内存兜底实现，按 tenant_id 隔离 + mutex 保护。
type MemoryStore struct {
	mu       sync.RWMutex
	scans    map[string]*model.Scan
	findings map[string]*model.Finding // id -> finding
}

// NewMemoryStore 构造内存存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		scans:    make(map[string]*model.Scan),
		findings: make(map[string]*model.Finding),
	}
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
	if scan.Status == "" {
		scan.Status = model.ScanStatusRunning
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

func (s *MemoryStore) ListScans(ctx context.Context, tenantID string, f ScanFilter) ([]*model.Scan, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	all := make([]*model.Scan, 0, len(s.scans))
	for _, sc := range s.scans {
		if sc.TenantID == tenantID {
			all = append(all, sc)
		}
	}
	out, total := filterScans(all, f)
	return out, total, nil
}

func (s *MemoryStore) ListDueScans(ctx context.Context, now time.Time) ([]*model.Scan, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*model.Scan
	for _, sc := range s.scans {
		if sc.Schedule == "" || sc.Schedule == model.ScheduleOnce {
			continue
		}
		if sc.Status == model.ScanStatusRunning {
			continue
		}
		if sc.NextRunAt == nil || sc.NextRunAt.After(now) {
			continue
		}
		out = append(out, sc)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].NextRunAt == nil || out[j].NextRunAt == nil {
			return false
		}
		return out[i].NextRunAt.Before(*out[j].NextRunAt)
	})
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
	for fid, f := range s.findings {
		if f.ScanID == id {
			delete(s.findings, fid)
		}
	}
	return nil
}

func (s *MemoryStore) UpsertFindings(ctx context.Context, findings []*model.Finding) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	// 先建 (tenant|dedupe) -> id 索引,保证同一批次内也不重复插入。
	idx := make(map[string]string, len(s.findings))
	for id, f := range s.findings {
		idx[f.TenantID+"|"+f.DedupeKey] = id
	}
	for _, f := range findings {
		if f.DedupeKey == "" {
			f.DedupeKey = f.ID
		}
		key := f.TenantID + "|" + f.DedupeKey
		if existID, ok := idx[key]; ok {
			old := s.findings[existID]
			if old.Suppressed(now) {
				// 抑制期内的忽略/误报:不重新打开,仅刷新最后出现时间。
				old.LastSeen = f.LastSeen
				old.Occurrences++
				continue
			}
			f.ID = old.ID
			f.FirstSeen = old.FirstSeen
			f.Occurrences = old.Occurrences + 1
			f.Status = model.FindingOpen
			s.findings[f.ID] = f
			continue
		}
		if f.ID == "" {
			f.ID = uuid.New().String()
		}
		if f.FirstSeen.IsZero() {
			f.FirstSeen = now
		}
		if f.LastSeen.IsZero() {
			f.LastSeen = now
		}
		if f.CreatedAt.IsZero() {
			f.CreatedAt = now
		}
		if f.Status == "" {
			f.Status = model.FindingOpen
		}
		s.findings[f.ID] = f
		idx[key] = f.ID
	}
	return nil
}

func (s *MemoryStore) listFindingsFiltered(scanID, tenantID string, f FindingFilter) ([]*model.Finding, int) {
	var out []*model.Finding
	for _, fd := range s.findings {
		if fd.TenantID != tenantID {
			continue
		}
		if scanID != "" && fd.ScanID != scanID {
			continue
		}
		if !matchFinding(fd, f) {
			continue
		}
		out = append(out, fd)
	}
	// 排序:级别优先(严重在前),同级按 CVSS 降序,再按最近出现时间。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank > out[j].Rank
		}
		if out[i].CVSS != out[j].CVSS {
			return out[i].CVSS > out[j].CVSS
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	total := len(out)
	if f.Offset > 0 {
		if f.Offset >= len(out) {
			return []*model.Finding{}, total
		}
		out = out[f.Offset:]
	}
	limit := f.Limit
	if limit <= 0 {
		limit = 200
	}
	if limit > len(out) {
		limit = len(out)
	}
	return out[:limit], total
}

func (s *MemoryStore) ListFindings(ctx context.Context, scanID, tenantID string, f FindingFilter) ([]*model.Finding, int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out, total := s.listFindingsFiltered(scanID, tenantID, f)
	return out, total, nil
}

func (s *MemoryStore) ListFindingsAll(ctx context.Context, tenantID string, f FindingFilter) ([]*model.Finding, int, error) {
	return s.ListFindings(ctx, "", tenantID, f)
}

func (s *MemoryStore) GetFinding(ctx context.Context, id, tenantID string) (*model.Finding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	f, ok := s.findings[id]
	if !ok || f.TenantID != tenantID {
		return nil, ErrNotFound
	}
	return f, nil
}

func (s *MemoryStore) UpdateFinding(ctx context.Context, id, tenantID string, upd FindingUpdate) (*model.Finding, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.findings[id]
	if !ok || f.TenantID != tenantID {
		return nil, ErrNotFound
	}
	if upd.Status != nil {
		f.Status = *upd.Status
	}
	if upd.Note != nil {
		f.Note = *upd.Note
	}
	if upd.ClearIgnore {
		f.IgnoreUntil = nil
	} else if upd.IgnoreUntil != nil {
		f.IgnoreUntil = upd.IgnoreUntil
	}
	return f, nil
}

func (s *MemoryStore) CloseMissedFindings(ctx context.Context, tenantID string, hosts []string, before time.Time) (int, error) {
	if len(hosts) == 0 {
		return 0, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	inScope := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		inScope[h] = struct{}{}
	}
	n := 0
	for _, f := range s.findings {
		if f.TenantID != tenantID || f.Status != model.FindingOpen {
			continue
		}
		if _, ok := inScope[f.Host]; !ok {
			continue
		}
		if !f.LastSeen.Before(before) {
			continue
		}
		f.Status = model.FindingFixed
		n++
	}
	return n, nil
}

func (s *MemoryStore) GetStats(ctx context.Context, tenantID string) (*model.VulnStats, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	stats := &model.VulnStats{
		BySeverity:      map[string]int{},
		ByStatus:        map[string]int{},
		ByFindingStatus: map[string]int{},
		OpenBySeverity:  map[string]int{},
	}
	var latest *model.Scan
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
		if latest == nil || sc.CreatedAt.After(latest.CreatedAt) {
			latest = sc
		}
	}
	if latest != nil {
		stats.ScannedTargets = latest.Targets
		stats.FailedTargets = latest.FailedTargets
	}
	for _, f := range s.findings {
		if f.TenantID != tenantID {
			continue
		}
		st := f.Status
		if st == "" {
			st = model.FindingOpen
		}
		stats.ByFindingStatus[string(st)]++
		if st == model.FindingOpen {
			stats.OpenFindings++
			stats.OpenBySeverity[string(f.Severity)]++
		}
	}
	return stats, nil
}
