package agentic

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/ai/model"
)

// ============================================================================
// 技能市场 (L4 生态: MCP 打包共享, 数字员工 + 依赖工具打包成可分享的市场形态)
//
// 把一个数字员工(提示词 / Skills / 工具白名单 / 依赖的自定义 MCP 工具)打包成可移植的
// 技能包(SkillPackage), 进目录 → 可浏览 / 安装 / 导出 / 导入, 实现运维能力的沉淀与复用。
//
// 安全边界:
//   - 安装时按需重建依赖的自定义工具; cli 类工具高风险, 沿用既有自定义工具安全约束;
//   - 技能包仅持久化配置, 不含任何凭据(CLI 命令模板中的密钥属用户责任, 安装方需复核)。
// ============================================================================

// SkillPackage 技能市场中的一个可分享技能包。
type SkillPackage struct {
	ID          string              `json:"id"`
	TenantID    string              `json:"tenant_id"`
	Name        string              `json:"name"`
	Version     string              `json:"version"`
	Author      string              `json:"author,omitempty"`
	Description string              `json:"description,omitempty"`
	Category    string              `json:"category,omitempty"` // 数据库/中间件/网络/安全/通用...
	Tags        []string            `json:"tags,omitempty"`
	Agent       AgentPayload        `json:"agent"`             // 数字员工配置
	Tools       []CustomToolPayload `json:"tools,omitempty"`   // 依赖的自定义 MCP 工具(安装时按需重建)
	Downloads   int                 `json:"downloads"`
	RatingAvg   float64             `json:"rating_avg"` // 同租户评分均值(1~5)
	RatingCount int                 `json:"rating_count"`
	Checksum    string              `json:"checksum"` // 对 (agent+tools) 的完整性校验
	CreatedAt   time.Time           `json:"created_at"`
	UpdatedAt   time.Time           `json:"updated_at"`
}

// AgentPayload 数字员工的可移植配置片段。
type AgentPayload struct {
	Name                string             `json:"name"`
	ModelName           string             `json:"model_name,omitempty"`
	SystemPrompt        string             `json:"system_prompt,omitempty"`
	KnowledgeCategories []string           `json:"knowledge_categories,omitempty"`
	Tools               []string           `json:"tools,omitempty"`
	Skills              []model.AgentSkill `json:"skills,omitempty"`
}

// CustomToolPayload 自定义 MCP 工具的可移植定义(供安装时重建)。
type CustomToolPayload struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        string            `json:"type"`
	Enabled     bool              `json:"enabled"`
	Parameters  []CustomToolParam `json:"parameters,omitempty"`
	HTTPMethod  string            `json:"http_method,omitempty"`
	HTTPURL     string            `json:"http_url,omitempty"`
	HTTPHeaders map[string]string `json:"http_headers,omitempty"`
	TimeoutSec  int               `json:"timeout_sec,omitempty"`
	CLICommand  string            `json:"cli_command,omitempty"`
}

// SkillPackageMeta 发布技能包时由调用方提供的元数据。
type SkillPackageMeta struct {
	Name        string
	Version     string
	Author      string
	Description string
	Category    string
	Tags        []string
}

// checksum 对 (agent+tools) 计算 SHA-256, 用于安装/导入时的完整性校验。
func (p *SkillPackage) checksum() string {
	b, _ := json.Marshal(struct {
		Agent AgentPayload        `json:"agent"`
		Tools []CustomToolPayload  `json:"tools"`
	}{p.Agent, p.Tools})
	return fmt.Sprintf("%x", sha256.Sum256(b))
}

// PublishSkill 把指定数字员工打包为技能包并发布到市场目录。
// 仅打包该 Agent 依赖的自定义工具(工具白名单命中的自定义工具; 白名单为空则打包全部租户自定义工具),
// 内建工具(llm_chat/http_get 等)无需打包, 安装环境已自带。
func (e *Engine) PublishSkill(ctx context.Context, tenantID, agentID string, meta SkillPackageMeta) (*SkillPackage, error) {
	ag, ok := e.GetAgent(agentID)
	if !ok || ag.TenantID != tenantID {
		return nil, fmt.Errorf("agent not found: %s", agentID)
	}
	now := time.Now()
	pkg := &SkillPackage{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        orDefault(meta.Name, ag.Name),
		Version:     orDefault(meta.Version, "1.0.0"),
		Author:      meta.Author,
		Description: meta.Description,
		Category:    meta.Category,
		Tags:        meta.Tags,
		Agent: AgentPayload{
			Name:                ag.Name,
			ModelName:           ag.ModelName,
			SystemPrompt:        ag.SystemPrompt,
			KnowledgeCategories: ag.KnowledgeCategories,
			Tools:               ag.Tools,
			Skills:              ag.Skills,
		},
		Downloads: 0,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// 收集依赖的自定义工具
	owned := e.ListCustomTools(tenantID)
	ownMap := make(map[string]*CustomTool, len(owned))
	for _, t := range owned {
		ownMap[t.Name] = t
	}
	if len(ag.Tools) > 0 {
		for _, name := range ag.Tools {
			if t, ok := ownMap[name]; ok {
				pkg.Tools = append(pkg.Tools, toToolPayload(t))
			}
		}
	} else {
		for _, t := range owned {
			pkg.Tools = append(pkg.Tools, toToolPayload(t))
		}
	}

	pkg.Checksum = pkg.checksum()
	if e.store != nil {
		// 同名同版本视为覆盖更新(复用既有行 ID), 避免重复行污染版本列表。
		if prev, gerr := e.store.GetSkillPackageByNameVersion(ctx, tenantID, pkg.Name, pkg.Version); gerr == nil && prev != nil {
			pkg.ID = prev.ID
			pkg.Downloads = prev.Downloads
			pkg.RatingAvg = prev.RatingAvg
			pkg.RatingCount = prev.RatingCount
			pkg.CreatedAt = prev.CreatedAt
		}
		if err := e.store.CreateSkillPackage(ctx, pkg); err != nil {
			return nil, err
		}
	}
	slog.Info("skill package published", "id", pkg.ID, "name", pkg.Name, "version", pkg.Version, "custom_tools", len(pkg.Tools))
	return pkg, nil
}

// ListSkillPackages 返回该租户的技能包目录(可按 category / tag 过滤, 更新时间倒序)。
func (e *Engine) ListSkillPackages(ctx context.Context, tenantID, category, tag string) []*SkillPackage {
	if e.store == nil {
		return []*SkillPackage{}
	}
	pkgs, err := e.store.ListSkillPackages(ctx, tenantID)
	if err != nil {
		slog.Error("list skill packages failed", "err", err)
		return []*SkillPackage{}
	}
	out := make([]*SkillPackage, 0, len(pkgs))
	for _, p := range pkgs {
		if category != "" && p.Category != category {
			continue
		}
		if tag != "" {
			hit := false
			for _, t := range p.Tags {
				if t == tag {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	return out
}

// GetSkillPackage 读取单个技能包(供详情 / 安装 / 导出)。
func (e *Engine) GetSkillPackage(ctx context.Context, tenantID, id string) (*SkillPackage, error) {
	if e.store == nil {
		return nil, fmt.Errorf("store not configured")
	}
	p, err := e.store.GetSkillPackage(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return nil, fmt.Errorf("skill package not found: %s", id)
	}
	return p, nil
}

// DeleteSkillPackage 下架一个技能包。
func (e *Engine) DeleteSkillPackage(ctx context.Context, tenantID, id string) error {
	if e.store == nil {
		return fmt.Errorf("store not configured")
	}
	return e.store.DeleteSkillPackage(ctx, tenantID, id)
}

// InstallSkill 从技能包实例化一个新的数字员工到当前租户, 并返回新建的 Agent。
// 安装时按需重建依赖的自定义工具(按名幂等: 已存在则跳过), 随后同步到 registry。
// 同名冲突风险通过 newName 后缀规避; newName 为空时自动追加 " (市场)" 后缀。
func (e *Engine) InstallSkill(ctx context.Context, tenantID, pkgID, newName string) (*model.AgentInstance, error) {
	p, err := e.GetSkillPackage(ctx, tenantID, pkgID)
	if err != nil {
		return nil, err
	}

	// 重建依赖的自定义工具(按名幂等)
	for _, tp := range p.Tools {
		existing := e.ListCustomTools(tenantID)
		exists := false
		for _, t := range existing {
			if t.Name == tp.Name {
				exists = true
				break
			}
		}
		if exists {
			continue
		}
		ct := fromToolPayload(tenantID, &tp)
		if err := e.CreateCustomTool(ctx, ct); err != nil {
			slog.Warn("failed to recreate custom tool during skill install", "name", tp.Name, "err", err)
			continue
		}
	}
	// 重建后重新同步到 registry, 使新工具立即对模型可见
	if err := e.SyncCustomTools(ctx); err != nil {
		slog.Warn("sync custom tools after skill install failed", "err", err)
	}

	name := newName
	if name == "" {
		name = p.Agent.Name + " (市场)"
	}
	ag := &model.AgentInstance{
		Name:                name,
		ModelName:           p.Agent.ModelName,
		SystemPrompt:        p.Agent.SystemPrompt,
		KnowledgeCategories: p.Agent.KnowledgeCategories,
		Tools:               p.Agent.Tools,
		Skills:              p.Agent.Skills,
		TenantID:            tenantID,
	}
	if err := e.CreateAgent(ctx, ag); err != nil {
		return nil, err
	}
	// 下载计数 +1
	if e.store != nil {
		_ = e.store.IncrSkillDownloads(ctx, tenantID, pkgID)
	}
	slog.Info("skill package installed", "pkg", pkgID, "agent", ag.ID, "name", name)
	return ag, nil
}

// ExportSkill 将技能包导出为可移植的 JSON(含完整 payload 与校验和), 供跨环境分享。
func (e *Engine) ExportSkill(ctx context.Context, tenantID, id string) ([]byte, error) {
	p, err := e.GetSkillPackage(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	return json.MarshalIndent(p, "", "  ")
}

// ImportSkill 从可移植 JSON 导入一个技能包(生成新 ID 避免冲突), 进当前租户目录。
func (e *Engine) ImportSkill(ctx context.Context, tenantID string, data []byte) (*SkillPackage, error) {
	var p SkillPackage
	if err := json.Unmarshal(data, &p); err != nil {
		return nil, fmt.Errorf("invalid skill package: %w", err)
	}
	if p.Agent.Name == "" {
		return nil, fmt.Errorf("skill package missing agent name")
	}
	// 重新生成身份, 归到当前租户, 重置统计与时效
	p.ID = uuid.New().String()
	p.TenantID = tenantID
	p.Downloads = 0
	p.CreatedAt = time.Now()
	p.UpdatedAt = time.Now()
	p.Checksum = p.checksum()
	if e.store != nil {
		if err := e.store.CreateSkillPackage(ctx, &p); err != nil {
			return nil, err
		}
	}
	slog.Info("skill package imported", "id", p.ID, "name", p.Name, "version", p.Version)
	return &p, nil
}

// ============================================================================
// 评分 / 收藏 / 版本列表 (技能市场增强)
// 评分与收藏均以「同租户 + 包 + 用户」去重, 用户身份由 handler 从请求上下文注入。
// ============================================================================

// RateSkill 对技能包打分(1~5 星, 同用户去重重算均值)。
func (e *Engine) RateSkill(ctx context.Context, tenantID, pkgID, user string, score int) error {
	if e.store == nil {
		return fmt.Errorf("store not configured")
	}
	if score < 1 || score > 5 {
		return fmt.Errorf("score must be between 1 and 5")
	}
	if _, err := e.GetSkillPackage(ctx, tenantID, pkgID); err != nil {
		return err
	}
	return e.store.RateSkill(ctx, tenantID, pkgID, user, score)
}

// GetSkillRating 返回技能包的评分均值 / 次数 / 当前用户的打分。
func (e *Engine) GetSkillRating(ctx context.Context, tenantID, pkgID, user string) (avg float64, count, myScore int, err error) {
	if e.store == nil {
		return 0, 0, 0, fmt.Errorf("store not configured")
	}
	return e.store.GetSkillRating(ctx, tenantID, pkgID, user)
}

// ToggleFavorite 切换当前用户对技能包的收藏状态, 返回切换后的收藏态。
func (e *Engine) ToggleFavorite(ctx context.Context, tenantID, pkgID, user string) (bool, error) {
	if e.store == nil {
		return false, fmt.Errorf("store not configured")
	}
	if _, err := e.GetSkillPackage(ctx, tenantID, pkgID); err != nil {
		return false, err
	}
	return e.store.ToggleFavorite(ctx, tenantID, pkgID, user)
}

// IsFavorite 返回当前用户是否已收藏该技能包。
func (e *Engine) IsFavorite(ctx context.Context, tenantID, pkgID, user string) (bool, error) {
	if e.store == nil {
		return false, fmt.Errorf("store not configured")
	}
	return e.store.IsFavorite(ctx, tenantID, pkgID, user)
}

// ListFavorites 返回当前用户收藏的技能包列表(含评分与下载数)。
func (e *Engine) ListFavorites(ctx context.Context, tenantID, user string) []*SkillPackage {
	if e.store == nil {
		return []*SkillPackage{}
	}
	ids, err := e.store.ListFavoriteIDs(ctx, tenantID, user)
	if err != nil {
		slog.Error("list favorites failed", "err", err)
		return []*SkillPackage{}
	}
	out := make([]*SkillPackage, 0, len(ids))
	for _, id := range ids {
		if p, err := e.GetSkillPackage(ctx, tenantID, id); err == nil && p != nil {
			out = append(out, p)
		}
	}
	return out
}

// ListVersions 返回与指定技能包同名的全部版本(按更新时间倒序)。
func (e *Engine) ListVersions(ctx context.Context, tenantID, pkgID string) []*SkillPackage {
	p, err := e.GetSkillPackage(ctx, tenantID, pkgID)
	if err != nil || p == nil {
		return []*SkillPackage{}
	}
	if e.store == nil {
		return []*SkillPackage{p}
	}
	vs, err := e.store.ListSkillPackageVersions(ctx, tenantID, p.Name)
	if err != nil {
		slog.Error("list versions failed", "err", err)
		return []*SkillPackage{p}
	}
	return vs
}

// —— 自定义工具 payload 互转辅助 ——

func toToolPayload(t *CustomTool) CustomToolPayload {
	return CustomToolPayload{
		Name:        t.Name,
		Description: t.Description,
		Type:        t.Type,
		Enabled:     t.Enabled,
		Parameters:  t.Parameters,
		HTTPMethod:  t.HTTPMethod,
		HTTPURL:     t.HTTPURL,
		HTTPHeaders: t.HTTPHeaders,
		TimeoutSec:  t.TimeoutSec,
		CLICommand:  t.CLICommand,
	}
}

func fromToolPayload(tenantID string, t *CustomToolPayload) *CustomTool {
	now := time.Now()
	return &CustomTool{
		ID:          uuid.New().String(),
		TenantID:    tenantID,
		Name:        t.Name,
		Description: t.Description,
		Type:        t.Type,
		Enabled:     t.Enabled,
		Parameters:  t.Parameters,
		HTTPMethod:  t.HTTPMethod,
		HTTPURL:     t.HTTPURL,
		HTTPHeaders: t.HTTPHeaders,
		TimeoutSec:  t.TimeoutSec,
		CLICommand:  t.CLICommand,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
}
