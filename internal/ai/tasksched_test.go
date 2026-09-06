package ai

import (
	"context"
	"testing"
	"time"
)

// T6 多任务并行调度单测: 并发满时入队、优先级排序、队列摘除与位次。

func newSchedService() *Service {
	return &Service{
		taskRunning: map[string]bool{},
		taskQueue:   []string{},
		taskLive:    map[string]*AITask{},
		taskPaused:  map[string]bool{},
		taskCancels: map[string]context.CancelFunc{},
		taskSteers:  map[string][]steerEntry{},
	}
}

func testTask(id string, priority int) *AITask {
	return &AITask{
		ID: id, TenantID: "tenant", Title: "task " + id, Status: "planning",
		Priority: priority, CreatedAt: time.Now(), Steps: []TaskStep{{ID: "s1", Title: "step", Status: "pending"}},
	}
}

// TestSubmitTaskQueuesWhenFull 并发槽占满时, 提交应入队而非直接启动。
func TestSubmitTaskQueuesWhenFull(t *testing.T) {
	t.Setenv("OPSORIGIN_AI_TASK_MAX_CONCURRENT", "1")
	s := newSchedService()
	s.taskRunning["busy-1"] = true // 占满唯一槽位

	tk := testTask("t-queued", taskPriorityNormal)
	started, queued := s.submitTask(context.Background(), tk)
	if started {
		t.Fatal("并发已满时不应直接启动")
	}
	if !queued {
		t.Fatal("并发已满时应入队")
	}
	if tk.Status != "queued" {
		t.Errorf("status = %q, want queued", tk.Status)
	}
	if got := s.queuePosition("t-queued"); got != 1 {
		t.Errorf("queuePosition = %d, want 1", got)
	}
}

// TestSubmitTaskStartsWhenSlotFree 有空位时应立即启动(不进队列)。
func TestSubmitTaskStartsWhenSlotFree(t *testing.T) {
	t.Setenv("OPSORIGIN_AI_TASK_MAX_CONCURRENT", "2")
	s := newSchedService()
	s.taskRunning["busy-1"] = true // 1/2, 仍有空位

	// 用无步骤任务: runTask 立刻走到 completed, 不触碰 nil Engine(裸 Service 无 LLM)。
	tk := testTask("t-start", taskPriorityNormal)
	tk.Steps = nil
	started, queued := s.submitTask(context.Background(), tk)
	if queued {
		t.Fatal("有空位时不应入队")
	}
	if !started {
		t.Error("有空位时应立即启动")
	}
	if len(s.taskQueue) != 0 {
		t.Errorf("队列应为空, got %d", len(s.taskQueue))
	}
	// 等待 runner goroutine 结束, 避免断言后仍在写共享状态
	for i := 0; i < 100; i++ {
		s.taskMu.Lock()
		_, busy := s.taskRunning["t-start"]
		s.taskMu.Unlock()
		if !busy {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestQueueOrderByPriority 同槽竞争时, 高优先级(数值小)先出队; 同优先级按创建时间。
func TestQueueOrderByPriority(t *testing.T) {
	s := newSchedService()
	base := time.Now()
	s.taskLive["low"] = &AITask{ID: "low", Priority: taskPriorityLow, CreatedAt: base}
	s.taskLive["high"] = &AITask{ID: "high", Priority: taskPriorityHigh, CreatedAt: base.Add(time.Minute)}
	s.taskLive["normal"] = &AITask{ID: "normal", Priority: taskPriorityNormal, CreatedAt: base}

	if !s.queueLess("high", "low") {
		t.Error("high 应排在 low 之前")
	}
	if !s.queueLess("normal", "low") {
		t.Error("normal 应排在 low 之前")
	}
	if s.queueLess("low", "high") {
		t.Error("low 不应排在 high 之前")
	}
	// 同优先级: 创建早的在前
	s.taskLive["normal2"] = &AITask{ID: "normal2", Priority: taskPriorityNormal, CreatedAt: base.Add(time.Second)}
	if !s.queueLess("normal", "normal2") {
		t.Error("同优先级应按创建时间升序")
	}
}

// TestDequeueAndPosition 排队任务被取消后应从队列摘除, 后续位次前移。
func TestDequeueAndPosition(t *testing.T) {
	s := newSchedService()
	s.taskQueue = []string{"a", "b", "c"}
	if got := s.queuePosition("b"); got != 2 {
		t.Errorf("b position = %d, want 2", got)
	}
	s.dequeue("a")
	if got := s.queuePosition("b"); got != 1 {
		t.Errorf("摘除 a 后 b position = %d, want 1", got)
	}
	if s.inQueue("a") {
		t.Error("a 应已出队")
	}
	s.dequeue("not-exist") // 幂等, 不应 panic
	if len(s.taskQueue) != 2 {
		t.Errorf("队列长度 = %d, want 2", len(s.taskQueue))
	}
}

// TestMaxConcurrentTasks 并发上限: 环境变量优先, 非法值回落默认 3。
func TestMaxConcurrentTasks(t *testing.T) {
	s := newSchedService()
	t.Setenv("OPSORIGIN_AI_TASK_MAX_CONCURRENT", "7")
	if got := s.maxConcurrentTasks(); got != 7 {
		t.Errorf("maxConcurrent = %d, want 7", got)
	}
	t.Setenv("OPSORIGIN_AI_TASK_MAX_CONCURRENT", "0")
	if got := s.maxConcurrentTasks(); got != 3 {
		t.Errorf("非法值应回落默认 3, got %d", got)
	}
	t.Setenv("OPSORIGIN_AI_TASK_MAX_CONCURRENT", "abc")
	if got := s.maxConcurrentTasks(); got != 3 {
		t.Errorf("非数字应回落默认 3, got %d", got)
	}
}
