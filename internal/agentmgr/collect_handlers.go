package agentmgr

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"
)

// ——— 采集任务 CRUD ———

func (s *Service) handleCreateCollectTask(w http.ResponseWriter, r *http.Request) {
	var t CollectTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t.TenantID = tenantFromCtx(r.Context())
	if err := s.collect.Create(&t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	slog.Info("collect task created", "id", t.ID, "name", t.Name, "type", t.Type)
	// 变更即推送:在线 Agent 立即收到最新任务清单(离线 Agent 注册时补齐)。
	s.pushTaskListToOnline()
	writeJSON(w, http.StatusCreated, t)
}

func (s *Service) handleListCollectTasks(w http.ResponseWriter, r *http.Request) {
	items := s.collect.List(tenantFromCtx(r.Context()))
	if items == nil {
		items = []*CollectTask{}
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Service) handleUpdateCollectTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var t CollectTask
	if err := json.NewDecoder(r.Body).Decode(&t); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	t.ID = id
	if err := s.collect.Update(&t); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.pushTaskListToOnline()
	writeJSON(w, http.StatusOK, t)
}

func (s *Service) handleDeleteCollectTask(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.collect.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	s.pushTaskListToOnline()
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleCollectTaskRuns(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	runs := s.collect.Runs(id)
	if runs == nil {
		runs = []*CollectRun{}
	}
	writeJSON(w, http.StatusOK, runs)
}

// ——— 采集调度器 ———

// startCollectScheduler 周期性扫描到期采集任务并派发到目标 Agent。
func (s *Service) startCollectScheduler(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.dispatchDueCollectTasks()
		}
	}
}

// dispatchDueCollectTasks 派发所有到期的采集任务。
func (s *Service) dispatchDueCollectTasks() {
	now := time.Now()
	for _, task := range s.collect.DueTasks(now) {
		s.dispatchCollectTask(task, now)
	}
}

// dispatchCollectTask 向目标 Agent 派发一次采集任务(复用轮询任务通道)。
// 目标为空时派发给全部在线 Agent。
func (s *Service) dispatchCollectTask(task *CollectTask, now time.Time) {
	// 内置类型(空脚本)由 Agent 本地调度器执行,CMDB 投影任务不在此(一次性命令)路径派发,
	// 避免与 Agent 内置任务重复跑、且 server 侧无脚本可下发。
	if task.Script == "" {
		return
	}
	ctx := context.Background()
	agents, err := s.store.List(ctx, task.TenantID)
	if err != nil {
		slog.Warn("collect: list agents failed", "task", task.ID, "err", err)
		return
	}
	for _, h := range agents {
		// 在线 Agent 才派发; 若指定了目标列表则仅派发列表内 Agent
		if h.Status != "online" {
			continue
		}
		if len(task.TargetAgents) > 0 && !contains(task.TargetAgents, h.ID) {
			continue
		}

		req := &TaskRequest{
			TaskID:       uuid.New().String(),
			Action:       "collect",
			Mode:         "collect", // Agent 端以只读采集模式执行
			Script:       task.Script,
			ScriptType:   task.ScriptType,
			Requirements: task.Requirements,
			Timeout:      task.TimeoutSec,
			Params: map[string]any{
				"collect_task_id": task.ID,
				"collect_type":    string(task.Type),
			},
		}
		if err := s.store.CreateTask(ctx, h.ID, req); err != nil {
			slog.Warn("collect: create task failed", "agent", h.ID, "err", err)
			continue
		}
		// 记录派发(结果由 handleCompleteTask → handleCollectCompletion 补充)
		s.collect.MarkRunStarted(task.ID, h.ID)
		slog.Info("collect task dispatched", "task", task.ID, "agent", h.ID, "type", task.Type)
	}
}

// handleCollectCompletion 处理采集任务完成回调:
// 回查任务 Params 中的 collect_task_id/collect_type, 按类型解析 Agent 输出并入库。
func (s *Service) handleCollectCompletion(ctx context.Context, agentID, taskID string, resp *TaskResponse) {
	if resp == nil {
		return
	}
	// 回查任务元数据(collect_task_id / collect_type)
	collectTaskID, collectType := s.lookupCollectMeta(ctx, agentID, taskID)
	if collectTaskID == "" {
		return // 非采集任务
	}

	run := &CollectRun{
		CollectTaskID: collectTaskID,
		AgentID:       agentID,
		Success:       resp.Success,
		Output:        resp.Output,
		Error:         resp.Error,
		FinishedAt:    time.Now(),
	}

	switch collectType {
	case "cmdb":
		// 输出为 CMDB 对象 JSON → 更新 agent_hosts 的 CMDB 字段
		var report cmdbReportPayload
		if err := json.Unmarshal([]byte(resp.Output), &report); err == nil && (report.Hostname != "" || report.OS != "") {
			if host, err := s.store.Get(ctx, agentID, "default"); err == nil && host != nil {
				applyCMDBReport(host, &report)
				host.UpdatedAt = time.Now()
				if err := s.store.Update(ctx, host); err != nil {
					slog.Warn("collect: apply cmdb report failed", "agent", agentID, "err", err)
				} else {
					slog.Info("collect: cmdb data applied", "agent", agentID, "task", collectTaskID)
					// 方案 X 闭环：采集资产落 agent_hosts 后立即同步为 CMDB host CI。
					s.syncHostCI(ctx, "default", host)
				}
			}
		}
	case "metric":
		// 输出为指标数组 JSON [{"name","value","unit"}] → 记录到运行结果 + 持久化
		var m []MetricPoint
		if err := json.Unmarshal([]byte(resp.Output), &m); err == nil && len(m) > 0 {
			run.Metrics = m
			if pg, ok := s.store.(*pgStore); ok {
				if err := pg.InsertCollectMetrics(ctx, "default", agentID, collectTaskID, m, time.Now()); err != nil {
					slog.Warn("collect: persist metrics failed", "agent", agentID, "err", err)
				}
			}
			slog.Info("collect: metrics recorded", "agent", agentID, "task", collectTaskID, "points", len(m))
		}
	}

	s.collect.CompleteRun(collectTaskID, agentID, run)
}

// handleQueryCollectMetrics 查询持久化的采集指标(?agent_id=&name=&limit=)。
func (s *Service) handleQueryCollectMetrics(w http.ResponseWriter, r *http.Request) {
	pg, ok := s.store.(*pgStore)
	if !ok {
		writeJSON(w, http.StatusOK, []any{})
		return
	}
	q := r.URL.Query()
	limit := 200
	if v := q.Get("limit"); v != "" {
		var n int
		_, _ = fmt.Sscanf(v, "%d", &n)
		if n > 0 {
			limit = n
		}
	}
	items, err := pg.QueryCollectMetrics(r.Context(), "default", q.Get("agent_id"), q.Get("name"), limit)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if items == nil {
		items = []map[string]any{}
	}
	writeJSON(w, http.StatusOK, items)
}

// lookupCollectMeta 从任务记录回查 collect_task_id 与 collect_type(派发时写入 Params)。
func (s *Service) lookupCollectMeta(ctx context.Context, agentID, taskID string) (string, string) {
	tasks, err := s.store.ListTasks(ctx, agentID)
	if err != nil {
		return "", ""
	}
	for _, t := range tasks {
		if t.TaskID != taskID {
			continue
		}
		cid, _ := t.Params["collect_task_id"].(string)
		ct, _ := t.Params["collect_type"].(string)
		return cid, ct
	}
	return "", ""
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
