package main

import (
	"context"
	"strings"

	"github.com/opsorigin/core/internal/cloud"
	"github.com/opsorigin/core/internal/cmdb"
	"github.com/opsorigin/core/internal/cmdb/model"
)

// cloudMetricCollector 实现 cmdb.Collector：对云资源执行 server 拉模式的监控采集。
//
// 复用 cloud.Collector.Collect(provider) 已经产出的指标（[]cloud.Metric），
// 转换为 cmdb.MetricPoint，交由调度器按 kind=monitor 路由到指标落库（ingest 管道）。
//
// 现状：各云厂商的 CollectMetrics 目前为占位实现（返回 nil），接入云监控 API
// （阿里云 CMS / 腾讯云 Barad / AWS CloudWatch / 华为云 CES）后即产出真实基础指标。
// 在此之前，本采集器保持"骨架通"：任务标记为 success，不产生假数据。
type cloudMetricCollector struct {
	col *cloud.Collector
}

// Run 执行一次云监控采集。target 形如 "cloud:<provider>"。
func (c *cloudMetricCollector) Run(ctx context.Context, task *model.CollectionTask) (*cmdb.CollectionOutput, error) {
	provider := strings.TrimPrefix(task.Target, "cloud:")
	_, metrics, err := c.col.Collect(provider)
	if err != nil {
		return nil, err
	}
	pts := make([]cmdb.MetricPoint, 0, len(metrics))
	for _, m := range metrics {
		pts = append(pts, cmdb.MetricPoint{
			ResourceID: m.ResourceID,
			Name:       m.Name,
			Value:      m.Value,
			Timestamp:  m.Timestamp,
			Labels: map[string]string{
				"provider":      m.Provider,
				"resource_type": string(m.Type),
			},
		})
	}
	return &cmdb.CollectionOutput{Metrics: pts}, nil
}
