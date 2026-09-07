package monitor

import (
	"context"
	"database/sql"
	"github.com/opsorigin/core/pkg/auth"
	"log/slog"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/opsorigin/core/internal/monitor/alert"
	"github.com/opsorigin/core/internal/monitor/discovery"
	"github.com/opsorigin/core/internal/monitor/ebpf"
	"github.com/opsorigin/core/internal/monitor/metrics"
	"github.com/opsorigin/core/internal/monitor/model"
	"github.com/opsorigin/core/internal/monitor/plugin"
	"github.com/opsorigin/core/internal/monitor/synthetic"
	"github.com/opsorigin/core/pkg/config"
	"github.com/redis/go-redis/v9"

	"github.com/opsorigin/core/pkg/httputil"
)

type Service struct {
	cfg          *config.Config
	discovery    *discovery.Engine
	prober       *synthetic.Prober
	ebpf         *ebpf.Collector
	aggregator   *alert.Aggregator
	topology     *SimpleTopology
	plugins      *plugin.Registry
	wsHub        interface{ BroadcastToTenant(interface{}, string) }
	probeMu      sync.Mutex
	probeResults []*model.ProbeResult

	// alertNotifiers 告警触发时的附加回调(可多个, 由 cmd/core 注入如值班路由/AI 接管)。
	// 与 aggregator.OnNotify(WS 广播) 并存, 避免覆盖既有行为。
	alertNotifiers []func(*model.AlertEvent)
	alertMu        sync.Mutex

	// cmdbEnricher CMDB 上下文富化器(SetCMDBEnricher 注入, nil 安全):
	// 告警进入 aggregator 前按 SourceCIID 附加 owner/criticality 等上下文。
	cmdbEnricher func(ctx context.Context, ciID, tenantID string) map[string]string
}

func NewService(cfg *config.Config, db *sql.DB, rdb *redis.Client) *Service {
	topo := NewSimpleTopology()
	return &Service{
		cfg:          cfg,
		discovery:    discovery.NewEngine(rdb, db),
		prober:       synthetic.NewProber(db),
		ebpf:         ebpf.NewCollector(),
		aggregator:   alert.NewAggregator(topo),
		topology:     topo,
		plugins:      plugin.NewDefaultRegistry(),
		probeResults: make([]*model.ProbeResult, 0),
	}
}

// Plugins 暴露插件注册表，供装配期追加自定义插件声明。
func (s *Service) Plugins() *plugin.Registry { return s.plugins }

// AddAlertNotifier 注册告警触发回调(每次新告警触发时调用, 异步安全)。
// 供 cmd/core 装配智能值班路由等扩展能力; 与内置 WS 广播互不干扰。
func (s *Service) AddAlertNotifier(fn func(*model.AlertEvent)) {
	if fn == nil {
		return
	}
	s.alertMu.Lock()
	s.alertNotifiers = append(s.alertNotifiers, fn)
	s.alertMu.Unlock()
}

// SetCMDBEnricher 注入 CMDB 上下文富化器(由 cmd/core 装配, 与 cmdb 包零耦合):
// 按告警关联的 CI 查 CMDB, 返回要附加到告警 Labels 的键值
// (owner/business_criticality/environment/ci_name 等)。
// nil 安全: 未注入时告警保持原样。
func (s *Service) SetCMDBEnricher(fn func(ctx context.Context, ciID, tenantID string) map[string]string) {
	s.cmdbEnricher = fn
}

// ingestAlert 是全部告警的统一入口: 先做 CMDB 上下文富化, 再进 aggregator
// applyCMDBEnrichment 执行 CMDB 上下文富化(幂等, nil 安全)。
// 抽成独立方法便于单测; 富化只补缺失键, 规则显式声明优先。
func (s *Service) applyCMDBEnrichment(ctx context.Context, alert *model.AlertEvent) *model.AlertEvent {
	if s.cmdbEnricher == nil || alert == nil || alert.SourceCIID == "" {
		return alert
	}
	for k, v := range s.cmdbEnricher(ctx, alert.SourceCIID, alert.TenantID) {
		if v == "" {
			continue
		}
		if alert.Labels == nil {
			alert.Labels = map[string]string{}
		}
		// 已有同键(规则自带)不覆盖, 规则显式声明优先。
		if _, exists := alert.Labels[k]; !exists {
			alert.Labels[k] = v
		}
	}
	return alert
}

// ingestAlert 是全部告警的统一入口: 先做 CMDB 上下文富化, 再进 aggregator
// (去重/聚类/抑制/通知)。所有评估路径(promql/threshold/采集)都必须经此进入,
// 保证富化在落库与通知前完成, 告警从诞生起就携带责任人/关键性上下文。
func (s *Service) ingestAlert(ctx context.Context, alert *model.AlertEvent) (*model.AlertEvent, error) {
	s.applyCMDBEnrichment(ctx, alert)
	return s.aggregator.Ingest(ctx, alert)
}

func (s *Service) Topology() *SimpleTopology {
	return s.topology
}

// SnapshotTargets 返回当前所有监控目标（供全景可观测拓扑按 CIID join 节点状态）。
func (s *Service) SnapshotTargets() []*model.MonitorTarget {
	return s.discovery.GetTargets()
}

// ActiveAlerts 返回当前活跃告警（供全景可观测拓扑告警 rollup）。
func (s *Service) ActiveAlerts() []*model.AlertEvent {
	return s.aggregator.GetActiveAlerts()
}

func (s *Service) SetWebSocketHub(hub interface{ BroadcastToTenant(interface{}, string) }) {
	s.wsHub = hub
}

func (s *Service) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/monitor/targets", s.handleListTargets)
	mux.HandleFunc("GET /api/v1/monitor/targets/{id}", s.handleGetTarget)
	mux.HandleFunc("POST /api/v1/monitor/targets", s.handleAddTarget)
	mux.HandleFunc("DELETE /api/v1/monitor/targets/{id}", s.handleDeleteTarget)
	mux.HandleFunc("POST /api/v1/monitor/probe", s.handleRunProbe)
	mux.HandleFunc("GET /api/v1/monitor/alerts", s.handleListAlerts)
	mux.HandleFunc("GET /api/v1/monitor/alert-stats", s.handleAlertStats)
	mux.HandleFunc("GET /api/v1/monitor/alerts/rca", s.handleAlertRCA)
	mux.HandleFunc("GET /api/v1/monitor/alerts/rca/paths", s.handleAlertRCAPaths)
	mux.HandleFunc("POST /api/v1/monitor/predict", s.handlePredict)
	mux.HandleFunc("POST /api/v1/monitor/alerts/{id}/ack", s.handleAckAlert)
	mux.HandleFunc("POST /api/v1/monitor/alerts/{id}/resolve", s.handleResolveAlert)
	mux.HandleFunc("GET /api/v1/monitor/rules", s.handleListRules)
	mux.HandleFunc("POST /api/v1/monitor/rules", s.handleCreateRule)
	mux.HandleFunc("DELETE /api/v1/monitor/rules/{id}", s.handleDeleteRule)
	mux.HandleFunc("GET /api/v1/monitor/metrics", s.handleGetMetrics)
	mux.HandleFunc("GET /api/v1/monitor/plugins", s.handleListPlugins)
	mux.HandleFunc("POST /api/v1/monitor/topology/parent", s.handleSetParent)
}

func (s *Service) Start(ctx context.Context) error {
	s.prober.OnResult(func(target *model.MonitorTarget, result *model.ProbeResult) {
		s.probeMu.Lock()
		s.probeResults = append(s.probeResults, result)
		if len(s.probeResults) > 500 {
			s.probeResults = s.probeResults[len(s.probeResults)-300:]
		}
		metrics.MonitorProbeResults.Set(float64(len(s.probeResults)))
		s.probeMu.Unlock()

		if !result.Success {
			alert := &model.AlertEvent{
				TenantID:    target.TenantID,
				TargetID:    target.ID,
				SourceCIID:  target.CIID,
				RuleName:    "probe_failure",
				Severity:    model.AlertWarning,
				Title:       target.Name + " probe failed",
				Description: string(target.Probes[0]) + " check to " + target.Address + " failed: " + result.ErrorMessage,
			}
			if _, err := s.ingestAlert(ctx, alert); err != nil {
				slog.Error("ingest probe alert failed", "error", err)
			}
		}
	})

	s.discovery.Subscribe(func(ctx context.Context, event discovery.ChangeEvent) {
		if event.EventType == "ci.created" || event.EventType == "ci.updated" {
			targets := s.discovery.GetTargets()
			for _, t := range targets {
				if t.CIID == event.CIID {
					s.prober.AddTarget(t)
					s.topology.RecordTenant(ctx, event.CIID, event.TenantID)
				}
			}
		}
		if event.EventType == "ci.deleted" {
			for _, t := range s.discovery.GetTargets() {
				if t.CIID == event.CIID {
					s.prober.RemoveTarget(t.ID)
				}
			}
		}
	})

	s.ebpf.OnMetric(func(sample ebpf.MetricSample) {
		s.aggregator.EvaluateMetric(alert.MetricSample{
			Name:      sample.Name,
			Value:     sample.Value,
			Host:      "localhost",
			Unit:      sample.Unit,
			Timestamp: sample.Timestamp,
		})
	})

	s.aggregator.OnNotify(func(alert *model.AlertEvent) {
		if s.wsHub != nil {
			s.wsHub.BroadcastToTenant(map[string]any{
				"type":    "alert",
				"payload": alert,
				"time":    time.Now().UnixMilli(),
			}, alert.TenantID)
		}
		s.alertMu.Lock()
		fns := append([]func(*model.AlertEvent){}, s.alertNotifiers...)
		s.alertMu.Unlock()
		for _, fn := range fns {
			fn(alert)
		}
	})

	go func() {
		if err := s.discovery.Start(ctx); err != nil {
			slog.Error("discovery engine failed", "error", err)
		}
	}()
	go func() {
		if err := s.prober.Run(ctx); err != nil {
			slog.Error("synthetic prober failed", "error", err)
		}
	}()
	go func() {
		if err := s.aggregator.Run(ctx); err != nil {
			slog.Error("alert aggregator failed", "error", err)
		}
	}()

	// 阶段 4: PromQL 告警规则评估器(需配置 VictoriaMetrics 地址)。
	// no_data 检测: 规则写 absent(<metric>) 表达式, 指标断流时命中告警。
	if s.cfg.Core.VMAddr != "" {
		go s.promqlEvalLoop(ctx)
	}

	s.ebpf.Start()
	go func() {
		<-ctx.Done()
		s.ebpf.Stop()
	}()
	return nil
}

func (s *Service) handleListTargets(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	targets := s.discovery.GetTargets()
	filtered := make([]*model.MonitorTarget, 0)
	for _, t := range targets {
		if t.TenantID == tenantID {
			filtered = append(filtered, t)
		}
	}
	if filtered == nil {
		filtered = []*model.MonitorTarget{}
	}
	httputil.WriteJSON(w, http.StatusOK, filtered)
}

// pluginWithUsage 在插件声明之上附加「当前租户已接入多少个目标」，
// 让目录页一眼看出哪些插件是真在跑的，而不是一份静态说明书。
type pluginWithUsage struct {
	*plugin.Plugin
	InUse   int      `json:"in_use"`
	Targets []string `json:"targets,omitempty"`
}

func (s *Service) handleListPlugins(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())

	// 统计各插件的接入目标：绑定关系记录在 target.Labels["plugin"]，复用既有目标存储。
	usage := make(map[string][]string)
	for _, t := range s.discovery.GetTargets() {
		if t.TenantID != tenantID || t.Labels == nil {
			continue
		}
		if key := t.Labels["plugin"]; key != "" {
			usage[key] = append(usage[key], t.Name)
		}
	}

	list := s.plugins.List()
	out := make([]pluginWithUsage, 0, len(list))
	for _, p := range list {
		names := usage[p.Key]
		out = append(out, pluginWithUsage{Plugin: p, InUse: len(names), Targets: names})
	}
	httputil.WriteJSON(w, http.StatusOK, out)
}

func (s *Service) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	tenantID := auth.TenantFromContext(r.Context())
	target := s.discovery.GetTarget(id)
	if target == nil || target.TenantID != tenantID {
		httputil.WriteError(w, http.StatusNotFound, "target not found")
		return
	}
	httputil.WriteJSON(w, http.StatusOK, target)
}

func (s *Service) handleAddTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name    string                `json:"name"`
		Address string                `json:"address"`
		Port    int                   `json:"port,omitempty"`
		Probes  []model.ProbeProtocol `json:"probes,omitempty"`
		CIID    string                `json:"ci_id,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" || req.Address == "" {
		httputil.WriteError(w, http.StatusBadRequest, "name and address required")
		return
	}
	if len(req.Probes) == 0 {
		req.Probes = []model.ProbeProtocol{model.ProbeTCP}
	}
	now := time.Now()
	ciID := req.CIID
	if ciID == "" {
		ciID = "manual-" + req.Name
	}
	target := &model.MonitorTarget{
		ID:        "manual-" + req.Name,
		TenantID:  auth.TenantFromContext(r.Context()),
		CIID:      ciID,
		Name:      req.Name,
		Address:   req.Address,
		Port:      req.Port,
		Probes:    req.Probes,
		Interval:  30 * time.Second,
		Timeout:   5 * time.Second,
		Status:    model.TargetUnknown,
		CreatedAt: now,
	}
	s.prober.AddTarget(target)
	s.topology.RecordTenant(r.Context(), ciID, target.TenantID)
	httputil.WriteJSON(w, http.StatusCreated, target)
}

func (s *Service) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.prober.RemoveTarget(id)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Service) handleRunProbe(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Address  string              `json:"address"`
		Port     int                 `json:"port"`
		Protocol model.ProbeProtocol `json:"protocol"`
		Timeout  string              `json:"timeout,omitempty"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	timeout := 5 * time.Second
	if req.Timeout != "" {
		if d, err := time.ParseDuration(req.Timeout); err == nil {
			timeout = d
		}
	}
	task := &model.ProbeTask{
		Address:  req.Address,
		Port:     req.Port,
		Protocol: req.Protocol,
		Timeout:  timeout,
		TenantID: auth.TenantFromContext(r.Context()),
	}
	result := s.prober.Probe(r.Context(), task)
	httputil.WriteJSON(w, http.StatusOK, result)
}

func (s *Service) handleListAlerts(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	ciID := r.URL.Query().Get("ci_id") // 按配置项过滤(CMDB 详情联动)
	// status=history 返回已确认/已解决的处理历史; 其余返回活跃告警
	if r.URL.Query().Get("status") == "history" {
		history := s.aggregator.GetAlertHistory()
		filtered := make([]*model.AlertEvent, 0)
		for _, a := range history {
			if a.TenantID == tenantID && (ciID == "" || a.SourceCIID == ciID) {
				filtered = append(filtered, a)
			}
		}
		if filtered == nil {
			filtered = []*model.AlertEvent{}
		}
		httputil.WriteJSON(w, http.StatusOK, filtered)
		return
	}
	allAlerts := s.aggregator.GetActiveAlerts()
	filtered := make([]*model.AlertEvent, 0)
	for _, a := range allAlerts {
		if a.TenantID == tenantID && (ciID == "" || a.SourceCIID == ciID) {
			filtered = append(filtered, a)
		}
	}
	if filtered == nil {
		filtered = []*model.AlertEvent{}
	}
	httputil.WriteJSON(w, http.StatusOK, filtered)
}

func (s *Service) handleAlertStats(w http.ResponseWriter, r *http.Request) {
	httputil.WriteJSON(w, http.StatusOK, s.aggregator.GetStats())
}

// handleAlertRCA 返回活跃告警的启发式根因分析(P0 补短板: 对标 Dynatrace Davis / AISWare RCA)。
// 基于拓扑 + 严重度 + 触发时序评分, 输出根因候选列表。
func (s *Service) handleAlertRCA(w http.ResponseWriter, r *http.Request) {
	tenantID := auth.TenantFromContext(r.Context())
	alerts := s.aggregator.GetActiveAlerts()
	filtered := make([]*model.AlertEvent, 0, len(alerts))
	for _, a := range alerts {
		if a.TenantID == tenantID {
			filtered = append(filtered, a)
		}
	}
	topN := 5
	if v := r.URL.Query().Get("top"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 && n <= 20 {
			topN = n
		}
	}
	entries := s.aggregator.AnalyzeRootCause(r.Context(), filtered, topN)
	if entries == nil {
		entries = []alert.RCAEntry{}
	}
	httputil.WriteJSON(w, http.StatusOK, entries)
}

func (s *Service) handleAckAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		AcknowledgedBy string `json:"acknowledged_by"`
	}
	httputil.BindJSON(r, &req)
	if req.AcknowledgedBy == "" {
		req.AcknowledgedBy = "admin"
	}
	if err := s.aggregator.Acknowledge(id, req.AcknowledgedBy); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "acknowledged"})
}

func (s *Service) handleResolveAlert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.aggregator.Resolve(id); err != nil {
		httputil.WriteError(w, http.StatusNotFound, err.Error())
		return
	}
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (s *Service) handleListRules(w http.ResponseWriter, r *http.Request) {
	rules := s.aggregator.GetRules()
	if rules == nil {
		rules = []*model.AlertRule{}
	}
	httputil.WriteJSON(w, http.StatusOK, rules)
}

func (s *Service) handleCreateRule(w http.ResponseWriter, r *http.Request) {
	var rule model.AlertRule
	if err := httputil.BindJSON(r, &rule); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	rule.TenantID = auth.TenantFromContext(r.Context())
	rule.Enabled = true
	s.aggregator.AddRule(&rule)
	httputil.WriteJSON(w, http.StatusCreated, rule)
}

func (s *Service) handleDeleteRule(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.aggregator.DeleteRule(id)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Service) handleGetMetrics(w http.ResponseWriter, r *http.Request) {
	type metricPoint struct {
		Name      string  `json:"name"`
		Value     float64 `json:"value"`
		Timestamp string  `json:"timestamp"`
		Unit      string  `json:"unit,omitempty"`
		Success   *bool   `json:"success,omitempty"`
	}
	points := make([]metricPoint, 0)
	s.probeMu.Lock()
	for _, r := range s.probeResults {
		success := r.Success
		points = append(points, metricPoint{
			Name:      "probe",
			Value:     float64(r.Latency.Milliseconds()),
			Timestamp: r.CheckedAt.Format(time.RFC3339),
			Unit:      "ms",
			Success:   &success,
		})
	}
	s.probeMu.Unlock()
	httputil.WriteJSON(w, http.StatusOK, points)
}

func (s *Service) handleSetParent(w http.ResponseWriter, r *http.Request) {
	var req struct {
		CIID     string `json:"ci_id"`
		ParentID string `json:"parent_id"`
	}
	if err := httputil.BindJSON(r, &req); err != nil {
		httputil.WriteError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	tenantID := auth.TenantFromContext(r.Context())
	if !s.topology.VerifyTenant(r.Context(), req.CIID, tenantID) {
		httputil.WriteError(w, http.StatusNotFound, "ci not found in tenant")
		return
	}
	s.topology.SetParent(r.Context(), req.CIID, req.ParentID)
	httputil.WriteJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
