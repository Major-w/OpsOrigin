package main

import (
	"context"
	"strings"

	"github.com/opsorigin/core/internal/cloud"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// cloudCollector 实现 cmdb.Collector：把云资源采集接入 CMDB 统一调度框架。
// 复用 internal/cloud 的 Collector.Collect 拉取真实云资源，经 ToCIMapping 映射为 CI。
// CMDB 包不直接依赖 cloud 实现，本桥接在 cmd/core 组装，保持依赖单向（core → cloud + cmdb）。
type cloudCollector struct {
	col *cloud.Collector
}

// Run 执行一次云资源采集，产出待 upsert 的 CI 列表。
func (c *cloudCollector) Run(ctx context.Context, task *model.CollectionTask) (*cmdb.CollectionOutput, error) {
	provider := providerFromTarget(task.Target) // "cloud:aliyun" -> "aliyun"
	resources, _, err := c.col.Collect(provider)
	if err != nil {
		return nil, err
	}
	out := &cmdb.CollectionOutput{}
	for _, res := range resources {
		m := cloud.ToCIMapping(res, task.TenantID)
		ci := &model.CIModel{
			TypeID:     m.TypeID,
			TenantID:   m.TenantID,
			Name:       m.Name,
			Status:     model.CIStatusActive,
			Attributes: m.Attributes,
			Labels:     m.Labels,
			Channel:    model.WeightDiscovery,
		}
		out.CIs = append(out.CIs, ci)
	}
	return out, nil
}

// providerFromTarget 从 target（"cloud:<provider>"）取出 provider 名。
func providerFromTarget(target string) string {
	if i := strings.Index(target, ":"); i >= 0 {
		return target[i+1:]
	}
	return target
}
