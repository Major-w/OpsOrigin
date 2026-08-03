package agentic

import (
	"context"
	"encoding/json"
	"log/slog"
)

// WorkforceMetrics 数字员工运营效果聚合指标(供 /ai/impact 运营效果页展示)。
//
// 数据口径:
//   - inspections/runs/findings/escalations 来自 AI 引擎内存态(巡检任务、运行记录、转单记录)
//   - tickets 来自 ITSM(由 cmd/core 注入的 TicketMetricsProvider 提供, 未装配时为 nil)
type WorkforceMetrics struct {
	Inspections struct {
		Total       int     `json:"total"`
		Enabled     int     `json:"enabled"`
		TotalRuns   int     `json:"total_runs"`
		SuccessRuns int     `json:"success_runs"`
		FailedRuns  int     `json:"failed_runs"`
		SuccessRate float64 `json:"success_rate"`
	} `json:"inspections"`
	Findings struct {
		High   int `json:"high"`
		Medium int `json:"medium"`
		Low    int `json:"low"`
		Total  int `json:"total"`
	} `json:"findings"`
	Escalations struct {
		Total int `json:"total"`
	} `json:"escalations"`
	Tickets *TicketMetrics `json:"tickets,omitempty"` // nil=未装配 ITSM 统计
}

// TicketMetrics ITSM 工单统计(来源过滤: source=ai_inspection)。
type TicketMetrics struct {
	Total                int            `json:"total"`
	ByStatus             map[string]int `json:"by_status"`
	AvgResolutionMinutes float64        `json:"avg_resolution_minutes"`
	// ByAssessment 处置效果评估分布(人工完成时标记): confirmed=确认有效 / false_positive=误报。
	ByAssessment map[string]int `json:"by_assessment,omitempty"`
}

// GetWorkforceMetrics 聚合数字员工运营效果指标(仅租户内数据)。
func (e *Engine) GetWorkforceMetrics(tenantID string) *WorkforceMetrics {
	m := &WorkforceMetrics{}
	m.Tickets = &TicketMetrics{ByStatus: map[string]int{}}

	e.mu.RLock()
	defer e.mu.RUnlock()

	for _, in := range e.inspections {
		if tenantID != "" && in.TenantID != tenantID {
			continue
		}
		m.Inspections.Total++
		if in.Enabled {
			m.Inspections.Enabled++
		}
		for _, r := range e.inspectionRuns[in.ID] {
			if r.TenantID != "" && tenantID != "" && r.TenantID != tenantID {
				continue
			}
			m.Inspections.TotalRuns++
			switch r.Status {
			case "success":
				m.Inspections.SuccessRuns++
			case "failed":
				m.Inspections.FailedRuns++
			}
			if r.Findings != "" {
				var fs []InspectionFinding
				if json.Unmarshal([]byte(r.Findings), &fs) == nil {
					for _, f := range fs {
						switch f.Level {
						case "high":
							m.Findings.High++
						case "medium":
							m.Findings.Medium++
						case "low":
							m.Findings.Low++
						}
						m.Findings.Total++
					}
				}
			}
			m.Escalations.Total += len(r.Escalations)
		}
	}

	if m.Inspections.TotalRuns > 0 {
		m.Inspections.SuccessRate = float64(m.Inspections.SuccessRuns) / float64(m.Inspections.TotalRuns)
	}
	return m
}

// FillTicketMetrics 通过注入的 provider 填充工单统计(未装配时置 nil)。
func (e *Engine) FillTicketMetrics(ctx context.Context, tenantID string, m *WorkforceMetrics) {
	if e.TicketMetricsProvider == nil {
		m.Tickets = nil
		return
	}
	total, byStatus, avgMin, byAssessment, err := e.TicketMetricsProvider(ctx, tenantID)
	if err != nil {
		slog.Warn("load ticket metrics failed", "tenant", tenantID, "err", err)
		m.Tickets = nil
		return
	}
	if byStatus == nil {
		byStatus = map[string]int{}
	}
	if byAssessment == nil {
		byAssessment = map[string]int{}
	}
	m.Tickets = &TicketMetrics{
		Total:                total,
		ByStatus:             byStatus,
		AvgResolutionMinutes: avgMin,
		ByAssessment:         byAssessment,
	}
}