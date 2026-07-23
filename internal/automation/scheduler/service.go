package scheduler

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Store interface {
	Create(ctx context.Context, t *ScheduledTask) error
	Get(ctx context.Context, id, tenantID string) (*ScheduledTask, error)
	List(ctx context.Context, tenantID string) ([]*ScheduledTask, error)
	Update(ctx context.Context, t *ScheduledTask) error
	Delete(ctx context.Context, id, tenantID string) error
}

type MemoryStore struct {
	mu    sync.RWMutex
	tasks map[string]*ScheduledTask
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{tasks: make(map[string]*ScheduledTask)}
}

func (s *MemoryStore) Create(ctx context.Context, t *ScheduledTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.ID = uuid.New().String()
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	s.tasks[t.ID] = t
	return nil
}

func (s *MemoryStore) Get(ctx context.Context, id, tenantID string) (*ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.tasks[id]
	if !ok || t.TenantID != tenantID {
		return nil, nil
	}
	return t, nil
}

func (s *MemoryStore) List(ctx context.Context, tenantID string) ([]*ScheduledTask, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*ScheduledTask
	for _, t := range s.tasks {
		if tenantID == "" || t.TenantID == tenantID {
			result = append(result, t)
		}
	}
	return result, nil
}

func (s *MemoryStore) Update(ctx context.Context, t *ScheduledTask) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	existing, ok := s.tasks[t.ID]
	if !ok || existing.TenantID != t.TenantID {
		return nil
	}
	t.UpdatedAt = time.Now()
	s.tasks[t.ID] = t
	return nil
}

func (s *MemoryStore) Delete(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.tasks[id]
	if !ok || t.TenantID != tenantID {
		return nil
	}
	delete(s.tasks, t.ID)
	return nil
}

type Service struct {
	store   Store
	trigger func(ctx context.Context, tenantID, workflowID string) error
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = NewPGStore(db)
	}
	return &Service{store: st}
}

// SetWorkflowTrigger 注入工作流执行入口，使定时任务能真正触发 workflow 执行。
func (s *Service) SetWorkflowTrigger(f func(ctx context.Context, tenantID, workflowID string) error) {
	s.trigger = f
}

func tenantFromCtx(ctx context.Context) string {
	if t, ok := ctx.Value("tenant_id").(string); ok && t != "" {
		return t
	}
	return "default"
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/schedules", s.handleCreate)
	mux.HandleFunc("GET /api/v1/schedules", s.handleList)
	mux.HandleFunc("GET /api/v1/schedules/{id}", s.handleGet)
	mux.HandleFunc("PUT /api/v1/schedules/{id}", s.handleUpdate)
	mux.HandleFunc("DELETE /api/v1/schedules/{id}", s.handleDelete)
}

// Start 启动定时扫描循环，按各任务的 CronExpr 触发 workflow 类任务。
// 阻塞直至 ctx 取消；建议在独立 goroutine 中调用。
func (s *Service) Start(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.scan(ctx)
		}
	}
}

// scan 遍历所有启用的 workflow 类任务，命中 cron 表达式即触发（同分钟内去重）。
func (s *Service) scan(ctx context.Context) {
	if s.trigger == nil {
		return
	}
	tasks, err := s.store.List(ctx, "")
	if err != nil {
		slog.Warn("scheduler scan list failed", "err", err)
		return
	}
	now := time.Now()
	for _, t := range tasks {
		if !t.Enabled || t.TaskType != TaskTypeWorkflow || t.CronExpr == "" {
			continue
		}
		if !matchCron(t.CronExpr, now) {
			continue
		}
		if t.LastRunAt != nil && now.Sub(*t.LastRunAt) < time.Minute {
			continue // 同分钟内不重复触发
		}
		t.LastRunAt = &now
		if uerr := s.store.Update(ctx, t); uerr != nil {
			slog.Warn("scheduler update last_run failed", "task", t.ID, "err", uerr)
		}
		go func(tenantID, workflowID string) {
			if err := s.trigger(context.WithoutCancel(ctx), tenantID, workflowID); err != nil {
				slog.Warn("scheduler trigger failed", "workflow", workflowID, "err", err)
			}
		}(t.TenantID, t.TargetID)
	}
}

// matchCron 是最小 cron 匹配子集，支持 *、*/n、a-b、a,b 五字段
// （分 时 日 月 周）。命中全部字段即视为匹配。
func matchCron(expr string, t time.Time) bool {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return false
	}
	weekday := int(t.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	vals := []int{t.Minute(), t.Hour(), t.Day(), int(t.Month()), weekday}
	for i, f := range fields {
		if !matchCronField(f, vals[i]) {
			return false
		}
	}
	return true
}

func matchCronField(field string, val int) bool {
	if field == "*" {
		return true
	}
	for _, part := range strings.Split(field, ",") {
		if matchCronPart(part, val) {
			return true
		}
	}
	return false
}

func matchCronPart(part string, val int) bool {
	if strings.HasPrefix(part, "*/") {
		n, err := strconv.Atoi(part[2:])
		if err != nil || n <= 0 {
			return false
		}
		return val%n == 0
	}
	if strings.Contains(part, "-") {
		ps := strings.SplitN(part, "-", 2)
		lo, err1 := strconv.Atoi(ps[0])
		hi, err2 := strconv.Atoi(ps[1])
		if err1 != nil || err2 != nil {
			return false
		}
		return val >= lo && val <= hi
	}
	n, err := strconv.Atoi(part)
	if err != nil {
		return false
	}
	return val == n
}

func (s *Service) handleCreate(w http.ResponseWriter, r *http.Request) {
	var task ScheduledTask
	if err := json.NewDecoder(r.Body).Decode(&task); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	task.TenantID = tenantFromCtx(r.Context())
	task.Enabled = true
	if err := s.store.Create(r.Context(), &task); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, task)
}

func (s *Service) handleList(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tasks, err := s.store.List(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if tasks == nil {
		tasks = []*ScheduledTask{}
	}
	writeJSON(w, http.StatusOK, tasks)
}

func (s *Service) handleGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	task, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || task == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	writeJSON(w, http.StatusOK, task)
}

func (s *Service) handleUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	existing, err := s.store.Get(r.Context(), id, tenantID)
	if err != nil || existing == nil {
		writeError(w, http.StatusNotFound, "task not found")
		return
	}
	var task ScheduledTask
	json.NewDecoder(r.Body).Decode(&task)
	task.ID = existing.ID
	task.TenantID = existing.TenantID
	task.CreatedAt = existing.CreatedAt
	s.store.Update(r.Context(), &task)
	writeJSON(w, http.StatusOK, task)
}

func (s *Service) handleDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	s.store.Delete(r.Context(), id, tenantID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}