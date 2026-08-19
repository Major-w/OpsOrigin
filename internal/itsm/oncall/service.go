package oncall

import (
	"context"
	"database/sql"
	"github.com/opsorigin/core/pkg/auth"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/pkg/httputil"
)

type Store interface {
	AddShift(ctx context.Context, s *Shift) error
	ListShifts(ctx context.Context, tenantID string) ([]*Shift, error)
	AddRotation(ctx context.Context, r *Rotation) error
	ListRotations(ctx context.Context, tenantID string) ([]*Rotation, error)
	DeleteRotation(ctx context.Context, id, tenantID string) error
}

type MemoryStore struct {
	mu        sync.RWMutex
	shifts    map[string]*Shift
	rotations map[string]*Rotation
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		shifts:    make(map[string]*Shift),
		rotations: make(map[string]*Rotation),
	}
}

func (s *MemoryStore) AddShift(ctx context.Context, shift *Shift) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	shift.ID = uuid.New().String()
	shift.CreatedAt = time.Now()
	s.shifts[shift.ID] = shift
	return nil
}

func (s *MemoryStore) ListShifts(ctx context.Context, tenantID string) ([]*Shift, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Shift
	for _, sh := range s.shifts {
		if sh.TenantID == tenantID {
			result = append(result, sh)
		}
	}
	return result, nil
}

func (s *MemoryStore) AddRotation(ctx context.Context, r *Rotation) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = uuid.New().String()
	now := time.Now()
	r.CreatedAt = now
	r.UpdatedAt = now
	s.rotations[r.ID] = r
	return nil
}

func (s *MemoryStore) ListRotations(ctx context.Context, tenantID string) ([]*Rotation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []*Rotation
	for _, r := range s.rotations {
		if r.TenantID == tenantID {
			result = append(result, r)
		}
	}
	return result, nil
}

func (s *MemoryStore) DeleteRotation(ctx context.Context, id, tenantID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rotations[id]
	if !ok || r.TenantID != tenantID {
		return nil
	}
	delete(s.rotations, id)
	return nil
}

type Service struct {
	store Store
	// agentHandler 当班成员为数字员工(agent:<id>)时的 AI 接管回调(cmd/core 注入)。
	// 回调在告警路由命中时触发, 用于创建 ITSM 工单/通知当班 Agent 自动处置。
	agentHandler func(ctx context.Context, tenantID, agentID, alertTitle, alertDesc string) error
}

// SetAgentHandler 注入值班 Agent 的 AI 接管回调(nil=关闭 AI 接管, 告警仅路由不触发)。
func (s *Service) SetAgentHandler(fn func(ctx context.Context, tenantID, agentID, alertTitle, alertDesc string) error) {
	s.agentHandler = fn
}

// Alert 告警最小结构(本包自持, 不依赖 monitor): 由 cmd/core 在 monitor 告警回调中转换。
type Alert struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Title       string            `json:"title"`
	Description string            `json:"description,omitempty"`
	Severity    string            `json:"severity"` // warning | critical
	Labels      map[string]string `json:"labels,omitempty"`
	TriggeredAt time.Time         `json:"triggered_at"`
}

// HandleAlert 告警路由入口(由 monitor 告警回调调用):
// 遍历该租户全部轮转组, 命中(labels 匹配)的组取当前当班成员,
// 当班成员为 agent:<id> 时触发 AI 接管回调(创建工单 + 通知)。
func (s *Service) HandleAlert(ctx context.Context, alert *Alert) {
	if alert == nil || alert.TenantID == "" {
		return
	}
	rotations, err := s.store.ListRotations(ctx, alert.TenantID)
	if err != nil || len(rotations) == 0 {
		return
	}
	now := time.Now()
	for _, r := range rotations {
		if !labelsMatch(r.Labels, alert.Labels) {
			continue
		}
		member := r.CurrentOncall(now)
		if member == "" {
			continue
		}
		slog.Info("oncall alert routed", "rotation", r.ID, "name", r.Name, "member", member, "alert", alert.ID, "title", alert.Title)
		if isAgentMember(member) && s.agentHandler != nil {
			agentID := member[len("agent:"):]
			if err := s.agentHandler(ctx, alert.TenantID, agentID, alert.Title, alert.Description); err != nil {
				slog.Warn("oncall agent takeover failed", "rotation", r.ID, "agent", agentID, "error", err)
				return
			}
			slog.Info("oncall agent takeover dispatched", "rotation", r.ID, "agent", agentID, "alert", alert.ID)
		}
		return // 命中第一个匹配的轮转组即路由(优先级=创建顺序)
	}
	slog.Debug("oncall: no rotation matched alert", "alert", alert.ID, "title", alert.Title)
}

func isAgentMember(member string) bool {
	return len(member) > len("agent:") && member[:len("agent:")] == "agent:"
}

func labelsMatch(required, actual map[string]string) bool {
	if len(required) == 0 {
		return true
	}
	for k, v := range required {
		if actual[k] != v {
			return false
		}
	}
	return true
}

func NewService(db *sql.DB) *Service {
	var st Store = NewMemoryStore()
	if db != nil {
		st = newPGStore(db)
	}
	return &Service{store: st}
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/oncall", s.handleListShifts)
	mux.HandleFunc("POST /api/v1/oncall", s.handleAddShift)
	mux.HandleFunc("GET /api/v1/oncall/rotations", s.handleListRotations)
	mux.HandleFunc("POST /api/v1/oncall/rotations", s.handleCreateRotation)
	mux.HandleFunc("DELETE /api/v1/oncall/rotations/{id}", s.handleDeleteRotation)
	mux.HandleFunc("GET /api/v1/oncall/rotations/{id}/oncall", s.handleCurrentOncall)
}

func (s *Service) handleListShifts(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	shifts, _ := s.store.ListShifts(r.Context(), tenantID)
	if shifts == nil {
		shifts = []*Shift{}
	}
	httputil.WriteJSON(w, http.StatusOK, shifts)
}

func (s *Service) handleAddShift(w http.ResponseWriter, r *http.Request) {
	var shift Shift
	httputil.BindJSON(r, &shift)
	shift.TenantID = auth.TenantFromContext(r.Context())
	s.store.AddShift(r.Context(), &shift)
	httputil.WriteJSON(w, http.StatusCreated, shift)
}

func (s *Service) handleListRotations(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	rotations, _ := s.store.ListRotations(r.Context(), tenantID)
	if rotations == nil {
		rotations = []*Rotation{}
	}
	httputil.WriteJSON(w, http.StatusOK, rotations)
}

func (s *Service) handleCreateRotation(w http.ResponseWriter, r *http.Request) {
	var rot Rotation
	httputil.BindJSON(r, &rot)
	rot.TenantID = auth.TenantFromContext(r.Context())
	s.store.AddRotation(r.Context(), &rot)
	httputil.WriteJSON(w, http.StatusCreated, rot)
}

func (s *Service) handleDeleteRotation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.store.DeleteRotation(r.Context(), id, auth.TenantFromContext(r.Context()))
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleCurrentOncall(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	rotations, _ := s.store.ListRotations(r.Context(), tenantID)
	for _, rot := range rotations {
		if rot.ID != id {
			continue
		}
		member := rot.CurrentOncall(time.Now())
		httputil.WriteJSON(w, http.StatusOK, map[string]any{
			"rotation_id": rot.ID,
			"name":        rot.Name,
			"member":      member,
			"members":     rot.Members,
			"cycle_days":  rot.CycleDays,
			"labels":      rot.Labels,
		})
		return
	}
	httputil.WriteError(w, http.StatusNotFound, "rotation not found")
}
