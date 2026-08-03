// Package predict 提供时间序列趋势预测(L2 预测性运维):
// 线性回归 + 外推预测 + 触达阈值时间(ETT)估计。
// 纯函数无状态, 输入任意数值序列, 输出趋势/预测/风险提示。
package predict

import "math"

// Result 一次趋势预测结果。
type Result struct {
	Slope     float64   `json:"slope"`     // 每点变化量(正=上升)
	Intercept float64   `json:"intercept"` // 截距
	R2        float64   `json:"r2"`        // 拟合优度 0-1(越高趋势越明显)
	Trend     string    `json:"trend"`     // up | down | flat
	Forecast  []float64 `json:"forecast"`  // 未来 horizon 个预测点
	// TimeToThresholdHours 按当前趋势外推触达阈值的小时数(stepHours=每点间隔小时)。
	// 趋势不朝向阈值或已越过阈值时 ok=false。
	TimeToThresholdHours float64 `json:"time_to_threshold_hours"`
	HitsThreshold        bool    `json:"hits_threshold"`
}

// LinearRegression 最小二乘线性回归 y = slope*x + intercept, 返回 r²。
// 输入至少 2 个点; 不足或方差为 0 时返回零值。
func LinearRegression(ys []float64) (slope, intercept, r2 float64) {
	n := len(ys)
	if n < 2 {
		return 0, 0, 0
	}
	var sumX, sumY, sumXY, sumX2, sumY2 float64
	for i, y := range ys {
		x := float64(i)
		sumX += x
		sumY += y
		sumXY += x * y
		sumX2 += x * x
		sumY2 += y * y
	}
	denom := float64(n)*sumX2 - sumX*sumX
	if math.Abs(denom) < 1e-12 {
		return 0, 0, 0
	}
	slope = (float64(n)*sumXY - sumX*sumY) / denom
	intercept = (sumY - slope*sumX) / float64(n)
	// r² = (SSR / SST)
	meanY := sumY / float64(n)
	var ssRes, ssTot float64
	for i, y := range ys {
		x := float64(i)
		fit := slope*x + intercept
		ssRes += (y - fit) * (y - fit)
		ssTot += (y - meanY) * (y - meanY)
	}
	if ssTot < 1e-12 {
		r2 = 0
	} else {
		r2 = 1 - ssRes/ssTot
	}
	return
}

// Forecast 基于回归模型外推未来 horizon 个点(从序列末尾继续)。
func Forecast(ys []float64, horizon int) []float64 {
	if horizon <= 0 {
		return nil
	}
	slope, intercept, _ := LinearRegression(ys)
	n := float64(len(ys))
	out := make([]float64, 0, horizon)
	for i := 1; i <= horizon; i++ {
		out = append(out, slope*(n+float64(i)-1)+intercept)
	}
	return out
}

// Analyze 完整趋势预测: 回归 + 趋势方向 + ETT(触达阈值时间)。
// stepHours = 每个数据点的时间间隔(小时), 用于把"点数"换算为小时。
// threshold 语义: 向上趋势时触达上限(如 CPU 95%), 向下趋势时触达下限(如可用率 90%)。
func Analyze(ys []float64, threshold float64, stepHours float64) Result {
	res := Result{}
	if len(ys) < 2 {
		res.Trend = "flat"
		return res
	}
	slope, intercept, r2 := LinearRegression(ys)
	res.Slope = slope
	res.Intercept = intercept
	res.R2 = r2
	switch {
	case slope > 0.001:
		res.Trend = "up"
	case slope < -0.001:
		res.Trend = "down"
	default:
		res.Trend = "flat"
	}

	// ETT: 解 slope*x + intercept = threshold
	if math.Abs(slope) > 1e-9 {
		pointsTo := (threshold - intercept) / slope
		last := float64(len(ys) - 1)
		remain := pointsTo - last // 距当前点还需要多少个未来点
		if remain > 0 && remain < 100000 {
			res.TimeToThresholdHours = remain * stepHours
			res.HitsThreshold = true
		}
	}
	horizon := 8
	if stepHours > 0 {
		horizon = int(24 / stepHours) // 预测 24 小时
		if horizon < 4 {
			horizon = 4
		}
		if horizon > 48 {
			horizon = 48
		}
	}
	res.Forecast = Forecast(ys, horizon)
	return res
}

// AnalyzeWithSeries 同 Analyze, 额外返回最后一个实际值(前端画图用)。
func AnalyzeWithSeries(ys []float64, threshold float64, stepHours float64) (Result, []float64) {
	return Analyze(ys, threshold, stepHours), ys
}
