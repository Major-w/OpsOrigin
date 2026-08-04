// Package form 实现 ITSM 可配置表单（Form Schema）的模型与校验引擎。
//
// 设计目标：把"工单/流程长什么样"从代码里抽出来变成数据（schema 存库），
// 后端按 schema 校验、前端按 schema 渲染。系统字段（title/risk/status...）仍是一等列，
// 业务自定义字段走 Ticket.CustomFields(JSONB)。该包不依赖 store / itsm，避免循环引用。
package form

import "context"

// FieldType 字段类型
type FieldType string

const (
	TypeText        FieldType = "text"        // 单行文本
	TypeTextarea    FieldType = "textarea"    // 多行文本
	TypeSelect      FieldType = "select"      // 单选（options）
	TypeMultiSelect FieldType = "multiselect" // 多选（options）
	TypeNumber      FieldType = "number"
	TypeDate        FieldType = "date"
	TypeBool        FieldType = "bool"
	TypeUser        FieldType = "user"     // 选人（source=iam:user，前端下拉搜索）
	TypeCI          FieldType = "ci"       // 选配置项（source=cmdb:<typeName>，前端下拉搜索）
	TypeCascader    FieldType = "cascader" // 级联（如 系统→环境）
)

// 条件运算符
const (
	OpEq       = "eq"        // 等于
	OpNeq      = "neq"       // 不等于
	OpIn       = "in"        // 属于（Value 用逗号分隔，或多选值命中其一）
	OpNotIn    = "not_in"    // 不属于
	OpContains = "contains"  // 包含（字符串子串 / 多选包含）
	OpEmpty    = "empty"     // 为空
	OpNotEmpty = "not_empty" // 非空
	OpGt       = "gt"        // 大于（数值）
	OpGte      = "gte"       // 大于等于
	OpLt       = "lt"        // 小于
	OpLte      = "lte"       // 小于等于
	OpIsTrue   = "is_true"   // 布尔真
	OpIsFalse  = "is_false"  // 布尔假
)

// 规则组合逻辑
const (
	LogicAnd = "and" // 规则间与（默认）
	LogicOr  = "or"  // 规则间或
)

// Condition 单条显隐规则：<Field> <Op> <Value>
type Condition struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// FieldOption 选项（select / multiselect 用）
type FieldOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// FieldValidation 字段级校验
type FieldValidation struct {
	Regex  string   `json:"regex,omitempty"`   // 正则匹配（字符串类型）
	Min    *float64 `json:"min,omitempty"`     // 数值下界
	Max    *float64 `json:"max,omitempty"`     // 数值上界
	MinLen int      `json:"min_len,omitempty"` // 字符串最小长度
	MaxLen int      `json:"max_len,omitempty"` // 字符串最大长度
}

// FieldDef 单个字段定义
type FieldDef struct {
	Key         string           `json:"key"`   // 字段标识，存 custom_fields 的 key
	Label       string           `json:"label"` // 显示名
	Type        FieldType        `json:"type"`
	Options     []FieldOption    `json:"options,omitempty"` // select / multiselect
	Required    bool             `json:"required"`
	Default     any              `json:"default,omitempty"`
	Placeholder string           `json:"placeholder,omitempty"`
	Help        string           `json:"help,omitempty"`
	Section     string           `json:"section,omitempty"` // 分组/分区
	Order       int              `json:"order"`
	Validation  *FieldValidation `json:"validation,omitempty"`
	ReadOnly    bool             `json:"readonly,omitempty"`

	// 条件显隐（两种写法并存，以 VisibleWhenRules 优先）
	// - VisibleWhen：旧版单条表达式，如 "risk_level==critical"，保留向后兼容
	// - VisibleWhenRules + VisibleLogic：多规则组合，可视化配置产出
	VisibleWhen      string      `json:"visible_when,omitempty"`
	VisibleWhenRules []Condition `json:"visible_when_rules,omitempty"`
	VisibleLogic     string      `json:"visible_logic,omitempty"` // and(默认) | or

	// Source 动态选项来源，为空表示用静态 Options。
	// 约定：cmdb:<typeName>（如 cmdb:host）、iam:user、iam:role、iam:group
	Source string `json:"source,omitempty"`
}

// FormSchema 一张可配置表单，按作用域定位
type FormSchema struct {
	Version int        `json:"version"`
	Scope   string     `json:"scope"` // 如 ticket:default / catalog:<id> / process:<id>:step:<step_id>
	Fields  []FieldDef `json:"fields"`
}

// SchemaStore 表单 schema 持久化能力（由 internal/itsm/store 的 PGStore / MemoryStore 实现）。
type SchemaStore interface {
	GetFormSchema(ctx context.Context, tenantID, scope string) (*FormSchema, error)
	UpsertFormSchema(ctx context.Context, tenantID, scope string, schema FormSchema) error
	ListFormScopes(ctx context.Context, tenantID string) ([]string, error)
}

// SourceResolver 动态选项解析器：把 FieldDef.Source 解析成可选项列表。
// 由外层（internal/itsm）实现并注入，form 包不依赖 cmdb / iam，避免循环引用。
// 实现方按前缀路由：cmdb:* 走 CMDB、iam:* 走 IAM/用户中心。
type SourceResolver interface {
	Resolve(ctx context.Context, tenantID, source string) ([]FieldOption, error)
}
