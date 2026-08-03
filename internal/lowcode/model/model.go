package model

import (
	"time"
)

// FieldType 字段类型
type FieldType string

const (
	FieldTypeText        FieldType = "text"
	FieldTypeNumber      FieldType = "number"
	FieldTypeBoolean     FieldType = "boolean"
	FieldTypeDate        FieldType = "date"
	FieldTypeDateTime    FieldType = "datetime"
	FieldTypeSelect      FieldType = "select"
	FieldTypeMultiSelect FieldType = "multi_select"
	FieldTypeEmail       FieldType = "email"
	FieldTypePhone       FieldType = "phone"
	FieldTypeURL         FieldType = "url"
	FieldTypeFile        FieldType = "file"
	FieldTypeRichText    FieldType = "rich_text"
	FieldTypeJSON        FieldType = "json"
	FieldTypeRelation    FieldType = "relation"
)

// FieldDefinition 字段定义
type FieldDefinition struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Label        string          `json:"label"`
	Type         FieldType       `json:"type"`
	Required     bool            `json:"required"`
	Unique       bool            `json:"unique"`
	DefaultValue any             `json:"default_value,omitempty"`
	Options      []FieldOption   `json:"options,omitempty"`
	Validation   *ValidationRule `json:"validation,omitempty"`
	Relation     *RelationConfig `json:"relation,omitempty"`
	SortOrder    int             `json:"sort_order"`
	Hidden       bool            `json:"hidden"`
	ReadOnly     bool            `json:"read_only"`
	Placeholder  string          `json:"placeholder,omitempty"`
	Description  string          `json:"description,omitempty"`
}

// FieldOption 字段选项
type FieldOption struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// ValidationRule 验证规则
type ValidationRule struct {
	Min       *float64 `json:"min,omitempty"`
	Max       *float64 `json:"max,omitempty"`
	MinLength *int     `json:"min_length,omitempty"`
	MaxLength *int     `json:"max_length,omitempty"`
	Pattern   string   `json:"pattern,omitempty"`
	CustomMsg string   `json:"custom_msg,omitempty"`
}

// RelationConfig 关联配置
type RelationConfig struct {
	TargetType  string `json:"target_type"`
	TargetField string `json:"target_field,omitempty"`
	Type        string `json:"type"` // one-to-one, one-to-many, many-to-many
}

// DataModel 数据模型
type DataModel struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	TableName   string            `json:"table_name"`
	Fields      []FieldDefinition `json:"fields"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// ModelInstance 模型实例
type ModelInstance struct {
	ID        string         `json:"id"`
	ModelID   string         `json:"model_id"`
	TenantID  string         `json:"tenant_id"`
	Data      map[string]any `json:"data"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// PageDefinition 页面定义
type PageDefinition struct {
	ID          string            `json:"id"`
	TenantID    string            `json:"tenant_id"`
	Name        string            `json:"name"`
	Description string            `json:"description,omitempty"`
	Type        PageType          `json:"type"`
	ModelID     string            `json:"model_id,omitempty"`
	Components  []UIComponent     `json:"components"`
	Layout      *PageLayout       `json:"layout,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	CreatedAt   time.Time         `json:"created_at"`
	UpdatedAt   time.Time         `json:"updated_at"`
}

// PageType 页面类型
type PageType string

const (
	PageTypeForm   PageType = "form"
	PageTypeList   PageType = "list"
	PageTypeDetail PageType = "detail"
	PageTypeCustom PageType = "custom"
)

// UIComponent UI组件
type UIComponent struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Label    string           `json:"label,omitempty"`
	Field    string           `json:"field,omitempty"`
	Props    map[string]any   `json:"props,omitempty"`
	Children []UIComponent    `json:"children,omitempty"`
	Layout   *ComponentLayout `json:"layout,omitempty"`
}

// ComponentLayout 组件布局
type ComponentLayout struct {
	Width   string      `json:"width,omitempty"`
	Height  string      `json:"height,omitempty"`
	Margin  string      `json:"margin,omitempty"`
	Padding string      `json:"padding,omitempty"`
	Flex    *FlexLayout `json:"flex,omitempty"`
}

// FlexLayout Flex布局
type FlexLayout struct {
	Direction string `json:"direction,omitempty"`
	Justify   string `json:"justify,omitempty"`
	Align     string `json:"align,omitempty"`
	Wrap      string `json:"wrap,omitempty"`
	Gap       string `json:"gap,omitempty"`
}

// PageLayout 页面布局
type PageLayout struct {
	Type     string        `json:"type"` // grid, flex, absolute
	Columns  int           `json:"columns,omitempty"`
	Gap      string        `json:"gap,omitempty"`
	Padding  string        `json:"padding,omitempty"`
	Children []UIComponent `json:"children,omitempty"`
}

// FormRule 表单规则
type FormRule struct {
	Field   string `json:"field"`
	Type    string `json:"type"` // required, min, max, pattern, custom
	Value   any    `json:"value,omitempty"`
	Message string `json:"message,omitempty"`
}

// FormData 表单数据
type FormData struct {
	ModelID string         `json:"model_id"`
	PageID  string         `json:"page_id,omitempty"`
	Data    map[string]any `json:"data"`
}
