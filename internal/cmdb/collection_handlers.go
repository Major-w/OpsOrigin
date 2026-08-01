package cmdb

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// handleGetCollectionPolicy 读取平台全局默认策略。
func (s *Service) handleGetCollectionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	policy, err := s.GetPlatformPolicy(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to get policy")
		return
	}
	writeJSON(w, 200, policy)
}

// handlePutCollectionPolicy 更新平台全局默认策略。
func (s *Service) handlePutCollectionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var p model.PlatformPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	p.TenantID = tenantID
	if p.CMDBDefaultFrequency == "" {
		p.CMDBDefaultFrequency = "1h"
	}
	if p.MonitorDefaultFrequency == "" {
		p.MonitorDefaultFrequency = "5m"
	}
	if err := s.SavePlatformPolicy(r.Context(), &p); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to save policy")
		return
	}
	writeJSON(w, 200, p)
}

// handleListCollectionTasks 列出采集任务。
func (s *Service) handleListCollectionTasks(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tasks, err := s.ListCollectionTasks(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list tasks")
		return
	}
	if tasks == nil {
		tasks = []*model.CollectionTask{}
	}
	writeJSON(w, 200, map[string]any{"tasks": tasks})
}

// handleCreateCollectionTask 手动创建一条采集任务。
func (s *Service) handleCreateCollectionTask(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Name         string `json:"name"`
		ResourceType string `json:"resource_type"`
		Target       string `json:"target"`
		Kind         string `json:"kind"`
		Frequency    string `json:"frequency"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validResourceType(req.ResourceType) {
		writeError(w, http.StatusBadRequest, "resource_type must be cloud|host|container")
		return
	}
	if req.Kind != "cmdb" && req.Kind != "monitor" {
		writeError(w, http.StatusBadRequest, "kind must be cmdb|monitor")
		return
	}
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if req.Frequency == "" {
		req.Frequency = "1h"
	}
	t := &model.CollectionTask{
		TenantID:     tenantID,
		Name:         req.Name,
		ResourceType: req.ResourceType,
		Target:       req.Target,
		Kind:         req.Kind,
		Frequency:    req.Frequency,
		Enabled:      req.Enabled,
	}
	if t.Name == "" {
		t.Name = req.ResourceType + " " + req.Kind
	}
	if err := s.CreateCollectionTask(r.Context(), t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to create task")
		return
	}
	writeJSON(w, 201, t)
}

// handleUpdateCollectionTask 更新采集任务（启停 / 频率）。
func (s *Service) handleUpdateCollectionTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	t, err := s.GetCollectionTask(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if t == nil {
		writeError(w, 404, "task not found")
		return
	}
	var req struct {
		Enabled   *bool   `json:"enabled"`
		Frequency *string `json:"frequency"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Frequency != nil && *req.Frequency != "" {
		t.Frequency = *req.Frequency
	}
	if err := s.UpdateCollectionTask(r.Context(), t); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to update task")
		return
	}
	writeJSON(w, 200, t)
}

// handleDeleteCollectionTask 删除采集任务。
func (s *Service) handleDeleteCollectionTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.DeleteCollectionTask(r.Context(), id, tenantID); err != nil {
		writeError(w, http.StatusInternalServerError, "failed to delete task")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

// handleRunCollectionTask 手动立即触发一次采集（同步执行，便于验证）。
func (s *Service) handleRunCollectionTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	t, err := s.GetCollectionTask(r.Context(), id, tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if t == nil {
		writeError(w, 404, "task not found")
		return
	}
	c, ok := s.registry.Lookup(t.Kind, t.ResourceType)
	if !ok {
		writeError(w, http.StatusBadRequest, "no collector registered for "+collectorKey(t.Kind, t.ResourceType))
		return
	}
	out, err := c.Run(r.Context(), t)
	if err != nil {
		s.markTaskFailed(r.Context(), t, err.Error())
		writeError(w, http.StatusBadGateway, "collection failed: "+err.Error())
		return
	}
	if err := s.ingestCollectionOutput(r.Context(), t, out); err != nil {
		s.markTaskFailed(r.Context(), t, err.Error())
		writeError(w, http.StatusInternalServerError, "ingest failed: "+err.Error())
		return
	}
	s.markTaskSuccess(r.Context(), t)
	writeJSON(w, 200, map[string]any{"status": "success", "cis": len(out.CIs), "metrics": len(out.Metrics)})
}

// handleOnboardResource 纳管资源入口：依据默认策略 + 逐资源覆盖开关创建采集任务。
func (s *Service) handleOnboardResource(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		ResourceType         string `json:"resource_type"`
		Target               string `json:"target"`
		EnableCMDBCollect    *bool  `json:"enable_cmdb_collect"`
		EnableMonitorCollect *bool  `json:"enable_monitor_collect"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validResourceType(req.ResourceType) {
		writeError(w, http.StatusBadRequest, "resource_type must be cloud|host|container")
		return
	}
	if req.Target == "" {
		writeError(w, http.StatusBadRequest, "target is required")
		return
	}
	if err := s.OnboardResource(r.Context(), tenantID, req.ResourceType, req.Target, req.EnableCMDBCollect, req.EnableMonitorCollect); err != nil {
		writeError(w, http.StatusInternalServerError, "onboard failed: "+err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "onboarded"})
}

func validResourceType(rt string) bool {
	switch rt {
	case "cloud", "host", "container":
		return true
	}
	return false
}
