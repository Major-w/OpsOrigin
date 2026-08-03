package model

import "time"

type CIStatus string

const (
	CIStatusActive   CIStatus = "active"
	CIStatusInactive CIStatus = "inactive"
	CIStatusDeleted  CIStatus = "deleted"
)

type ChannelWeight int

const (
	WeightHuman        ChannelWeight = 100
	WeightITSM         ChannelWeight = 80
	WeightAutoScript   ChannelWeight = 50
	WeightDiscovery    ChannelWeight = 30
)

type CIModelType struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	ParentID    string            `json:"parent_id,omitempty"`
	Schema      map[string]AttrDef `json:"schema"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

type AttrDef struct {
	Type        string `json:"type"`
	Required    bool   `json:"required"`
	Description string `json:"description,omitempty"`
	Default     any    `json:"default,omitempty"`
}

type CIModel struct {
	ID           string            `json:"id"`
	TypeID       string            `json:"type_id"`
	TenantID     string            `json:"tenant_id"`
	Name         string            `json:"name"`
	Description  string            `json:"description,omitempty"`
	Status       CIStatus          `json:"status"`
	Attributes   map[string]any    `json:"attributes"`
	AttributeMeta map[string]AttrMeta `json:"attribute_meta,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Channel      ChannelWeight     `json:"channel"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

// SourceKind 表示某个 CI 字段的数据来源。
type SourceKind string

const (
	SourceManual      SourceKind = "manual"       // 人工录入（最高置信）
	SourceITSM        SourceKind = "itsm"         // 从 ITSM 同步
	SourceAgent       SourceKind = "agent"        // 主机/容器 Agent 采集
	SourceCloudAPI    SourceKind = "cloud_api"    // 云厂商 API 拉取
	SourceAutoScript  SourceKind = "auto_script"  // 自动化脚本
	SourceDiscovery   SourceKind = "discovery"    // 自动发现（含模拟）
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
	ID           string `json:"id"`
	TenantID     string `json:"tenant_id"`
	SourceID     string `json:"source_id"`
	TargetID     string `json:"target_id"`
	RelationType string `json:"relation_type"`
	Properties   map[string]any `json:"properties,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
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
