package form

import "context"

// DecisionTableAssetRef 表单对「决策表资产」的引用声明。
//
// 与 FieldDef.DictRef 同构：schema 落库的始终是引用标记而非内容副本，
// 每次求值/读取时按已发布版本实时展开，资产发布新版本后所有跟随版表单立即生效。
//
// Version = 0 表示跟随资产的最新已发布版本；>0 表示锁定到该不可变版本快照。
// 资产未发布（或被删除）时引用解析为空表，决策表不产生输出，不阻断工单流转。
type DecisionTableAssetRef struct {
	AssetID string `json:"asset_id"`
	Version int    `json:"version,omitempty"`
}

// DecisionTableResolver 把「资产引用」解析成可求值的决策表快照。
// form 包不依赖 assets/store：接口由 internal/itsm/assets.PGStore 实现，
// 经 Engine 注入（分层单向，与 CIAttrResolver / SubmitterAttrResolver 同范式）。
//
// 返回 (nil, nil) 表示资产不存在/未发布/版本缺失 —— 调用方按容错跳过该表。
type DecisionTableResolver interface {
	ResolveDecisionTable(ctx context.Context, tenantID, assetID string, version int) (*DecisionTable, error)
}

// HasDecisionRefs 报告 schema 是否含资产引用（用于跳过无谓的 store 查询）。
func HasDecisionRefs(schema *FormSchema) bool {
	if schema == nil {
		return false
	}
	for i := range schema.DecisionTables {
		if schema.DecisionTables[i].AssetRef != nil {
			return true
		}
	}
	return false
}

// ExpandDecisionRefs 把 schema 中带 AssetRef 的决策表展开为已发布版本的实际内容。
//
// 返回浅拷贝（不修改入参），且展开产物保留 AssetRef 标记 —— 与 ExpandDictRefs 的
// 「引用式展开」语义一致：设计器加载展开结果再保存也不会把快照烤进表单，
// 保存侧由 NormalizeDecisionRefs 重新归一为纯引用，展开幂等。
//
// resolver 为 nil / 解析失败 / 资产不存在时保留空引用表（容错，不阻断表单）。
func ExpandDecisionRefs(ctx context.Context, tenantID string, schema *FormSchema, resolver DecisionTableResolver) (*FormSchema, error) {
	if schema == nil || !HasDecisionRefs(schema) {
		return schema, nil
	}
	out := *schema // 浅拷贝：Fields / DataLinks 等切片只读复用
	out.DecisionTables = make([]DecisionTable, 0, len(schema.DecisionTables))
	for _, dt := range schema.DecisionTables {
		if dt.AssetRef == nil || dt.AssetRef.AssetID == "" {
			out.DecisionTables = append(out.DecisionTables, dt)
			continue
		}
		if resolver == nil || ctx == nil {
			out.DecisionTables = append(out.DecisionTables, dt)
			continue
		}
		resolved, err := resolver.ResolveDecisionTable(ctx, tenantID, dt.AssetRef.AssetID, dt.AssetRef.Version)
		if err != nil || resolved == nil {
			// 资产缺失/未发布/版本不存在：保留引用标记但清空内容，运行期等价于空表。
			out.DecisionTables = append(out.DecisionTables, DecisionTable{
				ID:       dt.ID,
				Name:     "决策表资产不可用（" + dt.AssetRef.AssetID + "）",
				AssetRef: dt.AssetRef,
			})
			continue
		}
		merged := *resolved
		merged.ID = dt.ID // 保留表单内的行标识（React key / 前端定位）
		merged.AssetRef = dt.AssetRef
		out.DecisionTables = append(out.DecisionTables, merged)
	}
	return &out, nil
}

// NormalizeDecisionRefs 保存 schema 前把「已展开的引用表」归一回纯引用标记。
//
// 展开后的 DecisionTable 同时带 AssetRef 与内容快照；若不归一，设计器每次保存都会
// 把当前发布版本的行矩阵固化进表单，跨表单复用/版本升级即失效。
// 内嵌决策表（AssetRef == nil）原样保留，存量表单零改动。
func NormalizeDecisionRefs(schema *FormSchema) {
	if schema == nil {
		return
	}
	for i := range schema.DecisionTables {
		dt := &schema.DecisionTables[i]
		if dt.AssetRef == nil {
			continue
		}
		ref := *dt.AssetRef
		*dt = DecisionTable{ID: dt.ID, AssetRef: &ref}
	}
}
