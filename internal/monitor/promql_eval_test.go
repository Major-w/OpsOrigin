package monitor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/internal/monitor/model"
)

// mockVM 构造 mock VictoriaMetrics: 按 query 参数返回命中/空。
func mockVM(t *testing.T, hitExprs map[string]bool) string {
	t.Helper()
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/query" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		expr := r.URL.Query().Get("query")
		w.Header().Set("Content-Type", "application/json")
		if hitExprs[expr] {
			io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"agent_id":"a1"},"value":[1722500000,"0.9"]}]}}`)
		} else {
			io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[]}}`)
		}
	}))
	t.Cleanup(vm.Close)
	return vm.URL
}

func TestVmInstantQueryHit(t *testing.T) {
	vm := mockVM(t, map[string]bool{"avg(cpu.usage) > 0.8": true})
	e := newPromqlRuleEvaluator(vm)
	hit, err := e.vmInstantQuery(context.Background(), "avg(cpu.usage) > 0.8")
	if err != nil {
		t.Fatal(err)
	}
	if !hit {
		t.Fatal("expected hit")
	}
}

func TestVmInstantQueryNoHit(t *testing.T) {
	vm := mockVM(t, map[string]bool{})
	e := newPromqlRuleEvaluator(vm)
	hit, err := e.vmInstantQuery(context.Background(), "avg(cpu.usage) > 99")
	if err != nil {
		t.Fatal(err)
	}
	if hit {
		t.Fatal("expected no hit")
	}
}

func TestEvaluateOncePromQL(t *testing.T) {
	vm := mockVM(t, map[string]bool{
		"avg(cpu.usage) > 0.8": true,
		"absent(cpu.usage)":    true, // no_data: 指标断流时命中
	})
	e := newPromqlRuleEvaluator(vm)

	rules := []*model.AlertRule{
		{Name: "cpu-high", Type: "promql", Expr: "avg(cpu.usage) > 0.8", Severity: model.AlertWarning, Enabled: true},
		{Name: "no-data", Type: "promql", Expr: "absent(cpu.usage)", Severity: model.AlertCritical, Enabled: true},
		{Name: "not-hit", Type: "promql", Expr: "avg(mem.percent) > 99", Severity: model.AlertWarning, Enabled: true},
		{Name: "threshold-old", MetricName: "cpu", Operator: ">", Threshold: 90, Enabled: true}, // 阈值规则走 VM 查询评估, 此表达式不在 mock 命中集 → 不触发
		{Name: "disabled", Type: "promql", Expr: "absent(mem.used)", Enabled: false},
	}

	alerts := e.evaluateOnce(context.Background(), rules)
	if len(alerts) != 2 {
		t.Fatalf("expected 2 alerts (cpu-high + no-data), got %d: %+v", len(alerts), alerts)
	}
	names := map[string]bool{}
	for _, a := range alerts {
		names[a.RuleName] = true
	}
	if !names["cpu-high"] || !names["no-data"] {
		t.Fatalf("unexpected alert set: %v", names)
	}
	// no_data 告警应为 critical
	for _, a := range alerts {
		if a.RuleName == "no-data" && a.Severity != model.AlertCritical {
			t.Fatalf("no-data severity wrong: %v", a.Severity)
		}
	}
}

func TestEvaluateOnceNilVM(t *testing.T) {
	// VM 未配置时 evaluateOnce 返回 nil(不 panic)
	alerts := (*promqlRuleEvaluator)(nil).evaluateOnce(context.Background(), []*model.AlertRule{
		{Name: "x", Type: "promql", Expr: "absent(x)", Enabled: true},
	})
	if len(alerts) != 0 {
		t.Fatalf("expected no alerts without vm, got %d", len(alerts))
	}
}

func TestEvaluateOnceVMError(t *testing.T) {
	// VM 返回 500 → 规则跳过(不 panic, 不误报)
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		io.WriteString(w, "boom")
	}))
	defer vm.Close()
	e := newPromqlRuleEvaluator(vm.URL)
	alerts := e.evaluateOnce(context.Background(), []*model.AlertRule{
		{Name: "x", Type: "promql", Expr: "absent(x)", Enabled: true},
	})
	if len(alerts) != 0 {
		t.Fatalf("expected 0 alerts on vm error, got %d", len(alerts))
	}
}

func TestEvaluateOnceThreshold(t *testing.T) {
	// 阈值规则经 VM 查询评估: cpu.usage{source="prometheus",instance="10.0.0.1"} > 90 命中。
	vm := mockVM(t, map[string]bool{
		`cpu.usage{source="prometheus",instance="10.0.0.1"} > 90`: true,
	})
	e := newPromqlRuleEvaluator(vm)
	rules := []*model.AlertRule{
		{
			Name:       "cpu-threshold",
			Type:       "threshold",
			MetricName: "cpu.usage",
			Operator:   ">",
			Threshold:  90,
			Labels:     map[string]string{"source": "prometheus", "instance": "10.0.0.1", "ci_id": "ci-1"},
			Severity:   model.AlertCritical,
			Enabled:    true,
		},
		{
			Name:       "not-hit",
			Type:       "threshold",
			MetricName: "mem.usage",
			Operator:   "<",
			Threshold:  10,
			Labels:     map[string]string{"source": "prometheus", "instance": "10.0.0.1"},
			Enabled:    true,
		},
	}
	alerts := e.evaluateOnce(context.Background(), rules)
	if len(alerts) != 1 {
		t.Fatalf("expected 1 alert (cpu-threshold), got %d: %+v", len(alerts), alerts)
	}
	a := alerts[0]
	if a.RuleName != "cpu-threshold" {
		t.Fatalf("unexpected rule: %s", a.RuleName)
	}
	if a.SourceCIID != "ci-1" {
		t.Fatalf("SourceCIID should come from labels ci_id, got %q", a.SourceCIID)
	}
	if a.Severity != model.AlertCritical {
		t.Fatalf("severity wrong: %v", a.Severity)
	}
}

func TestThresholdExpr(t *testing.T) {
	cases := []struct {
		r    *model.AlertRule
		want string
	}{
		{
			&model.AlertRule{MetricName: "cpu", Operator: ">", Threshold: 90, Labels: map[string]string{"source": "prom", "instance": "1.1.1.1"}},
			`cpu{source="prom",instance="1.1.1.1"} > 90`,
		},
		{&model.AlertRule{MetricName: "cpu", Operator: ">", Threshold: 90}, `cpu > 90`},
		{
			// ci_id 不参与 matcher(非 series 标签), 避免查询为空
			&model.AlertRule{MetricName: "cpu", Operator: ">=", Threshold: 1.5, Labels: map[string]string{"ci_id": "x"}},
			`cpu >= 1.5`,
		},
	}
	for _, c := range cases {
		if got := thresholdExpr(c.r); got != c.want {
			t.Fatalf("thresholdExpr=%q want %q", got, c.want)
		}
	}
}
