package model

import "time"

type CIStatus string

const (
	CIStatusActive   CIStatus = "active"
	CIStatusInactive CIStatus = "inactive"
	CIStatusArchived CIStatus = "archived" // 归档：暂时保留却不展示在普通列表，可随时恢复
	CIStatusDeleted  CIStatus = "deleted"
)

type ChannelWeight int

const (
	WeightHuman      ChannelWeight = 100
	WeightITSM       ChannelWeight = 80
	WeightAutoScript ChannelWeight = 50
	WeightDiscovery  ChannelWeight = 30
)

// 模型分类（默认 5 大类，用户可扩展）：
// 业务资源 / 应用资源 / 中间件资源 / 数据库资源 / 基础设施资源。
// 分类由 cmdb_model_category 表承载（内置 5 类 + 用户自定义），
// 此处常量仅作内置种子与默认值，不再作为白名单。
const (
	ModelCategoryBusiness   = "业务资源"
	ModelCategoryApp        = "应用资源"
	ModelCategoryMiddleware = "中间件资源"
	ModelCategoryDatabase   = "数据库资源"
	ModelCategoryInfra      = "基础设施资源"
	ModelCategoryContainer  = "容器资源"
	// 兼容别名（旧代码引用）：platform≈基础设施资源，org≈业务资源
	ModelCategoryPlatform = ModelCategoryInfra
	ModelCategoryOrg      = ModelCategoryBusiness
)

// ModelCategories 返回默认内置分类名（前端兜底 / 种子使用）。
func ModelCategories() []string {
	return []string{ModelCategoryBusiness, ModelCategoryApp, ModelCategoryMiddleware, ModelCategoryDatabase, ModelCategoryInfra, ModelCategoryContainer}
}

// BuiltinAuthor 是系统内置模型的作者标识（内置模型治理语义）：
// 内置模型不可删除、不可导入导出、不可删除内置属性（仅可加属性）。
const BuiltinAuthor = "anonymous"

// 内置模型类型 ID（与 internal/cmdb/discovery/plugins 产出的 CIType 对齐，
// 由 EnsureDiscoveryModelTypes 启动时确保存在，发现结果方可落库）。
const (
	ModelTypeServer          = "server"           // 基础设施：物理服务器 / 裸金属
	ModelTypeNetwork         = "switch"           // 基础设施：网络设备（交换机/路由器）
	ModelTypeDatabase        = "database"         // 数据库资源：数据库实例
	ModelTypeMiddleware      = "middleware"       // 中间件资源：Redis/Nginx/ES/Kafka 等
	ModelTypeApp             = "app"              // 应用资源：应用服务实例
	ModelTypeBusinessService = "business_service" // 业务资源：业务服务（服务建模顶层）
	ModelTypeKubernetes      = "kubernetes"       // 容器资源：K8S 容器平台（抽象父类，集群/节点/Pod/工作负载等共性承载）
)

// 预置关系类型（RelationType 仍为自由字符串，常量仅规范服务建模链语义）：
//
//	业务服务 -depends_on-> 应用服务 -hosts-> 服务器
//	应用服务 -depends_on-> 数据库 / 中间件
const (
	RelationDependsOn = "depends_on" // 依赖：A 依赖 B，B 故障向上影响 A
	RelationHosts     = "hosts"      // 承载：服务器承载应用/中间件
	RelationConnects  = "connects"   // 连接：网络拓扑（服务器↔交换机）
)

// 关系基数（从声明模型视角）。
const (
	CardinalityOneToOne   = "one_to_one"   // 一对一：源至多 1 目标，目标至多被 1 源引用
	CardinalityOneToMany  = "one_to_many"  // 一对多：源可多目标，目标至多被 1 源引用（目标唯一归属）
	CardinalityManyToMany = "many_to_many" // 多对多：无基数约束
)

// RelationDef 模型级关系声明（与 Schema 并列，存入 cmdb_ci_model_type.relations）。
// 声明后，实例间建立该 relation_type 的关系时由后端按 TargetModel + Cardinality 校验。
// 未在此声明的"自由关系"仍可建立（兼容自动发现/历史关系），但跳过基数约束。
type RelationDef struct {
	// ID 是关系的稳定机器标识(关系级寻址), 供采集上报按 模型ID-关系ID 建立关系、
	// 以及属性级权限按关系引用。旧数据无 ID 时由 ResolveModelType 兼容补全为 RelationType。
	ID           string `json:"id,omitempty"`
	Name         string `json:"name"`                   // 关系展示名，如 "部署于"
	RelationType string `json:"relation_type"`          // 关系类型键（自由字符串，建议复用内置常量）
	TargetModel  string `json:"target_model"`           // 目标模型类型 ID
	Cardinality  string `json:"cardinality"`            // one_to_one | one_to_many | many_to_many
	ReverseName  string `json:"reverse_name,omitempty"` // 对端显示名（可选），如 "被部署于"
	Description  string `json:"description,omitempty"`  // 说明（可选）
}

// 业务关键性等级（服务建模标准字段 business_criticality 的取值）。
type Criticality string

const (
	CriticalityCritical Criticality = "critical" // 核心业务，故障即业务中断
	CriticalityHigh     Criticality = "high"
	CriticalityMedium   Criticality = "medium"
	CriticalityLow      Criticality = "low"
)

// AttrKeyCriticality 是业务服务等模型的标准关键性字段名。
const AttrKeyCriticality = "business_criticality"

type CIModelType struct {
	ID          string             `json:"id"`
	TenantID    string             `json:"tenant_id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	ParentID    string             `json:"parent_id,omitempty"` // 模型继承：父类模型 ID
	Category    string             `json:"category,omitempty"`  // app|platform|infra|org，默认 platform
	SubCategory string             `json:"sub_category,omitempty"`
	Icon        string             `json:"icon,omitempty"`
	ShowInMenu  bool               `json:"show_in_menu"`     // 在顶部导航菜单显示
	Builtin     bool               `json:"builtin"`          // 内置模型（不可删/导入导出）
	Abstract    bool               `json:"abstract"`         // 抽象父类（不可直接实例化，仅作继承基类）
	Author      string             `json:"author,omitempty"` // 模型作者，内置为 anonymous
	Schema      map[string]AttrDef `json:"schema"`
	// Relations 模型级关系声明（与 Schema 并列）。声明后建关系按 TargetModel + 基数校验。
	Relations []RelationDef `json:"relations,omitempty"`

	// 类型级治理字段（为 AI 自动建模 / 类型注册表打底，与 ci.go 已有的
	// 字段级治理 AttrMeta{Source,Confidence,LastSeenAt} 平行互补：
	// 字段级管「值」的置信度，类型级管「归类」的置信度）。
	Source              string   `json:"source,omitempty"`        // seed | discovered | ai_proposed
	DiscoveredBy        string   `json:"discovered_by,omitempty"` // 发现 / 提案主体
	Confidence          float64  `json:"confidence,omitempty"`    // 类型级置信度
	Evidence            []string `json:"evidence,omitempty"`      // 证据（banner / 端口 / 指纹）
	Version             int      `json:"version,omitempty"`
	SuggestedCollectors []string `json:"suggested_collectors,omitempty"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// EffectiveSchema 返回该类型「含父类继承」的完整有效 schema。
// 递归合并 ParentID 链上各代 Schema：父类字段先入，子类以同名覆盖、新字段追加。
// all 必须是「类型 ID → CIModelType」的全集（至少覆盖从本类型到根的整条继承链），
// 缺失的祖先会被安全跳过（仅返回已知部分）。
func (t CIModelType) EffectiveSchema(all map[string]CIModelType) map[string]AttrDef {
	eff := make(map[string]AttrDef)
	if t.ParentID != "" {
		if p, ok := all[t.ParentID]; ok {
			for k, v := range p.EffectiveSchema(all) {
				eff[k] = v
			}
		}
	}
	for k, v := range t.Schema {
		eff[k] = v // 子类覆盖父类同名
	}
	return eff
}

// IsInstantiable 返回该类型是否可被直接实例化（建 CI）。
// Abstract=true 的父类（如 database/middleware）仅作继承基类，禁止直接建实例。
func (t CIModelType) IsInstantiable() bool {
	return !t.Abstract
}

// FieldByNameOrID 按字段名或字段 ID 反查属性定义(采集上报按 field_id 定位写入时使用)。
// 优先精确匹配 ID, 其次匹配 Name, 兼容旧数据无 ID 场景。
func (t CIModelType) FieldByNameOrID(key string) (AttrDef, bool) {
	for name, def := range t.Schema {
		if def.ID != "" && def.ID == key {
			return def, true
		}
		if name == key {
			return def, true
		}
	}
	return AttrDef{}, false
}

// RelationByID 按关系 ID 或 relation_type 反查关系定义(采集上报按 relation_id 建关系时使用)。
func (t CIModelType) RelationByID(key string) (RelationDef, bool) {
	for _, r := range t.Relations {
		if r.ID != "" && r.ID == key {
			return r, true
		}
		if r.RelationType == key {
			return r, true
		}
	}
	return RelationDef{}, false
}

type AttrDef struct {
	// ID 是属性的稳定机器标识(属性级寻址), 供采集上报按 模型ID-字段ID 定位写入、
	// 以及属性级权限按字段引用。业务主键仍是字段 Name(前端寻址/展示);
	// 重命名字段时 ID 不变, 不破坏采集映射。旧数据无 ID 时由 ResolveModelType 兼容补全为 Name。
	ID          string `json:"id,omitempty"`
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
	// Unique 声明该字段在「同一模型类型 + 租户」内取值唯一,
	// 写入/更新时由 validateAttributes 拦截重复(而非仅质量中心事后报告)。
	Unique bool `json:"unique,omitempty"`
	// RefModel 引用型字段: Type=="ref" 时指向目标模型类型 ID,
	// 前端渲染为从该模型 CI 实例取选项的下拉选择器, 写入时校验目标 CI 存在。
	RefModel string `json:"ref_model,omitempty"`
	// RefDisplay 引用型字段的展示属性(默认 name)。
	RefDisplay string `json:"ref_display,omitempty"`
	// AutoFill 自动填充规则(we-cmdb autofillRule 借鉴): ref 字段选中目标 CI 后,
	// 自动把目标 CI 的这些属性值填入本 CI(如选机柜自动带出机房/数据中心)。
	AutoFill []string `json:"autofill,omitempty"`
	// Options 枚举选项(select/enum 类型), 前端展示为下拉。
	Options []string `json:"options,omitempty"`
	// Multi 枚举多选: Type==enum 且 Multi=true 时, 实例值为数组(如 ["prod","dev"])。
	Multi bool `json:"multi,omitempty"`
	// Pattern 文本类(string/longtext)正则校验(如邮箱/手机/IP), 前端录入时校验。
	Pattern string `json:"pattern,omitempty"`
	// MaxLength 文本类最大长度(string<=256 / longtext<=2000 等语义)。
	MaxLength int `json:"max_length,omitempty"`
	// Multiline 长文本标识: Type==longtext 时用多行输入(textarea)。
	Multiline bool `json:"multiline,omitempty"`
	// Min/Max 数值类(number/float)取值范围[Min,Max]。
	Min float64 `json:"min,omitempty"`
	Max float64 `json:"max,omitempty"`
	// TimeInclude 日期类含时分秒: Type==datetime 时启用(等价 date + 时间)。
	TimeInclude bool `json:"time_include,omitempty"`
	// Aggregate 聚合字段(we-cmdb count/sum/avg 借鉴):
	// 读取 CI 时按关系链对关联 CI 实时统计, 结果写入响应 CIModel.Aggregates。
	// 例: 机柜 rack 声明 {op:count, relation:hosts} → 自动统计该机柜下设备数。
	Aggregate *AttrAggregate `json:"aggregate,omitempty"`
}

// AttrAggregate 聚合字段定义。
type AttrAggregate struct {
	Op string `json:"op"` // count | sum | avg
	// Target sum/avg 时统计的关联 CI 属性名(count 忽略)。
	Target string `json:"target,omitempty"`
	// Relation 沿哪条关系统计(空=任意关系)。
	Relation string `json:"relation,omitempty"`
	// Direction 关系方向: out=从本 CI 指向目标(默认), in=其他 CI 指向本 CI。
	Direction string `json:"direction,omitempty"`
}

type CIModel struct {
	ID            string              `json:"id"`
	TypeID        string              `json:"type_id"`
	TenantID      string              `json:"tenant_id"`
	Name          string              `json:"name"`
	Description   string              `json:"description,omitempty"`
	Status        CIStatus            `json:"status"`
	Attributes    map[string]any      `json:"attributes"`
	AttributeMeta map[string]AttrMeta `json:"attribute_meta,omitempty"`
	// Aggregates 聚合字段计算结果(模型 schema 声明 Aggregate 时, 读取 CI 填充)。
	Aggregates map[string]any    `json:"aggregates,omitempty"`
	Labels     map[string]string `json:"labels,omitempty"`
	Channel    ChannelWeight     `json:"channel"`
	// FieldAccess 字段级规则权限结果：field → hidden|readonly|visible（P2-4）。
	// hidden 字段已从 attributes/attribute_meta 中剔除，前端据此渲染只读/隐藏。
	FieldAccess map[string]string `json:"field_access,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// SourceKind 表示某个 CI 字段的数据来源。
type SourceKind string

const (
	SourceManual     SourceKind = "manual"      // 人工录入（最高置信）
	SourceITSM       SourceKind = "itsm"        // 从 ITSM 同步
	SourceAgent      SourceKind = "agent"       // 主机/容器 Agent 采集
	SourceCloudAPI   SourceKind = "cloud_api"   // 云厂商 API 拉取
	SourceAutoScript SourceKind = "auto_script" // 自动化脚本
	SourceDiscovery  SourceKind = "discovery"   // 自动发现（含模拟）
	SourceAIProposed SourceKind = "ai_proposed" // AI 自动建模提交的提案类型
)

// AttrMeta 是单个 CI 属性的数据来源与置信度元数据。
// 与 CIModel.Attributes 的键一一对应，平行存储，不污染业务属性本身。
type AttrMeta struct {
	Source     SourceKind `json:"source"`
	Confidence float64    `json:"confidence"` // 0~1
	LastSeenAt time.Time  `json:"last_seen_at"`
}

// DefaultConfidence 返回某来源的默认置信度（兜底值，采集器可显式上报覆盖）。
func DefaultConfidence(src SourceKind) float64 {
	switch src {
	case SourceManual:
		return 1.0
	case SourceITSM:
		return 0.85
	case SourceAgent:
		return 0.9
	case SourceCloudAPI:
		return 0.8
	case SourceAutoScript:
		return 0.5
	case SourceDiscovery:
		return 0.3
	case SourceAIProposed:
		return 0.5
	default:
		return 0.5
	}
}

// SourceWeight 返回某来源的 anti-flapping 权重（与 ChannelWeight 一致语义），
// 用于未来把 anti-flapping 从「整 CI 级」升级到「字段级」（只跳过人工改过的字段）。
func SourceWeight(src SourceKind) ChannelWeight {
	switch src {
	case SourceManual:
		return WeightHuman
	case SourceITSM:
		return WeightITSM
	case SourceAutoScript:
		return WeightAutoScript
	default:
		return WeightDiscovery
	}
}

// SourceFromChannelWeight 把 ChannelWeight 映射回 SourceKind（兜底用，
// 区分不出 cloud_api 与 agent，故采集路径应在调用前注入具体 source）。
func SourceFromChannelWeight(ch ChannelWeight) SourceKind {
	switch ch {
	case WeightHuman:
		return SourceManual
	case WeightITSM:
		return SourceITSM
	case WeightAutoScript:
		return SourceAutoScript
	default:
		return SourceDiscovery
	}
}

// BuildAttrMeta 按给定来源为 CI 所有属性生成字段级元数据（兜底用）。
// 采集路径会在调用前注入更精确的 source（cloud_api / agent），此处仅作默认值。
func BuildAttrMeta(attrs map[string]any, src SourceKind, now time.Time) map[string]AttrMeta {
	m := make(map[string]AttrMeta, len(attrs))
	for k := range attrs {
		m[k] = AttrMeta{Source: src, Confidence: DefaultConfidence(src), LastSeenAt: now}
	}
	return m
}

// ShouldOverwriteField 字段级 anti-flapping 决策：返回 true 表示新来源可覆盖既有字段。
// 规则：仅当新来源信任权重 >= 既有来源权重时允许覆盖。
// 人工录入(manual,100) 永远高于采集来源(≤30)，因此采集链路不会覆盖人工改过的字段，
// 但仍可继续刷新同一 CI 上其它非人工字段（从整 CI 级拦截升级到字段级）。
func ShouldOverwriteField(incoming, existing SourceKind) bool {
	return SourceWeight(incoming) >= SourceWeight(existing)
}

type CIRelation struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id"`
	SourceID     string         `json:"source_id"`
	TargetID     string         `json:"target_id"`
	RelationType string         `json:"relation_type"`
	Properties   map[string]any `json:"properties,omitempty"`
	CreatedAt    time.Time      `json:"created_at"`
}

type AttributeChange struct {
	CIID        string        `json:"ci_id"`
	AttrName    string        `json:"attr_name"`
	OldValue    any           `json:"old_value"`
	NewValue    any           `json:"new_value"`
	Channel     ChannelWeight `json:"channel"`
	ChannelDesc string        `json:"channel_desc"`
	ChangedAt   time.Time     `json:"changed_at"`
}
