package model

import "time"

// ============================================================
// P2-1 IT 资源统计（stats）
// ============================================================

type CategoryStat struct {
	Category  string `json:"category"`
	Models    int    `json:"models"`
	Instances int    `json:"instances"`
}

type StatusStat struct {
	Status string `json:"status"`
	Count  int    `json:"count"`
}

type TypeStat struct {
	TypeID   string `json:"type_id"`
	TypeName string `json:"type_name"`
	Category string `json:"category"`
	Count    int    `json:"count"`
}

type AttrStat struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Count int    `json:"count"`
}

// CMDBStats 是 IT 资源统计大屏的数据结构。
type CMDBStats struct {
	TotalModels    int            `json:"total_models"`
	TotalInstances int            `json:"total_instances"`
	TotalRelations int            `json:"total_relations"`
	ByCategory     []CategoryStat `json:"by_category"`
	ByStatus       []StatusStat   `json:"by_status"`
	ByType         []TypeStat     `json:"by_type"` // Top N 模型实例数
	ByAttr         []AttrStat     `json:"by_attr"` // 按属性聚合（如 region/provider），Top N
}

// ============================================================
// P2-2 机房机柜视图（IDC / Rack / U 位）
// ============================================================

type IDCRoom struct {
	ID          string    `json:"id"`
	TenantID    string    `json:"tenant_id"`
	Name        string    `json:"name"`
	Location    string    `json:"location,omitempty"`
	Description string    `json:"description,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type Rack struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	IDCRoomID string    `json:"idc_id"`
	Name      string    `json:"name"` // 机柜编号，如 R01
	Row       string    `json:"row,omitempty"`
	Col       string    `json:"col,omitempty"`
	MaxUnits  int       `json:"max_units"` // U 位总数，默认 42
	CreatedAt time.Time `json:"created_at"`
}

// RackUnit 是一个 U 位：可被某 CI（设备）占用。
// (rack_id, unit) 为主键；ci_id 非空表示占用。
type RackUnit struct {
	RackID string `json:"rack_id"`
	Unit   int    `json:"unit"`
	CIID   string `json:"ci_id,omitempty"` // 占用该 U 位的 CI（设备）ID
	Label  string `json:"label,omitempty"` // 展示名（未绑 CI 时可手填）
}

// AssignRackUnitRequest 批量分配 U 位。
type AssignRackUnitRequest struct {
	Units []RackUnit `json:"units"`
}

// ============================================================
// P2-4 字段级规则权限
// ============================================================

// FieldRuleAction 字段访问动作。
type FieldRuleAction string

const (
	FieldActionHidden   FieldRuleAction = "hidden"   // 对无权限者隐藏字段
	FieldActionReadonly FieldRuleAction = "readonly" // 只读（不可编辑）
)

// FieldRule 定义「某模型某字段 × 某权限」的访问控制。
// 生效规则：同一字段命中多条时取最严格（hidden > readonly > 默认可见可编辑）。
type FieldRule struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	ModelTypeID string          `json:"model_type_id"`
	Field       string          `json:"field"`
	Permission  string          `json:"permission"` // 拥有该权限的用户受此规则约束，如 cmdb:admin / cmdb:write
	Action      FieldRuleAction `json:"action"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ============================================================
// P2-5 消息订阅（CI 变更推送）
// ============================================================

// SubscriptionEventType 订阅的事件类型。
type SubscriptionEventType string

const (
	SubEventCIAll      SubscriptionEventType = "*"
	SubEventCICreated  SubscriptionEventType = "ci_created"
	SubEventCIUpdated  SubscriptionEventType = "ci_updated"
	SubEventCIDeleted  SubscriptionEventType = "ci_deleted"
	SubEventCIArchived SubscriptionEventType = "ci_archived"
)

// SubscriptionChannel 订阅投递渠道。
type SubscriptionChannel string

const (
	SubChannelWebhook      SubscriptionChannel = "webhook"      // POST JSON 到 target URL
	SubChannelNotification SubscriptionChannel = "notification" // 走平台通知中心（注入回调）
)

type Subscription struct {
	ID          string                `json:"id"`
	TenantID    string                `json:"tenant_id"`
	Name        string                `json:"name"`
	ModelTypeID string                `json:"model_type_id,omitempty"` // 空=全部模型
	EventType   SubscriptionEventType `json:"event_type"`              // * 或具体事件
	Channel     SubscriptionChannel   `json:"channel"`
	Target      string                `json:"target"` // webhook=URL；notification=通知标题模板前缀（可为空）
	Enabled     bool                  `json:"enabled"`
	CreatedAt   time.Time             `json:"created_at"`
}

// SubscriptionDelivery 投递日志。
type SubscriptionDelivery struct {
	ID        string    `json:"id"`
	SubID     string    `json:"sub_id"`
	TenantID  string    `json:"tenant_id"`
	EventType string    `json:"event_type"`
	CIID      string    `json:"ci_id"`
	Payload   string    `json:"payload"`
	Status    string    `json:"status"` // success | failed
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// ============================================================
// P2-6 行级数据权限(we-cmdb 细粒度数据权限借鉴)
// 某角色对某模型类型的 CI, 仅可访问/操作满足条件的数据。
// ============================================================

// PolicyCondition 一条数据权限条件(AND 语义)。
type PolicyCondition struct {
	Attr  string `json:"attr"`  // CI 属性名
	Op    string `json:"op"`    // eq | in
	Value string `json:"value"` // 值或逗号分隔列表(in)
}

// DataPolicy 行级数据权限规则。
type DataPolicy struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Role        string            `json:"role"`          // 主体角色(如 admin/operator/viewer)
	ModelTypeID string            `json:"model_type_id"` // 目标模型类型
	Condition   []PolicyCondition `json:"condition"`     // AND 条件, 空=全部
	Action      string            `json:"action"`        // query | update | delete(空=query)
	CreatedAt   time.Time         `json:"created_at"`
}
