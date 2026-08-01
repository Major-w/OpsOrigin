package cmdb

import (
	"context"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/cmdb/model"
)

// Collector 执行一次采集周期，产出原始结果；CMDB 负责按 Kind 路由落库。
//
// 采集器可插拔：具体实现（云 / 主机 Agent / 容器 / 监控）由 cmd/core 注册，
// 本包只定义契约，不直接依赖任何采集实现，避免 import 环。
type Collector interface {
	// Run 执行一次采集。ctx 携带租户；task 提供 target / kind 等上下文。
	Run(ctx context.Context, task *model.CollectionTask) (*CollectionOutput, error)
}

// CollectionOutput 是采集器一次运行的产出。
type CollectionOutput struct {
	CIs     []*model.CIModel // kind=cmdb：待 upsert 的 CI
	Metrics []MetricPoint    // kind=monitor：指标采样点
}

// MetricPoint 是一条监控指标采样点。
type MetricPoint struct {
	ResourceID string
	Name       string
	Value      float64
	Timestamp  time.Time
	Labels     map[string]string
}

// collectorKey 注册表键：kind + ":" + resourceType。
func collectorKey(kind, resourceType string) string {
	return kind + ":" + resourceType
}

// collectorRegistry 是 (kind × resourceType) → Collector 的注册表。
type collectorRegistry struct {
	mu        sync.RWMutex
	collectors map[string]Collector
}

func newCollectorRegistry() *collectorRegistry {
	return &collectorRegistry{collectors: make(map[string]Collector)}
}

// Register 注册一个采集器。重复注册覆盖。
func (r *collectorRegistry) Register(kind, resourceType string, c Collector) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.collectors[collectorKey(kind, resourceType)] = c
}

// Lookup 查找采集器；未注册返回 (nil, false)。
func (r *collectorRegistry) Lookup(kind, resourceType string) (Collector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.collectors[collectorKey(kind, resourceType)]
	return c, ok
}
