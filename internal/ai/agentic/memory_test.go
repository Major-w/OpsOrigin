package agentic

import (
	"strings"
	"testing"
	"time"
)

// TestMemoryAddRetrieve 添加记忆 -> 按关键词检索命中 + HitCount 累计。
func TestMemoryAddRetrieve(t *testing.T) {
	e := newEngineForFinetune()
	m, err := e.AddMemory(&ExperienceMemory{
		TenantID:   "t1",
		AgentID:    "a1",
		Category:   "数据库",
		Problem:    "数据库连接数接近上限导致服务变慢",
		Resolution: "扩容连接池并排查慢查询",
		Outcome:    "succeeded",
	})
	if err != nil {
		t.Fatalf("add memory: %v", err)
	}
	if len(m.Keywords) == 0 {
		t.Fatal("expected auto keywords extracted")
	}

	// 命中检索
	mems := e.RetrieveMemories("t1", "a1", "数据库连接数满了怎么办")
	if len(mems) != 1 {
		t.Fatalf("expected 1 memory hit, got %d", len(mems))
	}
	// 命中统计(异步 goroutine, 等待)
	waitFor(t, func() bool {
		got := e.ListMemories("t1", MemoryFilter{})
		return len(got) > 0 && got[0].HitCount >= 1
	})

	// 其他 Agent 查不到(绑定 a1)
	if got := e.RetrieveMemories("t1", "a2", "数据库连接数"); len(got) != 0 {
		t.Fatal("agent a2 should not see a1's memory")
	}
	// 通用记忆(agent_id 空)所有 Agent 可见
	_, _ = e.AddMemory(&ExperienceMemory{TenantID: "t1", Problem: "磁盘空间不足", Resolution: "清理日志归档"})
	if got := e.RetrieveMemories("t1", "a2", "磁盘空间告急"); len(got) != 1 {
		t.Fatal("global memory should be visible to all agents")
	}
}

// TestMemoryIdempotent 同 agent+problem 重复添加 -> 更新而非新增。
func TestMemoryIdempotent(t *testing.T) {
	e := newEngineForFinetune()
	m1, _ := e.AddMemory(&ExperienceMemory{TenantID: "t1", AgentID: "a1", Problem: "CPU 过载", Resolution: "定位高消耗进程"})
	m2, _ := e.AddMemory(&ExperienceMemory{TenantID: "t1", AgentID: "a1", Problem: "CPU 过载", Resolution: "扩容 CPU"})
	if m1.ID != m2.ID {
		t.Fatalf("expected same id for idempotent add, got %s vs %s", m1.ID, m2.ID)
	}
	if m2.Resolution != "扩容 CPU" {
		t.Fatal("expected resolution updated")
	}
	if len(e.ListMemories("t1", MemoryFilter{})) != 1 {
		t.Fatal("expected only 1 memory")
	}
}

// TestMemoriesPrompt 记忆渲染为 system 片段(含处置方案与结果)。
func TestMemoriesPrompt(t *testing.T) {
	ms := []*ExperienceMemory{
		{Category: "数据库", Problem: "连接数爆满", Resolution: "扩容连接池", Outcome: "succeeded"},
		{Problem: "误报", Resolution: "忽略", Outcome: "false_positive"},
	}
	p := memoriesPrompt(ms)
	if !strings.Contains(p, "扩容连接池") || !strings.Contains(p, "成功") || !strings.Contains(p, "误报") {
		t.Fatalf("prompt missing expected parts: %s", p)
	}
}

// TestMemoryDelete 删除记忆。
func TestMemoryDelete(t *testing.T) {
	e := newEngineForFinetune()
	m, _ := e.AddMemory(&ExperienceMemory{TenantID: "t1", Problem: "网络丢包", Resolution: "检查交换机"})
	if !e.DeleteMemory("t1", m.ID) {
		t.Fatal("delete failed")
	}
	if e.DeleteMemory("t1", m.ID) {
		t.Fatal("delete should fail for missing")
	}
	if len(e.ListMemories("t1", MemoryFilter{})) != 0 {
		t.Fatal("expected empty after delete")
	}
}

// TestDefaultKeywords 关键词抽取: 中文滑窗 + 英文词。
func TestDefaultKeywords(t *testing.T) {
	kws := defaultKeywords("数据库连接数过高 mysql connections too many")
	joined := strings.Join(kws, "|")
	if !strings.Contains(joined, "mysql") || !strings.Contains(joined, "数据库") {
		t.Fatalf("keywords unexpected: %v", kws)
	}
}

var _ = time.Now
