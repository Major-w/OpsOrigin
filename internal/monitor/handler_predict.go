package monitor

import (
	"encoding/json"
	"net/http"

	"github.com/opsorigin/core/internal/monitor/predict"
)

// handleAlertRCAPaths 因果链根因分析: 返回传播路径(根因→受影响)而非扁平候选。
func (s *Service) handleAlertRCAPaths(w http.ResponseWriter, r *http.Request) {
	alerts := s.aggregator.GetActiveAlerts()
	paths := s.aggregator.AnalyzeRootCausePaths(r.Context(), alerts)
	writeJSON(w, http.StatusOK, paths)
}

// handlePredict 趋势预测(L2 预测性运维):
// body: {"series":[...], "threshold":90, "step_hours":1}
// resp: {slope, intercept, r2, trend, forecast, time_to_threshold_hours, hits_threshold}
func (s *Service) handlePredict(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Series    []float64 `json:"series"`
		Threshold float64   `json:"threshold"`
		StepHours float64   `json:"step_hours"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request: "+err.Error())
		return
	}
	if len(req.Series) < 2 {
		writeError(w, http.StatusBadRequest, "series must have at least 2 points")
		return
	}
	if req.StepHours <= 0 {
		req.StepHours = 1
	}
	writeJSON(w, http.StatusOK, predict.Analyze(req.Series, req.Threshold, req.StepHours))
}
