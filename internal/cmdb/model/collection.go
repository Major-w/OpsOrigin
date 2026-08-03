package model

import "time"

// CollectionTask 是统一采集调度框架中的一条采集任务。
//
// 设计要点（见竞品分析文档「六、采集架构设计」）：
//   - 资源纳管层（internal/cloud 等）只负责建立通道（装 Agent / 验证连通性 / 装容器 Agent），
//     不拉取数据、不直接写 CMDB；
//   - CMDB 是唯一调度者：持有采集任务，按 Frequency 触发，结果按 Kind 路由落库；
//   - 采集器可插拔（注册表模式），CMDB 不直接依赖具体采集实现。
type CollectionTask struct {
	ID           string     `json:"id"`
	TenantID     string     `json:"tenant_id"`
	Name         string     `json:"name"`
	ResourceType string     `json:"resource_type"` // cloud | host | container
	Target       string     `json:"target"`        // 指向某纳管资源，如 "cloud:aliyun" / "host:<id>"
	Kind         string     `json:"kind"`          // cmdb | monitor
	Channel      string     `json:"channel"`       // 采集通道：server(CMDB 服务端拉取) | agent(经 Agent 推送上报)
	Frequency    string     `json:"frequency"`     // 采集频率：Go duration 字符串，如 "15m" / "1h"
	// 动态执行目标（P1-4：主机查询策略驱动的动态目标范围）：
	//   target_mode="static"：沿用 Target 静态资源 ID（默认，向后兼容）
	//   target_mode="query" ：Target 留空，改由 TargetQuery 引用的保存查询（"我想查"）实时解析命中的 CI 集合，
	//                         调度/手动触发时对集合中每个资源分别下发采集（命中主机自动纳入采集范围）。
	TargetMode  string `json:"target_mode"`
	TargetQuery string `json:"target_query,omitempty"` // target_mode=query 时引用的保存查询 ID
	Enabled      bool   `json:"enabled"`
	LastRunAt    *time.Time `json:"last_run_at,omitempty"`
	Status       string     `json:"status"` // idle | running | success | failed | pending
	Error        string     `json:"error,omitempty"`
	CreatedAt    time.Time  `json:"created_at"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

// IsDue 判断任务是否到点需要触发。interval 为解析后的频率。
func (t *CollectionTask) IsDue(now time.Time, interval time.Duration) bool {
	if t.LastRunAt == nil {
		return true
	}
	return now.Sub(*t.LastRunAt) >= interval
}

// PlatformPolicy 是平台级「全局默认策略」（兜底），控制新纳管资源的默认采集行为。
// 单例行（按 tenant_id 主键）；纳管界面的勾选框预填此处值，可逐资源覆盖。
type PlatformPolicy struct {
	TenantID                string    `json:"tenant_id"`
	DefaultCMDBEnabled      bool      `json:"default_cmdb_enabled"`      // 新资源默认开启基础 CMDB 采集（CMDB 是平台基石）
	DefaultMonitorEnabled   bool      `json:"default_monitor_enabled"`   // 新资源默认关闭监控采集（兼容/资源风险）
	CMDBDefaultFrequency    string    `json:"cmdb_default_frequency"`    // cmdb 类采集任务默认频率
	MonitorDefaultFrequency string    `json:"monitor_default_frequency"` // monitor 类采集任务默认频率
	UpdatedAt               time.Time `json:"updated_at"`
}
