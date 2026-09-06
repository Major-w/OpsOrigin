package ai

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// 任务调度(T6): 全局并发上限 + 优先级等待队列。
//
// 背景: T1–T5 每个任务各自起 goroutine, 无并发约束——用户批量创建任务会同时打满
// LLM 配额与连接池。调度器把「启动」收敛为「提交」: 并发未满则立即执行, 否则置
// queued 入队; 任一任务到达终态后 pump 出队下一个(优先级升序 + 创建时间升序)。
//
// 计数口径: 用 taskRunning 占位(startTask 即置位), 而非 taskCancels——后者在
// runTask 内部才注册, 与 pump 存在竞态窗口。
//
// 持久化: 队列状态以 DB status='queued' 落库, 内存队列仅作索引; 服务重启由
// recoverTaskQueue 从 DB 重建, 不会丢排队任务。

const (
	taskPriorityHigh   = 1
	taskPriorityNormal = 5
	taskPriorityLow    = 9
)

var validPriorities = map[int]bool{taskPriorityHigh: true, taskPriorityNormal: true, taskPriorityLow: true}

// getTaskRaw 按 ID 取任务(不含租户过滤)。仅供调度器内部使用——队列里只有任务 ID,
// 调度动作本身已由入队前的鉴权保证, 无需二次租户匹配。
func (s *Service) getTaskRaw(ctx context.Context, id string) *AITask {
	if s.db == nil {
		taskMemStore.Lock()
		defer taskMemStore.Unlock()
		t := taskMemStore.m[id]
		if t == nil {
			return nil
		}
		cp := *t
		return &cp
	}
	t, err := scanTask(s.db.QueryRowContext(ctx,
		`SELECT `+taskSelectCols+` FROM ai_tasks WHERE id=$1`, id).Scan)
	if err != nil {
		return nil
	}
	return t
}

// maxConcurrent 同时执行的任务上限。环境变量优先, 缺省 3。
func (s *Service) maxConcurrentTasks() int {
	if v := os.Getenv("OPSORIGIN_AI_TASK_MAX_CONCURRENT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			return n
		}
	}
	return 3
}

// submitTask 调度入口: 并发未满则立即执行, 否则入队等待。
// 替代原 startTask 的直接调用, 语义从"必须立刻跑"变为"提交给调度器"。
// 返回 (started bool, queued bool); started=true 表示已开始执行。
func (s *Service) submitTask(ctx context.Context, t *AITask) (started, queued bool) {
	s.taskMu.Lock()
	running := len(s.taskRunning)
	limit := s.maxConcurrentTasks()
	if running >= limit {
		// 入队: 内存索引 + DB 状态
		if !s.inQueue(t.ID) {
			s.taskQueue = append(s.taskQueue, t.ID)
		}
		t.Status = "queued"
		s.taskMu.Unlock()
		t.UpdatedAt = time.Now()
		s.saveTask(ctx, t)
		pos := s.queuePosition(t.ID)
		s.emitTask(t.ID, "queued", map[string]any{
			"task_id": t.ID, "position": pos, "priority": t.Priority,
			"running": running, "max_concurrent": limit,
		})
		slog.Info("ai task queued", "task_id", t.ID, "position", pos, "running", running, "limit", limit)
		return false, true
	}
	s.taskMu.Unlock()
	return s.startTask(ctx, t), false
}

// pumpScheduler 出队调度: 并发有空位时, 按优先级依次启动排队任务。
// 在任务到达终态(completed/failed/cancelled/paused)后调用。
// 注意: 先在本方法内收齐待启动任务并释放锁, 再实际启动(runTask 会回调本方法, 持锁启动会死锁)。
func (s *Service) pumpScheduler() {
	s.taskMu.Lock()
	limit := s.maxConcurrentTasks()
	var toStart []*AITask
	for len(s.taskRunning)+len(toStart) < limit && len(s.taskQueue) > 0 {
		// 选优先级最高(数值最小)、同优先级创建最早的
		best := 0
		for i := 1; i < len(s.taskQueue); i++ {
			if s.queueLess(s.taskQueue[i], s.taskQueue[best]) {
				best = i
			}
		}
		id := s.taskQueue[best]
		s.taskQueue = append(s.taskQueue[:best], s.taskQueue[best+1:]...)
		t := s.taskLive[id]
		if t == nil {
			t = s.getTaskRaw(context.Background(), id)
		}
		if t == nil {
			continue // 任务已被删除
		}
		// 仅排队/规划态可启动; 终态(cancelled 等)直接丢弃
		if t.Status != "queued" && t.Status != "planning" {
			continue
		}
		toStart = append(toStart, t)
	}
	s.taskMu.Unlock()

	for _, t := range toStart {
		if s.startTask(context.Background(), t) {
			slog.Info("ai task dequeued", "task_id", t.ID, "priority", t.Priority)
		}
	}
}

// queueLess 队列排序: 优先级升序(1=高), 同优先级按创建时间升序(先来先服务)。
func (s *Service) queueLess(a, b string) bool {
	ta, tb := s.queueMeta(a), s.queueMeta(b)
	if ta.priority != tb.priority {
		return ta.priority < tb.priority
	}
	return ta.created.Before(tb.created)
}

type queueMetaT struct {
	priority int
	created  time.Time
}

// queueMeta 取队列排序元数据: 优先活体指针, 回落到 DB。调用方需持 taskMu(读 DB 除外)。
func (s *Service) queueMeta(id string) queueMetaT {
	if t := s.taskLive[id]; t != nil {
		p := t.Priority
		if p == 0 {
			p = taskPriorityNormal
		}
		return queueMetaT{priority: p, created: t.CreatedAt}
	}
	if t := s.getTaskRaw(context.Background(), id); t != nil {
		p := t.Priority
		if p == 0 {
			p = taskPriorityNormal
		}
		return queueMetaT{priority: p, created: t.CreatedAt}
	}
	return queueMetaT{priority: taskPriorityNormal, created: time.Now()}
}

func (s *Service) inQueue(id string) bool {
	for _, q := range s.taskQueue {
		if q == id {
			return true
		}
	}
	return false
}

// queuePosition 排队位次(1 起)。0 表示不在队列中。
func (s *Service) queuePosition(id string) int {
	for i, q := range s.taskQueue {
		if q == id {
			return i + 1
		}
	}
	return 0
}

// dequeue 从队列移除(取消排队任务时)。调用方需持 taskMu。
func (s *Service) dequeue(id string) {
	for i, q := range s.taskQueue {
		if q == id {
			s.taskQueue = append(s.taskQueue[:i], s.taskQueue[i+1:]...)
			return
		}
	}
}

// recoverTaskQueue 服务重启后从 DB 重建等待队列(内存队列丢失时的兜底)。
// 只恢复 status='queued' 的任务, 按优先级+创建时间排序。
func (s *Service) recoverTaskQueue() {
	if s.db == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM ai_tasks WHERE status='queued' ORDER BY priority ASC, created_at ASC`)
	if err != nil {
		slog.Warn("ai task queue recover failed", "err", err)
		return
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err == nil {
			ids = append(ids, id)
		}
	}
	s.taskMu.Lock()
	s.taskQueue = ids
	s.taskMu.Unlock()
	if len(ids) > 0 {
		slog.Info("ai task queue recovered", "queued", len(ids))
	}
}

// —— 端点 ——

// handleTaskQueue GET /api/v1/ai/tasks/queue — 调度器状态(并发上限/运行中/排队列表)。
func (s *Service) handleTaskQueue(w http.ResponseWriter, r *http.Request) {
	s.taskMu.Lock()
	limit := s.maxConcurrentTasks()
	running := len(s.taskRunning)
	type queuedItem struct {
		ID       string `json:"id"`
		Title    string `json:"title"`
		Priority int    `json:"priority"`
		Position int    `json:"position"`
		Status   string `json:"status"`
	}
	items := make([]queuedItem, 0, len(s.taskQueue))
	for i, id := range s.taskQueue {
		meta := s.queueMeta(id)
		title := ""
		if t := s.taskLive[id]; t != nil {
			title = t.Title
		} else if t := s.getTaskRaw(context.Background(), id); t != nil {
			title = t.Title
		}
		items = append(items, queuedItem{ID: id, Title: title, Priority: meta.priority, Position: i + 1, Status: "queued"})
	}
	// 运行中的任务列表(供前端展示"并发槽位被谁占用")
	runningIDs := make([]string, 0, len(s.taskRunning))
	for id := range s.taskRunning {
		runningIDs = append(runningIDs, id)
	}
	s.taskMu.Unlock()
	sort.Strings(runningIDs)

	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"max_concurrent": limit,
		"running":        running,
		"running_ids":    runningIDs,
		"queued":         items,
	})
}

// handleSetTaskPriority POST /api/v1/ai/tasks/{id}/priority — 调整优先级并重排队列。
// 仅排队/规划态可调(运行中的任务改优先级无意义, 会误导用户以为能插队)。
func (s *Service) handleSetTaskPriority(w http.ResponseWriter, r *http.Request) {
	t := s.getTask(r.Context(), r.PathValue("id"), auth.TenantFromContext(r.Context()))
	if t == nil {
		httputil.WriteError(w, http.StatusNotFound, "task not found")
		return
	}
	if t.Status != "queued" && t.Status != "planning" {
		httputil.WriteError(w, http.StatusConflict, "仅排队/待启动状态可调整优先级")
		return
	}
	var req struct {
		Priority int `json:"priority"`
	}
	if err := httputil.BindJSON(r, &req); err != nil || !validPriorities[req.Priority] {
		httputil.WriteError(w, http.StatusBadRequest, "priority must be 1(high) | 5(normal) | 9(low)")
		return
	}
	s.taskMu.Lock()
	t.Priority = req.Priority
	if lt := s.taskLive[t.ID]; lt != nil {
		lt.Priority = req.Priority
	}
	inQ := s.inQueue(t.ID)
	s.taskMu.Unlock()
	s.saveTask(r.Context(), t)
	pos := 0
	if inQ {
		s.taskMu.Lock()
		pos = s.queuePosition(t.ID)
		s.taskMu.Unlock()
	}
	s.emitTask(t.ID, "queued", map[string]any{
		"task_id": t.ID, "position": pos, "priority": t.Priority,
	})
	httputil.WriteJSON(w, http.StatusOK, map[string]any{"id": t.ID, "priority": t.Priority, "position": pos})
}
