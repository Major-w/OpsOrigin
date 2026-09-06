package agentic

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/opsorigin/core/pkg/llm"
)

// ProviderConfig 一个可配置的 LLM provider(模型列表成员)。
// 模型列表即在此定义各家模型的权重/类型/可见性; auto 模式按这些字段路由。
type ProviderConfig struct {
	Name    string `json:"name"` // 池内唯一标识(Agent.model_name 可填此名路由到该池默认模型)
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`    // 该 provider 的默认模型
	BaseURL string `json:"base_url"` // OpenAI 兼容端点
	Weight  int    `json:"weight"`   // 路由权重(越大越优先)
	// Types 模型能力类型列表(可多选, 一个模型可同时支持多种): text(文本) / image(多模态视觉) /
	// video(视频)。auto 模式按请求上下文(是否含图片)推导 PreferType 后据此过滤候选。
	Types []string `json:"types,omitempty"`
	// Type 旧版单类型字段(兼容存量配置); 与 Types 同时存在时以 Types 为准。
	Type string `json:"type,omitempty"`
	// Visibility 可见性: public(所有人可用, 默认) / private(仅 CreatedBy 可见可用)。
	Visibility string `json:"visibility"`
	// CreatedBy 创建者用户 ID; Visibility=private 时仅该用户可在 auto/下拉中选用。
	CreatedBy string `json:"created_by"`
	// Primary 主 LLM 标记: 取代旧"默认模型"概念, 作为兜底 default provider。
	Primary bool `json:"primary"`
}

// EffectiveTypes 返回归一化后的类型列表: 优先 Types; 兼容旧 Type 单字段(逗号分隔);
// 全空时默认 ["text"]。保证路由过滤始终有明确类型语义。
func (pc *ProviderConfig) EffectiveTypes() []string {
	if len(pc.Types) > 0 {
		return pc.Types
	}
	if pc.Type != "" {
		var out []string
		for _, t := range strings.Split(pc.Type, ",") {
			if t = strings.TrimSpace(t); t != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return []string{"text"}
}

// hasType 判断类型列表是否包含指定能力。
func hasType(types []string, t string) bool {
	return slices.Contains(types, t)
}

// ModelProvider 池内一个 provider 的运行时状态。
type ModelProvider struct {
	Name    string
	Weight  int
	Client  *llm.Client
	cfg     llm.Config // 构造时快照(路由选型用, llm.Client 未暴露 Config 访问器)
	healthy bool
	mu      sync.Mutex
	// failedAt 最近一次失败时间; 冷却期内视为不健康(降级), 冷却后恢复参与路由。
	failedAt time.Time
	// 路由元数据: 类型(可多种) / 可见性 / 创建者 / 主 LLM 标记(auto 模式过滤用)。
	Types      []string
	Visibility string
	CreatedBy  string
	Primary    bool
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

// ResolvePrimary 解析生效的主 provider 配置(兜底 default, 用于 embedding / RAG /
// 演进 / 技能市场等非对话内部调用)。优先级:
//  1. 池中带 Primary=true 且配置完整的 provider(取代旧"默认模型"概念);
//  2. 后端主配置(primary)若已填 API key;
//  3. 池中权重最高且配置完整的 provider(兼容「仅用池内模型」场景)。
//
// 无可用时返回原 primary 配置。
func ResolvePrimary(primary llm.Config, extra []ProviderConfig) llm.Config {
	// embedding 配置属于平台级配置(挂在主 LLM 配置上), 主 LLM 无论切到哪个 provider 都透传,
	// 避免主 LLM 标记切换后知识库向量化丢配置。
	withEmbedding := func(c llm.Config) llm.Config {
		if c.EmbeddingModel == "" {
			c.EmbeddingModel = primary.EmbeddingModel
			c.EmbeddingBaseURL = primary.EmbeddingBaseURL
		}
		return c
	}
	// 1. 显式主 LLM 标记优先
	for i := range extra {
		p := &extra[i]
		if p.Primary && p.APIKey != "" && p.BaseURL != "" && p.Model != "" {
			return withEmbedding(llm.Config{
				APIKey:     p.APIKey,
				Model:      p.Model,
				BaseURL:    p.BaseURL,
				Timeout:    primary.Timeout,
				MaxRetries: primary.MaxRetries,
			})
		}
	}
	// 2. 后端主配置
	if primary.APIKey != "" {
		return primary
	}
	// 3. 池中权重最高且配置完整的 provider
	var best *ProviderConfig
	for i := range extra {
		p := &extra[i]
		if p.APIKey == "" || p.BaseURL == "" || p.Model == "" {
			continue
		}
		if best == nil || p.Weight > best.Weight {
			best = p
		}
	}
	if best == nil {
		return primary
	}
	return withEmbedding(llm.Config{
		APIKey:     best.APIKey,
		Model:      best.Model,
		BaseURL:    best.BaseURL,
		Timeout:    primary.Timeout,
		MaxRetries: primary.MaxRetries,
	})
}

// NewModelRouter 构造路由池。primary(经 ResolvePrimary 解析, 含 Primary 标记优先) 作为
// 兜底 provider(name="default") 放最前; extra 为配置的附加 provider(权重参与排序)。
// extra 中带 Primary=true 的 provider 已被 ResolvePrimary 选为 default, 此处不再重复加入池列表,
// 避免兜底与池内出现同一模型两条记录。
func NewModelRouter(primary llm.Config, extra []ProviderConfig) *ModelRouter {
	r := &ModelRouter{}
	primary = ResolvePrimary(primary, extra)
	primaryCfg := primary
	primaryCfg.Model = orDefault(primaryCfg.Model, "deepseek-chat")
	// 主 LLM 标记者的 default 权重取其真实权重(而非固定 100),
	// 使 auto 模式按权重公平排序; 主 LLM 标记仅作兜底/embedding 语义。
	primaryWeight := 100
	var primaryMeta *ProviderConfig
	for i := range extra {
		pc := &extra[i]
		if pc.Primary && pc.APIKey != "" && pc.BaseURL != "" && pc.Model != "" {
			primaryWeight = pc.Weight
			primaryMeta = pc
			break
		}
	}
	def := &ModelProvider{
		Name:    "default",
		Weight:  primaryWeight,
		Client:  llm.NewClient(primaryCfg),
		cfg:     primaryCfg,
		healthy: true,
		Primary: true,
	}
	if primaryMeta != nil {
		def.Types = primaryMeta.EffectiveTypes()
		def.Visibility = primaryMeta.Visibility
		def.CreatedBy = primaryMeta.CreatedBy
	} else {
		def.Types = []string{"text"}
	}
	r.providers = append(r.providers, def)
	for _, pc := range extra {
		if pc.Name == "" || pc.Model == "" || pc.BaseURL == "" {
			slog.Warn("skip invalid llm provider config", "name", pc.Name)
			continue
		}
		// 主 LLM 标记者已作为 default, 池列表不再重复
		if pc.Primary {
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
			Name:       pc.Name,
			Weight:     pc.Weight,
			Client:     llm.NewClient(pcfg),
			cfg:        pcfg,
			healthy:    true,
			Types:      pc.EffectiveTypes(),
			Visibility: pc.Visibility,
			CreatedBy:  pc.CreatedBy,
			Primary:    pc.Primary,
		})
	}
	// 权重降序, 稳定路由
	sort.SliceStable(r.providers, func(i, j int) bool {
		return r.providers[i].Weight > r.providers[j].Weight
	})
	return r
}

// providerEligible 判断 provider 是否满足本次请求的路由约束(健康由调用方单独判定)。
//   - reqModel 非空: 仅匹配 provider 名或其默认模型(显式指定时不受类型/可见性约束以外的过滤影响)。
//   - 可见性: private 模型仅创建者(CreatedBy==user)可用; 系统调用(user 空)放行全部。
//   - 类型: 仅当 preferType 非空时过滤——text 请求匹配含 text/image 能力的模型
//     (视觉模型天然可处理纯文本), image/video 请求要求模型声明对应能力。
func providerEligible(p *ModelProvider, reqModel, user, preferType string) bool {
	if reqModel != "" {
		return reqModel == p.Name || reqModel == p.defaultModel()
	}
	if p.Visibility == "private" && user != "" && p.CreatedBy != user {
		return false
	}
	switch preferType {
	case "":
		// 不限定类型
	case "text":
		// 文本请求: 含文本或视觉能力的模型均可(视觉模型可作为文本对话)
		if !hasType(p.Types, "text") && !hasType(p.Types, "image") {
			return false
		}
	case "image":
		// 多模态(图片)请求: 须声明视觉能力
		if !hasType(p.Types, "image") {
			return false
		}
	case "video":
		if !hasType(p.Types, "video") {
			return false
		}
	}
	return true
}

// candidates 返回按权重排序、健康且符合类型/可见性约束的候选(供 Chat/StreamChat)。
func (r *ModelRouter) candidates(now time.Time, reqModel, user, preferType string) []*ModelProvider {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.eligibleLocked(now, reqModel, user, preferType, false)
}

// eligibleLocked 在已持读锁时筛选候选; ignoreHealth=true 时忽略健康(兜底路径用),
// 仍严格按类型/可见性过滤。providers 已按权重降序, 故返回序列亦权重降序。
func (r *ModelRouter) eligibleLocked(now time.Time, reqModel, user, preferType string, ignoreHealth bool) []*ModelProvider {
	var out []*ModelProvider
	for _, p := range r.providers {
		if !ignoreHealth && !p.isHealthy(now) {
			continue
		}
		if !providerEligible(p, reqModel, user, preferType) {
			continue
		}
		out = append(out, p)
	}
	return out
}

// Chat 带路由与降级: 按 req.Model 匹配健康 provider, 失败则标记不健康并降级到下一个。
// auto 模式(req.Model 空)按权重 + 健康 + 类型(req.PreferType) + 可见性(req.User) 选模型。
func (r *ModelRouter) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	now := time.Now()
	cands := r.candidates(now, req.Model, req.User, req.PreferType)
	if len(cands) == 0 {
		// 全不健康/被过滤: 放宽健康(忽略健康)重试最高权重且符合类型/可见性约束的 provider,
		// 避免仅因冷却期或可见性外约束而永久不可用。
		r.mu.RLock()
		cands = r.eligibleLocked(now, req.Model, req.User, req.PreferType, true)
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
	cands := r.candidates(now, req.Model, req.User, req.PreferType)
	if len(cands) == 0 {
		r.mu.RLock()
		cands = r.eligibleLocked(now, req.Model, req.User, req.PreferType, true)
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
// 跳过纯视频模型(embedding 为文本能力), 避免把无向量接口的 provider 误标记为故障。
func (r *ModelRouter) Embed(ctx context.Context, text string) ([]float32, error) {
	r.mu.RLock()
	providers := append([]*ModelProvider(nil), r.providers...)
	r.mu.RUnlock()
	for _, p := range providers {
		if !p.isHealthy(time.Now()) {
			continue
		}
		if !hasType(p.Types, "text") && !hasType(p.Types, "image") {
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
	Name       string   `json:"name"`
	Model      string   `json:"model"`
	BaseURL    string   `json:"base_url"`
	Weight     int      `json:"weight"`
	Healthy    bool     `json:"healthy"`
	Types      []string `json:"types"`      // text / image / video(可多种)
	Visibility string   `json:"visibility"` // public / private
	CreatedBy  string   `json:"created_by"` // private 模型创建者
	Primary    bool     `json:"primary"`    // 是否主 LLM 兜底
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
			Name:       p.Name,
			Model:      cfg.Model,
			BaseURL:    cfg.BaseURL,
			Weight:     p.Weight,
			Healthy:    p.isHealthy(now),
			Types:      p.Types,
			Visibility: p.Visibility,
			CreatedBy:  p.CreatedBy,
			Primary:    p.Primary,
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

	primary = ResolvePrimary(primary, extra)
	primaryCfg := primary
	// 主 LLM 标记者的 default 权重取其真实权重(而非固定 100), 使 auto 排序公平;
	// 并继承其类型/可见性元数据。
	primaryWeight := 100
	var primaryMeta *ProviderConfig
	for i := range extra {
		pc := &extra[i]
		if pc.Primary && pc.APIKey != "" && pc.BaseURL != "" && pc.Model != "" {
			primaryWeight = pc.Weight
			primaryMeta = pc
			break
		}
	}
	next := []*ModelProvider{build(primaryCfg, "default", primaryWeight, true, time.Time{})}
	next[0].Primary = true
	if primaryMeta != nil {
		next[0].Types = primaryMeta.EffectiveTypes()
		next[0].Visibility = primaryMeta.Visibility
		next[0].CreatedBy = primaryMeta.CreatedBy
	} else {
		next[0].Types = []string{"text"}
	}

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
		// 主 LLM 标记者已作为 default, 池列表不再重复
		if pc.Primary {
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
		np := build(pcfg, pc.Name, pc.Weight, healthy, failedAt)
		np.Types = pc.EffectiveTypes()
		np.Visibility = pc.Visibility
		np.CreatedBy = pc.CreatedBy
		np.Primary = pc.Primary
		next = append(next, np)
	}

	sort.SliceStable(next, func(i, j int) bool {
		return next[i].Weight > next[j].Weight
	})
	r.providers = next
	slog.Info("llm model pool hot-updated", "providers", len(next))
}

// AddProvider 热追加单个 provider(如 L3 微调产物模型注册进池)。
// 已存在同名 provider 时整体替换(重置为健康态, 因为微调产物是新模型可重新验证);
// 追加后按权重重排。供微调任务完成回调调用, 与 UpdateProviders 互补。
func (r *ModelRouter) AddProvider(name string, cfg llm.Config, weight int, typeNames []string, visibility, createdBy string, primary bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if cfg.Model == "" {
		cfg.Model = "deepseek-chat"
	}
	if visibility == "" {
		visibility = "public"
	}
	if len(typeNames) == 0 {
		typeNames = []string{"text"}
	}
	p := &ModelProvider{
		Name:       name,
		Weight:     weight,
		Client:     llm.NewClient(cfg),
		cfg:        cfg,
		healthy:    true,
		Types:      typeNames,
		Visibility: visibility,
		CreatedBy:  createdBy,
		Primary:    primary,
	}
	replaced := false
	for i, old := range r.providers {
		if old.Name == name {
			r.providers[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		r.providers = append(r.providers, p)
	}
	sort.SliceStable(r.providers, func(i, j int) bool {
		return r.providers[i].Weight > r.providers[j].Weight
	})
	slog.Info("llm provider added", "name", name, "model", cfg.Model, "weight", weight)
}

// errNoHealthyModel 全池不可用错误。
var errNoHealthyModel = errors.New("no healthy llm provider available")
