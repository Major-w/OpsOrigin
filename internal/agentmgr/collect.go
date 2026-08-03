package agentmgr

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

// ============================================================================
// 采集任务管理 (Agent 三种能力之一: CMDB/监控数据采集上报)
//
// 采集任务 = 定时向目标 Agent 派发 Mode=collect 的任务(只读采集脚本),
// Agent 以 collect 模式执行(可读主机、写受限、环境净化)并回传输出,
// Server 侧按任务类型解析入库:
//   - type=cmdb  : 输出 JSON 对象(hostname/os/cpu/memory/disk/ips/labels) → 更新 agent_hosts;
//   - type=metric: 输出 JSON 数组 [{"name","value","unit"}] → 记录为采集指标。
//
// 当前为内存实现(重启丢失), 后续可加 PG 持久化。
// ============================================================================

// CollectTaskType 采集任务类型。
type CollectTaskType string

const (
	CollectTypeCMDB   CollectTaskType = "cmdb"
	CollectTypeMetric CollectTaskType = "metric"
)

// CollectTask 一个采集任务定义。
type CollectTask struct {
	ID           string          `json:"id"`
	TenantID     string          `json:"tenant_id"`
	Name         string          `json:"name"`
	Type         CollectTaskType `json:"type"` // cmdb | metric
	Script       string          `json:"script"`
	ScriptType   string          `json:"script_type,omitempty"` // shell(默认) | python
	Requirements []string        `json:"requirements,omitempty"`
	IntervalSec  int             `json:"interval_sec"`            // 调度周期(秒)
	TargetAgents []string        `json:"target_agents,omitempty"` // 空 = 全部在线 Agent
	Enabled      bool            `json:"enabled"`
	TimeoutSec   int             `json:"timeout_sec,omitempty"` // 默认 60
	LastRunAt    *time.Time      `json:"last_run_at,omitempty"`
	CreatedAt    time.Time       `json:"created_at"`
	UpdatedAt    time.Time       `json:"updated_at"`
}

// CollectRun 一次采集执行的记录。
type CollectRun struct {
	RunID         string        `json:"run_id"`
	CollectTaskID string        `json:"collect_task_id"`
	AgentID       string        `json:"agent_id"`
	Success       bool          `json:"success"`
	Output        string        `json:"output,omitempty"`
	Metrics       []MetricPoint `json:"metrics,omitempty"`
	Error         string        `json:"error,omitempty"`
	StartedAt     time.Time     `json:"started_at"`
	FinishedAt    time.Time     `json:"finished_at"`
}

// MetricPoint 采集到的单个指标点。
type MetricPoint struct {
	Name  string  `json:"name"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
}

// CollectStore 采集任务的持久化接口(可选, PG 实现; nil 时仅内存)。
type CollectStore interface {
	LoadCollectTasks(ctx context.Context) ([]*CollectTask, error)
	SaveCollectTask(ctx context.Context, t *CollectTask) error
	DeleteCollectTask(ctx context.Context, id string) error
}

// CollectManager 采集任务的内存管理器(可选持久化)。
type CollectManager struct {
	mu    sync.RWMutex
	tasks map[string]*CollectTask  // id -> task
	runs  map[string][]*CollectRun // task_id -> runs(最近 100)
	store CollectStore             // 可选持久化
}

// maxCollectRuns ���个任务保留的最近运行记录数。
const maxCollectRuns = 100

func NewCollectManager(store CollectStore) *CollectManager {
	m := &CollectManager{
		tasks: make(map[string]*CollectTask),
		runs:  make(map[string][]*CollectRun),
		store: store,
	}
	if store != nil {
		if loaded, err := store.LoadCollectTasks(context.Background()); err == nil {
			for _, t := range loaded {
				m.tasks[t.ID] = t
			}
		}
	}
	return m
}

// Create 创建采集任务。
func (m *CollectManager) Create(t *CollectTask) error {
	if t.Name == "" {
		return errCollect("task name is required")
	}
	if t.Type != CollectTypeCMDB && t.Type != CollectTypeMetric {
		return errCollect("task type must be cmdb or metric")
	}
	if t.Script == "" {
		return errCollect("script is required")
	}
	if t.IntervalSec <= 0 {
		t.IntervalSec = 60
	}
	if t.TimeoutSec <= 0 {
		t.TimeoutSec = 60
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if t.ID == "" {
		t.ID = uuid.New().String()
	}
	if t.TenantID == "" {
		t.TenantID = "default"
	}
	now := time.Now()
	t.CreatedAt = now
	t.UpdatedAt = now
	m.tasks[t.ID] = t
	if m.store != nil {
		return m.store.SaveCollectTask(context.Background(), t)
	}
	return nil
}

// Update 更新采集任务(全量覆盖)。
func (m *CollectManager) Update(t *CollectTask) error {
	if t.ID == "" {
		return errCollect("task id is required")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	existing, ok := m.tasks[t.ID]
	if !ok {
		return errCollect("collect task not found: " + t.ID)
	}
	t.TenantID = existing.TenantID
	t.CreatedAt = existing.CreatedAt
	t.UpdatedAt = time.Now()
	m.tasks[t.ID] = t
	if m.store != nil {
		return m.store.SaveCollectTask(context.Background(), t)
	}
	return nil
}

// PutProjection 写入一条「投影」采集任务(由 CMDB 统一调度框架同步而来)。
// 与 Create 不同：跳过脚本非空校验——投影任务的采集动作由 Agent 内置调度器执行
// (cmdb/metric 类型空脚本即内置采集),CMDB 是单一来源与调度者,agentmgr 仅作传输层。
func (m *CollectManager) PutProjection(t *CollectTask) error {
	if t.ID == "" {
		return errCollect("projection id is required")
	}
	if t.Type != CollectTypeCMDB && t.Type != CollectTypeMetric {
		return errCollect("task type must be cmdb or metric")
	}
	if t.IntervalSec <= 0 {
		t.IntervalSec = 60
	}
	if t.TimeoutSec <= 0 {
		t.TimeoutSec = 60
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if existing, ok := m.tasks[t.ID]; ok {
		t.TenantID = existing.TenantID
		t.CreatedAt = existing.CreatedAt
	} else {
		now := time.Now()
		t.CreatedAt = now
	}
	t.UpdatedAt = time.Now()
	m.tasks[t.ID] = t
	if m.store != nil {
		return m.store.SaveCollectTask(context.Background(), t)
	}
	return nil
}

// Delete 删除采集任务。
func (m *CollectManager) Delete(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.tasks[id]; !ok {
		return errCollect("collect task not found: " + id)
	}
	delete(m.tasks, id)
	delete(m.runs, id)
	if m.store != nil {
		return m.store.DeleteCollectTask(context.Background(), id)
	}
	return nil
}

// Get 按 ID 取任务。
func (m *CollectManager) Get(id string) (*CollectTask, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.tasks[id]
	return t, ok
}

// List 列出指定租户的采集任务。
func (m *CollectManager) List(tenantID string) []*CollectTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*CollectTask, 0)
	for _, t := range m.tasks {
		if t.TenantID == tenantID {
			out = append(out, t)
		}
	}
	return out
}

// DueTasks 返回当前到期的 enabled 任务(now - LastRunAt >= IntervalSec)。
func (m *CollectManager) DueTasks(now time.Time) []*CollectTask {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*CollectTask, 0)
	for _, t := range m.tasks {
		if !t.Enabled {
			continue
		}
		if t.LastRunAt == nil || now.Sub(*t.LastRunAt) >= time.Duration(t.IntervalSec)*time.Second {
			out = append(out, t)
		}
	}
	return out
}

// MarkRunStarted 记录一次采集派发(开始时间), 完成时由 CompleteRun 补充结果。
func (m *CollectManager) MarkRunStarted(taskID, agentID string) {
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if t, ok := m.tasks[taskID]; ok {
		t.LastRunAt = &now
		t.UpdatedAt = now
	}
	m.runs[taskID] = append(m.runs[taskID], &CollectRun{
		RunID:         uuid.New().String(),
		CollectTaskID: taskID,
		AgentID:       agentID,
		StartedAt:     now,
	})
	if len(m.runs[taskID]) > maxCollectRuns {
		m.runs[taskID] = m.runs[taskID][len(m.runs[taskID])-maxCollectRuns:]
	}
}

// CompleteRun 补充最近一条未完成运行的结果(按 taskID+agentID 匹配最后一条 StartedAt 无 FinishedAt 的记录)。
func (m *CollectManager) CompleteRun(taskID, agentID string, run *CollectRun) {
	m.mu.Lock()
	defer m.mu.Unlock()
	runs := m.runs[taskID]
	for i := len(runs) - 1; i >= 0; i-- {
		r := runs[i]
		if r.AgentID == agentID && r.FinishedAt.IsZero() {
			if run.RunID != "" {
				r.RunID = run.RunID
			}
			r.Success = run.Success
			r.Output = run.Output
			r.Metrics = run.Metrics
			r.Error = run.Error
			r.FinishedAt = run.FinishedAt
			if r.FinishedAt.IsZero() {
				r.FinishedAt = time.Now()
			}
			return
		}
	}
	// 未找到开始记录(如 Server 重启): 追加一条完成记录
	if run.RunID == "" {
		run.RunID = uuid.New().String()
	}
	if run.StartedAt.IsZero() {
		run.StartedAt = time.Now()
	}
	if run.FinishedAt.IsZero() {
		run.FinishedAt = time.Now()
	}
	run.CollectTaskID = taskID
	run.AgentID = agentID
	m.runs[taskID] = append(m.runs[taskID], run)
	if len(m.runs[taskID]) > maxCollectRuns {
		m.runs[taskID] = m.runs[taskID][len(m.runs[taskID])-maxCollectRuns:]
	}
}

// Runs 返回某任务的最近运行记录(倒序)。
func (m *CollectManager) Runs(taskID string) []*CollectRun {
	m.mu.RLock()
	defer m.mu.RUnlock()
	runs := m.runs[taskID]
	out := make([]*CollectRun, 0, len(runs))
	for i := len(runs) - 1; i >= 0; i-- {
		out = append(out, runs[i])
	}
	return out
}

func errCollect(msg string) error {
	return &collectError{msg: msg}
}

type collectError struct{ msg string }

func (e *collectError) Error() string { return e.msg }

// ============================================================
// CMDB 投影接口（方案 X：CMDB 统一调度，agentmgr 为 Agent 传输层）
//
// CMDB 持有 channel=agent 的采集任务(主机/容器)，并通过 AgentChannel 接口把任务
// 以「投影」形式同步到 agentmgr：agentmgr 仅负责把任务下发到对应 Agent(server 侧不拉取)。
// 投影任务 ID 固定为 "cmdb:" + CmdbTaskID，保证幂等(重复同步=覆盖)。
// ============================================================

// CMDBCollectSpec 是 CMDB 同步过来的采集任务描述。
type CMDBCollectSpec struct {
	CmdbTaskID   string // cmdb 采集任务 ID（投影键来源）
	TenantID     string
	AgentType    CollectTaskType // cmdb | metric（由 cmdb.Kind 映射）
	TargetAgents []string        // 目标 Agent ID 列表（由 cmdb.Target "host:<id>" 解析）
	IntervalSec  int             // 采集周期（秒）
	Enabled      bool
}

// UpsertCollectTaskFromCMDB 把一条 CMDB 采集任务同步为 Agent 侧采集任务(投影)。
// 内置 cmdb/metric 类型使用空脚本——采集由 Agent 本地调度器执行,不在此路径派发。
func (s *Service) UpsertCollectTaskFromCMDB(spec CMDBCollectSpec) error {
	id := "cmdb:" + spec.CmdbTaskID
	t := &CollectTask{
		ID:           id,
		TenantID:     spec.TenantID,
		Name:         "cmdb-synced " + spec.CmdbTaskID + " " + string(spec.AgentType),
		Type:         spec.AgentType,
		Script:       "", // 内置采集:由 Agent 本地调度器执行
		IntervalSec:  spec.IntervalSec,
		TargetAgents: spec.TargetAgents,
		Enabled:      spec.Enabled,
	}
	if err := s.collect.PutProjection(t); err != nil {
		return err
	}
	// 变更即推送:在线 Agent 立即收到最新任务清单。
	s.pushTaskListToOnline()
	slog.Info("cmdb collect task projected to agent", "cmdb_task", spec.CmdbTaskID, "type", spec.AgentType, "agents", spec.TargetAgents)
	return nil
}

// RemoveCollectTaskFromCMDB 移除对应 Agent 侧投影任务(并推送空清单让 Agent 移除)。
func (s *Service) RemoveCollectTaskFromCMDB(cmdbTaskID string) error {
	id := "cmdb:" + cmdbTaskID
	if err := s.collect.Delete(id); err != nil {
		return err
	}
	s.pushTaskListToOnline()
	slog.Info("cmdb collect task projection removed", "cmdb_task", cmdbTaskID)
	return nil
}
