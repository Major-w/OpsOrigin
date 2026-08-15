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
	// 兼容别名（旧代码引用）：platform≈基础设施资源，org≈业务资源
	ModelCategoryPlatform = ModelCategoryInfra
	ModelCategoryOrg      = ModelCategoryBusiness
)

// ModelCategories 返回默认内置分类名（前端兜底 / 种子使用）。
func ModelCategories() []string {
	return []string{ModelCategoryBusiness, ModelCategoryApp, ModelCategoryMiddleware, ModelCategoryDatabase, ModelCategoryInfra}
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
	Author      string             `json:"author,omitempty"` // 模型作者，内置为 anonymous
	Schema      map[string]AttrDef `json:"schema"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
}

type AttrDef struct {
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
	// Options 枚举选项(select/enum 类型), 前端展示为下拉。
	Options []string `json:"options,omitempty"`
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
	Labels        map[string]string   `json:"labels,omitempty"`
	Channel       ChannelWeight       `json:"channel"`
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
