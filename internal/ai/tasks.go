// 任务级 Agent 骨架(T1): 把 AI 助手从"对话级"升级为"任务级"。
//
// 核心概念:
//   - AITask = 目标(goal) + 步骤清单(plan_steps) + 状态机(planning→running→completed/failed/cancelled) + 权限模式
//   - Task Runner: 服务端后台 goroutine 逐步骤驱动 Engine.ChatStream, 与 HTTP 请求生命周期解耦
//     (用户关页面任务继续跑, 重新打开通过 SSE 重连恢复状态快照)
//   - taskEventBus: 按任务 ID 的事件广播(内存, 单实例部署; 多副本时升级为 Redis Pub/Sub)
//
// 权限模式(PermissionMode)本阶段仅存储与透传; 风险分级与执行判定在 T3 落地。
// 设计文档: .docs/design-task-level-agent.md
package ai

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/opsorigin/core/internal/ai/mcp"
	"github.com/opsorigin/core/internal/ai/model"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
	"github.com/opsorigin/core/pkg/llm"
)

// ——— 数据模型 ———

// TaskStep 任务步骤(checklist 单项)。
type TaskStep struct {
	ID     string `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"` // pending | running | done | failed | skipped
	Note   string `json:"note,omitempty"`
	Output string `json:"output,omitempty"` // 执行轨迹(T4): 本步骤模型产出正文, 落库供断线重连补放
}

// steerEntry 运行中插话(T4): 用户在执行中途注入的指令, Runner 在步骤边界追加为会话 user 消息。
type steerEntry struct {
	ID        string    `json:"id"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// AITask 任务实体。
type AITask struct {
	ID             string       `json:"id"`
	TenantID       string       `json:"tenant_id"`
	ConversationID string       `json:"conversation_id"`   // 轨迹载体: 绑定一个引擎会话, 步骤上下文累积
	AgentID        string       `json:"agent_id"`          // 执行者(数字员工), 空=默认助手
	TeamID         string       `json:"team_id,omitempty"` // 协作团队(T5): 非空时步骤派发给该团队并行执行, 空=单 agent 串行
	Title          string       `json:"title"`
	Goal           string       `json:"goal"`
	Status         string       `json:"status"` // planning | running | waiting_approval | paused | completed | failed | cancelled
	PermissionMode string       `json:"permission_mode"`
	Steps          []TaskStep   `json:"steps"`
	Steers         []steerEntry `json:"steers,omitempty"` // 运行中插话记录(T4)
	Priority       int          `json:"priority"`         // T6 调度优先级: 1=高 / 5=普通(默认) / 9=低, 数值小者先出队
	ResultSummary  string       `json:"result_summary"`
	Error          string       `json:"error,omitempty"`
	CreatedBy      string       `json:"created_by"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

// Task 有效权限模式集合(T3 实施判定; 此处先做入口校验)。
var validPermissionModes = map[string]bool{"readonly": true, "standard": true, "auto": true, "full_auto": true}

// initTaskDDL 幂等建表(与 artifacts/attachments 同风格)。
func (s *Service) initTaskDDL(ctx context.Context) {
	if s.db == nil {
		return
	}
	if _, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS ai_tasks (
			id TEXT PRIMARY KEY,
			tenant_id TEXT NOT NULL,
			conversation_id TEXT NOT NULL DEFAULT '',
			agent_id TEXT NOT NULL DEFAULT '',
			title TEXT NOT NULL,
			goal TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'planning',
			permission_mode TEXT NOT NULL DEFAULT 'standard',
			plan_steps JSONB NOT NULL DEFAULT '[]',
			steers JSONB NOT NULL DEFAULT '[]',
			result_summary TEXT NOT NULL DEFAULT '',
			error TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL,
			updated_at TIMESTAMPTZ NOT NULL
		)`); err != nil {
		slog.Warn("ai tasks DDL failed", "err", err)
		return
	}
	// 升级兼容: 早期表无 permission_mode / steers 列
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE ai_tasks ADD COLUMN IF NOT EXISTS permission_mode TEXT NOT NULL DEFAULT 'standard'`); err != nil {
		slog.Warn("ai tasks alter failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE ai_tasks ADD COLUMN IF NOT EXISTS steers JSONB NOT NULL DEFAULT '[]'`); err != nil {
		slog.Warn("ai tasks alter failed", "err", err)
	}
	// T5: 协作团队(任务步骤派发给团队并行执行)
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE ai_tasks ADD COLUMN IF NOT EXISTS team_id TEXT NOT NULL DEFAULT ''`); err != nil {
		slog.Warn("ai tasks alter failed", "err", err)
	}
	// T6: 调度优先级(1=高 / 5=普通 / 9=低)
	if _, err := s.db.ExecContext(ctx,
		`ALTER TABLE ai_tasks ADD COLUMN IF NOT EXISTS priority INT NOT NULL DEFAULT 5`); err != nil {
		slog.Warn("ai tasks alter failed", "err", err)
	}
	if _, err := s.db.ExecContext(ctx,
		`CREATE INDEX IF NOT EXISTS idx_ai_tasks_tenant ON ai_tasks(tenant_id, status, updated_at DESC)`); err != nil {
		slog.Warn("ai tasks index failed", "err", err)
	}
}

// ——— 存储层(db 不可用时回退进程内存, 与其他模块降级策略一致) ———

var taskMemStore = struct {
	sync.Mutex
	m map[string]*AITask
}{m: map[string]*AITask{}}

func (s *Service) saveTask(ctx context.Context, t *AITask) {
	t.UpdatedAt = time.Now()
	if s.db == nil {
		taskMemStore.Lock()
		cp := *t
		taskMemStore.m[t.ID] = &cp
		taskMemStore.Unlock()
		return
	}
	steps, _ := json.Marshal(t.Steps)
	steers, _ := json.Marshal(t.Steers)
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO ai_tasks (id, tenant_id, conversation_id, agent_id, team_id, title, goal, status, permission_mode, plan_steps, steers, priority, result_summary, error, created_by, created_at, updated_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO UPDATE SET
		  conversation_id=EXCLUDED.conversation_id, agent_id=EXCLUDED.agent_id, team_id=EXCLUDED.team_id, title=EXCLUDED.title,
		  status=EXCLUDED.status, permission_mode=EXCLUDED.permission_mode, plan_steps=EXCLUDED.plan_steps,
		  steers=EXCLUDED.steers, priority=EXCLUDED.priority, result_summary=EXCLUDED.result_summary, error=EXCLUDED.error, updated_at=EXCLUDED.updated_at`,
		t.ID, t.TenantID, t.ConversationID, t.AgentID, t.TeamID, t.Title, t.Goal, t.Status, t.PermissionMode,
		steps, steers, t.Priority, t.ResultSummary, t.Error, t.CreatedBy, t.CreatedAt, t.UpdatedAt); err != nil {
		slog.Warn("ai task save failed", "id", t.ID, "err", err)
	}
}

const taskSelectCols = `id, tenant_id, conversation_id, agent_id, team_id, title, goal, status, permission_mode, plan_steps, steers, priority, result_summary, error, created_by, created_at, updated_at`

func scanTask(scan func(dest ...any) error) (*AITask, error) {
	var t AITask
	var stepsRaw, steersRaw []byte
	if err := scan(&t.ID, &t.TenantID, &t.ConversationID, &t.AgentID, &t.TeamID, &t.Title, &t.Goal, &t.Status,
		&t.PermissionMode, &stepsRaw, &steersRaw, &t.Priority, &t.ResultSummary, &t.Error, &t.CreatedBy, &t.CreatedAt, &t.UpdatedAt); err != nil {
		return nil, err
	}
	t.Steps = []TaskStep{}
	if len(stepsRaw) > 0 {
		_ = json.Unmarshal(stepsRaw, &t.Steps)
	}
	t.Steers = []steerEntry{}
	if len(steersRaw) > 0 {
		_ = json.Unmarshal(steersRaw, &t.Steers)
	}
	return &t, nil
}

func (s *Service) getTask(ctx context.Context, id, tenantID string) *AITask {
	if s.db == nil {
		taskMemStore.Lock()
		defer taskMemStore.Unlock()
		t := taskMemStore.m[id]
		if t == nil || t.TenantID != tenantID {
			return nil
		}
		cp := *t
		return &cp
	}
	t, err := scanTask(s.db.QueryRowContext(ctx,
		`SELECT `+taskSelectCols+` FROM ai_tasks WHERE id=$1 AND tenant_id=$2`, id, tenantID).Scan)
	if err != nil {
		return nil
	}
	return t
}

func (s *Service) listTasks(ctx context.Context, tenantID, status string) []*AITask {
	out := []*AITask{}
	if s.db == nil {
		taskMemStore.Lock()
		defer taskMemStore.Unlock()
		for _, t := range taskMemStore.m {
			if t.TenantID == tenantID && (status == "" || t.Status == status) {
				cp := *t
				out = append(out, &cp)
			}
		}
		return out
	}
	q := `SELECT ` + taskSelectCols + ` FROM ai_tasks WHERE tenant_id=$1`
	args := []any{tenantID}
	if status != "" {
		q += ` AND status=$2`
		args = append(args, status)
	}
	q += ` ORDER BY updated_at DESC LIMIT 200`
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		if t, err := scanTask(rows.Scan); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// ——— 事件总线 ———

// taskEvent SSE 事件: type ∈ snapshot|status|step|token|thinking|tool|done|failed|cancelled
type taskEvent struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type taskEventBus struct {
	mu   sync.Mutex
	subs map[string]map[chan taskEvent]struct{}
}

func newTaskEventBus() *taskEventBus {
	return &taskEventBus{subs: map[string]map[chan taskEvent]struct{}{}}
}

func (b *taskEventBus) subscribe(taskID string) chan taskEvent {
	ch := make(chan taskEvent, 512)
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.subs[taskID] == nil {
		b.subs[taskID] = map[chan taskEvent]struct{}{}
	}
	b.subs[taskID][ch] = struct{}{}
	return ch
}

func (b *taskEventBus) unsubscribe(taskID string, ch chan taskEvent) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if set := b.subs[taskID]; set != nil {
		if _, ok := set[ch]; ok {
			delete(set, ch)
			close(ch) // 广播同样持锁, 无 send-after-close 竞态
		}
	}
}

func (b *taskEventBus) broadcast(taskID, typ string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	ev := taskEvent{Type: typ, Data: raw}
	b.mu.Lock()
	defer b.mu.Unlock()
	for ch := range b.subs[taskID] {
		select {
		case ch <- ev: // 缓冲满则丢弃(纯 UI 事件, 订阅端有快照兜底)
		default:
		}
	}
}

func (s *Service) emitTask(taskID, typ string, payload any) {
	if s.taskBus != nil {
		s.taskBus.broadcast(taskID, typ, payload)
	}
}

// ——— Task Runner ———

// startTask 启动任务(重复启动防护 + 重置未完成步骤), 返回是否成功启动。
func (s *Service) startTask(ctx context.Context, t *AITask) bool {
	s.taskMu.Lock()
	if s.taskRunning[t.ID] {
		s.taskMu.Unlock()
		return false
	}
	// T6: 并发槽同步占位——早于 go runTask, 避免 pumpScheduler 计数时看到空档而超发。
	s.taskRunning[t.ID] = true
	delete(s.taskPaused, t.ID) // T4: 续跑时清除暂停标记
	s.taskMu.Unlock()
	// 预生成轨迹会话 ID: 保证步骤执行中 doc_write 等工具能立即以正确的 conversation_id 落产物元数据
	if t.ConversationID == "" {
		t.ConversationID = uuid.New().String()
	}
	for i := range t.Steps {
		if t.Steps[i].Status == "running" || t.Steps[i].Status == "failed" || t.Steps[i].Status == "pending" {
			t.Steps[i].Status = "pending"
		}
	}
	t.Status = "running"
	t.Error = ""
	s.saveTask(ctx, t)
	s.emitTask(t.ID, "status", map[string]string{"status": "running", "task_id": t.ID})
	go s.runTask(t)
	return true
}

// runTask 后台执行: 逐步骤驱动 ChatStream, 步骤间共享同一会话(上下文累积)。
// 使用独立 context(非请求作用域), 用户关页面任务继续跑; cancel 端点触发取消。
func (s *Service) runTask(t *AITask) {
	ctx, cancel := context.WithCancel(context.Background())
	s.taskMu.Lock()
	s.taskCancels[t.ID] = cancel
	s.taskLive[t.ID] = t // 活体指针: task_update_step 等工具 handler 直改本结构(与 Runner 同 goroutine)
	s.taskMu.Unlock()
	defer func() {
		cancel()
		s.taskMu.Lock()
		delete(s.taskCancels, t.ID)
		delete(s.taskLive, t.ID)
		delete(s.taskRunning, t.ID) // T6: 释放并发槽
		s.taskMu.Unlock()
		if r := recover(); r != nil {
			slog.Error("ai task runner panic", "task_id", t.ID, "panic", r)
			t.Status, t.Error = "failed", fmt.Sprintf("执行器内部错误: %v", r)
			pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
			s.saveTask(pctx, t)
			pcancel()
			s.emitTask(t.ID, "failed", map[string]string{"task_id": t.ID, "error": t.Error})
		}
		// T6: 无论正常完成/失败/取消/暂停, 释放槽位后都尝试出队下一个等待任务
		s.pumpScheduler()
	}()

	slog.Info("ai task runner start", "task_id", t.ID, "agent_id", t.AgentID, "steps", len(t.Steps))
	var lastOutput strings.Builder

	for i := range t.Steps {
		if ctx.Err() != nil {
			if s.taskPaused[t.ID] {
				s.finishPaused(t) // T4: 暂停取消 → 保留进度, 终态 paused 可续跑
			} else {
				s.finishCancelled(t)
			}
			return
		}
		step := &t.Steps[i]
		if step.Status == "done" || step.Status == "skipped" {
			continue
		}
		// T4: 步骤边界安全注入运行中插话(steer)——Runner 自有 goroutine, 无会话写竞态。
		// T5: 协作团队任务不消费会话历史, 跳过 steer 注入(避免往不存在的会话 Append 报错)。
		if t.TeamID == "" {
			s.injectPendingSteers(ctx, t)
		}

		step.Status, step.Note, step.Output = "running", "", ""
		pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
		s.saveTask(pctx, t)
		pcancel()
		s.emitTask(t.ID, "step", map[string]any{"index": i, "step": *step})

		output, err := s.runTaskStep(ctx, t, i)
		if ctx.Err() != nil {
			if s.taskPaused[t.ID] {
				s.finishPaused(t) // 暂停打断当前步: 步重置为 pending, 续跑时重跑该步
			} else {
				step.Status = "failed"
				step.Note = "已取消"
				s.finishCancelled(t)
			}
			return
		}
		if err != nil {
			step.Status = "failed"
			step.Note = err.Error()
			t.Status, t.Error = "failed", fmt.Sprintf("步骤 %d(%s) 失败: %v", i+1, step.Title, err)
			pctx2, pcancel2 := context.WithTimeout(context.Background(), 5*time.Second)
			s.saveTask(pctx2, t)
			pcancel2()
			s.emitTask(t.ID, "step", map[string]any{"index": i, "step": *step})
			s.emitTask(t.ID, "failed", map[string]string{"task_id": t.ID, "error": t.Error})
			slog.Warn("ai task step failed", "task_id", t.ID, "step", i, "err", err)
			return
		}
		step.Status, step.Note = "done", truncateNote(output)
		step.Output = output // T4: 轨迹落盘(断线重连补放)
		lastOutput.Reset()
		lastOutput.WriteString(output)
		pctx3, pcancel3 := context.WithTimeout(context.Background(), 5*time.Second)
		s.saveTask(pctx3, t)
		pcancel3()
		s.emitTask(t.ID, "step", map[string]any{"index": i, "step": *step})
	}

	t.Status = "completed"
	t.ResultSummary = truncateNote(lastOutput.String())
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	s.saveTask(pctx, t)
	pcancel()
	s.emitTask(t.ID, "status", map[string]string{"status": "completed", "task_id": t.ID})
	s.emitTask(t.ID, "done", map[string]string{"task_id": t.ID, "summary": t.ResultSummary})
	slog.Info("ai task completed", "task_id", t.ID)
}

// runTaskStep 执行单步骤: 任务上下文注入 system + 工具作用域(taskID), ChatStream 流式收集输出并转发事件。
func (s *Service) runTaskStep(ctx context.Context, t *AITask, idx int) (string, error) {
	if t.TeamID != "" {
		return s.runTaskStepTeam(ctx, t, idx)
	}
	step := t.Steps[idx]
	sys := "你是任务执行器, 正在后台为用户完成一个多步骤任务, 每个步骤都要产出可交付的成果。\n" +
		"任务目标: " + t.Goal + "\n" +
		"全部步骤:\n" + renderStepList(t.Steps) + "\n" +
		"产物要求: 需要交付文档/报告/清单时, 必须调用 doc_write 把成品写入任务工作区(用户在「产物」面板查看下载), 正文里只给简要总结;" +
		"新步骤开始前可先 doc_list 查看已有产物, 避免重复; 有长期复用价值的成果可用 doc_to_knowledge 沉淀为知识库草稿。\n" +
		"遇到方案歧义、需求不明确或需要用户拍板的选择时, 调用 task_confirm 向用户发起确认, 不要自行猜测; 步骤状态可经 task_update_step 更新。\n" +
		"要求: 聚焦完成当前步骤, 可调用可用工具(查询/检索/脚本等)获取真实数据; 完成后用 Markdown 输出本步骤成果总结。"
	prompt := fmt.Sprintf("请执行步骤 %d/%d: %s", idx+1, len(t.Steps), step.Title)

	// 任务作用域: doc_* 工具经 ctx 定位本任务工作区; 审批闸门: 引擎执行工具前按
	// permission_mode × risk 判定(readonly 拒写 / standard+auto 高危挂起等批复, 见 taskpolicy.go)。
	ctx = withTaskScope(mcp.WithTaskToolGate(ctx, func(gctx context.Context, tool string, args map[string]any) (bool, string) {
		return s.enforceTaskPolicy(gctx, t, tool, args)
	}), t.ID)
	history := []llm.Message{{Role: "system", Content: sys}}
	ch, _, err := s.Engine.ChatStream(ctx, t.AgentID, prompt, history, false, t.ConversationID, false, "")
	if err != nil {
		return "", err
	}

	var out strings.Builder
	for c := range ch {
		if ctx.Err() != nil {
			return out.String(), ctx.Err()
		}
		switch c.Type {
		case "token":
			out.WriteString(c.Content)
			step.Output = out.String() // T4: 实时轨迹(活体指针, 供断线重连补放)
			s.emitTask(t.ID, "token", map[string]any{"index": idx, "delta": c.Content})
		case "thinking":
			s.emitTask(t.ID, "thinking", map[string]any{"index": idx, "delta": c.Content})
		case "tool_event":
			s.emitTask(t.ID, "tool", map[string]any{"index": idx, "content": c.Content})
		case "error":
			return out.String(), fmt.Errorf("%s", chatFriendlyError(c.Content))
		}
	}
	if strings.TrimSpace(out.String()) == "" {
		return "", fmt.Errorf("步骤未产生任何输出(模型未返回内容), 请重试或检查模型配置")
	}
	return out.String(), nil
}

// runTaskStepTeam T5: 把步骤作为 message 派发给协作团队并行执行(supervisorPlan 拆解 + 成员并行 + 汇总定稿)。
// 团队 worker 经传入的 withTaskScope ctx 继承任务作用域与审批闸门(team.go 的 withAgentID 派生自本 ctx)。
// 团队各成员事件桥接为任务 SSE token 事件(同一步骤 idx), 中栏直接展示并行过程; 返回完整轨迹作为步骤输出(断线重连补放一致)。
func (s *Service) runTaskStepTeam(ctx context.Context, t *AITask, idx int) (string, error) {
	step := t.Steps[idx]
	team, ok := s.Engine.GetTeam(t.TeamID)
	if !ok || team == nil {
		return "", fmt.Errorf("team %s not found", t.TeamID)
	}
	message := fmt.Sprintf("任务目标: %s\n\n请各位成员协作完成以下步骤: %s\n(任务共 %d 步, 当前第 %d 步)",
		t.Goal, step.Title, len(t.Steps), idx+1)

	// 任务作用域注入(同单 agent 分支): worker 调 doc_write 落本任务工作区, 高危工具走 permission_mode 审批。
	ctx = withTaskScope(mcp.WithTaskToolGate(ctx, func(gctx context.Context, tool string, args map[string]any) (bool, string) {
		return s.enforceTaskPolicy(gctx, t, tool, args)
	}), t.ID)

	var out strings.Builder
	var mu sync.Mutex
	emit := func(ev model.TeamStreamEvent) {
		var delta string
		switch ev.Type {
		case "agent_start":
			delta = fmt.Sprintf("\n▶ **%s**（%s）开始\n", ev.AgentName, ev.Role)
		case "token":
			delta = fmt.Sprintf("【%s】%s\n", ev.AgentName, ev.Content)
		case "tool_call":
			delta = fmt.Sprintf("　🔧 %s(%s)\n", ev.ToolName, ev.ToolArgs)
		case "tool_result":
			r := ev.ToolResult
			if len(r) > 400 {
				r = r[:400] + "…"
			}
			delta = fmt.Sprintf("　↩ %s\n", r)
		case "agent_end":
			delta = fmt.Sprintf("✅ **%s** 完成\n", ev.AgentName)
		case "error":
			delta = fmt.Sprintf("⚠️ %s: %s\n", ev.AgentName, ev.Error)
		case "final":
			delta = fmt.Sprintf("\n## 团队汇总\n%s\n", ev.Content)
		default:
			return
		}
		mu.Lock()
		out.WriteString(delta)
		mu.Unlock()
		s.emitTask(t.ID, "token", map[string]any{"index": idx, "delta": delta})
	}
	final, _, err := s.Engine.RunTeamChat(ctx, team, message, nil, emit, "")
	if err != nil {
		return out.String(), err
	}
	if strings.TrimSpace(final) == "" {
		return out.String(), fmt.Errorf("团队未产出最终答复(可能全员失败)")
	}
	return out.String(), nil
}

func (s *Service) finishCancelled(t *AITask) {
	t.Status = "cancelled"
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	s.saveTask(pctx, t)
	pcancel()
	s.emitTask(t.ID, "status", map[string]string{"status": "cancelled", "task_id": t.ID})
	s.emitTask(t.ID, "cancelled", map[string]string{"task_id": t.ID})
}

// finishPaused T4: 暂停终态(保留进度, 当前 running 步重置为 pending 待续跑)。
func (s *Service) finishPaused(t *AITask) {
	t.Status = "paused"
	for i := range t.Steps {
		if t.Steps[i].Status == "running" {
			t.Steps[i].Status = "pending"
			t.Steps[i].Output = ""
		}
	}
	s.taskMu.Lock()
	s.taskPaused[t.ID] = false
	s.taskMu.Unlock()
	pctx, pcancel := context.WithTimeout(context.Background(), 5*time.Second)
	s.saveTask(pctx, t)
	pcancel()
	s.emitTask(t.ID, "status", map[string]string{"status": "paused", "task_id": t.ID})
	s.emitTask(t.ID, "paused", map[string]string{"task_id": t.ID})
}

// injectPendingSteers T4: 把用户插话(steer)队列追加为会话 user 消息, 模型在下一步可见。
// 仅在 Runner goroutine 内(步骤边界)调用, 与自身会话写无竞态; 空队列直接返回。
func (s *Service) injectPendingSteers(ctx context.Context, t *AITask) {
	if t.ConversationID == "" {
		return
	}
	s.taskMu.Lock()
	pending := s.taskSteers[t.ID]
	if len(pending) == 0 {
		s.taskMu.Unlock()
		return
	}
	delete(s.taskSteers, t.ID)
	s.taskMu.Unlock()

	for _, st := range pending {
		msg := llm.Message{Role: "user", Content: "[用户插话/中途指令] " + st.Message}
		if err := s.Engine.AppendMessage(ctx, t.ConversationID, msg); err != nil {
			slog.Warn("ai task steer inject failed", "task_id", t.ID, "err", err)
			continue
		}
	}
	s.emitTask(t.ID, "steer_applied", map[string]any{"task_id": t.ID, "count": len(pending)})
}

func renderStepList(steps []TaskStep) string {
	var b strings.Builder
	for i, st := range steps {
		mark := map[string]string{"done": "[已完成]", "running": "[进行中]", "failed": "[失败]"}[st.Status]
		if mark == "" {
			mark = "[待执行]"
		}
		fmt.Fprintf(&b, "%d. %s %s\n", i+1, mark, st.Title)
	}
	return b.String()
}

func truncateNote(s string) string {
	r := []rune(strings.TrimSpace(s))
	if len(r) > 2000 {
		return string(r[:2000]) + "…"
	}
	return string(r)
}

// ——— HTTP Handlers ———

// handleCreateTask 创建任务(默认单步骤=目标本身; 前端可传 steps 细化)。
func (s *Service) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	userID := auth.UserIDFromContext(r.Context())
	var req struct {
		Goal           string `json:"goal"`
		Title          string `json:"title"`
		AgentID        string `json:"agent_id"`
		TeamID         string `json:"team_id"`
		Priority       int    `json:"priority"`
		PermissionMode string `json:"permission_mode"`
		Steps          []struct {
			Title string `json:"title"`
		} `json:"steps"`
		AutoStart bool `json:"auto_start"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid body")
		return
	}
	goal := strings.TrimSpace(req.Goal)
	if goal == "" {
		httputil.WriteError(w, http.StatusBadRequest, "goal required")
		return
	}
	mode := strings.TrimSpace(req.PermissionMode)
	if mode == "" {
		mode = "standard"
	}
	if !validPermissionModes[mode] {
		httputil.WriteError(w, http.StatusBadRequest, "invalid permission_mode: must be readonly|standard|auto|full_auto")
		return
	}
	title := strings.TrimSpace(req.Title)
	if title == "" {
		title = truncateNote(goal)
		if len([]rune(title)) > 60 {
			title = string([]rune(title)[:60])
		}
	}

	teamID := strings.TrimSpace(req.TeamID)
	if teamID != "" {
		if _, ok := s.Engine.GetTeam(teamID); !ok {
			httputil.WriteError(w, http.StatusBadRequest, "team_id not found")
			return
		}
	}

	// T6: 调度优先级校验(未传/非法回落普通)
	priority := req.Priority
	if !validPriorities[priority] {
		priority = taskPriorityNormal
	}

	t := &AITask{
		ID: uuid.New().String(), TenantID: tenantID, AgentID: strings.TrimSpace(req.AgentID), TeamID: teamID,
		Title: title, Goal: goal, Status: "planning", PermissionMode: mode, Priority: priority,
		Steps: []TaskStep{}, CreatedBy: userID, CreatedAt: time.Now(),
	}
	if len(req.Steps) > 0 {
		for _, rs := range req.Steps {
			if title := strings.TrimSpace(rs.Title); title != "" {
				t.Steps = append(t.Steps, TaskStep{ID: uuid.New().String()[:8], Title: title, Status: "pending"})
			}
		}
	}
	if len(t.Steps) == 0 {
		t.Steps = []TaskStep{{ID: uuid.New().String()[:8], Title: "完成任务目标", Status: "pending"}}
	}
	s.saveTask(r.Context(), t)
	s.emitTask(t.ID, "status", map[string]string{"status": "planning", "task_id": t.ID})

	if req.AutoStart {
		// T3: 立即启动 = 先跑规划轮产 checklist, 成功后自动开始执行(规划失败留在 planning 态等人工介入)
		go s.planAndStart(t)
		httputil.WriteJSON(w, http.StatusCreated, t)
		return
	}
	httputil.WriteJSON(w, http.StatusCreated, t)
}

func (s *Service) handleStartTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	switch t.Status {
	case "running", "waiting_approval":
		httputil.WriteError(w, http.StatusConflict, "task already running")
		return
	case "completed", "cancelled":
		httputil.WriteError(w, http.StatusBadRequest, "task is finished; create a new task to rerun")
		return
	}
	// T6: 提交给调度器——并发未满则立即执行, 否则入队(status=queued)等槽位
	started, queued := s.submitTask(r.Context(), t)
	if !started && !queued {
		httputil.WriteError(w, http.StatusConflict, "task already running")
		return
	}
	updated := s.getTask(context.Background(), t.ID, tenantID)
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (s *Service) handleCancelTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "running" && t.Status != "waiting_approval" && t.Status != "planning" && t.Status != "queued" {
		httputil.WriteError(w, http.StatusBadRequest, "task is not cancellable in status "+t.Status)
		return
	}
	// T6: 排队中的任务无 runner, 直接从队列摘除再落终态
	if t.Status == "queued" {
		s.taskMu.Lock()
		s.dequeue(t.ID)
		s.taskMu.Unlock()
		s.finishCancelled(t)
		updated := s.getTask(context.Background(), t.ID, tenantID)
		httputil.WriteJSON(w, http.StatusOK, updated)
		return
	}
	s.taskMu.Lock()
	cancelFn := s.taskCancels[t.ID]
	s.taskMu.Unlock()
	if cancelFn != nil {
		cancelFn() // runner 检测到 ctx 取消后自行落库+广播
	} else {
		s.finishCancelled(t) // planning 态无 runner
	}
	updated := s.getTask(context.Background(), t.ID, tenantID)
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// handleSteerTask POST /api/v1/ai/tasks/{id}/steer — T4 运行中插话。
// 指令入队(taskSteers), Runner 在步骤边界安全注入会话; 同时落库 Steers 供重连补放。
func (s *Service) handleSteerTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "running" && t.Status != "paused" && t.Status != "waiting_approval" {
		httputil.WriteError(w, http.StatusConflict, "仅运行/暂停/待审批状态可插话")
		return
	}
	var req struct {
		Message string `json:"message"`
	}
	if err := httputil.BindJSON(r, &req); err != nil || strings.TrimSpace(req.Message) == "" {
		httputil.WriteError(w, http.StatusBadRequest, "message required")
		return
	}
	ent := steerEntry{ID: uuid.New().String()[:8], Message: strings.TrimSpace(req.Message), CreatedAt: time.Now()}
	s.taskMu.Lock()
	s.taskSteers[t.ID] = append(s.taskSteers[t.ID], ent)
	// 同步到 Runner 活体指针: 否则 runner 后续 saveTask 会用自身副本把 Steers 覆盖回空。
	if live := s.taskLive[t.ID]; live != nil {
		live.Steers = append(live.Steers, ent)
	}
	t.Steers = append(t.Steers, ent)
	s.taskMu.Unlock()
	s.saveTask(persistCtx(), t)
	s.emitTask(t.ID, "steer", ent) // 前端即时展示插话气泡
	httputil.WriteJSON(w, http.StatusOK, ent)
}

// handlePauseTask POST /api/v1/ai/tasks/{id}/pause — T4 暂停(取消 in-flight, 保留进度, 终态 paused)。
func (s *Service) handlePauseTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "running" && t.Status != "waiting_approval" {
		httputil.WriteError(w, http.StatusConflict, "仅运行/待审批状态可暂停")
		return
	}
	s.taskMu.Lock()
	s.taskPaused[t.ID] = true
	cancelFn := s.taskCancels[t.ID]
	s.taskMu.Unlock()
	if cancelFn != nil {
		cancelFn() // Runner 检测 ctx 取消 → finishPaused
	}
	updated := s.getTask(context.Background(), t.ID, tenantID)
	httputil.WriteJSON(w, http.StatusOK, updated)
}

// handleResumeTask POST /api/v1/ai/tasks/{id}/resume — T4 从暂停恢复(续跑未完成步骤)。
func (s *Service) handleResumeTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "paused" {
		httputil.WriteError(w, http.StatusConflict, "仅暂停状态可恢复")
		return
	}
	// T6: 恢复也走调度器(可能因并发已满而重新排队)
	started, queued := s.submitTask(r.Context(), t)
	if !started && !queued {
		httputil.WriteError(w, http.StatusConflict, "task already running")
		return
	}
	updated := s.getTask(context.Background(), t.ID, tenantID)
	httputil.WriteJSON(w, http.StatusOK, updated)
}

func (s *Service) handleListTasks(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	httputil.WriteJSON(w, http.StatusOK, s.listTasks(r.Context(), tenantID, r.URL.Query().Get("status")))
}

func (s *Service) handleGetTask(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, t)
}

// handleTaskEvents SSE 事件流。连接建立即下发当前任务快照(解决断线重连时的状态恢复,
// T1 无需逐 token 回放; 逐事件补放在 T4 结合 Trajectory 完善)。
func (s *Service) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	id := r.PathValue("id")
	t := s.getTask(r.Context(), id, tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httputil.WriteError(w, http.StatusInternalServerError, "streaming not supported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ch := s.taskBus.subscribe(id)
	defer s.taskBus.unsubscribe(id, ch)

	// 快照: 优先活体指针(含运行中实时轨迹 Output), 否则取存储快照(T4 断线重连补放)。
	// handleTaskEvents SSE 连接即下发, 已落库/进行中的步骤正文一次给全, 无需逐 token 回放。
	s.taskMu.Lock()
	live, hasLive := s.taskLive[id]
	s.taskMu.Unlock()
	var snapTask *AITask
	if hasLive {
		cp := *live
		snapTask = &cp
	} else {
		snapTask = s.getTask(context.Background(), id, tenantID)
	}
	snap, _ := json.Marshal(snapTask)
	fmt.Fprintf(w, "event: snapshot\ndata: %s\n\n", snap)
	flusher.Flush()

	keepalive := time.NewTicker(15 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case ev := <-ch:
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Type, ev.Data)
			flusher.Flush()
			switch ev.Type {
			case "done", "failed", "cancelled":
				return // 终态后关闭连接, 前端刷新任务列表
			}
		}
	}
}

// registerTaskRoutes 任务级 Agent 路由(由 RegisterRoutes 调用)。
func (s *Service) registerTaskRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/ai/tasks", s.handleCreateTask)
	mux.HandleFunc("GET /api/v1/ai/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/v1/ai/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/start", s.handleStartTask)
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/cancel", s.handleCancelTask)
	mux.HandleFunc("GET /api/v1/ai/tasks/{id}/events", s.handleTaskEvents)
	// 产物按任务维度查询(复用 ai_chat_artifacts, task 归档在 T2 打通写入)
	mux.HandleFunc("GET /api/v1/ai/tasks/{id}/artifacts", s.handleListTaskArtifacts)
	// T3: 规划轮(AI 生成/手动编辑 checklist) + 中途审批批复
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/plan", s.handlePlanTask)
	mux.HandleFunc("PUT /api/v1/ai/tasks/{id}/plan", s.handleUpdatePlan)
	mux.HandleFunc("GET /api/v1/ai/tasks/{id}/approvals", s.handleListTaskApprovals)
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/approvals/{call_id}", s.handleTaskApproval)
	// T4: 运行中插话 + 暂停/恢复
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/steer", s.handleSteerTask)
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/pause", s.handlePauseTask)
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/resume", s.handleResumeTask)
	// T6: 多任务并行调度(并发上限/等待队列/优先级)
	mux.HandleFunc("GET /api/v1/ai/tasks/queue", s.handleTaskQueue)
	mux.HandleFunc("POST /api/v1/ai/tasks/{id}/priority", s.handleSetTaskPriority)
}

// handleListTaskArtifacts 任务产物列表(T1 先按 conversation_id 关联查询; T2 doc_write 直写 task 命名空间)。
func (s *Service) handleListTaskArtifacts(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	t := s.getTask(r.Context(), r.PathValue("id"), tenantID)
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.ConversationID == "" || s.db == nil {
		httputil.WriteJSON(w, http.StatusOK, []ChatArtifact{})
		return
	}
	rows, err := s.db.QueryContext(r.Context(),
		`SELECT id, tenant_id, conversation_id, message_id, file_name, file_type, file_size, object_key, created_at
		 FROM ai_chat_artifacts WHERE conversation_id=$1 AND tenant_id=$2 ORDER BY created_at DESC`, t.ConversationID, tenantID)
	if err != nil {
		httputil.WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	defer rows.Close()
	out := []ChatArtifact{}
	for rows.Next() {
		var a ChatArtifact
		if err := rows.Scan(&a.ID, &a.TenantID, &a.ConversationID, &a.MessageID, &a.FileName, &a.FileType, &a.FileSize, &a.ObjectKey, &a.CreatedAt); err != nil {
			continue
		}
		out = append(out, a)
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}
