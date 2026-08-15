// Package registry 实现「AI 模型注册表 / 本体服务」：
// 让 AI 全自动建模时既能"一次成型定义类型"，又受治理、不炸存储。
//
// 设计基线见 .docs/design-resource-model-and-ai-registry.md（阶段 1）。
// 核心能力：
//   - Type Resolver：AI 特征（端口/banner/engine）→ 高置信归已有 leaf / 低置信挂 abstract 父类 / 全新特征提交提案
//   - 相似度去重：提案提交时近重复比对，真新类型才建记录
//   - 晋升机制：低置信暂挂父类的类型，累积证据后提升置信度直至激活
//   - 治理字段：复用 CIModelType 已有的 Source/Confidence/Evidence（类型级），与字段级 AttrMeta 平行
//
// 存储不变：类型目录仍是 cmdb_ci_model_type 表的记录，无新增物理表（设计 2.3）。
package registry

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// TypeCatalog 是注册表依赖的窄接口，由 *pg.Repository 满足（鸭子类型，无需显式声明）。
// 仅抽取注册表所需的最小目录读写能力，避免与具体存储耦合。
type TypeCatalog interface {
	ListCIModelTypes(ctx context.Context, tenantID string, limit, offset int) ([]*model.CIModelType, error)
	GetCIModelType(ctx context.Context, id, tenantID string) (*model.CIModelType, error)
	GetCIModelTypeByName(ctx context.Context, name, tenantID string) (*model.CIModelType, error)
	CreateCIModelType(ctx context.Context, mt *model.CIModelType) error
	UpdateCIModelType(ctx context.Context, mt *model.CIModelType) error
}

// ResolutionKind 是 ResolveType 的解析结果分类。
type ResolutionKind string

const (
	ResolvedLeaf     ResolutionKind = "resolved_leaf"      // 高置信：特征精确命中已有可实例化 leaf
	AttachedToParent ResolutionKind = "attached_to_parent" // 低置信：特征可归家族但无精确 leaf，暂挂 abstract 父类
	ProposedNew      ResolutionKind = "proposed_new"       // 全新特征：已提交 AITypeProposal
)

// TypeResolution 是 ResolveType 的返回，描述"这个特征应该落到哪个类型"。
type TypeResolution struct {
	Kind       ResolutionKind `json:"kind"`
	TypeID     string         `json:"typeId"`               // 命中/挂载的类型 ID（leaf 或 abstract 父类）；ProposedNew 时为空
	TypeName   string         `json:"typeName"`             // 类型展示名
	Confidence float64        `json:"confidence"`           // 本次解析的置信度
	ProposalID string         `json:"proposalId,omitempty"` // 当 Kind=ProposedNew 时填写
	Reason     string         `json:"reason"`
}

// ProposalStatus 是 ProposeType 返回的提案处置状态（函数级语义；
// 持久化层以 CIModelType.Source="ai_proposed" + Confidence 阈值表达 active/pending，无独立状态列）。
type ProposalStatus string

const (
	ProposalActive  ProposalStatus = "active"  // 已激活（高置信自动注册或晋升通过）
	ProposalPending ProposalStatus = "pending" // 待审核（低置信新类型，需人工/累积证据）
	ProposalMerged  ProposalStatus = "merged"  // 命中已有类型，已合并证据
)

// AITypeProposal 是 AI 提交的"新类型提案"。
type AITypeProposal struct {
	ID                  string                   `json:"id"`
	TenantID            string                   `json:"tenantId"`
	Name                string                   `json:"name"`        // 建议类型 ID，如 mysql_8 / redis_cluster
	DisplayName         string                   `json:"displayName"` // 展示名
	Category            string                   `json:"category"`    // 五大类之一
	ParentID            string                   `json:"parentId,omitempty"`
	Features            map[string]any           `json:"features"`         // 端口 / banner / 指纹等原始特征
	Schema              map[string]model.AttrDef `json:"schema,omitempty"` // 提案自带的有效 schema
	Icon                string                   `json:"icon,omitempty"`
	Confidence          float64                  `json:"confidence"`
	Evidence            []string                 `json:"evidence"`
	SuggestedCollectors []string                 `json:"suggestedCollectors,omitempty"`
	Status              ProposalStatus           `json:"status"`
	CreatedAt           time.Time                `json:"createdAt"`
	UpdatedAt           time.Time                `json:"updatedAt"`
}

// TypeRegistry 是 AI 模型注册表 / 本体服务对外接口。
type TypeRegistry interface {
	// ResolveType 把 AI 特征解析为：已有 leaf / 挂载的 abstract 父类 / 新提案。
	ResolveType(ctx context.Context, tenantID string, features map[string]any) (TypeResolution, error)
	// ProposeType 提交新类型提案（相似度去重 + 审核门控）。返回类型 ID 与处置状态。
	ProposeType(ctx context.Context, proposal AITypeProposal) (id string, status ProposalStatus, err error)
	// PromoteType 证据累积后晋升：提升置信度，达阈值即视为激活。
	// 相对设计骨架补全 tenantID 参数，以匹配底层 catalog 的租户隔离。
	PromoteType(ctx context.Context, tenantID, id string, evidence []string) error
	// EffectiveSchema 返回某类型含父类继承的完整有效 schema。
	EffectiveSchema(ctx context.Context, tenantID, typeID string) (map[string]model.AttrDef, error)
}

// 阈值与启发式常量。
const (
	resolveLeafConfidence  = 0.95 // 精确命中 leaf 的置信度
	attachParentConfidence = 0.4  // 挂父类的置信度
	proposeConfidence      = 0.3  // 全新提案的初始置信度
	autoActivateThreshold  = 0.8  // ProposeType 达此置信度自动激活，否则 pending
	promoteStep            = 0.15 // 每次 Promote 提升的置信度步长
	mergeConfidenceBoost   = 0.05 // 去重合并时给已有类型的小幅置信度提升
)

// 端口 → 归一化类型 ID（与方案 C 种子 leaf ID 对齐）。
var portHints = map[int]string{
	3306:  "mysql",
	5432:  "postgresql",
	1521:  "oracle",
	27017: "mongodb",
	1433:  "mssql",
	6379:  "redis",
	9200:  "elasticsearch",
	9092:  "kafka",
	2181:  "zookeeper",
	5672:  "rabbitmq",
	11211: "memcached",
}

// banner 关键词 → 归一化类型 ID。
var bannerHints = []struct {
	keyword string
	typeID  string
}{
	{"mysql", "mysql"},
	{"postgres", "postgresql"},
	{"oracle", "oracle"},
	{"mongodb", "mongodb"},
	{"mssql", "mssql"},
	{"redis", "redis"},
	{"elasticsearch", "elasticsearch"},
	{"kafka", "kafka"},
	{"zookeeper", "zookeeper"},
	{"rabbitmq", "rabbitmq"},
	{"nginx", "nginx"},
	{"memcached", "memcached"},
}

var versionStrip = regexp.MustCompile(`[._\-]?v?\d+(\.\d+)*`)

type catalogRegistry struct {
	catalog TypeCatalog
}

// New 构造基于 TypeCatalog 的注册表实现。
func New(catalog TypeCatalog) TypeRegistry {
	return &catalogRegistry{catalog: catalog}
}

// listAll 分页拉取租户下全部类型，返回 ID→类型 的映射（供匹配与继承合并使用）。
func (r *catalogRegistry) listAll(ctx context.Context, tenantID string) (map[string]model.CIModelType, error) {
	out := make(map[string]model.CIModelType)
	offset := 0
	const batchSize = 200
	for {
		batch, err := r.catalog.ListCIModelTypes(ctx, tenantID, batchSize, offset)
		if err != nil {
			return nil, fmt.Errorf("list model types: %w", err)
		}
		for _, mt := range batch {
			out[mt.ID] = *mt
		}
		if len(batch) < batchSize {
			break
		}
		offset += batchSize
	}
	return out, nil
}

// ResolveType 把 AI 特征解析为具体处置。
func (r *catalogRegistry) ResolveType(ctx context.Context, tenantID string, features map[string]any) (TypeResolution, error) {
	types, err := r.listAll(ctx, tenantID)
	if err != nil {
		return TypeResolution{}, err
	}

	// 1) 精确命中：engine / port / banner 提取的候选归一化 ID 直接是某可实例化 leaf。
	for _, cand := range extractCandidates(features) {
		if mt, ok := types[cand]; ok && mt.IsInstantiable() {
			return TypeResolution{
				Kind:       ResolvedLeaf,
				TypeID:     mt.ID,
				TypeName:   mt.Name,
				Confidence: resolveLeafConfidence,
				Reason:     "特征精确命中已有可实例化 leaf",
			}, nil
		}
		if mt := findByName(types, cand); mt.ID != "" && mt.IsInstantiable() {
			return TypeResolution{
				Kind:       ResolvedLeaf,
				TypeID:     mt.ID,
				TypeName:   mt.Name,
				Confidence: resolveLeafConfidence,
				Reason:     "特征按名称命中已有可实例化 leaf",
			}, nil
		}
	}

	// 2) 低置信挂父类：特征可归数据库/中间件家族，但库内尚无精确 leaf。
	if parent := familyParent(types, features); parent.ID != "" {
		return TypeResolution{
			Kind:       AttachedToParent,
			TypeID:     parent.ID,
			TypeName:   parent.Name,
			Confidence: attachParentConfidence,
			Reason:     "特征可归 abstract 父类家族，但无精确 leaf，暂挂父类待晋升",
		}, nil
	}

	// 3) 全新特征：提交提案。
	prop := AITypeProposal{
		TenantID:   tenantID,
		Name:       normalizedNameFromFeatures(features),
		Features:   features,
		Confidence: proposeConfidence,
		Evidence:   evidenceFromFeatures(features),
		Category:   categoryFromFeatures(features),
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}
	id, status, err := r.ProposeType(ctx, prop)
	if err != nil {
		return TypeResolution{}, err
	}
	return TypeResolution{
		Kind:       ProposedNew,
		TypeName:   prop.Name,
		Confidence: proposeConfidence,
		ProposalID: id,
		Reason:     fmt.Sprintf("全新特征，已提交提案（%s）", status),
	}, nil
}

// ProposeType 提交新类型提案：先相似度去重，真新才建记录。
func (r *catalogRegistry) ProposeType(ctx context.Context, proposal AITypeProposal) (string, ProposalStatus, error) {
	normalized := normalizeTypeID(proposal.Name)
	types, err := r.listAll(ctx, proposal.TenantID)
	if err != nil {
		return "", "", err
	}

	// 相似度去重：归一化 ID / Name 与已有类型高度相似 → 合并证据，返回已有 ID。
	for _, mt := range types {
		if similar(normalizeTypeID(mt.ID), normalized) || similar(normalizeTypeID(mt.Name), normalized) {
			merged := dedupeStrings(append(mt.Evidence, proposal.Evidence...))
			mt.Evidence = merged
			mt.Confidence = clampF(mt.Confidence+mergeConfidenceBoost, 0, 1)
			mt.UpdatedAt = time.Now()
			if err := r.catalog.UpdateCIModelType(ctx, &mt); err != nil {
				return "", "", fmt.Errorf("merge proposal evidence: %w", err)
			}
			return mt.ID, ProposalMerged, nil
		}
	}

	// 真新类型：创建 CIModelType 记录（源标记 ai_proposed）。
	now := time.Now()
	id := normalized
	if id == "" {
		id = fmt.Sprintf("ai_%d", now.UnixNano())
	}
	mt := &model.CIModelType{
		ID:                  id,
		TenantID:            proposal.TenantID,
		Name:                orDefault(proposal.DisplayName, proposal.Name),
		Description:         fmt.Sprintf("AI 提案类型（%s）", proposal.Name),
		ParentID:            proposal.ParentID,
		Category:            proposal.Category,
		Icon:                proposal.Icon,
		Schema:              proposal.Schema,
		Abstract:            false,
		Author:              proposal.Name,
		Source:              string(model.SourceAIProposed),
		DiscoveredBy:        proposal.Name,
		Confidence:          proposal.Confidence,
		Evidence:            proposal.Evidence,
		SuggestedCollectors: proposal.SuggestedCollectors,
		Version:             1,
		CreatedAt:           now,
		UpdatedAt:           now,
	}

	// 审核门控：高置信自动激活，低置信待审核（以 confidence 表达）。
	status := ProposalPending
	if proposal.Confidence >= autoActivateThreshold {
		status = ProposalActive
		mt.Confidence = clampF(proposal.Confidence, autoActivateThreshold, 1)
	}
	if err := r.catalog.CreateCIModelType(ctx, mt); err != nil {
		return "", "", fmt.Errorf("create proposed type: %w", err)
	}
	return id, status, nil
}

// PromoteType 证据累积后晋升：提升置信度，达阈值即视为激活。
func (r *catalogRegistry) PromoteType(ctx context.Context, tenantID, id string, evidence []string) error {
	mt, err := r.catalog.GetCIModelType(ctx, id, tenantID)
	if err != nil {
		return fmt.Errorf("get model type: %w", err)
	}
	if mt == nil {
		return fmt.Errorf("model type %s not found", id)
	}
	now := time.Now()
	if len(evidence) > 0 {
		mt.Evidence = dedupeStrings(append(mt.Evidence, evidence...))
		mt.Confidence = clampF(mt.Confidence+promoteStep, 0, 1)
	}
	mt.UpdatedAt = now
	if err := r.catalog.UpdateCIModelType(ctx, mt); err != nil {
		return fmt.Errorf("promote model type: %w", err)
	}
	return nil
}

// EffectiveSchema 返回某类型含父类继承的完整有效 schema。
func (r *catalogRegistry) EffectiveSchema(ctx context.Context, tenantID, typeID string) (map[string]model.AttrDef, error) {
	mt, err := r.catalog.GetCIModelType(ctx, typeID, tenantID)
	if err != nil {
		return nil, fmt.Errorf("get model type: %w", err)
	}
	if mt == nil {
		return nil, fmt.Errorf("model type %s not found", typeID)
	}
	all, err := r.listAll(ctx, tenantID)
	if err != nil {
		return nil, err
	}
	vals := make(map[string]model.CIModelType, len(all))
	for id, m := range all {
		vals[id] = m
	}
	return mt.EffectiveSchema(vals), nil
}

// ---- 特征提取与启发式 ----

func extractCandidates(features map[string]any) []string {
	var cands []string
	for _, key := range []string{"engine", "middleware_type", "type", "name"} {
		if s, ok := toString(features[key]); ok && s != "" {
			cands = append(cands, normalizeTypeID(s))
		}
	}
	if p := toInt(features["port"]); p > 0 {
		if tid, ok := portHints[p]; ok {
			cands = append(cands, tid)
		}
	}
	if b, ok := toString(features["banner"]); ok && b != "" {
		bl := strings.ToLower(b)
		for _, h := range bannerHints {
			if strings.Contains(bl, h.keyword) {
				cands = append(cands, h.typeID)
			}
		}
	}
	return cands
}

func familyParent(types map[string]model.CIModelType, features map[string]any) model.CIModelType {
	dbFamily, mwFamily := false, false
	if p := toInt(features["port"]); p > 0 {
		switch p {
		case 3306, 5432, 1521, 27017, 1433:
			dbFamily = true
		case 6379, 9200, 9092, 2181, 5672, 11211:
			mwFamily = true
		}
	}
	if b, ok := toString(features["banner"]); ok {
		bl := strings.ToLower(b)
		if strings.Contains(bl, "sql") || strings.Contains(bl, "mysql") || strings.Contains(bl, "oracle") || strings.Contains(bl, "postgres") {
			dbFamily = true
		}
		if strings.Contains(bl, "redis") || strings.Contains(bl, "nginx") || strings.Contains(bl, "kafka") || strings.Contains(bl, "elasticsearch") {
			mwFamily = true
		}
	}
	if e, ok := toString(features["engine"]); ok {
		switch strings.ToLower(e) {
		case "mysql", "postgresql", "oracle", "mongodb", "mssql":
			dbFamily = true
		case "redis", "nginx", "kafka", "elasticsearch", "zookeeper", "rabbitmq", "memcached":
			mwFamily = true
		}
	}
	if dbFamily {
		if mt, ok := types[model.ModelTypeDatabase]; ok {
			return mt
		}
	}
	if mwFamily {
		if mt, ok := types[model.ModelTypeMiddleware]; ok {
			return mt
		}
	}
	return model.CIModelType{}
}

func findByName(types map[string]model.CIModelType, cand string) model.CIModelType {
	for _, mt := range types {
		if strings.EqualFold(mt.Name, cand) && mt.IsInstantiable() {
			return mt
		}
	}
	return model.CIModelType{}
}

func normalizedNameFromFeatures(features map[string]any) string {
	for _, key := range []string{"engine", "middleware_type", "type", "name"} {
		if s, ok := toString(features[key]); ok && s != "" {
			return normalizeTypeID(s)
		}
	}
	if b, ok := toString(features["banner"]); ok {
		bl := strings.ToLower(b)
		for _, h := range bannerHints {
			if strings.Contains(bl, h.keyword) {
				return h.typeID
			}
		}
	}
	return ""
}

func categoryFromFeatures(features map[string]any) string {
	if p := toInt(features["port"]); p > 0 {
		switch p {
		case 3306, 5432, 1521, 27017, 1433:
			return model.ModelCategoryDatabase
		case 6379, 9200, 9092, 2181, 5672, 11211:
			return model.ModelCategoryMiddleware
		}
	}
	return model.ModelCategoryInfra
}

func evidenceFromFeatures(features map[string]any) []string {
	var ev []string
	for _, key := range []string{"engine", "middleware_type", "port", "banner", "fingerprint"} {
		if s, ok := toString(features[key]); ok && s != "" {
			ev = append(ev, fmt.Sprintf("%s=%s", key, s))
		}
	}
	return ev
}

// ---- 归一化与相似度 ----

// normalizeTypeID 把任意引擎名归一化为稳定类型 ID：小写、去版本号、去分隔符、统一别名。
func normalizeTypeID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = versionStrip.ReplaceAllString(s, "")
	s = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(s)
	switch s {
	case "pg", "postgres", "psql":
		s = "postgresql"
	case "maria", "mariadb":
		s = "mariadb"
	case "es":
		s = "elasticsearch"
	case "zk":
		s = "zookeeper"
	case "mq":
		s = "rabbitmq"
	case "mongo", "mongod":
		s = "mongodb"
	case "memcache", "memcached":
		s = "memcached"
	case "rediscluster", "redis_cluster":
		s = "redis"
	}
	return s
}

// similar 判断两个归一化类型名是否近重复：完全相等，或编辑距离 ≤2 且长度差 ≤2。
func similar(a, b string) bool {
	if a == b {
		return true
	}
	if a == "" || b == "" {
		return false
	}
	if abs(len(a)-len(b)) > 2 {
		return false
	}
	return levenshtein(a, b) <= 2
}

func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = min3(curr[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

func min3(x, y, z int) int {
	m := x
	if y < m {
		m = y
	}
	if z < m {
		m = z
	}
	return m
}

// ---- 小工具 ----

func toString(v any) (string, bool) {
	switch s := v.(type) {
	case string:
		return s, s != ""
	case fmt.Stringer:
		return s.String(), true
	default:
		return "", false
	}
}

func toInt(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	case string:
		var i int
		if _, err := fmt.Sscanf(n, "%d", &i); err == nil {
			return i
		}
	}
	return 0
}

func dedupeStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

func orDefault(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}

// clampF 把 v 限制在 [lo, hi] 区间（不依赖外部库，避免引入额外依赖）。
func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
