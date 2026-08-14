package monitor

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/opsorigin/core/internal/monitor/model"
)

// BenchmarkPromQLEvaluateOnce 基准: 100 条 promql 规则一次评估。
// 用于监控规则规模增长时的性能基线。
func BenchmarkPromQLEvaluateOnce(b *testing.B) {
	vm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"status":"success","data":{"resultType":"vector","result":[{"metric":{"agent_id":"a1"},"value":[1722500000,"0.9"]}]}}`)
	}))
	defer vm.Close()

	e := newPromqlRuleEvaluator(vm.URL)
	rules := make([]*model.AlertRule, 0, 100)
	for i := 0; i < 100; i++ {
		rules = append(rules, &model.AlertRule{
			Name: "r" + string(rune('a'+i%26)), Type: "promql",
			Expr: "avg(cpu.usage) > 0.8", Severity: model.AlertWarning, Enabled: true,
		})
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		alerts := e.evaluateOnce(context.Background(), rules)
		if len(alerts) == 0 {
			b.Fatal("expected alerts")
		}
	}
}
