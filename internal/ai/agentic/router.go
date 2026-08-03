package agentic

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/opsorigin/core/pkg/llm"
)

// ProviderConfig 一个可配置的 LLM provider(模型池成员)。
type ProviderConfig struct {
	Name    string `json:"name"`     // 池内唯一标识(Agent.model_name 可填此名路由到该池默认模型)
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`    // 该 provider 的默认模型
	BaseURL string `json:"base_url"` // OpenAI 兼容端点
	Weight  int    `json:"weight"`   // 路由权重(越大越优先)
}

// ModelProvider 池内一个 provider 的运行时状态。
type ModelProvider struct {
	Name   string
	Weight int
	Client *llm.Client
	cfg    llm.Config // 构造时快照(路由选型用, llm.Client 未暴露 Config 访问器)
	healthy bool
	mu      sync.Mutex
	// failedAt 最近一次失败时间; 冷却期内视为不健康(降级), 冷却后恢复参与路由。
	failedAt time.Time
}

func (p *ModelProvider) defaultModel() string {
	if p.cfg.Model != "" {
		return p.cfg.Model
	}
	return "deepseek-chat"
}

func (p *ModelProvider) isHealthy(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.healthy {
		return true
	}
	return p.failedAt.IsZero() || now.Sub(p.failedAt) > providerCooldown
}

func (p *ModelProvider) markFailed(now time.Time) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthy = false
	p.failedAt = now
}

func (p *ModelProvider) markHealthy() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.healthy = true
	p.failedAt = time.Time{}
}

// providerCooldown 失败后不参与路由的冷却时长。
const providerCooldown = 60 * time.Second

// ModelRouter 多模型路由(P3): 管理一组 LLM provider, 按权重 + 健康状态选择,
// 调用失败自动降级到下一个可用 provider(策略优化与可用性兜底)。
// 无池(单 provider)时行为等价于单个 llm.Client。
type ModelRouter struct {
	mu        sync.RWMutex
	providers []*ModelProvider // 按 Weight 降序
}

// NewModelRouter 构造路由池。primary 作为兜底 provider(name="default") 放最前;
// extra 为配置的附加 provider(权重参与排序)。
func NewModelRouter(primary llm.Config, extra []ProviderConfig) *ModelRouter {
	r := &ModelRouter{}
	primaryCfg := primary
	primaryCfg.Model = orDefault(primaryCfg.Model, "deepseek-chat")
	r.providers = append(r.providers, &ModelProvider{
		Name:    "default",
		Weight:  100,
		Client:  llm.NewClient(primaryCfg),
		cfg:     primaryCfg,
		healthy: true,
	})
	for _, pc := range extra {
		if pc.Name == "" || pc.Model == "" || pc.BaseURL == "" {
			slog.Warn("skip invalid llm provider config", "name", pc.Name)
			continue
		}
		pcfg := llm.Config{
			APIKey:     pc.APIKey,
			Model:      pc.Model,
			BaseURL:    pc.BaseURL,
			Timeout:    primary.Timeout,
			MaxRetries: primary.MaxRetries,
		}
		r.providers = append(r.providers, &ModelProvider{
			Name:    pc.Name,
			Weight:  pc.Weight,
			Client:  llm.NewClient(pcfg),
			cfg:     pcfg,
			healthy: true,
		})
	}
	// 权重降序, 稳定路由
	sort.SliceStable(r.providers, func(i, j int) bool {
		return r.providers[i].Weight > r.providers[j].Weight
	})
	return r
}

// chatCandidates 返回按权重排序的健康候选(排除与请求模型不匹配的 provider 时可选)。
func (r *ModelRouter) candidates(now time.Time, reqModel string) []*ModelProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []*ModelProvider
	for _, p := range r.providers {
		if !p.isHealthy(now) {
			continue
		}
		// 若请求指定了模型名, 优先匹配 provider 名或其默认模型
		if reqModel != "" && reqModel != p.Name && reqModel != p.defaultModel() {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Chat 带路由与降级: 按 req.Model 匹配健康 provider, 失败则标记不健康并降级到下一个。
func (r *ModelRouter) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	now := time.Now()
	cands := r.candidates(now, req.Model)
	if len(cands) == 0 {
		// 全不健康: 冷却期后重试最高权重(避免永久不可用)
		r.mu.RLock()
		for _, p := range r.providers {
			if req.Model == "" || req.Model == p.Name || req.Model == p.defaultModel() {
				cands = append(cands, p)
				break
			}
		}
		r.mu.RUnlock()
	}
	if len(cands) == 0 {
		return nil, errNoHealthyModel
	}

	reqModel := req.Model
	var lastErr error
	for _, p := range cands {
		cfg := p.cfg
		if reqModel == "" || reqModel == p.Name {
			req.Model = cfg.Model // 路由到该 provider 的默认模型
		} else {
			req.Model = reqModel // 用户显式指定模型
		}
		resp, err := p.Client.Chat(ctx, req)
		if err != nil {
			p.markFailed(time.Now())
			slog.Warn("llm provider failed, fallback to next",
				"provider", p.Name, "model", req.Model, "err", err)
			lastErr = err
			continue
		}
		p.markHealthy()
		return resp, nil
	}
	return nil, lastErr
}

// StreamChat 流式对话: 与 Chat 同选型逻辑(开流后失败不降级)。
func (r *ModelRouter) StreamChat(ctx context.Context, req llm.ChatRequest) (<-chan llm.StreamChunk, error) {
	now := time.Now()
	cands := r.candidates(now, req.Model)
	if len(cands) == 0 {
		r.mu.RLock()
		for _, p := range r.providers {
			if req.Model == "" || req.Model == p.Name || req.Model == p.defaultModel() {
				cands = append(cands, p)
				break
			}
		}
		r.mu.RUnlock()
	}
	if len(cands) == 0 {
		return nil, errNoHealthyModel
	}
	p := cands[0]
	cfg := p.cfg
	if req.Model == "" || req.Model == p.Name {
		req.Model = cfg.Model
	}
	ch, err := p.Client.StreamChat(ctx, req)
	if err != nil {
		p.markFailed(time.Now())
		return nil, err
	}
	p.markHealthy()
	return ch, nil
}

// Embed 取最高权重健康 provider 的 embedding(主 provider 优先; 失败不降级)。
func (r *ModelRouter) Embed(ctx context.Context, text string) ([]float32, error) {
	r.mu.RLock()
	providers := append([]*ModelProvider(nil), r.providers...)
	r.mu.RUnlock()
	for _, p := range providers {
		if !p.isHealthy(time.Now()) {
			continue
		}
		vec, err := p.Client.Embed(ctx, text)
		if err != nil {
			p.markFailed(time.Now())
			continue
		}
		p.markHealthy()
		return vec, nil
	}
	return nil, errNoHealthyModel
}

// ModelStatus 模型池状态(供 /api/v1/ai/models 展示)。
type ModelStatus struct {
	Name    string `json:"name"`
	Model   string `json:"model"`
	BaseURL string `json:"base_url"`
	Weight  int    `json:"weight"`
	Healthy bool   `json:"healthy"`
}

// Models 返回模型池当前状态(权重降序)。
func (r *ModelRouter) Models() []ModelStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]ModelStatus, 0, len(r.providers))
	now := time.Now()
	for _, p := range r.providers {
		cfg := p.cfg
		out = append(out, ModelStatus{
			Name:    p.Name,
			Model:   cfg.Model,
			BaseURL: cfg.BaseURL,
			Weight:  p.Weight,
			Healthy: p.isHealthy(now),
		})
	}
	return out
}

// UpdateProviders 运行时热更新模型池(P3-2): 整体替换 provider 集合。
// 供配置热更新通道(如 SysConfig 变更 / 运维调用)在不停机的情况下增减/调整 provider。
// 替换是原子的(持有写锁); 已存在的同名 provider 保留健康状态(避免热更新把故障中的
// provider 恢复成健康, 造成瞬时错误路由), 新 provider 默认健康。
func (r *ModelRouter) UpdateProviders(primary llm.Config, extra []ProviderConfig) {
	r.mu.Lock()
	defer r.mu.Unlock()

	build := func(cfg llm.Config, name string, weight int, healthy bool, failedAt time.Time) *ModelProvider {
		if cfg.Model == "" {
			cfg.Model = "deepseek-chat"
		}
		return &ModelProvider{
			Name:     name,
			Weight:   weight,
			Client:   llm.NewClient(cfg),
			cfg:      cfg,
			healthy:  healthy,
			failedAt: failedAt,
		}
	}

	primaryCfg := primary
	next := []*ModelProvider{build(primaryCfg, "default", 100, true, time.Time{})}

	// 保留既有健康状态: name -> provider(含 failedAt, 避免热更新把故障中的 provider 恢复健康)
	prev := map[string]*ModelProvider{}
	for _, p := range r.providers {
		prev[p.Name] = p
	}

	for _, pc := range extra {
		if pc.Name == "" || pc.Model == "" || pc.BaseURL == "" {
			slog.Warn("skip invalid llm provider config (hot update)", "name", pc.Name)
			continue
		}
		pcfg := llm.Config{
			APIKey:     pc.APIKey,
			Model:      pc.Model,
			BaseURL:    pc.BaseURL,
			Timeout:    primary.Timeout,
			MaxRetries: primary.MaxRetries,
		}
		healthy := true
		var failedAt time.Time
		if old, ok := prev[pc.Name]; ok {
			old.mu.Lock()
			healthy = old.healthy
			failedAt = old.failedAt
			old.mu.Unlock()
		}
		next = append(next, build(pcfg, pc.Name, pc.Weight, healthy, failedAt))
	}

	sort.SliceStable(next, func(i, j int) bool {
		return next[i].Weight > next[j].Weight
	})
	r.providers = next
	slog.Info("llm model pool hot-updated", "providers", len(next))
}

// errNoHealthyModel 全池不可用错误。
var errNoHealthyModel = errors.New("no healthy llm provider available")
