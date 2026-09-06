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
	cands := r.candidates(now, "", "", "")
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
	cands2 := r.candidates(after, "", "", "")
	if len(cands2) != 3 {
		t.Fatalf("expected 3 candidates after cooldown, got %d", len(cands2))
	}
}

// TestRouterModelMatch 验证按模型名/ provider 名匹配候选。
func TestRouterModelMatch(t *testing.T) {
	r := NewModelRouter(llm.Config{Model: "m-main", BaseURL: "http://x", Timeout: time.Second},
		[]ProviderConfig{{Name: "deepseek", Model: "deepseek-chat", BaseURL: "http://ds", Weight: 80}})

	// 请求指定 provider 名 → 只匹配该 provider
	cands := r.candidates(time.Now(), "deepseek", "", "")
	if len(cands) != 1 || cands[0].Name != "deepseek" {
		t.Fatalf("expected only deepseek provider matched, got %d", len(cands))
	}
	// 请求指定具体模型名(与 provider 默认模型一致) → 匹配
	cands2 := r.candidates(time.Now(), "deepseek-chat", "", "")
	if len(cands2) != 1 || cands2[0].Name != "deepseek" {
		t.Fatalf("expected deepseek matched by model name, got %d", len(cands2))
	}
	// 请求指定未知模型 → 所有 provider 都不匹配
	cands3 := r.candidates(time.Now(), "unknown-model", "", "")
	if len(cands3) != 0 {
		t.Fatalf("expected no candidates for unknown model, got %d", len(cands3))
	}
}

// TestRouterHotUpdate 验证 P3-2 热更新: UpdateProviders 整体替换池、保留健康状态、
// 新 provider 默认健康、权重排序生效。
func TestRouterHotUpdate(t *testing.T) {
	r := NewModelRouter(llm.Config{Model: "m-main", BaseURL: "http://x", Timeout: time.Second},
		[]ProviderConfig{{Name: "old", Model: "m-old", BaseURL: "http://old", Weight: 80}})
	if got := len(r.Models()); got != 2 {
		t.Fatalf("expected 2 providers initially, got %d", got)
	}

	// 标记 old 不健康(模拟故障中)
	for _, p := range r.providers {
		if p.Name == "old" {
			p.markFailed(time.Now())
		}
	}
	if r.Models()[0].Healthy == true && r.Models()[0].Name == "old" {
		// 找 old 的健康状态
	}
	oldHealthy := false
	for _, m := range r.Models() {
		if m.Name == "old" {
			oldHealthy = m.Healthy
		}
	}
	if oldHealthy {
		t.Fatal("expected old provider unhealthy after markFailed")
	}

	// 热更新: 替换池(old 同名保留健康状态 + 新增 new)
	r.UpdateProviders(llm.Config{Model: "m-main2", BaseURL: "http://x2", Timeout: time.Second},
		[]ProviderConfig{
			{Name: "old", Model: "m-old2", BaseURL: "http://old2", Weight: 90},
			{Name: "new", Model: "m-new", BaseURL: "http://new", Weight: 70},
		})

	models := r.Models()
	if len(models) != 3 {
		t.Fatalf("expected 3 providers after update, got %d", len(models))
	}
	// 排序: default(100) > old(90) > new(70)
	if models[0].Name != "default" || models[1].Name != "old" || models[2].Name != "new" {
		t.Fatalf("unexpected order: %v", []string{models[0].Name, models[1].Name, models[2].Name})
	}
	// old 保留不健康状态(故障中热更新不应恢复为健康)
	oldHealthyAfter := true
	for _, m := range models {
		if m.Name == "old" {
			oldHealthyAfter = m.Healthy
		}
	}
	if oldHealthyAfter {
		t.Fatal("expected old still unhealthy after hot update (health preserved)")
	}
	// new 默认健康
	newHealthy := false
	for _, m := range models {
		if m.Name == "new" {
			newHealthy = m.Healthy
		}
	}
	if !newHealthy {
		t.Fatal("expected new provider healthy after hot update")
	}
}

// TestRouterTypeVisibilityFilter 验证 auto 模式的类型与可见性过滤:
//   - text 请求排除纯视频模型;
//   - image 请求仅匹配视觉模型;
//   - private 模型仅创建者可见, 其他用户/不限定用户(system)规则不同;
//   - 显式指定模型名时不受类型/可见性约束。
func TestRouterTypeVisibilityFilter(t *testing.T) {
	r := NewModelRouter(llm.Config{Model: "m-main", BaseURL: "http://x", Timeout: time.Second},
		[]ProviderConfig{
			{Name: "txt", Model: "m-txt", BaseURL: "http://t", Weight: 90},
			// omni 声明多类型(新 Types 字段): 文本+视觉均可
			{Name: "omni", Model: "m-omni", BaseURL: "http://o", Weight: 85, Types: []string{"text", "image"}},
			// vision 走旧版单类型 Type 字段, 验证兼容归一化
			{Name: "vision", Model: "m-vis", BaseURL: "http://v", Weight: 80, Type: "image"},
			{Name: "video", Model: "m-vid", BaseURL: "http://d", Weight: 70, Type: "video"},
			{Name: "priv", Model: "m-priv", BaseURL: "http://p", Weight: 60, Visibility: "private", CreatedBy: "u1"},
		})
	now := time.Now()

	// text 请求: 排除纯视频模型, 其余(txt/omni/vision/priv/default)可选
	cands := r.candidates(now, "", "", "text")
	for _, p := range cands {
		if p.Name == "video" {
			t.Fatal("text request should exclude video-only provider")
		}
	}
	if len(cands) != 5 { // default + txt + omni + vision + priv
		t.Fatalf("expected 5 text candidates, got %d", len(cands))
	}

	// image 请求: 须声明视觉能力(vision 与多类型 omni)
	cands = r.candidates(now, "", "", "image")
	if len(cands) != 2 {
		t.Fatalf("expected 2 image candidates (vision+omni), got %d", len(cands))
	}
	hasOmni, hasVision := false, false
	for _, p := range cands {
		if p.Name == "omni" {
			hasOmni = true
		}
		if p.Name == "vision" {
			hasVision = true
		}
	}
	if !hasOmni || !hasVision {
		t.Fatal("image request should match multi-type and legacy single-type vision providers")
	}

	// 可见性: 用户 u2 不可见 private 模型
	cands = r.candidates(now, "", "u2", "")
	for _, p := range cands {
		if p.Name == "priv" {
			t.Fatal("private model should be invisible to other user")
		}
	}
	// 创建者 u1 可见
	cands = r.candidates(now, "", "u1", "")
	found := false
	for _, p := range cands {
		if p.Name == "priv" {
			found = true
		}
	}
	if !found {
		t.Fatal("private model should be visible to its creator")
	}
	// 系统调用(user 空)放行全部
	cands = r.candidates(now, "", "", "")
	found = false
	for _, p := range cands {
		if p.Name == "priv" {
			found = true
		}
	}
	if !found {
		t.Fatal("system call (empty user) should see all models")
	}

	// 显式指定 private 模型名: 不受可见性约束(直接点名可用)
	cands = r.candidates(now, "priv", "u2", "")
	if len(cands) != 1 || cands[0].Name != "priv" {
		t.Fatal("explicit model name should bypass visibility filter")
	}
}

// TestResolvePrimaryFlag 验证主 LLM 标记优先级: Primary 标记的 provider 成为兜底 default,
// 且 embedding 配置从主配置透传; 无标记时回退主配置。
func TestResolvePrimaryFlag(t *testing.T) {
	extra := []ProviderConfig{
		{Name: "a", Model: "ma", BaseURL: "http://a", Weight: 90},
		{Name: "b", Model: "mb", BaseURL: "http://b", Weight: 50, Primary: true, APIKey: "kb"},
	}
	got := ResolvePrimary(llm.Config{APIKey: "k-main", Model: "m-main", BaseURL: "http://main",
		Timeout: time.Second, EmbeddingModel: "emb-main"}, extra)
	if got.Model != "mb" || got.BaseURL != "http://b" {
		t.Fatalf("primary flag should win, got %+v", got)
	}
	if got.EmbeddingModel != "emb-main" {
		t.Fatalf("embedding config should be carried over, got %+v", got)
	}

	// 无 Primary 标记 → 主配置优先
	got2 := ResolvePrimary(llm.Config{APIKey: "k-main", Model: "m-main", BaseURL: "http://main"},
		[]ProviderConfig{{Name: "a", Model: "ma", BaseURL: "http://a", Weight: 90}})
	if got2.Model != "m-main" {
		t.Fatalf("main config should win without primary flag, got %+v", got2)
	}
}
