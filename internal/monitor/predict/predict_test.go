package predict

import (
	"math"
	"testing"
)

// TestLinearRegressionPerfectFit 完美线性数据: slope=2, intercept=1, r2≈1。
func TestLinearRegressionPerfectFit(t *testing.T) {
	ys := []float64{1, 3, 5, 7, 9} // y = 2x + 1
	slope, intercept, r2 := LinearRegression(ys)
	if math.Abs(slope-2) > 1e-9 {
		t.Fatalf("slope = %v, want 2", slope)
	}
	if math.Abs(intercept-1) > 1e-9 {
		t.Fatalf("intercept = %v, want 1", intercept)
	}
	if math.Abs(r2-1) > 1e-9 {
		t.Fatalf("r2 = %v, want 1", r2)
	}
}

// TestAnalyzeUpwardETT 上升趋势: 预测继续上升 + 触达阈值时间合理。
func TestAnalyzeUpwardETT(t *testing.T) {
	ys := []float64{10, 20, 30, 40, 50} // y = 10x, 每点 +10
	res := Analyze(ys, 100, 1.0)        // 阈值 100, 每点 1 小时
	if res.Trend != "up" {
		t.Fatalf("trend = %s, want up", res.Trend)
	}
	// y=10x 在 x=10 达到 100, 当前最后一个点 x=4 (值 50), 还需 6 点 = 6 小时
	if math.Abs(res.TimeToThresholdHours-5) > 0.5 {
		t.Fatalf("ETT = %v, want ~5h", res.TimeToThresholdHours)
	}
	if !res.HitsThreshold {
		t.Fatal("expected hits threshold")
	}
	if len(res.Forecast) == 0 {
		t.Fatal("expected forecast points")
	}
	// 预测点应继续上升
	last := res.Forecast[len(res.Forecast)-1]
	if last <= ys[len(ys)-1] {
		t.Fatalf("forecast should grow, got %v", last)
	}
}

// TestAnalyzeDownNoHit 下降趋势不触达上限阈值。
func TestAnalyzeDownNoHit(t *testing.T) {
	ys := []float64{100, 90, 80, 70, 60}
	res := Analyze(ys, 95, 1.0)
	if res.Trend != "down" {
		t.Fatalf("trend = %s, want down", res.Trend)
	}
	if res.HitsThreshold {
		t.Fatal("downward series should not hit upper threshold")
	}
}

// TestAnalyzeAlreadyOver 已越过阈值不再给 ETT。
func TestAnalyzeAlreadyOver(t *testing.T) {
	ys := []float64{80, 90, 100, 110, 120}
	res := Analyze(ys, 95, 1.0)
	if res.HitsThreshold {
		t.Fatal("already over threshold should not report ETT")
	}
}

// TestAnalyzeFlat 平稳序列: trend=flat, r2 低。
func TestAnalyzeFlat(t *testing.T) {
	ys := []float64{50, 51, 50, 49, 50, 51, 50}
	res := Analyze(ys, 90, 1.0)
	if res.Trend != "flat" {
		t.Fatalf("trend = %s, want flat", res.Trend)
	}
}
