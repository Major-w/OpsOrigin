package agentic

import (
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
)

// Postmortem 事故复盘沉淀: 复盘学习(对照 RCA 标注), 可回灌 agent 记忆/沉淀 runbook 建议。
// 对应差距分析"事故生命周期运营弱"中的复盘环节(on-call 轮值已由 itsm/oncall 覆盖)。
type Postmortem struct {
	ID         string    `json:"id"`
	TenantID   string    `json:"tenant_id"`
	IncidentID string    `json:"incident_id"`
	Title      string    `json:"title"`
	Summary    string    `json:"summary"`
	RootCause  string    `json:"root_cause"`
	Impact     string    `json:"impact,omitempty"`
	Actions    []string  `json:"actions"` // 改进措施
	Status     string    `json:"status"`  // draft | published
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}

// CreatePostmortem 创建复盘记录。
func (e *Engine) CreatePostmortem(tenantID string, pm *Postmortem) *Postmortem {
	if pm.ID == "" {
		pm.ID = uuid.New().String()
	}
	now := time.Now()
	pm.TenantID = tenantID
	pm.Status = "draft"
	pm.CreatedAt = now
	pm.UpdatedAt = now
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.postmortems == nil {
		e.postmortems = map[string]*Postmortem{}
	}
	e.postmortems[pm.ID] = pm
	slog.Info("postmortem created", "incident", pm.IncidentID, "title", pm.Title)
	return pm
}

// PublishPostmortem 发布复盘(发布时可选择回灌记忆: 提取经验到 memories)。
func (e *Engine) PublishPostmortem(tenantID, id string, withMemory bool) (*Postmortem, error) {
	e.mu.Lock()
	pm, ok := e.postmortems[id]
	if !ok || pm.TenantID != tenantID {
		e.mu.Unlock()
		return nil, fmt.Errorf("postmortem not found: %s", id)
	}
	pm.Status = "published"
	pm.UpdatedAt = time.Now()
	e.mu.Unlock()

	if withMemory && pm.RootCause != "" {
		// 回灌经验记忆: 供后续同因事故快速联想。
		e.AddMemory(&ExperienceMemory{
			TenantID:   tenantID,
			Category:   "postmortem",
			Keywords:   []string{pm.Title},
			Problem:    pm.Title,
			Resolution: fmt.Sprintf("复盘根因: %s; 改进措施: %v", pm.RootCause, pm.Actions),
			Outcome:    "succeeded",
			Source:     "postmortem",
		})
	}
	return pm, nil
}

// ListPostmortems 列出租户复盘记录(按创建时间倒序)。
func (e *Engine) ListPostmortems(tenantID string) []*Postmortem {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var out []*Postmortem
	for _, pm := range e.postmortems {
		if pm.TenantID == tenantID {
			cp := *pm
			out = append(out, &cp)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// GetPostmortem 按 ID 查询。
func (e *Engine) GetPostmortem(id string) (*Postmortem, bool) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	pm, ok := e.postmortems[id]
	if !ok {
		return nil, false
	}
	cp := *pm
	return &cp, true
}
