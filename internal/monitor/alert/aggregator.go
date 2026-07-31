package alert

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/opsorigin/core/internal/monitor/model"
)

type TopologyChecker interface {
	GetParentCI(ctx context.Context, ciID string) (string, error)
	IsAncestorOf(ctx context.Context, ancestorID, descendantID string) (bool, error)
}

type AlertNotifyFunc func(alert *model.AlertEvent)

type Aggregator struct {
	mu              sync.RWMutex
	activeAlerts    map[string]*model.AlertEvent
	rules           []*model.AlertRule
	suppressedCount int
	window          time.Duration
	topology        TopologyChecker
	onNotify        AlertNotifyFunc
	alertHistory    []*model.AlertEvent
}

func NewAggregator(topology TopologyChecker) *Aggregator {
	return &Aggregator{
		activeAlerts: make(map[string]*model.AlertEvent),
		rules:        make([]*model.AlertRule, 0),
		alertHistory: make([]*model.AlertEvent, 0),
		window:       5 * time.Second,
		topology:     topology,
	}
}

func (a *Aggregator) OnNotify(fn AlertNotifyFunc) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.onNotify = fn
}

func (a *Aggregator) AddRule(rule *model.AlertRule) {
	a.mu.Lock()
	defer a.mu.Unlock()
	rule.ID = uuid.New().String()
	a.rules = append(a.rules, rule)
}

func (a *Aggregator) DeleteRule(ruleID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, r := range a.rules {
		if r.ID == ruleID {
			a.rules = append(a.rules[:i], a.rules[i+1:]...)
			return
		}
	}
}

func (a *Aggregator) GetRules() []*model.AlertRule {
	a.mu.RLock()
	defer a.mu.RUnlock()
	result := make([]*model.AlertRule, len(a.rules))
	copy(result, a.rules)
	return result
}

func (a *Aggregator) EvaluateMetric(sample MetricSample) {
	a.mu.RLock()
	rules := make([]*model.AlertRule, len(a.rules))
	copy(rules, a.rules)
	a.mu.RUnlock()

	for _, rule := range rules {
		if !rule.Enabled || rule.MetricName != sample.Name {
			continue
		}

		triggered := false
		switch rule.Operator {
		case ">":
			triggered = sample.Value > rule.Threshold
		case "<":
			triggered = sample.Value < rule.Threshold
		case ">=":
			triggered = sample.Value >= rule.Threshold
		case "<=":
			triggered = sample.Value <= rule.Threshold
		case "==":
			triggered = sample.Value == rule.Threshold
		}

		if triggered {
			alert := &model.AlertEvent{
				TenantID:    "default",
				SourceCIID:  sample.Host,
				RuleName:    rule.Name,
				Severity:    rule.Severity,
				Title:       fmt.Sprintf("%s: %.1f (threshold %s %.1f)", rule.Name, sample.Value, rule.Operator, rule.Threshold),
				Description: fmt.Sprintf("Metric %s value %.2f%s exceeded threshold %s %.1f", rule.MetricName, sample.Value, sample.Unit, rule.Operator, rule.Threshold),
			}
			a.Ingest(context.Background(), alert)
		}
	}
}

type MetricSample struct {
	Name      string
	Value     float64
	Host      string
	Unit      string
	Timestamp time.Time
}

func (a *Aggregator) Run(ctx context.Context) error {
	slog.Info("alert aggregator started", "window", a.window)
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			slog.Info("alert aggregator stopped")
			return nil
		case <-ticker.C:
			a.cleanupExpired(ctx)
		}
	}
}

func (a *Aggregator) Ingest(ctx context.Context, alert *model.AlertEvent) (*model.AlertEvent, error) {
	alert.ID = uuid.New().String()
	alert.Fingerprint = a.fingerprint(alert)
	alert.TriggeredAt = time.Now()

	if a.topology != nil && alert.SourceCIID != "" {
		suppressed, rootCause, err := a.evaluateSuppression(ctx, alert)
		if err != nil {
			slog.Warn("suppression evaluation failed", "error", err)
		}
		alert.IsSuppressed = suppressed
		alert.RootCauseCI = rootCause
	}

	a.mu.Lock()
	a.activeAlerts[alert.Fingerprint] = alert
	a.alertHistory = append(a.alertHistory, alert)
	if len(a.alertHistory) > 1000 {
		a.alertHistory = a.alertHistory[len(a.alertHistory)-500:]
	}
	if alert.IsSuppressed {
		a.suppressedCount++
	}
	notifyFn := a.onNotify
	a.mu.Unlock()

	if !alert.IsSuppressed && notifyFn != nil {
		notifyFn(alert)
	}

	if alert.IsSuppressed {
		slog.Debug("alert suppressed", "title", alert.Title, "root_cause", alert.RootCauseCI)
		return alert, nil
	}

	slog.Warn("alert triggered", "title", alert.Title, "severity", alert.Severity)
	return alert, nil
}

func (a *Aggregator) Acknowledge(alertID, acknowledgedBy string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, alert := range a.activeAlerts {
		if alert.ID == alertID {
			delete(a.activeAlerts, alert.Fingerprint)
			alert.ResolvedAt = timePtr(time.Now())
			a.recordHistoryLocked(alert)
			return nil
		}
	}
	return fmt.Errorf("alert %s not found", alertID)
}

func (a *Aggregator) Resolve(alertID string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	for fp, alert := range a.activeAlerts {
		if alert.ID == alertID {
			alert.ResolvedAt = timePtr(time.Now())
			delete(a.activeAlerts, fp)
			a.recordHistoryLocked(alert)
			return nil
		}
	}
	return fmt.Errorf("alert %s not found", alertID)
}

// maxAlertHistory 保留的处理历史告警条数(环形上限)。
const maxAlertHistory = 200

// recordHistoryLocked 把已确认/已解决的告警追加到处理历史(调用方须持有 a.mu)。
func (a *Aggregator) recordHistoryLocked(alert *model.AlertEvent) {
	a.alertHistory = append(a.alertHistory, alert)
	if len(a.alertHistory) > maxAlertHistory {
		a.alertHistory = a.alertHistory[len(a.alertHistory)-maxAlertHistory:]
	}
}

// GetAlertHistory 返回最近的处理历史(已确认/已解决告警, 时间倒序)。
func (a *Aggregator) GetAlertHistory() []*model.AlertEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	out := make([]*model.AlertEvent, 0, len(a.alertHistory))
	for i := len(a.alertHistory) - 1; i >= 0; i-- {
		out = append(out, a.alertHistory[i])
	}
	return out
}

func timePtr(t time.Time) *time.Time {
	return &t
}

func (a *Aggregator) evaluateSuppression(ctx context.Context, alert *model.AlertEvent) (bool, string, error) {
	ciID := alert.SourceCIID
	for _, existing := range a.activeAlerts {
		if existing.IsSuppressed || existing.SourceCIID == ciID {
			continue
		}
		isAncestor, err := a.topology.IsAncestorOf(ctx, existing.SourceCIID, ciID)
		if err != nil {
			continue
		}
		if isAncestor {
			return true, existing.SourceCIID, nil
		}
	}

	parent, err := a.topology.GetParentCI(ctx, ciID)
	if err != nil || parent == "" {
		return false, "", nil
	}

	for _, existing := range a.activeAlerts {
		if existing.IsSuppressed {
			continue
		}
		if existing.SourceCIID == parent {
			return true, parent, nil
		}
	}
	return false, "", nil
}

func (a *Aggregator) cleanupExpired(ctx context.Context) {
	a.mu.Lock()
	defer a.mu.Unlock()
	now := time.Now()
	cutoff := now.Add(-10 * time.Minute)
	for fp, alert := range a.activeAlerts {
		if alert.ResolvedAt != nil && alert.ResolvedAt.Before(cutoff) {
			delete(a.activeAlerts, fp)
		}
	}
}

func (a *Aggregator) GetActiveAlerts() []*model.AlertEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	alerts := make([]*model.AlertEvent, 0, len(a.activeAlerts))
	for _, alert := range a.activeAlerts {
		alerts = append(alerts, alert)
	}
	return alerts
}

func (a *Aggregator) GetAlert(alertID string) *model.AlertEvent {
	a.mu.RLock()
	defer a.mu.RUnlock()
	for _, alert := range a.activeAlerts {
		if alert.ID == alertID {
			return alert
		}
	}
	return nil
}

func (a *Aggregator) GetStats() map[string]any {
	a.mu.RLock()
	defer a.mu.RUnlock()
	critical, warning, info := 0, 0, 0
	for _, alert := range a.activeAlerts {
		switch alert.Severity {
		case model.AlertCritical:
			critical++
		case model.AlertWarning:
			warning++
		default:
			info++
		}
	}
	return map[string]any{
		"active_alerts":     len(a.activeAlerts),
		"suppressed_alerts": a.suppressedCount,
		"critical":          critical,
		"warning":           warning,
		"info":              info,
	}
}

func (a *Aggregator) fingerprint(alert *model.AlertEvent) string {
	h := sha256.New()
	h.Write([]byte(alert.TenantID))
	h.Write([]byte(alert.SourceCIID))
	h.Write([]byte(alert.RuleName))
	h.Write([]byte(string(alert.Severity)))
	return fmt.Sprintf("%x", h.Sum(nil))
}