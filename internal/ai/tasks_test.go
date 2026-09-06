package ai

import (
	"sync"
	"testing"
)

// TestFinishPaused 验证 T4 暂停终态: 保留已完成步骤, 当前 running 步重置为 pending, 清除暂停标记。
func TestFinishPaused(t *testing.T) {
	s := &Service{
		taskMu:     sync.Mutex{},
		taskPaused: map[string]bool{"t1": true},
		taskBus:    newTaskEventBus(),
	}
	task := &AITask{
		ID: "t1", Status: "running", TenantID: "default",
		Steps: []TaskStep{
			{ID: "s1", Title: "已完成步", Status: "done"},
			{ID: "s2", Title: "进行中步", Status: "running"},
		},
	}
	s.finishPaused(task)

	if task.Status != "paused" {
		t.Fatalf("status = %q, want paused", task.Status)
	}
	if task.Steps[0].Status != "done" {
		t.Errorf("已完成步骤应保持 done, got %q", task.Steps[0].Status)
	}
	if task.Steps[1].Status != "pending" {
		t.Errorf("进行中步骤应重置为 pending 待续跑, got %q", task.Steps[1].Status)
	}
	if s.taskPaused["t1"] {
		t.Error("taskPaused 标记应在暂停终态后清除(否则续跑会被误判为取消)")
	}
}
