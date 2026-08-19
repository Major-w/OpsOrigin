package main

import (
	"context"
	"errors"
	"time"

	"github.com/opsorigin/core/internal/agentmgr"
	"github.com/opsorigin/core/internal/audit/interceptor"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/cmdb/pg"
)

// agentCMDBAdapter 实现 agentmgr.HostCIWriter：把 Agent 纳管的主机资产同步为 CMDB 的 host CI。
// 与 cloudCMDBAdapter 同层、同风格——agentmgr 不直接依赖 cmdb，本适配器在 cmd/core 组装，保持依赖单向。
//
// 这是方案 X 闭环的最后一跳：此前主机/容器资产只落在 agent_hosts，从没进 CMDB
// （云路径有 cloudCMDBAdapter，agent 路径缺这个等价物）。补齐后「纳管→采集→CMDB 落库」对主机/容器同样闭环。
type agentCMDBAdapter struct {
	pgRepo  *pg.Repository
	cmdbSvc *cmdb.Service
}

// SyncHost 幂等地把一条 AgentHost 同步为 CMDB host CI。
//   - 按 resource_id("host:<agentID>") 去重：已存在 → UpdateCI(受 anti-flapping 保护)；不存在 → CreateCI。
//   - 每个属性显式写入 AttributeMeta(source=agent, confidence=0.9)，符合 model 注释
//     “采集路径应在调用前注入具体 source”（否则 ChannelWeight 兜底会误标为 discovery）。
func (a agentCMDBAdapter) SyncHost(ctx context.Context, tenantID string, host *agentmgr.AgentHost) (created, updated int, err error) {
	if a.cmdbSvc == nil {
		return 0, 0, errors.New("cmdb service unavailable")
	}
	traceID := interceptor.TraceIDFromContext(ctx)
	if traceID == "" {
		traceID = "agent-sync"
	}
	ci := hostToCI(host, tenantID)

	if a.pgRepo != nil {
		if existing, ok := findHostCI(ctx, a.pgRepo, tenantID, ci.Attributes["resource_id"].(string)); ok {
			ci.ID = existing.ID
			if e := a.cmdbSvc.UpdateCI(ctx, ci, model.WeightDiscovery, traceID); e != nil {
				if errors.Is(e, pg.ErrAntiFlapping) {
					// 人工改过的字段受保护，视为跳过（不算失败）。
					return 0, 0, nil
				}
				return 0, 0, e
			}
			return 0, 1, nil
		}
	}
	if e := a.cmdbSvc.CreateCI(ctx, ci, traceID); e != nil {
		return 0, 0, e
	}
	return 1, 0, nil
}

// hostToCI 把 AgentHost 资产映射为 CMDB host 类型 CI（含显式 source=agent 元数据）。
func hostToCI(h *agentmgr.AgentHost, tenantID string) *model.CIModel {
	now := time.Now()
	attrs := map[string]any{
		"resource_id": "host:" + h.ID,
		"hostname":    h.Hostname,
		"os":          h.OSType,
		"os_version":  h.OSVersion,
		"cpu":         h.CPU,
		"memory":      h.Memory,
		"disk":        h.Disk,
		"ip":          h.Address,
		"agent_id":    h.ID,
	}
	if len(h.Labels) > 0 {
		attrs["labels"] = h.Labels
	}
	// 显式注入字段级来源元数据：agent 采集，置信度 0.9（见 model.DefaultConfidence）。
	// 否则依赖 ChannelWeight 兜底会把来源误标为 discovery，与「Agent 采集」语义不符。
	meta := make(map[string]model.AttrMeta, len(attrs))
	for k := range attrs {
		meta[k] = model.AttrMeta{
			Source:     model.SourceAgent,
			Confidence: model.DefaultConfidence(model.SourceAgent),
			LastSeenAt: now,
		}
	}
	name := h.Hostname
	if name == "" {
		name = firstNonEmpty(h.Address, h.ID)
	}
	return &model.CIModel{
		TypeID:        "host",
		TenantID:      tenantID,
		Name:          name,
		Status:        model.CIStatusActive,
		Attributes:    attrs,
		AttributeMeta: meta,
		Channel:       model.WeightDiscovery,
	}
}

// findHostCI 按 resource_id 在 host 类型 CI 中查找已存在的 CI（用于幂等 upsert）。
func findHostCI(ctx context.Context, repo *pg.Repository, tenantID, resourceID string) (*model.CIModel, bool) {
	cur, err := repo.ListCI(ctx, tenantID, "host")
	if err != nil {
		return nil, false
	}
	for _, c := range cur {
		if c.Attributes == nil {
			continue
		}
		if rid, ok := c.Attributes["resource_id"].(string); ok && rid == resourceID {
			return c, true
		}
	}
	return nil, false
}

// firstNonEmpty 返回首个非空字符串（用于 host CI 名称兜底）。
func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
