package cmdb

import (
	"encoding/json"
	"github.com/opsorigin/core/pkg/httputil"
	"net/http"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// handleGetCollectionPolicy 读取平台全局默认策略。
func (s *Service) handleGetCollectionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	policy, err := s.GetPlatformPolicy(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to get policy")
		return
	}
	httputil.WriteJSON(w, 200, policy)
}

// handlePutCollectionPolicy 更新平台全局默认策略。
func (s *Service) handlePutCollectionPolicy(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var p model.PlatformPolicy
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
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
		httputil.WriteError(w, http.StatusInternalServerError, "failed to save policy")
		return
	}
	httputil.WriteJSON(w, 200, p)
}

// handleListCollectionTasks 列出采集任务。
func (s *Service) handleListCollectionTasks(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	tasks, err := s.ListCollectionTasks(r.Context(), tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to list tasks")
		return
	}
	if tasks == nil {
		tasks = []*model.CollectionTask{}
	}
	httputil.WriteJSON(w, 200, map[string]any{"tasks": tasks})
}

// handleCreateCollectionTask 手动创建一条采集任务。
func (s *Service) handleCreateCollectionTask(w http.ResponseWriter, r *http.Request) {
	tenantID := tenantFromCtx(r.Context())
	var req struct {
		Name         string `json:"name"`
		ResourceType string `json:"resource_type"`
		Target       string `json:"target"`
		TargetMode   string `json:"target_mode"`  // static | query（默认 static）
		TargetQuery  string `json:"target_query"` // target_mode=query 时引用的保存查询 ID
		Kind         string `json:"kind"`
		Frequency    string `json:"frequency"`
		Enabled      bool   `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validResourceType(req.ResourceType) {
		httputil.WriteError(w, http.StatusBadRequest, "resource_type must be cloud|host|container")
		return
	}
	if req.Kind != "cmdb" && req.Kind != "monitor" {
		httputil.WriteError(w, http.StatusBadRequest, "kind must be cmdb|monitor")
		return
	}
	if req.TargetMode == "" {
		req.TargetMode = "static"
	}
	if req.TargetMode != "static" && req.TargetMode != "query" {
		httputil.WriteError(w, http.StatusBadRequest, "target_mode must be static|query")
		return
	}
	// 动态目标（query）模式：Target 留空，必须引用已存在的保存查询（"我想查"）。
	if req.TargetMode == "query" {
		if req.TargetQuery == "" {
			httputil.WriteError(w, http.StatusBadRequest, "target_query is required when target_mode=query")
			return
		}
		if s.pg != nil {
			sq, gerr := s.pg.GetSavedQuery(r.Context(), req.TargetQuery, tenantID)
			if gerr != nil {
				httputil.WriteError(w, http.StatusInternalServerError, "failed to verify target_query")
				return
			}
			if sq == nil {
				httputil.WriteError(w, http.StatusBadRequest, "target_query references a non-existent saved query")
				return
			}
		}
	} else {
		if req.Target == "" {
			httputil.WriteError(w, http.StatusBadRequest, "target is required for static mode")
			return
		}
	}
	if req.Frequency == "" {
		req.Frequency = "1h"
	}
	t := &model.CollectionTask{
		TenantID:     tenantID,
		Name:         req.Name,
		ResourceType: req.ResourceType,
		Target:       req.Target,
		TargetMode:   req.TargetMode,
		TargetQuery:  req.TargetQuery,
		Kind:         req.Kind,
		Frequency:    req.Frequency,
		Enabled:      req.Enabled,
	}
	if t.Name == "" {
		t.Name = req.ResourceType + " " + req.Kind
	}
	if err := s.CreateCollectionTask(r.Context(), t); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to create task")
		return
	}
	httputil.WriteJSON(w, 201, t)
}

// handleUpdateCollectionTask 更新采集任务（启停 / 频率）。
func (s *Service) handleUpdateCollectionTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	t, err := s.GetCollectionTask(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if t == nil {
		httputil.WriteError(w, 404, "task not found")
		return
	}
	var req struct {
		Enabled     *bool   `json:"enabled"`
		Frequency   *string `json:"frequency"`
		TargetMode  *string `json:"target_mode"`
		TargetQuery *string `json:"target_query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Enabled != nil {
		t.Enabled = *req.Enabled
	}
	if req.Frequency != nil && *req.Frequency != "" {
		t.Frequency = *req.Frequency
	}
	if req.TargetMode != nil && *req.TargetMode != "" {
		if *req.TargetMode != "static" && *req.TargetMode != "query" {
			httputil.WriteError(w, http.StatusBadRequest, "target_mode must be static|query")
			return
		}
		t.TargetMode = *req.TargetMode
	}
	if req.TargetQuery != nil {
		if t.TargetMode == "query" && *req.TargetQuery == "" {
			httputil.WriteError(w, http.StatusBadRequest, "target_query is required when target_mode=query")
			return
		}
		t.TargetQuery = *req.TargetQuery
	}
	if err := s.UpdateCollectionTask(r.Context(), t); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to update task")
		return
	}
	httputil.WriteJSON(w, 200, t)
}

// handleDeleteCollectionTask 删除采集任务。
func (s *Service) handleDeleteCollectionTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	if err := s.DeleteCollectionTask(r.Context(), id, tenantID); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "failed to delete task")
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "deleted"})
}

// handleRunCollectionTask 手动立即触发一次采集（同步执行，便于验证）。
func (s *Service) handleRunCollectionTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := tenantFromCtx(r.Context())
	t, err := s.GetCollectionTask(r.Context(), id, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "internal error")
		return
	}
	if t == nil {
		httputil.WriteError(w, 404, "task not found")
		return
	}
	c, ok := s.registry.Lookup(t.Kind, t.ResourceType)
	if !ok {
		httputil.WriteError(w, http.StatusBadRequest, "no collector registered for "+collectorKey(t.Kind, t.ResourceType))
		return
	}

	// P1-4 动态执行目标：target_mode=query 时按保存查询实时解析命中 CI，逐资源下发采集。
	if t.TargetMode == "query" && t.TargetQuery != "" {
		resolved, totalCIS, totalMetrics, rerr := s.runCollectionTaskResolved(r.Context(), t, tenantID)
		if rerr != nil {
			s.markTaskFailed(r.Context(), t, "resolve target query failed: "+rerr.Error())
			httputil.WriteError(w, http.StatusInternalServerError, "resolve target query failed: "+rerr.Error())
			return
		}
		s.markTaskSuccess(r.Context(), t)
		httputil.WriteJSON(w, 200, map[string]any{"status": "success", "resolved_targets": resolved, "cis": totalCIS, "metrics": totalMetrics})
		return
	}

	out, err := c.Run(r.Context(), t)
	if err != nil {
		s.markTaskFailed(r.Context(), t, err.Error())
		httputil.WriteError(w, http.StatusBadGateway, "collection failed: "+err.Error())
		return
	}
	if err := s.ingestCollectionOutput(r.Context(), t, out); err != nil {
		s.markTaskFailed(r.Context(), t, err.Error())
		httputil.WriteError(w, http.StatusInternalServerError, "ingest failed: "+err.Error())
		return
	}
	s.markTaskSuccess(r.Context(), t)
	httputil.WriteJSON(w, 200, map[string]any{"status": "success", "cis": len(out.CIs), "metrics": len(out.Metrics)})
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
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if !validResourceType(req.ResourceType) {
		httputil.WriteError(w, http.StatusBadRequest, "resource_type must be cloud|host|container")
		return
	}
	if req.Target == "" {
		httputil.WriteError(w, http.StatusBadRequest, "target is required")
		return
	}
	if err := s.OnboardResource(r.Context(), tenantID, req.ResourceType, req.Target, req.EnableCMDBCollect, req.EnableMonitorCollect); err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, "onboard failed: "+err.Error())
		return
	}
	httputil.WriteJSON(w, 200, map[string]string{"status": "onboarded"})
}

func validResourceType(rt string) bool {
	switch rt {
	case "cloud", "host", "container":
		return true
	}
	return false
}
