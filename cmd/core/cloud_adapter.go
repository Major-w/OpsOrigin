package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/opsorigin/core/internal/audit/interceptor"
	"github.com/opsorigin/core/internal/cloud"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// cloudCMDBAdapter 实现 cloud.CMDBWriter，把云资源同步进 CMDB。
// 同时持有 pgRepo（仅用于去重索引读取）与 cmdbSvc（写入），
// 与 vuln 的 cmdbInventoryAdapter 同层、同风格，解耦 cloud↔cmdb。
type cloudCMDBAdapter struct {
	pgRepo  *pg.Repository
	cmdbSvc *cmdb.Service
}

// SyncResources 幂等地把一批云资源同步为 CMDB CI。
//   - 先按 CI 类型分组，对每种类型调 pgRepo.ListCI 建立 resource_id→已有CI 索引；
//   - 已存在 → cmdbSvc.UpdateCI（WeightDiscovery 渠道，受 anti-flapping 保护）；
//   - 不存在 → cmdbSvc.CreateCI（经 outbox 事件 + onCICreated 自动进入拓扑/dgraph）；
//   - 返回 {created,updated,skipped,failed,errors}。
func (a cloudCMDBAdapter) SyncResources(ctx context.Context, tenantID string, resources []cloud.Resource) cloud.SyncResult {
	result := cloud.SyncResult{}
	if a.cmdbSvc == nil {
		for range resources {
			result.Failed++
		}
		result.Errors = append(result.Errors, "cmdb service unavailable")
		return result
	}

	traceID := interceptor.TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = "cloud-sync"
	}

	// 先按 CI 类型分组，尽量减少 ListCI 调用次数。
	byType := make(map[string][]cloud.Resource)
	for _, res := range resources {
		t := cloud.CITypeOf(res)
		byType[t] = append(byType[t], res)
	}

	for typeID, ress := range byType {
		existing := make(map[string]*model.CIModel)
		if a.pgRepo != nil {
			cis, err := a.pgRepo.ListCI(ctx, tenantID, typeID)
			if err != nil {
				result.Failed += len(ress)
				result.Errors = append(result.Errors, fmt.Sprintf("list CI for %s failed: %v", typeID, err))
				continue
			}
			for _, ci := range cis {
				if rid, ok := ci.Attributes["resource_id"].(string); ok && rid != "" {
					existing[rid] = ci
				}
			}
		}

		for _, res := range ress {
			m := cloud.ToCIMapping(res, tenantID)
			ci := &model.CIModel{
				TypeID:     m.TypeID,
				TenantID:   m.TenantID,
				Name:       m.Name,
				Status:     model.CIStatusActive,
				Attributes: m.Attributes,
				Labels:     m.Labels,
				Channel:    model.WeightDiscovery,
			}
			if prev, ok := existing[m.NaturalKey]; ok {
				ci.ID = prev.ID
				if err := a.cmdbSvc.UpdateCI(ctx, ci, model.WeightDiscovery, traceID); err != nil {
					// anti-flapping：人工改过的 CI 不被自动发现覆盖 → 视为跳过。
					if errors.Is(err, pg.ErrAntiFlapping) {
						result.Skipped++
						continue
					}
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", m.NaturalKey, err))
					continue
				}
				result.Updated++
			} else {
				if err := a.cmdbSvc.CreateCI(ctx, ci, traceID); err != nil {
					result.Failed++
					result.Errors = append(result.Errors, fmt.Sprintf("%s: %v", m.NaturalKey, err))
					continue
				}
				result.Created++
			}
		}
	}
	return result
}
