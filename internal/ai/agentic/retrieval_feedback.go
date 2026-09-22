package agentic

// 检索反馈闭环: 用户对 AI 回答的赞踩, 经「文章级反馈」落到回答引用的具体知识文章,
// 再在检索 rerank 中对净踩文章降权 —— 让低质文章越用越靠后。
//
// 数据流: 前端反馈(带引用文章列表) → /ai/feedback → FeedbackStore.SaveArticleFeedback
//        → 检索时 MCPToolSet 经 ArticlePenaltyProvider 取降权表(带缓存) → rrfMergeChunksExplained 减分。

import (
	"context"
	"time"
)

// ArticlePenaltyProvider 提供「租户 → 文章净踩数」映射。由 FeedbackStore 的具体实现满足;
// 未注入(setArticlePenaltyProvider 未调用)时检索不做反馈降权, 行为与闭环上线前一致。
type ArticlePenaltyProvider interface {
	ArticlePenalties(ctx context.Context, tenant string) (map[string]int, error)
}

// feedbackPenaltyCap 单篇文章最大净踩计票数: 超过部分不再加重, 防止小圈子集中踩踩把
// 一篇其实正确的文章彻底埋掉。
const feedbackPenaltyCap = 5

// feedbackPenaltyWeight 每一票净踩的减分幅度。rerankBonus 上限约 1.05(0.6+0.25+0.2),
// RRF 名次分量级 ~0.016×路数; 0.3/票意味着 3 票净踩即可抵消满额质量加成, 力度够用不粗暴。
const feedbackPenaltyWeight = 0.3

// feedbackPenalty 单篇文章的减分值(正数, 由调用方从总分中扣除)。
func feedbackPenalty(netDown int) float64 {
	if netDown <= 0 {
		return 0
	}
	if netDown > feedbackPenaltyCap {
		netDown = feedbackPenaltyCap
	}
	return feedbackPenaltyWeight * float64(netDown)
}

// SetArticlePenaltyProvider 注入反馈降权来源(nil = 关闭闭环)。
func (ts *MCPToolSet) SetArticlePenaltyProvider(p ArticlePenaltyProvider) {
	ts.penaltyMu.Lock()
	ts.penaltyProvider = p
	ts.penaltyMu.Unlock()
}

// penaltyCacheEntry 按租户缓存的降权表。
type penaltyCacheEntry struct {
	pen    map[string]int
	loaded time.Time
}

const penaltyCacheTTL = 5 * time.Minute

// articlePenaltiesFor 取租户降权表(带 TTL 缓存); 未注入或查询失败返回 nil(不降权, 检索不受影响)。
func (ts *MCPToolSet) articlePenaltiesFor(ctx context.Context, tenant string) map[string]int {
	ts.penaltyMu.Lock()
	p := ts.penaltyProvider
	e, ok := ts.penaltyCache[tenant]
	ts.penaltyMu.Unlock()
	if p == nil {
		return nil
	}
	if ok && time.Since(e.loaded) < penaltyCacheTTL {
		return e.pen
	}
	pen, err := p.ArticlePenalties(ctx, tenant)
	if err != nil {
		// 失败不阻断检索: 用旧值(若有), 没有则不降权
		if ok {
			return e.pen
		}
		return nil
	}
	ts.penaltyMu.Lock()
	ts.penaltyCache[tenant] = penaltyCacheEntry{pen: pen, loaded: time.Now()}
	ts.penaltyMu.Unlock()
	return pen
}
