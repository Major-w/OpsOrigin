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
	TypeDatetime    FieldType = "datetime" // 日期时间(如 2026-08-05T18:30:00+08:00 / 2026-08-05 18:30)
	TypeDuration    FieldType = "duration" // 时长(如 1h / 30m / 3d / 1h30m)
	TypeAttachment  FieldType = "attachment" // 附件(值为附件 ID 字符串数组)
	TypeRichText    FieldType = "richtext" // 富文本(值为 HTML/纯文本字符串)
	TypeTable       FieldType = "table"    // 表格(值为二维字符串数组 [][]string)
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

// FieldOption 选项（select / multiselect / cascader 用）
//
// Children 非空表示该项还有下一级，供 cascader 级联逐级展开；
// 扁平类型（select 等）忽略 Children。
type FieldOption struct {
	Value    string        `json:"value"`
	Label    string        `json:"label"`
	Children []FieldOption `json:"children,omitempty"`
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
	// 约定：cmdb:<typeName>（如 cmdb:host）、iam:user、iam:role、iam:group、
	// cmdb:types（CMDB 模型类型层级树，供 cascader 级联逐级展开）
	Source string `json:"source,omitempty"`

	// DictRef 引用数据字典 ID: 非空时本字段定义从字典展开(见 ExpandDictRefs)。
	DictRef string `json:"dict_ref,omitempty"`
	// Roles 可编辑角色白名单: 非空表示仅这些角色的用户可编辑, 空=所有人可编辑。
	Roles []string `json:"roles,omitempty"`

	// ColSpan 字段在 12 栅格中占用的列数(1~12)，0/缺省视为 12(整行)。
	// 由表单设计器画布拖拽调节，渲染端按 grid-cols-12 布局。
	ColSpan int `json:"col_span,omitempty"`

	// 附件类字段(TypeAttachment)的上传约束。
	// MaxSize 单文件最大体积(MB，0=不限)；Accept 允许的扩展名/MIME，逗号分隔(如 ".png,.pdf,image/*")；
	// MaxCount 最多文件数(0=不限)；Multiple 是否允许多选上传。
	// Multiple 用指针以区分「未配置(nil，老 schema，按不限处理)」与「显式单文件(false)」。
	MaxSize  int    `json:"max_size,omitempty"`
	Accept   string `json:"accept,omitempty"`
	MaxCount int    `json:"max_count,omitempty"`
	Multiple *bool  `json:"multiple,omitempty"`
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

// TreeSourceResolver 可选能力：解析器若支持层级数据源（cascader 级联）则实现之。
// parent 为空表示取根层级；非空表示取该父节点下的直接子节点（懒加载，避免一次拉整棵树）。
// 对没有层级语义的来源，实现方可退化为「parent 为空返回全量、非空返回空」。
type TreeSourceResolver interface {
	SourceResolver
	ResolveTree(ctx context.Context, tenantID, source, parent string) ([]FieldOption, error)
}

// FieldDict 数据字典: 可跨表单复用的字段定义集合。
// 表单字段通过 FieldDef.DictRef 引用字典, 展开后获得完整的字段定义(免复制粘贴)。
type FieldDict struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Name        string     `json:"name"`        // 字典名(唯一)
	Description string     `json:"description,omitempty"`
	Fields      []FieldDef `json:"fields"`      // 字段定义集(供 dict_ref 展开)
}

// DictStore 数据字典持久化能力(由 internal/itsm/store 的 PGStore / MemoryStore 实现)。
type DictStore interface {
	GetFieldDict(ctx context.Context, tenantID, id string) (*FieldDict, error)
	ListFieldDicts(ctx context.Context, tenantID string) ([]*FieldDict, error)
	UpsertFieldDict(ctx context.Context, tenantID string, dict *FieldDict) error
	DeleteFieldDict(ctx context.Context, tenantID, id string) error
}

// ExpandDictRefs 将 schema 中带 DictRef 的字段展开为字典里的字段定义。
// 找不到字典或字段 key 时保留原字段(容错, 不阻断表单)。
func ExpandDictRefs(schema *FormSchema, dicts map[string]*FieldDict) {
	if schema == nil {
		return
	}
	var expanded []FieldDef
	for _, f := range schema.Fields {
		if f.DictRef != "" {
			if d, ok := dicts[f.DictRef]; ok {
				for _, df := range d.Fields {
					// 保留原字段的分组/排序/必填覆盖, 其余以字典定义为准
					df.Section = f.Section
					if df.Order == 0 {
						df.Order = f.Order
					}
					if !f.Required {
						df.Required = f.Required
					}
					expanded = append(expanded, df)
				}
				continue
			}
		}
		expanded = append(expanded, f)
	}
	schema.Fields = expanded
}
