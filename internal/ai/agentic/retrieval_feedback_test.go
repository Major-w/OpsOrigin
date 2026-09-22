package agentic

import (
	"context"
	"math"
	"testing"
)

// TestFeedbackPenalty 净踩计票减分: 0 票不减, 每票 0.3, 封顶 5 票(防小圈子集中踩)。
func TestFeedbackPenalty(t *testing.T) {
	cases := []struct {
		net  int
		want float64
	}{
		{0, 0}, {-2, 0}, // 无净踩 / 赞多踩少 → 不减
		{1, 0.3}, {3, 0.9},
		{5, 1.5}, {10, 1.5}, {100, 1.5}, // 封顶
	}
	for _, c := range cases {
		if got := feedbackPenalty(c.net); math.Abs(got-c.want) > 1e-9 {
			t.Fatalf("feedbackPenalty(%d) = %v, want %v", c.net, got, c.want)
		}
	}
}

// TestMemArticlePenalties 内存实现聚合语义: 同消息改判覆盖(不重复计票), 净踩 = 踩-赞 下限 0。
func TestMemArticlePenalties(t *testing.T) {
	s := NewMemFeedbackStore()
	ctx := context.Background()
	if err := s.SaveArticleFeedback(ctx, "t1", "m1", "a1", "down"); err != nil {
		t.Fatalf("save down: %v", err)
	}
	if err := s.SaveArticleFeedback(ctx, "t1", "m2", "a1", "down"); err != nil {
		t.Fatalf("save down2: %v", err)
	}
	if err := s.SaveArticleFeedback(ctx, "t1", "m1", "a1", "down"); err != nil {
		t.Fatalf("save dup: %v", err) // 同键覆盖, 不新增
	}
	pen, err := s.ArticlePenalties(ctx, "t1")
	if err != nil {
		t.Fatalf("penalties: %v", err)
	}
	if pen["a1"] != 2 {
		t.Fatalf("a1 net down = %d, want 2 (同键必须覆盖)", pen["a1"])
	}
	// 改判为赞: 净踩 -1
	if err := s.SaveArticleFeedback(ctx, "t1", "m1", "a1", "up"); err != nil {
		t.Fatalf("save up: %v", err)
	}
	pen, _ = s.ArticlePenalties(ctx, "t1")
	if pen["a1"] != 1 {
		t.Fatalf("a1 net down after flip = %d, want 1", pen["a1"])
	}
	// 赞多于踩 → 不出现在降权表
	if err := s.SaveArticleFeedback(ctx, "t1", "m3", "a2", "up"); err != nil {
		t.Fatalf("save up2: %v", err)
	}
	pen, _ = s.ArticlePenalties(ctx, "t1")
	if _, ok := pen["a2"]; ok {
		t.Fatal("a2 无净踩不应出现在降权表")
	}
	// 租户隔离
	pen, _ = s.ArticlePenalties(ctx, "other")
	if len(pen) != 0 {
		t.Fatalf("跨租户泄漏: %v", pen)
	}
}

// TestArticlePenaltiesForNilProvider 未注入 Provider 时检索降权函数必须返回 nil(闭环关闭, 行为不变)。
func TestArticlePenaltiesForNilProvider(t *testing.T) {
	ts := NewMCPToolSet(nil)
	if pen := ts.articlePenaltiesFor(context.Background(), "t1"); pen != nil {
		t.Fatalf("nil provider 应返回 nil, got %v", pen)
	}
}

// TestArticlePenaltiesForCached 注入后取数生效且命中缓存(第二次调用不再触发 Provider 查询)。
func TestArticlePenaltiesForCached(t *testing.T) {
	ts := NewMCPToolSet(nil)
	calls := 0
	ts.SetArticlePenaltyProvider(providerFunc(func(ctx context.Context, tenant string) (map[string]int, error) {
		calls++
		return map[string]int{"a1": 2}, nil
	}))
	p1 := ts.articlePenaltiesFor(context.Background(), "t1")
	p2 := ts.articlePenaltiesFor(context.Background(), "t1")
	if calls != 1 {
		t.Fatalf("provider 调用次数 = %d, want 1(缓存未生效)", calls)
	}
	if p1["a1"] != 2 || p2["a1"] != 2 {
		t.Fatalf("penalty lost: %v %v", p1, p2)
	}
}

type providerFunc func(ctx context.Context, tenant string) (map[string]int, error)

func (f providerFunc) ArticlePenalties(ctx context.Context, tenant string) (map[string]int, error) {
	return f(ctx, tenant)
}
