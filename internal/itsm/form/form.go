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
	TypeUser        FieldType = "user"     // 选人（对接 IAM，P0 先用文本输入）
	TypeCI          FieldType = "ci"       // 选配置项（对接 CMDB，P0 先用文本输入）
	TypeCascader    FieldType = "cascader" // 级联（如 系统→环境，P0 先用文本输入）
)

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
	VisibleWhen string           `json:"visible_when,omitempty"` // 条件显隐，如 "risk_level==critical"
	ReadOnly    bool             `json:"readonly,omitempty"`
	Source      string           `json:"source,omitempty"` // 选项来源: static | cmdb:switch | iam:role
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
