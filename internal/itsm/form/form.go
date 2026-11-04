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
	TypeDatetime    FieldType = "datetime"   // 日期时间(如 2026-08-05T18:30:00+08:00 / 2026-08-05 18:30)
	TypeDuration    FieldType = "duration"   // 时长(如 1h / 30m / 3d / 1h30m)
	TypeAttachment  FieldType = "attachment" // 附件(值为附件 ID 字符串数组)
	TypeRichText    FieldType = "richtext"   // 富文本(值为 HTML/纯文本字符串)
	TypeTable       FieldType = "table"      // 表格(值为二维字符串数组 [][]string)
	TypeBool        FieldType = "bool"
	TypeUser        FieldType = "user"       // 选人（source=iam:user，前端下拉搜索）
	TypeCI          FieldType = "ci"         // 选配置项（source=cmdb:<typeName>，前端下拉搜索）
	TypeCascader    FieldType = "cascader"   // 级联（如 系统→环境）
	TypePassword    FieldType = "password"   // 密码（前端掩码输入，值为字符串）
	TypeTime        FieldType = "time"       // 时间（HH:mm）
	TypeTimeRange   FieldType = "timerange"  // 时间段（HH:mm~HH:mm）
	TypeLink        FieldType = "link"       // 链接（URL）
	TypeExpression  FieldType = "expression" // 计算/表达式（只读展示，值为表达式文本）
	TypeGroup       FieldType = "group"      // 分组容器（子表单轻量版：Children 内嵌字段，值仍平铺 custom_fields）
	TypeRating      FieldType = "rating"     // 评分（1~5 星，值为数字；对标 SN 5-point scale）
	TypeDateRange   FieldType = "daterange"  // 日期区间（值为 "开始~结束"，两端日期）
	TypeNote        FieldType = "note"       // 说明区块（纯展示 Help/Label 富说明，不产生值；对标 SN UI Macro/说明变量）
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
	// Price 选项价格（变量价格，对标 SN Variable Pricing）：>0 时前端费用合计计入。
	// 纯展示/汇总语义，后端不做校验与计价，仅透传保存。
	Price float64 `json:"price,omitempty"`
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
	// Builtin 内置字段标记：工单一等列(title/description/risk_level)以锁定卡片呈现在设计器中，
	// 标识与类型不可改，但标签/必填/宽度/分区可配置。仅作前端呈现与持久化标记，后端不强制。
	Builtin bool `json:"builtin,omitempty"`

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
	// DictKey 配合 DictRef 做字段级引用: 非空时只展开字典内 key 匹配的那一个字段
	// (空=展开字典全部字段, 保持旧行为)。
	DictKey string `json:"dict_key,omitempty"`
	// Children 分组容器(TypeGroup)的内嵌字段: 值仍平铺存 custom_fields(与顶层字段同层),
	// 校验/渲染/字典展开统一经 FlattenFields 展平处理, 存储结构与旧 schema 完全兼容。
	Children []FieldDef `json:"children,omitempty"`
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

	// Tab 字段所属页签的 key：FormSchema.Tabs 非空时生效，空=归入第一个页签。
	// 页签是纯渲染层分组（长表单分步），不影响存储与校验（值仍平铺 custom_fields）。
	Tab string `json:"tab,omitempty"`

	// Columns 表格字段(TypeTable)的列定义：非空时进入结构化模式（对标 SN MRVS），
	// 值从自由二维数组 [][]string 升级为「按列 key 组织的对象数组」[]map[string]string；
	// 空=旧行为（自由二维表格）。列定义在设计器配置，校验/渲染双端按列驱动。
	Columns []TableColumn `json:"table_columns,omitempty"`

	// Price 字段单价（变量价格，对标 SN Variable Pricing）：仅对可计价类型生效——
	// bool(勾选后计入单价)、number(值×单价)、select/multiselect(走选项级 Price)。
	// 纯前端汇总展示，后端不校验不参与服务端计价。
	Price float64 `json:"price,omitempty"`
}

// TableColumn 结构化表格的列定义（MRVS：变量即列）。
type TableColumn struct {
	Key      string `json:"key"`      // 列标识，行对象里的 key
	Label    string `json:"label"`    // 列头显示名
	Required bool   `json:"required"` // 该列必填（行级校验）
}

// FormTab 表单页签定义：长表单分步布局（对标 SN Form Designer 的 Tab 页）。
// Key 是 FieldDef.Tab 的取值；Label 为页签显示名。纯渲染层概念。
type FormTab struct {
	Key   string `json:"key"`
	Label string `json:"label"`
}

// FormSchema 一张可配置表单，按作用域定位
type FormSchema struct {
	Version int        `json:"version"`
	Scope   string     `json:"scope"` // 如 ticket:default / catalog:<id> / process:<id>:step:<step_id>
	Fields  []FieldDef `json:"fields"`

	// BehaviorRules UI 联动规则（schema 级）：条件命中时对目标字段施加显隐/必填/只读/可编辑动作。
	// 与字段级 VisibleWhenRules 互补：后者只描述"字段自己何时显示"，前者支持四类动作且可多目标。
	BehaviorRules []BehaviorRule `json:"behavior_rules,omitempty"`
	// ComboRules 跨字段组合校验：条件命中时按 severity 阻断(block)或提示(warn)。
	ComboRules []ComboRule `json:"combo_rules,omitempty"`
	// DataLinks 数据联动规则（"选 A 自动带出 B"）：条件命中时把固定值或其它字段值写入目标字段。
	DataLinks []DataLinkRule `json:"data_links,omitempty"`
	// DecisionTables 决策表：条件列 × 输出列的规则矩阵，首个命中行生效。
	DecisionTables []DecisionTable `json:"decision_tables,omitempty"`
	// Tabs 页签定义：非空时渲染端按 FieldDef.Tab 分页签展示（长表单分步）；
	// 未归入任何页签的字段落入第一个页签。存储/校验不受影响。
	Tabs []FormTab `json:"tabs,omitempty"`
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
	Name        string     `json:"name"` // 字典名(唯一)
	Description string     `json:"description,omitempty"`
	Fields      []FieldDef `json:"fields"` // 字段定义集(供 dict_ref 展开)
}

// DictStore 数据字典持久化能力(由 internal/itsm/store 的 PGStore / MemoryStore 实现)。
type DictStore interface {
	GetFieldDict(ctx context.Context, tenantID, id string) (*FieldDict, error)
	ListFieldDicts(ctx context.Context, tenantID string) ([]*FieldDict, error)
	UpsertFieldDict(ctx context.Context, tenantID string, dict *FieldDict) error
	DeleteFieldDict(ctx context.Context, tenantID, id string) error
}

// maxGroupDepth 分组嵌套深度上限（防环/防异常 schema 递归爆栈）。
const maxGroupDepth = 8

// FlattenFields 把字段列表递归展平为叶子字段：分组容器(TypeGroup)本身不产生值,
// 其 Children 与顶层字段同层平铺; 嵌套分组继续下钻(受 maxGroupDepth 保护)。
// 校验/默认值/计算字段/字典展开统一基于展平结果, 存储仍是平铺 custom_fields。
func FlattenFields(fields []FieldDef) []FieldDef {
	return flattenFieldsDepth(fields, 0)
}

func flattenFieldsDepth(fields []FieldDef, depth int) []FieldDef {
	if depth > maxGroupDepth {
		return nil
	}
	var out []FieldDef
	for _, f := range fields {
		if f.Type == TypeGroup {
			out = append(out, flattenFieldsDepth(f.Children, depth+1)...)
			continue
		}
		out = append(out, f)
	}
	return out
}

// ExpandDictRefs 将 schema 中带 DictRef 的字段展开为字典里的字段定义。
// DictKey 非空时只展开字典内 key 匹配的单个字段(字段级引用); 空则展开字典全部字段(旧行为)。
// 找不到字典或字段 key 时保留原字段(容错, 不阻断表单)。分组容器的 Children 同样递归展开。
//
// 引用式展开（对标 SN Variable Set）：展开产物保留 DictRef/DictKey 标记——schema 落库的
// 始终是引用而非副本，每次渲染（GET schema/校验）都从字典实时解析。这样设计器加载
// 展开结果再保存也不会断开关联，字典改一处全部引用它的表单全局生效，且展开幂等。
func ExpandDictRefs(schema *FormSchema, dicts map[string]*FieldDict) {
	if schema == nil {
		return
	}
	schema.Fields = expandDictFields(schema.Fields, dicts, 0)
}

func expandDictFields(fields []FieldDef, dicts map[string]*FieldDict, depth int) []FieldDef {
	if depth > maxGroupDepth {
		return fields
	}
	var expanded []FieldDef
	for _, f := range fields {
		if f.Type == TypeGroup {
			f.Children = expandDictFields(f.Children, dicts, depth+1)
			expanded = append(expanded, f)
			continue
		}
		if f.DictRef != "" {
			if d, ok := dicts[f.DictRef]; ok {
				var matched bool
				for _, df := range d.Fields {
					if f.DictKey != "" && df.Key != f.DictKey {
						continue // 字段级引用: 只取字典内匹配的单个字段
					}
					matched = true
					// 保留原字段的分组/排序/必填覆盖, 其余以字典定义为准
					df.Section = f.Section
					if df.Order == 0 {
						df.Order = f.Order
					}
					if !f.Required {
						df.Required = f.Required
					}
					// 引用式展开关键：保留引用标记，设计器保存后关联不断开，
					// 下次渲染仍以字典为准重新展开（幂等）。
					df.DictRef = f.DictRef
					df.DictKey = f.DictKey
					expanded = append(expanded, df)
					if f.DictKey != "" {
						break // 单字段引用只取一个
					}
				}
				if matched {
					continue
				}
			}
		}
		expanded = append(expanded, f)
	}
	return expanded
}
