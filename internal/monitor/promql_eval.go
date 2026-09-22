package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/opsorigin/core/internal/monitor/model"
)

// promqlRuleEvaluator 评估 Type=promql 的告警规则(阶段 4)。
// 周期查询 VictoriaMetrics 的 /api/v1/query, 表达式命中(结果非空)即生成告警事件。
// no_data 检测: 规则可写 absent(<metric>) 表达式, 指标断流时命中 → "数据断流"告警。
type promqlRuleEvaluator struct {
	vmBase string
	client *http.Client
}

func newPromqlRuleEvaluator(vmBase string) *promqlRuleEvaluator {
	return &promqlRuleEvaluator{
		vmBase: vmBase,
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

// vmInstantQuery 执行 PromQL instant query, 返回是否命中(结果非空)。
func (e *promqlRuleEvaluator) vmInstantQuery(ctx context.Context, expr string) (bool, error) {
	u := e.vmBase + "/api/v1/query?query=" + url.QueryEscape(expr)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("vm query: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return false, fmt.Errorf("vm query status %d: %s", resp.StatusCode, string(body))
	}
	var out struct {
		Status string `json:"status"`
		Data   struct {
			ResultType string `json:"resultType"`
			Result     []any  `json:"result"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return false, fmt.Errorf("vm query decode: %w", err)
	}
	return out.Status == "success" && len(out.Data.Result) > 0, nil
}

// evaluateOnce 评估一轮告警规则(threshold / promql 两类), 返回应触发的告警事件(尚未 Ingest, 由调用方落库)。
//   - promql: 直接查 VM, 表达式命中(非空)即告警(含 no_data 检测 absent(...))。
//   - threshold: 由 MetricName+Operator+Threshold 构造 PromQL 表达式查 VM, 对齐采集 baseLabels(source/instance)。
//
// 两类统一走 VM 查询, 避免阈值评估死路(原 EvaluateMetric 从未被调用)。
// 抽取为纯函数便于单测。
func (e *promqlRuleEvaluator) evaluateOnce(ctx context.Context, rules []*model.AlertRule) []*model.AlertEvent {
	if e == nil || e.vmBase == "" {
		return nil
	}
	var alerts []*model.AlertEvent
	for _, r := range rules {
		if r == nil || !r.Enabled {
			continue
		}
		var (
			hit bool
			err error
		)
		switch r.Type {
		case "promql":
			if r.Expr == "" {
				continue
			}
			hit, err = e.vmInstantQuery(ctx, r.Expr)
		case "threshold", "":
			if r.MetricName == "" || r.Operator == "" {
				continue
			}
			hit, err = e.vmInstantQuery(ctx, thresholdExpr(r))
		default:
			continue
		}
		if err != nil {
			slog.Warn("alert rule eval failed", "rule", r.Name, "type", r.Type, "err", err)
			continue
		}
		if !hit {
			continue
		}
		alerts = append(alerts, &model.AlertEvent{
			TenantID:    r.TenantID,
			SourceCIID:  r.Labels["ci_id"],
			RuleName:    r.Name,
			Severity:    r.Severity,
			Title:       r.Name,
			Description: ruleDescription(r),
			Labels:      r.Labels,
			TriggeredAt: time.Now(),
		})
	}
	return alerts
}

// thresholdExpr 由阈值规则构造 PromQL 表达式, 仅带采集 baseLabels 中存在的 series 标签(source/instance),
// 避免匹配不存在的 label 导致查询为空。
func thresholdExpr(r *model.AlertRule) string {
	matchers := ""
	for _, k := range []string{"source", "instance"} {
		if v := r.Labels[k]; v != "" {
			matchers += fmt.Sprintf(`%s=%q,`, k, v)
		}
	}
	if matchers != "" {
		matchers = "{" + matchers[:len(matchers)-1] + "}"
	}
	return fmt.Sprintf("%s%s %s %v", r.MetricName, matchers, r.Operator, r.Threshold)
}

// ruleDescription 人类可读的触发原因。
func ruleDescription(r *model.AlertRule) string {
	if r.Type == "promql" {
		return "PromQL 表达式命中: " + r.Expr
	}
	return fmt.Sprintf("指标 %s %s %.2f 命中阈值规则", r.MetricName, r.Operator, r.Threshold)
}

// promqlEvalLoop 周期评估 PromQL 规则(按各自 EvalIntervalSec, 默认 60s)。
// 命中即经 aggregator.Ingest 落库(去重由 aggregator 处理)。
func (s *Service) promqlEvalLoop(ctx context.Context) {
	eval := newPromqlRuleEvaluator(s.cfg.Core.VMAddr)
	ticker := time.NewTicker(15 * time.Second) // 全局检查周期
	defer ticker.Stop()
	slog.Info("monitor: promql rule evaluator started", "vm", s.cfg.Core.VMAddr)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			alerts := eval.evaluateOnce(ctx, s.aggregator.GetRules())
			for _, a := range alerts {
				if _, err := s.ingestAlert(ctx, a); err != nil {
					slog.Warn("promql alert ingest failed", "rule", a.RuleName, "err", err)
				}
			}
		}
	}
}
