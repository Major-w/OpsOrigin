package cmdb

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// CIRef 轻量 CI 引用：给外部模块（如 ITSM 表单动态选项）做"选配置项"用，
// 不暴露完整 CIModel，避免跨模块耦合到 CMDB 内部结构。
type CIRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

// CITypeNode CMDB 模型类型层级节点：作为 ITSM 级联选择器（cascader）的目录数据源。
// 模型类型自带 ParentID 继承关系（业务域 → 类别 → 具体类型），天然是一棵树；
// CI 实例之间没有父子字段，所以级联走"类型树"而非"实例树"。
type CITypeNode struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ParentID string `json:"parent_id,omitempty"`
}

const defaultLookupLimit = 200

// ListCITypeTree 返回本租户全部 CI 模型类型（扁平列表 + ParentID，由调用方按需组树）。
// 内存回退模式没有模型类型定义，退化为一层扁平根节点，仅供本地演示。
func (s *Service) ListCITypeTree(ctx context.Context, tenantID string) ([]CITypeNode, error) {
	if s.pg == nil {
		seen := map[string]struct{}{}
		for _, ci := range s.mem.listCIs(tenantID, "") {
			if ci == nil || strings.TrimSpace(ci.TypeID) == "" {
				continue
			}
			seen[ci.TypeID] = struct{}{}
		}
		names := sortedKeys(seen)
		out := make([]CITypeNode, 0, len(names))
		for _, n := range names {
			out = append(out, CITypeNode{ID: n, Name: n})
		}
		return out, nil
	}

	types, err := s.pg.ListCIModelTypes(ctx, tenantID, 0, 0)
	if err != nil {
		return nil, err
	}
	out := make([]CITypeNode, 0, len(types))
	for _, t := range types {
		if t == nil || strings.TrimSpace(t.Name) == "" {
			continue
		}
		out = append(out, CITypeNode{
			ID:       t.ID,
			Name:     t.Name,
			ParentID: strings.TrimSpace(t.ParentID),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListCITypeNames 返回本租户可用的 CI 模型类型名（用于表单配置时挑选 source）。
func (s *Service) ListCITypeNames(ctx context.Context, tenantID string) ([]string, error) {
	if s.pg == nil {
		seen := map[string]struct{}{}
		for _, ci := range s.mem.listCIs(tenantID, "") {
			seen[ci.TypeID] = struct{}{}
		}
		return sortedKeys(seen), nil
	}
	types, err := s.pg.ListCIModelTypes(ctx, tenantID, 0, 0)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(types))
	for _, t := range types {
		names = append(names, t.Name)
	}
	sort.Strings(names)
	return names, nil
}

// ListCIsByTypeName 按模型类型名列出本租户的 CI（已排除归档/删除）。
// typeName 为空时返回全部类型；keyword 非空时按名称做不区分大小写的模糊过滤；
// limit <= 0 时使用默认上限，避免选项列表打爆前端。
func (s *Service) ListCIsByTypeName(ctx context.Context, tenantID, typeName, keyword string, limit int) ([]CIRef, error) {
	if limit <= 0 {
		limit = defaultLookupLimit
	}

	// 内存回退：CI 的 TypeID 本身就是语义类型名
	if s.pg == nil {
		typeNameByID := map[string]string{}
		for _, ci := range s.mem.listCIs(tenantID, "") {
			typeNameByID[ci.TypeID] = ci.TypeID
		}
		return toCIRefs(s.mem.listCIs(tenantID, typeName), typeNameByID, keyword, limit), nil
	}

	types, err := s.pg.ListCIModelTypes(ctx, tenantID, 0, 0)
	if err != nil {
		return nil, err
	}
	typeID := ""
	typeNameByID := make(map[string]string, len(types))
	for _, t := range types {
		typeNameByID[t.ID] = t.Name
		if typeName != "" && strings.EqualFold(t.Name, typeName) {
			typeID = t.ID
		}
	}
	if typeName != "" && typeID == "" {
		return nil, fmt.Errorf("CI 模型类型 %q 不存在", typeName)
	}

	cis, err := s.pg.ListCIFiltered(ctx, tenantID, typeID, "", "name", "asc", limit, 0)
	if err != nil {
		return nil, err
	}
	return toCIRefs(cis, typeNameByID, keyword, limit), nil
}

// ListCIsByAttrs 按模型类型名 + 属性等值过滤列出 CI（已排除归档/删除）。
// attrs 为空等价于 ListCIsByTypeName(无 keyword)；任一属性不匹配即排除。
// 实现取该类型前 attrScanLimit 条(按名称排序)在内存逐条比对 attributes ——
// 表单级联(机房→机柜)候选量级远低于此, 不值得为它加 SQL attributes 包含索引。
const attrScanLimit = 1000

func (s *Service) ListCIsByAttrs(ctx context.Context, tenantID, typeName string, attrs map[string]string, limit int) ([]CIRef, error) {
	if len(attrs) == 0 {
		return s.ListCIsByTypeName(ctx, tenantID, typeName, "", limit)
	}
	if limit <= 0 {
		limit = defaultLookupLimit
	}
	cis, err := s.ListCIsByTypeName(ctx, tenantID, typeName, "", attrScanLimit)
	if err != nil {
		return nil, err
	}
	// 需要逐属性比对, 仅靠 CIRef(ID/Name/Type) 不够, 回查完整模型。
	var full []*model.CIModel
	if s.pg == nil {
		full = s.mem.listCIs(tenantID, typeName)
	} else {
		typeID := ""
		types, terr := s.pg.ListCIModelTypes(ctx, tenantID, 0, 0)
		if terr != nil {
			return nil, terr
		}
		for _, t := range types {
			if strings.EqualFold(t.Name, typeName) {
				typeID = t.ID
				break
			}
		}
		if typeID == "" {
			return nil, fmt.Errorf("CI 模型类型 %q 不存在", typeName)
		}
		if full, err = s.pg.ListCIFiltered(ctx, tenantID, typeID, "", "name", "asc", attrScanLimit, 0); err != nil {
			return nil, err
		}
	}
	byID := make(map[string]*model.CIModel, len(full))
	for _, c := range full {
		if c != nil {
			byID[c.ID] = c
		}
	}
	out := make([]CIRef, 0, len(cis))
	for _, ref := range cis {
		ci := byID[ref.ID]
		if ci == nil {
			continue
		}
		match := true
		for k, want := range attrs {
			if fmt.Sprintf("%v", ci.Attributes[k]) != want {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		out = append(out, ref)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// GetCIAttrByName 按模型类型名 + CI 名称取单个属性值（表单数据联动 ci_attr 回填）。
// found=false 表示 CI 不存在 / 属性为空；口径与 ListCIsByAttrs 一致（排除归档/删除、
// 名称精确等值、attrScanLimit 扫描上限——表单回填对象量级远低于此）。
func (s *Service) GetCIAttrByName(ctx context.Context, tenantID, typeName, name, attr string) (string, bool, error) {
	name, attr = strings.TrimSpace(name), strings.TrimSpace(attr)
	if typeName == "" || name == "" || attr == "" {
		return "", false, fmt.Errorf("typeName/name/attr 均不能为空")
	}
	var full []*model.CIModel
	if s.pg == nil {
		full = s.mem.listCIs(tenantID, typeName)
	} else {
		typeID := ""
		types, terr := s.pg.ListCIModelTypes(ctx, tenantID, 0, 0)
		if terr != nil {
			return "", false, terr
		}
		for _, t := range types {
			if strings.EqualFold(t.Name, typeName) {
				typeID = t.ID
				break
			}
		}
		if typeID == "" {
			return "", false, fmt.Errorf("CI 模型类型 %q 不存在", typeName)
		}
		if full, terr = s.pg.ListCIFiltered(ctx, tenantID, typeID, "", "name", "asc", attrScanLimit, 0); terr != nil {
			return "", false, terr
		}
	}
	for _, ci := range full {
		if ci == nil || ci.Status == model.CIStatusArchived || ci.Status == model.CIStatusDeleted {
			continue
		}
		if ci.Name != name {
			continue
		}
		if v, ok := ci.Attributes[attr]; ok && v != nil && fmt.Sprintf("%v", v) != "" {
			return fmt.Sprintf("%v", v), true, nil
		}
		return "", false, nil // CI 存在但属性为空
	}
	return "", false, nil
}

func toCIRefs(cis []*model.CIModel, typeNameByID map[string]string, keyword string, limit int) []CIRef {
	kw := strings.ToLower(strings.TrimSpace(keyword))
	out := make([]CIRef, 0, len(cis))
	for _, ci := range cis {
		if ci == nil || ci.Status == model.CIStatusArchived || ci.Status == model.CIStatusDeleted {
			continue
		}
		if kw != "" && !strings.Contains(strings.ToLower(ci.Name), kw) {
			continue
		}
		out = append(out, CIRef{ID: ci.ID, Name: ci.Name, Type: typeNameByID[ci.TypeID]})
		if len(out) >= limit {
			break
		}
	}
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
