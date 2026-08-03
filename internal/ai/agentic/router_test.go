package agentic

import (
	"testing"
	"time"

	"github.com/opsorigin/core/pkg/llm"
)

// TestRouterCandidateHealth 验证候选过滤: 不健康 provider 被排除, 冷却期后恢复。
func TestRouterCandidateHealth(t *testing.T) {
	r := NewModelRouter(llm.Config{Model: "m-main", BaseURL: "http://x", Timeout: time.Second},
		[]ProviderConfig{
			{Name: "p1", Model: "m1", BaseURL: "http://p1", Weight: 80},
			{Name: "p2", Model: "m2", BaseURL: "http://p2", Weight: 50},
		})
	// 三个 provider: default(100), p1(80), p2(50)
	if got := len(r.Models()); got != 3 {
		t.Fatalf("expected 3 providers, got %d", got)
	}

	now := time.Now()
	// 标记权重最高的 p1 不健康(冷却中) → candidates 应排除 p1
	for _, p := range r.providers {
		if p.Name == "p1" {
			p.markFailed(now)
		}
	}
	cands := r.candidates(now, "")
	foundP1 := false
	for _, p := range cands {
		if p.Name == "p1" {
			foundP1 = true
		}
	}
	if foundP1 {
		t.Fatal("unhealthy provider p1 should be excluded from candidates")
	}
	if len(cands) != 2 {
		t.Fatalf("expected 2 candidates (default+p2), got %d", len(cands))
	}
	// 排序: default(100) 应在 p2(50) 前
	if cands[0].Name != "default" || cands[1].Name != "p2" {
		t.Fatalf("unexpected candidate order: %v", []string{cands[0].Name, cands[1].Name})
	}

	// 冷却期过后 p1 恢复参与路由
	after := now.Add(providerCooldown + time.Second)
	cands2 := r.candidates(after, "")
	if len(cands2) != 3 {
		t.Fatalf("expected 3 candidates after cooldown, got %d", len(cands2))
	}
}

// TestRouterModelMatch 验证按模型名/ provider 名匹配候选。
func TestRouterModelMatch(t *testing.T) {
	r := NewModelRouter(llm.Config{Model: "m-main", BaseURL: "http://x", Timeout: time.Second},
		[]ProviderConfig{{Name: "deepseek", Model: "deepseek-chat", BaseURL: "http://ds", Weight: 80}})

	// 请求指定 provider 名 → 只匹配该 provider
	cands := r.candidates(time.Now(), "deepseek")
	if len(cands) != 1 || cands[0].Name != "deepseek" {
		t.Fatalf("expected only deepseek provider matched, got %d", len(cands))
	}
	// 请求指定具体模型名(与 provider 默认模型一致) → 匹配
	cands2 := r.candidates(time.Now(), "deepseek-chat")
	if len(cands2) != 1 || cands2[0].Name != "deepseek" {
		t.Fatalf("expected deepseek matched by model name, got %d", len(cands2))
	}
	// 请求指定未知模型 → 所有 provider 都不匹配
	cands3 := r.candidates(time.Now(), "unknown-model")
	if len(cands3) != 0 {
		t.Fatalf("expected no candidates for unknown model, got %d", len(cands3))
	}
}