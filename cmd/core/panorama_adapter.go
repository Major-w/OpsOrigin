package main

import (
	"context"

	"github.com/opsorigin/core/internal/cmdb/model"
	"github.com/opsorigin/core/internal/monitor"
)

// monitorSnapshotAdapter 把 monitor.Service 适配为 cmdb 的 MonitorSnapshotProvider，
// 供全景可观测拓扑按 CIID join 监控状态 + 活跃告警。
// 解耦要点：cmdb 包只依赖 model.MonitorSnapshotProvider 接口，不 import monitor；
// 具体适配在 cmd/core 完成，避免 cmdb ↔ monitor 循环依赖。
type monitorSnapshotAdapter struct {
	svc *monitor.Service
}

func (a *monitorSnapshotAdapter) Snapshot(ctx context.Context, tenantID string) (*model.MonitorSnapshot, error) {
	snap := &model.MonitorSnapshot{StatusByCI: map[string]string{}}
	for _, t := range a.svc.SnapshotTargets() {
		if tenantID != "" && t.TenantID != tenantID {
			continue
		}
		snap.StatusByCI[t.CIID] = string(t.Status)
	}
	for _, al := range a.svc.ActiveAlerts() {
		if tenantID != "" && al.TenantID != tenantID {
			continue
		}
		rootCause := al.RootCauseCI != "" && al.RootCauseCI == al.SourceCIID
		msg := al.Title
		if msg == "" {
			msg = al.Description
		}
		snap.Alerts = append(snap.Alerts, model.PanoramaAlert{
			ID:          al.ID,
			CIID:        al.SourceCIID,
			Severity:    string(al.Severity),
			RootCause:   rootCause,
			RootCauseCI: al.RootCauseCI,
			Message:     msg,
		})
	}
	return snap, nil
}
