package monitor

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/opsorigin/core/internal/monitor/model"
	"github.com/opsorigin/core/pkg/auth"
	"github.com/opsorigin/core/pkg/httputil"
)

// 第三方告警统一接入：接收 Zabbix / Prometheus Alertmanager / 通用 webhook，
// 归一化为内部 AlertEvent 汇入告警中心(去重/聚类/抑制/通知 + CMDB 富化)。

type externalIngestPayload struct {
	Source      string            `json:"source"` // "alertmanager" | "zabbix" | "" 自动识别
	Alerts      []amAlert         `json:"alerts"`
	Labels      map[string]string `json:"labels"`
	Annotations map[string]string `json:"annotations"`
	// Zabbix / 通用平铺字段
	EventID     string `json:"eventid"`
	Trigger     string `json:"trigger"`
	Host        string `json:"host"`
	Severity    string `json:"severity"`
	Status      string `json:"status"` // firing/resolved/problem/ok
	Title       string `json:"title"`
	Message     string `json:"message"`
	Description string `json:"description"`
}

type amAlert struct {
	Status       string            `json:"status"` // firing | resolved
	Labels       map[string]string `json:"labels"`
	Annotations  map[string]string `json:"annotations"`
	StartsAt     string            `json:"startsAt"`
	EndsAt       string            `json:"endsAt"`
	GeneratorURL string            `json:"generatorURL"`
}

// extAlert 归一化后的中间结构。
type extAlert struct {
	status      string
	labels      map[string]string
	title       string
	description string
	severity    string
	ruleName    string
	sourceCIID  string
}

func (s *Service) handleIngestExternalAlert(w http.ResponseWriter, r *http.Request) {
	var payload externalIngestPayload
	if err := httputil.BindJSON(r, &payload); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if tenantID == "" {
		tenantID = "default"
	}

	items := normalizeExternal(payload)
	ingested, resolved := 0, 0
	for _, it := range items {
		sev := normalizeSeverity(it.severity)
		ruleName := it.ruleName
		if ruleName == "" {
			ruleName = it.title
		}
		// resolved / ok: 按指纹解析已有活跃告警, 不新增。
		if strings.EqualFold(it.status, "resolved") || strings.EqualFold(it.status, "ok") {
			if err := s.aggregator.ResolveExternal(tenantID, it.sourceCIID, ruleName, sev); err == nil {
				resolved++
			}
			continue
		}
		labels := it.labels
		if labels == nil {
			labels = map[string]string{}
		}
		if it.sourceCIID != "" {
			labels["ci_id"] = it.sourceCIID
		}
		alert := &model.AlertEvent{
			TenantID:    tenantID,
			SourceCIID:  it.sourceCIID,
			RuleName:    ruleName,
			Severity:    sev,
			Title:       it.title,
			Description: it.description,
			Labels:      labels,
		}
		if _, err := s.ingestAlert(r.Context(), alert); err != nil {
			slog.Warn("monitor: ingest external alert failed", "title", it.title, "err", err)
			continue
		}
		ingested++
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":   "accepted",
		"ingested": ingested,
		"resolved": resolved,
	})
}

// normalizeExternal 把多种第三方格式归一为 extAlert 列表。
func normalizeExternal(p externalIngestPayload) []extAlert {
	out := make([]extAlert, 0)
	// Prometheus Alertmanager
	if len(p.Alerts) > 0 {
		for _, a := range p.Alerts {
			labels := cloneMap(a.Labels)
			out = append(out, extAlert{
				status:      a.Status,
				labels:      labels,
				title:       firstNonEmpty(a.Annotations["summary"], a.Labels["alertname"]),
				description: a.Annotations["description"],
				severity:    a.Labels["severity"],
				ruleName:    a.Labels["alertname"],
				sourceCIID:  firstNonEmpty(a.Labels["instance"], a.Labels["host"], a.Labels["ci_id"]),
			})
		}
		return out
	}
	// Zabbix 平铺
	if p.Host != "" || p.Trigger != "" {
		labels := cloneMap(p.Labels)
		if p.Host != "" {
			labels["host"] = p.Host
		}
		if p.Trigger != "" {
			labels["trigger"] = p.Trigger
		}
		if p.EventID != "" {
			labels["eventid"] = p.EventID
		}
		out = append(out, extAlert{
			status:      p.Status,
			labels:      labels,
			title:       p.Trigger,
			description: p.Message,
			severity:    p.Severity,
			ruleName:    p.Trigger,
			sourceCIID:  p.Host,
		})
		return out
	}
	// 通用
	labels := cloneMap(p.Labels)
	out = append(out, extAlert{
		status:      p.Status,
		labels:      labels,
		title:       firstNonEmpty(p.Title, p.Labels["alertname"]),
		description: firstNonEmpty(p.Description, p.Annotations["description"]),
		severity:    p.Labels["severity"],
		ruleName:    p.Labels["alertname"],
		sourceCIID:  firstNonEmpty(p.Labels["instance"], p.Labels["host"], p.Labels["ci_id"]),
	})
	return out
}

func normalizeSeverity(s string) model.AlertSeverity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical", "disaster", "high", "page", "sev1", "p1":
		return model.AlertCritical
	case "warning", "average", "error", "major", "sev2", "p2", "middle":
		return model.AlertWarning
	case "info", "information", "notice", "low", "sev3", "p3", "ok":
		return model.AlertInfo
	default:
		return model.AlertWarning
	}
}

func cloneMap(m map[string]string) map[string]string {
	if m == nil {
		return map[string]string{}
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
