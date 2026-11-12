// Package assets 提供 ITSM 的两种「配置资产」：决策表资产与动作模板。
//
// 设计目标：把原先只能内嵌在单张 FormSchema 里的决策表，提升为可跨表单复用的
// 独立资产，具备草稿/发布状态与不可变版本快照；动作模板同样版本化，流程 service
// 节点既可「复制为快照」也可「引用模板」（运行期按已发布版本解析）。
//
// 本包只依赖 internal/itsm/form（复用 DecisionTable 结构），不反向依赖 iam/store，
// 由 internal/itsm.Service 负责装配并把 PGStore 注入 form.Engine（分层单向）。
package assets

import (
	"errors"
	"time"

	"github.com/opsorigin/core/internal/itsm/form"
)

// 资产状态
const (
	StatusDraft     = "draft"     // 草稿：可编辑，引用不生效
	StatusPublished = "published" // 已发布：至少有一个版本，引用按版本解析
)

var ErrNotFound = errors.New("asset not found")

// DecisionTableAsset 决策表资产（独立管理、跨表单引用、版本发布）。
//
// Table 是当前草稿内容；发布时把 Table 快照写入 itsm_decision_table_versions，
// 并把 Version 递增、Status 置为 published。表单通过 form.DecisionTable.AssetRef
// 引用（version=0 跟随最新已发布版本，>0 锁定到指定不可变快照）。
type DecisionTableAsset struct {
	ID          string             `json:"id"`
	TenantID    string             `json:"tenant_id"`
	Name        string             `json:"name"`
	Description string             `json:"description,omitempty"`
	Status      string             `json:"status"`
	Version     int                `json:"version"`
	Table       form.DecisionTable `json:"table"`
	CreatedAt   time.Time          `json:"created_at"`
	UpdatedAt   time.Time          `json:"updated_at"`
	UsedBy      []string           `json:"used_by,omitempty"` // 列表端点附带：引用本资产的表单/流程
}

// DecisionTableVersion 决策表版本的不可变快照。
// 发布只追加新版本，历史版本永不改写；回滚 = 以历史快照再发布一个新版本。
type DecisionTableVersion struct {
	ID        string             `json:"id"`
	TenantID  string             `json:"tenant_id"`
	AssetID   string             `json:"asset_id"`
	Version   int                `json:"version"`
	Table     form.DecisionTable `json:"table"`
	Note      string             `json:"note,omitempty"`
	CreatedBy string             `json:"created_by,omitempty"`
	CreatedAt time.Time          `json:"created_at"`
}

// ActionTemplate 动作模板：5 种内置动作类型之一 + 预置参数。
//
// 两种消费方式并存：
//   - 配置期快照：流程设计器「复制为快照」把 ActionType/Inputs 复制进步骤，之后解耦；
//   - 运行期引用：步骤写 template_ref，执行前按已发布版本解析（version=0 跟随最新），
//     模板为权威来源、步骤 Inputs 同名覆盖，模板删除受 409 保护。
//
// 保存草稿不改动 Status/Version；PublishActionTemplate 才追加不可变版本快照。
type ActionTemplate struct {
	ID          string         `json:"id"`
	TenantID    string         `json:"tenant_id"`
	Name        string         `json:"name"`
	Description string         `json:"description,omitempty"`
	ActionType  string         `json:"action_type"`
	Inputs      map[string]any `json:"inputs"`
	Enabled     bool           `json:"enabled"`
	Status      string         `json:"status"`
	Version     int            `json:"version"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	UsedBy      []string       `json:"used_by,omitempty"` // 列表端点附带：引用本模板的流程步骤
}

// ActionTemplateVersion 动作模板版本的不可变快照（发布时写入）。
type ActionTemplateVersion struct {
	ID         string         `json:"id"`
	TenantID   string         `json:"tenant_id"`
	TemplateID string         `json:"template_id"`
	Version    int            `json:"version"`
	ActionType string         `json:"action_type"`
	Inputs     map[string]any `json:"inputs"`
	Note       string         `json:"note,omitempty"`
	CreatedBy  string         `json:"created_by,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
}

// BuiltinActionTypes 与 internal/automation/actions 的动作常量、前端 ACTION_DEFS 一一对齐。
// 模板保存时校验 ActionType 必须在此白名单内，避免设计出运行期必然失败的动作。
var BuiltinActionTypes = []string{
	"http", "notify", "create_ticket", "script", "cmdb_update",
}

// IsBuiltinActionType 判断动作类型是否受支持。
func IsBuiltinActionType(t string) bool {
	for _, v := range BuiltinActionTypes {
		if v == t {
			return true
		}
	}
	return false
}

// SyntheticSchema 用决策表的条件列 + 输出列合成虚拟 FormSchema。
//
// 资产不绑定具体表单，但 form.ApplyDataRules 只在输出列已声明为 FieldDef 时才写入结果；
// 独立试算与流程级决策绑定共用此函数，保证两条路径的推导结果一致。
func SyntheticSchema(t form.DecisionTable) *form.FormSchema {
	t.AssetRef = nil
	seen := map[string]bool{}
	fields := make([]form.FieldDef, 0, len(t.ConditionKeys)+len(t.OutputKeys))
	for _, k := range append(append([]string{}, t.ConditionKeys...), t.OutputKeys...) {
		if k == "" || seen[k] {
			continue
		}
		seen[k] = true
		fields = append(fields, form.FieldDef{Key: k, Label: k, Type: form.TypeText})
	}
	return &form.FormSchema{Fields: fields, DecisionTables: []form.DecisionTable{t}}
}

// ValidateTable 决策表资产内容校验：复用表单侧同一套列/行校验器。
// 与「列必须在表单字段中声明」无关（那一层在表单保存时按真实字段再校验一次）。
func ValidateTable(t form.DecisionTable) ([]string, error) {
	return form.ValidateDecisionTables(SyntheticSchema(t))
}
