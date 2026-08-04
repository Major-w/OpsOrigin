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

const defaultLookupLimit = 200

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
